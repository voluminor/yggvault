package server

import (
	"net/http"
	"strconv"

	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

const (
	// cOpenAPIContentType is the spec body content type.
	cOpenAPIContentType = "application/json; charset=utf-8"
)

// // // // // // // // // //

type openapiSpecObj struct {
	body []byte
	etag string
}

func newOpenAPISpec() (openapiSpecObj, error) {
	body, err := api.OpenAPIJSON()
	if err != nil {
		return openapiSpecObj{}, err
	}
	return openapiSpecObj{body: body, etag: etagOf("openapi", string(body))}, nil
}

// // // // // // // // // //

func (obj *ServerObj) serveOpenAPI(w http.ResponseWriter, r *http.Request, lc listenerCtxObj) {
	if lc.rateLimited(r) {
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
		return
	}
	headerObj := w.Header()
	headerObj.Set("ETag", obj.openapiSpec.etag)
	headerObj.Set("Cache-Control", cNoCacheControl)
	if condMatch(api.NewOptString(r.Header.Get("If-None-Match")), obj.openapiSpec.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	headerObj.Set("Content-Type", cOpenAPIContentType)
	headerObj.Set("Content-Length", strconv.Itoa(len(obj.openapiSpec.body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(obj.openapiSpec.body)
}
