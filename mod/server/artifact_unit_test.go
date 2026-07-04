package server

import (
	"testing"

	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

func TestDecideRange(t *testing.T) {
	const size = int64(8)
	const etag = `"e"`
	set := api.NewOptString
	unset := api.OptString{}

	cases := []struct {
		name        string
		rangeHeader api.OptString
		ifRange     api.OptString
		wantPartial bool
		wantSatisf  bool
		wantStart   int64
		wantLength  int64
		wantCRange  string
	}{
		{"no-range", unset, unset, false, true, 0, 8, ""},
		{"empty-range", set("  "), unset, false, true, 0, 8, ""},
		{"first-4", set("bytes=0-3"), unset, true, true, 0, 4, "bytes 0-3/8"},
		{"open", set("bytes=4-"), unset, true, true, 4, 4, "bytes 4-7/8"},
		{"suffix", set("bytes=-3"), unset, true, true, 5, 3, "bytes 5-7/8"},
		{"end-clamp", set("bytes=5-100"), unset, true, true, 5, 3, "bytes 5-7/8"},
		{"suffix-overflow", set("bytes=-100"), unset, true, true, 0, 8, "bytes 0-7/8"},
		{"unsatisfiable", set("bytes=100-200"), unset, false, false, 0, 0, "bytes */8"},
		{"multi-range-full", set("bytes=0-3,5-7"), unset, false, true, 0, 8, ""},
		{"malformed-full", set("bytes=abc"), unset, false, true, 0, 8, ""},
		{"wrong-unit-full", set("items=0-3"), unset, false, true, 0, 8, ""},
		{"suffix-zero-full", set("bytes=-0"), unset, false, true, 0, 8, ""},
		{"reversed-full", set("bytes=5-2"), unset, false, true, 0, 8, ""},
		{"ifrange-mismatch-full", set("bytes=0-3"), set(`"other"`), false, true, 0, 8, ""},
		{"ifrange-match-partial", set("bytes=0-3"), set(etag), true, true, 0, 4, "bytes 0-3/8"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decideRange(c.rangeHeader, c.ifRange, etag, size)
			if got.partial != c.wantPartial || got.satisfiable != c.wantSatisf {
				t.Fatalf("partial=%v satisfiable=%v want %v/%v", got.partial, got.satisfiable, c.wantPartial, c.wantSatisf)
			}
			if got.partial && (got.start != c.wantStart || got.length != c.wantLength || got.contentRange != c.wantCRange) {
				t.Fatalf("start=%d length=%d cr=%q want %d/%d/%q", got.start, got.length, got.contentRange, c.wantStart, c.wantLength, c.wantCRange)
			}
			if !got.satisfiable && got.contentRange != c.wantCRange {
				t.Fatalf("416 contentRange=%q want %q", got.contentRange, c.wantCRange)
			}
		})
	}
}
