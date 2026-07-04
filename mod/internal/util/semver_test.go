package util

import "testing"

// // // // // // // // // //

func TestIsCanonicalSemver(t *testing.T) {
	if !IsCanonicalSemver("v1.2.3") {
		t.Fatal("expected canonical semver to be accepted")
	}
	if IsCanonicalSemver("1.2.3") {
		t.Fatal("expected non-canonical semver to be rejected")
	}
}

func TestCompareSemver(t *testing.T) {
	compareValue, err := CompareSemver("v1.2.3", "v1.2.4")
	if err != nil {
		t.Fatalf("CompareSemver returned error: %v", err)
	}
	if compareValue >= 0 {
		t.Fatalf("unexpected compare result: %d", compareValue)
	}
}

func TestIsStorableSemver(t *testing.T) {
	storable := []string{"v1.2.3", "1.2.3", "7.12.3", "v0.0.0-hr1", "1.0", "v2.0.0-rc.1"}
	for _, version := range storable {
		if !IsStorableSemver(version) {
			t.Fatalf("expected %q to be storable", version)
		}
	}
	rejected := []string{"", "dev-master", "2.x-dev", "1.2.3.4", "1.2.3+build", "v1.2.3+incompatible", " 1.2.3", "1.2.3\n"}
	for _, version := range rejected {
		if IsStorableSemver(version) {
			t.Fatalf("expected %q to be rejected", version)
		}
	}
}

func TestCompareSemverMixed(t *testing.T) {
	if cmp, err := CompareSemver("7.9.1", "7.12.3"); err != nil || cmp >= 0 {
		t.Fatalf("expected 7.9.1 < 7.12.3, got cmp=%d err=%v", cmp, err)
	}
	if cmp, err := CompareSemver("1.2.3", "v1.2.4"); err != nil || cmp >= 0 {
		t.Fatalf("expected 1.2.3 < v1.2.4, got cmp=%d err=%v", cmp, err)
	}
	if cmp, err := CompareSemver("v1.2.3", "1.2.3"); err != nil || cmp != 0 {
		t.Fatalf("expected v1.2.3 == 1.2.3, got cmp=%d err=%v", cmp, err)
	}
}
