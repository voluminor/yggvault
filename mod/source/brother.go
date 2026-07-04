package source

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/rpc"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

const (
	// cTreeObjectMax caps a single tree object and matches the storage tree-codec.
	cTreeObjectMax = 256 << 20

	// cBatchMax is the default blob hash count for one batch.
	cBatchMax = brotherwire.DefaultMaxFetchBatchCount

	// cIndexPageMax caps the number of versions on a single index page.
	cIndexPageMax = 4096

	// cMaxVersionBytes caps version strings received from a brother.
	cMaxVersionBytes = 256

	// cMaxBodyMDBytes caps release notes received from a brother.
	cMaxBodyMDBytes = 128 << 10

	// cMaxSourceURLBytes caps source URLs declared by a brother.
	cMaxSourceURLBytes = 2048

	// Per-response gob header cap; body cap is derived from configured fetch limits per session.
	cRPCHeaderCap = 1 << 20
)

// // // // // // // // // //

// BrotherSessionObj describes a single brother session over net/rpc via CONNECT, hijack and gob.
// localKey is used for diagnostics; remoteKey goes into RPC arguments.
//
// The session is not concurrent-safe: a single goroutine owns it, and mu makes each call atomic.
// Invariant errors do not kill the session; transport/context errors mark it unhealthy for a rescan redial.
type BrotherSessionObj struct {
	obj        *Obj
	mu         sync.Mutex
	client     *rpc.Client
	conn       net.Conn
	failed     error
	brotherURL string
	localKey   string
	remoteKey  string
	fetchBytes uint64
	fetchCount uint
}

// // // // // // // // // //

func toWire(h core.HashObj) brotherwire.HashWire { return brotherwire.HashWire(h) }

func fromWire(w brotherwire.HashWire) core.HashObj { return core.HashObj(w) }

func (obj *Obj) dialTimeout() time.Duration {
	if obj.requestTimeout > 0 {
		return obj.requestTimeout
	}
	return 30 * time.Second
}

func (obj *Obj) rpcBodyCap() int64 {
	maxObj := obj.maxFetchBytes
	if maxObj < cTreeObjectMax {
		maxObj = cTreeObjectMax
	}
	const extraObj = 8 << 20
	const maxInt64Obj = uint64(^uint64(0) >> 1)
	if maxObj > maxInt64Obj-extraObj {
		return int64(maxInt64Obj)
	}
	return int64(maxObj + extraObj)
}

func rpcAddress(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	port := "80"
	if u.Scheme == "https" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

func replaceFile(tempPath string, targetPath string) error {
	err := os.Rename(tempPath, targetPath)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	if _, statErr := os.Stat(targetPath); statErr != nil {
		return err
	}
	if removeErr := os.Remove(targetPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return errors.Join(err, removeErr)
	}
	if renameErr := os.Rename(tempPath, targetPath); renameErr != nil {
		return errors.Join(err, renameErr)
	}
	return nil
}

func writeBlobFile(path string, data []byte) error {
	tmpObj, err := os.CreateTemp(filepath.Dir(path), ".blob-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp blob: %w", err)
	}
	tmpName := tmpObj.Name()
	if _, err := tmpObj.Write(data); err != nil {
		_ = tmpObj.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmpObj.Sync(); err != nil {
		_ = tmpObj.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmpObj.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := replaceFile(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// // // // // // // // // //

// BrotherSessionInterface defines the brother session contract for rescan.
// BrotherDial returns the interface so callers and tests do not depend on the concrete session type.
type BrotherSessionInterface interface {
	Hello(ctx context.Context) (HelloResultObj, error)
	Index(ctx context.Context, page uint32) ([]BrotherIndexEntryObj, uint32, BrotherSourceInfoObj, error)
	Version(ctx context.Context, version string) (BrotherVersionObj, error)
	BlobsFetch(ctx context.Context, version string, reqs []BlobReqObj, destDir string) (BrotherFetchResultObj, error)
	FetchLimits() (uint64, uint)
	// Healthy reports whether the session has seen no transport/context error.
	Healthy() bool
	Close() error
}

var _ BrotherSessionInterface = (*BrotherSessionObj)(nil)

// BrotherDial opens a session via routedDial, optional TLS, CONNECT /rpc and a gob rpc.Client.
func (obj *Obj) BrotherDial(ctx context.Context, localKey, remoteKey, brotherURL string) (BrotherSessionInterface, error) {
	u, err := url.Parse(brotherURL)
	if err != nil {
		return nil, permanent(fmt.Errorf("parse brother url: %w", err))
	}

	dialCtx, cancel := context.WithTimeout(ctx, obj.dialTimeout())
	defer cancel()

	var conn net.Conn
	conn, err = obj.routedDial(dialCtx, "tcp", rpcAddress(u))
	if err != nil {
		return nil, err
	}

	if u.Scheme == "https" && !obj.isMeshHost(u.Hostname()) {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: u.Hostname()})
		if err := tlsConn.HandshakeContext(dialCtx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		conn = tlsConn
	}

	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(conn, "CONNECT "+brotherwire.RPCPath+" HTTP/1.0\n\n"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, permanent(fmt.Errorf("brother CONNECT %s: status %s", brotherwire.RPCPath, resp.Status))
	}
	_ = conn.SetDeadline(time.Time{})

	return &BrotherSessionObj{
		obj:        obj,
		client:     rpc.NewClientWithCodec(newBoundedGobCodec(conn, cRPCHeaderCap, obj.rpcBodyCap())),
		conn:       conn,
		brotherURL: brotherURL,
		localKey:   localKey,
		remoteKey:  remoteKey,
		fetchBytes: obj.maxFetchBytes,
		fetchCount: obj.maxFetchCount,
	}, nil
}

// Close idempotently closes the rpc client and the connection.
func (s *BrotherSessionObj) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil
	}
	err := s.client.Close()
	s.client = nil
	return err
}

// Healthy reports whether the session is fit for further use.
func (s *BrotherSessionObj) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed == nil && s.client != nil
}

// FetchLimits returns effective blob fetch limits for this session.
func (s *BrotherSessionObj) FetchLimits() (uint64, uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetchBytes, s.fetchCount
}

func (s *BrotherSessionObj) updateFetchLimits(remoteBytes uint64, remoteCount uint32) {
	bytesObj := s.obj.maxFetchBytes
	if remoteBytes > 0 && remoteBytes < bytesObj {
		bytesObj = remoteBytes
	}
	countObj := s.obj.maxFetchCount
	if remoteCount > 0 && uint(remoteCount) < countObj {
		countObj = uint(remoteCount)
	}
	s.mu.Lock()
	s.fetchBytes = bytesObj
	s.fetchCount = countObj
	s.mu.Unlock()
}

// // // // // // // // // //

func (s *BrotherSessionObj) rpcCall(ctx context.Context, method string, arg, reply any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.failed
	}
	if s.client == nil {
		return rpc.ErrShutdown
	}

	if s.obj.rpcLimiter != nil {
		if err := s.obj.rpcLimiter.Wait(ctx); err != nil {
			return err
		}
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		budget := s.obj.requestTimeout
		if budget <= 0 {
			budget = 30 * time.Second
		}
		deadline = time.Now().Add(budget)
	}
	_ = s.conn.SetDeadline(deadline)

	call := s.client.Go(method, arg, reply, make(chan *rpc.Call, 1))
	select {
	case <-ctx.Done():
		_ = s.client.Close()
		s.failed = ctx.Err()
		return ctx.Err()
	case done := <-call.Done:
		_ = s.conn.SetDeadline(time.Time{})
		if done.Error != nil {
			var serverErr rpc.ServerError
			if !errors.As(done.Error, &serverErr) {
				s.failed = done.Error
			}
		}
		return done.Error
	}
}

func (s *BrotherSessionObj) rpcInvalid(method, reason string, respBytes, maxBytes uint64, cause error) error {
	return permanent(stcode.NewErrBrotherRpcInvalid(cause, s.localKey, maxBytes, method, respBytes, reason, s.brotherURL))
}

// // // // // // // // // //

// Hello returns the protocol and instance checksums; callers validate the protocol.
func (s *BrotherSessionObj) Hello(ctx context.Context) (HelloResultObj, error) {
	var reply brotherwire.HelloReplyObj
	if err := s.rpcCall(ctx, brotherwire.MethodHello, brotherwire.HelloArgObj{}, &reply); err != nil {
		return HelloResultObj{}, err
	}
	s.updateFetchLimits(reply.MaxFetchResponseBytes, reply.MaxFetchBatchCount)
	bytesObj, countObj := s.FetchLimits()
	return HelloResultObj{Protocol: reply.Protocol, MaxFetchResponseBytes: bytesObj, MaxFetchBatchCount: countObj}, nil
}

// Index returns one page of remoteKey versions with release notes and source info.
// NextPage==0 means the end; otherwise the page must strictly increase.
// Non-monotonic pages, oversize fields or source info map to brother_rpc_invalid.
func (s *BrotherSessionObj) Index(ctx context.Context, page uint32) ([]BrotherIndexEntryObj, uint32, BrotherSourceInfoObj, error) {
	var reply brotherwire.IndexReplyObj
	arg := brotherwire.IndexArgObj{Key: s.remoteKey, Page: page}
	if err := s.rpcCall(ctx, brotherwire.MethodIndex, arg, &reply); err != nil {
		return nil, 0, BrotherSourceInfoObj{}, err
	}
	if len(reply.Entries) > cIndexPageMax {
		return nil, 0, BrotherSourceInfoObj{}, s.rpcInvalid(brotherwire.MethodIndex, "index page too large", uint64(len(reply.Entries)), cIndexPageMax, nil)
	}
	if reply.NextPage != 0 && reply.NextPage <= page {
		return nil, 0, BrotherSourceInfoObj{}, s.rpcInvalid(brotherwire.MethodIndex, "non-monotonic next page", uint64(reply.NextPage), uint64(page), nil)
	}
	if len(reply.Source.SourceURL) > cMaxSourceURLBytes {
		return nil, 0, BrotherSourceInfoObj{}, s.rpcInvalid(brotherwire.MethodIndex, "source url too large", uint64(len(reply.Source.SourceURL)), cMaxSourceURLBytes, nil)
	}

	out := make([]BrotherIndexEntryObj, len(reply.Entries))
	for i := range reply.Entries {
		entry := reply.Entries[i]
		if len(entry.Version) == 0 || len(entry.Version) > cMaxVersionBytes {
			return nil, 0, BrotherSourceInfoObj{}, s.rpcInvalid(brotherwire.MethodIndex, "version string length", uint64(len(entry.Version)), cMaxVersionBytes, nil)
		}
		if len(entry.BodyMD) > cMaxBodyMDBytes {
			return nil, 0, BrotherSourceInfoObj{}, s.rpcInvalid(brotherwire.MethodIndex, "release notes too large", uint64(len(entry.BodyMD)), cMaxBodyMDBytes, nil)
		}
		if entry.UpstreamSeq < 0 {
			return nil, 0, BrotherSourceInfoObj{}, s.rpcInvalid(brotherwire.MethodIndex, "negative upstream seq", uint64(len(entry.Version)), 0, nil)
		}
		out[i] = BrotherIndexEntryObj{
			Version:      entry.Version,
			ReleaseNotes: entry.BodyMD,
			TreeHash:     fromWire(entry.TreeHash),
			SourceHash:   fromWire(entry.SourceHash),
			UpstreamSeq:  entry.UpstreamSeq,
		}
	}
	srcInfoObj := BrotherSourceInfoObj{SourceURL: reply.Source.SourceURL}
	return out, reply.NextPage, srcInfoObj, nil
}

// Version returns the canonical tree bytes and checks transport integrity against the declared hash24.
func (s *BrotherSessionObj) Version(ctx context.Context, version string) (BrotherVersionObj, error) {
	var reply brotherwire.VersionReplyObj
	arg := brotherwire.VersionArgObj{Key: s.remoteKey, Version: version}
	if err := s.rpcCall(ctx, brotherwire.MethodVersion, arg, &reply); err != nil {
		return BrotherVersionObj{}, err
	}
	if n := len(reply.TreeBytes); n == 0 || n > cTreeObjectMax {
		return BrotherVersionObj{}, s.rpcInvalid(brotherwire.MethodVersion, "tree object size", uint64(n), cTreeObjectMax, nil)
	}
	treeHash := fromWire(reply.TreeHash)
	if err := core.VerifyHash(treeHash, reply.TreeBytes); err != nil {
		return BrotherVersionObj{}, s.rpcInvalid(brotherwire.MethodVersion, "tree hash mismatch", uint64(len(reply.TreeBytes)), cTreeObjectMax, err)
	}
	return BrotherVersionObj{TreeBytes: reply.TreeBytes}, nil
}

// BlobsFetch requests sized blobs, verifies their hash24 and writes them to destDir.
// The total declared size is checked before sending so that valid large batches fit within the codec cap.
// Unrequested, duplicate or oversized blobs are dropped or map to brother_rpc_invalid.
func (s *BrotherSessionObj) BlobsFetch(ctx context.Context, version string, reqs []BlobReqObj, destDir string) (BrotherFetchResultObj, error) {
	if len(reqs) == 0 {
		return BrotherFetchResultObj{}, nil
	}
	maxFetchBytes, maxFetchCount := s.FetchLimits()
	if len(reqs) > int(maxFetchCount) {
		return BrotherFetchResultObj{}, permanent(fmt.Errorf("blobs fetch batch %d exceeds cap %d", len(reqs), maxFetchCount))
	}

	var totalBytes uint64
	want := make(map[core.HashObj]struct{}, len(reqs))
	wire := make([]brotherwire.HashWire, len(reqs))
	for i := range reqs {
		totalBytes += reqs[i].SizeBytes
		want[reqs[i].Hash] = struct{}{}
		wire[i] = toWire(reqs[i].Hash)
	}
	if totalBytes > maxFetchBytes {
		return BrotherFetchResultObj{}, permanent(fmt.Errorf("blobs fetch batch %d bytes exceeds cap %d", totalBytes, maxFetchBytes))
	}

	var reply brotherwire.BlobsFetchReplyObj
	if err := s.rpcCall(ctx, brotherwire.MethodBlobsFetch, brotherwire.BlobsFetchArgObj{Hashes: wire}, &reply); err != nil {
		return BrotherFetchResultObj{}, err
	}

	result := BrotherFetchResultObj{Blobs: make([]core.StagedBlobObj, 0, len(reply.Blobs))}
	seen := make(map[core.HashObj]struct{}, len(reply.Blobs))
	for _, blob := range reply.Blobs {
		h := fromWire(blob.Hash)
		if _, ok := want[h]; !ok {
			continue
		}
		if _, dup := seen[h]; dup {
			continue
		}
		if uint64(len(blob.Value)) > s.obj.maxBlobBytes {
			return BrotherFetchResultObj{}, s.rpcInvalid(brotherwire.MethodBlobsFetch, "blob too large", uint64(len(blob.Value)), s.obj.maxBlobBytes, nil)
		}
		if err := core.VerifyHash(h, blob.Value); err != nil {
			return BrotherFetchResultObj{}, permanent(stcode.NewErrBrotherBlobHashMismatch(
				core.HashBytes(blob.Value).Hex(), err, h.Hex(), s.localKey, s.brotherURL, version))
		}

		fpath := spoolPath(destDir, h.Hex()+".blob")
		if err := writeBlobFile(fpath, blob.Value); err != nil {
			return BrotherFetchResultObj{}, permanent(err)
		}
		seen[h] = struct{}{}
		result.Blobs = append(result.Blobs, core.StagedBlobObj{
			BlobHash:  h,
			SizeBytes: uint64(len(blob.Value)),
			FilePath:  fpath,
		})
	}
	return result, nil
}
