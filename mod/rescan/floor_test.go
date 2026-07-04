package rescan

import (
	"testing"

	"github.com/voluminor/yggvault/mod/source"
)

// // // // // // // // // //

func relArr(versions ...string) []source.GitReleaseObj {
	outArr := make([]source.GitReleaseObj, len(versions))
	for i, v := range versions {
		outArr[i] = source.GitReleaseObj{Version: v}
	}
	return outArr
}

func TestMinReleaseVersion(t *testing.T) {
	caseArr := []struct {
		name string
		in   []source.GitReleaseObj
		want string
	}{
		{"newest_first", relArr("v2.0.0", "v1.5.0", "v1.2.0"), "v1.2.0"},
		{"oldest_first", relArr("v1.2.0", "v1.5.0", "v2.0.0"), "v1.2.0"},
		{"single", relArr("v3.1.4"), "v3.1.4"},
		{"empty", relArr(), ""},
		{"junk_ignored", relArr("not-semver", "v1.0.0"), "v1.0.0"},
		{"all_junk", relArr("garbage", "x"), ""},
	}
	for _, tc := range caseArr {
		if got := minReleaseVersion(tc.in); got != tc.want {
			t.Errorf("%s: minReleaseVersion=%q want %q", tc.name, got, tc.want)
		}
	}
}

func TestBelowFloor(t *testing.T) {
	if !belowFloor("v1.0.0", "v1.5.0") {
		t.Error("v1.0.0 must be below floor v1.5.0")
	}
	if belowFloor("v2.0.0", "v1.5.0") {
		t.Error("v2.0.0 must not be below floor v1.5.0")
	}
	if belowFloor("v1.5.0", "v1.5.0") {
		t.Error("equal version must not be below floor")
	}
	if belowFloor("junk", "v1.5.0") {
		t.Error("non-semver must not be treated as below floor")
	}
}
