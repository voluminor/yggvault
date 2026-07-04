package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// // // // // // // // // //

func TestAccessWriterTracksImplicitStatusAndBytes(t *testing.T) {
	recorderObj := httptest.NewRecorder()
	writerObj := newAccessWriter(recorderObj, true)

	n, err := writerObj.Write([]byte("body"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != 4 {
		t.Fatalf("Write bytes=%d, want 4", n)
	}
	if writerObj.status() != http.StatusOK {
		t.Fatalf("status=%d, want 200", writerObj.status())
	}
	if writerObj.bytes != 4 {
		t.Fatalf("bytes=%d, want 4", writerObj.bytes)
	}
}

func TestAccessWriterTracksExplicitStatus(t *testing.T) {
	recorderObj := httptest.NewRecorder()
	writerObj := newAccessWriter(recorderObj, true)

	writerObj.WriteHeader(http.StatusTooManyRequests)
	_, _ = writerObj.Write([]byte("limited"))

	if writerObj.status() != http.StatusTooManyRequests {
		t.Fatalf("status=%d, want 429", writerObj.status())
	}
	if writerObj.bytes != int64(len("limited")) {
		t.Fatalf("bytes=%d, want %d", writerObj.bytes, len("limited"))
	}
}

func TestAccessWriterSkipsHeadBodyBytes(t *testing.T) {
	recorderObj := httptest.NewRecorder()
	writerObj := newAccessWriter(recorderObj, false)

	n, err := writerObj.Write([]byte("head body"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != len("head body") {
		t.Fatalf("Write bytes=%d, want %d", n, len("head body"))
	}
	if writerObj.status() != http.StatusOK {
		t.Fatalf("status=%d, want 200", writerObj.status())
	}
	if writerObj.bytes != 0 {
		t.Fatalf("bytes=%d, want 0", writerObj.bytes)
	}
}

func TestAccessWriterKeepsFinalStatusAfterInformationalHeader(t *testing.T) {
	recorderObj := httptest.NewRecorder()
	writerObj := newAccessWriter(recorderObj, true)

	writerObj.WriteHeader(http.StatusEarlyHints)
	writerObj.WriteHeader(http.StatusCreated)

	if writerObj.status() != http.StatusCreated {
		t.Fatalf("status=%d, want 201", writerObj.status())
	}
}

func TestAccessWriterIgnoresRepeatedFinalStatus(t *testing.T) {
	recorderObj := httptest.NewRecorder()
	writerObj := newAccessWriter(recorderObj, true)

	writerObj.WriteHeader(http.StatusAccepted)
	writerObj.WriteHeader(http.StatusInternalServerError)

	if writerObj.status() != http.StatusAccepted {
		t.Fatalf("status=%d, want 202", writerObj.status())
	}
}

func TestAccessWriterFlushCommitsOKStatus(t *testing.T) {
	recorderObj := httptest.NewRecorder()
	writerObj := newAccessWriter(recorderObj, true)

	writerObj.Flush()
	writerObj.WriteHeader(http.StatusInternalServerError)

	if writerObj.status() != http.StatusOK {
		t.Fatalf("status=%d, want 200", writerObj.status())
	}
}
