package server

import (
	"context"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// // // // // // // // // //

type loggerCtxKeyObj struct{}

type accessWriterObj struct {
	http.ResponseWriter
	statusCode    int
	bytes         int64
	countBodyByte bool
}

// //

func loggerFrom(ctx context.Context) *zerolog.Logger {
	logObj, _ := ctx.Value(loggerCtxKeyObj{}).(*zerolog.Logger)
	return logObj
}

func withLogger(r *http.Request, logObj *zerolog.Logger) *http.Request {
	if logObj == nil {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), loggerCtxKeyObj{}, logObj))
}

func newAccessWriter(w http.ResponseWriter, countBodyByte bool) *accessWriterObj {
	return &accessWriterObj{ResponseWriter: w, countBodyByte: countBodyByte}
}

func (obj *accessWriterObj) WriteHeader(statusCode int) {
	if statusCode >= 100 && statusCode < 200 {
		obj.ResponseWriter.WriteHeader(statusCode)
		return
	}
	if obj.statusCode != 0 {
		return
	}
	obj.statusCode = statusCode
	obj.ResponseWriter.WriteHeader(statusCode)
}

func (obj *accessWriterObj) Write(bufArr []byte) (int, error) {
	if obj.statusCode == 0 {
		obj.statusCode = http.StatusOK
	}
	n, err := obj.ResponseWriter.Write(bufArr)
	if obj.countBodyByte {
		obj.bytes += int64(n)
	}
	return n, err
}

// Flush forwards flushing through ResponseController while preserving wrapped writer compatibility.
func (obj *accessWriterObj) Flush() {
	if obj.statusCode == 0 {
		obj.statusCode = http.StatusOK
	}
	_ = http.NewResponseController(obj.ResponseWriter).Flush()
}

// Unwrap lets ResponseController reach the underlying writer.
func (obj *accessWriterObj) Unwrap() http.ResponseWriter {
	return obj.ResponseWriter
}

func (obj *accessWriterObj) status() int {
	if obj.statusCode == 0 {
		return http.StatusOK
	}
	return obj.statusCode
}

// //

func accessLevel(status int) zerolog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return zerolog.ErrorLevel
	case status == http.StatusTooManyRequests:
		return zerolog.WarnLevel
	default:
		return zerolog.DebugLevel
	}
}

func shortText(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}

func requestEvent(logObj *zerolog.Logger, r *http.Request, status int) *zerolog.Event {
	const (
		maxHeaderLen = 128
		maxHostLen   = 255
		maxPathLen   = 512
	)

	eventObj := logObj.WithLevel(accessLevel(status))
	if eventObj == nil {
		return eventObj
	}

	lc := listenerCtxFrom(r.Context())
	eventObj = eventObj.
		Str("request_id", requestIDFrom(r.Context())).
		Str("listener", lc.listenerID.String()).
		Str("method", r.Method).
		Str("path", shortText(r.URL.Path, maxPathLen)).
		Bool("query", r.URL.RawQuery != "").
		Str("host", shortText(r.Host, maxHostLen)).
		Str("remote", clientKey(r)).
		Str("user_agent", shortText(r.UserAgent(), maxHeaderLen)).
		Int("status", status)
	if len(r.URL.Path) > maxPathLen {
		eventObj = eventObj.Bool("path_truncated", true)
	}
	if len(r.Host) > maxHostLen {
		eventObj = eventObj.Bool("host_truncated", true)
	}
	if rangeText := shortText(r.Header.Get("Range"), maxHeaderLen); rangeText != "" {
		eventObj = eventObj.Str("range", rangeText)
	}
	if ifNoneMatch := shortText(r.Header.Get("If-None-Match"), maxHeaderLen); ifNoneMatch != "" {
		eventObj = eventObj.Str("if_none_match", ifNoneMatch)
	}
	return eventObj
}

func (obj *ServerObj) logAccess(w *accessWriterObj, r *http.Request, startedAt time.Time) {
	status := w.status()
	requestEvent(&obj.logObj, r, status).
		Dur("duration", time.Since(startedAt)).
		Int64("bytes", w.bytes).
		Msg("http request completed")
}

func logRequestError(ctx context.Context, r *http.Request, status int, code string, err error) {
	logObj := loggerFrom(ctx)
	if logObj == nil {
		return
	}
	requestEvent(logObj, r, status).
		Err(err).
		Str("error_code", code).
		Msg("http request failed")
}
