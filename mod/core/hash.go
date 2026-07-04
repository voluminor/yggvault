package core

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/zeebo/blake3"
)

// // // // // // // // // //

// HashObj is a fixed-size truncated BLAKE3 digest; it is comparable and usable as a map key.
type HashObj [HashSize]byte

// //

// HashBytes returns the BLAKE3 digest of an arbitrary buffer.
func HashBytes(dataArr []byte) HashObj {
	hasherObj := blake3.New()
	_, _ = hasherObj.Write(dataArr)
	return HashFromHasher(hasherObj)
}

// HashFromBytes restores HashObj from raw bytes and requires exactly HashSize bytes.
func HashFromBytes(dataArr []byte) (HashObj, error) {
	var hashObj HashObj
	if len(dataArr) != HashSize {
		return hashObj, fmt.Errorf("hash must be exactly %d bytes", HashSize)
	}
	copy(hashObj[:], dataArr)
	return hashObj, nil
}

// VerifyHash checks integrity by comparing the buffer digest with the expected hash.
func VerifyHash(expectedObj HashObj, dataArr []byte) error {
	actualObj := HashBytes(dataArr)
	if actualObj != expectedObj {
		return errors.New("content hash mismatch")
	}
	return nil
}

// HashFromHasher extracts the truncated digest from an already-filled streaming hasher.
func HashFromHasher(hasherObj *blake3.Hasher) HashObj {
	var hashObj HashObj
	_, _ = hasherObj.Digest().Read(hashObj[:])
	return hashObj
}

// //

// Hex returns the digest as hex.
func (obj HashObj) Hex() string {
	return hex.EncodeToString(obj[:])
}

// BytesCopy returns an independent copy of digest bytes.
func (obj HashObj) BytesCopy() []byte {
	return append([]byte(nil), obj[:]...)
}

// IsZero reports whether the digest is empty and uninitialized.
func (obj HashObj) IsZero() bool {
	var emptyObj HashObj
	return obj == emptyObj
}
