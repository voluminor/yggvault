package config

import (
	"regexp"
)

// // // // // // // // // //

// VictoriaLogs sink abuse caps: queue buffer in lines and batch size in bytes.
const cMaxVictorialogsBufferLines = 1000000

const cMaxVictorialogsBatchBytes = 64 * 1000 * 1000

// //

var keyPatternObj = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,38}[a-z0-9]$`)

// A vN key is indistinguishable from a Go module major suffix in goproxy paths (`.../{key}/vN/@v/...`).
var goMajorKeyPatternObj = regexp.MustCompile(`^v[0-9]+$`)

var historyPrefixPatternObj = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)

var routingPrefixPatternObj = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

var reservedKeySetObj = map[string]bool{
	"list":          true,
	"latest":        true,
	"@v":            true,
	"@latest":       true,
	"health":        true,
	"info":          true,
	"metrics":       true,
	"openapi.json":  true,
	"p":             true,
	"p2":            true,
	"packages":      true,
	"packages.json": true,
	"rpc":           true,
	// Global literals; otherwise a key could shadow its own bare HTML page at /<key>.
	"catalog.json": true,
	"feed.xml":     true,
	"logo":         true,
	"og.png":       true,
	"favicon.ico":  true,
	"sitemap.xml":  true,
}
