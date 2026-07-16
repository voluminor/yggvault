package webui

import (
	"context"

	"golang.org/x/mod/semver"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/view"
)

// // // // // // // // // //

// Key builds the key page: newest-first version window, keyset pagination, badges and install snippets.
// found=false means the caller will return 404. The zero cursor selects the newest page.
func Key(ctx context.Context, st StateReaderInterface, store VersionReaderInterface, lnk link.Obj, ctxObj view.ContextObj, key string, cursor PageCursorObj, pageSize int) ([]byte, bool, error) {
	rnd, err := renderer()
	if err != nil {
		return nil, false, err
	}
	if pageSize < 1 {
		pageSize = 1
	}

	totalCount, err := store.CountVersions(ctx, key)
	if err != nil {
		return nil, false, err
	}
	keyStateObj, known := st.KeyState(key)
	if !known && totalCount == 0 {
		return nil, false, nil
	}

	pageWindow, hasNewer, hasOlder, err := fetchVersionPage(ctx, store, key, cursor, pageSize)
	if err != nil {
		return nil, false, err
	}

	newestPage := !hasNewer
	latest := keyStateObj.LatestVersion
	if latest == "" && newestPage && len(pageWindow) > 0 {
		latest = pageWindow[0].Version
	}
	overlayArr, installArr, altInstallArr, err := keyOverlaysAndInstall(ctx, store, lnk, ctxObj.Alternate, key, latest)
	if err != nil {
		return nil, false, err
	}

	rowArr := make([]view.VersionEntryObj, 0, len(pageWindow))
	for i := range pageWindow {
		versionObj := pageWindow[i]
		goFlag, composerFlag := versionEcosystems(ctx, store, key, versionObj.Version)
		rowArr = append(rowArr, view.VersionEntryObj{
			Version:         versionObj.Version,
			IngestedAt:      versionObj.IngestTS,
			SourceSizeBytes: versionObj.SourceSizeBytes,
			Go:              goFlag,
			Composer:        composerFlag,
		})
	}

	pageObj := view.PageObj{Total: totalCount, Newest: newestPage}
	if hasNewer && len(pageWindow) > 0 {
		firstObj := pageWindow[0]
		pageObj.NewerCursor = EncodeCursor(firstObj.UpstreamSeq, firstObj.Version)
	}
	if hasOlder && len(pageWindow) > 0 {
		lastObj := pageWindow[len(pageWindow)-1]
		pageObj.OlderCursor = EncodeCursor(lastObj.UpstreamSeq, lastObj.Version)
	}

	bodyArr, err := rnd.Key(view.KeyObj{
		Context:       ctxObj,
		Key:           key,
		LatestVersion: latest,
		Source:        originOf(keyStateObj),
		Overlays:      overlayArr,
		Install:       installArr,
		AltInstall:    altInstallArr,
		Versions:      rowArr,
		Page:          pageObj,
	})
	if err != nil {
		return nil, false, err
	}
	return bodyArr, true, nil
}

func fetchVersionPage(ctx context.Context, store VersionReaderInterface, key string, cursor PageCursorObj, pageSize int) (window []core.VersionObj, hasNewer bool, hasOlder bool, err error) {
	switch {
	case cursor.Set && cursor.Newer:
		ascArr, qErr := store.ListVersionsKeysetBefore(ctx, key, false, cursor.Seq, cursor.Version, pageSize+1)
		if qErr != nil {
			return nil, false, false, qErr
		}
		hasNewer = len(ascArr) > pageSize
		if hasNewer {
			ascArr = ascArr[:pageSize]
		}
		reverseVersions(ascArr)
		return ascArr, hasNewer, true, nil
	case cursor.Set:
		descArr, qErr := store.ListVersionsKeyset(ctx, key, false, cursor.Seq, cursor.Version, pageSize+1)
		if qErr != nil {
			return nil, false, false, qErr
		}
		hasOlder = len(descArr) > pageSize
		if hasOlder {
			descArr = descArr[:pageSize]
		}
		return descArr, true, hasOlder, nil
	default:
		descArr, qErr := store.ListVersionsKeyset(ctx, key, false, 0, "", pageSize+1)
		if qErr != nil {
			return nil, false, false, qErr
		}
		hasOlder = len(descArr) > pageSize
		if hasOlder {
			descArr = descArr[:pageSize]
		}
		return descArr, false, hasOlder, nil
	}
}

func reverseVersions(arr []core.VersionObj) {
	for i, j := 0, len(arr)-1; i < j; i, j = i+1, j-1 {
		arr[i], arr[j] = arr[j], arr[i]
	}
}

func versionEcosystems(ctx context.Context, store VersionReaderInterface, key string, version string) (goFlag bool, composerFlag bool) {
	detectionObj, ok, err := store.GetDetection(ctx, key, version)
	if err != nil || !ok {
		return false, false
	}
	return detectionObj.IsGo && !detectionObj.GoZipBlocked, detectionObj.IsComposer
}

func keyOverlaysAndInstall(ctx context.Context, store VersionReaderInterface, lnk link.Obj, altObj view.AlternateObj, key string, latest string) ([]string, []view.CodeSnippetObj, []view.CodeSnippetObj, error) {
	if latest == "" {
		return nil, nil, nil, nil
	}
	detectionObj, ok, err := store.GetDetection(ctx, key, latest)
	if err != nil {
		return nil, nil, nil, err
	}
	if !ok {
		return nil, nil, nil, nil
	}
	_, composerCand := overlay.CandidateFromDetection(detectionObj)

	var overlayArr []string
	if detectionObj.IsGo {
		overlayArr = append(overlayArr, "go")
	}
	if detectionObj.IsComposer {
		overlayArr = append(overlayArr, "composer")
	}

	scheme, host := schemeHost(lnk)
	installArr := keyInstall(detectionObj, composerCand, lnk, key, latest, scheme, host)
	var altArr []view.CodeSnippetObj
	if altObj.CopyHost != "" {
		altArr = dropDuplicateSnippets(keyInstall(detectionObj, composerCand, lnk, key, latest, altObj.Scheme, altObj.CopyHost), installArr)
	}
	return overlayArr, installArr, altArr, nil
}

func keyInstall(detectionObj core.DetectionObj, composerCand *overlay.CandidateObj, lnk link.Obj, key string, latest string, scheme string, host string) []view.CodeSnippetObj {
	var installArr []view.CodeSnippetObj
	if detectionObj.IsGo && !detectionObj.GoZipBlocked && host != "" {
		modulePath := host + lnk.Key(key, "")
		if major := semver.Major(latest); major != "" && major != "v0" && major != "v1" {
			modulePath += "/" + major
		}
		installArr = append(installArr, view.CodeSnippetObj{
			Label: "go",
			Body:  overlay.GoInstallSnippet(modulePath, "latest", scheme, host),
		})
	}
	if detectionObj.IsComposer && composerCand != nil && host != "" {
		installArr = append(installArr, view.CodeSnippetObj{
			Label: "composer",
			Body:  overlay.ComposerRequireSnippet(composerCand.ComposerName, latest, scheme, host, lnk.Base()),
		})
	}
	return installArr
}
