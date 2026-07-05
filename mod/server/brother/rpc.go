package brother

import (
	"context"
	"errors"
	"fmt"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/pebblestore"
	"github.com/voluminor/yggvault/mod/storage/treecodec"
)

// // // // // // // // // //

type handlerObj struct {
	ctx                   context.Context
	storeObj              StoreInterface
	releaseMirrors        map[string]string
	maxFetchResponseBytes uint64
	maxFetchBatchCount    int
}

var _ brotherwire.BrotherInterface = (*handlerObj)(nil)

// // // // // // // // // //

func (h *handlerObj) sourceInfo(key string) brotherwire.SourceInfoObj {
	return brotherwire.SourceInfoObj{SourceURL: h.releaseMirrors[key]}
}

func (h *handlerObj) fetchResponseBytes() uint64 {
	if h.maxFetchResponseBytes == 0 {
		return brotherwire.DefaultMaxFetchResponseBytes
	}
	return h.maxFetchResponseBytes
}

func (h *handlerObj) fetchBatchCount() int {
	if h.maxFetchBatchCount <= 0 {
		return brotherwire.DefaultMaxFetchBatchCount
	}
	return h.maxFetchBatchCount
}

// // // // // // // // // //

// Hello returns the negotiated wire protocol version.
func (h *handlerObj) Hello(_ brotherwire.HelloArgObj, reply *brotherwire.HelloReplyObj) error {
	reply.Protocol = brotherwire.Protocol
	reply.MaxFetchResponseBytes = h.fetchResponseBytes()
	reply.MaxFetchBatchCount = uint32(h.fetchBatchCount())
	return nil
}

// Index returns one key version page with release notes and source info.
// NextPage is strictly increasing for resumability; 0 means end.
func (h *handlerObj) Index(arg brotherwire.IndexArgObj, reply *brotherwire.IndexReplyObj) error {
	pageNum := arg.Page
	if pageNum < 1 {
		pageNum = 1
	}
	if pageNum > cMaxIndexPage {
		reply.Source = h.sourceInfo(arg.Key)
		return nil
	}
	offset := int(pageNum-1) * cIndexPageSize
	versionArr, err := h.storeObj.ListVersionsPage(h.ctx, arg.Key, false, cIndexPageSize+1, offset)
	if err != nil {
		return err
	}
	hasNext := len(versionArr) > cIndexPageSize
	if hasNext {
		versionArr = versionArr[:cIndexPageSize]
	}
	reply.Entries = make([]brotherwire.IndexEntryObj, 0, len(versionArr))
	for i := range versionArr {
		reply.Entries = append(reply.Entries, brotherwire.IndexEntryObj{
			Version:     versionArr[i].Version,
			BodyMD:      versionArr[i].ReleaseNotes,
			TreeHash:    brotherwire.HashWire(versionArr[i].TreeHash),
			SourceHash:  brotherwire.HashWire(versionArr[i].SourceHash),
			UpstreamSeq: versionArr[i].UpstreamSeq,
		})
	}
	if hasNext {
		reply.NextPage = pageNum + 1
	}
	reply.Source = h.sourceInfo(arg.Key)
	return nil
}

// Version returns canonical tree bytes and tree_hash for client-side transport verification.
func (h *handlerObj) Version(arg brotherwire.VersionArgObj, reply *brotherwire.VersionReplyObj) error {
	versionObj, ok, err := h.storeObj.GetVersion(h.ctx, arg.Key, arg.Version)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("version not found: %s@%s", arg.Key, arg.Version)
	}
	entryArr, err := h.storeObj.ReadTree(h.ctx, versionObj.TreeHash)
	if err != nil {
		return err
	}
	treeBytes, treeHashObj, err := treecodec.Encode(entryArr)
	if err != nil {
		return err
	}
	reply.TreeHash = brotherwire.HashWire(treeHashObj)
	reply.TreeBytes = treeBytes
	return nil
}

// BlobsFetch returns requested blobs up to MaxFetchResponseBytes.
// Duplicates and missing blobs are dropped; over-batch and oversize requests are errors.
func (h *handlerObj) BlobsFetch(arg brotherwire.BlobsFetchArgObj, reply *brotherwire.BlobsFetchReplyObj) error {
	maxBatchCount := h.fetchBatchCount()
	if len(arg.Hashes) > maxBatchCount {
		return fmt.Errorf("blob batch too large: %d > %d", len(arg.Hashes), maxBatchCount)
	}
	seenSet := make(map[brotherwire.HashWire]struct{}, len(arg.Hashes))
	reply.Blobs = make([]brotherwire.BlobObj, 0, len(arg.Hashes))
	var totalBytes uint64
	maxResponseBytes := h.fetchResponseBytes()
	for _, hashWire := range arg.Hashes {
		if _, dup := seenSet[hashWire]; dup {
			continue
		}
		seenSet[hashWire] = struct{}{}
		dataArr, err := h.storeObj.ReadBlob(h.ctx, core.HashObj(hashWire))
		if err != nil {
			if errors.Is(err, pebblestore.ErrObjectNotFound) {
				continue
			}
			return fmt.Errorf("blob fetch: read %s: %w", core.HashObj(hashWire).Hex(), err)
		}
		totalBytes += uint64(len(dataArr))
		if totalBytes > maxResponseBytes {
			return fmt.Errorf("blob batch response exceeds %d bytes", maxResponseBytes)
		}
		reply.Blobs = append(reply.Blobs, brotherwire.BlobObj{Hash: hashWire, Value: dataArr})
	}
	return nil
}
