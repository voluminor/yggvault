package server

import (
	"context"
	"net/http"

	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

const (
	cContentTypeJSON = "application/json; charset=utf-8"

	cNoStoreControl = "no-store"
)

// // // // // // // // // //

func writeDefaultError(w http.ResponseWriter, ctx context.Context, isHead bool, status int, code string, message string) {
	bodyObj := api.DefaultErrorObj{Error: code, Message: message}
	if reqID := requestIDFrom(ctx); reqID != "" {
		bodyObj.SetRequestID(api.NewOptString(reqID))
	}
	headerObj := w.Header()
	headerObj.Set("Content-Type", cContentTypeJSON)
	headerObj.Set("Cache-Control", cNoStoreControl)
	w.WriteHeader(status)
	if isHead {
		return
	}
	_, _ = w.Write(encodeJSON(&bodyObj))
}
