package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/ogen-go/ogen/middleware"
	"github.com/ogen-go/ogen/ogenerrors"
	"go.opentelemetry.io/otel/attribute"

	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/mod/server/webui"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

// errRateLimited is a sentinel mapped to 429 by the error handler.
var errRateLimited = errors.New("rate limit exceeded")

// // // // // // // // // //

func isDataOp(operationName api.OperationName) bool {
	switch operationName {
	case api.GetHealthOperation,
		api.GetInfoOperation,
		api.GetMetricsIndexOperation,
		api.GetMetricsCoreOperation,
		api.GetMetricsCacheOperation,
		api.GetMetricsErrorsOperation,
		api.GetMetricsRescanOperation,
		api.GetMetricsInternalOperation:
		return false
	default:
		return true
	}
}

// // // // // // // // // //

func opMiddleware(req middleware.Request, next middleware.Next) (middleware.Response, error) {
	lc := listenerCtxFrom(req.Context)
	if labelerObj, ok := api.LabelerFromContext(req.Context); ok {
		labelerObj.Add(attribute.String("listener", lc.listenerID.String()))
	}
	if isDataOp(req.OperationName) && lc.rateLimited(req.Raw) {
		return middleware.Response{}, errRateLimited
	}
	return next(req)
}

// // // // // // // // // //

func errStatus(err error) (int, string, string) {
	var decodeParamsErr *ogenerrors.DecodeParamsError
	switch {
	case errors.Is(err, errRateLimited):
		return http.StatusTooManyRequests, "rate_limited", "rate limit exceeded"
	case errors.Is(err, serr.ErrNotFound):
		return http.StatusNotFound, "not_found", "resource not found"
	case errors.Is(err, serr.ErrBadInput), errors.As(err, &decodeParamsErr), errors.Is(err, storage.ErrInvalidRef):
		return http.StatusBadRequest, "bad_request", "invalid request"
	case errors.Is(err, serr.ErrUnavailable):
		return http.StatusServiceUnavailable, "unavailable", "resource temporarily unavailable"
	case errors.Is(err, serr.ErrRangeNotSatisfiable):
		return http.StatusRequestedRangeNotSatisfiable, "range_not_satisfiable", "requested range not satisfiable"
	}
	return http.StatusInternalServerError, "internal_error", "internal server error"
}

func errorHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
	if ctx.Err() != nil {
		return
	}
	status, code, message := errStatus(err)
	logRequestError(ctx, r, status, code, err)
	writeDefaultError(w, ctx, r.Method == http.MethodHead, status, code, message)
}

// // // // // // // // // //

// parseKeyCursor reads the ?before / ?after keyset cursor for the key page plus a cache key that folds it
// into the ETag. A malformed or absent token degrades to the newest page.
func parseKeyCursor(q url.Values) (webui.PageCursorObj, string) {
	if tok := q.Get("before"); tok != "" {
		if seq, ver, ok := webui.DecodeCursor(tok); ok {
			return webui.BeforeCursor(seq, ver), "b:" + tok
		}
	}
	if tok := q.Get("after"); tok != "" {
		if seq, ver, ok := webui.DecodeCursor(tok); ok {
			return webui.AfterCursor(seq, ver), "a:" + tok
		}
	}
	return webui.PageCursorObj{}, ""
}

// // // // // // // // // //

func (obj *Obj) notFound(w http.ResponseWriter, r *http.Request) {
	lc := listenerCtxFrom(r.Context())
	if lc.rateLimited(r) {
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
		return
	}

	segArr := splitSegments(r.URL.Path)
	if len(segArr) > 1 {
		http.NotFound(w, r)
		return
	}
	lnk := obj.funcImplObj.linkCtx(lc)
	viewCtxObj := obj.funcImplObj.viewContext(lc)

	// ETag gate before rendering; matching If-None-Match avoids full HTML render and inline CSS.
	if len(segArr) == 0 {
		etag := etagOf("catalog-page", lc.listenerID.String(), lc.scheme(), obj.funcImplObj.crossFreshness())
		if writeHTMLNotModified(w, r, etag) {
			return
		}
		htmlArr, err := webui.Catalog(r.Context(), obj.funcImplObj.deps.State, obj.funcImplObj.deps.Storage, viewCtxObj)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		writeHTML(w, r, etag, htmlArr)
		return
	}

	cursor, cursorKey := parseKeyCursor(r.URL.Query())
	etag := etagOf("key-page", segArr[0], cursorKey, lc.listenerID.String(), lc.scheme(), obj.funcImplObj.keyFreshness(segArr[0]))
	if writeHTMLNotModified(w, r, etag) {
		return
	}
	htmlArr, found, err := webui.Key(r.Context(), obj.funcImplObj.deps.State, obj.funcImplObj.deps.Storage, lnk, viewCtxObj, segArr[0], cursor, obj.funcImplObj.listPageSize())
	if err != nil {
		logRequestError(r.Context(), r, http.StatusInternalServerError, "internal_error", err)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	writeHTML(w, r, etag, htmlArr)
}
