package archive

import (
	"context"
	"os"

	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

// Extract unpacks an archive into temporary blobs under anti-bomb and anti-traversal limits.
// On error the created blobs are deleted and no result is returned.
func (obj *Obj) Extract(ctx context.Context, requestObj RequestObj) (ResultObj, error) {
	if obj == nil {
		return ResultObj{}, os.ErrInvalid
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateFormat(requestObj.Format); err != nil {
		return ResultObj{}, newRejectedErr(requestObj, cCheckUnsupported, "", err)
	}
	spoolPath, spoolGuardObj, err := validateSpoolPath(requestObj.SpoolPath)
	if err != nil {
		return ResultObj{}, newRejectedErr(requestObj, cCheckSpool, "", err)
	}
	defer func() {
		_ = spoolGuardObj.close()
	}()
	requestObj.SpoolPath = spoolPath
	sourcePath, err := cleanSourcePath(requestObj.SourcePath)
	if err != nil {
		return ResultObj{}, newRejectedErr(requestObj, cCheckMalformed, "", err)
	}
	requestObj.SourcePath = sourcePath
	requestObj.limitsObj = obj.limitsObj
	sizeBytes, err := sourceSize(sourcePath)
	if err != nil {
		return ResultObj{}, newRejectedErr(requestObj, cCheckMalformed, "", err)
	}
	requestObj.SourceSizeBytes = sizeBytes
	if obj.limitsObj.MaxArchiveSize > 0 && sizeBytes > obj.limitsObj.MaxArchiveSize {
		return ResultObj{}, stcode.NewErrArchiveLimitExceeded(nil, "archive_size", requestObj.Key, obj.limitsObj.MaxArchiveSize, sizeBytes, requestObj.Version)
	}

	stateObj := newState(requestObj, spoolGuardObj)
	var resultObj ResultObj
	switch requestObj.Format {
	case FormatZip:
		resultObj, err = obj.extractZip(ctx, requestObj, stateObj)
	case FormatTar:
		resultObj, err = obj.extractTar(ctx, requestObj, stateObj, false)
	case FormatTarGz:
		resultObj, err = obj.extractTar(ctx, requestObj, stateObj, true)
	default:
		err = newRejectedErr(requestObj, cCheckUnsupported, "", nil)
	}
	if err != nil {
		stateObj.cleanupCreated()
		return ResultObj{}, err
	}
	return resultObj, nil
}
