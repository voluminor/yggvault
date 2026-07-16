package overlay

import "errors"

// // // // // // // // // //

var (
	errManifestMissing       = errors.New("go.mod not found in version tree")
	errManifestTooLarge      = errors.New("go.mod exceeds manifest size cap")
	errRewrittenFileTooLarge = errors.New("rewritten file exceeds max file bytes")
	errInvalidArtifactRef    = errors.New("invalid key/version for artifact")
	errInvalidGoVersion      = errors.New("invalid version for go module zip")
)

// // // // // // // // // //

// DegradedReason returns a stable degradation code for non-nil overlay build/detect errors. overlay owns this
// dictionary; rescan/server only store the returned code. Unknown build errors map to "materialization_error".
func DegradedReason(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, errSymlinkInGoModule):
		return "go_symlink_in_module", true
	case errors.Is(err, errManifestMissing):
		return "go_manifest_missing", true
	case errors.Is(err, errManifestTooLarge):
		return "go_manifest_too_large", true
	case errors.Is(err, errRewrittenFileTooLarge):
		return "rewritten_file_too_large", true
	case errors.Is(err, errInvalidArtifactRef):
		return "invalid_artifact_ref", true
	case errors.Is(err, errInvalidGoVersion):
		return "invalid_go_version", true
	default:
		return "materialization_error", true
	}
}
