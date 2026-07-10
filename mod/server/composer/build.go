package composer

import (
	"context"
	"encoding/hex"
	"path"
	"strings"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/server/pager"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/target/api"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

// ResolveObj is parsed p2 addressing: composer name, dev flag, and winner key.
type ResolveObj struct {
	Name string
	Dev  bool
	Key  string
}

// // // // // // // // // //

func parsePackageFile(vendor string, packageFile string) (name string, dev bool, ok bool) {
	if !strings.HasSuffix(packageFile, ".json") {
		return "", false, false
	}
	baseText := strings.TrimSuffix(packageFile, ".json")
	devFlag := false
	if strings.HasSuffix(baseText, "~dev") {
		devFlag = true
		baseText = strings.TrimSuffix(baseText, "~dev")
	}
	if vendor == "" || baseText == "" {
		return "", false, false
	}
	return vendor + "/" + baseText, devFlag, true
}

func shallowerPath(aText string, bText string) bool {
	da, db := strings.Count(aText, "/"), strings.Count(bText, "/")
	if da != db {
		return da < db
	}
	return aText < bText
}

func composerJSONOf(ctx context.Context, store VersionReaderInterface, treeHashObj core.HashObj) ([]byte, error) {
	entriesArr, err := store.ReadTree(ctx, treeHashObj)
	if err != nil {
		return nil, err
	}
	var bestObj core.TreeEntryObj
	foundFlag := false
	for i := range entriesArr {
		entryObj := entriesArr[i]
		if entryObj.Mode != core.ModeFile || path.Base(entryObj.Path) != "composer.json" {
			continue
		}
		if !foundFlag || shallowerPath(entryObj.Path, bestObj.Path) {
			bestObj = entryObj
			foundFlag = true
		}
	}
	if !foundFlag {
		return nil, nil
	}
	return store.ReadBlob(ctx, bestObj.BlobHash)
}

func universalZipShasum(ctx context.Context, store VersionReaderInterface, key string, version string, listenerID stcode.ListenerType) string {
	candidateArr := []stcode.ListenerType{listenerID}
	if listenerID != stcode.ListenerGlobal {
		candidateArr = append(candidateArr, stcode.ListenerGlobal)
	}
	for _, lidObj := range candidateArr {
		keyObj := core.ArtifactKeyObj{MaterializerID: stcode.MaterializerUniversal.String(), ArtifactKind: string(archive.FormatZip), ListenerID: lidObj.String(), Key: key, Version: version}
		artifactObj, ok, err := store.GetArtifact(ctx, keyObj)
		if err != nil || !ok {
			continue
		}
		if len(artifactObj.BodySha1) > 0 {
			return hex.EncodeToString(artifactObj.BodySha1)
		}
	}
	return ""
}

func inputsFor(ctx context.Context, store VersionReaderInterface, key string, linkObj link.Obj, listenerID stcode.ListenerType) ([]overlay.ComposerVersionInputObj, error) {
	inputArr := make([]overlay.ComposerVersionInputObj, 0, pager.PageSize)
	var totalJSONBytes uint64
	err := pager.EachVersion(ctx, store, key, func(versionObj core.VersionObj) (bool, error) {
		if util.IsRawVersionName(versionObj.Version) {
			return false, nil
		}
		composerJSON, jerr := composerJSONOf(ctx, store, versionObj.TreeHash)
		if jerr != nil {
			return false, jerr
		}
		if uint64(len(composerJSON)) > cMaxManifestBytes || totalJSONBytes+uint64(len(composerJSON)) > cMaxInputBytes {
			composerJSON = nil
		} else {
			totalJSONBytes += uint64(len(composerJSON))
		}
		inputArr = append(inputArr, overlay.ComposerVersionInputObj{
			Version:      versionObj.Version,
			IngestTS:     versionObj.IngestTS,
			DistURL:      linkObj.Abs(linkObj.Key(key, "/"+versionObj.Version+".zip")),
			DistShasum:   universalZipShasum(ctx, store, key, versionObj.Version, listenerID),
			ComposerJSON: composerJSON,
		})
		return len(inputArr) >= pager.MaxVersions, nil
	})
	if err != nil {
		return nil, err
	}
	return inputArr, nil
}

// // // // // // // // // //

// Resolve maps vendor/packageFile to composer name, dev flag, and winner key.
// Bad files and unknown names return serr.ErrNotFound; callers derive ETags from the result.
func Resolve(names NamesReaderInterface, vendor string, packageFile string) (ResolveObj, error) {
	composerName, devFlag, ok := parsePackageFile(vendor, packageFile)
	if !ok {
		return ResolveObj{}, serr.ErrNotFound
	}
	key, ok := names.ComposerKeyForName(composerName)
	if !ok {
		return ResolveObj{}, serr.ErrNotFound
	}
	return ResolveObj{Name: composerName, Dev: devFlag, Key: key}, nil
}

// BuildPackages builds `/packages.json` from cross-key composer names without storage reads.
func BuildPackages(overlayObj *overlay.Obj, names NamesReaderInterface) *api.ComposerPackagesObj {
	return overlayObj.ComposerPackages(names.ComposerPackageNames())
}

// BuildPackageList builds `/packages/list.json` with optional prefix or glob filtering.
// The caller gates filter length first; an empty filter returns all names.
func BuildPackageList(overlayObj *overlay.Obj, names NamesReaderInterface, filter string) *api.ComposerPackageListObj {
	nameArr := names.ComposerPackageNames()
	if filter != "" {
		filteredArr := make([]string, 0, len(nameArr))
		for _, nameText := range nameArr {
			if filterMatch(filter, nameText) {
				filteredArr = append(filteredArr, nameText)
			}
		}
		nameArr = filteredArr
	}
	return overlayObj.ComposerPackageList(nameArr)
}

// BuildP2 builds `/p2/{vendor}/{packageFile}` for an already resolved package.
// Dev variants return an empty version set; overlay rejection maps to serr.ErrNotFound.
// Dist URLs are host-sensitive, and the caller resolves addressing first for ETag calculation.
func BuildP2(ctx context.Context, store VersionReaderInterface, overlayObj *overlay.Obj, resolveObj ResolveObj, linkObj link.Obj, listenerID stcode.ListenerType) (*api.ComposerP2Obj, error) {
	var inputArr []overlay.ComposerVersionInputObj
	if !resolveObj.Dev {
		builtArr, err := inputsFor(ctx, store, resolveObj.Key, linkObj, listenerID)
		if err != nil {
			return nil, err
		}
		inputArr = builtArr
	}
	p2Obj, err := overlayObj.ComposerP2(resolveObj.Name, inputArr)
	if err != nil {
		return nil, serr.ErrNotFound
	}
	return p2Obj, nil
}
