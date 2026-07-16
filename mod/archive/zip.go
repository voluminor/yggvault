package archive

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
)

// // // // // // // // // //

const (
	cMaxZipCompressionRatio = 1000
	cMinRatioCheckBytes     = 4096
)

// //

func zipEntryCountErr(requestObj RequestObj, countValue uint64) error {
	if countValue > uint64(cTreeMaxEntries) {
		return newLimitsErr(requestObj, cCheckEntryHardCap, nil, limitFactsObj{headerCount: cTreeMaxEntries + 1})
	}
	filesValue := uint(countValue)
	if countValue > uint64(^uint(0)) {
		filesValue = ^uint(0)
	}
	return newLimitsErr(requestObj, cCheckHeaderCount, nil, limitFactsObj{headerCount: filesValue})
}

func zipMode(fileObj *zip.File) (string, bool, error) {
	if strings.HasSuffix(fileObj.Name, "/") || fileObj.FileInfo().IsDir() {
		return "", true, nil
	}
	modeObj := fileObj.FileInfo().Mode()
	if modeObj&os.ModeSymlink != 0 {
		return core.ModeSymlink, false, nil
	}
	if modeObj.Type() == 0 {
		return core.ModeFile, false, nil
	}
	return "", false, fmt.Errorf("unsupported zip entry mode: %s", modeObj.String())
}

func readZipSymlinkTarget(fileObj *zip.File, maxBytes uint) ([]byte, error) {
	readerObj, err := fileObj.Open()
	if err != nil {
		return nil, err
	}
	defer readerObj.Close()
	targetArr, err := io.ReadAll(io.LimitReader(readerObj, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if uint(len(targetArr)) > maxBytes {
		return nil, util.ErrArchiveEntryPathTooLong
	}
	return targetArr, nil
}

// //

func (obj *Obj) extractZip(ctx context.Context, requestObj RequestObj, stateObj *stateObj) (ResultObj, error) {
	fileObj, err := os.Open(requestObj.SourcePath)
	if err != nil {
		return ResultObj{}, newRejectedErr(requestObj, cCheckMalformed, "", err)
	}
	defer fileObj.Close()
	if err = verifyOpenSourceFile(requestObj, fileObj); err != nil {
		return ResultObj{}, err
	}
	readerObj, err := zip.NewReader(fileObj, int64(requestObj.SourceSizeBytes))
	if err != nil {
		return ResultObj{}, newRejectedErr(requestObj, cCheckMalformed, "", err)
	}
	if requestObj.limitsObj.MaxArchiveFiles > 0 && uint64(len(readerObj.File)) > uint64(requestObj.limitsObj.MaxArchiveFiles) {
		return ResultObj{}, zipEntryCountErr(requestObj, uint64(len(readerObj.File)))
	}
	for _, fileItemObj := range readerObj.File {
		select {
		case <-ctx.Done():
			return ResultObj{}, ctx.Err()
		default:
		}
		if err = stateObj.countHeader(requestObj.limitsObj); err != nil {
			return ResultObj{}, err
		}
		pathText, cleanErr := validateCleanPath(requestObj, fileItemObj.Name, stateObj.headerCount)
		if cleanErr != nil {
			return ResultObj{}, cleanErr
		}
		if fileItemObj.Flags&0x1 != 0 {
			return ResultObj{}, newRejectedErr(requestObj, cCheckUnsupported, pathText, errors.New("encrypted zip entry is not supported"))
		}
		if fileItemObj.Method != zip.Store && fileItemObj.Method != zip.Deflate {
			return ResultObj{}, newRejectedErr(requestObj, cCheckUnsupported, pathText, fmt.Errorf("zip method %d is not supported", fileItemObj.Method))
		}
		modeText, dirFlag, modeErr := zipMode(fileItemObj)
		if modeErr != nil {
			return ResultObj{}, newRejectedErr(requestObj, cCheckUnsupported, pathText, modeErr)
		}
		if dirFlag {
			continue
		}
		if modeText == core.ModeSymlink {
			targetArr, readErr := readZipSymlinkTarget(fileItemObj, requestObj.limitsObj.MaxArchivePathBytes)
			if readErr != nil {
				if errors.Is(readErr, util.ErrArchiveEntryPathTooLong) {
					return ResultObj{}, newLimitsErr(requestObj, cCheckPathBytes, readErr, limitFactsObj{headerCount: stateObj.headerCount, pathBytes: uint(requestObj.limitsObj.MaxArchivePathBytes) + 1})
				}
				return ResultObj{}, newRejectedErr(requestObj, cCheckMalformed, fileItemObj.Name, readErr)
			}
			if err = stateObj.writeBytesEntry(requestObj.limitsObj, pathText, modeText, targetArr); err != nil {
				return ResultObj{}, err
			}
			continue
		}
		if fileItemObj.CompressedSize64 >= cMinRatioCheckBytes &&
			fileItemObj.UncompressedSize64/cMaxZipCompressionRatio > fileItemObj.CompressedSize64 {
			return ResultObj{}, newLimitsErr(requestObj, cCheckCompressionRatio, nil, limitFactsObj{
				headerCount:   stateObj.headerCount,
				fileBytes:     fileItemObj.UncompressedSize64,
				pathBytes:     uint(len(pathText)),
				unpackedBytes: fileItemObj.UncompressedSize64,
			})
		}
		entryReaderObj, openErr := fileItemObj.Open()
		if openErr != nil {
			return ResultObj{}, newRejectedErr(requestObj, cCheckMalformed, fileItemObj.Name, openErr)
		}
		err = stateObj.writeReaderEntry(ctx, requestObj.limitsObj, pathText, modeText, entryReaderObj, fileItemObj.UncompressedSize64)
		closeErr := entryReaderObj.Close()
		if err != nil {
			return ResultObj{}, err
		}
		if closeErr != nil {
			return ResultObj{}, newRejectedErr(requestObj, cCheckMalformed, fileItemObj.Name, closeErr)
		}
	}
	return stateObj.result()
}
