package storage

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"io"

	"github.com/zeebo/blake3"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

type cappedDigestWriterObj struct {
	ctx      context.Context
	inner    io.Writer
	written  uint64
	maxBytes uint64
}

// Write checks context cancellation and output cap on every call; cap overflow returns errArtifactOversize.
func (w *cappedDigestWriterObj) Write(dataArr []byte) (int, error) {
	select {
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	default:
	}
	attemptedBytes, overflowFlag := saturatingAddUint64(w.written, uint64(len(dataArr)))
	if overflowFlag || attemptedBytes > w.maxBytes {
		return 0, errArtifactOversize
	}
	n, err := w.inner.Write(dataArr)
	w.written += uint64(n)
	return n, err
}

func etagFromHash(bodyHashObj core.HashObj) string {
	return `"` + bodyHashObj.Hex() + `"`
}

// // // // // // // // // //

// ArtifactDigest runs a builder once and computes artifact identity: blake3-24, sha256, sha1, size, and ETag.
// It uses the same hashing scheme as hot-build serving, so body_hash matches on rebuild.
// Storage owns artifact hashing; overlay provides the builder and rescan calls this before RegisterArtifact.
func (obj *Obj) ArtifactDigest(ctx context.Context, builderObj ArtifactBuilderInterface) (core.ArtifactDigestObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.ArtifactDigestObj{}, err
	}
	defer releaseFunc()

	if err := obj.acquireBuildSlot(ctx); err != nil {
		return core.ArtifactDigestObj{}, err
	}
	defer obj.releaseBuildSlot()

	blake3Obj := blake3.New()
	sha256Obj := sha256.New()
	sha1Obj := sha1.New()
	writerObj := &cappedDigestWriterObj{ctx: ctx, inner: io.MultiWriter(blake3Obj, sha256Obj, sha1Obj), maxBytes: obj.maxArtifactBytes()}

	if err := builderObj.Build(ctx, writerObj); err != nil {
		return core.ArtifactDigestObj{}, err
	}

	bodyHashObj := core.HashFromHasher(blake3Obj)
	return core.ArtifactDigestObj{
		BodyHash:   bodyHashObj,
		BodySha256: sha256Obj.Sum(nil),
		BodySha1:   sha1Obj.Sum(nil),
		SizeBytes:  writerObj.written,
		ETag:       etagFromHash(bodyHashObj),
	}, nil
}
