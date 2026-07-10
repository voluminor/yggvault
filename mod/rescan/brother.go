package rescan

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/storage/treecodec"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const cMaxBrotherVersions = 200000

const cMaxBrotherIndexBytes = 128 << 20

const (
	cMaxBrotherRedials    = 3
	cBrotherRedialBackoff = 500 * time.Millisecond
)

func toStagedEntries(treeArr []core.TreeEntryObj) []core.StagedEntryObj {
	outArr := make([]core.StagedEntryObj, len(treeArr))
	for i := range treeArr {
		outArr[i] = core.StagedEntryObj{
			Path:      treeArr[i].Path,
			Mode:      treeArr[i].Mode,
			BlobHash:  treeArr[i].BlobHash,
			SizeBytes: treeArr[i].SizeBytes,
		}
	}
	return outArr
}

func (obj *Obj) filterMissingBlobReqs(ctx context.Context, reqArr []source.BlobReqObj) ([]source.BlobReqObj, error) {
	if len(reqArr) == 0 {
		return reqArr, nil
	}
	hashArr := make([]core.HashObj, len(reqArr))
	for i := range reqArr {
		hashArr[i] = reqArr[i].Hash
	}
	missingArr, err := obj.storageObj.FilterMissingBlobs(ctx, hashArr)
	if err != nil {
		return nil, err
	}
	if len(missingArr) == len(reqArr) {
		return reqArr, nil
	}
	missingSet := make(map[core.HashObj]struct{}, len(missingArr))
	for i := range missingArr {
		missingSet[missingArr[i]] = struct{}{}
	}
	outArr := reqArr[:0]
	for i := range reqArr {
		if _, ok := missingSet[reqArr[i].Hash]; ok {
			outArr = append(outArr, reqArr[i])
		}
	}
	return outArr, nil
}

func brotherEntryBytes(entryObj source.BrotherIndexEntryObj) uint64 {
	return uint64(len(entryObj.Version)) + uint64(len(entryObj.ReleaseNotes)) + 96
}

func blobReqsFromEntries(treeArr []core.TreeEntryObj) []source.BlobReqObj {
	seenSet := make(map[core.HashObj]struct{}, len(treeArr))
	reqArr := make([]source.BlobReqObj, 0, len(treeArr))
	for i := range treeArr {
		hashObj := treeArr[i].BlobHash
		if _, ok := seenSet[hashObj]; ok {
			continue
		}
		seenSet[hashObj] = struct{}{}
		reqArr = append(reqArr, source.BlobReqObj{Hash: hashObj, SizeBytes: treeArr[i].SizeBytes})
	}
	return reqArr
}

// // // // // // // // // //

func (obj *Obj) ingestBrother(ctx context.Context, key string, sourceURL string, remoteKey string, brotherURL string, keySourceObj core.KeySourceObj, forceRefresh bool, cycleStart time.Time) {
	if remoteKey == "" {
		remoteKey = key
	}

	sessionObj, err := obj.dialBrotherFallback(ctx, key, remoteKey, brotherURL, keySourceObj)
	if err != nil {
		if ok, publicErr := obj.ingestBrotherPublicFallback(ctx, key, remoteKey, brotherURL, keySourceObj, forceRefresh, cycleStart); ok {
			return
		} else if publicErr != nil {
			err = errors.Join(err, publicErr)
		}
		if obj.tryOriginFallback(ctx, key, brotherURL, keySourceObj, sourceURL, cycleStart) {
			return
		}
		_ = obj.stateObj.MarkUnavailable(key, cycleStart)
		obj.raiseKeyUnavailable(key, err)
		return
	}
	defer func() { _ = sessionObj.Close() }()

	indexArr, srcInfoObj, err := obj.brotherIndex(ctx, sessionObj)
	if err != nil {
		_ = obj.stateObj.MarkUnavailable(key, cycleStart)
		obj.raiseKeyUnavailable(key, err)
		return
	}

	obj.persistBrotherOrigin(ctx, key, brotherURL, keySourceObj, srcInfoObj.SourceURL)

	if obj.configObj.Brother.Prefer == stconf.BrotherPreferFirstSource {
		firstSourceURL := srcInfoObj.SourceURL
		if firstSourceURL == "" {
			firstSourceURL = keySourceObj.OriginURL
		}
		if firstSourceURL != "" && firstSourceURL != brotherURL && obj.firstSourceReachable(ctx, firstSourceURL) {
			obj.ingestGit(ctx, key, firstSourceURL, cycleStart, false, forceRefresh, keySourceObj.ListingMode, false)
			return
		}
	}

	if err := obj.stateObj.MarkAvailable(key, len(indexArr) > 0, cycleStart); err == nil {
		obj.clearKeyUnavailable(key)
	}
	upstreamSet := make(map[string]struct{}, len(indexArr))
	for i := range indexArr {
		upstreamSet[indexArr[i].Version] = struct{}{}
	}
	fallbackSeqByVersion := obj.brotherFallbackSeqs(ctx, key, indexArr)
	ingestedSet := make(map[string]struct{}, len(indexArr))
	redialsLeft := cMaxBrotherRedials
	for i := range indexArr {
		if ctx.Err() != nil {
			return
		}
		entryObj := indexArr[i]
		if _, dup := ingestedSet[entryObj.Version]; dup {
			continue
		}
		ingestedSet[entryObj.Version] = struct{}{}
		if !forceRefresh && obj.canSkipKnownTree(ctx, key, entryObj.Version, entryObj.TreeHash) {
			continue
		}
		if forceRefresh {
			obj.forceClearPermanentFailure(ctx, key, entryObj.Version)
		} else if !entryObj.TreeHash.IsZero() && obj.permanentFailureSkip(key, entryObj.Version, entryObj.TreeHash.Hex()) {
			continue
		}
		upstreamSeq := entryObj.UpstreamSeq
		if upstreamSeq <= 0 {
			upstreamSeq = fallbackSeqByVersion[entryObj.Version]
		}
		obj.ingestBrotherVersion(ctx, key, sessionObj, entryObj, upstreamSeq)

		if !sessionObj.Healthy() && ctx.Err() == nil && redialsLeft > 0 {
			redialsLeft--
			newSession, ok := obj.redialBrother(ctx, sessionObj, key, remoteKey, brotherURL)
			sessionObj = newSession
			if !ok {
				break
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	obj.applyDeletionGrace(ctx, key, upstreamSet, "", 0)
}

func (obj *Obj) dialBrotherFallback(ctx context.Context, key string, remoteKey string, brotherURL string, keySourceObj core.KeySourceObj) (source.BrotherSessionInterface, error) {
	candidateArr := brotherDialCandidates(brotherURL, keySourceObj, obj.configObj.Brother.TransportOrder)
	var lastErr error
	for _, candURL := range candidateArr {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sessionObj, err := obj.sourceObj.BrotherDial(ctx, key, remoteKey, candURL)
		if err == nil {
			if err = confirmBrotherSession(ctx, sessionObj); err != nil {
				_ = sessionObj.Close()
				lastErr = err
				continue
			}
			return sessionObj, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no brother dial candidates for %q", key)
	}
	return nil, lastErr
}

func confirmBrotherSession(ctx context.Context, sessionObj source.BrotherSessionInterface) error {
	helloObj, err := sessionObj.Hello(ctx)
	if err != nil {
		return err
	}
	if helloObj.Protocol != brotherwire.Protocol {
		return fmt.Errorf("brother protocol mismatch: got %q want %q", helloObj.Protocol, brotherwire.Protocol)
	}
	return nil
}

func brotherDialCandidates(brotherURL string, keySourceObj core.KeySourceObj, order stconf.BrotherTransportOrderEnum) []string {
	u, err := url.Parse(brotherURL)
	if err != nil {
		return []string{brotherURL}
	}
	host := u.Hostname()
	pathPart := u.Path

	webURL, yggURL := "", ""
	if keySourceObj.WebAddr != "" {
		if strings.EqualFold(host, keySourceObj.WebAddr) {
			webURL = brotherURL
		} else {
			webURL = (&url.URL{Scheme: "https", Host: keySourceObj.WebAddr, Path: pathPart}).String()
		}
	}
	if keySourceObj.YggAddr != "" {
		if strings.EqualFold(host, keySourceObj.YggAddr) {
			yggURL = brotherURL
		} else {
			yggURL = (&url.URL{Scheme: "http", Host: keySourceObj.YggAddr, Path: pathPart}).String()
		}
	}

	outArr := make([]string, 0, 3)
	addFunc := func(candURL string) {
		if candURL == "" {
			return
		}
		for _, existing := range outArr {
			if existing == candURL {
				return
			}
		}
		outArr = append(outArr, candURL)
	}
	if order == stconf.BrotherTransportOrderYggWeb {
		addFunc(yggURL)
		addFunc(webURL)
	} else {
		addFunc(webURL)
		addFunc(yggURL)
	}
	addFunc(brotherURL)
	return outArr
}

func (obj *Obj) brotherFallbackSeqs(ctx context.Context, key string, indexArr []source.BrotherIndexEntryObj) map[string]int64 {
	versionArr := make([]string, 0, len(indexArr))
	for i := range indexArr {
		if indexArr[i].UpstreamSeq <= 0 {
			versionArr = append(versionArr, indexArr[i].Version)
		}
	}
	if len(versionArr) == 0 {
		return nil
	}
	return obj.assignUpstreamSeqs(ctx, key, versionArr, nil)
}

func (obj *Obj) canSkipKnownTree(ctx context.Context, key string, version string, treeHashObj core.HashObj) bool {
	if treeHashObj.IsZero() {
		return false
	}
	existingObj, ok, err := obj.storageObj.GetVersion(ctx, key, version)
	if err != nil || !ok {
		return false
	}
	if existingObj.UpstreamDeleted {
		return false
	}
	if existingObj.TreeHash != treeHashObj {
		return false
	}
	if existingObj.HealPending {
		return obj.clearBlockedGoHeal(ctx, key, version, existingObj)
	}
	obj.markVersionTerminalSuccess(ctx, key, version)
	return true
}

func dropUnstorablePublicMirrorVersions(entryArr []source.PublicMirrorVersionObj) []source.PublicMirrorVersionObj {
	return dropUnstorableVersions(entryArr, func(entryObj source.PublicMirrorVersionObj) string { return entryObj.Version })
}

func publicMirrorFailureRef(entryObj source.PublicMirrorVersionObj) string {
	if entryObj.TreeHash.IsZero() {
		return ""
	}
	return entryObj.TreeHash.Hex()
}

func (obj *Obj) ingestBrotherPublicFallback(ctx context.Context, key string, remoteKey string, brotherURL string, keySourceObj core.KeySourceObj, forceRefresh bool, cycleStart time.Time) (bool, error) {
	candidateArr := brotherDialCandidates(brotherURL, keySourceObj, obj.configObj.Brother.TransportOrder)
	var lastErr error
	for _, candURL := range candidateArr {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		ok, err := obj.ingestBrotherPublic(ctx, key, remoteKey, candURL, forceRefresh, cycleStart)
		if ok {
			return true, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no public mirror candidates for %q", key)
	}
	return false, lastErr
}

func (obj *Obj) publicMirrorDetailSkip(ctx context.Context, key string, forceRefresh bool) func(string) bool {
	return func(version string) bool {
		if !util.IsStorableSemver(version) && !util.IsStorableRawVersion(version) {
			return true
		}
		if forceRefresh {
			return false
		}
		existingObj, ok, err := obj.storageObj.GetVersion(ctx, key, version)
		if err != nil || !ok || existingObj.UpstreamDeleted || existingObj.HealPending {
			return false
		}
		obj.markVersionTerminalSuccess(ctx, key, version)
		return true
	}
}

func (obj *Obj) ingestBrotherPublic(ctx context.Context, key string, remoteKey string, brotherURL string, forceRefresh bool, cycleStart time.Time) (bool, error) {
	listingObj, err := obj.sourceObj.PublicMirrorVersions(ctx, brotherURL, remoteKey, obj.publicMirrorDetailSkip(ctx, key, forceRefresh))
	if err != nil {
		return false, err
	}
	if err := obj.stateObj.MarkAvailable(key, len(listingObj.Names) > 0, cycleStart); err == nil {
		obj.clearKeyUnavailable(key)
	}
	if listingObj.Truncated {
		obj.raiseReleasesTruncated(key, errors.New("public mirror releases exceeded the processing cap; deletion disabled this cycle"))
	}
	if listingObj.Unresolved > 0 {
		obj.logObj.Warn().
			Str("component", "rescan").
			Str("key", key).
			Int("unresolved", listingObj.Unresolved).
			Msg("public mirror: some versions could not be resolved this cycle; they are retried next cycle")
	}

	upstreamSet := make(map[string]struct{}, len(listingObj.Names))
	for _, nameText := range listingObj.Names {
		upstreamSet[nameText] = struct{}{}
	}

	entryArr := dropUnstorablePublicMirrorVersions(listingObj.Versions)
	versionArr := make([]string, len(entryArr))
	for i := range entryArr {
		versionArr[i] = entryArr[i].Version
	}
	seqByVersion := obj.assignUpstreamSeqs(ctx, key, versionArr, nil)
	ingestedSet := make(map[string]struct{}, len(entryArr))
	for i := range entryArr {
		if ctx.Err() != nil {
			return true, nil
		}
		entryObj := entryArr[i]
		if _, dup := ingestedSet[entryObj.Version]; dup {
			continue
		}
		ingestedSet[entryObj.Version] = struct{}{}
		if !forceRefresh && obj.canSkipKnownTree(ctx, key, entryObj.Version, entryObj.TreeHash) {
			continue
		}
		failureRef := publicMirrorFailureRef(entryObj)
		if forceRefresh {
			obj.forceClearPermanentFailure(ctx, key, entryObj.Version)
		} else if failureRef != "" && obj.permanentFailureSkip(key, entryObj.Version, failureRef) {
			continue
		}
		obj.ingestPublicMirrorVersion(ctx, key, entryObj, seqByVersion[entryObj.Version])
	}
	if ctx.Err() != nil {
		return true, nil
	}
	if !listingObj.Truncated && len(listingObj.Names) > 0 {
		obj.applyDeletionGrace(ctx, key, upstreamSet, "", 0)
	}
	return true, nil
}

func (obj *Obj) ingestPublicMirrorVersion(ctx context.Context, key string, entryObj source.PublicMirrorVersionObj, upstreamSeq int64) bool {
	if !util.IsStorableSemver(entryObj.Version) && !util.IsStorableRawVersion(entryObj.Version) {
		return false
	}
	spoolObj, err := obj.storageObj.NewBlobSpool(ctx)
	if err != nil {
		obj.raiseVersionDegraded(key, entryObj.Version, "spool_failed", err)
		return false
	}
	committed := false
	defer func() {
		if !committed {
			_ = spoolObj.Close(ctx)
		}
	}()

	archiveObj, phaseText, err := obj.fetchExtractArchive(ctx, key, entryObj.Version, entryObj.ArchiveURL, entryObj.Format, spoolObj)
	if err != nil {
		failureRef := publicMirrorFailureRef(entryObj)
		if phaseText == "fetch_failed" {
			var limitErr *stcode.ErrArchiveLimitExceededObj
			if failureRef != "" && errors.As(err, &limitErr) {
				obj.recordPermanentFailure(ctx, key, entryObj.Version, failureRef, "fetch_failed", err.Error())
			}
		}
		if phaseText == "archive_invalid" && failureRef != "" {
			obj.recordPermanentFailure(ctx, key, entryObj.Version, failureRef, "archive_invalid", err.Error())
		}
		obj.raiseVersionDegraded(key, entryObj.Version, phaseText, err)
		return false
	}

	return obj.publishVersion(ctx, publishInputObj{
		key:                  key,
		version:              entryObj.Version,
		releaseNotes:         truncateReleaseNotes(entryObj.ReleaseNotes),
		sourceHash:           archiveObj.sourceHash,
		sourceSize:           archiveObj.sourceSize,
		upstreamSeq:          upstreamSeq,
		verifiedTS:           time.Now().UTC(),
		expectedTreeHash:     entryObj.TreeHash,
		treeHashMismatchCode: "public_mirror_hash_mismatch",
		permanentFailureRef:  publicMirrorFailureRef(entryObj),
		entries:              archiveObj.entries,
		blobs:                archiveObj.blobs,
		droppedSymlinks:      archiveObj.droppedSymlinks,
		spool:                spoolObj,
	}, &committed)
}

func (obj *Obj) tryOriginFallback(ctx context.Context, key string, brotherURL string, keySourceObj core.KeySourceObj, sourceURL string, cycleStart time.Time) bool {
	originURL := keySourceObj.OriginURL
	if originURL == "" || originURL == brotherURL || originURL == sourceURL {
		return false
	}
	if !obj.firstSourceReachable(ctx, originURL) {
		return false
	}
	obj.ingestGit(ctx, key, originURL, cycleStart, false, false, keySourceObj.ListingMode, false)
	return true
}

func (obj *Obj) persistBrotherOrigin(ctx context.Context, key string, brotherURL string, keySourceObj core.KeySourceObj, announcedOrigin string) {
	if announcedOrigin == "" || announcedOrigin == keySourceObj.OriginURL {
		return
	}
	ksObj := keySourceObj
	ksObj.Key = key
	ksObj.OriginURL = announcedOrigin
	if ksObj.URL == "" {
		ksObj.URL = brotherURL
	}
	if ksObj.Class == "" {
		ksObj.Class = "brother"
	}
	_ = obj.storageObj.PutKeySource(ctx, ksObj)
}

func (obj *Obj) redialBrother(ctx context.Context, deadSession source.BrotherSessionInterface, key, remoteKey, brotherURL string) (source.BrotherSessionInterface, bool) {
	select {
	case <-ctx.Done():
		return deadSession, false
	case <-time.After(cBrotherRedialBackoff):
	}
	newSession, err := obj.sourceObj.BrotherDial(ctx, key, remoteKey, brotherURL)
	if err != nil {
		return deadSession, false
	}
	if err := confirmBrotherSession(ctx, newSession); err != nil {
		_ = newSession.Close()
		return deadSession, false
	}
	_ = deadSession.Close()
	return newSession, true
}

func (obj *Obj) brotherIndex(ctx context.Context, sessionObj source.BrotherSessionInterface) ([]source.BrotherIndexEntryObj, source.BrotherSourceInfoObj, error) {
	var indexArr []source.BrotherIndexEntryObj
	var srcInfoObj source.BrotherSourceInfoObj
	var indexBytes uint64
	page := uint32(1)
	for {
		entryArr, nextPage, infoObj, err := sessionObj.Index(ctx, page)
		if err != nil {
			return nil, source.BrotherSourceInfoObj{}, err
		}
		if infoObj.SourceURL != "" {
			srcInfoObj = infoObj
		}
		for i := range entryArr {
			indexBytes += brotherEntryBytes(entryArr[i])
		}
		if indexBytes > cMaxBrotherIndexBytes {
			return nil, source.BrotherSourceInfoObj{}, fmt.Errorf("brother index exceeds byte budget (%d bytes)", cMaxBrotherIndexBytes)
		}
		indexArr = append(indexArr, entryArr...)
		if len(indexArr) > cMaxBrotherVersions {
			return nil, source.BrotherSourceInfoObj{}, fmt.Errorf("brother index exceeds version cap (%d)", cMaxBrotherVersions)
		}
		if nextPage == 0 {
			break
		}
		if len(entryArr) == 0 {
			return nil, source.BrotherSourceInfoObj{}, fmt.Errorf("brother index returned empty page %d with next page %d", page, nextPage)
		}
		page = nextPage
	}
	return indexArr, srcInfoObj, nil
}

func (obj *Obj) firstSourceReachable(ctx context.Context, sourceURL string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, obj.configObj.Brother.FirstSourceTimeout)
	defer cancel()
	_, _, err := obj.sourceObj.Releases(probeCtx, sourceURL, 1)
	return err == nil || source.IsNotFound(err)
}

func (obj *Obj) brotherFetchBlobs(ctx context.Context, sessionObj source.BrotherSessionInterface, version string, reqArr []source.BlobReqObj, destDir string) ([]core.StagedBlobObj, error) {
	var allBlobArr []core.StagedBlobObj
	var batchArr []source.BlobReqObj
	var batchSize uint64
	maxFetchBytes, maxFetchCount := sessionObj.FetchLimits()
	if maxFetchBytes == 0 {
		maxFetchBytes = brotherwire.DefaultMaxFetchResponseBytes
	}
	if maxFetchCount == 0 {
		maxFetchCount = brotherwire.DefaultMaxFetchBatchCount
	}
	flushFunc := func() error {
		if len(batchArr) == 0 {
			return nil
		}
		fetchObj, err := sessionObj.BlobsFetch(ctx, version, batchArr, destDir)
		if err != nil {
			return err
		}
		allBlobArr = append(allBlobArr, fetchObj.Blobs...)
		batchArr = batchArr[:0]
		batchSize = 0
		return nil
	}
	for _, reqObj := range reqArr {
		overBytes := batchSize+reqObj.SizeBytes > maxFetchBytes
		overCount := len(batchArr) >= int(maxFetchCount)
		if len(batchArr) > 0 && (overBytes || overCount) {
			if err := flushFunc(); err != nil {
				return nil, err
			}
		}
		batchArr = append(batchArr, reqObj)
		batchSize += reqObj.SizeBytes
	}
	if err := flushFunc(); err != nil {
		return nil, err
	}
	return allBlobArr, nil
}

func (obj *Obj) recordBrotherPermanentFailure(ctx context.Context, key string, entryObj source.BrotherIndexEntryObj, code string, message string) {
	if entryObj.TreeHash.IsZero() {
		return
	}
	obj.recordPermanentFailure(ctx, key, entryObj.Version, entryObj.TreeHash.Hex(), code, message)
}

func (obj *Obj) ingestBrotherVersion(ctx context.Context, key string, sessionObj source.BrotherSessionInterface, entryObj source.BrotherIndexEntryObj, upstreamSeq int64) {
	if !util.IsStorableSemver(entryObj.Version) && !util.IsStorableRawVersion(entryObj.Version) {
		return
	}
	spoolObj, err := obj.storageObj.NewBlobSpool(ctx)
	if err != nil {
		obj.raiseVersionDegraded(key, entryObj.Version, "spool_failed", err)
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = spoolObj.Close(ctx)
		}
	}()

	versionObj, err := sessionObj.Version(ctx, entryObj.Version)
	if err != nil {
		obj.raiseVersionDegraded(key, entryObj.Version, "brother_version_failed", err)
		return
	}
	if !entryObj.TreeHash.IsZero() {
		if pulledHashObj := core.HashBytes(versionObj.TreeBytes); pulledHashObj != entryObj.TreeHash {
			hashErr := fmt.Errorf("index tree hash %s does not match pulled tree %s", entryObj.TreeHash.Hex(), pulledHashObj.Hex())
			obj.recordBrotherPermanentFailure(ctx, key, entryObj, "brother_index_hash_mismatch", hashErr.Error())
			obj.raiseVersionDegraded(key, entryObj.Version, "brother_index_hash_mismatch", hashErr)
			return
		}
	}
	treeArr, err := treecodec.Decode(versionObj.TreeBytes)
	if err != nil {
		obj.recordBrotherPermanentFailure(ctx, key, entryObj, "tree_decode_failed", err.Error())
		obj.raiseVersionDegraded(key, entryObj.Version, "tree_decode_failed", err)
		return
	}
	stagedArr := toStagedEntries(treeArr)
	if _, _, limErr := obj.storageObj.CanonicalTree(stagedArr, key, entryObj.Version); limErr != nil {
		obj.recordBrotherPermanentFailure(ctx, key, entryObj, "tree_invalid", limErr.Error())
		obj.raiseVersionDegraded(key, entryObj.Version, "tree_invalid", limErr)
		return
	}
	missingReqArr, err := obj.filterMissingBlobReqs(ctx, blobReqsFromEntries(treeArr))
	if err != nil {
		obj.raiseVersionDegraded(key, entryObj.Version, "brother_blob_filter_failed", err)
		return
	}
	blobArr, err := obj.brotherFetchBlobs(ctx, sessionObj, entryObj.Version, missingReqArr, spoolObj.RootPath())
	if err != nil {
		obj.raiseVersionDegraded(key, entryObj.Version, "brother_blobs_failed", err)
		return
	}

	sourceHashObj := entryObj.SourceHash
	if sourceHashObj.IsZero() {
		sourceHashObj = core.HashBytes(versionObj.TreeBytes)
	}

	obj.publishVersion(ctx, publishInputObj{
		key:                 key,
		version:             entryObj.Version,
		releaseNotes:        truncateReleaseNotes(entryObj.ReleaseNotes),
		sourceHash:          sourceHashObj,
		sourceSize:          uint64(len(versionObj.TreeBytes)),
		upstreamSeq:         upstreamSeq,
		permanentFailureRef: entryObj.TreeHash.Hex(),
		entries:             stagedArr,
		blobs:               blobArr,
		spool:               spoolObj,
	}, &committed)
}
