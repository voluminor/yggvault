package core

// // // // // // // // // //

// ArtifactObj is a built edge artifact: body, validators, disk path, and stale/degraded flags for server output.
type ArtifactObj struct {
	MaterializerID string
	ArtifactKind   string
	ListenerID     string
	Key            string
	Version        string
	ETag           string
	BodyHash       HashObj
	BodySha256     []byte
	BodySha1       []byte
	SizeBytes      uint64
	FormatVersion  uint32
	FilePath       string
	DegradedReason string
}

// ArtifactKeyObj identifies an artifact for storage addressing by materializer, kind, listener, key, and version.
type ArtifactKeyObj struct {
	MaterializerID string
	ArtifactKind   string
	ListenerID     string
	Key            string
	Version        string
}

// ArtifactDigestObj is the built artifact identity used by RegisterArtifact and computed by storage.
type ArtifactDigestObj struct {
	BodyHash   HashObj
	BodySha256 []byte
	BodySha1   []byte
	SizeBytes  uint64
	ETag       string
}
