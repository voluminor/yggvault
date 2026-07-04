package link

import (
	"testing"
)

// // // // // // // // // //

func TestKeyNestedVsRoot(t *testing.T) {
	nestedObj := Obj{RoutePrefix: "pkg"}
	if got := nestedObj.Key("lib/x", ""); got != "/pkg/lib/x" {
		t.Fatalf("nested Key: want /pkg/lib/x, got %q", got)
	}
	if got := nestedObj.Key("lib/x", "/releases.json"); got != "/pkg/lib/x/releases.json" {
		t.Fatalf("nested Key with suffix: want /pkg/lib/x/releases.json, got %q", got)
	}

	rootObj := Obj{}
	if got := rootObj.Key("lib/x", ""); got != "/lib/x" {
		t.Fatalf("root Key: want /lib/x, got %q", got)
	}
	if got := rootObj.Key("lib/x", "/v.json"); got != "/lib/x/v.json" {
		t.Fatalf("root Key with suffix: want /lib/x/v.json, got %q", got)
	}
}

func TestKeyEmptyKeyHomePath(t *testing.T) {
	if got := (Obj{}).Key("", ""); got != "/" {
		t.Fatalf("root home: want /, got %q", got)
	}
	if got := (Obj{RoutePrefix: "pkg"}).Key("", ""); got != "/pkg/" {
		t.Fatalf("nested home: want /pkg/, got %q", got)
	}
}

// // // // // // // // // //

func TestAbsAbsoluteWhenEntryHost(t *testing.T) {
	webObj := Obj{Scheme: "https", EntryHost: "vault.test"}
	if got := webObj.Abs("/p"); got != "https://vault.test/p" {
		t.Fatalf("https Abs: want https://vault.test/p, got %q", got)
	}

	yggObj := Obj{Scheme: "http", EntryHost: "[200:abcd::1]"}
	if got := yggObj.Abs("/x.tar.gz"); got != "http://[200:abcd::1]/x.tar.gz" {
		t.Fatalf("http(ygg) Abs: want http://[200:abcd::1]/x.tar.gz, got %q", got)
	}
}

func TestAbsRelativeWhenNoEntryHost(t *testing.T) {
	relObj := Obj{Scheme: "https"}
	if got := relObj.Abs("/p"); got != "/p" {
		t.Fatalf("relative Abs: want /p, got %q", got)
	}
	if got := relObj.Abs("/"); got != "/" {
		t.Fatalf("relative Abs root: want /, got %q", got)
	}
}
