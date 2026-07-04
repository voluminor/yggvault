package storage

import (
	"context"
	"errors"
	"os"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/internal/hotverify"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

const cArtifactAbsoluteMaxBytes = 1024 * 1024 * 1024

// //

func artifactKeyFromObj(artifactObj core.ArtifactObj) core.ArtifactKeyObj {
	return core.ArtifactKeyObj{
		MaterializerID: artifactObj.MaterializerID,
		ArtifactKind:   artifactObj.ArtifactKind,
		ListenerID:     artifactObj.ListenerID,
		Key:            artifactObj.Key,
		Version:        artifactObj.Version,
	}
}

func newArtifactSizeErr(artifactObj core.ArtifactObj, checkName string, maxBytes uint64, sizeBytes uint64) error {
	return stcode.NewErrArtifactSizeExceeded(
		artifactObj.ArtifactKind,
		checkName,
		artifactObj.Key,
		artifactObj.ListenerID,
		artifactObj.MaterializerID,
		maxBytes,
		sizeBytes,
		artifactObj.Version,
	)
}

func newArtifactBuildErr(keyObj core.ArtifactKeyObj, cause error, checkName string, expectedHash string, actualHash string, expectedSize uint64, actualSize uint64) error {
	return stcode.NewErrArtifactBuildFailed(
		actualHash,
		actualSize,
		keyObj.ArtifactKind,
		cause,
		checkName,
		expectedHash,
		expectedSize,
		keyObj.Key,
		keyObj.ListenerID,
		keyObj.MaterializerID,
		keyObj.Version,
	)
}

func (obj *Obj) maxArtifactBytes() uint64 {
	maxBytes := uint64(obj.configObj.Storage.ArchiveLimits.Size.Compressed)
	hotBytes := uint64(obj.configObj.Storage.Hot.MaxSize)
	if maxBytes == 0 || hotBytes > 0 && hotBytes < maxBytes {
		maxBytes = hotBytes
	}
	if maxBytes == 0 || cArtifactAbsoluteMaxBytes < maxBytes {
		maxBytes = cArtifactAbsoluteMaxBytes
	}
	return maxBytes
}

func (obj *Obj) validateArtifactSize(artifactObj core.ArtifactObj) error {
	maxBytes := obj.maxArtifactBytes()
	if maxBytes > 0 && artifactObj.SizeBytes > maxBytes {
		return newArtifactSizeErr(artifactObj, cArtifactCheckMetadataSize, maxBytes, artifactObj.SizeBytes)
	}
	return nil
}

func (obj *Obj) prepareArtifacts(ctx context.Context, key string, version string, artifactArr []core.ArtifactObj) ([]core.ArtifactObj, error) {
	resultArr := make([]core.ArtifactObj, 0, len(artifactArr))
	for i := range artifactArr {
		artifactObj := artifactArr[i]
		artifactObj.Key = key
		artifactObj.Version = version
		artifactObj.ListenerID = validateListener(artifactObj.ListenerID)
		if artifactObj.FormatVersion == 0 {
			artifactObj.FormatVersion = cDefaultFormat
		}
		if artifactObj.ETag == "" {
			artifactObj.ETag = etagFromHash(artifactObj.BodyHash)
		}
		if err := validateETag(artifactObj.ETag); err != nil {
			return nil, err
		}
		if err := validateDegradedReason(artifactObj.DegradedReason); err != nil {
			return nil, err
		}
		if artifactObj.BodyHash.IsZero() {
			return nil, errors.New("artifact body hash is empty")
		}
		if err := obj.validateArtifactSize(artifactObj); err != nil {
			return nil, err
		}
		if artifactObj.FilePath != "" {
			filePath, err := obj.validateHotPath(artifactObj.FilePath)
			if err != nil {
				return nil, err
			}
			infoObj, err := os.Lstat(filePath)
			if err != nil {
				return nil, err
			}
			sha256Arr, sha1Arr, err := hotverify.VerifyHotFileDigests(ctx, filePath, infoObj, artifactObj.BodyHash, artifactObj.SizeBytes)
			if err != nil {
				var mismatchObj *hotverify.MismatchObj
				if errors.As(err, &mismatchObj) {
					return nil, newArtifactBuildErr(
						artifactKeyFromObj(artifactObj),
						err,
						cArtifactCheckHotFile,
						mismatchObj.ExpectedHashText(),
						mismatchObj.ActualHashText(),
						mismatchObj.ExpectedSizeBytes(),
						mismatchObj.ActualSizeBytes(),
					)
				}
				if errors.Is(err, hotverify.ErrHashMismatch) || errors.Is(err, hotverify.ErrSizeMismatch) {
					return nil, newArtifactBuildErr(artifactKeyFromObj(artifactObj), err, cArtifactCheckHotFile, artifactObj.BodyHash.Hex(), "", artifactObj.SizeBytes, uint64(infoObj.Size()))
				}
				return nil, err
			}
			artifactObj.FilePath = filePath
			artifactObj.BodySha256 = sha256Arr
			artifactObj.BodySha1 = sha1Arr
		}
		if _, err := validateArtifactKey(artifactKeyFromObj(artifactObj)); err != nil {
			return nil, err
		}
		resultArr = append(resultArr, artifactObj)
	}
	return resultArr, nil
}
