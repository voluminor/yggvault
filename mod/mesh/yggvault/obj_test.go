package yggvault

import (
	"strings"
	"testing"
)

// // // // // // // // // //

func TestNewParams(t *testing.T) {
	obj, err := New("v1.2.3", "build-hash", "2026-07-09")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	block, ok := obj.Params()[sigName].(map[string]any)
	if !ok {
		t.Fatal("Params missing sigil block")
	}
	if block[cKeyVersion] != "v1.2.3" || block[cKeyHash] != "build-hash" || block[cKeyDate] != "2026-07-09" {
		t.Fatalf("sigil block mismatch: %+v", block)
	}
}

func TestMatchAndParseRoundTrip(t *testing.T) {
	obj, err := New("v1.2.3", "build-hash", "2026-07-09")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	nodeInfo, err := obj.SetParams(map[string]any{})
	if err != nil {
		t.Fatalf("SetParams returned error: %v", err)
	}
	if !Match(nodeInfo) {
		t.Fatal("Match false on own SetParams output")
	}
	if Match(map[string]any{}) {
		t.Fatal("Match true on empty NodeInfo")
	}

	parsed := &Obj{}
	parsed.ParseParams(nodeInfo)
	if parsed.Version() != "v1.2.3" || parsed.Hash() != "build-hash" || parsed.Date() != "2026-07-09" {
		t.Fatalf("ParseParams roundtrip mismatch: %+v", parsed)
	}
	parsedObj, err := Parse(nodeInfo)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsedObj.Version() != parsed.Version() || parsedObj.Hash() != parsed.Hash() || parsedObj.Date() != parsed.Date() {
		t.Fatalf("Parse mismatch: %+v vs %+v", parsedObj, parsed)
	}
}

func TestNewRejectsInvalidFields(t *testing.T) {
	cases := []struct {
		name    string
		version string
		hash    string
		date    string
	}{
		{name: "empty-version", version: "", hash: "h", date: "d"},
		{name: "long-version", version: strings.Repeat("v", cMaxValueBytes+1), hash: "h", date: "d"},
		{name: "long-hash", version: "v1", hash: strings.Repeat("h", cMaxValueBytes+1), date: "d"},
		{name: "long-date", version: "v1", hash: "h", date: strings.Repeat("d", cMaxValueBytes+1)},
	}
	for _, caseObj := range cases {
		if _, err := New(caseObj.version, caseObj.hash, caseObj.date); err == nil {
			t.Fatalf("%s: expected error", caseObj.name)
		}
	}
}

func TestParseRejectsHostileNodeInfo(t *testing.T) {
	cases := []struct {
		name string
		info map[string]any
	}{
		{name: "missing", info: map[string]any{}},
		{name: "not-map", info: map[string]any{sigName: "bad"}},
		{name: "version-not-string", info: map[string]any{sigName: map[string]any{cKeyVersion: 1, cKeyHash: "h", cKeyDate: "d"}}},
		{name: "hash-not-string", info: map[string]any{sigName: map[string]any{cKeyVersion: "v1", cKeyHash: 1, cKeyDate: "d"}}},
		{name: "date-not-string", info: map[string]any{sigName: map[string]any{cKeyVersion: "v1", cKeyHash: "h", cKeyDate: 1}}},
		{name: "empty-version", info: map[string]any{sigName: map[string]any{cKeyVersion: "", cKeyHash: "h", cKeyDate: "d"}}},
		{name: "long-version", info: map[string]any{sigName: map[string]any{cKeyVersion: strings.Repeat("v", cMaxValueBytes+1), cKeyHash: "h", cKeyDate: "d"}}},
	}
	for _, caseObj := range cases {
		if _, err := Parse(caseObj.info); err == nil {
			t.Fatalf("%s: expected error", caseObj.name)
		}
	}
}
