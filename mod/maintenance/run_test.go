package maintenance

import (
	"errors"
	"testing"
)

// // // // // // // // // //

func TestIsStorageLockErrorFallbackText(t *testing.T) {
	err := errors.New("open index: resource temporarily unavailable")
	if !isStorageLockError(err) {
		t.Fatal("resource temporarily unavailable fallback was not recognized")
	}
}
