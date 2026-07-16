package overlay

import (
	"encoding/base64"
	"fmt"
)

// // // // // // // // // //

// BazelSnippet returns http_archive over universal.tar.gz with sha256 integrity and top-dir strip_prefix.
func BazelSnippet(name string, url string, sha256Arr []byte, stripPrefix string) string {
	integrity := "sha256-" + base64.StdEncoding.EncodeToString(sha256Arr)
	return fmt.Sprintf(
		"http_archive(\n    name = %q,\n    urls = [%q],\n    integrity = %q,\n    strip_prefix = %q,\n)\n",
		name, url, integrity, stripPrefix,
	)
}

// ZigSnippet returns build.zig.zon.url; the client computes the hash through `zig fetch`.
func ZigSnippet(url string) string {
	return fmt.Sprintf(".url = %q,\n", url)
}

// GoInstallSnippet returns a Go-module consumption recipe: GOPROXY for our entry host, private mirror sumdb settings,
// optional GOINSECURE for http/ygg, and `go get`.
func GoInstallSnippet(modulePath string, version string, proxyScheme string, host string) string {
	envText := fmt.Sprintf("GOPROXY=%s://%s GOSUMDB=off", proxyScheme, host)
	if proxyScheme != "https" {
		envText += fmt.Sprintf(" GOINSECURE=%s/*", host)
	}
	return fmt.Sprintf("%s go get %s@%s\n", envText, modulePath, version)
}

// ComposerRequireSnippet returns a mirror install recipe. The mirror has priority for its packages,
// transitive dependencies stay on Packagist, and http/ygg entries disable secure-http.
// basePath is the nested route base ("/pkg") or empty in root mode: Composer resolves
// packages.json under the repository URL, so the URL must carry the prefix.
func ComposerRequireSnippet(name string, version string, scheme string, host string, basePath string) string {
	prefix := ""
	if scheme != "https" {
		prefix = "composer config secure-http false\n"
	}
	return fmt.Sprintf("%scomposer config repositories.yggvault composer %s://%s%s\ncomposer require %s:%s\n",
		prefix, scheme, host, basePath, name, version)
}

// TargetModulePath returns the served Go module path for snippets and pages.
func (obj *Obj) TargetModulePath(key string, version string, listenerCtxObj ListenerCtxObj) string {
	return obj.targetModulePath(key, version, listenerCtxObj)
}
