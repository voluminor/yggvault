package brotherwire

// // // // // // // // // //

const (
	// HashSize mirrors storage hash24 (BLAKE3 XOF, 24B) and changes only with the storage format.
	HashSize = 24

	// DefaultMaxFetchResponseBytes is the default total-byte cap for one BlobsFetch response. It matches the
	// absolute single-blob cap (256MiB) so one large blob remains fetchable; config may lower or raise it.
	DefaultMaxFetchResponseBytes = 256 << 20

	// DefaultMaxFetchBatchCount is the default hash-count cap for one BlobsFetch request.
	DefaultMaxFetchBatchCount = 256

	// Protocol is the brother protocol version from Hello; mismatches mean unavailable.
	Protocol = "mesh1"

	// RPCPath is the fixed CONNECT/hijack route on enabled listeners.
	RPCPath = "/rpc"

	// ServiceName is the net/rpc service name used for BrotherInterface registration.
	ServiceName = "Brother"

	// MethodHello is the full RPC method name for protocol and checksum verification.
	MethodHello = "Brother.Hello"
	// MethodIndex is the full RPC method name for paged version index.
	MethodIndex = "Brother.Index"
	// MethodVersion is the full RPC method name for version tree transfer.
	MethodVersion = "Brother.Version"
	// MethodBlobsFetch is the full RPC method name for batched blob pull.
	MethodBlobsFetch = "Brother.BlobsFetch"
)

// //

// HashWire is storage hash24 in gob representation; conversion to core.HashObj lives in mod/source.
type HashWire [HashSize]byte

// // // // // // // // // //

// HelloArgObj is parameterless Hello input required by the net/rpc signature.
type HelloArgObj struct{}

// HelloReplyObj carries the wire protocol version negotiated on connect.
type HelloReplyObj struct {
	Protocol              string
	MaxFetchResponseBytes uint64
	MaxFetchBatchCount    uint32
}

// //

// IndexArgObj requests a paged version index for a key.
type IndexArgObj struct {
	Key  string
	Page uint32
}

// IndexEntryObj is one version with upstream metadata not derivable from the tree and origin hashes for
// index-driven updates. TreeHash is the content digest for cheap skips; SourceHash is the archive provenance.
// A zero hash means an older or silent brother, so downstream fetches the tree as before.
// UpstreamSeq is the source listing position; larger is newer. Zero means a legacy node, so downstream
// assigns positions by index order. This gob field remains backward-compatible with the older protocol.
type IndexEntryObj struct {
	Version     string
	BodyMD      string
	TreeHash    HashWire
	SourceHash  HashWire
	UpstreamSeq int64
}

// SourceInfoObj describes the key source shared by a brother: its upstream URL. Empty SourceURL means the
// brother is the origin or does not disclose it; downstream still classifies and verifies first-source itself.
type SourceInfoObj struct {
	SourceURL string // upstream used by the brother for this key (its release_mirrors[key])
}

// IndexReplyObj contains current page entries. NextPage==0 means end; otherwise it must strictly increase.
// Source is repeated on every page, so downstream may read it from any page.
type IndexReplyObj struct {
	Entries  []IndexEntryObj
	NextPage uint32
	Source   SourceInfoObj
}

// //

// VersionArgObj requests a concrete version tree.
type VersionArgObj struct {
	Key     string
	Version string
}

// VersionReplyObj carries canonical Pebble tree bytes and their hash24 for transport verification. TreeBytes uses
// mod/storage/treecodec (YRV_TREE), not an arbitrary payload; changing that format breaks the protocol.
type VersionReplyObj struct {
	TreeHash  HashWire
	TreeBytes []byte
}

// //

// BlobObj is one batched-pull blob: hash24 plus plaintext value.
type BlobObj struct {
	Hash  HashWire
	Value []byte
}

// BlobsFetchArgObj is a batch of requested hashes, size-capped by the client.
type BlobsFetchArgObj struct {
	Hashes []HashWire
}

// BlobsFetchReplyObj contains returned blobs; the client verifies each before writing.
type BlobsFetchReplyObj struct {
	Blobs []BlobObj
}

// // // // // // // // // //

// BrotherInterface is the server-side net/rpc contract implemented in mod/server.
// Signatures use net/rpc form: (arg, *reply) error.
type BrotherInterface interface {
	Hello(arg HelloArgObj, reply *HelloReplyObj) error
	Index(arg IndexArgObj, reply *IndexReplyObj) error
	Version(arg VersionArgObj, reply *VersionReplyObj) error
	BlobsFetch(arg BlobsFetchArgObj, reply *BlobsFetchReplyObj) error
}
