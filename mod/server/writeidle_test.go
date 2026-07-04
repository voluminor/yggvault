package server

import (
	"net/http/httptest"
	"testing"
	"time"
)

// // // // // // // // // //

func TestWriteIdleWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newWriteIdleWriter(rec, time.Second)

	if w.Unwrap() != rec {
		t.Fatal("Unwrap must return the inner writer")
	}
	w.WriteHeader(206)
	if _, err := w.Write([]byte("abc")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if rec.Code != 206 || rec.Body.String() != "abc" {
		t.Fatalf("code=%d body=%q want 206/abc", rec.Code, rec.Body.String())
	}
}
