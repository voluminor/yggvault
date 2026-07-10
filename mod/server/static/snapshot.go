package static

import (
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

const (
	cDefaultCacheMaxAge = time.Hour

	cFallbackContentType = "application/octet-stream"
)

// // // // // // // // // //

type fileObj struct {
	pathText    string
	info        os.FileInfo
	size        int64
	etag        string
	contentType string
	modtime     time.Time
}

// SnapshotObj is an immutable metadata snapshot of the static tree.
// After New, fields are read-only, so concurrent serving needs no locks.
type SnapshotObj struct {
	files       map[string]fileObj
	indexFile   string
	cacheMaxAge time.Duration
}

// // // // // // // // // //

func denied(webPath string, denyArr []string) bool {
	lower := strings.ToLower(webPath)
	for _, ruleText := range denyArr {
		rule := strings.ToLower(strings.TrimSpace(ruleText))
		if rule == "" {
			continue
		}
		if strings.HasPrefix(rule, ".") {
			if strings.HasSuffix(lower, rule) {
				return true
			}
			continue
		}
		if lower == rule || strings.HasPrefix(lower, strings.TrimSuffix(rule, "/")+"/") {
			return true
		}
	}
	return false
}

func detectContentType(webPath string) string {
	contentType := mime.TypeByExtension(path.Ext(webPath))
	if contentType == "" {
		return cFallbackContentType
	}
	return contentType
}

// // // // // // // // // //

// New builds an immutable metadata snapshot of dir.
//
// Safety and bounds are fail-closed at startup, not during requests:
//   - symlinks, devices, and sockets are skipped; only regular files are served;
//   - deny rules apply to web paths by extension or prefix, case-insensitively;
//   - per-file and total size are capped by maxSize; overflow is an error.
//
// Map keys are cleaned absolute web paths, so "../" cannot escape the root and serving uses exact lookup only.
func New(dir string, indexFile string, maxSize uint64, deny []string) (*SnapshotObj, error) {
	indexFile = strings.TrimSpace(indexFile)
	if indexFile == "" {
		return nil, fmt.Errorf("static: index file must not be empty")
	}
	if strings.ContainsAny(indexFile, "/\\") {
		return nil, fmt.Errorf("static: index file %q must be a bare file name", indexFile)
	}

	snapshotObj := &SnapshotObj{
		files:       make(map[string]fileObj),
		indexFile:   indexFile,
		cacheMaxAge: cDefaultCacheMaxAge,
	}

	rootClean := filepath.Clean(dir)
	var total uint64

	walkErr := filepath.WalkDir(rootClean, func(pathText string, dirEntry fs.DirEntry, entryErr error) error {
		if entryErr != nil {
			return entryErr
		}
		if dirEntry.IsDir() {
			return nil
		}
		if !dirEntry.Type().IsRegular() {
			return nil
		}

		relText, relErr := filepath.Rel(rootClean, pathText)
		if relErr != nil {
			return relErr
		}
		webPath := path.Clean("/" + filepath.ToSlash(relText))
		if denied(webPath, deny) {
			return nil
		}

		infoObj, infoErr := dirEntry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !infoObj.Mode().IsRegular() {
			return nil
		}
		fileSize := uint64(infoObj.Size())
		if maxSize > 0 && (fileSize > maxSize || total > maxSize-fileSize) {
			return fmt.Errorf("static: snapshot exceeds max_size budget (%d bytes): file %q overflows the limit", maxSize, webPath)
		}
		total += fileSize

		snapshotObj.files[webPath] = fileObj{
			pathText:    pathText,
			info:        infoObj,
			size:        infoObj.Size(),
			etag:        weakStaticETag(webPath, infoObj),
			contentType: detectContentType(webPath),
			modtime:     infoObj.ModTime(),
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	return snapshotObj, nil
}

// SetCacheMaxAge sets Cache-Control max-age for served files.
// Negative values are ignored; call before serving because the snapshot is read-only afterwards.
func (obj *SnapshotObj) SetCacheMaxAge(maxAge time.Duration) {
	if maxAge < 0 {
		return
	}
	obj.cacheMaxAge = maxAge
}

func weakStaticETag(webPath string, infoObj os.FileInfo) string {
	metaText := webPath + "\x00" + strconv.FormatInt(infoObj.Size(), 10) + "\x00" + strconv.FormatInt(infoObj.ModTime().UnixNano(), 10)
	return `W/` + quoteETag(core.HashBytes([]byte(metaText)).Hex())
}
