package maintenance

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

const (
	cRebuildKeyPage = 256
)

// // // // // // // // // //

// RebuildArtifacts rebuilds and verifies all materialized artifacts.
func RebuildArtifacts(ctx context.Context, storeObj *storage.Obj, overlayObj *overlay.Obj, listenerArr []overlay.ListenerCtxObj) (RebuildResultObj, error) {
	var resultObj RebuildResultObj
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

func driftItem(artifactObj core.ArtifactObj, storedHash string, rebuiltHash string) ArtifactDriftObj {
	return ArtifactDriftObj{
		Key:            artifactObj.Key,
		Version:        artifactObj.Version,
		MaterializerID: artifactObj.MaterializerID,
		ArtifactKind:   artifactObj.ArtifactKind,
		ListenerID:     artifactObj.ListenerID,
		FormatVersion:  artifactObj.FormatVersion,
		StoredHash:     storedHash,
		RebuiltHash:    rebuiltHash,
	}
}

func rebuildVersionArtifacts(ctx context.Context, storeObj *storage.Obj, overlayObj *overlay.Obj, listenerArr []overlay.ListenerCtxObj, key string, version string, resultObj *RebuildResultObj) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	storedArr, err := storeObj.ListArtifacts(ctx, key, version)
	if err != nil {
		return err
	}
	planArr, ok, err := artifactPlanForVersion(ctx, storeObj, overlayObj, listenerArr, key, version)
	if err != nil || !ok {
		return err
	}
	storedMap := make(map[string]core.ArtifactObj, len(storedArr))
	for i := range storedArr {
		storedMap[storedIdentityKey(storedArr[i])] = storedArr[i]
	}

	for i := range planArr {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		planObj := planArr[i]
		resultObj.Scanned++
		digestObj, digestErr := storeObj.ArtifactDigest(ctx, planObj.Builder)
		if digestErr != nil {
			return digestErr
		}
		newArtifactObj := artifactFromPlan(planObj, key, version, digestObj)
		storedObj, exists := storedMap[planIdentityKey(planObj)]
		if !exists {
			if err = storeObj.RegisterArtifact(ctx, newArtifactObj); err != nil {
				return err
			}
			resultObj.Created++
			resultObj.Items = append(resultObj.Items, driftItem(newArtifactObj, "", digestObj.BodyHash.Hex()))
			continue
		}
		fresh := storedObj.BodyHash == digestObj.BodyHash &&
			storedObj.FormatVersion == planObj.FormatVersion &&
			storedObj.DegradedReason == ""
		if fresh {
			continue
		}
		resultObj.Drift++
		if err = storeObj.UpdateArtifactDigest(ctx, newArtifactObj); err != nil {
			return err
		}
		resultObj.Updated++
		resultObj.Items = append(resultObj.Items, driftItem(newArtifactObj, storedObj.BodyHash.Hex(), digestObj.BodyHash.Hex()))
	}

	for i := range storedArr {
		storedObj := storedArr[i]
		legacyUniversal := storedObj.MaterializerID == stcode.MaterializerUniversal.String() && storedObj.ListenerID != stcode.ListenerGlobal.String()
		legacyGlobalGo := storedObj.MaterializerID == stcode.MaterializerGo.String() && storedObj.ListenerID == stcode.ListenerGlobal.String()
		if !legacyUniversal && !legacyGlobalGo {
			continue
		}
		if err = storeObj.DeleteArtifact(ctx, core.ArtifactKeyObj{
			MaterializerID: storedObj.MaterializerID,
			ArtifactKind:   storedObj.ArtifactKind,
			ListenerID:     storedObj.ListenerID,
			Key:            key,
			Version:        version,
		}); err != nil {
			return err
		}
		resultObj.Pruned++
		resultObj.Items = append(resultObj.Items, driftItem(storedObj, storedObj.BodyHash.Hex(), ""))
	}
	return nil
}
