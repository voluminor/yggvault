package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/target/api"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	cOpenAPIContentType = "application/json; charset=utf-8"
)

// // // // // // // // // //

type openapiSpecObj struct {
	body []byte
	etag string
}

func openAPIPrefix(configObj *stconf.ConfigObj) string {
	if configObj == nil || configObj.Web.Static.Dir == "" || configObj.Web.Routing.Prefix == "" {
		return ""
	}
	return "/" + configObj.Web.Routing.Prefix
}

func prefixOpenAPIPath(prefixText string, pathText string) string {
	if pathText == "/" {
		return prefixText
	}
	return prefixText + pathText
}

func patchOpenAPIJSON(bodyArr []byte, prefixText string) ([]byte, error) {
	if prefixText == "" {
		return bodyArr, nil
	}

	var rootObj map[string]json.RawMessage
	if err := json.Unmarshal(bodyArr, &rootObj); err != nil {
		return nil, fmt.Errorf("decode openapi json: %w", err)
	}

	rawPathsArr, ok := rootObj["paths"]
	if !ok {
		return nil, errors.New("openapi json has no paths")
	}
	var pathMapObj map[string]json.RawMessage
	if err := json.Unmarshal(rawPathsArr, &pathMapObj); err != nil {
		return nil, fmt.Errorf("decode openapi paths: %w", err)
	}

	patchedMapObj := make(map[string]json.RawMessage, len(pathMapObj))
	for pathText, pathBodyArr := range pathMapObj {
		if pathText == "" || pathText[0] != '/' {
			return nil, fmt.Errorf("openapi path must start with slash: %q", pathText)
		}
		targetText := pathText
		if !route.IsService(pathText) {
			targetText = prefixOpenAPIPath(prefixText, pathText)
		}
		if _, existsFlag := patchedMapObj[targetText]; existsFlag {
			return nil, fmt.Errorf("openapi nested path collision: %s", targetText)
		}
		patchedMapObj[targetText] = pathBodyArr
	}

	patchedPathsArr, err := json.Marshal(patchedMapObj)
	if err != nil {
		return nil, fmt.Errorf("encode openapi paths: %w", err)
	}
	rootObj["paths"] = patchedPathsArr

	patchedBodyArr, err := json.Marshal(rootObj)
	if err != nil {
		return nil, fmt.Errorf("encode openapi json: %w", err)
	}
	return patchedBodyArr, nil
}

func newOpenAPISpec(configObj *stconf.ConfigObj) (openapiSpecObj, error) {
	bodyArr, err := api.OpenAPIJSON()
	if err != nil {
		return openapiSpecObj{}, err
	}
	bodyArr, err = patchOpenAPIJSON(bodyArr, openAPIPrefix(configObj))
	if err != nil {
		return openapiSpecObj{}, err
	}
	return openapiSpecObj{body: bodyArr, etag: etagOf("openapi", string(bodyArr))}, nil
}

// // // // // // // // // //

func (obj *Obj) serveOpenAPI(w http.ResponseWriter, r *http.Request, lc listenerCtxObj) {
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
