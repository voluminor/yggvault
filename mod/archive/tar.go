package archive

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

var errGzipTrailing = errors.New("gzip stream has trailing data")

type gzipReadCloserObj struct {
	fileObj *os.File
	bufObj  *bufio.Reader
	gzipObj *gzip.Reader
}

type tarStreamReaderObj struct {
	requestObj RequestObj
	readerObj  io.Reader
	maxBytes   uint64
	readBytes  uint64
}

// //

func tarStreamMaxBytes(limitsObj LimitsObj) uint64 {
	if limitsObj.MaxArchiveUnpackedSize == 0 {
		return 0
	}
	overheadPerEntry := uint64(limitsObj.MaxArchivePathBytes) + 1024
	if overheadPerEntry > 0 && uint64(limitsObj.MaxArchiveFiles) > ^uint64(0)/overheadPerEntry {
		return ^uint64(0)
	}
	overheadBytes := uint64(limitsObj.MaxArchiveFiles) * overheadPerEntry
	resultBytes, overflowFlag := saturatingAdd(limitsObj.MaxArchiveUnpackedSize, overheadBytes)
	if overflowFlag {
		return ^uint64(0)
	}
	return resultBytes
}

// Read caps total bytes read from the stream at maxBytes, making tar-stream bombs fail with a policy limit error.
func (obj *tarStreamReaderObj) Read(targetArr []byte) (int, error) {
	if obj.maxBytes > 0 {
		if obj.readBytes >= obj.maxBytes {
			return 0, newLimitsErr(obj.requestObj, cCheckTarStream, nil, limitFactsObj{unpackedBytes: obj.readBytes + 1})
		}
		remainingBytes := obj.maxBytes - obj.readBytes
		if uint64(len(targetArr)) > remainingBytes {
			targetArr = targetArr[:int(remainingBytes)]
		}
	}
	n, err := obj.readerObj.Read(targetArr)
	if n > 0 {
		obj.readBytes += uint64(n)
	}
	return n, err
}

func openTarReader(requestObj RequestObj, gzipFlag bool) (*tar.Reader, io.Closer, error) {
	fileObj, err := os.Open(requestObj.SourcePath)
	if err != nil {
		return nil, nil, newRejectedErr(requestObj, cCheckMalformed, "", err)
	}
	if err = verifyOpenSourceFile(requestObj, fileObj); err != nil {
		_ = fileObj.Close()
		return nil, nil, err
	}
	maxBytes := tarStreamMaxBytes(requestObj.limitsObj)
	if !gzipFlag {
		readerObj := &tarStreamReaderObj{requestObj: requestObj, readerObj: fileObj, maxBytes: maxBytes}
		return tar.NewReader(readerObj), fileObj, nil
	}
	bufObj := bufio.NewReader(fileObj)
	gzipObj, err := gzip.NewReader(bufObj)
	if err != nil {
		_ = fileObj.Close()
		return nil, nil, newRejectedErr(requestObj, cCheckMalformed, "", err)
	}
	gzipObj.Multistream(false)
	closerObj := &gzipReadCloserObj{
		fileObj: fileObj,
		bufObj:  bufObj,
		gzipObj: gzipObj,
	}
	readerObj := &tarStreamReaderObj{requestObj: requestObj, readerObj: gzipObj, maxBytes: maxBytes}
	return tar.NewReader(readerObj), closerObj, nil
}

// Close closes gzip and file readers and reports trailing data after the gzip stream as malformed abuse.
func (obj *gzipReadCloserObj) Close() error {
	err := obj.gzipObj.Close()
	if _, peekErr := obj.bufObj.Peek(1); peekErr == nil {
		err = errors.Join(err, errGzipTrailing)
	} else if !errors.Is(peekErr, io.EOF) {
		err = errors.Join(err, peekErr)
	}
	return errors.Join(err, obj.fileObj.Close())
}

func tarEntryMode(headerObj *tar.Header) (string, bool, error) {
	switch headerObj.Typeflag {
	case tar.TypeDir:
		return "", true, nil
	case tar.TypeReg:
		return core.ModeFile, false, nil
	case tar.TypeSymlink:
		return core.ModeSymlink, false, nil
	default:
		return "", false, fmt.Errorf("unsupported tar entry type: %d", headerObj.Typeflag)
	}
}

// //

func (obj *Obj) extractTar(ctx context.Context, requestObj RequestObj, stateObj *stateObj, gzipFlag bool) (ResultObj, error) {
	readerObj, closerObj, err := openTarReader(requestObj, gzipFlag)
	if err != nil {
		return ResultObj{}, err
	}
	closedFlag := false
	defer func() {
		if !closedFlag {
			_ = closerObj.Close()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ResultObj{}, ctx.Err()
		default:
		}
		headerObj, nextErr := readerObj.Next()
		if errors.Is(nextErr, io.EOF) {
			if closeErr := closerObj.Close(); closeErr != nil {
				closedFlag = true
				if isArchivePolicyErr(closeErr) {
					return ResultObj{}, closeErr
				}
				if gzipFlag && errors.Is(closeErr, errGzipTrailing) {
					return ResultObj{}, newRejectedErr(requestObj, cCheckGzipTrailing, "", closeErr)
				}
				return ResultObj{}, newRejectedErr(requestObj, cCheckMalformed, "", closeErr)
			}
			closedFlag = true
			return stateObj.result()
		}
		if nextErr != nil {
			if isArchivePolicyErr(nextErr) {
				return ResultObj{}, nextErr
			}
			return ResultObj{}, newRejectedErr(requestObj, cCheckMalformed, "", nextErr)
		}
		if err = stateObj.countHeader(requestObj.limitsObj); err != nil {
			return ResultObj{}, err
		}
		if headerObj.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		pathText, cleanErr := validateCleanPath(requestObj, headerObj.Name, stateObj.headerCount)
		if cleanErr != nil {
			return ResultObj{}, cleanErr
		}
		modeText, dirFlag, modeErr := tarEntryMode(headerObj)
		if modeErr != nil {
			return ResultObj{}, newRejectedErr(requestObj, cCheckUnsupported, headerObj.Name, modeErr)
		}
		if dirFlag {
			continue
		}
		if headerObj.Size < 0 {
			return ResultObj{}, newRejectedErr(requestObj, cCheckMalformed, headerObj.Name, errors.New("tar entry has negative size"))
		}
		if modeText == core.ModeSymlink {
			targetArr := []byte(headerObj.Linkname)
			if err = validateSymlinkTarget(requestObj, targetArr); err != nil {
				return ResultObj{}, err
			}
			if err = stateObj.writeBytesEntry(requestObj.limitsObj, pathText, modeText, targetArr); err != nil {
				return ResultObj{}, err
			}
			continue
		}
		if err = stateObj.writeReaderEntry(ctx, requestObj.limitsObj, pathText, modeText, readerObj, uint64(headerObj.Size)); err != nil {
			return ResultObj{}, err
		}
	}
}
