package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

type majorKeyObj struct{}
type headOnlyKeyObj struct{}

// // // // // // // // // //

func newRequestID() string {
	var bufArr [8]byte
	if _, err := rand.Read(bufArr[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(bufArr[:])
}

func requestIDFrom(ctx context.Context) string {
	idText, _ := ctx.Value(requestIDKeyObj{}).(string)
	return idText
}

func majorFrom(ctx context.Context) string {
	major, _ := ctx.Value(majorKeyObj{}).(string)
	return major
}

// headOnlyFrom reports that the original request was HEAD, rewritten to GET on a clone.
// Artifact handlers answer from metadata without opening or materializing bodies.
func headOnlyFrom(ctx context.Context) bool {
	flag, _ := ctx.Value(headOnlyKeyObj{}).(bool)
	return flag
}

func withRequestID(r *http.Request, reqID string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestIDKeyObj{}, reqID))
}

func shallowClone(r *http.Request) *http.Request {
	clone := *r
	urlCopy := *r.URL
	clone.URL = &urlCopy
	return &clone
}

func looksLikeGoProxy(path string) bool {
	return strings.Contains(path, "/@v/") || strings.HasSuffix(path, "/@latest")
}

// stripEntryHost removes the entry-host prefix from goproxy paths; client module paths start with the host.
func stripEntryHost(path string, entryHost string) (string, bool) {
	if entryHost == "" || !looksLikeGoProxy(path) {
		return path, false
	}
	trimmed := strings.TrimPrefix(path, "/")
	hostPrefix := entryHost + "/"
	if !strings.HasPrefix(trimmed, hostPrefix) {
		return path, false
	}
	rest := trimmed[len(hostPrefix):]
	// The go toolchain probes the host itself as a module prefix and only tolerates 404/410.
	// Keep the host in place so the unknown key returns 404 instead of an empty-key 400.
	if strings.HasPrefix(rest, "@") {
		return path, false
	}
	return "/" + rest, true
}

// stripMajor removes a `vN` segment immediately before `/@v/` or `/@latest`.
// Go module paths for major >=2 end in `/vN`, while standalone vN keys are config-invalid.
// Explicit `/v0/` and `/v1/` are still stripped and later return strict 404.
func stripMajor(path string) (string, string) {
	markerIdx := strings.Index(path, "/@v/")
	if markerIdx < 0 {
		if !strings.HasSuffix(path, "/@latest") {
			return path, ""
		}
		markerIdx = len(path) - len("/@latest")
	}
	head := path[:markerIdx]
	lastSlash := strings.LastIndexByte(head, '/')
	if lastSlash <= 0 {
		return path, ""
	}
	segment := head[lastSlash+1:]
	if len(segment) < 2 || segment[0] != 'v' || !isAllDigits(segment[1:]) {
		return path, ""
	}
	return path[:lastSlash] + path[markerIdx:], segment
}

func isAllDigits(text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

func splitSegments(pathText string) []string {
	trimmed := strings.Trim(pathText, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

func (obj *Obj) splitNested(reqPath string) (string, bool) {
	if route.IsService(reqPath) {
		return reqPath, true
	}
	prefixText := "/" + obj.funcImplObj.effectivePrefix()
	if reqPath == prefixText {
		return "/", true
	}
	if strings.HasPrefix(reqPath, prefixText+"/") {
		return reqPath[len(prefixText):], true
	}
	return reqPath, false
}

func (obj *Obj) brotherRPCEnabled(lc listenerCtxObj) bool {
	switch lc.listenerID {
	case stcode.ListenerWeb:
		return obj.cfg.Brother.Rpc.WebEnabled
	case stcode.ListenerYgg:
		return obj.cfg.Brother.Rpc.YggEnabled
	default:
		return false
	}
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code string, message string) {
	writeDefaultError(w, r.Context(), r.Method == http.MethodHead, status, code, message)
}

// // // // // // // // // //

// Handler is the outer handler for one entry listener.
// It injects listener context, applies RPC/ingress/method gates, request ID, and go-proxy host/major stripping.
// HEAD is routed as GET because ogen only defines GET operations here.
func (obj *Obj) Handler(lc listenerCtxObj) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), listenerCtxKeyObj{}, lc))
		r = withRequestID(r, newRequestID())
		r = withLogger(r, &obj.logObj)

		if r.URL.Path == brotherwire.RPCPath {
			if !obj.brotherRPCEnabled(lc) {
				logWriterObj := newAccessWriter(w, r.Method != http.MethodHead)
				defer obj.logAccess(logWriterObj, r, time.Now())
				writeError(logWriterObj, r, http.StatusNotFound, "not_found", "resource not found")
				return
			}
			// Hijacked RPC sessions never reach logAccess; log the session envelope instead.
			sessionStart := time.Now()
			obj.brotherObj.Handler().ServeHTTP(w, r)
			obj.logObj.Info().
				Str("listener", string(lc.listenerID)).
				Str("remote", r.RemoteAddr).
				Dur("elapsed", time.Since(sessionStart)).
				Msg("brother rpc session finished")
			return
		}

		startedAt := time.Now()
		logWriterObj := newAccessWriter(w, r.Method != http.MethodHead)
		w = logWriterObj
		defer obj.logAccess(logWriterObj, r, startedAt)

		if obj.cfg.Web.Ingress.IdleTimeout > 0 {
			w = newWriteIdleWriter(w, obj.cfg.Web.Ingress.IdleTimeout)
		}

		if maxURI := obj.cfg.Web.Ingress.MaxRequestUriBytes; maxURI > 0 && uint(len(r.RequestURI)) > maxURI {
			writeError(w, r, http.StatusRequestURITooLong, "uri_too_long", "request uri exceeds limit")
			return
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and HEAD are allowed")
			return
		}

		if r.URL.Path == route.OpenAPI {
			obj.serveOpenAPI(w, r, lc)
			return
		}

		routedPath, rewritten := stripEntryHost(r.URL.Path, lc.entryHost)

		if !rewritten && obj.staticSnap != nil && obj.funcImplObj.effectivePrefix() != "" {
			sub, isService := obj.splitNested(routedPath)
			if !isService {
				if lc.rateLimited(r) {
					writeError(w, r, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
					return
				}
				obj.staticSnap.Serve(w, r)
				return
			}
			if sub != routedPath {
				routedPath = sub
				rewritten = true
			}
		} else if rewritten && obj.staticSnap != nil && obj.funcImplObj.effectivePrefix() != "" {
			if sub, isService := obj.splitNested(routedPath); isService {
				routedPath = sub
			}
		}

		// Strip major after the route prefix; nested mode looks like /prefix/key/vN/@v/...
		major := ""
		if sub, majorSeg := stripMajor(routedPath); majorSeg != "" {
			routedPath, major, rewritten = sub, majorSeg, true
		}

		needHeadGet := r.Method == http.MethodHead

		if rewritten || needHeadGet {
			r = shallowClone(r)
			if rewritten {
				r.URL.Path = routedPath
			}
			if major != "" {
				r = r.WithContext(context.WithValue(r.Context(), majorKeyObj{}, major))
			}
			if needHeadGet {
				r.Method = http.MethodGet
				r = r.WithContext(context.WithValue(r.Context(), headOnlyKeyObj{}, true))
			}
		}

		obj.router.ServeHTTP(w, r)
	})
}
