package logger

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voluminor/yggvault/target"
	"github.com/voluminor/yggvault/target/stcode"
	stcfg "github.com/voluminor/yggvault/target/stconf"

	"github.com/rs/zerolog"
)

// // // // // // // // // //

func newConsoleConfigObjForTest() *stcfg.ConfigObj {
	return &stcfg.ConfigObj{
		Logging: stcfg.LoggingObj{
			Console: stcfg.LoggingConsoleObj{
				Enabled: true,
				Level:   stcfg.ConsoleLogLevelInfo,
			},
			File: stcfg.LoggingFileObj{
				Enabled: false,
			},
		},
	}
}

func newFileConfigObjForTest(dirPath string) *stcfg.ConfigObj {
	return &stcfg.ConfigObj{
		Logging: stcfg.LoggingObj{
			Console: stcfg.LoggingConsoleObj{
				Enabled: false,
			},
			File: stcfg.LoggingFileObj{
				Enabled:    true,
				Level:      stcfg.FileLogLevelInfo,
				Dir:        dirPath,
				MaxSize:    stcfg.SizeObj(1),
				MaxBackups: 2,
				MaxAge:     24 * time.Hour,
				Compress:   false,
			},
		},
	}
}

func newVictoriaLogsConfigObjForTest(url string) *stcfg.ConfigObj {
	return &stcfg.ConfigObj{
		Logging: stcfg.LoggingObj{
			Victorialogs: stcfg.LoggingVictorialogsObj{
				Enabled:        true,
				Url:            url,
				Level:          stcfg.VictorialogsLogLevelInfo,
				FlushInterval:  time.Hour,
				BatchMaxBytes:  stcfg.SizeObj(1000000),
				BufferMaxLines: 16,
				Timeout:        time.Second,
				Gzip:           false,
			},
		},
		Web: stcfg.WebObj{
			Server: stcfg.WebServerObj{
				Domain: "example.com",
			},
		},
	}
}

func TestNewConsoleOnly(t *testing.T) {
	loggerObj, err := New(newConsoleConfigObjForTest())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if loggerObj == nil {
		t.Fatal("New returned nil logger")
	}
}

func TestNewFileOnlyWritesLog(t *testing.T) {
	dirPath := t.TempDir()
	loggerObj, err := New(newFileConfigObjForTest(dirPath))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	loggerObj.Zero().Info().Msg("file logger works")
	if err = loggerObj.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dirPath, target.Name+".log"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "file logger works") {
		t.Fatalf("unexpected log contents: %s", string(data))
	}
}

func TestNewVictoriaLogsOnlyWritesLog(t *testing.T) {
	requestChan := make(chan struct {
		path        string
		streamField string
		body        string
	}, 1)

	serverObj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dataArr, _ := io.ReadAll(r.Body)
		requestChan <- struct {
			path        string
			streamField string
			body        string
		}{
			path:        r.URL.Path,
			streamField: r.URL.Query().Get("_stream_fields"),
			body:        string(dataArr),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer serverObj.Close()

	loggerObj, err := New(newVictoriaLogsConfigObjForTest(serverObj.URL))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	loggerObj.Zero().Info().Msg("victorialogs works")
	if err = loggerObj.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	select {
	case requestObj := <-requestChan:
		if requestObj.path != cVictoriaLogsInsertPath {
			t.Fatalf("unexpected path: %q", requestObj.path)
		}
		if requestObj.streamField != "service,instance,listener" {
			t.Fatalf("unexpected stream fields: %q", requestObj.streamField)
		}
		if !strings.Contains(requestObj.body, "victorialogs works") {
			t.Fatalf("unexpected request body: %s", requestObj.body)
		}
		if !strings.Contains(requestObj.body, `"service":"`+target.Name+`"`) {
			t.Fatalf("service field missing: %s", requestObj.body)
		}
		if !strings.Contains(requestObj.body, `"instance":"example.com"`) {
			t.Fatalf("instance field missing: %s", requestObj.body)
		}
	case <-time.After(time.Second):
		t.Fatal("victorialogs request was not received")
	}
}

func TestVictoriaLogsCloseDrainsInBatches(t *testing.T) {
	requestChan := make(chan string, 8)
	serverObj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dataArr, _ := io.ReadAll(r.Body)
		requestChan <- string(dataArr)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer serverObj.Close()

	configObj := newVictoriaLogsConfigObjForTest(serverObj.URL)
	configObj.Logging.Victorialogs.BatchMaxBytes = stcfg.SizeObj(80)
	configObj.Logging.Victorialogs.BufferMaxLines = 8
	loggerObj, err := New(configObj)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	loggerObj.Zero().Info().Str("item", strings.Repeat("a", 32)).Msg("one")
	loggerObj.Zero().Info().Str("item", strings.Repeat("b", 32)).Msg("two")
	loggerObj.Zero().Info().Str("item", strings.Repeat("c", 32)).Msg("three")
	if err = loggerObj.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	gotCount := 0
	deadlineObj := time.After(time.Second)
	for gotCount < 2 {
		select {
		case <-requestChan:
			gotCount++
		case <-deadlineObj:
			t.Fatalf("got %d VictoriaLogs requests, want at least 2", gotCount)
		}
	}
}

func TestVictoriaLogsCloseDrainIsBounded(t *testing.T) {
	var requestCount atomic.Int64
	serverObj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requestCount.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer serverObj.Close()

	endpointText, err := buildVictoriaLogsEndpoint(serverObj.URL)
	if err != nil {
		t.Fatalf("buildVictoriaLogsEndpoint returned error: %v", err)
	}
	writerObj := &victorialogsWriterObj{
		clientObj:     serverObj.Client(),
		endpoint:      endpointText,
		batchMaxBytes: 1,
		flushInterval: time.Hour,
		lineChan:      make(chan []byte, cVictoriaLogsCloseMaxFlushes+8),
		stopChan:      make(chan struct{}),
		doneChan:      make(chan struct{}),
	}
	for i := 0; i < cVictoriaLogsCloseMaxFlushes+8; i++ {
		writerObj.lineChan <- []byte(fmt.Sprintf(`{"message":"line-%d"}`, i))
	}

	close(writerObj.stopChan)
	writerObj.run()

	if gotCount := int(requestCount.Load()); gotCount != cVictoriaLogsCloseMaxFlushes {
		t.Fatalf("request count=%d, want %d", gotCount, cVictoriaLogsCloseMaxFlushes)
	}
	if writerObj.droppedTotal.Load() == 0 {
		t.Fatal("droppedTotal=0, want dropped close backlog")
	}
}

func TestVictoriaLogsCloseCancelsInFlightFlush(t *testing.T) {
	startedChan := make(chan struct{}, 1)
	releaseChan := make(chan struct{})
	serverObj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case startedChan <- struct{}{}:
		default:
		}
		select {
		case <-releaseChan:
		case <-r.Context().Done():
		}
	}))
	defer serverObj.Close()
	defer close(releaseChan)

	configObj := newVictoriaLogsConfigObjForTest(serverObj.URL)
	configObj.Logging.Victorialogs.BatchMaxBytes = stcfg.SizeObj(1)
	configObj.Logging.Victorialogs.BufferMaxLines = 4
	configObj.Logging.Victorialogs.Timeout = time.Hour
	writerObj, err := newVictorialogsWriter(configObj)
	if err != nil {
		t.Fatalf("newVictorialogsWriter returned error: %v", err)
	}
	if _, err = writerObj.Write([]byte(`{"message":"slow"}`)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	select {
	case <-startedChan:
	case <-time.After(time.Second):
		t.Fatal("victorialogs request was not started")
	}

	startedAt := time.Now()
	if err = writerObj.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Fatalf("Close took %s, want under 2s", elapsed)
	}
	if writerObj.droppedTotal.Load() == 0 {
		t.Fatal("droppedTotal=0, want canceled in-flight batch counted as dropped")
	}
	if writerObj.failureTotal.Load() != 0 {
		t.Fatalf("failureTotal=%d, want 0 for intentional close cancel", writerObj.failureTotal.Load())
	}
}

func TestNewRejectsNoEnabledSinks(t *testing.T) {
	_, err := New(&stcfg.ConfigObj{})
	if err == nil {
		t.Fatal("New returned nil error")
	}
	if !strings.Contains(err.Error(), "logger has no enabled sinks") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewRejectsMissingFileDir(t *testing.T) {
	configObj := newFileConfigObjForTest("")

	_, err := New(configObj)
	if err == nil {
		t.Fatal("New returned nil error")
	}
	if !strings.Contains(err.Error(), "file logger dir is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestXErrFallsBackToStandardError(t *testing.T) {
	bufferObj := bytes.NewBuffer(nil)
	loggerObj := &Obj{
		value: zerolog.New(bufferObj),
	}

	loggerObj.XErr(errors.New("boom")).Msg("failed")

	text := bufferObj.String()
	if !strings.Contains(text, `"error":"boom"`) {
		t.Fatalf("unexpected log output: %s", text)
	}
}

func TestXErrUsesGeneratedStructuredError(t *testing.T) {
	bufferObj := bytes.NewBuffer(nil)
	loggerObj := &Obj{
		value: zerolog.New(bufferObj),
	}

	loggerObj.XErr(stcode.NewErrComposerNameCollision("other-key", "core-lib", "vendor/pkg")).Msg("failed")

	text := bufferObj.String()
	if !strings.Contains(text, `"err-name":"composer_name_collision"`) {
		t.Fatalf("unexpected log output: %s", text)
	}
	if !strings.Contains(text, `"package_name":"vendor/pkg"`) {
		t.Fatalf("unexpected log output: %s", text)
	}
}

func TestXErrUsesWrappedGeneratedStructuredError(t *testing.T) {
	bufferObj := bytes.NewBuffer(nil)
	loggerObj := &Obj{
		value: zerolog.New(bufferObj),
	}
	err := fmt.Errorf("wrapped: %w", stcode.NewErrComposerNameCollision("other-key", "core-lib", "vendor/pkg"))

	loggerObj.XErr(err).Msg("failed")

	text := bufferObj.String()
	if !strings.Contains(text, `"err-name":"composer_name_collision"`) {
		t.Fatalf("unexpected log output: %s", text)
	}
	if !strings.Contains(text, `"package_name":"vendor/pkg"`) {
		t.Fatalf("unexpected log output: %s", text)
	}
}

func TestXErrIncludesNameForErrorSeverity(t *testing.T) {
	bufferObj := bytes.NewBuffer(nil)
	loggerObj := &Obj{
		value: zerolog.New(bufferObj),
	}

	loggerObj.XErr(stcode.NewErrArtifactBuildFailed("", 0, "", nil, "body_hash", "", 0, "core-lib", "", "", "v1.0.0")).Msg("failed")

	text := bufferObj.String()
	if !strings.Contains(text, `"error":"artifact_build_failed"`) {
		t.Fatalf("unexpected log output: %s", text)
	}
	if !strings.Contains(text, `"err-name":"artifact_build_failed"`) {
		t.Fatalf("unexpected log output: %s", text)
	}
}

func TestHelperConversions(t *testing.T) {
	sizeValue, err := sizeBytesToMegabytes(stcfg.SizeObj(1000001))
	if err != nil {
		t.Fatalf("sizeBytesToMegabytes returned error: %v", err)
	}
	if sizeValue != 2 {
		t.Fatalf("unexpected megabyte value: %d", sizeValue)
	}

	dayValue, err := durationToDays(25 * time.Hour)
	if err != nil {
		t.Fatalf("durationToDays returned error: %v", err)
	}
	if dayValue != 2 {
		t.Fatalf("unexpected day value: %d", dayValue)
	}

	_, err = parseLevel("nonsense")
	if err == nil {
		t.Fatal("parseLevel returned nil error")
	}
}
