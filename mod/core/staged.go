package core

import "time"

// // // // // // // // // //

// StagedEntryObj is a prepared publication tree entry: path, mode, and blob hash reference.
type StagedEntryObj struct {
	Path      string
	Mode      string
	BlobHash  HashObj
	SizeBytes uint64
}

// StagedBlobObj is a prepared publication blob whose body is stored in a temporary file on disk.
type StagedBlobObj struct {
	BlobHash  HashObj
	SizeBytes uint64
	FilePath  string
}

// StagedPublishObj is a publication with bodies staged to disk, describing tree and blobs by file references so
// archive content does not stay in RAM.
type StagedPublishObj struct {
	Key             string
	Version         string
	SourceHash      HashObj
	SourceSizeBytes uint64
	UpstreamSeq     int64 // precomputed source position; 0 lets publish assign max+1
	Entries         []StagedEntryObj
	Blobs           []StagedBlobObj
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
