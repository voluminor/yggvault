package pebblestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"

	"github.com/cockroachdb/pebble/v2"
	"github.com/cockroachdb/pebble/v2/bloom"
	"github.com/cockroachdb/pebble/v2/sstable/block"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

const cFormatKeyText = "\x00pebble_key_format"

const (
	cDefaultBlockCacheBytes = 128 << 20
	cDefaultMemTableBytes   = 64 << 20
	cPebbleBytesPerSync     = 1 << 20
)

// TuningObj stores Pebble config parameters; zero values use defaults.
type TuningObj struct {
	Compression     string
	BlockCacheBytes uint64
	MemTableBytes   uint64
}

// //

func (obj *Obj) empty() (bool, error) {
	iterObj, err := obj.dbObj.NewIter(nil)
	if err != nil {
		return false, err
	}
	emptyFlag := !iterObj.First()
	err = iterObj.Error()
	if closeErr := iterObj.Close(); err == nil {
		err = closeErr
	}
	return emptyFlag, err
}

func (obj *Obj) ensureFormat() error {
	valueArr, closeObj, err := obj.dbObj.Get([]byte(cFormatKeyText))
	if err == nil {
		matchFlag := bytes.Equal(valueArr, []byte(core.PebbleKeyFormat))
		valueText := string(append([]byte(nil), valueArr...))
		closeErr := closeObj.Close()
		if closeErr != nil {
			return closeErr
		}
		if !matchFlag {
			return fmt.Errorf("pebble key format mismatch: database=%s binary=%s", valueText, core.PebbleKeyFormat)
		}
		return nil
	}
	if !errors.Is(err, pebble.ErrNotFound) {
		return err
	}

	emptyFlag, err := obj.empty()
	if err != nil {
		return err
	}
	if !emptyFlag {
		return errors.New("pebble key format metadata is missing")
	}
	return obj.dbObj.Set([]byte(cFormatKeyText), []byte(core.PebbleKeyFormat), pebble.Sync)
}

func compressionSettings(nameText string) pebble.DBCompressionSettings {
	switch nameText {
	case "balanced":
		return pebble.DBCompressionBalanced
	case "good":
		return pebble.DBCompressionGood
	case "none":
		return pebble.DBCompressionNone
	}
	if profileObj := block.CompressionProfileByName(nameText); profileObj != nil {
		return pebble.UniformDBCompressionSettings(profileObj)
	}
	return pebble.UniformDBCompressionSettings(block.SnappyCompression)
}

func buildPebbleOptions(tuningObj TuningObj) *pebble.Options {
	blockCacheBytes := tuningObj.BlockCacheBytes
	if blockCacheBytes == 0 {
		blockCacheBytes = cDefaultBlockCacheBytes
	}
	memTableBytes := tuningObj.MemTableBytes
	if memTableBytes == 0 {
		memTableBytes = cDefaultMemTableBytes
	}
	settingsObj := compressionSettings(tuningObj.Compression)
	optionsObj := &pebble.Options{
		FormatMajorVersion:         pebble.FormatTableFormatV6,
		Cache:                      pebble.NewCache(int64(blockCacheBytes)),
		MemTableSize:               memTableBytes,
		BytesPerSync:               cPebbleBytesPerSync,
		CompactionConcurrencyRange: func() (int, int) { return 1, max(2, runtime.NumCPU()/2) },
	}
	optionsObj.ApplyCompressionSettings(func() pebble.DBCompressionSettings { return settingsObj })
	for i := range optionsObj.Levels {
		optionsObj.Levels[i].FilterPolicy = bloom.FilterPolicy(10)
	}
	return optionsObj
}

// //

// Open opens the store with default tuning.
func Open(pathToDir string) (*Obj, error) {
	return OpenWithTuning(pathToDir, TuningObj{})
}

// OpenWithTuning opens Pebble with a curated profile; zero fields use defaults.
func OpenWithTuning(pathToDir string, tuningObj TuningObj, loggerArr ...pebble.Logger) (*Obj, error) {
	countersObj := &pebbleEventCountersObj{}
	optionsObj := buildPebbleOptions(tuningObj)
	var loggerObj pebble.Logger = pebble.DefaultLogger
	if len(loggerArr) > 0 && loggerArr[0] != nil {
		loggerObj = loggerArr[0]
	}
	optionsObj.Logger = loggerObj
	optionsObj.EventListener = newEventListener(countersObj, loggerObj)
	if optionsObj.Cache != nil {
		defer optionsObj.Cache.Unref()
	}
	dbObj, err := pebble.Open(pathToDir, optionsObj)
	if err != nil {
		return nil, fmt.Errorf("open pebble: %w", err)
	}
	obj := &Obj{dbObj: dbObj, countersObj: countersObj}
	if err = obj.ensureFormat(); err != nil {
		_ = dbObj.Close()
		return nil, err
	}
	return obj, nil
}

// Close closes Pebble and is safe for a nil receiver and unopened DB.
func (obj *Obj) Close() error {
	if obj == nil || obj.dbObj == nil {
		return nil
	}
	return obj.dbObj.Close()
}

// DiskBytes returns live data plus WAL footprint for quota eviction.
func (obj *Obj) DiskBytes() uint64 {
	if obj == nil || obj.dbObj == nil {
		return 0
	}
	metricsObj := obj.dbObj.Metrics()
	return metricsObj.Table.Local.LiveSize + metricsObj.BlobFiles.Local.LiveSize + metricsObj.WAL.PhysicalSize
}

// RealDiskBytes returns the full physical footprint before obsolete data compaction.
func (obj *Obj) RealDiskBytes() uint64 {
	if obj == nil || obj.dbObj == nil {
		return 0
	}
	return obj.dbObj.Metrics().DiskSpaceUsage()
}

// CompactAll force-compacts the whole keyspace after bulk deletion.
// The upper bound is wider than any tag+hash key and covers the full keyspace.
func (obj *Obj) CompactAll(ctx context.Context) error {
	return obj.dbObj.Compact(ctx, []byte{0}, []byte{0xff, 0xff, 0xff, 0xff}, true)
}
