package storage

import (
	"context"
	"errors"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/sqliteindex"
)

// // // // // // // // // //

// //

func (obj *Obj) getArtifact(ctx context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error) {
	keyObj, err := validateArtifactKey(keyObj)
	if err != nil {
		return core.ArtifactObj{}, false, err
	}
	return obj.indexObj.GetArtifact(ctx, keyObj)
}

// //

// GetArtifact returns artifact metadata by key; the second result reports whether it exists.
func (obj *Obj) GetArtifact(ctx context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.ArtifactObj{}, false, err
	}
	defer releaseFunc()

	return obj.getArtifact(ctx, keyObj)
}

// ListArtifacts returns all artifacts for a version for release_detail.
func (obj *Obj) ListArtifacts(ctx context.Context, key string, version string) ([]core.ArtifactObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	return obj.indexObj.ListArtifacts(ctx, key, version)
}

// RegisterArtifact registers one version artifact under writeMu in one transaction.
// Digests are computed beforehand by ArtifactDigest, then metadata and a history event are written.
func (obj *Obj) RegisterArtifact(ctx context.Context, artifactObj core.ArtifactObj) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	artifactArr, err := obj.prepareArtifacts(ctx, artifactObj.Key, artifactObj.Version, []core.ArtifactObj{artifactObj})
	if err != nil {
		return err
	}
	eventType, err := validateEventType("artifact")
	if err != nil {
		return err
	}
	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		if err = txObj.InsertArtifacts(ctx, artifactObj.Key, artifactObj.Version, artifactArr); err != nil {
			return err
		}
		return txObj.AddHistory(ctx, artifactObj.Key, artifactObj.Version, eventType, core.HashObj{}, artifactObj.BodyHash, "")
	})
}

// SmallestArtifactByDescriptor returns the smallest artifact for a descriptor triple for startup self-test.
func (obj *Obj) SmallestArtifactByDescriptor(ctx context.Context, materializerID string, artifactKind string, formatVersion uint32) (core.ArtifactObj, bool, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.ArtifactObj{}, false, err
	}
	defer releaseFunc()

	return obj.indexObj.SmallestArtifactByDescriptor(ctx, materializerID, artifactKind, formatVersion)
}

// UpdateArtifactDigest rewrites artifact digest metadata under writeMu for --rebuild-cache drift repair.
// It does not write history because this is metadata repair, not a new publication.
func (obj *Obj) UpdateArtifactDigest(ctx context.Context, artifactObj core.ArtifactObj) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	return obj.indexObj.UpdateArtifactDigest(ctx, artifactObj)
}

// EnsureArtifactFile returns a valid materialized hot file or builds it.
// Singleflight shares builds by identity so concurrent readers do not rebuild the same artifact.
// Final hash and size are checked against metadata; degraded or stale artifacts are rejected.
func (obj *Obj) EnsureArtifactFile(ctx context.Context, keyObj core.ArtifactKeyObj, builderObj ArtifactBuilderInterface) (*HotFileObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	keyObj, err = validateArtifactKey(keyObj)
	if err != nil {
		return nil, err
	}
	artifactObj, ok, err := obj.getArtifact(ctx, keyObj)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("artifact metadata not found")
	}
	if artifactObj.DegradedReason != "" {
		return nil, errors.New("artifact is degraded")
	}
	if fileObj, exists, openErr := obj.openValidHotFile(ctx, artifactObj); openErr != nil {
		return nil, openErr
	} else if exists {
		return fileObj, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	flightKey := artifactIdentityText(artifactObj)
	flightObj, ownerFlag := obj.joinArtifactFlight(flightKey)
	if ownerFlag {
		go obj.runArtifactFlight(flightKey, flightObj, keyObj, builderObj)
	}

	select {
	case <-ctx.Done():
		obj.leaveArtifactFlight(flightObj)
		return nil, ctx.Err()
	case <-flightObj.doneChan:
	}

	if flightObj.err != nil {
		obj.leaveArtifactFlight(flightObj)
		return nil, flightObj.err
	}
	fileObj, err := artifactResultFile(flightObj.result)
	obj.leaveArtifactFlight(flightObj)
	if err != nil {
		return nil, err
	}
	if fileObj.BodyHash != artifactObj.BodyHash {
		_ = fileObj.Close()
		return nil, newArtifactBuildErr(keyObj, nil, cArtifactCheckStaleHash, artifactObj.BodyHash.Hex(), fileObj.BodyHash.Hex(), 0, 0)
	}
	if fileObj.SizeBytes != artifactObj.SizeBytes {
		_ = fileObj.Close()
		return nil, newArtifactBuildErr(keyObj, nil, cArtifactCheckStaleSize, "", "", artifactObj.SizeBytes, fileObj.SizeBytes)
	}
	select {
	case <-ctx.Done():
		_ = fileObj.Close()
		return nil, ctx.Err()
	default:
	}
	return fileObj, nil
}

// DeleteArtifact removes one materialized artifact row by identity and its hot file under writeMu.
// A missing row is a no-op. Used by artifact reconciliation to prune rows the overlay plan no longer produces.
func (obj *Obj) DeleteArtifact(ctx context.Context, keyObj core.ArtifactKeyObj) error {
	keyObj, err := validateArtifactKey(keyObj)
	if err != nil {
		return err
	}
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	var filePath string
	err = obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		pathText, delErr := txObj.DeleteArtifactRow(ctx, keyObj)
		if delErr != nil {
			return delErr
		}
		filePath = pathText
		return nil
	})
	if err != nil {
		return err
	}
	if filePath != "" {
		_ = obj.removeHotPathAccountedLocked(filePath)
	}
	return nil
}
