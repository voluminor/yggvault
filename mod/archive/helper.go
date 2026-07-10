package archive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/zeebo/blake3"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/osfs"
	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

type stateObj struct {
	requestObj          RequestObj
	spoolGuardObj       *spoolGuardObj
	headerCount         uint
	unpackedBytes       uint64
	entryArr            []core.StagedEntryObj
	blobByHashObj       map[core.HashObj]core.StagedBlobObj
	symlinkTargetByHash map[core.HashObj][]byte
	droppedSymlinkArr   []string
	createdPathArr      []string
	copyBufferArr       []byte
}

type spoolGuardObj struct {
	infoObj os.FileInfo
	fileObj *os.File
}

type limitFactsObj struct {
	headerCount   uint
	fileBytes     uint64
	pathBytes     uint
	unpackedBytes uint64
}

// //

const cErrorPathMaxBytes = 256

// //

func safeErrorPath(pathText string) string {
	if pathText == "" {
		return ""
	}
	var builderObj strings.Builder
	builderObj.Grow(cErrorPathMaxBytes + 3)
	for len(pathText) > 0 && builderObj.Len() < cErrorPathMaxBytes {
		runeValue, sizeValue := utf8.DecodeRuneInString(pathText)
		if runeValue == utf8.RuneError && sizeValue == 1 {
			runeValue = '?'
		}
		if runeValue < 0x20 || runeValue == 0x7f {
			runeValue = '?'
		}
		runeSize := utf8.RuneLen(runeValue)
		if runeSize < 0 {
			runeValue = '?'
			runeSize = 1
		}
		if builderObj.Len()+runeSize > cErrorPathMaxBytes {
			break
		}
		builderObj.WriteRune(runeValue)
		pathText = pathText[sizeValue:]
	}
	if len(pathText) > 0 {
		builderObj.WriteString("...")
	}
	return builderObj.String()
}

func newRejectedErr(requestObj RequestObj, checkName string, pathText string, cause error) error {
	return stcode.NewErrArchiveRejected(
		cause,
		checkName,
		string(requestObj.Format),
		requestObj.Key,
		safeErrorPath(pathText),
		requestObj.Version,
	)
}

func newLimitsErr(requestObj RequestObj, checkName string, cause error, factsObj limitFactsObj) error {
	limitsObj := requestObj.limitsObj
	var value, maxValue uint64
	switch checkName {
	case cCheckFileBytes:
		value, maxValue = factsObj.fileBytes, limitsObj.MaxArchiveFileBytes
	case cCheckPathBytes:
		value, maxValue = uint64(factsObj.pathBytes), uint64(limitsObj.MaxArchivePathBytes)
	case cCheckHeaderCount, cCheckEntryHardCap:
		value, maxValue = uint64(factsObj.headerCount), uint64(limitsObj.MaxArchiveFiles)
	default:
		value, maxValue = factsObj.unpackedBytes, limitsObj.MaxArchiveUnpackedSize
	}
	return stcode.NewErrArchiveLimitExceeded(cause, checkName, requestObj.Key, maxValue, value, requestObj.Version)
}

func isArchivePolicyErr(err error) bool {
	var limitErr *stcode.ErrArchiveLimitExceededObj
	return errors.As(err, &limitErr)
}

func hasSeenParent(seenObj map[string]struct{}, pathText string) (string, bool) {
	for i := 0; i < len(pathText); i++ {
		if pathText[i] != '/' {
			continue
		}
		if _, ok := seenObj[pathText[:i]]; ok {
			return pathText[:i], true
		}
	}
	return "", false
}

func topSegment(pathText string) (string, bool) {
	indexValue := strings.IndexByte(pathText, '/')
	if indexValue < 0 {
		return pathText, false
	}
	return pathText[:indexValue], true
}

func stripCommonTopDir(entryArr []core.StagedEntryObj) []core.StagedEntryObj {
	if len(entryArr) == 0 {
		return entryArr
	}
	topText, ok := topSegment(entryArr[0].Path)
	if !ok {
		return entryArr
	}
	for i := 1; i < len(entryArr); i++ {
		itemTopText, itemOk := topSegment(entryArr[i].Path)
		if !itemOk || itemTopText != topText {
			return entryArr
		}
	}
	resultArr := make([]core.StagedEntryObj, len(entryArr))
	for i := range entryArr {
		resultArr[i] = entryArr[i]
		resultArr[i].Path = strings.TrimPrefix(entryArr[i].Path, topText+"/")
	}
	return resultArr
}

func validateFinalEntries(requestObj RequestObj, entryArr []core.StagedEntryObj) error {
	if len(entryArr) == 0 {
		return newRejectedErr(requestObj, cCheckEmpty, "", errors.New("source archive has no files"))
	}
	sort.Slice(entryArr, func(i, j int) bool {
		return entryArr[i].Path < entryArr[j].Path
	})
	seenPathObj := make(map[string]struct{}, len(entryArr))
	for i := range entryArr {
		cleanPath, err := util.CleanArchiveEntryPath(entryArr[i].Path)
		if err != nil {
			return newRejectedErr(requestObj, cCheckMalformed, entryArr[i].Path, err)
		}
		if cleanPath != entryArr[i].Path {
			return newRejectedErr(requestObj, cCheckMalformed, entryArr[i].Path, errors.New("entry path is not canonical"))
		}
		if _, ok := seenPathObj[entryArr[i].Path]; ok {
			return newRejectedErr(requestObj, cCheckDuplicatePath, entryArr[i].Path, errors.New("duplicate archive path"))
		}
		if _, ok := hasSeenParent(seenPathObj, entryArr[i].Path); ok {
			return newRejectedErr(requestObj, cCheckPathConflict, entryArr[i].Path, errors.New("archive contains file and child path conflict"))
		}
		seenPathObj[entryArr[i].Path] = struct{}{}
	}
	return nil
}

func validateCleanPath(requestObj RequestObj, pathText string, headerCount uint) (string, error) {
	if len(pathText) > int(requestObj.limitsObj.MaxArchivePathBytes) {
		return "", newLimitsErr(requestObj, cCheckPathBytes, util.ErrArchiveEntryPathTooLong, limitFactsObj{headerCount: headerCount, pathBytes: uint(len(pathText))})
	}
	cleanPath, err := util.CleanArchiveEntryPath(pathText)
	if err != nil {
		return "", newRejectedErr(requestObj, cCheckMalformed, pathText, err)
	}
	return cleanPath, nil
}

func validateSymlinkTarget(requestObj RequestObj, targetArr []byte) error {
	if len(targetArr) > int(requestObj.limitsObj.MaxArchivePathBytes) {
		return newLimitsErr(requestObj, cCheckPathBytes, util.ErrArchiveEntryPathTooLong, limitFactsObj{pathBytes: uint(len(targetArr))})
	}
	return nil
}

func newState(requestObj RequestObj, spoolGuardObj *spoolGuardObj) *stateObj {
	return &stateObj{
		requestObj:          requestObj,
		spoolGuardObj:       spoolGuardObj,
		blobByHashObj:       make(map[core.HashObj]core.StagedBlobObj),
		symlinkTargetByHash: make(map[core.HashObj][]byte),
		copyBufferArr:       make([]byte, cCopyBufferBytes),
	}
}

func (obj *stateObj) countHeader(limitsObj LimitsObj) error {
	obj.headerCount++
	if obj.headerCount > cTreeMaxEntries {
		return newLimitsErr(obj.requestObj, cCheckEntryHardCap, nil, limitFactsObj{headerCount: obj.headerCount})
	}
	if limitsObj.MaxArchiveFiles > 0 && obj.headerCount > limitsObj.MaxArchiveFiles {
		return newLimitsErr(obj.requestObj, cCheckHeaderCount, nil, limitFactsObj{headerCount: obj.headerCount})
	}
	return nil
}

func (obj *stateObj) addUnpacked(limitsObj LimitsObj, sizeBytes uint64) error {
	attemptedBytes, overflowFlag := saturatingAdd(obj.unpackedBytes, sizeBytes)
	if limitsObj.MaxArchiveUnpackedSize > 0 && (overflowFlag || attemptedBytes > limitsObj.MaxArchiveUnpackedSize) {
		return newLimitsErr(obj.requestObj, cCheckUnpackedSize, nil, limitFactsObj{headerCount: obj.headerCount, fileBytes: sizeBytes, unpackedBytes: attemptedBytes})
	}
	obj.unpackedBytes = attemptedBytes
	return nil
}

func (obj *stateObj) cleanupCreated() {
	for _, pathText := range obj.createdPathArr {
		_ = os.Remove(pathText)
	}
}

func (obj *stateObj) addEntry(pathText string, modeText string, hashObj core.HashObj, sizeBytes uint64, filePath string) error {
	if seenObj, ok := obj.blobByHashObj[hashObj]; ok {
		if seenObj.SizeBytes != sizeBytes {
			return newRejectedErr(obj.requestObj, cCheckMalformed, pathText, fmt.Errorf("blob %s has inconsistent sizes", hashObj.Hex()))
		}
		if filePath != "" {
			_ = os.Remove(filePath)
		}
	} else {
		obj.blobByHashObj[hashObj] = core.StagedBlobObj{
			BlobHash:  hashObj,
			SizeBytes: sizeBytes,
			FilePath:  filePath,
		}
	}
	obj.entryArr = append(obj.entryArr, core.StagedEntryObj{
		Path:      pathText,
		Mode:      modeText,
		BlobHash:  hashObj,
		SizeBytes: sizeBytes,
	})
	return nil
}

func (obj *stateObj) writeBytesEntry(limitsObj LimitsObj, pathText string, modeText string, dataArr []byte) error {
	if limitsObj.MaxArchiveFileBytes > 0 && uint64(len(dataArr)) > limitsObj.MaxArchiveFileBytes {
		return newLimitsErr(obj.requestObj, cCheckFileBytes, nil, limitFactsObj{headerCount: obj.headerCount, fileBytes: uint64(len(dataArr)), pathBytes: uint(len(pathText))})
	}
	if err := obj.addUnpacked(limitsObj, uint64(len(dataArr))); err != nil {
		return err
	}
	filePath, err := writeTempBytes(obj.requestObj.SpoolPath, obj.spoolGuardObj, dataArr)
	if err != nil {
		return newRejectedErr(obj.requestObj, cCheckSpool, pathText, err)
	}
	obj.createdPathArr = append(obj.createdPathArr, filePath)
	hashObj := core.HashBytes(dataArr)
	if modeText == core.ModeSymlink {
		obj.symlinkTargetByHash[hashObj] = append([]byte(nil), dataArr...)
	}
	return obj.addEntry(pathText, modeText, hashObj, uint64(len(dataArr)), filePath)
}

func (obj *stateObj) writeReaderEntry(ctx context.Context, limitsObj LimitsObj, pathText string, modeText string, readerObj io.Reader, declaredBytes uint64) error {
	if limitsObj.MaxArchiveFileBytes > 0 && declaredBytes > limitsObj.MaxArchiveFileBytes {
		return newLimitsErr(obj.requestObj, cCheckFileBytes, nil, limitFactsObj{headerCount: obj.headerCount, fileBytes: declaredBytes, pathBytes: uint(len(pathText))})
	}
	fileObj, err := createTempBlob(obj.requestObj.SpoolPath, obj.spoolGuardObj)
	if err != nil {
		return newRejectedErr(obj.requestObj, cCheckSpool, pathText, err)
	}
	filePath := fileObj.Name()
	removeFlag := true
	defer func() {
		_ = fileObj.Close()
		if removeFlag {
			_ = os.Remove(filePath)
		}
	}()

	hasherObj := blake3.New()
	var sizeBytes uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := readerObj.Read(obj.copyBufferArr)
		if n > 0 {
			nextSizeBytes, overflowFlag := saturatingAdd(sizeBytes, uint64(n))
			if limitsObj.MaxArchiveFileBytes > 0 && (overflowFlag || nextSizeBytes > limitsObj.MaxArchiveFileBytes) {
				return newLimitsErr(obj.requestObj, cCheckFileBytes, nil, limitFactsObj{headerCount: obj.headerCount, fileBytes: nextSizeBytes, pathBytes: uint(len(pathText))})
			}
			attemptedBytes, totalOverflowFlag := saturatingAdd(obj.unpackedBytes, nextSizeBytes)
			if limitsObj.MaxArchiveUnpackedSize > 0 && (totalOverflowFlag || attemptedBytes > limitsObj.MaxArchiveUnpackedSize) {
				return newLimitsErr(obj.requestObj, cCheckUnpackedSize, nil, limitFactsObj{headerCount: obj.headerCount, fileBytes: nextSizeBytes, pathBytes: uint(len(pathText)), unpackedBytes: attemptedBytes})
			}
			if _, err = fileObj.Write(obj.copyBufferArr[:n]); err != nil {
				return newRejectedErr(obj.requestObj, cCheckSpool, pathText, err)
			}
			_, _ = hasherObj.Write(obj.copyBufferArr[:n])
			sizeBytes = nextSizeBytes
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return newRejectedErr(obj.requestObj, cCheckMalformed, pathText, readErr)
		}
	}
	if err = obj.addUnpacked(limitsObj, sizeBytes); err != nil {
		return err
	}
	if err = fileObj.Close(); err != nil {
		return newRejectedErr(obj.requestObj, cCheckSpool, pathText, err)
	}
	removeFlag = false
	obj.createdPathArr = append(obj.createdPathArr, filePath)
	return obj.addEntry(pathText, modeText, core.HashFromHasher(hasherObj), sizeBytes, filePath)
}

func (obj *stateObj) filterEscapingSymlinks(entryArr []core.StagedEntryObj) []core.StagedEntryObj {
	filteredArr := entryArr[:0]
	for i := range entryArr {
		if entryArr[i].Mode != core.ModeSymlink {
			filteredArr = append(filteredArr, entryArr[i])
			continue
		}
		target := string(obj.symlinkTargetByHash[entryArr[i].BlobHash])
		if err := util.SymlinkTargetWithinRoot(entryArr[i].Path, target); err != nil {
			obj.droppedSymlinkArr = append(obj.droppedSymlinkArr, entryArr[i].Path)
			continue
		}
		filteredArr = append(filteredArr, entryArr[i])
	}
	if len(obj.droppedSymlinkArr) == 0 {
		return entryArr
	}
	obj.dropOrphanBlobs(filteredArr)
	return filteredArr
}

func (obj *stateObj) dropOrphanBlobs(entryArr []core.StagedEntryObj) {
	usedObj := make(map[core.HashObj]struct{}, len(entryArr))
	for i := range entryArr {
		usedObj[entryArr[i].BlobHash] = struct{}{}
	}
	for hashObj, blobObj := range obj.blobByHashObj {
		if _, ok := usedObj[hashObj]; ok {
			continue
		}
		if blobObj.FilePath != "" {
			_ = os.Remove(blobObj.FilePath)
		}
		delete(obj.blobByHashObj, hashObj)
		delete(obj.symlinkTargetByHash, hashObj)
	}
}

func (obj *stateObj) result() (ResultObj, error) {
	if err := validateSameSpoolPath(obj.requestObj.SpoolPath, obj.spoolGuardObj); err != nil {
		return ResultObj{}, newRejectedErr(obj.requestObj, cCheckSpool, "", err)
	}
	entryArr := stripCommonTopDir(obj.entryArr)
	entryArr = obj.filterEscapingSymlinks(entryArr)
	if err := validateFinalEntries(obj.requestObj, entryArr); err != nil {
		return ResultObj{}, err
	}
	resultObj := ResultObj{
		Entries:         entryArr,
		Blobs:           make([]core.StagedBlobObj, 0, len(obj.blobByHashObj)),
		DroppedSymlinks: append([]string(nil), obj.droppedSymlinkArr...),
	}
	for _, blobObj := range obj.blobByHashObj {
		resultObj.Blobs = append(resultObj.Blobs, blobObj)
	}
	sort.Slice(resultObj.Blobs, func(i, j int) bool {
		return bytes.Compare(resultObj.Blobs[i].BlobHash[:], resultObj.Blobs[j].BlobHash[:]) < 0
	})
	return resultObj, nil
}

func createTempBlob(spoolPath string, spoolGuardObj *spoolGuardObj) (*os.File, error) {
	if err := validateSameSpoolPath(spoolPath, spoolGuardObj); err != nil {
		return nil, err
	}
	fileObj, err := os.CreateTemp(spoolPath, "blob-*")
	if err != nil {
		return nil, err
	}
	if err = validateSameSpoolPath(spoolPath, spoolGuardObj); err != nil {
		filePath := fileObj.Name()
		_ = fileObj.Close()
		_ = os.Remove(filePath)
		return nil, err
	}
	return fileObj, nil
}

func writeTempBytes(spoolPath string, spoolGuardObj *spoolGuardObj, dataArr []byte) (string, error) {
	fileObj, err := createTempBlob(spoolPath, spoolGuardObj)
	if err != nil {
		return "", err
	}
	filePath := fileObj.Name()
	if _, err = fileObj.Write(dataArr); err != nil {
		_ = fileObj.Close()
		_ = os.Remove(filePath)
		return "", err
	}
	if err = fileObj.Close(); err != nil {
		_ = os.Remove(filePath)
		return "", err
	}
	return filePath, nil
}

func (obj *spoolGuardObj) close() error {
	if obj == nil || obj.fileObj == nil {
		return nil
	}
	err := obj.fileObj.Close()
	obj.fileObj = nil
	return err
}

func validateSameSpoolPath(pathText string, spoolGuardObj *spoolGuardObj) error {
	infoObj, err := os.Lstat(pathText)
	if err != nil {
		return err
	}
	if infoObj.Mode()&os.ModeSymlink != 0 || !infoObj.IsDir() {
		return errors.New("spool path is not a regular directory")
	}
	if spoolGuardObj == nil {
		return nil
	}
	if !os.SameFile(infoObj, spoolGuardObj.infoObj) {
		return errors.New("spool path changed during extraction")
	}
	if spoolGuardObj.fileObj != nil {
		openInfoObj, statErr := spoolGuardObj.fileObj.Stat()
		if statErr != nil {
			return statErr
		}
		if !os.SameFile(infoObj, openInfoObj) {
			return errors.New("spool path changed during extraction")
		}
	}
	return nil
}

func validateSpoolPath(pathText string) (string, *spoolGuardObj, error) {
	if strings.TrimSpace(pathText) == "" {
		return "", nil, errors.New("spool path is empty")
	}
	absPath, err := filepath.Abs(pathText)
	if err != nil {
		return "", nil, err
	}
	spoolGuardObj, err := newSpoolGuard(absPath)
	if err != nil {
		return "", nil, err
	}
	return absPath, spoolGuardObj, nil
}

func newSpoolGuard(absPath string) (*spoolGuardObj, error) {
	infoObj, err := os.Lstat(absPath)
	if err != nil {
		return nil, err
	}
	if infoObj.Mode()&os.ModeSymlink != 0 || !infoObj.IsDir() {
		return nil, errors.New("spool path is not a regular directory")
	}

	guardObj := &spoolGuardObj{infoObj: infoObj}
	if runtime.GOOS == "windows" {
		return guardObj, nil
	}

	fileObj, err := osfs.OpenNoFollow(absPath)
	if err != nil {
		return nil, err
	}
	guardObj.fileObj = fileObj

	openInfoObj, err := fileObj.Stat()
	if err != nil {
		_ = guardObj.close()
		return nil, err
	}
	infoObj, err = os.Lstat(absPath)
	if err != nil {
		_ = guardObj.close()
		return nil, err
	}
	if infoObj.Mode()&os.ModeSymlink != 0 || !infoObj.IsDir() {
		_ = guardObj.close()
		return nil, errors.New("spool path is not a regular directory")
	}
	if !os.SameFile(infoObj, openInfoObj) {
		_ = guardObj.close()
		return nil, errors.New("spool path changed during validation")
	}

	guardObj.infoObj = openInfoObj
	return guardObj, nil
}

func sourceSize(pathText string) (uint64, error) {
	infoObj, err := os.Stat(pathText)
	if err != nil {
		return 0, err
	}
	if !infoObj.Mode().IsRegular() {
		return 0, errors.New("source archive is not a regular file")
	}
	return uint64(infoObj.Size()), nil
}

func verifyOpenSourceFile(requestObj RequestObj, fileObj *os.File) error {
	infoObj, err := fileObj.Stat()
	if err != nil {
		return newRejectedErr(requestObj, cCheckMalformed, "", err)
	}
	if !infoObj.Mode().IsRegular() {
		return newRejectedErr(requestObj, cCheckMalformed, "", errors.New("source archive is not a regular file"))
	}
	sizeBytes := uint64(infoObj.Size())
	if requestObj.limitsObj.MaxArchiveSize > 0 && sizeBytes > requestObj.limitsObj.MaxArchiveSize {
		return stcode.NewErrArchiveLimitExceeded(nil, "archive_size", requestObj.Key, requestObj.limitsObj.MaxArchiveSize, sizeBytes, requestObj.Version)
	}
	if sizeBytes != requestObj.SourceSizeBytes {
		return newRejectedErr(requestObj, cCheckMalformed, "", errors.New("source archive changed during extraction"))
	}
	return nil
}

func saturatingAdd(leftValue uint64, rightValue uint64) (uint64, bool) {
	if rightValue > ^uint64(0)-leftValue {
		return ^uint64(0), true
	}
	return leftValue + rightValue, false
}

func cleanSourcePath(pathText string) (string, error) {
	if strings.TrimSpace(pathText) == "" {
		return "", errors.New("source path is empty")
	}
	return filepath.Abs(pathText)
}
