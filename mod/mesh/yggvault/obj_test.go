package yggvault

import (
	"testing"

	"github.com/voluminor/yggvault/target"
)

// // // // // // // // // //

func TestNewParams(t *testing.T) {
	block, ok := New().Params()[sigName].(map[string]any)
	if !ok {
		t.Fatal("Params missing sigil block")
	}
	if block[cKeyVersion] != target.Version || block[cKeyHash] != target.Hash || block[cKeyDate] != target.DateUpdate {
		t.Fatalf("sigil block != target build meta: %+v", block)
	}
}

func TestMatchAndParseRoundTrip(t *testing.T) {
	nodeInfo, err := New().SetParams(map[string]any{})
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
	if parsed.Version() != target.Version || parsed.Hash() != target.Hash || parsed.Date() != target.DateUpdate {
		t.Fatalf("ParseParams roundtrip mismatch: %+v", parsed)
	}
}
