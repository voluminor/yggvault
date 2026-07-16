package archive

import (
	"errors"
	"fmt"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

const (
	// FormatZip is the zip format.
	FormatZip FormatType = "zip"
	// FormatTar is the tar format.
	FormatTar FormatType = "tar"
	// FormatTarGz is the tar.gz format.
	FormatTarGz FormatType = "tar.gz"

	cCheckMalformed        = "malformed"
	cCheckUnsupported      = "unsupported_entry"
	cCheckDuplicatePath    = "duplicate_path"
	cCheckPathConflict     = "path_conflict"
	cCheckEmpty            = "empty_archive"
	cCheckSymlinkTarget    = "symlink_target"
	cCheckGzipTrailing     = "gzip_trailing_data"
	cCheckHeaderCount      = "header_count"
	cCheckFileBytes        = "file_bytes"
	cCheckPathBytes        = "path_bytes"
	cCheckUnpackedSize     = "unpacked_size"
	cCheckCompressionRatio = "compression_ratio"
	cCheckTarStream        = "tar_stream_bytes"
	cCheckEntryHardCap     = "entry_hard_cap"
	cCheckSpool            = "spool"

	cCopyBufferBytes = 64 * 1024
	cTreeMaxEntries  = 1_000_000
)

// //

// FormatType is an archive format; see the Format constants.
type FormatType string

// Obj is the archive extract/write engine; it keeps anti-bomb and anti-traversal limits and is immutable after New.
type Obj struct {
	limitsObj LimitsObj
}

// LimitsObj contains caps against zip/tar bombs and abuse: compressed and unpacked size, file size and count,
// and path length. All non-size fields are required; 0 in size fields means unlimited.
type LimitsObj struct {
	MaxArchiveSize         uint64
	MaxArchiveUnpackedSize uint64
	MaxArchiveFileBytes    uint64
	MaxArchiveFiles        uint
	MaxArchivePathBytes    uint
}

// RequestObj is an extraction request: format, source path, and spool directory for temporary blobs.
type RequestObj struct {
	Key             string
	Version         string
	Format          FormatType
	SourcePath      string
	SourceSizeBytes uint64
	SpoolPath       string
	limitsObj       LimitsObj
}

// ResultObj is the extraction result: canonical tree entry paths and deduplicated staged blobs.
type ResultObj struct {
	Entries         []core.StagedEntryObj
	Blobs           []core.StagedBlobObj
	DroppedSymlinks []string
}

// //

// String returns the textual format representation.
func (obj FormatType) String() string {
	return string(obj)
}

func validateFormat(formatObj FormatType) error {
	switch formatObj {
	case FormatZip, FormatTar, FormatTarGz:
		return nil
	default:
		return fmt.Errorf("unsupported archive format: %s", formatObj)
	}
}

func validateLimits(limitsObj LimitsObj) error {
	if limitsObj.MaxArchiveFiles == 0 {
		return errors.New("max archive files must be positive")
	}
	if limitsObj.MaxArchivePathBytes == 0 {
		return errors.New("max archive path bytes must be positive")
	}
	if limitsObj.MaxArchiveFileBytes == 0 {
		return errors.New("max archive file bytes must be positive")
	}
	if limitsObj.MaxArchiveUnpackedSize == 0 {
		return errors.New("max archive unpacked size must be positive")
	}
	return nil
}

// //

// New creates an engine with the given limits and rejects incomplete limit sets.
func New(limitsObj LimitsObj) (*Obj, error) {
	if err := validateLimits(limitsObj); err != nil {
		return nil, err
	}
	return &Obj{limitsObj: limitsObj}, nil
}
