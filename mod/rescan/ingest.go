package rescan

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

const (
	// cVerifyRecentRanks is the recent tier boundary: ranks 1..N use recent_interval, older ranks use archive_interval.
	cVerifyRecentRanks = 10

	// cMaxDeepVerifiesPerKey caps scheduled deep checks per key per cycle; latest is outside this cap.
	// Rows created before verified_ts existed look immediately due, so the cap prevents a single large key
	// from causing a download storm after upgrade.
	cMaxDeepVerifiesPerKey = 5
)

// // // // // // // // // //

func (obj *Obj) runKey(ctx context.Context, key string, forceRefresh bool, cycleStart time.Time) {
	// Key stats are reconciled per key so one slow key does not delay latest visibility for the rest.
	defer obj.reconcileKeyStats(ctx, key)
	if obj.suppressedKey(key) {
		if statsObj := obj.cycleStats(); statsObj != nil {
			statsObj.keysSuppressed.Add(1)
		}
		return
	}
	if statsObj := obj.cycleStats(); statsObj != nil {
		statsObj.keysProcessed.Add(1)
	}
	keyStateObj, _ := obj.stateObj.KeyState(key)
	classObj := keyStateObj.Classification
	sourceURL := keyStateObj.SourceURL
	brotherURL := keyStateObj.BrotherURL
	remoteKey := keyStateObj.RemoteKey

	keySourceObj, _, _ := obj.storageObj.GetKeySource(ctx, key)

	recovering := keyStateObj.Availability == stcode.AvailabilityStatusPermanentDown
	if !keyStateObj.Classified || recovering {
		rootURL := obj.configObj.ReleaseMirrors[key]
		discoveryObj, err := obj.sourceObj.Discover(ctx, key, rootURL)
		if err != nil {
			_ = obj.stateObj.MarkUnavailable(key, cycleStart)
			obj.raiseKeyUnavailable(key, err)
			return
		}
		if recovering {
			if err := obj.stateObj.MarkAvailable(key, true, cycleStart); err != nil {
				obj.raiseKeyUnavailable(key, err)
				return
			}
		}
		if err := obj.stateObj.SetClassification(key, discoveryObj.Class, discoveryObj.SourceURL, discoveryObj.BrotherURL); err != nil {
			obj.raiseKeyUnavailable(key, err)
			return
		}
		_ = obj.stateObj.SetRemoteKey(key, discoveryObj.RemoteKey)
		classObj, sourceURL, brotherURL = discoveryObj.Class, discoveryObj.SourceURL, discoveryObj.BrotherURL
		remoteKey = discoveryObj.RemoteKey
		keySourceObj = obj.persistDiscovery(ctx, key, rootURL, discoveryObj, keySourceObj)
	}

	switch classObj {
	case stcode.SourceClassGit:
		obj.ingestGit(ctx, key, sourceURL, cycleStart, true, forceRefresh, keySourceObj.ListingMode, true)
	case stcode.SourceClassBrother:
		obj.ingestBrother(ctx, key, sourceURL, remoteKey, brotherURL, keySourceObj, forceRefresh, cycleStart)
	}
}

func (obj *Obj) persistDiscovery(ctx context.Context, key string, rootURL string, discoveryObj source.DiscoveryResultObj, priorObj core.KeySourceObj) core.KeySourceObj {
	ksObj := core.KeySourceObj{
		Key:       key,
		URL:       rootURL,
		Class:     discoveryObj.Class.String(),
		WebAddr:   discoveryObj.WebAddr,
		YggAddr:   discoveryObj.YggAddr,
		OriginURL: priorObj.OriginURL,
		// Listing mode survives reclassification so recovery cannot erase conflict protection.
		ListingMode: priorObj.ListingMode,
	}
	_ = obj.storageObj.PutKeySource(ctx, ksObj)
	return ksObj
}

// dropUnstorableReleases removes names that cannot be published as semver or raw versions.
// The input slice is not mutated because it may belong to the source layer.
func dropUnstorableReleases(releaseArr []source.GitReleaseObj) []source.GitReleaseObj {
	storable := func(version string) bool {
		return util.IsStorableSemver(version) || util.IsStorableRawVersion(version)
	}
	firstBad := -1
	for i := range releaseArr {
		if !storable(releaseArr[i].Version) {
			firstBad = i
			break
		}
	}
	if firstBad < 0 {
		return releaseArr
	}
	outArr := make([]source.GitReleaseObj, 0, len(releaseArr)-1)
	outArr = append(outArr, releaseArr[:firstBad]...)
	for i := firstBad + 1; i < len(releaseArr); i++ {
		if storable(releaseArr[i].Version) {
			outArr = append(outArr, releaseArr[i])
		}
	}
	return outArr
}

// listGitVersions implements sticky listing mode: releases are preferred, tags are fallback.
// Mode selection, conflict probes, and availability use only storable names, so rolling aliases and prereleases
// cannot lock a key into an empty releases mode. persistMode=false is used for opportunistic brother-origin
// fallback; the decision is then ephemeral and cannot freeze the key.
func (obj *Obj) listGitVersions(ctx context.Context, key string, sourceURL string, listingMode string, persistMode bool, cycleStart time.Time) ([]source.GitReleaseObj, bool, bool) {
	depth := obj.configObj.Rescan.InitialDepth

	if !persistMode {
		return obj.listGitUndecided(ctx, key, sourceURL, depth, false, cycleStart)
	}

	switch listingMode {
	case core.ListingModeReleases:
		releaseArr, truncated, err := obj.sourceObj.Releases(ctx, sourceURL, depth)
		if err != nil {
			_ = obj.stateObj.MarkUnavailable(key, cycleStart)
			obj.raiseKeyUnavailable(key, err)
			return nil, false, false
		}
		releaseArr = dropUnstorableReleases(releaseArr)
		_ = obj.stateObj.MarkAvailable(key, len(releaseArr) > 0, cycleStart)
		return releaseArr, truncated, true

	case core.ListingModeTags:
		// A failed probe is not a conflict: transient upstream errors must not freeze a key.
		probeArr, _, probeErr := obj.sourceObj.Releases(ctx, sourceURL, 1)
		if probeErr == nil && len(dropUnstorableReleases(probeArr)) > 0 {
			obj.raiseListingModeConflict(key)
			return nil, false, false
		}
		tagArr, truncated, err := obj.sourceObj.Tags(ctx, sourceURL, depth)
		if err != nil {
			_ = obj.stateObj.MarkUnavailable(key, cycleStart)
			obj.raiseKeyUnavailable(key, err)
			return nil, false, false
		}
		tagArr = dropUnstorableReleases(tagArr)
		_ = obj.stateObj.MarkAvailable(key, len(tagArr) > 0, cycleStart)
		return tagArr, truncated, true

	default:
		return obj.listGitUndecided(ctx, key, sourceURL, depth, true, cycleStart)
	}
}

// listGitUndecided resolves listing mode by trying releases first, then tags.
// A 404 from the releases endpoint means "no releases feature" for Gitea/Forgejo-style forges, not outage.
func (obj *Obj) listGitUndecided(ctx context.Context, key string, sourceURL string, depth uint, persistMode bool, cycleStart time.Time) ([]source.GitReleaseObj, bool, bool) {
	releaseArr, truncated, err := obj.sourceObj.Releases(ctx, sourceURL, depth)
	if err != nil && !source.IsNotFound(err) {
		_ = obj.stateObj.MarkUnavailable(key, cycleStart)
		obj.raiseKeyUnavailable(key, err)
		return nil, false, false
	}
	releaseArr = dropUnstorableReleases(releaseArr)
	if len(releaseArr) > 0 {
		if persistMode {
			obj.persistListingMode(ctx, key, core.ListingModeReleases)
		}
		_ = obj.stateObj.MarkAvailable(key, true, cycleStart)
		return releaseArr, truncated, true
	}
	tagArr, tagTruncated, tagErr := obj.sourceObj.Tags(ctx, sourceURL, depth)
	if tagErr != nil {
		_ = obj.stateObj.MarkUnavailable(key, cycleStart)
		obj.raiseKeyUnavailable(key, tagErr)
		return nil, false, false
	}
	tagArr = dropUnstorableReleases(tagArr)
	if len(tagArr) > 0 {
		if persistMode {
			obj.persistListingMode(ctx, key, core.ListingModeTags)
		}
		_ = obj.stateObj.MarkAvailable(key, true, cycleStart)
		return tagArr, tagTruncated, true
	}
	// Both listings are empty: the key is reachable, but no storable mode has been proven yet.
	_ = obj.stateObj.MarkAvailable(key, false, cycleStart)
	return nil, false, true
}

func (obj *Obj) persistListingMode(ctx context.Context, key string, mode string) {
	if err := obj.storageObj.SetKeyListingMode(ctx, key, mode); err != nil {
		obj.logObj.Warn().
			Str("component", "rescan").
			Str("key", key).
			Str("mode", mode).
			Str("error", err.Error()).
			Msg("failed to persist listing mode; decision will repeat next cycle")
	}
}

// gitCycleCountersObj summarizes one git key's cycle decisions for debug logs.
type gitCycleCountersObj struct {
	newIngested int
	skipped     int
	skippedPerm int
	shaChecked  int
	refetched   int
	verified    int
	adopted     int
	clamped     int
}

func (obj *Obj) ingestGit(ctx context.Context, key string, sourceURL string, cycleStart time.Time, allowDeletion bool, forceRefresh bool, listingMode string, persistMode bool) {
	releaseArr, truncated, ok := obj.listGitVersions(ctx, key, sourceURL, listingMode, persistMode, cycleStart)
	if !ok {
		return
	}
	if truncated && allowDeletion {
		allowDeletion = false
		obj.raiseReleasesTruncated(key, errors.New("upstream releases exceeded the processing cap; deletion disabled this cycle"))
	}

	refsObj := obj.fetchRefs(ctx, key, sourceURL)
	localObj, rankObj := obj.localGitState(ctx, key)

	upstreamSet := make(map[string]struct{}, len(releaseArr))
	versionArr := make([]string, len(releaseArr))
	for i := range releaseArr {
		upstreamSet[releaseArr[i].Version] = struct{}{}
		versionArr[i] = releaseArr[i].Version
	}
	seqByVersion := obj.assignUpstreamSeqs(ctx, key, versionArr, localObj)

	countersObj := gitCycleCountersObj{}
	verifyBudget := cMaxDeepVerifiesPerKey
	for i := range releaseArr {
		if ctx.Err() != nil {
			return
		}
		obj.processGitRelease(ctx, key, releaseArr[i], seqByVersion[releaseArr[i].Version], refsObj, localObj, rankObj, cycleStart, forceRefresh, &verifyBudget, &countersObj)
	}
	if ctx.Err() != nil {
		return
	}
	if allowDeletion && len(releaseArr) > 0 {
		obj.applyDeletionGrace(ctx, key, upstreamSet, minReleaseVersion(releaseArr), minListedSeq(releaseArr, localObj, seqByVersion))
	}
	if eventObj := obj.logObj.Debug(); eventObj != nil {
		eventObj.
			Str("component", "rescan").
			Str("key", key).
			Int("listed", len(releaseArr)).
			Bool("refs_available", refsObj != nil).
			Int("new", countersObj.newIngested).
			Int("skipped", countersObj.skipped).
			Int("skipped_permanent", countersObj.skippedPerm).
			Int("sha_checked", countersObj.shaChecked).
			Int("refetched", countersObj.refetched).
			Int("verified", countersObj.verified).
			Int("adopted", countersObj.adopted).
			Int("verify_clamped", countersObj.clamped).
			Msg("git ingest cycle summary")
	}
}

// processGitRelease decides whether one listed version needs download, resurrection, heal, or deep verification.
func (obj *Obj) processGitRelease(ctx context.Context, key string, releaseObj source.GitReleaseObj, upstreamSeq int64, refsObj map[string]string, localObj map[string]core.VersionObj, rankObj map[string]int, cycleStart time.Time, forceRefresh bool, verifyBudget *int, countersObj *gitCycleCountersObj) {
	refSHA := refsObj[releaseObj.Version]
	existingObj, hasRow := localObj[releaseObj.Version]
	if !hasRow {
		// A deterministic failure for the same SHA is useless to redownload until content changes.
		if forceRefresh {
			obj.clearPermanentFailure(key, releaseObj.Version)
		} else if obj.permanentFailureSkip(key, releaseObj.Version, refSHA) {
			countersObj.skippedPerm++
			return
		}
		if obj.ingestGitVersion(ctx, key, releaseObj, upstreamSeq, refSHA) {
			countersObj.newIngested++
		}
		return
	}

	refKnown := refSHA != "" && existingObj.UpstreamRef != ""
	if refKnown {
		countersObj.shaChecked++
	}
	refetchFlag := false
	verifyFlag := false
	switch {
	case forceRefresh:
		refetchFlag = true
	case existingObj.UpstreamDeleted:
		refetchFlag = true
	case refKnown && existingObj.UpstreamRef != refSHA:
		refetchFlag = true
	case existingObj.HealPending:
		// Blocked Go zip with all other artifacts present can heal without download.
		refetchFlag = !obj.clearBlockedGoHeal(ctx, key, releaseObj.Version, existingObj)
	default:
		if verifyDue(existingObj, rankObj[releaseObj.Version], cycleStart, obj.configObj.Rescan.Verify.RecentInterval, obj.configObj.Rescan.Verify.ArchiveInterval) {
			switch {
			case rankObj[releaseObj.Version] == 0:
				verifyFlag = true
			case *verifyBudget > 0:
				*verifyBudget--
				verifyFlag = true
			default:
				countersObj.clamped++
			}
		}
	}

	if !refetchFlag && !verifyFlag {
		countersObj.skipped++
		// Rows without ref can silently adopt the current SHA from the same source.
		if refSHA != "" && existingObj.UpstreamRef == "" {
			if touchErr := obj.storageObj.TouchVersionVerified(ctx, key, releaseObj.Version, time.Time{}, refSHA); touchErr == nil {
				countersObj.adopted++
			}
		}
		return
	}

	if forceRefresh {
		obj.clearPermanentFailure(key, releaseObj.Version)
	} else if obj.permanentFailureSkip(key, releaseObj.Version, refSHA) {
		countersObj.skippedPerm++
		return
	}
	if !obj.ingestGitVersion(ctx, key, releaseObj, upstreamSeq, refSHA) {
		return
	}
	if verifyFlag {
		countersObj.verified++
	} else {
		countersObj.refetched++
	}
	// Publish may cheap-skip by tree hash, so mark verification explicitly.
	_ = obj.storageObj.TouchVersionVerified(ctx, key, releaseObj.Version, time.Now().UTC(), refSHA)
}

// verifyDue reports whether a version needs deep verification: rank 0 every cycle, legacy rows immediately,
// then by tiered interval.
func verifyDue(versionObj core.VersionObj, rank int, now time.Time, recentInterval time.Duration, archiveInterval time.Duration) bool {
	if rank == 0 {
		return true
	}
	if versionObj.VerifiedTS.IsZero() {
		return true
	}
	intervalValue := archiveInterval
	if rank <= cVerifyRecentRanks {
		intervalValue = recentInterval
	}
	return now.Sub(versionObj.VerifiedTS) >= intervalValue
}

// fetchRefs returns tag-to-SHA once per key per cycle; failure disables SHA checks without changing availability.
func (obj *Obj) fetchRefs(ctx context.Context, key string, sourceURL string) map[string]string {
	refsObj, err := obj.sourceObj.Refs(ctx, sourceURL)
	if err != nil {
		obj.logObj.Warn().
			Str("component", "rescan").
			Str("code", "refs_check_failed").
			Str("key", key).
			Str("error", err.Error()).
			Msg("git refs advertisement failed; sha check unavailable this cycle")
		return nil
	}
	return refsObj
}

// localGitState loads all key versions once: version map and active rank map where 0 is newest.
// Read failure returns nil maps, which safely falls back to full re-download.
func (obj *Obj) localGitState(ctx context.Context, key string) (map[string]core.VersionObj, map[string]int) {
	versionArr, err := obj.storageObj.ListVersions(ctx, key, true)
	if err != nil {
		return nil, nil
	}
	byVersionObj := make(map[string]core.VersionObj, len(versionArr))
	rankByVersionObj := make(map[string]int, len(versionArr))
	rank := 0
	for i := range versionArr {
		byVersionObj[versionArr[i].Version] = versionArr[i]
		if !versionArr[i].UpstreamDeleted {
			rankByVersionObj[versionArr[i].Version] = rank
			rank++
		}
	}
	return byVersionObj, rankByVersionObj
}

// minListedSeq returns the minimum source position among listed versions. Rows below it are older than
// the listing window and exempt from deletion grace. Unknown positions are skipped; 0 disables exemption.
func minListedSeq(releaseArr []source.GitReleaseObj, localObj map[string]core.VersionObj, seqByVersion map[string]int64) int64 {
	minSeq := int64(0)
	for i := range releaseArr {
		version := releaseArr[i].Version
		seq := int64(0)
		if rowObj, ok := localObj[version]; ok {
			seq = rowObj.UpstreamSeq
		} else if assigned, ok := seqByVersion[version]; ok {
			seq = assigned
		}
		if seq <= 0 {
			continue
		}
		if minSeq == 0 || seq < minSeq {
			minSeq = seq
		}
	}
	return minSeq
}

// minReleaseVersion returns the semver floor of the listing window. Raw names use minListedSeq instead;
// semver keeps its own floor because source seq order can diverge from semver order.
func minReleaseVersion(releaseArr []source.GitReleaseObj) string {
	floor := ""
	for i := range releaseArr {
		version := releaseArr[i].Version
		if !util.IsStorableSemver(version) {
			continue
		}
		if floor == "" {
			floor = version
			continue
		}
		if cmp, err := util.CompareSemver(version, floor); err == nil && cmp < 0 {
			floor = version
		}
	}
	return floor
}

// assignUpstreamSeqs assigns source positions to not-yet-published listed versions before publication.
// Existing versions keep their position; read failure skips assignment so publish can fall back to max+1.
// localObj is an optional snapshot to avoid one GetVersion per release.
func (obj *Obj) assignUpstreamSeqs(ctx context.Context, key string, versionArr []string, localObj map[string]core.VersionObj) map[string]int64 {
	baseSeq, err := obj.storageObj.MaxUpstreamSeq(ctx, key)
	if err != nil {
		return nil
	}
	newArr := make([]string, 0, len(versionArr))
	seenSet := make(map[string]struct{}, len(versionArr))
	for _, version := range versionArr {
		if _, dup := seenSet[version]; dup {
			continue
		}
		seenSet[version] = struct{}{}
		if localObj != nil {
			if _, ok := localObj[version]; ok {
				continue
			}
		} else if _, ok, getErr := obj.storageObj.GetVersion(ctx, key, version); getErr != nil || ok {
			continue
		}
		newArr = append(newArr, version)
	}
	count := int64(len(newArr))
	if count == 0 || baseSeq > math.MaxInt64-count {
		return nil
	}
	seqByVersion := make(map[string]int64, count)
	for i, version := range newArr {
		seqByVersion[version] = baseSeq + count - int64(i)
	}
	return seqByVersion
}

type fetchedArchiveObj struct {
	sourceHash core.HashObj
	sourceSize uint64
	entries    []core.StagedEntryObj
	blobs      []core.StagedBlobObj
}

func (obj *Obj) fetchExtractArchive(ctx context.Context, key string, version string, archiveURL string, format string, spoolObj *storage.BlobSpoolObj) (fetchedArchiveObj, string, error) {
	fetchObj, err := obj.sourceObj.FetchArchive(ctx, source.GitFetchRequestObj{
		Key:        key,
		Version:    version,
		ArchiveURL: archiveURL,
		Format:     format,
		DestDir:    spoolObj.RootPath(),
	})
	if err != nil {
		return fetchedArchiveObj{}, "fetch_failed", err
	}
	sourceHashObj, err := hashFile(fetchObj.ArchivePath)
	if err != nil {
		return fetchedArchiveObj{}, "source_hash_failed", err
	}
	extractObj, err := obj.archiveObj.Extract(ctx, archive.RequestObj{
		Key:             key,
		Version:         version,
		Format:          archive.FormatType(fetchObj.Format),
		SourcePath:      fetchObj.ArchivePath,
		SourceSizeBytes: fetchObj.SizeBytes,
		SpoolPath:       spoolObj.RootPath(),
	})
	if err != nil {
		return fetchedArchiveObj{}, "archive_invalid", err
	}
	return fetchedArchiveObj{
		sourceHash: sourceHashObj,
		sourceSize: fetchObj.SizeBytes,
		entries:    extractObj.Entries,
		blobs:      extractObj.Blobs,
	}, "", nil
}

// ingestGitVersion downloads an archive and publishes a version; true means publish or cheap-skip succeeded.
func (obj *Obj) ingestGitVersion(ctx context.Context, key string, releaseObj source.GitReleaseObj, upstreamSeq int64, upstreamRef string) bool {
	spoolObj, err := obj.storageObj.NewBlobSpool(ctx)
	if err != nil {
		obj.raiseVersionDegraded(key, releaseObj.Version, "spool_failed", err)
		return false
	}
	committed := false
	defer func() {
		if !committed {
			_ = spoolObj.Close(ctx)
		}
	}()

	archiveObj, phaseText, err := obj.fetchExtractArchive(ctx, key, releaseObj.Version, releaseObj.ArchiveURL, releaseObj.Format, spoolObj)
	if err != nil {
		if phaseText == "fetch_failed" {
			// Size overflow is content-deterministic, so retrying is useless until the upstream ref changes.
			var limitErr *stcode.ErrArchiveLimitExceededObj
			if errors.As(err, &limitErr) {
				obj.recordPermanentFailure(key, releaseObj.Version, upstreamRef)
			}
		}
		if phaseText == "archive_invalid" {
			// Extraction failures are deterministic for this archive content.
			obj.recordPermanentFailure(key, releaseObj.Version, upstreamRef)
		}
		obj.raiseVersionDegraded(key, releaseObj.Version, phaseText, err)
		return false
	}

	obj.clearPermanentFailure(key, releaseObj.Version)
	return obj.publishVersion(ctx, publishInputObj{
		key:          key,
		version:      releaseObj.Version,
		releaseNotes: releaseObj.BodyMD,
		sourceHash:   archiveObj.sourceHash,
		sourceSize:   archiveObj.sourceSize,
		upstreamSeq:  upstreamSeq,
		upstreamRef:  upstreamRef,
		verifiedTS:   time.Now().UTC(),
		entries:      archiveObj.entries,
		blobs:        archiveObj.blobs,
		spool:        spoolObj,
	}, &committed)
}

// // // // // // // // // //

type publishInputObj struct {
	key                  string
	version              string
	releaseNotes         string
	sourceHash           core.HashObj
	sourceSize           uint64
	upstreamSeq          int64
	upstreamRef          string
	verifiedTS           time.Time
	expectedTreeHash     core.HashObj
	treeHashMismatchCode string
	permanentFailureRef  string
	entries              []core.StagedEntryObj
	blobs                []core.StagedBlobObj
	spool                *storage.BlobSpoolObj
}

// cRawVersionEvidence marks raw versions in detection evidence; the version name remains the source of truth.
const cRawVersionEvidence = `{"raw_version":true}`

// detectTree skips ecosystem detection for raw versions so they publish only universal archives.
func (obj *Obj) detectTree(ctx context.Context, version string, treeArr []core.TreeEntryObj, spoolSrc spoolSourceObj) (overlay.DetectionResultObj, error) {
	if util.IsRawVersionName(version) {
		return overlay.DetectionResultObj{Detection: core.DetectionObj{EvidenceJSON: cRawVersionEvidence}}, nil
	}
	return obj.overlayObj.Detect(ctx, treeArr, spoolSrc)
}

// publishVersion returns true for terminal success: publish, tree-hash skip, resurrection, or heal.
func (obj *Obj) publishVersion(ctx context.Context, inObj publishInputObj, committed *bool) bool {
	treeArr, treeHashObj, err := obj.storageObj.CanonicalTree(inObj.entries, inObj.key, inObj.version)
	if err != nil {
		if inObj.permanentFailureRef != "" {
			obj.recordPermanentFailure(inObj.key, inObj.version, inObj.permanentFailureRef)
		}
		obj.raiseVersionDegraded(inObj.key, inObj.version, "tree_invalid", err)
		return false
	}
	if !inObj.expectedTreeHash.IsZero() && treeHashObj != inObj.expectedTreeHash {
		if inObj.permanentFailureRef != "" {
			obj.recordPermanentFailure(inObj.key, inObj.version, inObj.permanentFailureRef)
		}
		codeText := inObj.treeHashMismatchCode
		if codeText == "" {
			codeText = "tree_hash_mismatch"
		}
		obj.raiseVersionDegraded(inObj.key, inObj.version, codeText,
			fmt.Errorf("expected tree hash %s, got %s", inObj.expectedTreeHash.Hex(), treeHashObj.Hex()))
		return false
	}
	existingObj, ok, getErr := obj.storageObj.GetVersion(ctx, inObj.key, inObj.version)
	if getErr == nil && ok && existingObj.TreeHash == treeHashObj {
		if existingObj.UpstreamDeleted {
			if resErr := obj.storageObj.ResurrectVersion(ctx, inObj.key, inObj.version); resErr != nil {
				obj.raiseVersionDegraded(inObj.key, inObj.version, "resurrect_failed", resErr)
				return false
			}
			return true
		}
		if existingObj.HealPending {
			obj.healIncompleteArtifacts(ctx, inObj, treeArr, treeHashObj)
		}
		return true
	}

	spoolSrc := newSpoolSource(treeArr, inObj.blobs, obj.storageObj)
	detectionObj, err := obj.detectTree(ctx, inObj.version, treeArr, spoolSrc)
	if err != nil {
		obj.raiseVersionDegraded(inObj.key, inObj.version, "detect_failed", err)
		return false
	}
	// Log once for a valid version whose tree cannot produce a Go module zip.
	if detectionObj.Detection.GoZipBlocked {
		obj.logObj.Info().
			Str("component", "rescan").
			Str("key", inObj.key).
			Str("version", inObj.version).
			Str("reason", detectionObj.Detection.GoZipBlockReason).
			Msg("version tree cannot form a valid go module zip; go overlay excluded")
	}

	artifactArr, completeFlag := obj.materialize(ctx, inObj.key, inObj.version, treeHashObj, detectionObj, spoolSrc)

	stagedObj := core.StagedPublishObj{
		Key:             inObj.key,
		Version:         inObj.version,
		SourceHash:      inObj.sourceHash,
		SourceSizeBytes: inObj.sourceSize,
		UpstreamSeq:     inObj.upstreamSeq,
		UpstreamRef:     inObj.upstreamRef,
		VerifiedTS:      inObj.verifiedTS,
		Entries:         inObj.entries,
		Blobs:           inObj.blobs,
		Detection:       detectionObj.Detection,
		RewriteBlobs:    detectionObj.RewriteBlobs,
		Artifacts:       artifactArr,
		HealPending:     !completeFlag,
		ReleaseNotes:    inObj.releaseNotes,
	}
	*committed = true
	resultObj, err := obj.storageObj.PublishStaged(ctx, inObj.spool, stagedObj)
	if err != nil {
		obj.raiseVersionDegraded(inObj.key, inObj.version, "publish_failed", err)
		return false
	}
	obj.metricsObj.recordVersionPublished()
	if statsObj := obj.cycleStats(); statsObj != nil {
		if resultObj.Published {
			statsObj.versionsPublished.Add(1)
		}
	}
	if eventObj := obj.logObj.Debug(); eventObj != nil {
		eventObj.
			Str("component", "rescan").
			Str("key", inObj.key).
			Str("version", inObj.version).
			Str("tree_hash", treeHashObj.Hex()).
			Uint64("source_size_bytes", inObj.sourceSize).
			Int("entries", len(inObj.entries)).
			Int("blobs", len(inObj.blobs)).
			Int("artifacts", len(artifactArr)).
			Bool("complete", completeFlag).
			Bool("published", resultObj.Published).
			Bool("skipped", resultObj.Skipped).
			Str("historical", resultObj.Historical).
			Msg("version publish completed")
	}
	return true
}

func (obj *Obj) materialize(ctx context.Context, key string, version string, treeHashObj core.HashObj, detectionObj overlay.DetectionResultObj, spoolSrc spoolSourceObj) ([]core.ArtifactObj, bool) {
	planArr := obj.overlayObj.ArtifactPlan(spoolSrc, key, version, treeHashObj, detectionObj.Detection, detectionObj.Go, detectionObj.RewriteBlobs, obj.listenerArr)
	artifactArr := make([]core.ArtifactObj, 0, len(planArr))
	completeFlag := true
	for i := range planArr {
		if ctx.Err() != nil {
			return artifactArr, false
		}
		planObj := planArr[i]
		digestObj, err := obj.digestGated(ctx, planObj.Builder)
		if err != nil {
			if ctx.Err() != nil {
				return artifactArr, false
			}
			code, _ := overlay.DegradedReason(err)
			obj.raiseVersionDegraded(key, version, code, err)
			completeFlag = false
			continue
		}
		artifactArr = append(artifactArr, artifactFromPlan(planObj, key, version, digestObj))
	}
	return artifactArr, completeFlag
}

func artifactFromPlan(planObj overlay.ArtifactPlanObj, key string, version string, digestObj core.ArtifactDigestObj) core.ArtifactObj {
	return core.ArtifactObj{
		MaterializerID: planObj.MaterializerID.String(),
		ArtifactKind:   planObj.ArtifactKind.String(),
		ListenerID:     planObj.ListenerID.String(),
		Key:            key,
		Version:        version,
		FormatVersion:  planObj.FormatVersion,
		BodyHash:       digestObj.BodyHash,
		BodySha256:     digestObj.BodySha256,
		BodySha1:       digestObj.BodySha1,
		SizeBytes:      digestObj.SizeBytes,
		ETag:           digestObj.ETag,
	}
}

func planArtifactKey(planObj overlay.ArtifactPlanObj) string {
	return planObj.MaterializerID.String() + "\x00" + planObj.ArtifactKind.String() + "\x00" + planObj.ListenerID.String()
}

func storedArtifactKey(artifactObj core.ArtifactObj) string {
	return artifactObj.MaterializerID + "\x00" + artifactObj.ArtifactKind + "\x00" + artifactObj.ListenerID
}

func (obj *Obj) healIncompleteArtifacts(ctx context.Context, inObj publishInputObj, treeArr []core.TreeEntryObj, treeHashObj core.HashObj) {
	spoolSrc := newSpoolSource(treeArr, inObj.blobs, obj.storageObj)
	detectionObj, err := obj.detectTree(ctx, inObj.version, treeArr, spoolSrc)
	if err != nil {
		return
	}
	// Legacy rows may predate Go-zip block detection; persist it or goproxy would advertise
	// an impossible version and heal would keep retrying.
	if storedObj, ok, getErr := obj.storageObj.GetDetection(ctx, inObj.key, inObj.version); getErr == nil && ok &&
		(storedObj.GoZipBlocked != detectionObj.Detection.GoZipBlocked || storedObj.GoZipBlockReason != detectionObj.Detection.GoZipBlockReason) {
		if putErr := obj.storageObj.PutDetection(ctx, inObj.key, inObj.version, detectionObj.Detection); putErr != nil {
			return
		}
		if detectionObj.Detection.GoZipBlocked {
			obj.logObj.Info().
				Str("component", "rescan").
				Str("key", inObj.key).
				Str("version", inObj.version).
				Str("reason", detectionObj.Detection.GoZipBlockReason).
				Msg("version tree cannot form a valid go module zip; go overlay excluded")
		}
	}
	planArr := obj.overlayObj.ArtifactPlan(spoolSrc, inObj.key, inObj.version, treeHashObj, detectionObj.Detection, detectionObj.Go, detectionObj.RewriteBlobs, obj.listenerArr)
	existingArr, err := obj.storageObj.ListArtifacts(ctx, inObj.key, inObj.version)
	if err != nil {
		return
	}
	haveSet := make(map[string]struct{}, len(existingArr))
	for i := range existingArr {
		haveSet[storedArtifactKey(existingArr[i])] = struct{}{}
	}
	registerFailed := false
	for i := range planArr {
		if ctx.Err() != nil {
			return
		}
		planObj := planArr[i]
		if _, exists := haveSet[planArtifactKey(planObj)]; exists {
			continue
		}
		digestObj, derr := obj.digestGated(ctx, planObj.Builder)
		if derr != nil {
			if ctx.Err() != nil {
				return
			}
			code, _ := overlay.DegradedReason(derr)
			obj.raiseVersionDegraded(inObj.key, inObj.version, code, derr)
			continue
		}
		if rerr := obj.storageObj.RegisterArtifact(ctx, artifactFromPlan(planObj, inObj.key, inObj.version, digestObj)); rerr != nil {
			obj.raiseVersionDegraded(inObj.key, inObj.version, "artifact_register_failed", rerr)
			registerFailed = true
		}
	}
	if !registerFailed {
		_ = obj.storageObj.SetHealPending(ctx, inObj.key, inObj.version, false)
	}
}

// clearBlockedGoHeal clears legacy HealPending without refetching when the stored detection already proves
// that Go artifacts are intentionally blocked and every remaining planned artifact exists.
func (obj *Obj) clearBlockedGoHeal(ctx context.Context, key string, version string, versionObj core.VersionObj) bool {
	detectionObj, ok, err := obj.storageObj.GetDetection(ctx, key, version)
	if err != nil || !ok || !detectionObj.GoZipBlocked {
		return false
	}
	goCand, _ := overlay.CandidateFromDetection(detectionObj)
	rewriteArr, err := obj.storageObj.RewriteSet(ctx, key, version)
	if err != nil {
		return false
	}
	planArr := obj.overlayObj.ArtifactPlan(obj.storageObj, key, version, versionObj.TreeHash, detectionObj, goCand, rewriteArr, obj.listenerArr)
	existingArr, err := obj.storageObj.ListArtifacts(ctx, key, version)
	if err != nil {
		return false
	}
	haveSet := make(map[string]struct{}, len(existingArr))
	for i := range existingArr {
		haveSet[storedArtifactKey(existingArr[i])] = struct{}{}
	}
	for i := range planArr {
		if _, exists := haveSet[planArtifactKey(planArr[i])]; !exists {
			return false
		}
	}
	return obj.storageObj.SetHealPending(ctx, key, version, false) == nil
}

func (obj *Obj) digestGated(ctx context.Context, builderObj overlay.ArtifactBuilderInterface) (core.ArtifactDigestObj, error) {
	select {
	case obj.buildSem <- struct{}{}:
	case <-ctx.Done():
		return core.ArtifactDigestObj{}, ctx.Err()
	}
	defer func() { <-obj.buildSem }()
	return obj.storageObj.ArtifactDigest(ctx, builderObj)
}
