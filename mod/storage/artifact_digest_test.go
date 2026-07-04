package storage

import (
	"context"
	"io"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

type fixedBytesBuilderObj struct {
	dataArr []byte
}

func (b fixedBytesBuilderObj) Build(_ context.Context, writerObj io.Writer) error {
	_, err := writerObj.Write(b.dataArr)
	return err
}

func TestArtifactDigest(t *testing.T) {
	configObj := newTestConfigObj(t)
	obj := newTestObj(t, configObj)

	builderObj := fixedBytesBuilderObj{dataArr: []byte("deterministic artifact bytes")}
	d1, err := obj.ArtifactDigest(context.Background(), builderObj)
	if err != nil {
		t.Fatalf("ArtifactDigest 1: %v", err)
	}
	d2, err := obj.ArtifactDigest(context.Background(), builderObj)
	if err != nil {
		t.Fatalf("ArtifactDigest 2: %v", err)
	}

	if d1.BodyHash != d2.BodyHash || d1.SizeBytes != d2.SizeBytes || d1.ETag != d2.ETag {
		t.Fatalf("digest not stable: %+v vs %+v", d1, d2)
	}
	if d1.SizeBytes != uint64(len(builderObj.dataArr)) {
		t.Fatalf("size=%d, want %d", d1.SizeBytes, len(builderObj.dataArr))
	}
	if len(d1.BodySha256) != 32 || len(d1.BodySha1) != 20 {
		t.Fatalf("unexpected digest lengths: sha256=%d sha1=%d", len(d1.BodySha256), len(d1.BodySha1))
	}
	if d1.ETag != `"`+d1.BodyHash.Hex()+`"` {
		t.Fatalf("etag mismatch: %s", d1.ETag)
	}
	if d1.BodyHash != core.HashBytes(builderObj.dataArr) {
		t.Fatalf("body hash != blake3-24 of bytes")
	}
}
