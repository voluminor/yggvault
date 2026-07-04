package core

import (
	"testing"

	"github.com/zeebo/blake3"
)

// // // // // // // // // //

func TestHashSizeUsesBLAKE3XOF24(t *testing.T) {
	if HashSize != 24 {
		t.Fatalf("HashSize=%d, want 24", HashSize)
	}

	dataArr := []byte("storage hash boundary")
	hasherObj := blake3.New()
	_, _ = hasherObj.Write(dataArr)
	var expectedObj HashObj
	_, _ = hasherObj.Digest().Read(expectedObj[:])

	actualObj := HashBytes(dataArr)
	if actualObj != expectedObj {
		t.Fatal("HashBytes does not use 24-byte BLAKE3 XOF output")
	}
	if len(actualObj.Hex()) != HashSize*2 {
		t.Fatalf("hex length=%d, want %d", len(actualObj.Hex()), HashSize*2)
	}

	fromBytesObj, err := HashFromBytes(actualObj[:])
	if err != nil {
		t.Fatalf("HashFromBytes rejected storage hash: %v", err)
	}
	if fromBytesObj != actualObj {
		t.Fatal("HashFromBytes changed storage hash")
	}

	if _, err = HashFromBytes(make([]byte, HashSize+8)); err == nil {
		t.Fatal("HashFromBytes accepted wrong-size hash")
	}
}
