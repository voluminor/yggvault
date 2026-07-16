package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/mod/mesh"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func newValidConfigObjForTest(t *testing.T) *stcfg.ConfigObj {
	t.Helper()

	configObj := stcfg.FullConfig()

	configObj.ReleaseMirrors = map[string]string{
		"core-lib": "https://upstream.example.org/core-lib",
	}
	configObj.Ygg.PemKey = "test-pem-key"

	return configObj
}

func enableWebForTest(configObj *stcfg.ConfigObj) {
	configObj.Web.Server.Domain = "example.com"
	configObj.Web.Server.Mode = stcfg.WebServerModeShared
	configObj.Web.Server.Shared.Listen = "127.0.0.1:8080"
}

func writeConfigFileForTest(t *testing.T, configObj *stcfg.ConfigObj) string {
	t.Helper()

	data, err := configObj.RenderYAML(false)
	if err != nil {
		t.Fatalf("RenderYAML returned error: %v", err)
	}

	pathToFile := filepath.Join(t.TempDir(), "config.yml")
	if err = os.WriteFile(pathToFile, data, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	return pathToFile
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	configObj := newValidConfigObjForTest(t)

	if err := validate(configObj); err != nil {
		t.Fatalf("validate returned error: %v", err)
	}
}

func TestValidateRejectsSourceRateLimitBurstZero(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	configObj.Source.RateLimit.RequestsPerSecond = 1
	configObj.Source.RateLimit.Burst = 0

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), "source.rate_limit.burst must be >= 1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewLoadsValidConfig(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	pathToFile := writeConfigFileForTest(t, configObj)

	loadedObj, err := New(pathToFile)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if loadedObj == nil {
		t.Fatal("New returned nil config")
	}
	if loadedObj.ReleaseMirrors["core-lib"] != "https://upstream.example.org/core-lib" {
		t.Fatalf("unexpected release mirror: %#v", loadedObj.ReleaseMirrors)
	}
}

func TestValidateRejectsSelfMirror(t *testing.T) {
	t.Run("mirror host equals own web domain", func(t *testing.T) {
		configObj := newValidConfigObjForTest(t)
		enableWebForTest(configObj)
		configObj.ReleaseMirrors["looped"] = "https://example.com/looped"

		err := validate(configObj)
		if err == nil || !strings.Contains(err.Error(), "cannot mirror itself") {
			t.Fatalf("expected self-mirror rejection, got: %v", err)
		}
	})

	t.Run("mirror host equals own ygg host", func(t *testing.T) {
		pemBytes, ownHost, err := mesh.GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey returned error: %v", err)
		}
		pemPath := filepath.Join(t.TempDir(), "node.pem")
		if err := mesh.WriteKeyFile(pemPath, pemBytes); err != nil {
			t.Fatalf("WriteKeyFile returned error: %v", err)
		}

		configObj := newValidConfigObjForTest(t)
		configObj.Ygg.PemKey = pemPath
		configObj.Ygg.Peers.Initial = []string{"tls://peer.example:443"}
		configObj.ReleaseMirrors["looped"] = "http://" + ownHost + "/looped"

		err = validate(configObj)
		if err == nil || !strings.Contains(err.Error(), "cannot mirror itself") {
			t.Fatalf("expected self-mirror rejection, got: %v", err)
		}
	})

	t.Run("foreign ygg host and loopback stay valid", func(t *testing.T) {
		pemBytes, _, err := mesh.GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey returned error: %v", err)
		}
		pemPath := filepath.Join(t.TempDir(), "node.pem")
		if err := mesh.WriteKeyFile(pemPath, pemBytes); err != nil {
			t.Fatalf("WriteKeyFile returned error: %v", err)
		}
		_, foreignHost, err := mesh.GenerateKey()
		if err != nil {
			t.Fatalf("GenerateKey returned error: %v", err)
		}

		configObj := newValidConfigObjForTest(t)
		enableWebForTest(configObj)
		configObj.Ygg.PemKey = pemPath
		configObj.ReleaseMirrors["brother"] = "http://" + foreignHost + "/brother"
		configObj.ReleaseMirrors["local"] = "http://127.0.0.1:9999/local"

		if err := validate(configObj); err != nil {
			t.Fatalf("validate returned error: %v", err)
		}
	})
}

func TestValidateRejectsMissingIngress(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	configObj.Ygg.PemKey = ""

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), "at least one ingress") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsReservedMirrorKey(t *testing.T) {
	for _, keyText := range []string{"health", "info", "openapi.json", "logo", "og.png", "favicon.ico", "sitemap.xml"} {
		configObj := newValidConfigObjForTest(t)
		configObj.ReleaseMirrors = map[string]string{
			keyText: "https://example.com/" + keyText,
		}

		err := validate(configObj)
		if err == nil {
			t.Fatalf("key %q: validate returned nil error", keyText)
		}
		if !strings.Contains(err.Error(), `release_mirrors: key "`+keyText+`" is reserved`) {
			t.Fatalf("key %q: unexpected error: %v", keyText, err)
		}
	}
}

func TestValidateRejectsReservedComposerMirrorKey(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	configObj.ReleaseMirrors = map[string]string{
		"packages.json": "https://example.com/packages.json",
	}

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), `release_mirrors: key "packages.json" is reserved`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A vN key would collide with the goproxy major suffix in `/{key}/vN/@v/...`.
func TestValidateRejectsGoMajorShapedMirrorKey(t *testing.T) {
	for _, keyText := range []string{"v2", "v10", "v100"} {
		configObj := newValidConfigObjForTest(t)
		configObj.ReleaseMirrors = map[string]string{
			keyText: "https://example.com/" + keyText,
		}

		err := validate(configObj)
		if err == nil {
			t.Fatalf("key %q: validate returned nil error", keyText)
		}
		if !strings.Contains(err.Error(), `must not look like a Go module major version suffix`) {
			t.Fatalf("key %q: unexpected error: %v", keyText, err)
		}
	}
}

func TestValidateRejectsInvalidMirrorURL(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	configObj.ReleaseMirrors = map[string]string{
		"core-lib": "ftp://example.com/core-lib",
	}

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), "must be an http or https URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsInvalidRoutingPrefix(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	enableWebForTest(configObj)
	configObj.Web.Routing.Prefix = "bad/prefix"

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), `web.routing.prefix invalid: "bad/prefix"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsInvalidRoutingPrefixWithoutWeb(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	configObj.Web.Routing.Prefix = "bad/prefix"

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), `web.routing.prefix invalid: "bad/prefix"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsDomainWithPortSchemeOrPath(t *testing.T) {
	for _, domainText := range []string{"vault.test:8080", "https://vault.test", "vault.test/mirror"} {
		configObj := newValidConfigObjForTest(t)
		enableWebForTest(configObj)
		configObj.Web.Server.Domain = domainText

		err := validate(configObj)
		if err == nil {
			t.Fatalf("domain %q: validate returned nil error", domainText)
		}
		if !strings.Contains(err.Error(), "web.server.domain must be a bare hostname") {
			t.Fatalf("domain %q: unexpected error: %v", domainText, err)
		}
	}
}

func TestValidateRejectsReservedRoutingPrefix(t *testing.T) {
	for _, prefix := range []string{"health", "info", "metrics", "openapi.json", "og", "logo", "og.png", "favicon.ico", "sitemap.xml"} {
		configObj := newValidConfigObjForTest(t)
		enableWebForTest(configObj)
		configObj.Web.Routing.Prefix = prefix

		err := validate(configObj)
		if err == nil {
			t.Fatalf("prefix %q: validate returned nil error", prefix)
		}
		if !strings.Contains(err.Error(), `web.routing.prefix must not be a reserved path: "`+prefix+`"`) {
			t.Fatalf("prefix %q: unexpected error: %v", prefix, err)
		}
	}
}

func TestValidateRejectsStaticCacheOverlap(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	staticDirPath := filepath.Join(t.TempDir(), "static")
	cacheDirPath := filepath.Join(staticDirPath, "cache")

	if err := os.MkdirAll(staticDirPath, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDirPath, "index.html"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	configObj.Web.Static.Dir = staticDirPath
	configObj.Web.Static.IndexFile = "index.html"
	configObj.Storage.Dir = cacheDirPath

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), "storage.dir and web.static.dir must not overlap") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsUnsafeStaticIndexFile(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	staticDirPath := t.TempDir()

	configObj.Web.Static.Dir = staticDirPath
	configObj.Web.Static.IndexFile = "../index.html"

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), "web.static.index_file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsInvalidDenyRule(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	configObj.Web.Static.Deny = []string{"../secrets.txt"}

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), "web.static.deny[0]: rule must not contain '..' or backslash") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAcceptsVictoriaLogsOnly(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	configObj.Logging.Console.Enabled = false
	configObj.Logging.File.Enabled = false
	configObj.Logging.Victorialogs.Enabled = true
	configObj.Logging.Victorialogs.Url = "http://127.0.0.1:9428"

	if err := validate(configObj); err != nil {
		t.Fatalf("validate returned error: %v", err)
	}
}

func TestValidateRejectsHugeVictoriaLogsBatch(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	configObj.Logging.Victorialogs.Enabled = true
	configObj.Logging.Victorialogs.Url = "http://127.0.0.1:9428"
	configObj.Logging.Victorialogs.BatchMaxBytes = stcfg.SizeObj(cMaxVictorialogsBatchBytes + 1)

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), "logging.victorialogs.batch_max_bytes") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRejectsPartialTLSPair(t *testing.T) {
	configObj := newValidConfigObjForTest(t)
	enableWebForTest(configObj)
	configObj.Web.Server.Tls.Cert = "/tmp/cert.pem"
	configObj.Web.Server.Tls.Key = ""

	err := validate(configObj)
	if err == nil {
		t.Fatal("validate returned nil error")
	}
	if !strings.Contains(err.Error(), "web.server.tls.cert and tls.key must both be set or both empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPathHelpers(t *testing.T) {
	parentPath := t.TempDir()
	childPath := filepath.Join(parentPath, "nested")
	otherPath := filepath.Join(t.TempDir(), "other")

	if err := os.MkdirAll(childPath, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	ok, err := util.IsInsideOrEqual(parentPath, childPath)
	if err != nil {
		t.Fatalf("IsInsideOrEqual returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected child path to be inside parent")
	}

	ok, err = util.PathsOverlap(parentPath, otherPath)
	if err != nil {
		t.Fatalf("PathsOverlap returned error: %v", err)
	}
	if ok {
		t.Fatal("expected separate paths to not overlap")
	}
}
