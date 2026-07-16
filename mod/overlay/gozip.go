package overlay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"time"

	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

var errSymlinkInGoModule = errors.New("go module contains a symlink, which the go module zip format forbids")

const cRewriteCacheBudgetFallback = 64 << 20

const cMaxCachedRewriteBytes = 4 << 20

type rewriteCacheObj struct {
	entryMap  map[core.HashObj][]byte
	usedBytes uint64
	budget    uint64
}

func newRewriteCache(budget uint64) *rewriteCacheObj {
	if budget == 0 {
		budget = cRewriteCacheBudgetFallback
	}
	return &rewriteCacheObj{entryMap: make(map[core.HashObj][]byte), budget: budget}
}

func (c *rewriteCacheObj) get(hashObj core.HashObj) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	dataArr, ok := c.entryMap[hashObj]
	return dataArr, ok
}

func (c *rewriteCacheObj) fits(n int) bool {
	return c != nil && c.usedBytes+uint64(n) <= c.budget
}

func (c *rewriteCacheObj) put(hashObj core.HashObj, dataArr []byte) {
	if c == nil {
		return
	}
	if _, ok := c.entryMap[hashObj]; ok {
		return
	}
	if !c.fits(len(dataArr)) {
		return
	}
	c.entryMap[hashObj] = dataArr
	c.usedBytes += uint64(len(dataArr))
}

type goFileInfoObj struct {
	name string
	size int64
}

// Name returns the file base name.
func (i goFileInfoObj) Name() string { return i.name }

// Size returns file size in bytes.
func (i goFileInfoObj) Size() int64 { return i.size }

// Mode returns fixed mode 0644.
func (i goFileInfoObj) Mode() fs.FileMode { return 0o644 }

// ModTime returns zero time for deterministic output.
func (i goFileInfoObj) ModTime() time.Time { return time.Time{} }

// IsDir is always false because only files are exposed.
func (i goFileInfoObj) IsDir() bool { return false }

// Sys returns no platform-specific data.
func (i goFileInfoObj) Sys() any { return nil }

type goFileObj struct {
	ctx        context.Context
	st         StorageInterface
	entry      core.TreeEntryObj
	rewriteSet map[core.HashObj]struct{}
	oldArr     []byte
	newArr     []byte
	cache      *rewriteCacheObj
}

func (f *goFileObj) content() ([]byte, error) {
	if _, ok := f.rewriteSet[f.entry.BlobHash]; ok {
		if cachedArr, hit := f.cache.get(f.entry.BlobHash); hit {
			return cachedArr, nil
		}
		dataArr, err := f.st.ReadBlob(f.ctx, f.entry.BlobHash)
		if err != nil {
			return nil, err
		}
		return rewriteContent(dataArr, f.oldArr, f.newArr), nil
	}
	return f.st.ReadBlob(f.ctx, f.entry.BlobHash)
}

// Path returns the entry path inside the module.
func (f *goFileObj) Path() string { return f.entry.Path }

// Lstat returns entry size. For rewrite-set entries, it computes post-rewrite size and caches small rewritten content
// for the following Open.
func (f *goFileObj) Lstat() (fs.FileInfo, error) {
	size := f.entry.SizeBytes
	if _, ok := f.rewriteSet[f.entry.BlobHash]; ok {
		if cachedArr, hit := f.cache.get(f.entry.BlobHash); hit {
			size = uint64(len(cachedArr))
			return goFileInfoObj{name: path.Base(f.entry.Path), size: int64(size)}, nil
		}
		dataArr, err := f.st.ReadBlob(f.ctx, f.entry.BlobHash)
		if err != nil {
			return nil, err
		}
		postSize := rewrittenSize(dataArr, f.oldArr, f.newArr)
		if postSize <= cMaxCachedRewriteBytes && f.cache.fits(postSize) {
			f.cache.put(f.entry.BlobHash, rewriteContent(dataArr, f.oldArr, f.newArr))
		}
		size = uint64(postSize)
	}
	return goFileInfoObj{name: path.Base(f.entry.Path), size: int64(size)}, nil
}

// Open returns entry content, rewritten for rewrite-set entries, by reading the blob into memory.
func (f *goFileObj) Open() (io.ReadCloser, error) {
	dataArr, err := f.content()
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(dataArr)), nil
}

// // // // // // // // // //

type goZipBuilderObj struct {
	st           StorageInterface
	modulePath   string
	version      string
	treeHash     core.HashObj
	rewriteSet   map[core.HashObj]struct{}
	oldArr       []byte
	newArr       []byte
	maxFileBytes uint64
}

// Build writes the Go module proxy zip for a version. Tree symlinks degrade the version because the format forbids
// them; x/mod/zip errors are wrapped consistently for degraded/404 mapping.
func (b goZipBuilderObj) Build(ctx context.Context, writerObj io.Writer) error {
	if !validRefSegment(b.version) {
		return fmt.Errorf("invalid version for go module zip: %q: %w", b.version, errInvalidGoVersion)
	}
	entriesArr, err := b.st.ReadTree(ctx, b.treeHash)
	if err != nil {
		return err
	}
	cacheObj := newRewriteCache(b.maxFileBytes)
	filesArr := make([]modzip.File, 0, len(entriesArr))
	for i := range entriesArr {
		entryObj := entriesArr[i]
		if entryObj.Mode != core.ModeFile {
			return errSymlinkInGoModule
		}
		filesArr = append(filesArr, &goFileObj{
			ctx:        ctx,
			st:         b.st,
			entry:      entryObj,
			rewriteSet: b.rewriteSet,
			oldArr:     b.oldArr,
			newArr:     b.newArr,
			cache:      cacheObj,
		})
	}
	if err := modzip.Create(writerObj, module.Version{Path: b.modulePath, Version: b.version}, filesArr); err != nil {
		return fmt.Errorf("go module zip build failed for %s@%s: %w", b.modulePath, b.version, err)
	}
	return nil
}

// GoModuleZipBuilder builds @v/<version>.zip for a key. Module path is always our target, and content is rewritten by
// the shared rewritePlan. Call only for GoPublishable versions.
func (obj *Obj) GoModuleZipBuilder(
	st StorageInterface,
	key string,
	version string,
	treeHashObj core.HashObj,
	detectionObj core.DetectionObj,
	candidateObj *CandidateObj,
	rewriteArr []core.HashObj,
	listenerCtxObj ListenerCtxObj,
) ArtifactBuilderInterface {
	planObj := obj.rewritePlan(key, version, detectionObj, candidateObj, listenerCtxObj)
	rewriteSet := map[core.HashObj]struct{}{}
	var oldArr, newArr []byte
	if planObj.Rewrite {
		rewriteSet = hashSetOf(rewriteArr)
		oldArr = planObj.OldArr
		newArr = planObj.NewArr
	}
	return goZipBuilderObj{
		st:           st,
		modulePath:   obj.targetModulePath(key, version, listenerCtxObj),
		version:      version,
		treeHash:     treeHashObj,
		rewriteSet:   rewriteSet,
		oldArr:       oldArr,
		newArr:       newArr,
		maxFileBytes: obj.maxFileBytes,
	}
}
