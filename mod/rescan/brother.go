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

// cMaxBrotherVersions caps total versions across index pages to limit pagination abuse and OOM risk.
const cMaxBrotherVersions = 200000

// cMaxBrotherIndexBytes bounds the cumulative in-memory size of one index across all pages. Together with
// the per-entry note cap it keeps a hostile brother's footprint modest; the closed-ring model needs no
// more headroom than this (lowered from 512 MiB).
const cMaxBrotherIndexBytes = 128 << 20

const (
	// cMaxBrotherRedials caps reconnects to one brother during a single key ingest cycle.
	cMaxBrotherRedials = 3
	// cBrotherRedialBackoff pauses before redialing to avoid hammering a dead link.
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

// brotherEntryBytes estimates one index entry's heap footprint: version, notes, hashes, and seq overhead.
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
			// Brother-origin fallback uses ephemeral listing mode; sticky conflict freezes are for git keys only.
			obj.ingestGit(ctx, key, firstSourceURL, cycleStart, false, forceRefresh, keySourceObj.ListingMode, false)
			return
		}
	}

	_ = obj.stateObj.MarkAvailable(key, len(indexArr) > 0, cycleStart)
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
		if !forceRefresh && obj.canSkipBrotherVersion(ctx, key, entryObj) {
			continue
		}
		// Do not retry a deterministic failure for the same tree hash; a bad brother could force endless downloads.
		if forceRefresh {
			obj.clearPermanentFailure(key, entryObj.Version)
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
	// Brother indexes are complete lists, so seq-window release does not apply.
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

// brotherFallbackSeqs assigns positions only to legacy entries without seq; brother indexes are newest-first.
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

func (obj *Obj) canSkipBrotherVersion(ctx context.Context, key string, entryObj source.BrotherIndexEntryObj) bool {
	if entryObj.TreeHash.IsZero() {
		return false
	}
	existingObj, ok, err := obj.storageObj.GetVersion(ctx, key, entryObj.Version)
	if err != nil || !ok {
		return false
	}
	if existingObj.UpstreamDeleted {
		return false
	}
	if existingObj.TreeHash != entryObj.TreeHash {
		return false
	}
	if existingObj.HealPending {
		// A blocked Go zip can heal without resync; other heal cases require normal resync.
		return obj.clearBlockedGoHeal(ctx, key, entryObj.Version, existingObj)
	}
	return true
}

func dropUnstorablePublicMirrorVersions(entryArr []source.PublicMirrorVersionObj) []source.PublicMirrorVersionObj {
	storable := func(version string) bool {
		return util.IsStorableSemver(version) || util.IsStorableRawVersion(version)
	}
	firstBad := -1
	for i := range entryArr {
		if !storable(entryArr[i].Version) {
			firstBad = i
			break
		}
	}
	if firstBad < 0 {
		return entryArr
	}
	outArr := make([]source.PublicMirrorVersionObj, 0, len(entryArr)-1)
	outArr = append(outArr, entryArr[:firstBad]...)
	for i := firstBad + 1; i < len(entryArr); i++ {
		if storable(entryArr[i].Version) {
			outArr = append(outArr, entryArr[i])
		}
	}
	return outArr
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

func (obj *Obj) ingestBrotherPublic(ctx context.Context, key string, remoteKey string, brotherURL string, forceRefresh bool, cycleStart time.Time) (bool, error) {
	entryArr, truncated, err := obj.sourceObj.PublicMirrorVersions(ctx, brotherURL, remoteKey)
	if err != nil {
		return false, err
	}
	entryArr = dropUnstorablePublicMirrorVersions(entryArr)
	_ = obj.stateObj.MarkAvailable(key, len(entryArr) > 0, cycleStart)
	if truncated {
		obj.raiseReleasesTruncated(key, errors.New("public mirror releases exceeded the processing cap; deletion disabled this cycle"))
	}

	upstreamSet := make(map[string]struct{}, len(entryArr))
	versionArr := make([]string, len(entryArr))
	for i := range entryArr {
		upstreamSet[entryArr[i].Version] = struct{}{}
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
		if !forceRefresh && obj.canSkipPublicMirrorVersion(ctx, key, entryObj) {
			continue
		}
		failureRef := publicMirrorFailureRef(entryObj)
		if forceRefresh {
			obj.clearPermanentFailure(key, entryObj.Version)
		} else if failureRef != "" && obj.permanentFailureSkip(key, entryObj.Version, failureRef) {
			continue
		}
		obj.ingestPublicMirrorVersion(ctx, key, entryObj, seqByVersion[entryObj.Version])
	}
	if ctx.Err() != nil {
		return true, nil
	}
	if !truncated && len(entryArr) > 0 {
		obj.applyDeletionGrace(ctx, key, upstreamSet, "", 0)
	}
	return true, nil
}

func (obj *Obj) canSkipPublicMirrorVersion(ctx context.Context, key string, entryObj source.PublicMirrorVersionObj) bool {
	if entryObj.TreeHash.IsZero() {
		return false
	}
	existingObj, ok, err := obj.storageObj.GetVersion(ctx, key, entryObj.Version)
	if err != nil || !ok {
		return false
	}
	if existingObj.UpstreamDeleted {
		return false
	}
	if existingObj.TreeHash != entryObj.TreeHash {
		return false
	}
	if existingObj.HealPending {
		return obj.clearBlockedGoHeal(ctx, key, entryObj.Version, existingObj)
	}
	return true
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
				obj.recordPermanentFailure(key, entryObj.Version, failureRef)
			}
		}
		if phaseText == "archive_invalid" && failureRef != "" {
			obj.recordPermanentFailure(key, entryObj.Version, failureRef)
		}
		obj.raiseVersionDegraded(key, entryObj.Version, phaseText, err)
		return false
	}

	obj.clearPermanentFailure(key, entryObj.Version)
	return obj.publishVersion(ctx, publishInputObj{
		key:                  key,
		version:              entryObj.Version,
		releaseNotes:         entryObj.ReleaseNotes,
		sourceHash:           archiveObj.sourceHash,
		sourceSize:           archiveObj.sourceSize,
		upstreamSeq:          upstreamSeq,
		verifiedTS:           time.Now().UTC(),
		expectedTreeHash:     entryObj.TreeHash,
		treeHashMismatchCode: "public_mirror_hash_mismatch",
		permanentFailureRef:  publicMirrorFailureRef(entryObj),
		entries:              archiveObj.entries,
		blobs:                archiveObj.blobs,
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
	// Emergency origin fallback is a normal cycle without forced deep verification.
	// Listing mode is ephemeral for brother keys and cannot create a sticky conflict freeze.
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
	// A releases 404 on Gitea/Forgejo proves the repo is reachable; ingest will use tag fallback.
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

// recordBrotherPermanentFailure remembers deterministic brother-version failures by advertised tree hash.
// Legacy zero tree hashes are not keyed and keep normal retry behavior.
func (obj *Obj) recordBrotherPermanentFailure(key string, entryObj source.BrotherIndexEntryObj) {
	if entryObj.TreeHash.IsZero() {
		return
	}
	obj.recordPermanentFailure(key, entryObj.Version, entryObj.TreeHash.Hex())
}

func (obj *Obj) ingestBrotherVersion(ctx context.Context, key string, sessionObj source.BrotherSessionInterface, entryObj source.BrotherIndexEntryObj, upstreamSeq int64) {
	// Raw versions replicate between brothers like semver; other names are dropped.
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
	// Version must return exactly the tree bytes advertised by the index hash. A mismatch means a lying
	// or inconsistent brother; reject deterministically or the same version would redownload every cycle.
	if !entryObj.TreeHash.IsZero() {
		if pulledHashObj := core.HashBytes(versionObj.TreeBytes); pulledHashObj != entryObj.TreeHash {
			obj.recordBrotherPermanentFailure(key, entryObj)
			obj.raiseVersionDegraded(key, entryObj.Version, "brother_index_hash_mismatch",
				fmt.Errorf("index tree hash %s does not match pulled tree %s", entryObj.TreeHash.Hex(), pulledHashObj.Hex()))
			return
		}
	}
	treeArr, err := treecodec.Decode(versionObj.TreeBytes)
	if err != nil {
		obj.recordBrotherPermanentFailure(key, entryObj)
		obj.raiseVersionDegraded(key, entryObj.Version, "tree_decode_failed", err)
		return
	}
	// Check archive count/size/path limits on the cheap tree before downloading blobs.
	stagedArr := toStagedEntries(treeArr)
	if _, _, limErr := obj.storageObj.CanonicalTree(stagedArr, key, entryObj.Version); limErr != nil {
		obj.recordBrotherPermanentFailure(key, entryObj)
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

	// Publication path accepted the content; clear previous permanent failure memory.
	obj.clearPermanentFailure(key, entryObj.Version)
	obj.publishVersion(ctx, publishInputObj{
		key:          key,
		version:      entryObj.Version,
		releaseNotes: entryObj.ReleaseNotes,
		sourceHash:   sourceHashObj,
		sourceSize:   uint64(len(versionObj.TreeBytes)),
		upstreamSeq:  upstreamSeq,
		entries:      stagedArr,
		blobs:        blobArr,
		spool:        spoolObj,
	}, &committed)
}
