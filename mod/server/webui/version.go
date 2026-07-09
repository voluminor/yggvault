package webui

import (
	"context"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/server/artifactio"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/view"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

var (
	cFormatTarGz     = string(archive.FormatTarGz)
	cListenerGlobal  = stcode.ListenerGlobal.String()
	cUniversalMatzer = stcode.MaterializerUniversal.String()
	cGoMatzer        = stcode.MaterializerGo.String()
)

// artifactHashes collects all stored body digests for an artifact.
func artifactHashes(artifactObj core.ArtifactObj) []view.ArtifactHashObj {
	hashArr := []view.ArtifactHashObj{{Algo: "blake3-24", Sum: artifactObj.BodyHash[:]}}
	if len(artifactObj.BodySha256) > 0 {
		hashArr = append(hashArr, view.ArtifactHashObj{Algo: "sha256", Sum: artifactObj.BodySha256})
	}
	if len(artifactObj.BodySha1) > 0 {
		hashArr = append(hashArr, view.ArtifactHashObj{Algo: "sha1", Sum: artifactObj.BodySha1})
	}
	return hashArr
}

// // // // // // // // // //

// Version builds the version page: integrity, source, downloads, snippets, notes and history navigation.
// found=false means 404; listenerID keeps host-sensitive artifacts in sync.
// altOv/altListenerID bind the opposite entry. nil altOv disables alternate snippets to avoid mixing
// host-sensitive artifacts and current-entry module paths with a different host.
func Version(ctx context.Context, st StateReaderInterface, store DetailReaderInterface, ov OverlayInterface, lnk link.Obj, ctxObj view.ContextObj, key string, version string, listenerID string, altOv OverlayInterface, altListenerID string) ([]byte, bool, error) {
	rnd, err := renderer()
	if err != nil {
		return nil, false, err
	}

	versionObj, ok, err := store.GetVersion(ctx, key, version)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}

	viewModel := view.VersionObj{
		Context:              ctxObj,
		Key:                  key,
		Version:              version,
		SourceSizeBytes:      versionObj.SourceSizeBytes,
		UpstreamDeleted:      versionObj.UpstreamDeleted,
		IngestedAt:           versionObj.IngestTS,
		VerifiedAt:           versionObj.VerifiedTS,
		ReleaseNotesMarkdown: versionObj.ReleaseNotes,
	}
	if keyStateObj, known := st.KeyState(key); known {
		viewModel.Source = originOf(keyStateObj)
	}

	detectionObj, detectionOK, err := store.GetDetection(ctx, key, version)
	if err != nil {
		return nil, false, err
	}
	if detectionOK {
		if detectionObj.IsGo {
			viewModel.Overlays = append(viewModel.Overlays, "go")
		}
		if detectionObj.IsComposer {
			viewModel.Overlays = append(viewModel.Overlays, "composer")
		}
		// Show detection evidence as user-facing text, not raw evidence JSON.
		goCand, composerCand := overlay.CandidateFromDetection(detectionObj)
		if goCand != nil && goCand.GoModulePath != "" {
			viewModel.Detected = append(viewModel.Detected, "go module "+goCand.GoModulePath)
		}
		// A blocked Go zip is still Go-detected, but it has no @v routes.
		if detectionObj.GoZipBlocked && detectionObj.GoZipBlockReason != "" {
			viewModel.Detected = append(viewModel.Detected, "go module zip unavailable: "+detectionObj.GoZipBlockReason)
		}
		if composerCand != nil && composerCand.ComposerName != "" {
			viewModel.Detected = append(viewModel.Detected, "composer "+composerCand.ComposerName)
		}
		// Raw versions skip ecosystem parsing and expose universal archives only; the name predicate is shared.
		if util.IsRawVersionName(version) {
			viewModel.Detected = append(viewModel.Detected, "raw version: universal archives only")
		}
	}

	artifactArr, err := store.ListArtifacts(ctx, key, version)
	if err != nil {
		return nil, false, err
	}
	for i := range artifactArr {
		artifactObj := artifactArr[i]
		// Host-sensitive archives exist per entry; show current-entry variants plus host-independent artifacts.
		if artifactObj.ListenerID != cListenerGlobal && artifactObj.ListenerID != listenerID {
			continue
		}
		nameText, suffix := artifactio.NameSuffix(artifactObj)
		viewModel.Downloads = append(viewModel.Downloads, view.ArtifactEntryObj{
			Name:      nameText,
			Kind:      artifactObj.ArtifactKind,
			SizeBytes: artifactObj.SizeBytes,
			Hashes:    artifactHashes(artifactObj),
			URL:       lnk.Key(artifactObj.Key, suffix),
			GoModule:  artifactObj.MaterializerID == cGoMatzer,
		})
		if artifactObj.DegradedReason != "" {
			viewModel.Degraded = append(viewModel.Degraded, artifactObj.DegradedReason)
		}
	}

	scheme, host := schemeHost(lnk)
	viewModel.Install = versionSnippets(ctx, store, ov, lnk, key, version, scheme, host, listenerID, detectionObj, detectionOK)
	if altObj := ctxObj.Alternate; altObj.CopyHost != "" && altOv != nil {
		altArr := versionSnippets(ctx, store, altOv, lnk, key, version, altObj.Scheme, altObj.CopyHost, altListenerID, detectionObj, detectionOK)
		viewModel.AltInstall = dropDuplicateSnippets(altArr, viewModel.Install)
	}
	fillHistoryNav(ctx, store, versionObj, &viewModel)

	bodyArr, err := rnd.Version(viewModel)
	if err != nil {
		return nil, false, err
	}
	return bodyArr, true, nil
}

func versionSnippets(ctx context.Context, store DetailReaderInterface, ov OverlayInterface, lnk link.Obj, key string, version string, scheme string, host string, listenerID string, detectionObj core.DetectionObj, detectionOK bool) []view.CodeSnippetObj {
	var snippetArr []view.CodeSnippetObj

	var goCand, composerCand *overlay.CandidateObj
	if detectionOK {
		goCand, composerCand = overlay.CandidateFromDetection(detectionObj)
	}

	tgzURL := absURL(scheme, host, lnk.Key(key, "/"+version+".tar.gz"))
	if sha256Arr := universalSha256(ctx, store, key, version, cFormatTarGz, listenerID); len(sha256Arr) > 0 {
		stripPrefix := key + "-" + version
		if detectionOK {
			stripPrefix = ov.UniversalTopDir(key, version, detectionObj, goCand)
		}
		snippetArr = append(snippetArr, view.CodeSnippetObj{Label: "bazel", Body: overlay.BazelSnippet(key, tgzURL, sha256Arr, stripPrefix)})
	}
	snippetArr = append(snippetArr, view.CodeSnippetObj{Label: "zig", Body: overlay.ZigSnippet(tgzURL)})

	if detectionOK && ov.GoPublishable(key, version, detectionObj, goCand) {
		snippetArr = append(snippetArr, view.CodeSnippetObj{Label: "go", Body: overlay.GoInstallSnippet(ov.TargetModulePath(key, version), version, scheme, host)})
	}
	if composerCand != nil && host != "" {
		snippetArr = append(snippetArr, view.CodeSnippetObj{Label: "composer", Body: overlay.ComposerRequireSnippet(composerCand.ComposerName, version, scheme, host)})
	}
	return snippetArr
}

func universalSha256(ctx context.Context, store DetailReaderInterface, key string, version string, kind string, listenerID string) []byte {
	listenerArr := make([]string, 0, 2)
	if listenerID != "" && listenerID != cListenerGlobal {
		listenerArr = append(listenerArr, listenerID)
	}
	listenerArr = append(listenerArr, cListenerGlobal)
	for _, lid := range listenerArr {
		keyObj := core.ArtifactKeyObj{
			MaterializerID: cUniversalMatzer,
			ArtifactKind:   kind,
			ListenerID:     lid,
			Key:            key,
			Version:        version,
		}
		artifactObj, found, err := store.GetArtifact(ctx, keyObj)
		if err == nil && found && len(artifactObj.BodySha256) > 0 {
			return artifactObj.BodySha256
		}
	}
	return nil
}

// fillHistoryNav finds immediate newer/older neighbors by newest-first keyset walk.
// It stops after the match and two neighbors; history navigation errors must not fail the whole page.
// fillHistoryNav sets the immediate newer/older neighbors of versionObj with two bounded keyset lookups
// (LIMIT 1 each) around its (upstream_seq, version) cursor, instead of scanning the whole history from the
// head. This keeps the version page O(1) in history length and avoids the O(N^2) enumeration amplification.
func fillHistoryNav(ctx context.Context, store DetailReaderInterface, versionObj core.VersionObj, viewModel *view.VersionObj) {
	if olderArr, err := store.ListVersionsKeyset(ctx, versionObj.Key, false, versionObj.UpstreamSeq, versionObj.Version, 1); err == nil && len(olderArr) > 0 {
		viewModel.History.OlderVersion = olderArr[0].Version
	}
	if newerArr, err := store.ListVersionsKeysetBefore(ctx, versionObj.Key, false, versionObj.UpstreamSeq, versionObj.Version, 1); err == nil && len(newerArr) > 0 {
		viewModel.History.NewerVersion = newerArr[0].Version
	}
}
