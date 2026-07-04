package static

import (
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/voluminor/yggvault/mod/internal/osfs"
)

// // // // // // // // // //

func quoteETag(hexText string) string {
	if hexText == "" {
		return ""
	}
	return `"` + hexText + `"`
}

func (obj *SnapshotObj) resolvePath(reqPath string) string {
	if reqPath == "" || strings.HasSuffix(reqPath, "/") {
		reqPath = strings.TrimSuffix(reqPath, "/") + "/" + obj.indexFile
	}
	return path.Clean(reqPath)
}

// // // // // // // // // //

func (entryObj fileObj) open() (*os.File, bool) {
	fileObj, err := osfs.OpenNoFollow(entryObj.pathText)
	if err != nil {
		return nil, false
	}

	infoObj, err := fileObj.Stat()
	if err != nil {
		_ = fileObj.Close()
		return nil, false
	}
	if !infoObj.Mode().IsRegular() ||
		!os.SameFile(entryObj.info, infoObj) ||
		infoObj.Size() != entryObj.size ||
		!infoObj.ModTime().Equal(entryObj.modtime) {
		_ = fileObj.Close()
		return nil, false
	}

	return fileObj, true
}

// // // // // // // // // //

// Serve returns a file from the metadata snapshot; directories and "/" resolve to index_file.
// http.ServeContent handles Range, If-Range, status codes, and Last-Modified over the opened file.
// If-None-Match is handled by the ETag header, and files open only after snapshot lookup.
func (obj *SnapshotObj) Serve(w http.ResponseWriter, r *http.Request) {
	cleanPath := obj.resolvePath(r.URL.Path)
	entryObj, ok := obj.files[cleanPath]
	if !ok {
		http.NotFound(w, r)
		return
	}
	fileObj, ok := entryObj.open()
	if !ok {
		http.NotFound(w, r)
		return
	}
	defer fileObj.Close()

	headerObj := w.Header()
	headerObj.Set("Etag", entryObj.etag)
	headerObj.Set("Cache-Control", "public, max-age="+strconv.FormatInt(int64(obj.cacheMaxAge.Seconds()), 10))
	headerObj.Set("Content-Type", entryObj.contentType)

	http.ServeContent(w, r, path.Base(cleanPath), entryObj.modtime, fileObj)
}
