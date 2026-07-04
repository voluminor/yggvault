package logger

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/voluminor/yggvault/target"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const cVictoriaLogsInsertPath = "/insert/jsonline"

const cVictoriaLogsContentType = "application/stream+json"

const cVictoriaLogsCloseMaxFlushes = 8

const cVictoriaLogsClosePushTimeout = time.Second

// //

type victorialogsWriterObj struct {
	clientObj     *http.Client
	endpoint      string
	gzipEnabled   bool
	batchMaxBytes uint64
	flushInterval time.Duration
	runCtx        context.Context
	runCancelFunc context.CancelFunc
	lineChan      chan []byte
	stopChan      chan struct{}
	doneChan      chan struct{}
	closeOnce     sync.Once
	closed        atomic.Bool
	droppedTotal  atomic.Uint64
	failureTotal  atomic.Uint64
	closeErr      error
}

// //

func newVictorialogsWriter(configObj *stcfg.ConfigObj) (*victorialogsWriterObj, error) {
	if configObj == nil {
		return nil, errors.New("victorialogs config is nil")
	}

	vlConfigObj := configObj.Logging.Victorialogs
	endpoint, err := buildVictoriaLogsEndpoint(vlConfigObj.Url)
	if err != nil {
		return nil, err
	}

	runCtx, runCancelFunc := context.WithCancel(context.Background())
	writerObj := &victorialogsWriterObj{
		clientObj: &http.Client{
			Timeout: vlConfigObj.Timeout,
		},
		endpoint:      endpoint,
		gzipEnabled:   vlConfigObj.Gzip,
		batchMaxBytes: uint64(vlConfigObj.BatchMaxBytes),
		flushInterval: vlConfigObj.FlushInterval,
		runCtx:        runCtx,
		runCancelFunc: runCancelFunc,
		lineChan:      make(chan []byte, int(vlConfigObj.BufferMaxLines)),
		stopChan:      make(chan struct{}),
		doneChan:      make(chan struct{}),
	}

	go writerObj.run()
	return writerObj, nil
}

func buildVictoriaLogsEndpoint(rawURL string) (string, error) {
	trimmedURL := strings.TrimSpace(rawURL)
	if trimmedURL == "" {
		return "", errors.New("victorialogs url is empty")
	}

	parsedObj, err := url.Parse(trimmedURL)
	if err != nil {
		return "", fmt.Errorf("parse victorialogs url: %w", err)
	}
	if parsedObj.Scheme != "http" && parsedObj.Scheme != "https" {
		return "", errors.New("victorialogs url must use http or https")
	}
	if parsedObj.Host == "" {
		return "", errors.New("victorialogs url must include a host")
	}

	basePath := strings.TrimRight(parsedObj.Path, "/")
	parsedObj.Path = basePath + cVictoriaLogsInsertPath

	queryObj := parsedObj.Query()
	queryObj.Set("_time_field", "time")
	queryObj.Set("_msg_field", "message")
	queryObj.Set("_stream_fields", "service,instance,listener")
	parsedObj.RawQuery = queryObj.Encode()

	return parsedObj.String(), nil
}

func loggerInstance(configObj *stcfg.ConfigObj) string {
	instance := strings.TrimSpace(configObj.Web.Server.Domain)
	if instance != "" {
		return instance
	}
	return "local"
}

// //

// Write enqueues a line copy without blocking. A full queue drops the record and increments droppedTotal; after Close
// it is a silent no-op.
func (obj *victorialogsWriterObj) Write(data []byte) (int, error) {
	if obj.closed.Load() {
		return len(data), nil
	}

	lineArr := append([]byte(nil), data...)
	select {
	case obj.lineChan <- lineArr:
	default:
		obj.droppedTotal.Add(1)
	}

	return len(data), nil
}

// Close stops the background worker, drains the queue once, and reports final losses to stderr.
func (obj *victorialogsWriterObj) Close() error {
	obj.closeOnce.Do(func() {
		obj.closed.Store(true)
		if obj.runCancelFunc != nil {
			obj.runCancelFunc()
		}
		close(obj.stopChan)
		<-obj.doneChan
		if dropped, failures := obj.droppedTotal.Load(), obj.failureTotal.Load(); dropped > 0 || failures > 0 {
			fmt.Fprintf(os.Stderr, "victorialogs sink: dropped=%d push_failures=%d (best-effort; log file remains source of truth)\n", dropped, failures)
		}
	})
	return obj.closeErr
}

// //

func (obj *victorialogsWriterObj) run() {
	defer close(obj.doneChan)

	tickerObj := time.NewTicker(obj.flushInterval)
	defer tickerObj.Stop()

	batchArr := make([][]byte, 0, 64)
	var batchBytes uint64

	flush := func(ctx context.Context) error {
		if len(batchArr) == 0 {
			return nil
		}
		if err := obj.flush(ctx, batchArr); err != nil {
			if obj.closed.Load() && errors.Is(err, context.Canceled) {
				obj.dropVictoriaLogsBatch(batchArr)
				batchArr = batchArr[:0]
				batchBytes = 0
				return nil
			}
			obj.closeErr = err
			batchArr = batchArr[:0]
			batchBytes = 0
			return err
		}
		batchArr = batchArr[:0]
		batchBytes = 0
		return nil
	}
	drainClose := func() {
		closeFlushes := 0
		flushClose := func() error {
			if closeFlushes >= cVictoriaLogsCloseMaxFlushes {
				obj.dropVictoriaLogsBatch(batchArr)
				batchArr = batchArr[:0]
				batchBytes = 0
				obj.dropVictoriaLogsQueue()
				return nil
			}
			ctx, cancelFunc := context.WithTimeout(context.Background(), cVictoriaLogsClosePushTimeout)
			err := flush(ctx)
			cancelFunc()
			closeFlushes++
			return err
		}
		for {
			select {
			case lineArr := <-obj.lineChan:
				batchArr, batchBytes = appendVictoriaLogsLine(batchArr, batchBytes, lineArr)
				if batchBytes >= obj.batchMaxBytes {
					if err := flushClose(); err != nil {
						obj.dropVictoriaLogsQueue()
						return
					}
				}
			default:
				_ = flushClose()
				return
			}
		}
	}

	for {
		select {
		case <-obj.stopChan:
			drainClose()
			return
		default:
		}

		select {
		case lineArr := <-obj.lineChan:
			batchArr, batchBytes = appendVictoriaLogsLine(batchArr, batchBytes, lineArr)
			if batchBytes >= obj.batchMaxBytes {
				_ = flush(obj.normalFlushContext())
			}
		case <-tickerObj.C:
			_ = flush(obj.normalFlushContext())
		case <-obj.stopChan:
			drainClose()
			return
		}
	}
}

func appendVictoriaLogsLine(batchArr [][]byte, batchBytes uint64, lineArr []byte) ([][]byte, uint64) {
	if len(lineArr) == 0 {
		return batchArr, batchBytes
	}
	return append(batchArr, lineArr), batchBytes + uint64(len(lineArr)) + 1
}

func (obj *victorialogsWriterObj) normalFlushContext() context.Context {
	if obj.runCtx == nil {
		return context.Background()
	}
	return obj.runCtx
}

func (obj *victorialogsWriterObj) dropVictoriaLogsBatch(batchArr [][]byte) {
	if len(batchArr) == 0 {
		return
	}
	obj.droppedTotal.Add(uint64(len(batchArr)))
}

func (obj *victorialogsWriterObj) dropVictoriaLogsQueue() {
	for {
		select {
		case <-obj.lineChan:
			obj.droppedTotal.Add(1)
		default:
			return
		}
	}
}

func buildVictoriaLogsBody(batchArr [][]byte) []byte {
	var bufferObj bytes.Buffer
	for _, lineArr := range batchArr {
		trimmedArr := bytes.TrimRight(lineArr, "\r\n")
		if len(trimmedArr) == 0 {
			continue
		}
		bufferObj.Write(trimmedArr)
		bufferObj.WriteByte('\n')
	}
	return bufferObj.Bytes()
}

func encodeVictoriaLogsBody(bodyArr []byte, gzipEnabled bool) ([]byte, string, error) {
	if !gzipEnabled {
		return bodyArr, "", nil
	}

	var bufferObj bytes.Buffer
	gzipWriterObj := gzip.NewWriter(&bufferObj)
	if _, err := gzipWriterObj.Write(bodyArr); err != nil {
		_ = gzipWriterObj.Close()
		return nil, "", err
	}
	if err := gzipWriterObj.Close(); err != nil {
		return nil, "", err
	}

	return bufferObj.Bytes(), "gzip", nil
}

func (obj *victorialogsWriterObj) flush(ctx context.Context, batchArr [][]byte) error {
	bodyArr := buildVictoriaLogsBody(batchArr)
	if len(bodyArr) == 0 {
		return nil
	}

	payloadArr, contentEncoding, err := encodeVictoriaLogsBody(bodyArr, obj.gzipEnabled)
	if err != nil {
		obj.failureTotal.Add(1)
		return fmt.Errorf("encode victorialogs payload: %w", err)
	}

	requestObj, err := http.NewRequestWithContext(ctx, http.MethodPost, obj.endpoint, bytes.NewReader(payloadArr))
	if err != nil {
		obj.failureTotal.Add(1)
		return fmt.Errorf("create victorialogs request: %w", err)
	}

	requestObj.Header.Set("Content-Type", cVictoriaLogsContentType)
	requestObj.Header.Set("User-Agent", target.Name)
	if contentEncoding != "" {
		requestObj.Header.Set("Content-Encoding", contentEncoding)
	}

	responseObj, err := obj.clientObj.Do(requestObj)
	if err != nil {
		if obj.closed.Load() && errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		obj.failureTotal.Add(1)
		return fmt.Errorf("send victorialogs request: %w", err)
	}
	defer responseObj.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(responseObj.Body, 4096))
	if responseObj.StatusCode < http.StatusOK || responseObj.StatusCode >= http.StatusMultipleChoices {
		obj.failureTotal.Add(1)
		return fmt.Errorf("victorialogs push failed: status=%d", responseObj.StatusCode)
	}

	return nil
}
