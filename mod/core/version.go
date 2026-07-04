package core

import "time"

// // // // // // // // // //

// DetectionObj is the source-type detection result for Go modules and Composer packages, including evidence.
type DetectionObj struct {
	IsGo         bool
	IsComposer   bool
	Conflict     bool
	EvidenceJSON string

	// GoZipBlocked means the version tree cannot become a valid Go module zip
	// because of invalid paths, fold collisions, symlinks, or hard module-zip limits.
	GoZipBlocked bool
	// GoZipBlockReason is a short English reason; empty means not blocked.
	GoZipBlockReason string
}

// VersionObj is a persisted mirror version: source/tree hashes, ingest timestamp, lifecycle flags, and neighbor links.
type VersionObj struct {
	Key             string
	Version         string
	SourceHash      HashObj
	SourceSizeBytes uint64
	TreeHash        HashObj
	IngestTS        time.Time
	UpstreamSeq     int64 // source listing position: larger is newer; 0 means unset
	UpstreamDeleted bool
	ReplacedBy      string
	ReleaseNotes    string
	HealPending     bool
	UpstreamRef     string    // commit SHA from source refs advertisement; empty means unknown (brother/legacy)
	VerifiedTS      time.Time // last deep verification time; zero means never verified
}

// PublishObj requests version publication with entry bodies in memory, including tree, detection, artifacts, and
// history event parameters.
type PublishObj struct {
	Key             string
	Version         string
	SourceHash      HashObj
	SourceSizeBytes uint64
	UpstreamSeq     int64 // precomputed source position; 0 lets publish assign max+1
	Entries         []InputEntryObj
	Detection       DetectionObj
	RewriteBlobs    []HashObj
	Artifacts       []ArtifactObj
	UpstreamDeleted bool
	EventType       string
	EventMessage    string
	ReleaseNotes    string
	HealPending     bool
	UpstreamRef     string    // source commit SHA; empty means unknown
	VerifiedTS      time.Time // deep verification timestamp to publish; zero leaves it unset
}

// PublishResultObj reports whether a version was written or skipped and the final TreeHash.
type PublishResultObj struct {
	Key        string
	Version    string
	TreeHash   HashObj
	Published  bool
	Skipped    bool
	Historical string
}

// Git listing mode is sticky: the first successful non-empty listing chooses it, and a later
// conflict freezes key updates instead of switching automatically.
const (
	ListingModeUndecided = ""
	ListingModeReleases  = "releases"
	ListingModeTags      = "tags"
)

// KeySourceObj is the durable binding between key and source: source URL/class, learned public brother addresses,
// and advertised upstream origin. It survives restarts; web/ygg/origin stay empty until first contact.
// ListingMode pins the git listing strategy (empty, "releases", or "tags"); it changes only via SetKeyListingMode.
type KeySourceObj struct {
	Key         string
	URL         string
	Class       string
	WebAddr     string
	YggAddr     string
	OriginURL   string
	ListingMode string
	BoundTS     time.Time
}
