package overlay

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-faster/jx"

	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

type composerManifestObj struct {
	Type       string                     `json:"type"`
	Require    map[string]string          `json:"require"`
	RequireDev map[string]string          `json:"require-dev"`
	Autoload   map[string]json.RawMessage `json:"autoload"`
	License    json.RawMessage            `json:"license"` // in the wild: string | []string
}

// ComposerVersionInputObj is one version build input assembled by rescan/server: version, time, universal dist, and
// raw composer.json. overlay parses it and builds generated api.* objects.
type ComposerVersionInputObj struct {
	Version      string
	IngestTS     time.Time // composer `time` source is local ingest time
	DistURL      string
	DistShasum   string // lowercase hex sha1 universal zip (rescan: hex.EncodeToString(digest.BodySha1))
	ComposerJSON []byte
}

// ComposerCollisionObj is a composer-name conflict between keys.
type ComposerCollisionObj struct {
	Name           string
	Key            string
	ConflictingKey string
}

// // // // // // // // // //

func parseComposerLicense(rawArr json.RawMessage) []string {
	if len(rawArr) == 0 {
		return nil
	}
	var arrValue []string
	if json.Unmarshal(rawArr, &arrValue) == nil {
		return arrValue
	}
	var singleValue string
	if json.Unmarshal(rawArr, &singleValue) == nil && singleValue != "" {
		return []string{singleValue}
	}
	return nil
}

func (obj *Obj) composerPath(suffix string) string {
	if !obj.nested || obj.routingPrefix == "" {
		return suffix
	}
	return "/" + obj.routingPrefix + suffix
}

func composerVersion(packageName string, inputObj ComposerVersionInputObj) api.ComposerPackageVersionObj {
	var manifestObj composerManifestObj
	if n := len(inputObj.ComposerJSON); n > 0 && n <= cMaxManifestBytes {
		_ = json.Unmarshal(inputObj.ComposerJSON, &manifestObj)
	}

	distObj := api.ComposerDistObj{
		Reference: inputObj.Version,
		Type:      api.ComposerDistObjTypeZip,
		URL:       inputObj.DistURL,
	}
	if inputObj.DistShasum != "" {
		distObj.Shasum = api.NewOptString(inputObj.DistShasum)
	}

	versionObj := api.ComposerPackageVersionObj{
		Name:    packageName,
		Version: inputObj.Version,
		License: parseComposerLicense(manifestObj.License),
		Dist:    distObj,
	}
	versionObj.VersionNormalized = api.NewOptString(composerNormalize(inputObj.Version))
	if !inputObj.IngestTS.IsZero() {
		versionObj.Time = api.NewOptDateTime(inputObj.IngestTS.UTC())
	}
	if manifestObj.Type != "" {
		versionObj.Type = api.NewOptString(manifestObj.Type)
	}
	if len(manifestObj.Require) > 0 {
		versionObj.Require = api.NewOptComposerPackageVersionObjRequire(manifestObj.Require)
	}
	if len(manifestObj.RequireDev) > 0 {
		versionObj.RequireMinusDev = api.NewOptComposerPackageVersionObjRequireMinusDev(manifestObj.RequireDev)
	}
	if len(manifestObj.Autoload) > 0 {
		autoloadObj := make(api.ComposerPackageVersionObjAutoload, len(manifestObj.Autoload))
		for sectionName, rawValue := range manifestObj.Autoload {
			autoloadObj[sectionName] = jx.Raw(rawValue)
		}
		versionObj.Autoload = api.NewOptComposerPackageVersionObjAutoload(autoloadObj)
	}
	return versionObj
}

// // // // // // // // // //

// ComposerP2 builds a generated p2 package document sorted descending. mod/server returns it directly.
// dist is universal zip; shasum is sha1.
func (obj *Obj) ComposerP2(packageName string, inputArr []ComposerVersionInputObj) (*api.ComposerP2Obj, error) {
	if !validComposerName(packageName) {
		return nil, fmt.Errorf("invalid composer package name: %q", packageName)
	}
	sortedArr := append([]ComposerVersionInputObj(nil), inputArr...)
	sort.SliceStable(sortedArr, func(i, j int) bool {
		compareValue, err := util.CompareSemver(sortedArr[i].Version, sortedArr[j].Version)
		if err != nil {
			return sortedArr[i].Version > sortedArr[j].Version
		}
		return compareValue > 0
	})

	versionArr := make([]api.ComposerPackageVersionObj, 0, len(sortedArr))
	for _, inputObj := range sortedArr {
		if strings.HasPrefix(inputObj.Version, "v0.0.0-") {
			continue
		}
		versionArr = append(versionArr, composerVersion(packageName, inputObj))
	}
	return &api.ComposerP2Obj{Packages: api.ComposerP2ObjPackages{packageName: versionArr}}, nil
}

// ComposerPackages builds generated packages.json with metadata-url, available-packages, and list.
func (obj *Obj) ComposerPackages(packageNames []string) *api.ComposerPackagesObj {
	namesArr := append([]string(nil), packageNames...)
	sort.Strings(namesArr)
	packagesObj := &api.ComposerPackagesObj{
		AvailableMinusPackages: namesArr,
		MetadataMinusURL:       obj.composerPath("/p2/%package%.json"),
	}
	packagesObj.List = api.NewOptString(obj.composerPath("/packages/list.json"))
	return packagesObj
}

// ComposerPackageList builds generated packages/list.json.
func (obj *Obj) ComposerPackageList(packageNames []string) *api.ComposerPackageListObj {
	namesArr := append([]string(nil), packageNames...)
	sort.Strings(namesArr)
	return &api.ComposerPackageListObj{PackageNames: namesArr}
}

// // // // // // // // // //

// ResolveComposerNames enforces uniqueness across keys: sorted unique names, name-to-winner-key map for serve-time
// p2, and collisions. The lexicographically smaller key wins; rescan owns cross-key aggregation, cache, and ETag.
func ResolveComposerNames(nameByKey map[string]string) (names []string, nameToKey map[string]string, collisions []ComposerCollisionObj) {
	keysArr := make([]string, 0, len(nameByKey))
	for keyText := range nameByKey {
		keysArr = append(keysArr, keyText)
	}
	sort.Strings(keysArr)

	ownerByName := make(map[string]string, len(keysArr))
	var collisionArr []ComposerCollisionObj
	for _, keyText := range keysArr {
		nameText := nameByKey[keyText]
		if existingKey, ok := ownerByName[nameText]; ok {
			collisionArr = append(collisionArr, ComposerCollisionObj{Name: nameText, Key: keyText, ConflictingKey: existingKey})
			continue
		}
		ownerByName[nameText] = keyText
	}

	namesArr := make([]string, 0, len(ownerByName))
	for nameText := range ownerByName {
		namesArr = append(namesArr, nameText)
	}
	sort.Strings(namesArr)
	return namesArr, ownerByName, collisionArr
}
