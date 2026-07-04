package storage

import (
	"errors"
	"fmt"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/mod/storage/treecodec"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func saturatingAddUint64(leftValue uint64, rightValue uint64) (uint64, bool) {
	if rightValue > ^uint64(0)-leftValue {
		return ^uint64(0), true
	}
	return leftValue + rightValue, false
}

// //

func newArchiveLimitsErr(cause error, checkName string, files uint, key string, maxFiles uint, maxFileBytes uint64, maxPathBytes uint, maxUnpackedBytes uint64, fileBytes uint64, pathBytes uint, unpackedBytes uint64, version string) error {
	var value, maxValue uint64
	switch checkName {
	case cArchiveCheckFileBytes:
		value, maxValue = fileBytes, maxFileBytes
	case cArchiveCheckPathBytes:
		value, maxValue = uint64(pathBytes), uint64(maxPathBytes)
	case cArchiveCheckUnpackedSize:
		value, maxValue = unpackedBytes, maxUnpackedBytes
	default:
		value, maxValue = uint64(files), uint64(maxFiles)
	}
	return stcode.NewErrArchiveLimitExceeded(cause, checkName, key, maxValue, value, version)
}

// // // // // // // // // //

func normalizeInputEntries(entriesArr []core.InputEntryObj, key string, version string, maxPathBytes uint, maxFileBytes uint64, maxFiles uint, maxUnpackedBytes uint64) ([]core.TreeEntryObj, map[core.HashObj][]byte, error) {
	if len(entriesArr) == 0 {
		return nil, nil, errors.New("entries must not be empty")
	}
	filesCount := uint(len(entriesArr))
	if len(entriesArr) > treecodec.MaxEntries {
		return nil, nil, newArchiveLimitsErr(nil, cArchiveCheckEntryHardCap, filesCount, key, treecodec.MaxEntries, maxFileBytes, maxPathBytes, maxUnpackedBytes, 0, 0, 0, version)
	}
	if maxFiles > 0 && filesCount > maxFiles {
		return nil, nil, newArchiveLimitsErr(nil, cArchiveCheckFileCount, filesCount, key, maxFiles, maxFileBytes, maxPathBytes, maxUnpackedBytes, 0, 0, 0, version)
	}

	treeArr := make([]core.TreeEntryObj, 0, len(entriesArr))
	contentByHashObj := make(map[core.HashObj][]byte, len(entriesArr))
	sizeByHashObj := make(map[core.HashObj]uint64, len(entriesArr))
	var totalBytes uint64
	for i := range entriesArr {
		inputObj := entriesArr[i]
		pathText, err := validateEntryPath(inputObj.Path, maxPathBytes)
		if err != nil {
			if errors.Is(err, util.ErrArchiveEntryPathTooLong) {
				return nil, nil, newArchiveLimitsErr(err, cArchiveCheckPathBytes, filesCount, key, maxFiles, maxFileBytes, maxPathBytes, maxUnpackedBytes, 0, uint(len(inputObj.Path)), 0, version)
			}
			return nil, nil, fmt.Errorf("entry %q: %w", inputObj.Path, err)
		}
		modeText, err := validateEntryMode(inputObj.Mode)
		if err != nil {
			return nil, nil, err
		}

		var hashObj core.HashObj
		sizeBytes := inputObj.SizeBytes
		if inputObj.Content != nil {
			sizeBytes = uint64(len(inputObj.Content))
			if maxFileBytes > 0 && sizeBytes > maxFileBytes {
				return nil, nil, newArchiveLimitsErr(nil, cArchiveCheckFileBytes, filesCount, key, maxFiles, maxFileBytes, maxPathBytes, maxUnpackedBytes, sizeBytes, uint(len(pathText)), 0, version)
			}
			hashObj = core.HashBytes(inputObj.Content)
			if _, ok := contentByHashObj[hashObj]; !ok {
				contentByHashObj[hashObj] = append([]byte(nil), inputObj.Content...)
			}
		} else {
			hashObj = inputObj.BlobHash
			if hashObj.IsZero() {
				return nil, nil, fmt.Errorf("entry %s has no content or blob hash", pathText)
			}
			if maxFileBytes > 0 && sizeBytes > maxFileBytes {
				return nil, nil, newArchiveLimitsErr(nil, cArchiveCheckFileBytes, filesCount, key, maxFiles, maxFileBytes, maxPathBytes, maxUnpackedBytes, sizeBytes, uint(len(pathText)), 0, version)
			}
		}
		seenSizeBytes, seenFlag := sizeByHashObj[hashObj]
		if seenFlag && seenSizeBytes != sizeBytes {
			return nil, nil, fmt.Errorf("blob %s has inconsistent entry sizes", hashObj.Hex())
		}
		if !seenFlag {
			sizeByHashObj[hashObj] = sizeBytes
		}
		attemptedBytes, overflowFlag := saturatingAddUint64(totalBytes, sizeBytes)
		if maxUnpackedBytes > 0 {
			if overflowFlag || attemptedBytes > maxUnpackedBytes {
				return nil, nil, newArchiveLimitsErr(nil, cArchiveCheckUnpackedSize, filesCount, key, maxFiles, maxFileBytes, maxPathBytes, maxUnpackedBytes, sizeBytes, uint(len(pathText)), attemptedBytes, version)
			}
		}
		totalBytes = attemptedBytes

		treeArr = append(treeArr, core.TreeEntryObj{
			Path:      pathText,
			Mode:      modeText,
			SizeBytes: sizeBytes,
			BlobHash:  hashObj,
		})
	}

	return treeArr, contentByHashObj, nil
}
