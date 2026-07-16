package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
)

// // // // // // // // // //

const (
	cArchiveFileMode    = 0o644
	cArchiveSymlinkMode = 0o777
	cMaxTarEntrySize    = uint64(1<<63 - 1)
)

var archiveModTimeObj = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// //

// BlobSourceInterface serves a blob by hash; useFunc is invoked exactly once, synchronously.
type BlobSourceInterface interface {
	UseBlob(ctx context.Context, hashObj core.HashObj, useFunc func([]byte) error) error
}

// TreeSourceInterface adds reading a version tree by hash.
type TreeSourceInterface interface {
	BlobSourceInterface
	ReadTree(ctx context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error)
}

// WriteRequestObj describes writing an archive from a ready-made list of entries.
type WriteRequestObj struct {
	Format  FormatType
	Writer  io.Writer
	Entries []core.TreeEntryObj
	Source  BlobSourceInterface
}

// WriteTreeRequestObj describes writing an archive by tree hash; entries are read from Source.
type WriteTreeRequestObj struct {
	Format   FormatType
	Writer   io.Writer
	TreeHash core.HashObj
	Source   TreeSourceInterface
}

// WriteResultObj records the entry count and the actually written body size.
type WriteResultObj struct {
	EntryCount uint
	BodyBytes  uint64
}

type countingWriterObj struct {
	writerObj    io.Writer
	writtenBytes uint64
	maxBytes     uint64
}

// //

// Write counts written bytes and stops at maxBytes, bounding the archive size.
func (obj *countingWriterObj) Write(dataArr []byte) (int, error) {
	writeArr := dataArr
	limitHit := false
	if obj.maxBytes > 0 {
		if obj.writtenBytes >= obj.maxBytes {
			return 0, errors.New("archive output size exceeds limit")
		}
		remainingBytes := obj.maxBytes - obj.writtenBytes
		if uint64(len(writeArr)) > remainingBytes {
			writeArr = writeArr[:int(remainingBytes)]
			limitHit = true
		}
	}

	n, err := obj.writerObj.Write(writeArr)
	if n < 0 || n > len(writeArr) {
		return n, errors.New("writer returned invalid byte count")
	}
	if n > 0 {
		nextBytes, overflowFlag := saturatingAdd(obj.writtenBytes, uint64(n))
		obj.writtenBytes = nextBytes
		if overflowFlag && err == nil {
			err = errors.New("archive output size overflow")
		}
	}
	if limitHit && err == nil {
		err = errors.New("archive output size exceeds limit")
	}
	if n != len(dataArr) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}

func validateWriteRequest(requestObj WriteRequestObj) error {
	if err := validateFormat(requestObj.Format); err != nil {
		return err
	}
	if requestObj.Writer == nil {
		return errors.New("archive writer is nil")
	}
	if requestObj.Source == nil {
		return errors.New("archive blob source is nil")
	}
	return nil
}

func validateWriteTreeRequest(requestObj WriteTreeRequestObj) error {
	if err := validateFormat(requestObj.Format); err != nil {
		return err
	}
	if requestObj.Writer == nil {
		return errors.New("archive writer is nil")
	}
	if requestObj.Source == nil {
		return errors.New("archive tree source is nil")
	}
	if requestObj.TreeHash.IsZero() {
		return errors.New("tree hash is empty")
	}
	return nil
}

func validateWriteEntryPath(pathText string, maxPathBytes uint) error {
	if maxPathBytes > 0 && len(pathText) > int(maxPathBytes) {
		return fmt.Errorf("%w of %d bytes", util.ErrArchiveEntryPathTooLong, maxPathBytes)
	}
	cleanPath, err := util.CleanArchiveEntryPath(pathText)
	if err != nil {
		return err
	}
	if cleanPath != pathText {
		return errors.New("archive entry path is not canonical")
	}
	return nil
}

func validateWriteSymlinkTarget(pathText string, targetArr []byte, maxPathBytes uint) error {
	if maxPathBytes > 0 && len(targetArr) > int(maxPathBytes) {
		return fmt.Errorf("symlink target for %s exceeds maximum path length", pathText)
	}
	if err := util.SymlinkTargetWithinRoot(pathText, string(targetArr)); err != nil {
		return fmt.Errorf("symlink target for %s: %w", pathText, err)
	}
	return nil
}

func validateWriteEntries(entriesArr []core.TreeEntryObj, limitsObj LimitsObj) ([]core.TreeEntryObj, uint64, error) {
	if len(entriesArr) == 0 {
		return nil, 0, errors.New("archive entries must not be empty")
	}
	if len(entriesArr) > cTreeMaxEntries {
		return nil, 0, errors.New("archive entry count exceeds hard limit")
	}
	if limitsObj.MaxArchiveFiles > 0 && uint(len(entriesArr)) > limitsObj.MaxArchiveFiles {
		return nil, 0, errors.New("archive entry count exceeds limit")
	}

	sortedArr := append([]core.TreeEntryObj(nil), entriesArr...)
	sort.Slice(sortedArr, func(i, j int) bool {
		return sortedArr[i].Path < sortedArr[j].Path
	})

	seenPathObj := make(map[string]struct{}, len(sortedArr))
	var unpackedBytes uint64
	for i := range sortedArr {
		entryObj := sortedArr[i]
		if err := validateWriteEntryPath(entryObj.Path, limitsObj.MaxArchivePathBytes); err != nil {
			return nil, 0, fmt.Errorf("archive entry path %q is invalid: %w", safeErrorPath(entryObj.Path), err)
		}
		if entryObj.Mode != core.ModeFile && entryObj.Mode != core.ModeSymlink {
			return nil, 0, fmt.Errorf("unsupported archive entry mode for %s: %s", safeErrorPath(entryObj.Path), entryObj.Mode)
		}
		if entryObj.BlobHash.IsZero() {
			return nil, 0, fmt.Errorf("archive entry %s has empty blob hash", safeErrorPath(entryObj.Path))
		}
		if limitsObj.MaxArchiveFileBytes > 0 && entryObj.SizeBytes > limitsObj.MaxArchiveFileBytes {
			return nil, 0, fmt.Errorf("archive entry %s exceeds file size limit", safeErrorPath(entryObj.Path))
		}
		nextBytes, overflowFlag := saturatingAdd(unpackedBytes, entryObj.SizeBytes)
		if limitsObj.MaxArchiveUnpackedSize > 0 && (overflowFlag || nextBytes > limitsObj.MaxArchiveUnpackedSize) {
			return nil, 0, errors.New("archive unpacked size exceeds limit")
		}
		unpackedBytes = nextBytes
		if _, ok := seenPathObj[entryObj.Path]; ok {
			return nil, 0, fmt.Errorf("duplicate archive entry path: %s", safeErrorPath(entryObj.Path))
		}
		if parentPath, ok := hasSeenParent(seenPathObj, entryObj.Path); ok {
			return nil, 0, fmt.Errorf("archive path conflict between %s and %s", safeErrorPath(parentPath), safeErrorPath(entryObj.Path))
		}
		seenPathObj[entryObj.Path] = struct{}{}
	}
	return sortedArr, unpackedBytes, nil
}

func writeFull(writerObj io.Writer, dataArr []byte) error {
	n, err := writerObj.Write(dataArr)
	if err != nil {
		return err
	}
	if n != len(dataArr) {
		return io.ErrShortWrite
	}
	return nil
}

func tarEntrySize(entryObj core.TreeEntryObj) (int64, error) {
	if entryObj.SizeBytes > cMaxTarEntrySize {
		return 0, fmt.Errorf("archive entry %s exceeds tar size limit", safeErrorPath(entryObj.Path))
	}
	return int64(entryObj.SizeBytes), nil
}

func contextErr(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func writeBlobBytes(ctx context.Context, writerObj io.Writer, blobArr []byte) error {
	for len(blobArr) > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		writeBytes := len(blobArr)
		if writeBytes > cCopyBufferBytes {
			writeBytes = cCopyBufferBytes
		}
		if err := writeFull(writerObj, blobArr[:writeBytes]); err != nil {
			return err
		}
		blobArr = blobArr[writeBytes:]
	}
	return nil
}

func useEntryBlob(ctx context.Context, sourceObj BlobSourceInterface, entryObj core.TreeEntryObj, maxPathBytes uint, useFunc func([]byte) error) error {
	var callbackMu sync.Mutex
	calledFlag := false
	closedFlag := false
	err := sourceObj.UseBlob(ctx, entryObj.BlobHash, func(blobArr []byte) error {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		if closedFlag {
			return errors.New("blob source called use function after return")
		}
		if calledFlag {
			return errors.New("blob source called use function more than once")
		}
		calledFlag = true
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if uint64(len(blobArr)) != entryObj.SizeBytes {
			return fmt.Errorf("blob size mismatch for %s", safeErrorPath(entryObj.Path))
		}
		if entryObj.Mode == core.ModeSymlink {
			if err := validateWriteSymlinkTarget(entryObj.Path, blobArr, maxPathBytes); err != nil {
				return err
			}
		}
		return useFunc(blobArr)
	})
	callbackMu.Lock()
	defer callbackMu.Unlock()
	closedFlag = true
	if err != nil {
		return err
	}
	if !calledFlag {
		return errors.New("blob source did not call use function")
	}
	return nil
}

func writeZipEntry(ctx context.Context, writerObj *zip.Writer, sourceObj BlobSourceInterface, entryObj core.TreeEntryObj, maxPathBytes uint) error {
	return useEntryBlob(ctx, sourceObj, entryObj, maxPathBytes, func(blobArr []byte) error {
		headerObj := &zip.FileHeader{Name: entryObj.Path}
		headerObj.Modified = archiveModTimeObj
		if entryObj.Mode == core.ModeSymlink {
			headerObj.Method = zip.Store
			headerObj.SetMode(os.ModeSymlink | cArchiveSymlinkMode)
		} else {
			headerObj.Method = zip.Deflate
			headerObj.SetMode(cArchiveFileMode)
		}
		entryWriterObj, err := writerObj.CreateHeader(headerObj)
		if err != nil {
			return err
		}
		return writeBlobBytes(ctx, entryWriterObj, blobArr)
	})
}

func writeZipArchive(ctx context.Context, writerObj io.Writer, entriesArr []core.TreeEntryObj, sourceObj BlobSourceInterface, maxPathBytes uint) error {
	zipWriterObj := zip.NewWriter(writerObj)
	for i := range entriesArr {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := writeZipEntry(ctx, zipWriterObj, sourceObj, entriesArr[i], maxPathBytes); err != nil {
			return err
		}
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	return zipWriterObj.Close()
}

func writeTarFileEntry(ctx context.Context, writerObj *tar.Writer, sourceObj BlobSourceInterface, entryObj core.TreeEntryObj, maxPathBytes uint) error {
	sizeBytes, err := tarEntrySize(entryObj)
	if err != nil {
		return err
	}
	return useEntryBlob(ctx, sourceObj, entryObj, maxPathBytes, func(blobArr []byte) error {
		headerObj := &tar.Header{
			Name:     entryObj.Path,
			Mode:     cArchiveFileMode,
			ModTime:  archiveModTimeObj,
			Size:     sizeBytes,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatPAX,
		}
		if err = writerObj.WriteHeader(headerObj); err != nil {
			return err
		}
		return writeBlobBytes(ctx, writerObj, blobArr)
	})
}

func writeTarSymlinkEntry(ctx context.Context, writerObj *tar.Writer, sourceObj BlobSourceInterface, entryObj core.TreeEntryObj, maxPathBytes uint) error {
	return useEntryBlob(ctx, sourceObj, entryObj, maxPathBytes, func(blobArr []byte) error {
		headerObj := &tar.Header{
			Name:     entryObj.Path,
			Mode:     cArchiveSymlinkMode,
			ModTime:  archiveModTimeObj,
			Typeflag: tar.TypeSymlink,
			Linkname: string(blobArr),
			Format:   tar.FormatPAX,
		}
		return writerObj.WriteHeader(headerObj)
	})
}

func writeTarEntries(ctx context.Context, writerObj *tar.Writer, entriesArr []core.TreeEntryObj, sourceObj BlobSourceInterface, maxPathBytes uint) error {
	for i := range entriesArr {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entriesArr[i].Mode == core.ModeSymlink {
			if err := writeTarSymlinkEntry(ctx, writerObj, sourceObj, entriesArr[i], maxPathBytes); err != nil {
				return err
			}
			continue
		}
		if err := writeTarFileEntry(ctx, writerObj, sourceObj, entriesArr[i], maxPathBytes); err != nil {
			return err
		}
	}
	return nil
}

func writeTarArchive(ctx context.Context, writerObj io.Writer, entriesArr []core.TreeEntryObj, sourceObj BlobSourceInterface, gzipFlag bool, maxPathBytes uint) error {
	if !gzipFlag {
		tarWriterObj := tar.NewWriter(writerObj)
		if err := writeTarEntries(ctx, tarWriterObj, entriesArr, sourceObj, maxPathBytes); err != nil {
			return err
		}
		if err := contextErr(ctx); err != nil {
			return err
		}
		return tarWriterObj.Close()
	}

	gzipWriterObj := gzip.NewWriter(writerObj)
	gzipWriterObj.Header.ModTime = archiveModTimeObj
	tarWriterObj := tar.NewWriter(gzipWriterObj)
	if err := writeTarEntries(ctx, tarWriterObj, entriesArr, sourceObj, maxPathBytes); err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := tarWriterObj.Close(); err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	return gzipWriterObj.Close()
}

// //

// Write builds an archive from the validated entry list under limits.
// Output is deterministic: fixed mode/time, sorted paths, rejected conflicts, duplicates and symlink targets.
func (obj *Obj) Write(ctx context.Context, requestObj WriteRequestObj) (WriteResultObj, error) {
	if obj == nil {
		return WriteResultObj{}, os.ErrInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateWriteRequest(requestObj); err != nil {
		return WriteResultObj{}, err
	}
	entriesArr, _, err := validateWriteEntries(requestObj.Entries, obj.limitsObj)
	if err != nil {
		return WriteResultObj{}, err
	}

	counterObj := &countingWriterObj{writerObj: requestObj.Writer, maxBytes: obj.limitsObj.MaxArchiveSize}
	switch requestObj.Format {
	case FormatZip:
		err = writeZipArchive(ctx, counterObj, entriesArr, requestObj.Source, obj.limitsObj.MaxArchivePathBytes)
	case FormatTar:
		err = writeTarArchive(ctx, counterObj, entriesArr, requestObj.Source, false, obj.limitsObj.MaxArchivePathBytes)
	case FormatTarGz:
		err = writeTarArchive(ctx, counterObj, entriesArr, requestObj.Source, true, obj.limitsObj.MaxArchivePathBytes)
	default:
		err = fmt.Errorf("unsupported archive format: %s", requestObj.Format)
	}
	if err != nil {
		return WriteResultObj{}, err
	}
	return WriteResultObj{
		EntryCount: uint(len(entriesArr)),
		BodyBytes:  counterObj.writtenBytes,
	}, nil
}

// WriteTree reads the tree by hash from Source and builds the archive via Write.
func (obj *Obj) WriteTree(ctx context.Context, requestObj WriteTreeRequestObj) (WriteResultObj, error) {
	if obj == nil {
		return WriteResultObj{}, os.ErrInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateWriteTreeRequest(requestObj); err != nil {
		return WriteResultObj{}, err
	}
	entriesArr, err := requestObj.Source.ReadTree(ctx, requestObj.TreeHash)
	if err != nil {
		return WriteResultObj{}, err
	}
	return obj.Write(ctx, WriteRequestObj{
		Format:  requestObj.Format,
		Writer:  requestObj.Writer,
		Entries: entriesArr,
		Source:  requestObj.Source,
	})
}
