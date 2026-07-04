package dataapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"strconv"

	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

func encodeReleaseCursor(upstreamSeq int64, version string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(upstreamSeq, 10) + "\x00" + version))
}

func decodeReleaseCursor(cursorText string) (upstreamSeq int64, version string, err error) {
	if cursorText == "" {
		return 0, "", nil
	}
	rawArr, decErr := base64.RawURLEncoding.DecodeString(cursorText)
	if decErr != nil {
		return 0, "", serr.ErrBadInput
	}
	beforeArr, afterArr, found := bytes.Cut(rawArr, []byte{0})
	if !found || len(afterArr) == 0 {
		return 0, "", serr.ErrBadInput
	}
	seqValue, parseErr := strconv.ParseInt(string(beforeArr), 10, 64)
	if parseErr != nil || seqValue < 0 {
		return 0, "", serr.ErrBadInput
	}
	return seqValue, string(afterArr), nil
}

// // // // // // // // // //

// BuildReleaseList builds a lightweight newest-first release list with keyset pagination and no OFFSET.
// It requests pageSize+1 rows to detect the next page; unknown empty first pages return not found.
func BuildReleaseList(ctx context.Context, store VersionReaderInterface, st StateReaderInterface, key string, afterCursor string, pageSize int, linkObj link.Obj) (api.ReleaseListObj, error) {
	afterSeq, afterVersion, err := decodeReleaseCursor(afterCursor)
	if err != nil {
		return api.ReleaseListObj{}, err
	}
	pageSize = max(1, pageSize)
	versionArr, err := store.ListVersionsKeyset(ctx, key, false, afterSeq, afterVersion, pageSize+1)
	if err != nil {
		return api.ReleaseListObj{}, err
	}
	keyStateObj, known := st.KeyState(key)
	if !known && afterCursor == "" && len(versionArr) == 0 {
		return api.ReleaseListObj{}, serr.ErrNotFound
	}

	nextCursor := ""
	if len(versionArr) > pageSize {
		lastObj := versionArr[pageSize-1]
		nextCursor = encodeReleaseCursor(lastObj.UpstreamSeq, lastObj.Version)
		versionArr = versionArr[:pageSize]
	}

	releaseArr := make([]api.ReleaseSummaryObj, 0, len(versionArr))
	for i := range versionArr {
		versionObj := versionArr[i]
		releaseArr = append(releaseArr, api.ReleaseSummaryObj{
			Version: versionObj.Version,
			Time:    versionObj.IngestTS.UTC(),
			Size:    int64(versionObj.SourceSizeBytes),
			URL:     api.NewOptString(linkObj.Key(key, "/"+versionObj.Version+".json")),
		})
	}
	listObj := api.ReleaseListObj{Key: key, Releases: releaseArr, Source: sourceStatusOf(keyStateObj)}
	if nextCursor != "" {
		listObj.Next = api.NewOptString(nextCursor)
	}
	return listObj, nil
}
