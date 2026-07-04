package treecodec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
)

// // // // // // // // // //

const (
	// MaxEntries caps entries in one tree and is shared with storage ingest normalization.
	MaxEntries = 1_000_000

	// BinaryMagic is the tree format signature and part of the public codec contract.
	BinaryMagic = "YRV_TREE\x00"

	cMaxObjectBytes = 256 * 1024 * 1024
	cMaxPathBytes   = 4096
	cBinaryMinEntry = 1 + 1 + 1 + 1 + core.HashSize
)

// // // // // // // // // //

func modeByte(mode string) (byte, error) {
	switch mode {
	case core.ModeFile:
		return 1, nil
	case core.ModeSymlink:
		return 2, nil
	default:
		return 0, fmt.Errorf("unsupported tree mode: %s", mode)
	}
}

func modeText(modeValue byte) (string, error) {
	switch modeValue {
	case 1:
		return core.ModeFile, nil
	case 2:
		return core.ModeSymlink, nil
	default:
		return "", fmt.Errorf("unsupported tree mode byte: %d", modeValue)
	}
}

func uvarintSize(value uint64) int {
	var bufferArr [binary.MaxVarintLen64]byte
	return binary.PutUvarint(bufferArr[:], value)
}

func readUvarint(dataArr []byte, offsetValue int) (uint64, int, error) {
	value, readCount := binary.Uvarint(dataArr[offsetValue:])
	if readCount == 0 {
		return 0, 0, errors.New("tree object has invalid uvarint")
	}
	if readCount < 0 {
		return 0, 0, errors.New("tree object has overflowing uvarint")
	}

	var canonicalArr [binary.MaxVarintLen64]byte
	canonicalCount := binary.PutUvarint(canonicalArr[:], value)
	if readCount != canonicalCount || !bytes.Equal(dataArr[offsetValue:offsetValue+readCount], canonicalArr[:canonicalCount]) {
		return 0, 0, errors.New("tree object has non-canonical uvarint")
	}
	return value, offsetValue + readCount, nil
}

func validateEntryPath(path string, maxPathBytes uint) (string, error) {
	if err := util.ValidateArchiveEntryPath(path, maxPathBytes); err != nil {
		return "", err
	}
	return util.CleanArchiveEntryPath(path)
}

func validatePath(pathText string) error {
	cleanPath, err := validateEntryPath(pathText, cMaxPathBytes)
	if err != nil {
		return err
	}
	if cleanPath != pathText {
		return errors.New("tree entry path is not canonical")
	}
	return nil
}

func seenParentPath(seenObj map[string]struct{}, pathText string) bool {
	for i := 0; i < len(pathText); i++ {
		if pathText[i] != '/' {
			continue
		}
		if _, ok := seenObj[pathText[:i]]; ok {
			return true
		}
	}
	return false
}

func validateEntry(entryObj core.TreeEntryObj) error {
	if err := validatePath(entryObj.Path); err != nil {
		return fmt.Errorf("tree entry path %q is invalid: %w", entryObj.Path, err)
	}
	if entryObj.Mode != core.ModeFile && entryObj.Mode != core.ModeSymlink {
		return fmt.Errorf("unsupported tree mode for %s: %s", entryObj.Path, entryObj.Mode)
	}
	if entryObj.BlobHash.IsZero() {
		return fmt.Errorf("tree entry %s has empty blob hash", entryObj.Path)
	}
	return nil
}

func binaryCapacity(entriesArr []core.TreeEntryObj) (int, error) {
	sizeValue := uint64(len(BinaryMagic) + uvarintSize(uint64(len(entriesArr))))
	for i := range entriesArr {
		sizeValue += uint64(uvarintSize(uint64(len(entriesArr[i].Path))))
		sizeValue += uint64(len(entriesArr[i].Path))
		sizeValue++
		sizeValue += uint64(uvarintSize(entriesArr[i].SizeBytes))
		sizeValue += core.HashSize
		if sizeValue > cMaxObjectBytes {
			return 0, errors.New("tree object exceeds maximum size")
		}
	}
	return int(sizeValue), nil
}

// // // // // // // // // //

// Decode parses canonical tree-object bytes into entries.
// It validates magic and bounds before parsing, so untrusted brother-wire input returns errors instead of panics.
// This is the only public tree parser.
func Decode(dataArr []byte) ([]core.TreeEntryObj, error) {
	if len(dataArr) == 0 {
		return nil, errors.New("tree object is empty")
	}
	if len(dataArr) > cMaxObjectBytes {
		return nil, errors.New("tree object exceeds maximum size")
	}
	if !bytes.HasPrefix(dataArr, []byte(BinaryMagic)) {
		return nil, errors.New("tree object has invalid binary magic")
	}
	return parseBinary(dataArr)
}

func parseBinary(dataArr []byte) ([]core.TreeEntryObj, error) {
	offsetValue := len(BinaryMagic)
	countValue, nextOffset, err := readUvarint(dataArr, offsetValue)
	if err != nil {
		return nil, err
	}
	offsetValue = nextOffset
	if countValue == 0 {
		return nil, errors.New("tree object has no entries")
	}
	if countValue > MaxEntries {
		return nil, errors.New("tree entry count exceeds limit")
	}

	if countValue > uint64((len(dataArr)-offsetValue)/cBinaryMinEntry) {
		return nil, errors.New("tree entry count is invalid")
	}

	resultArr := make([]core.TreeEntryObj, 0, int(countValue))
	for i := uint64(0); i < countValue; i++ {
		pathSize, readOffset, readErr := readUvarint(dataArr, offsetValue)
		if readErr != nil {
			return nil, readErr
		}
		offsetValue = readOffset
		if pathSize == 0 || pathSize > cMaxPathBytes || pathSize > uint64(len(dataArr)-offsetValue) {
			return nil, errors.New("tree path length is invalid")
		}
		pathText := string(dataArr[offsetValue : offsetValue+int(pathSize)])
		offsetValue += int(pathSize)
		if offsetValue >= len(dataArr) {
			return nil, errors.New("tree mode is missing")
		}
		modeTextValue, modeErr := modeText(dataArr[offsetValue])
		if modeErr != nil {
			return nil, modeErr
		}
		offsetValue++
		sizeValue, readOffset, readErr := readUvarint(dataArr, offsetValue)
		if readErr != nil {
			return nil, readErr
		}
		offsetValue = readOffset
		if len(dataArr)-offsetValue < core.HashSize {
			return nil, errors.New("tree blob hash is truncated")
		}
		hashObj, hashErr := core.HashFromBytes(dataArr[offsetValue : offsetValue+core.HashSize])
		if hashErr != nil {
			return nil, hashErr
		}
		offsetValue += core.HashSize

		resultArr = append(resultArr, core.TreeEntryObj{
			Path:      pathText,
			Mode:      modeTextValue,
			SizeBytes: sizeValue,
			BlobHash:  hashObj,
		})
	}
	if offsetValue != len(dataArr) {
		return nil, errors.New("tree object has trailing bytes")
	}
	return validateParsed(resultArr)
}

func validateParsed(resultArr []core.TreeEntryObj) ([]core.TreeEntryObj, error) {
	if len(resultArr) == 0 {
		return nil, errors.New("tree object has no entries")
	}
	seenPathObj := make(map[string]struct{}, len(resultArr))
	for i := range resultArr {
		if err := validateEntry(resultArr[i]); err != nil {
			return nil, err
		}
		if i > 0 && resultArr[i-1].Path >= resultArr[i].Path {
			return nil, errors.New("tree paths are not canonical")
		}
		if seenParentPath(seenPathObj, resultArr[i].Path) {
			return nil, errors.New("tree contains file and child path conflict")
		}
		seenPathObj[resultArr[i].Path] = struct{}{}
	}
	return resultArr, nil
}

// Encode serializes entries into canonical tree-object bytes and tree_hash.
// Entries are sorted, duplicates and file-child conflicts are rejected, and it mirrors Decode.
func Encode(entriesArr []core.TreeEntryObj) ([]byte, core.HashObj, error) {
	if len(entriesArr) == 0 {
		return nil, core.HashObj{}, errors.New("tree must contain at least one entry")
	}
	if len(entriesArr) > MaxEntries {
		return nil, core.HashObj{}, errors.New("tree entry count exceeds limit")
	}

	sortedArr := append([]core.TreeEntryObj(nil), entriesArr...)
	sort.Slice(sortedArr, func(i, j int) bool {
		return sortedArr[i].Path < sortedArr[j].Path
	})

	seenPathObj := make(map[string]struct{}, len(sortedArr))
	for i := range sortedArr {
		if err := validateEntry(sortedArr[i]); err != nil {
			return nil, core.HashObj{}, err
		}
		if _, ok := seenPathObj[sortedArr[i].Path]; ok {
			return nil, core.HashObj{}, fmt.Errorf("duplicate tree path: %s", sortedArr[i].Path)
		}
		if seenParentPath(seenPathObj, sortedArr[i].Path) {
			return nil, core.HashObj{}, fmt.Errorf("tree contains file and child path conflict: %s", sortedArr[i].Path)
		}
		seenPathObj[sortedArr[i].Path] = struct{}{}
	}

	capacityValue, err := binaryCapacity(sortedArr)
	if err != nil {
		return nil, core.HashObj{}, err
	}

	dataArr := make([]byte, 0, capacityValue)
	dataArr = append(dataArr, BinaryMagic...)
	dataArr = binary.AppendUvarint(dataArr, uint64(len(sortedArr)))
	for i := range sortedArr {
		entryObj := sortedArr[i]
		modeValue, err := modeByte(entryObj.Mode)
		if err != nil {
			return nil, core.HashObj{}, err
		}

		dataArr = binary.AppendUvarint(dataArr, uint64(len(entryObj.Path)))
		dataArr = append(dataArr, entryObj.Path...)
		dataArr = append(dataArr, modeValue)
		dataArr = binary.AppendUvarint(dataArr, entryObj.SizeBytes)
		dataArr = append(dataArr, entryObj.BlobHash[:]...)
	}

	return dataArr, core.HashBytes(dataArr), nil
}
