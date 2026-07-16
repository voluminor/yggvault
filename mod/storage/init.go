package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/rs/zerolog"

	"github.com/voluminor/yggvault/mod/internal/osfs"
	"github.com/voluminor/yggvault/mod/storage/pebblestore"
	"github.com/voluminor/yggvault/mod/storage/sqliteindex"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// //

func prepareLayout(rootPath string) (string, string, string, string, error) {
	if strings.TrimSpace(rootPath) == "" {
		return "", "", "", "", errors.New("storage.dir is empty")
	}

	absPath, err := filepath.Abs(rootPath)
	if err != nil {
		return "", "", "", "", fmt.Errorf("resolve storage.dir: %w", err)
	}
	if err = validateStoragePath(absPath); err != nil {
		return "", "", "", "", fmt.Errorf("validate storage.dir path: %w", err)
	}
	if err = os.MkdirAll(absPath, 0o700); err != nil {
		return "", "", "", "", fmt.Errorf("create storage.dir: %w", err)
	}
	if err = validateStorageDir(absPath); err != nil {
		return "", "", "", "", fmt.Errorf("validate storage.dir: %w", err)
	}

	pebblePath := filepath.Join(absPath, cDirPebbleName)
	hotPath := filepath.Join(absPath, cDirHotName)
	tempPath := filepath.Join(absPath, cDirTempName)
	for _, dirPath := range []string{pebblePath, hotPath, tempPath} {
		if err = os.MkdirAll(dirPath, 0o700); err != nil {
			return "", "", "", "", fmt.Errorf("create storage directory %s: %w", dirPath, err)
		}
		if err = validateStorageDir(dirPath); err != nil {
			return "", "", "", "", fmt.Errorf("validate storage directory %s: %w", dirPath, err)
		}
	}

	return absPath, pebblePath, hotPath, tempPath, nil
}

func validateStoragePath(pathText string) error {
	cleanPath, err := filepath.Abs(pathText)
	if err != nil {
		return err
	}
	currentPath, relPath := splitPathRoot(cleanPath)
	for _, partText := range strings.Split(relPath, string(filepath.Separator)) {
		if partText == "" {
			continue
		}
		currentPath = filepath.Join(currentPath, partText)
		infoObj, err := os.Lstat(currentPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if infoObj.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("storage path contains symlink component: %s", currentPath)
		}
		if currentPath != cleanPath && !infoObj.IsDir() {
			return fmt.Errorf("storage path parent is not a regular directory: %s", currentPath)
		}
	}
	return nil
}

func splitPathRoot(cleanPath string) (string, string) {
	volumeText := filepath.VolumeName(cleanPath)
	rootPath := string(filepath.Separator)
	if volumeText != "" {
		rootPath = volumeText + string(filepath.Separator)
	}
	return rootPath, strings.TrimPrefix(cleanPath, rootPath)
}

func validateStorageDir(pathToDir string) error {
	infoObj, err := os.Lstat(pathToDir)
	if err != nil {
		return err
	}
	if infoObj.Mode()&os.ModeSymlink != 0 || !infoObj.IsDir() {
		return errors.New("storage directory is not a regular directory")
	}
	return nil
}

func validateStorageFile(pathToFile string) error {
	infoObj, err := os.Lstat(pathToFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if infoObj.Mode()&os.ModeSymlink != 0 || !osfs.IsRegularFile(infoObj) {
		return errors.New("storage file is not a regular file")
	}
	return nil
}

func cleanupTempDir(ctx context.Context, pathToDir string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if err := os.RemoveAll(pathToDir); err != nil {
		return err
	}
	if err := os.MkdirAll(pathToDir, 0o700); err != nil {
		return err
	}
	if err := validateStorageDir(pathToDir); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func buildSemaphoreSize(configObj *stcfg.ConfigObj) uint {
	sizeValue := configObj.Storage.OverlayBuildMaxParallel
	if sizeValue == 0 {
		return 1
	}
	return sizeValue
}

func (obj *Obj) finishClose(doneChan chan struct{}) {
	obj.activeWG.Wait()
	<-obj.gcLoopDone

	obj.metricRegMu.Lock()
	regArr := obj.metricRegArr
	obj.metricRegArr = nil
	obj.metricRegMu.Unlock()
	for _, regObj := range regArr {
		_ = regObj.Unregister()
	}

	var errArr []error
	if obj.pebbleStoreObj != nil {
		if err := obj.pebbleStoreObj.Close(); err != nil {
			errArr = append(errArr, fmt.Errorf("close pebble: %w", err))
		}
	}
	if obj.indexObj != nil {
		ctx, cancelFunc := context.WithTimeout(context.Background(), 30*time.Second)
		if err := obj.indexObj.CloseContext(ctx); err != nil {
			errArr = append(errArr, err)
		}
		cancelFunc()
	}

	obj.closeMu.Lock()
	obj.closeErr = errors.Join(errArr...)
	if obj.closeErr != nil {
		obj.logObj.Error().Err(obj.closeErr).Str("component", "storage").Msg("storage close failed")
	} else {
		obj.logObj.Info().Str("component", "storage").Msg("storage closed")
	}
	close(doneChan)
	obj.closeMu.Unlock()
}

func (obj *Obj) waitClose(ctx context.Context, doneChan chan struct{}) error {
	timerObj := time.NewTimer(30 * time.Second)
	defer timerObj.Stop()
	select {
	case <-doneChan:
		obj.closeMu.RLock()
		err := obj.closeErr
		obj.closeMu.RUnlock()
		return err
	case <-ctx.Done():
		return fmt.Errorf("close storage: %w", ctx.Err())
	case <-timerObj.C:
		return errors.New("close storage timed out")
	}
}

// //

// New opens storage, prepares the layout, cleans temp, opens SQLite/Pebble and runs Recover.
// The config is copied so ownership is not shared with the caller.
func New(ctx context.Context, configObj *stcfg.ConfigObj, logArr ...zerolog.Logger) (*Obj, error) {
	if configObj == nil {
		return nil, errors.New("storage config is nil")
	}
	logObj := zerolog.Nop()
	pebbleLoggerArr := []pebble.Logger(nil)
	if len(logArr) > 0 {
		logObj = logArr[0]
		pebbleLoggerArr = append(pebbleLoggerArr, pebblestore.NewLogger(logObj))
	}
	configCopyObj := *configObj
	configObj = &configCopyObj

	if err := validateMemoryBudget(configObj); err != nil {
		return nil, err
	}

	rootPath, pebblePath, hotPath, tempPath, err := prepareLayout(configObj.Storage.Dir)
	if err != nil {
		return nil, err
	}
	if err = cleanupTempDir(ctx, tempPath); err != nil {
		return nil, fmt.Errorf("clean storage temp dir: %w", err)
	}

	indexPath := filepath.Join(rootPath, cFileIndexName)
	if err = validateStorageFile(indexPath); err != nil {
		return nil, fmt.Errorf("validate storage index file: %w", err)
	}
	indexObj, err := sqliteindex.Open(ctx, indexPath)
	if err != nil {
		return nil, err
	}

	pebbleStoreObj, err := pebblestore.OpenWithTuning(pebblePath, pebblestore.TuningObj{
		Compression:     configObj.Storage.Pebble.Compression.String(),
		BlockCacheBytes: uint64(configObj.Storage.Pebble.BlockCacheSize),
		MemTableBytes:   uint64(configObj.Storage.Pebble.MemtableSize),
	}, pebbleLoggerArr...)
	if err != nil {
		_ = indexObj.CloseContext(ctx)
		return nil, err
	}

	obj := &Obj{
		configObj:      configObj,
		rootPath:       rootPath,
		indexPath:      indexPath,
		pebbleDir:      pebblePath,
		hotDir:         hotPath,
		tempDir:        tempPath,
		logObj:         logObj,
		indexObj:       indexObj,
		pebbleStoreObj: pebbleStoreObj,
		hotActiveObj:   make(map[string]int),
		publishSem:     make(chan struct{}, 1),
		buildSem:       make(chan struct{}, buildSemaphoreSize(configObj)),
		gcLoopDone:     make(chan struct{}),
		gcTrigger:      make(chan struct{}, 1),
	}
	obj.inFlightSlots = buildInFlightSlots(uint64(configObj.Storage.InFlightReadBytes), obj.maxBlobBytes())
	obj.rootCtx, obj.rootCancel = context.WithCancel(context.Background())
	go obj.runDurableGCLoop()

	if err = obj.Recover(ctx); err != nil {
		_ = obj.Close(ctx)
		return nil, err
	}
	obj.logObj.Info().
		Str("component", "storage").
		Str("root", rootPath).
		Str("pebble", pebblePath).
		Str("sqlite", indexPath).
		Msg("storage opened")
	return obj, nil
}

// Close stops storage idempotently.
// It cancels the root context, waits for active operations/background GC and closes Pebble/index.
// Repeated calls wait for the same completion; ctx bounds the wait.
func (obj *Obj) Close(ctx context.Context) error {
	if obj == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	obj.closeMu.Lock()
	if obj.closedFlag {
		doneChan := obj.closeDoneChan
		obj.closeMu.Unlock()
		return obj.waitClose(ctx, doneChan)
	}
	obj.closedFlag = true
	obj.closeDoneChan = make(chan struct{})
	doneChan := obj.closeDoneChan
	obj.rootCancel()
	obj.closeMu.Unlock()

	go obj.finishClose(doneChan)
	return obj.waitClose(ctx, doneChan)
}

func (obj *Obj) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = obj.rootCtx
	}
	mergedCtx, cancelFunc := context.WithCancel(ctx)
	stopFunc := context.AfterFunc(obj.rootCtx, cancelFunc)
	return mergedCtx, func() {
		stopFunc()
		cancelFunc()
	}
}

const cDurableGCInterval = 10 * time.Minute

func (obj *Obj) triggerGC() {
	select {
	case obj.gcTrigger <- struct{}{}:
	default:
	}
}

func (obj *Obj) runDurableGCLoop() {
	defer close(obj.gcLoopDone)
	intervalValue := obj.configObj.Storage.Quota.GcInterval
	if intervalValue <= 0 {
		intervalValue = cDurableGCInterval
	}
	tickerObj := time.NewTicker(intervalValue)
	defer tickerObj.Stop()
	for {
		select {
		case <-obj.rootCtx.Done():
			return
		case <-tickerObj.C:
		case <-obj.gcTrigger:
		}
		if err := obj.CollectGarbage(obj.rootCtx); err != nil && obj.rootCtx.Err() == nil {
			obj.logObj.Warn().
				Err(err).
				Str("component", "storage").
				Msg("storage background garbage collection failed")
		}
		if maxEvents := obj.configObj.HistoryPolicy.MaxEvents; maxEvents > 0 {
			if err := obj.pruneHistoryUnderLock(obj.rootCtx, maxEvents); err != nil && obj.rootCtx.Err() == nil {
				obj.logObj.Warn().
					Err(err).
					Str("component", "storage").
					Uint("max_events", maxEvents).
					Msg("storage background history pruning failed")
			}
		}
	}
}

func (obj *Obj) pruneHistoryUnderLock(ctx context.Context, maxEvents uint) error {
	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()
	_, err := obj.indexObj.PruneHistory(ctx, maxEvents)
	return err
}
