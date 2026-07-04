package mirror

import (
	"bytes"
	"context"

	"github.com/go-faster/jx"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/server/serr"
)

// // // // // // // // // //

// BuildLatest returns `/{key}/latest`, preferring in-memory KeyState.LatestVersion.
// Storage is the fallback; unknown keys without versions return serr.ErrNotFound.
func BuildLatest(ctx context.Context, store VersionReaderInterface, st StateReaderInterface, key string) ([]byte, error) {
	if keyStateObj, ok := st.KeyState(key); ok && keyStateObj.LatestVersion != "" {
		return []byte(keyStateObj.LatestVersion + "\n"), nil
	}
	latestObj, ok, err := store.LatestVersion(ctx, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, serr.ErrNotFound
	}
	return []byte(latestObj.Version + "\n"), nil
}

// // // // // // // // // //

// BuildList returns `/{key}/list` as newest-first text with one version per line.
// It builds in RAM with a version cap; unknown keys with zero versions return serr.ErrNotFound.
func BuildList(ctx context.Context, store VersionReaderInterface, st StateReaderInterface, key string) ([]byte, error) {
	var bufferObj bytes.Buffer
	count, err := eachVersion(ctx, store, key, func(versionObj core.VersionObj) error {
		bufferObj.WriteString(versionObj.Version)
		bufferObj.WriteByte('\n')
		return nil
	})
	if err != nil {
		return nil, err
	}
	if count == 0 {
		if _, known := st.KeyState(key); !known {
			return nil, serr.ErrNotFound
		}
	}
	return bufferObj.Bytes(), nil
}

// // // // // // // // // //

// BuildListFull returns `/{key}/list/full` as a newest-first JSON array of version_full_entry objects.
// It streams directly into the jx encoder; unknown keys with zero versions return serr.ErrNotFound.
func BuildListFull(ctx context.Context, store VersionReaderInterface, st StateReaderInterface, key string) ([]byte, error) {
	encoderObj := new(jx.Encoder)
	encoderObj.ArrStart()
	count, err := eachVersion(ctx, store, key, func(versionObj core.VersionObj) error {
		encoderObj.ObjStart()
		encoderObj.FieldStart("version")
		encoderObj.Str(versionObj.Version)
		encoderObj.FieldStart("hash")
		encoderObj.Str(versionObj.TreeHash.Hex())
		encoderObj.FieldStart("size")
		encoderObj.Int64(int64(versionObj.SourceSizeBytes))
		encoderObj.ObjEnd()
		return nil
	})
	if err != nil {
		return nil, err
	}
	encoderObj.ArrEnd()
	if count == 0 {
		if _, known := st.KeyState(key); !known {
			return nil, serr.ErrNotFound
		}
	}
	return encoderObj.Bytes(), nil
}
