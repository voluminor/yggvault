package overlay

import (
	"errors"
	"fmt"
	"testing"
)

// // // // // // // // // //

func TestDegradedReason(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
		wantDeg  bool
	}{
		{"nil", nil, "", false},
		{"symlink", fmt.Errorf("ctx: %w", errSymlinkInGoModule), "go_symlink_in_module", true},
		{"manifest missing", fmt.Errorf("ctx: %w", errManifestMissing), "go_manifest_missing", true},
		{"manifest too large", fmt.Errorf("ctx: %w", errManifestTooLarge), "go_manifest_too_large", true},
		{"rewritten too large", fmt.Errorf("ctx: %w", errRewrittenFileTooLarge), "rewritten_file_too_large", true},
		{"invalid ref", fmt.Errorf("ctx: %w", errInvalidArtifactRef), "invalid_artifact_ref", true},
		{"invalid go version", fmt.Errorf("ctx: %w", errInvalidGoVersion), "invalid_go_version", true},
		{"unknown build error", errors.New("boom"), "materialization_error", true},
	}
	for _, c := range cases {
		code, deg := DegradedReason(c.err)
		if code != c.wantCode || deg != c.wantDeg {
			t.Errorf("%s: got (%q,%v) want (%q,%v)", c.name, code, deg, c.wantCode, c.wantDeg)
		}
	}
}
