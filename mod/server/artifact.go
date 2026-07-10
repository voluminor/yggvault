package server

import (
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/server/artifactio"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

type rangeSpecObj struct {
	start        int64
	length       int64
	contentRange string
	partial      bool
	satisfiable  bool
}

// // // // // // // // // //

func decideRange(rangeHeader api.OptString, ifRange api.OptString, etag string, size int64) rangeSpecObj {
	full := rangeSpecObj{start: 0, length: size, satisfiable: true}
	if !rangeHeader.Set || strings.TrimSpace(rangeHeader.Value) == "" {
		return full
	}
	if ifRange.Set && strings.TrimSpace(ifRange.Value) != etag {
		return full
	}
	const prefix = "bytes="
	spec := strings.TrimSpace(rangeHeader.Value)
	if !strings.HasPrefix(spec, prefix) {
		return full
	}
	spec = strings.TrimSpace(spec[len(prefix):])
	if spec == "" || strings.Contains(spec, ",") {
		return full
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return full
	}
	startText, endText := strings.TrimSpace(spec[:dash]), strings.TrimSpace(spec[dash+1:])

	var start, end int64
	switch {
	case startText == "":
		n, err := strconv.ParseInt(endText, 10, 64)
		if err != nil || n <= 0 {
			return full
		}
		if n > size {
			n = size
		}
		start, end = size-n, size-1
	case endText == "":
		s, err := strconv.ParseInt(startText, 10, 64)
		if err != nil || s < 0 {
			return full
		}
		start, end = s, size-1
	default:
		s, errS := strconv.ParseInt(startText, 10, 64)
		e, errE := strconv.ParseInt(endText, 10, 64)
		if errS != nil || errE != nil || s < 0 || e < s {
			return full
		}
		start, end = s, e
		if end > size-1 {
			end = size - 1
		}
	}

	if size == 0 || start >= size {
		return rangeSpecObj{contentRange: "bytes */" + strconv.FormatInt(size, 10)}
	}
	return rangeSpecObj{
		start:        start,
		length:       end - start + 1,
		contentRange: "bytes " + strconv.FormatInt(start, 10) + "-" + strconv.FormatInt(end, 10) + "/" + strconv.FormatInt(size, 10),
		partial:      true,
		satisfiable:  true,
	}
}

// // // // // // // // // //

type artifactGateObj struct {
	etag        string
	spec        rangeSpecObj
	notModified bool
	headOnly    bool
}

func gateArtifact(ctx context.Context, artObj core.ArtifactObj, ifNoneMatch api.OptString, rangeHeader api.OptString, ifRange api.OptString) artifactGateObj {
	etag := artifactio.ETag(artObj)
	if condMatch(ifNoneMatch, etag) {
		return artifactGateObj{etag: etag, notModified: true}
	}
	if headOnlyFrom(ctx) {
		return artifactGateObj{etag: etag, headOnly: true}
	}
	return artifactGateObj{etag: etag, spec: decideRange(rangeHeader, ifRange, etag, int64(artObj.SizeBytes))}
}

// // // // // // // // // //

func partialResp(body *artifactio.BodyObj, spec rangeSpecObj, etag string, cacheControl string, lastModified string, disposition string) (*api.PartialContentRespObjHeaders, error) {
	if _, seekErr := body.Seek(spec.start, io.SeekStart); seekErr != nil {
		_ = body.Close()
		return nil, serr.ErrUnavailable
	}
	headersObj := &api.PartialContentRespObjHeaders{
		AcceptRanges:  api.NewOptString("bytes"),
		CacheControl:  api.NewOptString(cacheControl),
		ContentLength: api.NewOptInt64(spec.length),
		ContentRange:  api.NewOptString(spec.contentRange),
		ETag:          api.NewOptString(etag),
		LastModified:  api.NewOptString(lastModified),
		Response:      api.PartialContentRespObj{Data: &rangeBodyObj{BodyObj: body, remaining: spec.length}},
	}
	if disposition != "" {
		headersObj.ContentDisposition = api.NewOptString(disposition)
	}
	return headersObj, nil
}

// // // // // // // // // //

type rangeBodyObj struct {
	*artifactio.BodyObj
	remaining int64
}

// Read returns at most remaining bytes, then EOF even if the underlying body continues.
func (obj *rangeBodyObj) Read(bufArr []byte) (int, error) {
	if obj.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(bufArr)) > obj.remaining {
		bufArr = bufArr[:obj.remaining]
	}
	n, err := obj.BodyObj.Read(bufArr)
	obj.remaining -= int64(n)
	return n, err
}
