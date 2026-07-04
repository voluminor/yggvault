package overlay

import (
	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

// MaterializerDescriptorObj is materializer_id, format_version, and artifact kinds for self-test.
type MaterializerDescriptorObj struct {
	MaterializerID stcode.MaterializerType
	FormatVersion  uint32
	Kinds          []archive.FormatType
}

// Descriptors returns current format versions for byte-stable materializers. The driver rebuilds the smallest
// artifact for each pair and compares body_hash. Served-live Composer/Go JSON metadata is excluded.
func (obj *Obj) Descriptors() []MaterializerDescriptorObj {
	return []MaterializerDescriptorObj{
		{MaterializerID: stcode.MaterializerUniversal, FormatVersion: UniversalZipFormatVersion, Kinds: []archive.FormatType{archive.FormatZip}},
		{MaterializerID: stcode.MaterializerUniversal, FormatVersion: UniversalTarGzFormatVersion, Kinds: []archive.FormatType{archive.FormatTarGz}},
		{MaterializerID: stcode.MaterializerGo, FormatVersion: GoZipFormatVersion, Kinds: []archive.FormatType{archive.FormatZip}},
	}
}

// // // // // // // // // //

// CollisionError returns a stable stcode error for composer name collisions.
func CollisionError(collisionObj ComposerCollisionObj) error {
	return stcode.NewErrComposerNameCollision(collisionObj.ConflictingKey, collisionObj.Key, collisionObj.Name)
}
