package source

import (
	"errors"
	"testing"
	"time"
)

// // // // // // // // // //

// TestStallReaderThroughputFloor: a drip feed below the floor cancels the download; a saturated window does not.
func TestStallReaderThroughputFloor(t *testing.T) {
	var cancelled error
	newReader := func() *stallReaderObj {
		return &stallReaderObj{
			minRate: cMinDownloadBytesPerSec,
			window:  cDownloadRateWindow,
			cancel:  func(cause error) { cancelled = cause },
		}
	}

	healthy := newReader()
	healthy.windowStart = time.Now().Add(-cDownloadRateWindow - time.Second)
	healthy.windowBytes = int64(cDownloadRateWindow.Seconds()) * cMinDownloadBytesPerSec * 2
	healthy.checkThroughput(0)
	if cancelled != nil {
		t.Fatalf("healthy window must not cancel, got %v", cancelled)
	}
	if healthy.windowBytes != 0 {
		t.Fatalf("healthy window must reset counter, got %d", healthy.windowBytes)
	}

	cancelled = nil
	starved := newReader()
	starved.windowStart = time.Now().Add(-cDownloadRateWindow - time.Second)
	starved.windowBytes = 1
	starved.checkThroughput(0)
	if !errors.Is(cancelled, errDownloadStalled) {
		t.Fatalf("starved window must cancel with errDownloadStalled, got %v", cancelled)
	}
}
