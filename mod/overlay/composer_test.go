package overlay

import (
	"strings"
	"testing"
	"time"

	"github.com/voluminor/yggvault/target/api"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func TestComposerP2(t *testing.T) {
	obj := newTestOverlay(t)
	ts := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	inputArr := []ComposerVersionInputObj{
		{Version: "v1.0.0", IngestTS: ts, DistURL: "/foo/v1.0.0.zip", DistShasum: "sha1-100", ComposerJSON: []byte(`{"type":"library","require":{"php":">=8.1"},"license":"MIT"}`)},
		{Version: "v1.1.0", IngestTS: ts, DistURL: "/foo/v1.1.0.zip", DistShasum: "sha1-110", ComposerJSON: []byte(`{"type":"library","license":["MIT","Apache-2.0"],"autoload":{"psr-4":{"Foo\\":"src/"}}}`)},
	}

	p2, err := obj.ComposerP2("vendor/pkg", inputArr)
	if err != nil {
		t.Fatalf("ComposerP2: %v", err)
	}
	versionsArr := p2.Packages["vendor/pkg"]
	if len(versionsArr) != 2 {
		t.Fatalf("versions=%d, want 2", len(versionsArr))
	}
	if versionsArr[0].Version != "v1.1.0" || versionsArr[0].VersionNormalized.Value != "1.1.0.0" {
		t.Fatalf("sort/normalize: %+v", versionsArr[0])
	}
	if versionsArr[0].Dist.Type != api.ComposerDistObjTypeZip || versionsArr[0].Dist.URL != "/foo/v1.1.0.zip" || versionsArr[0].Dist.Shasum.Value != "sha1-110" {
		t.Fatalf("dist: %+v", versionsArr[0].Dist)
	}
	if len(versionsArr[0].License) != 2 {
		t.Fatalf("license array: %v", versionsArr[0].License)
	}
	if len(versionsArr[1].License) != 1 || versionsArr[1].License[0] != "MIT" {
		t.Fatalf("license string: %v", versionsArr[1].License)
	}
	if !versionsArr[0].Autoload.Set {
		t.Fatal("autoload must be set for v1.1.0")
	}
	if !versionsArr[1].Require.Set || versionsArr[1].Require.Value["php"] != ">=8.1" {
		t.Fatalf("require: %+v", versionsArr[1].Require)
	}
	if versionsArr[0].Type.Value != "library" {
		t.Fatalf("type: %+v", versionsArr[0].Type)
	}
}

func TestComposerPackagesAndListNestedPrefix(t *testing.T) {
	configObj := stconf.FullConfig()
	configObj.Web.Routing.Prefix = "api"
	configObj.Web.Static.Dir = "static"
	obj, err := New(configObj)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	packagesObj := obj.ComposerPackages([]string{"vendor/b", "vendor/a"})
	if packagesObj.MetadataMinusURL != "/api/p2/%package%.json" {
		t.Fatalf("metadata-url=%q", packagesObj.MetadataMinusURL)
	}
	if packagesObj.List.Value != "/api/packages/list.json" {
		t.Fatalf("list=%q", packagesObj.List.Value)
	}
	if len(packagesObj.AvailableMinusPackages) != 2 || packagesObj.AvailableMinusPackages[0] != "vendor/a" {
		t.Fatalf("available-packages not sorted: %v", packagesObj.AvailableMinusPackages)
	}

	listObj := obj.ComposerPackageList([]string{"vendor/b", "vendor/a"})
	if len(listObj.PackageNames) != 2 || listObj.PackageNames[0] != "vendor/a" {
		t.Fatalf("package list not sorted: %v", listObj.PackageNames)
	}
}

func TestComposerP2RejectsBadName(t *testing.T) {
	obj := newTestOverlay(t)
	if _, err := obj.ComposerP2("../../etc/passwd", nil); err == nil {
		t.Fatal("expected error for invalid package name")
	}
}

func TestGoInfo(t *testing.T) {
	ts := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	infoObj := GoInfo("v1.2.3", ts)
	if infoObj.Version != "v1.2.3" || !infoObj.Time.Equal(ts) {
		t.Fatalf("unexpected GoInfo: %+v", infoObj)
	}
}

func TestResolveComposerNamesCollision(t *testing.T) {
	namesArr, nameToKey, collisionArr := ResolveComposerNames(map[string]string{
		"key-b": "vendor/dup",
		"key-a": "vendor/dup",
		"key-c": "vendor/unique",
	})
	if len(namesArr) != 2 {
		t.Fatalf("unique names=%v", namesArr)
	}
	if len(collisionArr) != 1 {
		t.Fatalf("collisions=%d, want 1", len(collisionArr))
	}
	if collisionArr[0].Key != "key-b" || collisionArr[0].ConflictingKey != "key-a" || collisionArr[0].Name != "vendor/dup" {
		t.Fatalf("unexpected collision: %+v", collisionArr[0])
	}
	if nameToKey["vendor/dup"] != "key-a" || nameToKey["vendor/unique"] != "key-c" {
		t.Fatalf("unexpected name→key: %+v", nameToKey)
	}
	if err := CollisionError(collisionArr[0]); err == nil {
		t.Fatal("expected non-nil collision error")
	}
}

func TestSnippets(t *testing.T) {
	bazel := BazelSnippet("mykey", "https://host/mykey/v1.0.0.tar.gz", []byte("0123456789abcdef0123456789abcdef"), "mykey-v1.0.0")
	if !strings.Contains(bazel, "http_archive(") || !strings.Contains(bazel, "integrity = \"sha256-") || !strings.Contains(bazel, `strip_prefix = "mykey-v1.0.0"`) {
		t.Fatalf("unexpected bazel snippet: %s", bazel)
	}
	zigSnippet := ZigSnippet("https://host/mykey/v1.0.0.tar.gz")
	if !strings.Contains(zigSnippet, ".url = \"https://host/mykey/v1.0.0.tar.gz\"") {
		t.Fatalf("unexpected zig snippet: %s", zigSnippet)
	}
}

// Composer recipes must route through the mirror; http entries also disable secure-http.
func TestComposerRequireSnippet(t *testing.T) {
	wantHTTPS := "composer config repositories.yggvault composer https://vault.test\n" +
		"composer require acme/alpha:v1.0.0\n"
	if got := ComposerRequireSnippet("acme/alpha", "v1.0.0", "https", "vault.test"); got != wantHTTPS {
		t.Errorf("https snippet = %q, want %q", got, wantHTTPS)
	}

	wantHTTP := "composer config secure-http false\n" +
		"composer config repositories.yggvault composer http://node.pk.ygg\n" +
		"composer require acme/alpha:v1.0.0\n"
	if got := ComposerRequireSnippet("acme/alpha", "v1.0.0", "http", "node.pk.ygg"); got != wantHTTP {
		t.Errorf("http snippet = %q, want %q", got, wantHTTP)
	}
}
