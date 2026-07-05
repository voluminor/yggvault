package overlay

import (
	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

// universalFormatArr lists universal artifact formats with their format versions.
var universalFormatArr = []struct {
	format        archive.FormatType
	formatVersion uint32
}{
	{archive.FormatZip, UniversalZipFormatVersion},
	{archive.FormatTarGz, UniversalTarGzFormatVersion},
}

// // // // // // // // // //

// ArtifactPlanObj is one byte-stable artifact to materialize: identity tuple, listener, and ready builder. overlay
// owns the build policy; rescan hashes Builder and registers ArtifactObj.
type ArtifactPlanObj struct {
	MaterializerID stcode.MaterializerType
	ArtifactKind   archive.FormatType
	FormatVersion  uint32
	ListenerID     stcode.ListenerType
	Builder        ArtifactBuilderInterface
}

// // // // // // // // // //

// ArtifactPlan returns byte-stable artifacts for a version under active listeners:
//   - universal zip+tar.gz are the raw uploaded tree, host-independent, and always a single global row;
//   - go-zip is per listener and only for GoPublishable versions.
//
// Composer dist uses universal zip; served-live JSON metadata is not part of the plan.
func (obj *Obj) ArtifactPlan(
	st StorageInterface,
	key string,
	version string,
	treeHashObj core.HashObj,
	detectionObj core.DetectionObj,
	candidateObj *CandidateObj,
	rewriteArr []core.HashObj,
	listenerArr []ListenerCtxObj,
) []ArtifactPlanObj {
	var planArr []ArtifactPlanObj

	for _, formatSpec := range universalFormatArr {
		planArr = append(planArr, obj.universalPlan(st, key, version, treeHashObj, formatSpec.format, formatSpec.formatVersion))
	}

	for i := range listenerArr {
		listenerCtxObj := listenerArr[i]
		if !obj.GoPublishable(key, version, detectionObj, candidateObj, listenerCtxObj) {
			continue
		}
		planArr = append(planArr, ArtifactPlanObj{
			MaterializerID: stcode.MaterializerGo,
			ArtifactKind:   archive.FormatZip,
			FormatVersion:  GoZipFormatVersion,
			ListenerID:     listenerCtxObj.ListenerID,
			Builder:        obj.GoModuleZipBuilder(st, key, version, treeHashObj, detectionObj, candidateObj, rewriteArr, listenerCtxObj),
		})
	}
	return planArr
}

func (obj *Obj) universalPlan(st StorageInterface, key string, version string, treeHashObj core.HashObj, format archive.FormatType, formatVersion uint32) ArtifactPlanObj {
	return ArtifactPlanObj{
		MaterializerID: stcode.MaterializerUniversal,
		ArtifactKind:   format,
		FormatVersion:  formatVersion,
		ListenerID:     stcode.ListenerGlobal,
		Builder:        obj.UniversalBuilder(st, key, version, treeHashObj, format),
	}
}
