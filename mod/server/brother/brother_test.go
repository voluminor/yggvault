package brother

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"testing"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/pebblestore"
	"github.com/voluminor/yggvault/mod/storage/treecodec"
)

// // // // // // // // // //

type bufConnObj struct {
	in     *bytes.Buffer
	out    *bytes.Buffer
	closed bool
}

func (c *bufConnObj) Read(p []byte) (int, error)  { return c.in.Read(p) }
func (c *bufConnObj) Write(p []byte) (int, error) { return c.out.Write(p) }
func (c *bufConnObj) Close() error                { c.closed = true; return nil }

type fakeStoreObj struct {
	versionsPage map[string][]core.VersionObj
	keysetCalls  int
	keysetAfter  []string
	versionOf    map[string]core.VersionObj
	trees        map[core.HashObj][]core.TreeEntryObj
	blobs        map[core.HashObj][]byte
	readErr      map[core.HashObj]error

	listCalls   int
	blobReadSet []core.HashObj
}

func (f *fakeStoreObj) ListVersionsPage(_ context.Context, key string, _ bool, limit int, offset int) ([]core.VersionObj, error) {
	f.listCalls++
	arr := f.versionsPage[key]
	if offset >= len(arr) {
		return nil, nil
	}
	end := offset + limit
	if end > len(arr) {
		end = len(arr)
	}
	return arr[offset:end], nil
}
func (f *fakeStoreObj) ListVersionsKeyset(_ context.Context, key string, _ bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error) {
	f.keysetCalls++
	f.keysetAfter = append(f.keysetAfter, fmt.Sprintf("%d/%s", afterSeq, afterVersion))
	arr := f.versionsPage[key]
	start := 0
	if afterVersion != "" {
		for i := range arr {
			if arr[i].UpstreamSeq == afterSeq && arr[i].Version == afterVersion {
				start = i + 1
				break
			}
		}
	}
	if start >= len(arr) {
		return nil, nil
	}
	end := start + limit
	if end > len(arr) {
		end = len(arr)
	}
	return arr[start:end], nil
}
func (f *fakeStoreObj) GetVersion(_ context.Context, key string, version string) (core.VersionObj, bool, error) {
	vObj, ok := f.versionOf[key+"@"+version]
	return vObj, ok, nil
}
func (f *fakeStoreObj) ReadTree(_ context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error) {
	arr, ok := f.trees[treeHashObj]
	if !ok {
		return nil, errors.New("tree not found")
	}
	return arr, nil
}
func (f *fakeStoreObj) ReadBlob(_ context.Context, hashObj core.HashObj) ([]byte, error) {
	f.blobReadSet = append(f.blobReadSet, hashObj)
	if e, ok := f.readErr[hashObj]; ok {
		return nil, e
	}
	data, ok := f.blobs[hashObj]
	if !ok {
		return nil, pebblestore.ErrObjectNotFound
	}
	return data, nil
}

func newHandler(store StoreInterface, mirrors map[string]string) *handlerObj {
	return &handlerObj{ctx: context.Background(), storeObj: store, releaseMirrors: mirrors}
}

// // // // // // // // // //

func TestBoundedCodecHeaderCap(t *testing.T) {
	var stream bytes.Buffer
	enc := gob.NewEncoder(&stream)
	if err := enc.Encode(&rpc.Request{ServiceMethod: "Brother.Hello", Seq: 1}); err != nil {
		t.Fatalf("encode request: %v", err)
	}
	conn := &bufConnObj{in: bytes.NewBuffer(stream.Bytes()), out: &bytes.Buffer{}}

	codecObj := newBoundedCodec(conn, 8, 16)
	var reqObj rpc.Request
	err := codecObj.ReadRequestHeader(&reqObj)
	if err == nil {
		t.Fatal("expected error when header exceeds cap, got nil")
	}
}

func TestBoundedCodecBodyCap(t *testing.T) {
	var stream bytes.Buffer
	enc := gob.NewEncoder(&stream)
	if err := enc.Encode(&rpc.Request{ServiceMethod: "Brother.BlobsFetch", Seq: 1}); err != nil {
		t.Fatalf("encode header: %v", err)
	}
	hashArr := make([]brotherwire.HashWire, 200)
	for i := range hashArr {
		for j := range hashArr[i] {
			hashArr[i][j] = byte(i + j + 1)
		}
	}
	if err := enc.Encode(&brotherwire.BlobsFetchArgObj{Hashes: hashArr}); err != nil {
		t.Fatalf("encode body: %v", err)
	}
	conn := &bufConnObj{in: bytes.NewBuffer(stream.Bytes()), out: &bytes.Buffer{}}

	codecObj := newBoundedCodec(conn, 1<<20, 1024)
	var reqObj rpc.Request
	if err := codecObj.ReadRequestHeader(&reqObj); err != nil {
		t.Fatalf("header should decode under generous cap: %v", err)
	}
	var argObj brotherwire.BlobsFetchArgObj
	if err := codecObj.ReadRequestBody(&argObj); err == nil {
		t.Fatal("expected error when body exceeds cap, got nil")
	}
}

func TestLimitedReaderExhaust(t *testing.T) {
	lr := &limitedReaderObj{r: bytes.NewReader(bytes.Repeat([]byte{0xAB}, 100))}
	lr.reset(4)
	buf := make([]byte, 16)
	n, _ := lr.Read(buf)
	if n != 4 {
		t.Fatalf("first read should yield budget (4), got %d", n)
	}
	if _, err := lr.Read(buf); !errors.Is(err, errRequestTooLarge) {
		t.Fatalf("exhausted budget: want errRequestTooLarge, got %v", err)
	}
}

// // // // // // // // // //

func TestHandlerMethodGate(t *testing.T) {
	srvObj := &ServerObj{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://x/rpc", nil)
	srvObj.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /rpc: want 405, got %d", rec.Code)
	}
}

func TestCloseIdempotent(t *testing.T) {
	_, rootCancel := context.WithCancel(context.Background())
	srvObj := &ServerObj{rootCancel: rootCancel}
	srvObj.Close()
	srvObj.Close()
}

// // // // // // // // // //

func TestHandlerHello(t *testing.T) {
	h := newHandler(&fakeStoreObj{}, nil)

	var reply brotherwire.HelloReplyObj
	if err := h.Hello(brotherwire.HelloArgObj{}, &reply); err != nil {
		t.Fatalf("Hello: %v", err)
	}
	if reply.Protocol != brotherwire.Protocol {
		t.Errorf("protocol: want %q, got %q", brotherwire.Protocol, reply.Protocol)
	}
	if reply.MaxFetchResponseBytes != brotherwire.DefaultMaxFetchResponseBytes {
		t.Errorf("max fetch bytes: want %d, got %d", uint64(brotherwire.DefaultMaxFetchResponseBytes), reply.MaxFetchResponseBytes)
	}
	if reply.MaxFetchBatchCount != brotherwire.DefaultMaxFetchBatchCount {
		t.Errorf("max fetch batch count: want %d, got %d", brotherwire.DefaultMaxFetchBatchCount, reply.MaxFetchBatchCount)
	}
	if !reply.IndexKeyset {
		t.Error("IndexKeyset capability must be advertised")
	}
}

func TestHandlerIndexPageUpperBound(t *testing.T) {
	store := &fakeStoreObj{versionsPage: map[string][]core.VersionObj{}}
	h := newHandler(store, map[string]string{"pkg/alpha": "https://up.test/alpha"})

	var reply brotherwire.IndexReplyObj
	if err := h.Index(brotherwire.IndexArgObj{Key: "pkg/alpha", Page: cMaxIndexPage + 1}, &reply); err != nil {
		t.Fatalf("Index over-page: %v", err)
	}
	if store.listCalls != 0 {
		t.Fatalf("over-page must not query storage, got %d ListVersionsPage calls", store.listCalls)
	}
	if reply.NextPage != 0 {
		t.Errorf("over-page NextPage: want 0 (end), got %d", reply.NextPage)
	}
	if len(reply.Entries) != 0 {
		t.Errorf("over-page entries: want 0, got %d", len(reply.Entries))
	}
	if reply.Source.SourceURL != "https://up.test/alpha" {
		t.Errorf("source url: want https://up.test/alpha, got %q", reply.Source.SourceURL)
	}
}

func TestHandlerIndexNextPage(t *testing.T) {
	key := "pkg/alpha"
	arr := make([]core.VersionObj, cIndexPageSize+1)
	for i := range arr {
		arr[i] = core.VersionObj{Key: key, Version: "v" + itoa(i), UpstreamSeq: int64(cIndexPageSize + 1 - i)}
	}
	store := &fakeStoreObj{versionsPage: map[string][]core.VersionObj{key: arr}}
	h := newHandler(store, map[string]string{key: "https://up.test/alpha"})

	var reply brotherwire.IndexReplyObj
	if err := h.Index(brotherwire.IndexArgObj{Key: key, Page: 1}, &reply); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(reply.Entries) != cIndexPageSize {
		t.Fatalf("entries: want %d (page truncated), got %d", cIndexPageSize, len(reply.Entries))
	}
	if reply.NextPage != 2 {
		t.Errorf("NextPage: want 2, got %d", reply.NextPage)
	}
	if store.keysetCalls != 1 || store.listCalls != 0 {
		t.Fatalf("first page must use keyset: keyset=%d offset=%d", store.keysetCalls, store.listCalls)
	}
}

func TestHandlerIndexKeysetCursor(t *testing.T) {
	key := "pkg/alpha"
	arr := []core.VersionObj{
		{Key: key, Version: "v3.0.0", UpstreamSeq: 3},
		{Key: key, Version: "v2.0.0", UpstreamSeq: 2},
		{Key: key, Version: "v1.0.0", UpstreamSeq: 1},
	}
	store := &fakeStoreObj{versionsPage: map[string][]core.VersionObj{key: arr}}
	h := newHandler(store, nil)

	var reply brotherwire.IndexReplyObj
	if err := h.Index(brotherwire.IndexArgObj{Key: key, Page: 2, AfterSeq: 2, AfterVersion: "v2.0.0"}, &reply); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if store.keysetCalls != 1 || store.listCalls != 0 {
		t.Fatalf("cursor request must use keyset: keyset=%d offset=%d", store.keysetCalls, store.listCalls)
	}
	if len(reply.Entries) != 1 || reply.Entries[0].Version != "v1.0.0" {
		t.Fatalf("unexpected keyset page: %+v", reply.Entries)
	}
	if got := store.keysetAfter[0]; got != "2/v2.0.0" {
		t.Fatalf("keyset cursor=%q", got)
	}
}

func TestHandlerIndexLegacyPageFallback(t *testing.T) {
	key := "pkg/alpha"
	arr := []core.VersionObj{
		{Key: key, Version: "v3.0.0"},
		{Key: key, Version: "v2.0.0"},
		{Key: key, Version: "v1.0.0"},
	}
	store := &fakeStoreObj{versionsPage: map[string][]core.VersionObj{key: arr}}
	h := newHandler(store, nil)

	var reply brotherwire.IndexReplyObj
	if err := h.Index(brotherwire.IndexArgObj{Key: key, Page: 2}, &reply); err != nil {
		t.Fatalf("Index: %v", err)
	}
	if store.keysetCalls != 0 || store.listCalls != 1 {
		t.Fatalf("legacy page must use offset fallback: keyset=%d offset=%d", store.keysetCalls, store.listCalls)
	}
}

func TestHandlerVersion(t *testing.T) {
	key, version := "pkg/alpha", "v1.0.0"
	entryArr := []core.TreeEntryObj{
		{Path: "go.mod", Mode: core.ModeFile, SizeBytes: 3, BlobHash: core.HashBytes([]byte("abc"))},
	}
	wantBytes, wantHash, err := treecodec.Encode(entryArr)
	if err != nil {
		t.Fatalf("treecodec.Encode: %v", err)
	}
	vObj := core.VersionObj{Key: key, Version: version, TreeHash: wantHash}
	store := &fakeStoreObj{
		versionOf: map[string]core.VersionObj{key + "@" + version: vObj},
		trees:     map[core.HashObj][]core.TreeEntryObj{wantHash: entryArr},
	}
	h := newHandler(store, nil)

	var reply brotherwire.VersionReplyObj
	if err := h.Version(brotherwire.VersionArgObj{Key: key, Version: version}, &reply); err != nil {
		t.Fatalf("Version: %v", err)
	}
	if !bytes.Equal(reply.TreeBytes, wantBytes) {
		t.Error("tree bytes mismatch")
	}
	if reply.TreeHash != brotherwire.HashWire(wantHash) {
		t.Error("tree hash mismatch")
	}

	var miss brotherwire.VersionReplyObj
	if err := h.Version(brotherwire.VersionArgObj{Key: key, Version: "v9.9.9"}, &miss); err == nil {
		t.Fatal("missing version: expected error, got nil")
	}
}

func TestHandlerBlobsFetchBatchCap(t *testing.T) {
	store := &fakeStoreObj{}
	h := newHandler(store, nil)

	overArg := brotherwire.BlobsFetchArgObj{Hashes: make([]brotherwire.HashWire, cBlobBatchMax+1)}
	var reply brotherwire.BlobsFetchReplyObj
	if err := h.BlobsFetch(overArg, &reply); err == nil {
		t.Fatal("over-batch BlobsFetch: expected error, got nil")
	}
	if len(store.blobReadSet) != 0 {
		t.Fatalf("over-batch must reject before any ReadBlob, got %d reads", len(store.blobReadSet))
	}
}

func TestHandlerBlobsFetchConfiguredBatchCap(t *testing.T) {
	store := &fakeStoreObj{}
	h := &handlerObj{ctx: context.Background(), storeObj: store, maxFetchBatchCount: 1}

	overArg := brotherwire.BlobsFetchArgObj{Hashes: make([]brotherwire.HashWire, 2)}
	var reply brotherwire.BlobsFetchReplyObj
	if err := h.BlobsFetch(overArg, &reply); err == nil {
		t.Fatal("configured over-batch BlobsFetch: expected error, got nil")
	}
	if len(store.blobReadSet) != 0 {
		t.Fatalf("configured over-batch must reject before any ReadBlob, got %d reads", len(store.blobReadSet))
	}
}

func TestHandlerBlobsFetchDedup(t *testing.T) {
	h1 := core.HashBytes([]byte("blob-1"))
	h2 := core.HashBytes([]byte("blob-2"))
	missing := core.HashBytes([]byte("nope"))
	store := &fakeStoreObj{blobs: map[core.HashObj][]byte{
		h1: []byte("one"),
		h2: []byte("two"),
	}}
	h := newHandler(store, nil)

	arg := brotherwire.BlobsFetchArgObj{Hashes: []brotherwire.HashWire{
		brotherwire.HashWire(h1),
		brotherwire.HashWire(h1),
		brotherwire.HashWire(h2),
		brotherwire.HashWire(missing),
	}}
	var reply brotherwire.BlobsFetchReplyObj
	if err := h.BlobsFetch(arg, &reply); err != nil {
		t.Fatalf("BlobsFetch: %v", err)
	}
	if len(store.blobReadSet) != 3 {
		t.Errorf("dedup: want 3 ReadBlob calls, got %d", len(store.blobReadSet))
	}
	if len(reply.Blobs) != 2 {
		t.Fatalf("reply blobs: want 2, got %d", len(reply.Blobs))
	}
}

// TestHandlerBlobsFetchReadErrorFailsClosed asserts that a real read/IO error (not a genuine absence) fails the
// whole batch instead of being silently dropped — the peer must retry rather than ingest a truncated version.
func TestHandlerBlobsFetchReadErrorFailsClosed(t *testing.T) {
	h1 := core.HashBytes([]byte("blob-1"))
	h2 := core.HashBytes([]byte("blob-2"))
	store := &fakeStoreObj{
		blobs:   map[core.HashObj][]byte{h1: []byte("one"), h2: []byte("two")},
		readErr: map[core.HashObj]error{h2: errors.New("disk read failed")},
	}
	h := newHandler(store, nil)

	arg := brotherwire.BlobsFetchArgObj{Hashes: []brotherwire.HashWire{
		brotherwire.HashWire(h1),
		brotherwire.HashWire(h2),
	}}
	var reply brotherwire.BlobsFetchReplyObj
	err := h.BlobsFetch(arg, &reply)
	if err == nil {
		t.Fatal("BlobsFetch: expected an error on a real read failure, got nil")
	}
	if errors.Is(err, pebblestore.ErrObjectNotFound) {
		t.Fatalf("a real read error must not be classified as not-found: %v", err)
	}
}

// // // // // // // // // //

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

var _ io.ReadWriteCloser = (*bufConnObj)(nil)

// // // // // // // // // //

// TestPerPeerSessionCap: one peer cannot occupy more than peerMax slots; release returns the slot
// and cleans the map, while distinct peers are counted independently.
func TestPerPeerSessionCap(t *testing.T) {
	srvObj := &ServerObj{peerMax: 2}
	if !srvObj.acquirePeerSlot("peerA") {
		t.Fatal("peerA must get its first slot")
	}
	if !srvObj.acquirePeerSlot("peerA") {
		t.Fatal("peerA must get its second slot")
	}
	if srvObj.acquirePeerSlot("peerA") {
		t.Fatal("peerA must be rejected past peerMax")
	}
	if !srvObj.acquirePeerSlot("peerB") {
		t.Fatal("a different peer must not be starved by peerA")
	}
	srvObj.releasePeerSlot("peerA")
	if !srvObj.acquirePeerSlot("peerA") {
		t.Fatal("peerA must reclaim a slot after release")
	}
	srvObj.releasePeerSlot("peerA")
	srvObj.releasePeerSlot("peerA")
	srvObj.releasePeerSlot("peerB")
	if len(srvObj.peerSessions) != 0 {
		t.Fatalf("peer map must self-clean at zero, got %d entries", len(srvObj.peerSessions))
	}
}

// TestPerPeerSessionCapDisabled: peerMax<=0 disables accounting, so acquire always succeeds.
func TestPerPeerSessionCapDisabled(t *testing.T) {
	srvObj := &ServerObj{}
	for i := 0; i < 100; i++ {
		if !srvObj.acquirePeerSlot("peer") {
			t.Fatal("disabled per-peer accounting must always admit")
		}
	}
	if srvObj.peerSessions != nil {
		t.Fatal("disabled accounting must not allocate the peer map")
	}
}
