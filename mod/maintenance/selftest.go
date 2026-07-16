package maintenance

import (
	"context"
	"fmt"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// ListenersFromConfig builds listener contexts for host-sensitive materializers.
func ListenersFromConfig(configObj *stconf.ConfigObj, yggHost string) []overlay.ListenerCtxObj {
	return overlay.ListenerContexts(configObj.Web.Server.Domain, configObj.Web.Routing.Prefix, yggHost)
}

const cHostIdentityGlobalKey = "host_identity"

func hostIdentityFingerprint(configObj *stconf.ConfigObj, yggHost string) string {
	rawText := configObj.Web.Server.Domain + "\x00" + configObj.Web.Routing.Prefix + "\x00" + yggHost
	return core.HashBytes([]byte(rawText)).Hex()
}

const ArtifactLayoutGlobalKey = "artifact_layout"

const cArtifactLayoutRevision = 2

// ArtifactLayoutFingerprint pins the revision of formats affecting cache artifacts.
func ArtifactLayoutFingerprint() string {
	return fmt.Sprintf("r%d:uz%d:ut%d:gz%d", cArtifactLayoutRevision,
		overlay.UniversalZipFormatVersion, overlay.UniversalTarGzFormatVersion, overlay.GoZipFormatVersion)
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

// SelfTestFormats verifies small stored artifacts against current materializer descriptors.
func SelfTestFormats(ctx context.Context, storeObj *storage.Obj, overlayObj *overlay.Obj, listenerArr []overlay.ListenerCtxObj) ([]ArtifactDriftObj, error) {
	var driftArr []ArtifactDriftObj
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
				driftArr = append(driftArr, ArtifactDriftObj{
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
