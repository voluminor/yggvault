package main

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	// cRebuildKeyPage — key page size during a full rebuild (keyset pagination, anti-fan-out).
	cRebuildKeyPage = 256
)

// //

type artifactDriftObj struct {
	Key            string `json:"key"`
	Version        string `json:"version"`
	MaterializerID string `json:"materializer_id"`
	ArtifactKind   string `json:"artifact_kind"`
	ListenerID     string `json:"listener_id"`
	FormatVersion  uint32 `json:"format_version"`
	StoredHash     string `json:"stored_hash"`
	RebuiltHash    string `json:"rebuilt_hash"`
}

type rebuildResultObj struct {
	Scanned uint64
	Drift   uint64
	Updated uint64
	Items   []artifactDriftObj
}

// // // // // // // // // //

func listenerContextsFromConfig(configObj *stconf.ConfigObj, yggHost string) []overlay.ListenerCtxObj {
	return overlay.ListenerContexts(configObj.Web.Server.Domain, configObj.Web.Routing.Prefix, yggHost)
}

func artifactIdentityKey(materializerID string, artifactKind string, listenerID string) string {
	return materializerID + "\x00" + artifactKind + "\x00" + listenerID
}

func planIdentityKey(planObj overlay.ArtifactPlanObj) string {
	return artifactIdentityKey(planObj.MaterializerID.String(), planObj.ArtifactKind.String(), planObj.ListenerID.String())
}

func storedIdentityKey(artifactObj core.ArtifactObj) string {
	return artifactIdentityKey(artifactObj.MaterializerID, artifactObj.ArtifactKind, artifactObj.ListenerID)
}

func artifactFromPlan(planObj overlay.ArtifactPlanObj, key string, version string, digestObj core.ArtifactDigestObj) core.ArtifactObj {
	return core.ArtifactObj{
		MaterializerID: planObj.MaterializerID.String(),
		ArtifactKind:   planObj.ArtifactKind.String(),
		ListenerID:     planObj.ListenerID.String(),
		Key:            key,
		Version:        version,
		FormatVersion:  planObj.FormatVersion,
		BodyHash:       digestObj.BodyHash,
		BodySha256:     digestObj.BodySha256,
		BodySha1:       digestObj.BodySha1,
		SizeBytes:      digestObj.SizeBytes,
		ETag:           digestObj.ETag,
	}
}

func artifactPlanForVersion(ctx context.Context, storeObj *storage.Obj, overlayObj *overlay.Obj, listenerArr []overlay.ListenerCtxObj, key string, version string) ([]overlay.ArtifactPlanObj, bool, error) {
	versionObj, ok, err := storeObj.GetVersion(ctx, key, version)
	if err != nil || !ok {
		return nil, false, err
	}
	detectionObj, _, err := storeObj.GetDetection(ctx, key, version)
	if err != nil {
		return nil, false, err
	}
	rewriteArr, err := storeObj.RewriteSet(ctx, key, version)
	if err != nil {
		return nil, false, err
	}
	goCandidate, _ := overlay.CandidateFromDetection(detectionObj)
	planArr := overlayObj.ArtifactPlan(storeObj, key, version, versionObj.TreeHash, detectionObj, goCandidate, rewriteArr, listenerArr)
	return planArr, true, nil
}

// // // // // // // // // //

func selfTestFormats(ctx context.Context, storeObj *storage.Obj, overlayObj *overlay.Obj, listenerArr []overlay.ListenerCtxObj) ([]artifactDriftObj, error) {
	var driftArr []artifactDriftObj
	for _, descriptorObj := range overlayObj.Descriptors() {
		materializerID := descriptorObj.MaterializerID.String()
		for _, kindObj := range descriptorObj.Kinds {
			artifactObj, ok, err := storeObj.SmallestArtifactByDescriptor(ctx, materializerID, kindObj.String(), descriptorObj.FormatVersion)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			digestObj, found, err := rebuildArtifactDigest(ctx, storeObj, overlayObj, listenerArr, artifactObj)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			if digestObj.BodyHash != artifactObj.BodyHash {
				driftArr = append(driftArr, artifactDriftObj{
					MaterializerID: materializerID,
					FormatVersion:  descriptorObj.FormatVersion,
					ArtifactKind:   artifactObj.ArtifactKind,
					ListenerID:     artifactObj.ListenerID,
					Key:            artifactObj.Key,
					Version:        artifactObj.Version,
					StoredHash:     artifactObj.BodyHash.Hex(),
					RebuiltHash:    digestObj.BodyHash.Hex(),
				})
			}
		}
	}
	return driftArr, nil
}

func rebuildArtifactDigest(ctx context.Context, storeObj *storage.Obj, overlayObj *overlay.Obj, listenerArr []overlay.ListenerCtxObj, artifactObj core.ArtifactObj) (core.ArtifactDigestObj, bool, error) {
	planArr, ok, err := artifactPlanForVersion(ctx, storeObj, overlayObj, listenerArr, artifactObj.Key, artifactObj.Version)
	if err != nil || !ok {
		return core.ArtifactDigestObj{}, false, err
	}
	wantKey := storedIdentityKey(artifactObj)
	for i := range planArr {
		if planIdentityKey(planArr[i]) != wantKey {
			continue
		}
		digestObj, digestErr := storeObj.ArtifactDigest(ctx, planArr[i].Builder)
		if digestErr != nil {
			return core.ArtifactDigestObj{}, false, digestErr
		}
		return digestObj, true, nil
	}
	return core.ArtifactDigestObj{}, false, nil
}

// // // // // // // // // //

func rebuildAllArtifacts(ctx context.Context, storeObj *storage.Obj, overlayObj *overlay.Obj, listenerArr []overlay.ListenerCtxObj) (rebuildResultObj, error) {
	var resultObj rebuildResultObj
	afterKey := ""
	for {
		if ctx.Err() != nil {
			return resultObj, ctx.Err()
		}
		keyArr, err := storeObj.DistinctKeys(ctx, afterKey, cRebuildKeyPage)
		if err != nil {
			return resultObj, err
		}
		if len(keyArr) == 0 {
			break
		}
		for _, key := range keyArr {
			afterKey = key
			versionArr, listErr := storeObj.ListVersions(ctx, key, true)
			if listErr != nil {
				return resultObj, listErr
			}
			for i := range versionArr {
				if err = rebuildVersionArtifacts(ctx, storeObj, overlayObj, listenerArr, key, versionArr[i].Version, &resultObj); err != nil {
					return resultObj, err
				}
			}
		}
		if len(keyArr) < cRebuildKeyPage {
			break
		}
	}
	return resultObj, nil
}

func rebuildVersionArtifacts(ctx context.Context, storeObj *storage.Obj, overlayObj *overlay.Obj, listenerArr []overlay.ListenerCtxObj, key string, version string, resultObj *rebuildResultObj) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	storedArr, err := storeObj.ListArtifacts(ctx, key, version)
	if err != nil {
		return err
	}
	if len(storedArr) == 0 {
		return nil
	}
	planArr, ok, err := artifactPlanForVersion(ctx, storeObj, overlayObj, listenerArr, key, version)
	if err != nil || !ok {
		return err
	}
	planMap := make(map[string]overlay.ArtifactPlanObj, len(planArr))
	for i := range planArr {
		planMap[planIdentityKey(planArr[i])] = planArr[i]
	}

	for i := range storedArr {
		storedObj := storedArr[i]
		planObj, planExists := planMap[storedIdentityKey(storedObj)]
		if !planExists {
			continue
		}
		resultObj.Scanned++
		digestObj, digestErr := storeObj.ArtifactDigest(ctx, planObj.Builder)
		if digestErr != nil {
			return digestErr
		}
		fresh := storedObj.BodyHash == digestObj.BodyHash &&
			storedObj.FormatVersion == planObj.FormatVersion &&
			storedObj.DegradedReason == ""
		if fresh {
			continue
		}
		resultObj.Drift++
		newArtifactObj := artifactFromPlan(planObj, key, version, digestObj)
		if err = storeObj.UpdateArtifactDigest(ctx, newArtifactObj); err != nil {
			return err
		}
		resultObj.Updated++
		resultObj.Items = append(resultObj.Items, artifactDriftObj{
			Key:            key,
			Version:        version,
			MaterializerID: newArtifactObj.MaterializerID,
			ArtifactKind:   newArtifactObj.ArtifactKind,
			ListenerID:     newArtifactObj.ListenerID,
			FormatVersion:  newArtifactObj.FormatVersion,
			StoredHash:     storedObj.BodyHash.Hex(),
			RebuiltHash:    digestObj.BodyHash.Hex(),
		})
	}
	return nil
}
