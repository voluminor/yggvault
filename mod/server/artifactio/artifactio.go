package artifactio

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

// BodyObj streams an artifact body directly from *os.File without RAM buffering.
// Close delegates to HotFileObj.Close, closing the fd and releasing the shared-file refcount.
// ogen calls Close after io.Copy through io.Closer, so the refcount is released.
type BodyObj struct {
	fileObj *storage.HotFileObj
}

// Read streams body bytes from disk.
func (obj *BodyObj) Read(bufArr []byte) (int, error) {
	return obj.fileObj.File.Read(bufArr)
}

// Seek positions the body for http.ServeContent range handling by delegating to *os.File.
// The type stays opaque, so Close remains the only path that releases the refcount.
func (obj *BodyObj) Seek(offset int64, whence int) (int64, error) {
	return obj.fileObj.File.Seek(offset, whence)
}

// Close closes the descriptor and releases the shared hot-file refcount.
func (obj *BodyObj) Close() error {
	return obj.fileObj.Close()
}

var (
	_ io.ReadCloser = (*BodyObj)(nil)
	_ io.ReadSeeker = (*BodyObj)(nil)
)

// // // // // // // // // //

// StoreInterface is the narrow storage surface for artifact lookup and materialization.
// The caller builds the EnsureArtifactFile builder.
type StoreInterface interface {
	GetArtifact(ctx context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error)
	EnsureArtifactFile(ctx context.Context, keyObj core.ArtifactKeyObj, builderObj storage.ArtifactBuilderInterface) (*storage.HotFileObj, error)
}

// OpenObj is the result of open: streaming body plus its modification time.
// ETag is decided by the caller from the located metadata, not carried here.
type OpenObj struct {
	Body    *BodyObj
	ModTime time.Time
}

// // // // // // // // // //

// ETag returns a stable artifact ETag from metadata or the quoted blake3-24 body hash.
func ETag(artifactObj core.ArtifactObj) string {
	if artifactObj.ETag != "" {
		return artifactObj.ETag
	}
	return `"` + artifactObj.BodyHash.Hex() + `"`
}

// KindLabel returns the schema/UI artifact kind label for a materializer and kind.
func KindLabel(artifactObj core.ArtifactObj) string {
	switch {
	case artifactObj.MaterializerID == stcode.MaterializerGo.String():
		return "go-zip"
	case artifactObj.ArtifactKind == string(archive.FormatTarGz):
		return "tree-targz"
	case artifactObj.ArtifactKind == string(archive.FormatZip):
		return "tree-zip"
	default:
		return artifactObj.MaterializerID + "-" + artifactObj.ArtifactKind
	}
}

// NameSuffix returns the artifact file name and key-relative suffix.
// The caller builds URLs with its link builder, so host context does not enter this package.
func NameSuffix(artifactObj core.ArtifactObj) (name string, suffix string) {
	switch {
	case artifactObj.MaterializerID == stcode.MaterializerGo.String():
		return artifactObj.Version + ".zip", "/@v/" + artifactObj.Version + ".zip"
	case artifactObj.ArtifactKind == string(archive.FormatTarGz):
		return artifactObj.Version + ".tar.gz", "/" + artifactObj.Version + ".tar.gz"
	default:
		return artifactObj.Version + ".zip", "/" + artifactObj.Version + ".zip"
	}
}

func candidates(listenerID stcode.ListenerType) []stcode.ListenerType {
	if listenerID == stcode.ListenerGlobal {
		return []stcode.ListenerType{stcode.ListenerGlobal}
	}
	return []stcode.ListenerType{listenerID, stcode.ListenerGlobal}
}

// // // // // // // // // //

// LocateKey finds a registered artifact from listener-specific to global scope without materialization,
// returning both metadata and the matched key. Metadata first lets callers answer 304 before expensive
// builds; the returned key lets a follow-up open reuse the resolved scope instead of re-querying it (OpenResolved).
func LocateKey(ctx context.Context, store StoreInterface, materializerID string, kind string, key string, version string, listenerID stcode.ListenerType) (core.ArtifactObj, core.ArtifactKeyObj, bool, error) {
	for _, lidObj := range candidates(listenerID) {
		keyObj := core.ArtifactKeyObj{
			MaterializerID: materializerID,
			ArtifactKind:   kind,
			ListenerID:     lidObj.String(),
			Key:            key,
			Version:        version,
		}
		artifactObj, ok, err := store.GetArtifact(ctx, keyObj)
		if err != nil {
			return core.ArtifactObj{}, core.ArtifactKeyObj{}, false, err
		}
		if ok {
			return artifactObj, keyObj, true, nil
		}
	}
	return core.ArtifactObj{}, core.ArtifactKeyObj{}, false, nil
}

// OpenResolved materializes the hot file for an artifact key already resolved by LocateKey, so a full
// GET does not re-query GetArtifact. Missing/failed materialization returns serr.ErrUnavailable;
// context cancellation is passed through. On success OpenObj.Body owns the file and the caller must close it.
func OpenResolved(ctx context.Context, store StoreInterface, keyObj core.ArtifactKeyObj, builderObj storage.ArtifactBuilderInterface, modTime time.Time) (OpenObj, error) {
	fileObj, buildErr := store.EnsureArtifactFile(ctx, keyObj, builderObj)
	if buildErr != nil {
		if errors.Is(buildErr, context.Canceled) || errors.Is(buildErr, context.DeadlineExceeded) {
			return OpenObj{}, buildErr
		}
		return OpenObj{}, serr.ErrUnavailable
	}
	return OpenObj{Body: &BodyObj{fileObj: fileObj}, ModTime: modTime}, nil
}
