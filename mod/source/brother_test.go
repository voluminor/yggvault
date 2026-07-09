package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func TestDiscoverClassifiesGitWhenNoMarker(t *testing.T) {
	ts := startFakeBrother(t, &fakeBrotherObj{}, false)
	obj := newTestObj(t, testConfigObj(t))

	res, err := obj.Discover(context.Background(), "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if res.Class != stcode.SourceClassGit || res.RemoteKey != "core-lib" {
		t.Fatalf("expected git classification, got class=%v key=%q", res.Class, res.RemoteKey)
	}
}

func TestDiscoverClassifiesBrother(t *testing.T) {
	fake := &fakeBrotherObj{helloReply: brotherwire.HelloReplyObj{Protocol: brotherwire.Protocol}}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, testConfigObj(t))

	res, err := obj.Discover(context.Background(), "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if res.Class != stcode.SourceClassBrother || res.BrotherURL != ts.URL {
		t.Fatalf("expected brother classification, got class=%v brotherURL=%q", res.Class, res.BrotherURL)
	}
}

func TestDiscoverProtocolMismatchNotConfirmed(t *testing.T) {
	fake := &fakeBrotherObj{helloReply: brotherwire.HelloReplyObj{Protocol: "wrong"}}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, testConfigObj(t))

	_, err := obj.Discover(context.Background(), "core-lib", ts.URL)
	var typedErr *stcode.ErrBrotherContractNotConfirmedObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("expected ErrBrotherContractNotConfirmed, got %v", err)
	}
}

// // // // // // // // // //

func TestBlobsFetchVerifiesAndWrites(t *testing.T) {
	value := []byte("hello brother world")
	hashObj := core.HashBytes(value)
	fake := &fakeBrotherObj{
		helloReply: brotherwire.HelloReplyObj{Protocol: brotherwire.Protocol},
		blobs:      map[brotherwire.HashWire][]byte{toWire(hashObj): value},
	}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, testConfigObj(t))

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer func() { _ = session.Close() }()

	destDir := t.TempDir()
	res, err := session.BlobsFetch(context.Background(), "v1.0.0", []BlobReqObj{{Hash: hashObj, SizeBytes: uint64(len(value))}}, destDir)
	if err != nil {
		t.Fatalf("BlobsFetch returned error: %v", err)
	}
	if len(res.Blobs) != 1 || res.Blobs[0].BlobHash != hashObj {
		t.Fatalf("unexpected blobs result: %+v", res.Blobs)
	}
	got, err := os.ReadFile(filepath.Join(destDir, hashObj.Hex()+".blob"))
	if err != nil || string(got) != string(value) {
		t.Fatalf("blob file mismatch: got=%q err=%v", got, err)
	}
}

func TestHelloNegotiatesFetchLimits(t *testing.T) {
	configObj := testConfigObj(t)
	configObj.Brother.Rpc.MaxFetchResponseBytes = 128
	configObj.Brother.Rpc.MaxFetchBatchCount = 10
	fake := &fakeBrotherObj{helloReply: brotherwire.HelloReplyObj{
		Protocol:              brotherwire.Protocol,
		MaxFetchResponseBytes: 64,
		MaxFetchBatchCount:    4,
	}}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, configObj)

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer func() { _ = session.Close() }()

	helloObj, err := session.Hello(context.Background())
	if err != nil {
		t.Fatalf("Hello returned error: %v", err)
	}
	if helloObj.MaxFetchResponseBytes != 64 || helloObj.MaxFetchBatchCount != 4 {
		t.Fatalf("negotiated limits=%d/%d want 64/4", helloObj.MaxFetchResponseBytes, helloObj.MaxFetchBatchCount)
	}
	if bytesObj, countObj := session.FetchLimits(); bytesObj != 64 || countObj != 4 {
		t.Fatalf("session limits=%d/%d want 64/4", bytesObj, countObj)
	}
}

func TestBlobsFetchRejectsNegotiatedBatchCount(t *testing.T) {
	configObj := testConfigObj(t)
	configObj.Brother.Rpc.MaxFetchBatchCount = 10
	fake := &fakeBrotherObj{helloReply: brotherwire.HelloReplyObj{
		Protocol:              brotherwire.Protocol,
		MaxFetchResponseBytes: 1024,
		MaxFetchBatchCount:    1,
	}}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, configObj)

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer session.Close()
	if _, err := session.Hello(context.Background()); err != nil {
		t.Fatalf("Hello returned error: %v", err)
	}

	reqArr := []BlobReqObj{
		{Hash: core.HashBytes([]byte("a")), SizeBytes: 1},
		{Hash: core.HashBytes([]byte("b")), SizeBytes: 1},
	}
	if _, err := session.BlobsFetch(context.Background(), "v1.0.0", reqArr, t.TempDir()); err == nil {
		t.Fatal("expected batch-count error")
	}
}

func TestWriteBlobFileReplacesExistingFile(t *testing.T) {
	pathToFile := filepath.Join(t.TempDir(), "blob.data")
	if err := os.WriteFile(pathToFile, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := writeBlobFile(pathToFile, []byte("new")); err != nil {
		t.Fatalf("writeBlobFile returned error: %v", err)
	}
	dataArr, err := os.ReadFile(pathToFile)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(dataArr) != "new" {
		t.Fatalf("file content=%q, want new", dataArr)
	}
}

func TestBlobsFetchRejectsHashMismatch(t *testing.T) {
	value := []byte("genuine content")
	hashObj := core.HashBytes(value)
	fake := &fakeBrotherObj{
		helloReply: brotherwire.HelloReplyObj{Protocol: brotherwire.Protocol},
		fetchFunc: func(arg brotherwire.BlobsFetchArgObj, reply *brotherwire.BlobsFetchReplyObj) error {
			reply.Blobs = append(reply.Blobs, brotherwire.BlobObj{Hash: arg.Hashes[0], Value: []byte("tampered")})
			return nil
		},
	}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, testConfigObj(t))

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer session.Close()

	_, err = session.BlobsFetch(context.Background(), "v1.0.0", []BlobReqObj{{Hash: hashObj, SizeBytes: uint64(len(value))}}, t.TempDir())
	var typedErr *stcode.ErrBrotherBlobHashMismatchObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("expected ErrBrotherBlobHashMismatch, got %v", err)
	}
}

func TestBlobsFetchRejectsOversizeBlob(t *testing.T) {
	value := []byte("0123456789abcdef")
	hashObj := core.HashBytes(value)
	configObj := testConfigObj(t)
	configObj.Storage.ArchiveLimits.Size.PerFile = 4
	fake := &fakeBrotherObj{
		helloReply: brotherwire.HelloReplyObj{Protocol: brotherwire.Protocol},
		blobs:      map[brotherwire.HashWire][]byte{toWire(hashObj): value},
	}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, configObj)

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer session.Close()

	_, err = session.BlobsFetch(context.Background(), "v1.0.0", []BlobReqObj{{Hash: hashObj, SizeBytes: uint64(len(value))}}, t.TempDir())
	var typedErr *stcode.ErrBrotherRpcInvalidObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("expected ErrBrotherRpcInvalid, got %v", err)
	}
}

func TestBlobsFetchDropsUnrequested(t *testing.T) {
	requested := []byte("requested blob")
	reqHash := core.HashBytes(requested)
	rogue := []byte("rogue blob")
	rogueHash := core.HashBytes(rogue)
	fake := &fakeBrotherObj{
		helloReply: brotherwire.HelloReplyObj{Protocol: brotherwire.Protocol},
		fetchFunc: func(_ brotherwire.BlobsFetchArgObj, reply *brotherwire.BlobsFetchReplyObj) error {
			reply.Blobs = append(reply.Blobs, brotherwire.BlobObj{Hash: toWire(rogueHash), Value: rogue})
			return nil
		},
	}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, testConfigObj(t))

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer session.Close()

	res, err := session.BlobsFetch(context.Background(), "v1.0.0", []BlobReqObj{{Hash: reqHash, SizeBytes: uint64(len(rogue))}}, t.TempDir())
	if err != nil {
		t.Fatalf("BlobsFetch returned error: %v", err)
	}
	if len(res.Blobs) != 0 {
		t.Fatalf("unrequested blob should be dropped, got %d blobs", len(res.Blobs))
	}
}

func TestVersionRejectsTreeHashMismatch(t *testing.T) {
	fake := &fakeBrotherObj{
		helloReply: brotherwire.HelloReplyObj{Protocol: brotherwire.Protocol},
		versionReply: brotherwire.VersionReplyObj{
			TreeHash:  toWire(core.HashBytes([]byte("declared"))),
			TreeBytes: []byte("actual different bytes"),
		},
	}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, testConfigObj(t))

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer session.Close()

	_, err = session.Version(context.Background(), "v1.0.0")
	var typedErr *stcode.ErrBrotherRpcInvalidObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("expected ErrBrotherRpcInvalid for tree hash mismatch, got %v", err)
	}
}

func TestIndexReturnsEntriesWithNotes(t *testing.T) {
	fake := &fakeBrotherObj{
		helloReply: brotherwire.HelloReplyObj{Protocol: brotherwire.Protocol},
		indexReply: brotherwire.IndexReplyObj{
			Entries:  []brotherwire.IndexEntryObj{{Version: "v1.0.0", BodyMD: "release notes"}},
			NextPage: 0,
			Source:   brotherwire.SourceInfoObj{SourceURL: "https://upstream.example/core-lib/"},
		},
	}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, testConfigObj(t))

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer session.Close()

	entries, next, srcInfo, err := session.Index(context.Background(), 1)
	if err != nil {
		t.Fatalf("Index returned error: %v", err)
	}
	if next != 0 || len(entries) != 1 || entries[0].Version != "v1.0.0" || entries[0].ReleaseNotes != "release notes" {
		t.Fatalf("unexpected index result: %+v next=%d", entries, next)
	}
	if srcInfo.SourceURL != "https://upstream.example/core-lib/" {
		t.Fatalf("unexpected source info: %+v", srcInfo)
	}
}

func TestIndexUsesKeysetCursorWhenAdvertised(t *testing.T) {
	callValue := 0
	fake := &fakeBrotherObj{
		helloReply: brotherwire.HelloReplyObj{Protocol: brotherwire.Protocol, IndexKeyset: true},
		indexFunc: func(_ brotherwire.IndexArgObj, reply *brotherwire.IndexReplyObj) error {
			callValue++
			switch callValue {
			case 1:
				reply.Entries = []brotherwire.IndexEntryObj{{Version: "v2.0.0", UpstreamSeq: 2}}
				reply.NextPage = 2
			default:
				reply.Entries = []brotherwire.IndexEntryObj{{Version: "v1.0.0", UpstreamSeq: 1}}
			}
			return nil
		},
	}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, testConfigObj(t))

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer func() { _ = session.Close() }()
	if _, err = session.Hello(context.Background()); err != nil {
		t.Fatalf("Hello returned error: %v", err)
	}

	_, next, _, err := session.Index(context.Background(), 1)
	if err != nil || next != 2 {
		t.Fatalf("first Index next=%d err=%v", next, err)
	}
	_, next, _, err = session.Index(context.Background(), next)
	if err != nil || next != 0 {
		t.Fatalf("second Index next=%d err=%v", next, err)
	}
	if len(fake.indexArgs) != 2 {
		t.Fatalf("index args=%d want 2", len(fake.indexArgs))
	}
	if fake.indexArgs[0].AfterVersion != "" || fake.indexArgs[0].AfterSeq != 0 {
		t.Fatalf("first page must not carry cursor: %+v", fake.indexArgs[0])
	}
	if fake.indexArgs[1].AfterVersion != "v2.0.0" || fake.indexArgs[1].AfterSeq != 2 {
		t.Fatalf("second page cursor=%+v", fake.indexArgs[1])
	}
}

func TestIndexRejectsNonMonotonicNextPage(t *testing.T) {
	fake := &fakeBrotherObj{
		helloReply: brotherwire.HelloReplyObj{Protocol: brotherwire.Protocol},
		indexReply: brotherwire.IndexReplyObj{NextPage: 1},
	}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, testConfigObj(t))

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer session.Close()

	_, _, _, err = session.Index(context.Background(), 1)
	var typedErr *stcode.ErrBrotherRpcInvalidObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("expected ErrBrotherRpcInvalid for non-monotonic next page, got %v", err)
	}
}

func TestSessionHealthyAfterInvariantError(t *testing.T) {
	fake := &fakeBrotherObj{
		helloReply: brotherwire.HelloReplyObj{Protocol: brotherwire.Protocol},
		versionReply: brotherwire.VersionReplyObj{
			TreeHash:  toWire(core.HashBytes([]byte("declared"))),
			TreeBytes: []byte("actual different bytes"),
		},
	}
	ts := startFakeBrother(t, fake, true)
	obj := newTestObj(t, testConfigObj(t))

	session, err := obj.BrotherDial(context.Background(), "core-lib", "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("BrotherDial returned error: %v", err)
	}
	defer session.Close()

	if _, err := session.Version(context.Background(), "v1.0.0"); err == nil {
		t.Fatal("expected invariant error from tree hash mismatch")
	}
	if !session.(*BrotherSessionObj).Healthy() {
		t.Fatal("session must stay healthy after an invariant (non-transport) error")
	}
	if _, err := session.Hello(context.Background()); err != nil {
		t.Fatalf("session should still serve after invariant error: %v", err)
	}
}
