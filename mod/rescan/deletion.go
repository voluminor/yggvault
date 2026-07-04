package rescan

import (
	"context"

	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func belowFloor(version string, floor string) bool {
	cmp, err := util.CompareSemver(version, floor)
	return err == nil && cmp < 0
}

// // // // // // // // // //

func (obj *Obj) incMiss(key string, version string) uint {
	obj.missMu.Lock()
	defer obj.missMu.Unlock()
	missObj := missKeyObj{key: key, version: version}
	obj.missMap[missObj]++
	return obj.missMap[missObj]
}

func (obj *Obj) resetMiss(key string, version string) {
	obj.missMu.Lock()
	defer obj.missMu.Unlock()
	delete(obj.missMap, missKeyObj{key: key, version: version})
}

func (obj *Obj) applyDeletionGrace(ctx context.Context, key string, upstreamSet map[string]struct{}, windowFloor string, minSeq int64) {
	localArr, err := obj.storageObj.ListVersions(ctx, key, false)
	if err != nil {
		return
	}
	graceCycles := obj.configObj.HistoryPolicy.Deletion.GraceCycles
	deleteMode := obj.configObj.HistoryPolicy.Deletion.Mode == stconf.HistoryDeletionModeDelete
	historyPrefix := obj.configObj.HistoryPolicy.Prefix

	for i := range localArr {
		version := localArr[i].Version
		if _, ok := upstreamSet[version]; ok {
			obj.resetMiss(key, version)
			continue
		}
		if windowFloor != "" && belowFloor(version, windowFloor) {
			continue
		}
		// Raw names cannot use the semver floor; their window guard is the source position.
		// Rows older than the listed seq window are not treated as deleted.
		if minSeq > 0 && util.IsRawVersionName(version) && localArr[i].UpstreamSeq > 0 && localArr[i].UpstreamSeq < minSeq {
			continue
		}
		if util.IsHistoricalVersion(version, historyPrefix) || localArr[i].UpstreamDeleted {
			continue
		}
		if obj.incMiss(key, version) < graceCycles {
			continue
		}
		if deleteMode {
			_ = obj.storageObj.DeleteVersion(ctx, key, version)
		} else {
			_ = obj.storageObj.MarkUpstreamDeleted(ctx, key, version)
		}
		obj.resetMiss(key, version)
	}
}
