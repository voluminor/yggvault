package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/osfs"
	"github.com/voluminor/yggvault/mod/storage/internal/hotverify"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// //

func samePathVolume(leftPath string, rightPath string) bool {
	return strings.EqualFold(filepath.VolumeName(leftPath), filepath.VolumeName(rightPath))
}

func (obj *Obj) retainHotPath(pathToFile string) func() error {
	obj.hotActiveMu.Lock()
	if obj.hotActiveObj == nil {
		obj.hotActiveObj = make(map[string]int)
	}
	obj.hotActiveObj[pathToFile]++
	obj.hotActiveMu.Unlock()

	return func() error {
		obj.hotActiveMu.Lock()
		countValue := obj.hotActiveObj[pathToFile]
		pendingDelete := false
		if countValue <= 1 {
			delete(obj.hotActiveObj, pathToFile)
			if _, ok := obj.hotDeleteObj[pathToFile]; ok {
				delete(obj.hotDeleteObj, pathToFile)
				pendingDelete = true
			}
		} else {
			obj.hotActiveObj[pathToFile] = countValue - 1
		}
		obj.hotActiveMu.Unlock()
		if pendingDelete {
			return obj.removeHotPath(pathToFile)
		}
		return nil
	}
}

func (obj *Obj) markHotDeletePending(pathToFile string) bool {
	obj.hotActiveMu.Lock()
	defer obj.hotActiveMu.Unlock()
	if obj.hotActiveObj[pathToFile] <= 0 {
		return false
	}
	if obj.hotDeleteObj == nil {
		obj.hotDeleteObj = make(map[string]struct{})
	}
	obj.hotDeleteObj[pathToFile] = struct{}{}
	return true
}

func pathInside(rootPath string, pathToFile string, outsideErrText string) (string, error) {
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(pathToFile)
	if err != nil {
		return "", err
	}
	if !samePathVolume(absRoot, absPath) {
		return "", errors.New(outsideErrText)
	}
	relPath, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", err
	}
	if relPath == "." || relPath == ".." || filepath.IsAbs(relPath) || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		return "", errors.New(outsideErrText)
	}
	return absPath, nil
}

func validateNoSymlinkParents(rootPath string, pathToFile string) error {
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return err
	}
	if err = validateStorageDir(absRoot); err != nil {
		return err
	}
	relPath, err := filepath.Rel(absRoot, pathToFile)
	if err != nil {
		return err
	}
	partArr := strings.Split(relPath, string(os.PathSeparator))
	if len(partArr) <= 1 {
		return nil
	}

	currentPath := absRoot
	for i := 0; i < len(partArr)-1; i++ {
		currentPath = filepath.Join(currentPath, partArr[i])
		infoObj, statErr := os.Lstat(currentPath)
		if os.IsNotExist(statErr) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if infoObj.Mode()&os.ModeSymlink != 0 || !infoObj.IsDir() {
			return errors.New("hot artifact parent path is not a regular directory")
		}
	}
	return nil
}

func (obj *Obj) validateHotPath(pathToFile string) (string, error) {
	pathToFile, err := pathInside(obj.hotDir, pathToFile, "path is outside hot cache")
	if err != nil {
		return "", err
	}
	if err = validateNoSymlinkParents(obj.hotDir, pathToFile); err != nil {
		return "", err
	}
	infoObj, err := os.Lstat(pathToFile)
	if err == nil && !osfs.IsRegularFile(infoObj) {
		return "", errors.New("hot artifact path is not a regular file")
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return pathToFile, nil
}

func (obj *Obj) validateHotTargetPath(pathToFile string) (string, error) {
	pathToFile, err := pathInside(obj.hotDir, pathToFile, "path is outside hot cache")
	if err != nil {
		return "", err
	}
	if err = validateNoSymlinkParents(obj.hotDir, pathToFile); err != nil {
		return "", err
	}
	return pathToFile, nil
}

func (obj *Obj) removeHotPath(pathToFile string) error {
	pathToFile, err := obj.validateHotPath(pathToFile)
	if err != nil {
		return err
	}
	err = os.Remove(pathToFile)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (obj *Obj) shouldVerifyHotRead() bool {
	switch obj.configObj.Storage.Hot.VerifyOnRead {
	case stcfg.HotVerifyOnReadNever:
		return false
	case stcfg.HotVerifyOnReadSampled:
		return obj.verifySampleCounter.Add(1)%cHotVerifySampleRate == 0
	default:
		return true
	}
}

func (obj *Obj) shouldRetainArtifactLocked(ctx context.Context, keyObj core.ArtifactKeyObj) (bool, error) {
	switch obj.configObj.Storage.Hot.Retain {
	case stcfg.CacheRetainModeAll:
		return true, nil
	case stcfg.CacheRetainModeNone:
		return false, nil
	case stcfg.CacheRetainModeLatest:
		versionObj, ok, err := obj.indexObj.LatestVersion(ctx, keyObj.Key)
		if err != nil || !ok {
			return false, err
		}
		return versionObj.Version == keyObj.Version, nil
	default:
		return false, errors.New("invalid hot retain mode")
	}
}

func (obj *Obj) openValidHotFile(ctx context.Context, artifactObj core.ArtifactObj) (*HotFileObj, bool, error) {
	if artifactObj.FilePath == "" {
		return nil, false, nil
	}
	// Hot serve is the most frequent path: containment is checked via pathInside, the leaf is protected
	// by OpenNoFollow + os.SameFile + IsRegularFile below. The parent-symlink walk stays on write/remove paths
	// to avoid adding about 5 Lstat syscalls to every request.
	filePath, err := pathInside(obj.hotDir, artifactObj.FilePath, "path is outside hot cache")
	if err != nil {
		return nil, false, err
	}
	lstatInfo, err := os.Lstat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !osfs.IsRegularFile(lstatInfo) {
		return nil, false, nil
	}
	releaseFunc := obj.retainHotPath(filePath)
	fileObj, err := osfs.OpenNoFollow(filePath)
	if err != nil {
		_ = releaseFunc()
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	infoObj, err := fileObj.Stat()
	if err != nil {
		_ = fileObj.Close()
		_ = releaseFunc()
		return nil, false, err
	}
	if !osfs.IsRegularFile(infoObj) || !os.SameFile(lstatInfo, infoObj) || uint64(infoObj.Size()) != artifactObj.SizeBytes {
		_ = fileObj.Close()
		_ = releaseFunc()
		return nil, false, nil
	}
	if obj.shouldVerifyHotRead() {
		if err = hotverify.VerifyOpenHotFile(ctx, fileObj, infoObj, artifactObj.BodyHash, artifactObj.SizeBytes); err != nil {
			_ = fileObj.Close()
			_ = releaseFunc()
			if errors.Is(err, hotverify.ErrHashMismatch) {
				_ = obj.removeHotPath(filePath)
				return nil, false, nil
			}
			return nil, false, err
		}
	}
	if time.Since(infoObj.ModTime()) >= cHotAccessUpdateInterval {
		accessTime := time.Now()
		_ = os.Chtimes(filePath, accessTime, accessTime)
	}
	return &HotFileObj{Path: filePath, File: fileObj, SizeBytes: artifactObj.SizeBytes, BodyHash: artifactObj.BodyHash, cleanup: releaseFunc}, true, nil
}
