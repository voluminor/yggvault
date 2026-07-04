package server

import (
	"net/http"
	"time"
)

// // // // // // // // // //

type writeIdleWriterObj struct {
	http.ResponseWriter
	ctrl    *http.ResponseController
	timeout time.Duration
}

func newWriteIdleWriter(w http.ResponseWriter, timeout time.Duration) *writeIdleWriterObj {
	return &writeIdleWriterObj{ResponseWriter: w, ctrl: http.NewResponseController(w), timeout: timeout}
}

func (obj *writeIdleWriterObj) bump() {
	_ = obj.ctrl.SetWriteDeadline(time.Now().Add(obj.timeout))
}

func (obj *writeIdleWriterObj) WriteHeader(statusCode int) {
	obj.bump()
	obj.ResponseWriter.WriteHeader(statusCode)
}

func (obj *writeIdleWriterObj) Write(bufArr []byte) (int, error) {
	obj.bump()
	return obj.ResponseWriter.Write(bufArr)
}

// Flush forwards flushing and refreshes the write deadline for streaming responses.
func (obj *writeIdleWriterObj) Flush() {
	obj.bump()
	_ = obj.ctrl.Flush()
}

// Unwrap lets ResponseController and unwrapping reach the underlying writer.
func (obj *writeIdleWriterObj) Unwrap() http.ResponseWriter {
	return obj.ResponseWriter
}
