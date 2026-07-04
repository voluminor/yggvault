package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// // // // // // // // // //

const (
	// Connection pool caps; stdlib defaults can be too open on a weak mesh.
	cMaxConnsPerHost     = 8
	cMaxIdleConns        = 32
	cMaxIdleConnsPerHost = 4
	cIdleConnTimeout     = 90 * time.Second
	cTLSHandshakeTimeout = 10 * time.Second

	// cMaxRedirects caps redirects; cross-scheme redirects into or out of Yggdrasil are forbidden.
	cMaxRedirects = 5

	cUserAgent = "yggvault"

	// Minimum body throughput per window. The idle timer catches silence; this floor catches
	// byte-per-idle slowloris streams that would hold a download slot and rescan cycle.
	cMinDownloadBytesPerSec = 1024
	cDownloadRateWindow     = 30 * time.Second
)

// //

var (
	errArchiveTooLarge  = errors.New("source archive exceeds size limit")
	errRedirectCrossYgg = errors.New("redirect crosses yggdrasil/clearnet boundary")
	errDownloadStalled  = errors.New("download stalled: no progress within idle window")
)

// // // // // // // // // //

func (obj *Obj) isMeshHost(host string) bool {
	return obj.mesh != nil && obj.mesh.OwnsHost(host)
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func (obj *Obj) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= cMaxRedirects {
		return fmt.Errorf("stopped after %d redirects", cMaxRedirects)
	}
	if len(via) > 0 && obj.isMeshHost(req.URL.Hostname()) != obj.isMeshHost(via[0].URL.Hostname()) {
		return errRedirectCrossYgg
	}
	return nil
}

// // // // // // // // // //

func (obj *Obj) routedDial(ctx context.Context, network, address string) (net.Conn, error) {
	if obj.isMeshHost(hostOnly(address)) {
		if obj.mesh == nil || !obj.mesh.Enabled() {
			return nil, fmt.Errorf("yggdrasil host %q requires enabled mesh", address)
		}
		return obj.mesh.DialContext(ctx, network, address)
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: obj.denyInternalDial}
	return dialer.DialContext(ctx, network, address)
}

func (obj *Obj) buildClients() {
	newTransport := func() *http.Transport {
		return &http.Transport{
			DialContext:           obj.routedDial,
			MaxConnsPerHost:       cMaxConnsPerHost,
			MaxIdleConns:          cMaxIdleConns,
			MaxIdleConnsPerHost:   cMaxIdleConnsPerHost,
			IdleConnTimeout:       cIdleConnTimeout,
			TLSHandshakeTimeout:   cTLSHandshakeTimeout,
			ResponseHeaderTimeout: obj.requestTimeout,
			ForceAttemptHTTP2:     true,
		}
	}

	obj.metaClient = &http.Client{
		Transport:     obj.withAuth(newTransport()),
		Timeout:       obj.requestTimeout,
		CheckRedirect: obj.checkRedirect,
	}
	// refs (git smart HTTP) runs anonymously: git endpoints reject Bearer on public GitHub repos,
	// and API rate limits do not apply to this protocol. SSRF guards and timeouts remain identical.
	obj.refsClient = &http.Client{
		Transport:     newTransport(),
		Timeout:       obj.requestTimeout,
		CheckRedirect: obj.checkRedirect,
	}
	obj.downloadClient = &http.Client{
		Transport:     obj.withAuth(newTransport()),
		CheckRedirect: obj.checkRedirect,
	}
}

// probeBody distinguishes "the endpoint definitively does not exist" (4xx) from
// "the host does not answer reliably" (network/5xx/429): for source classification these are different outcomes.
func (obj *Obj) probeBody(ctx context.Context, rawURL string, maxBytes int64) ([]byte, probeOutcomeObj) {
	reqCtx, cancel := context.WithTimeout(ctx, obj.dialTimeout())
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, probeOutcomeIndeterminate
	}
	req.Header.Set("User-Agent", cUserAgent)

	resp, err := obj.metaClient.Do(req)
	if err != nil {
		return nil, probeOutcomeIndeterminate
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, probeOutcomeIndeterminate
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return nil, probeOutcomeMiss
	default:
		return nil, probeOutcomeIndeterminate
	}
	// Read maxBytes+1: a body exactly at the limit is indistinguishable from a truncated one.
	// Oversize cannot be a compact vault marker (/health and /info are tiny JSON), so it is a
	// definitive miss rather than "unknown"; large forge health pages must not stall discovery forever.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, probeOutcomeIndeterminate
	}
	if int64(len(body)) > maxBytes {
		return nil, probeOutcomeMiss
	}
	return body, probeOutcomeBody
}

func (obj *Obj) getLimitedBody(ctx context.Context, rawURL string, maxBytes int64) ([]byte, bool) {
	body, outcome := obj.probeBody(ctx, rawURL, maxBytes)
	return body, outcome == probeOutcomeBody
}

// // // // // // // // // //

func (obj *Obj) acquireDownload(ctx context.Context) (func(), error) {
	select {
	case obj.downloadSem <- struct{}{}:
		return func() { <-obj.downloadSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type resumeStateObj struct {
	validator string
}

type stallReaderObj struct {
	reader io.Reader
	timer  *time.Timer
	idle   time.Duration
	armed  atomic.Bool

	// Throughput floor: idle timeout alone misses drip feeds, so every window must meet minRate*elapsed.
	cancel      context.CancelCauseFunc
	minRate     int64
	window      time.Duration
	windowStart time.Time
	windowBytes int64
}

// Read arms the idle timer during a blocking read, disarms it, then enforces the throughput floor.
func (r *stallReaderObj) Read(dataArr []byte) (int, error) {
	r.armed.Store(true)
	r.timer.Reset(r.idle)
	n, err := r.reader.Read(dataArr)
	r.timer.Stop()
	r.armed.Store(false)
	r.checkThroughput(n)
	return n, err
}

// checkThroughput cancels the request when a window receives less than minRate*elapsed bytes.
func (r *stallReaderObj) checkThroughput(n int) {
	if r.minRate <= 0 || r.cancel == nil {
		return
	}
	now := time.Now()
	if r.windowStart.IsZero() {
		r.windowStart = now
	}
	r.windowBytes += int64(n)
	elapsed := now.Sub(r.windowStart)
	if elapsed < r.window {
		return
	}
	if r.windowBytes < int64(elapsed.Seconds()*float64(r.minRate)) {
		r.cancel(errDownloadStalled)
		return
	}
	r.windowStart = now
	r.windowBytes = 0
}

func (obj *Obj) streamToSpool(ctx context.Context, rawURL, destPath string, stateObj *resumeStateObj) (uint64, error) {
	var startAt int64
	if stateObj.validator != "" {
		if fi, statErr := os.Stat(destPath); statErr == nil && fi.Mode().IsRegular() {
			startAt = fi.Size()
		}
	}

	reqCtx, reqCancel := context.WithCancelCause(ctx)
	defer reqCancel(nil)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, permanent(err)
	}
	req.Header.Set("User-Agent", cUserAgent)
	if startAt > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startAt))
		req.Header.Set("If-Range", stateObj.validator)
	}

	resp, err := obj.downloadClient.Do(req)
	if err != nil {
		return uint64(startAt), err
	}
	defer resp.Body.Close()

	resume := false
	switch resp.StatusCode {
	case http.StatusOK:
		resume = false
	case http.StatusPartialContent:
		resume = true
	default:
		statusErr := fmt.Errorf("download %q: http status %d", rawURL, resp.StatusCode)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return 0, permanent(statusErr)
		}
		return 0, statusErr
	}

	if tag := resp.Header.Get("ETag"); tag != "" {
		stateObj.validator = tag
	} else if lm := resp.Header.Get("Last-Modified"); lm != "" {
		stateObj.validator = lm
	} else {
		stateObj.validator = ""
	}

	written := int64(0)
	if resume {
		written = startAt
	}

	max := obj.maxArchiveSize
	if max > 0 && resp.ContentLength > 0 {
		total := resp.ContentLength + written
		if uint64(total) > max {
			return uint64(total), permanent(errArchiveTooLarge)
		}
	}

	var fileObj *os.File
	if resume {
		fileObj, err = os.OpenFile(destPath, os.O_APPEND|os.O_WRONLY, 0o600)
	} else {
		fileObj, err = os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	}
	if err != nil {
		return uint64(written), permanent(fmt.Errorf("open spool file: %w", err))
	}

	stallReader := &stallReaderObj{
		reader:  resp.Body,
		idle:    obj.requestTimeout,
		cancel:  reqCancel,
		minRate: cMinDownloadBytesPerSec,
		window:  cDownloadRateWindow,
	}
	stallReader.timer = time.AfterFunc(obj.requestTimeout, func() {
		if stallReader.armed.Load() {
			reqCancel(errDownloadStalled)
		}
	})
	defer stallReader.timer.Stop()

	var reader io.Reader = stallReader
	if max > 0 {
		reader = io.LimitReader(reader, int64(max)+1-written)
	}

	copied, copyErr := io.Copy(fileObj, reader)
	written += copied
	if copyErr != nil {
		_ = fileObj.Close()
		return uint64(written), copyErr
	}
	if max > 0 && uint64(written) > max {
		_ = fileObj.Close()
		_ = os.Remove(destPath)
		return uint64(written), permanent(errArchiveTooLarge)
	}
	if err := fileObj.Sync(); err != nil {
		_ = fileObj.Close()
		return uint64(written), permanent(fmt.Errorf("sync spool file: %w", err))
	}
	if err := fileObj.Close(); err != nil {
		return uint64(written), permanent(fmt.Errorf("close spool file: %w", err))
	}
	return uint64(written), nil
}

func spoolPath(destDir, name string) string {
	return filepath.Join(destDir, name)
}
