package core

// // // // // // // // // //

// InputEntryObj is an input publication tree entry with in-memory content for ingestion.
type InputEntryObj struct {
	Path      string
	Mode      string
	Content   []byte
	BlobHash  HashObj
	SizeBytes uint64
}

// TreeEntryObj is a persisted version-tree entry: path, mode, and blob hash reference without body.
type TreeEntryObj struct {
	Path      string
	Mode      string
	SizeBytes uint64
	BlobHash  HashObj
}
