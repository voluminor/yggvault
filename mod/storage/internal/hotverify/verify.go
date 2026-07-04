package hotverify

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/zeebo/blake3"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/osfs"
)

// // // // // // // // // //

var (
	// ErrHashMismatch means the hot-file blake3 hash differs from the expected hash.
	ErrHashMismatch = errors.New("hot artifact hash mismatch")
	// ErrSizeMismatch means the hot-file size differs from the expected size.
	ErrSizeMismatch = errors.New("hot artifact size mismatch")
)

// //

const cVerifyBufferBytes = 128 * 1024

// verifyBufferPool reuses 128 KiB buffers for streaming hashes.
// verify_on_read=always would otherwise allocate a new buffer on every hot-path read.
var verifyBufferPool = sync.Pool{
	New: func() any {
		bufArr := make([]byte, cVerifyBufferBytes)
		return &bufArr
	},
}

// //

// MismatchObj describes a hot-file mismatch for structured artifact errors.
// Fields are private and callers read them through exported accessors.
type MismatchObj struct {
	causeObj          error
	expectedHashObj   core.HashObj
	actualHashObj     core.HashObj
	expectedSizeBytes uint64
	actualSizeBytes   uint64
}

// Error returns the cause text.
func (obj *MismatchObj) Error() string { return obj.causeObj.Error() }

// Unwrap returns the sentinel cause for errors.Is.
func (obj *MismatchObj) Unwrap() error { return obj.causeObj }

// ExpectedHashText returns the expected hash hex, or empty when unset.
func (obj *MismatchObj) ExpectedHashText() string {
	if obj.expectedHashObj.IsZero() {
		return ""
	}
	return obj.expectedHashObj.Hex()
}

// ActualHashText returns the actual hash hex, or empty when the mismatch was size-only.
func (obj *MismatchObj) ActualHashText() string {
	if obj.actualHashObj.IsZero() {
		return ""
	}
	return obj.actualHashObj.Hex()
}

// ExpectedSizeBytes returns the expected file size.
func (obj *MismatchObj) ExpectedSizeBytes() uint64 { return obj.expectedSizeBytes }

// ActualSizeBytes returns the actual file size.
func (obj *MismatchObj) ActualSizeBytes() uint64 { return obj.actualSizeBytes }

// // // // // // // // // //

// VerifyOpenHotFile re-hashes an already open hot file and checks blake3 plus size on the read path.
func VerifyOpenHotFile(ctx context.Context, fileObj *os.File, infoObj os.FileInfo, expectedHashObj core.HashObj, expectedSizeBytes uint64) error {
	actualSizeBytes := uint64(infoObj.Size())
	if actualSizeBytes != expectedSizeBytes {
		return &MismatchObj{
			causeObj:          ErrSizeMismatch,
			expectedHashObj:   expectedHashObj,
			expectedSizeBytes: expectedSizeBytes,
			actualSizeBytes:   actualSizeBytes,
		}
	}

	actualHashObj, err := hashOpenedFile(ctx, fileObj)
	if err != nil {
		return err
	}
	if actualHashObj != expectedHashObj {
		return &MismatchObj{
			causeObj:          ErrHashMismatch,
			expectedHashObj:   expectedHashObj,
			actualHashObj:     actualHashObj,
			expectedSizeBytes: expectedSizeBytes,
			actualSizeBytes:   actualSizeBytes,
		}
	}
	return nil
}

// VerifyHotFileDigests reads a file once, checks blake3, and computes sha256 and sha1 for serving.
// It is used only on the register path, not the read path.
func VerifyHotFileDigests(ctx context.Context, pathToFile string, infoObj os.FileInfo, expectedHashObj core.HashObj, expectedSizeBytes uint64) ([]byte, []byte, error) {
	actualSizeBytes := uint64(infoObj.Size())
	if actualSizeBytes != expectedSizeBytes {
		return nil, nil, &MismatchObj{
			causeObj:          ErrSizeMismatch,
			expectedHashObj:   expectedHashObj,
			expectedSizeBytes: expectedSizeBytes,
			actualSizeBytes:   actualSizeBytes,
		}
	}
	fileObj, err := osfs.OpenNoFollow(pathToFile)
	if err != nil {
		return nil, nil, err
	}
	defer fileObj.Close()
	fdInfoObj, err := fileObj.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !osfs.IsRegularFile(fdInfoObj) || !os.SameFile(infoObj, fdInfoObj) {
		return nil, nil, errors.New("hot artifact file changed during verification")
	}

	blakeObj := blake3.New()
	sha256Obj := sha256.New()
	sha1Obj := sha1.New()
	bufPtr := verifyBufferPool.Get().(*[]byte)
	defer verifyBufferPool.Put(bufPtr)
	bufferArr := *bufPtr
	for {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}
		countValue, readErr := fileObj.Read(bufferArr)
		if countValue > 0 {
			_, _ = blakeObj.Write(bufferArr[:countValue])
			_, _ = sha256Obj.Write(bufferArr[:countValue])
			_, _ = sha1Obj.Write(bufferArr[:countValue])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, nil, readErr
		}
	}
	if actualHashObj := core.HashFromHasher(blakeObj); actualHashObj != expectedHashObj {
		return nil, nil, &MismatchObj{
			causeObj:          ErrHashMismatch,
			expectedHashObj:   expectedHashObj,
			actualHashObj:     actualHashObj,
			expectedSizeBytes: expectedSizeBytes,
			actualSizeBytes:   actualSizeBytes,
		}
	}
	return sha256Obj.Sum(nil), sha1Obj.Sum(nil), nil
}

func hashOpenedFile(ctx context.Context, fileObj *os.File) (core.HashObj, error) {
	if _, err := fileObj.Seek(0, io.SeekStart); err != nil {
		return core.HashObj{}, err
	}
	hashObj := blake3.New()
	bufPtr := verifyBufferPool.Get().(*[]byte)
	defer verifyBufferPool.Put(bufPtr)
	bufferArr := *bufPtr
	for {
		select {
		case <-ctx.Done():
			return core.HashObj{}, ctx.Err()
		default:
		}
		countValue, readErr := fileObj.Read(bufferArr)
		if countValue > 0 {
			_, _ = hashObj.Write(bufferArr[:countValue])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return core.HashObj{}, readErr
		}
	}
	if _, err := fileObj.Seek(0, io.SeekStart); err != nil {
		return core.HashObj{}, err
	}
	return core.HashFromHasher(hashObj), nil
}
