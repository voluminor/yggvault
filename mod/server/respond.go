package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/jx"

	"github.com/voluminor/yggvault/mod/cache"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

const (
	cNoCacheControl = "no-cache, max-age=0, must-revalidate"

	cContentTypeHTML = "text/html; charset=utf-8"
)

// // // // // // // // // //

func writeHTML(w http.ResponseWriter, r *http.Request, etag string, htmlArr []byte) {
	headerObj := w.Header()
	headerObj.Set("Content-Type", cContentTypeHTML)
	headerObj.Set("Cache-Control", cNoCacheControl)
	if etag != "" {
		headerObj.Set("ETag", etag)
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(htmlArr)
}

func writeHTMLNotModified(w http.ResponseWriter, r *http.Request, etag string) bool {
	if !condMatch(api.NewOptString(r.Header.Get("If-None-Match")), etag) {
		return false
	}
	headerObj := w.Header()
	headerObj.Set("ETag", etag)
	headerObj.Set("Cache-Control", cNoCacheControl)
	w.WriteHeader(http.StatusNotModified)
	return true
}

// // // // // // // // // //

func etagOf(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	return `"` + core.HashBytes([]byte(target.Version+"\x00"+target.Hash+"\x00"+strings.Join(parts, "\x00"))).Hex() + `"`
}

func encodeJSON(enc interface{ Encode(*jx.Encoder) }) []byte {
	encoderObj := new(jx.Encoder)
	enc.Encode(encoderObj)
	return encoderObj.Bytes()
}

func condMatch(ifNoneMatch api.OptString, etag string) bool {
	if !ifNoneMatch.Set || etag == "" {
		return false
	}
	headerVal := strings.TrimSpace(ifNoneMatch.Value)
	if headerVal == "" {
		return false
	}
	if headerVal == "*" {
		return true
	}
	for _, partText := range strings.Split(headerVal, ",") {
		if strings.TrimPrefix(strings.TrimSpace(partText), "W/") == etag {
			return true
		}
	}
	return false
}

// // // // // // // // // //

func (obj *funcObj) cacheControlData() string {
	lastRescan := obj.deps.State.Snapshot().LastRescan
	interval := obj.deps.Config.Rescan.Interval
	secs := int64(cache.SecondsToNextRescan(lastRescan, interval, time.Now()).Seconds())
	if secs < 0 {
		secs = 0
	}
	return "public, must-revalidate, max-age=" + strconv.FormatInt(secs, 10)
}

// // // // // // // // // //

func (obj *funcObj) crossFreshness() string {
	return obj.deps.State.Checksums().Content.Hex()
}

func (obj *funcObj) keyFreshness(key string) string {
	if keyStateObj, ok := obj.deps.State.KeyState(key); ok {
		return strconv.FormatInt(keyStateObj.LastPublishTS.UnixNano(), 10) + "\x00" + strconv.FormatUint(keyStateObj.VersionCount, 10)
	}
	return "0"
}
