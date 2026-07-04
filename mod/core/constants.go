package core

// // // // // // // // // //

const (
	// HashSize is the truncated BLAKE3 digest length in bytes (24 = 192 bits); it defines HashObj and key size.
	HashSize = 24

	// PebbleKeyFormat marks the storage key schema (tag + 24-byte hash) and is written to metadata.
	PebbleKeyFormat = "tag_hash24"

	// ModeFile is the tree entry mode for a regular file.
	ModeFile = "file"
	// ModeSymlink is the tree entry mode for a symbolic link.
	ModeSymlink = "symlink"

	// DefaultFormat is the default artifact format version.
	DefaultFormat = 1
)

// TimeFormat is the canonical RFC3339 format with milliseconds for UTC timestamps.
const TimeFormat = "2006-01-02T15:04:05.000Z07:00"
