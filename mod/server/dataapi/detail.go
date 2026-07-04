package dataapi

import (
	"context"
	"encoding/hex"

	"github.com/voluminor/yggvault/mod/server/artifactio"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

// BuildReleaseDetail builds version release_detail from metadata, detection, artifacts, and source.
// found=false means the version is absent; storage errors pass through unchanged.
func BuildReleaseDetail(ctx context.Context, store DetailReaderInterface, st StateReaderInterface, key string, version string, linkObj link.Obj) (api.ReleaseDetailObj, bool, error) {
	versionObj, ok, err := store.GetVersion(ctx, key, version)
	if err != nil {
		return api.ReleaseDetailObj{}, false, err
	}
	if !ok {
		return api.ReleaseDetailObj{}, false, nil
	}

	detailObj := api.ReleaseDetailObj{
		Key:             key,
		Version:         version,
		Time:            versionObj.IngestTS.UTC(),
		Hash:            api.NewOptString(versionObj.TreeHash.Hex()),
		Size:            api.NewOptInt64(int64(versionObj.SourceSizeBytes)),
		UpstreamPresent: api.NewOptBool(!versionObj.UpstreamDeleted),
		Overlays:        []string{},
		Artifacts:       []api.ArtifactEntryObj{},
		Degraded:        []string{},
	}
	if versionObj.ReleaseNotes != "" {
		detailObj.NotesMarkdown = api.NewOptString(versionObj.ReleaseNotes)
	}
	if keyStateObj, known := st.KeyState(key); known {
		detailObj.Source = sourceStatusOf(keyStateObj)
	}
	if detectionObj, dok, derr := store.GetDetection(ctx, key, version); derr == nil && dok {
		if detectionObj.IsGo {
			detailObj.Overlays = append(detailObj.Overlays, "go")
		}
		if detectionObj.IsComposer {
			detailObj.Overlays = append(detailObj.Overlays, "composer")
		}
	}

	artifactArr, err := store.ListArtifacts(ctx, key, version)
	if err != nil {
		return api.ReleaseDetailObj{}, false, err
	}
	for i := range artifactArr {
		artifactObj := artifactArr[i]
		nameText, suffix := artifactio.NameSuffix(artifactObj)
		entryObj := api.ArtifactEntryObj{
			Hash: artifactObj.BodyHash.Hex(),
			Kind: artifactio.KindLabel(artifactObj),
			Name: nameText,
			Size: int64(artifactObj.SizeBytes),
			URL:  linkObj.Key(artifactObj.Key, suffix),
		}
		if len(artifactObj.BodySha256) > 0 {
			entryObj.SHA256 = api.NewOptString(hex.EncodeToString(artifactObj.BodySha256))
		}
		if len(artifactObj.BodySha1) > 0 {
			entryObj.Shasum = api.NewOptString(hex.EncodeToString(artifactObj.BodySha1))
		}
		if artifactObj.DegradedReason != "" {
			detailObj.Degraded = append(detailObj.Degraded, artifactObj.DegradedReason)
		}
		detailObj.Artifacts = append(detailObj.Artifacts, entryObj)
	}
	return detailObj, true, nil
}
