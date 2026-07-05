package overlay

import (
	"bytes"

	"golang.org/x/mod/module"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
)

// // // // // // // // // //

func isModulePathByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '.', c == '-', c == '_', c == '~', c == '/':
		return true
	}
	return false
}

func moduleBoundary(dataArr []byte, start int, end int) bool {
	if start > 0 && isModulePathByte(dataArr[start-1]) {
		return false
	}
	if end < len(dataArr) {
		nextByte := dataArr[end]
		if nextByte != '/' && isModulePathByte(nextByte) {
			return false
		}
	}
	return true
}

func rewriteContent(dataArr []byte, oldArr []byte, newArr []byte) []byte {
	if len(oldArr) == 0 || bytes.Equal(oldArr, newArr) {
		return dataArr
	}
	resultArr := make([]byte, 0, len(dataArr))
	pos := 0
	for {
		idx := bytes.Index(dataArr[pos:], oldArr)
		if idx < 0 {
			resultArr = append(resultArr, dataArr[pos:]...)
			break
		}
		start := pos + idx
		end := start + len(oldArr)
		resultArr = append(resultArr, dataArr[pos:start]...)
		if moduleBoundary(dataArr, start, end) {
			resultArr = append(resultArr, newArr...)
		} else {
			resultArr = append(resultArr, oldArr...)
		}
		pos = end
	}
	return resultArr
}

func rewrittenSize(dataArr []byte, oldArr []byte, newArr []byte) int {
	if len(oldArr) == 0 || bytes.Equal(oldArr, newArr) {
		return len(dataArr)
	}
	delta := len(newArr) - len(oldArr)
	total := len(dataArr)
	pos := 0
	for {
		idx := bytes.Index(dataArr[pos:], oldArr)
		if idx < 0 {
			break
		}
		start := pos + idx
		end := start + len(oldArr)
		if moduleBoundary(dataArr, start, end) {
			total += delta
		}
		pos = end
	}
	return total
}

func containsModulePath(dataArr []byte, needleArr []byte) bool {
	if len(needleArr) == 0 {
		return false
	}
	pos := 0
	for {
		idx := bytes.Index(dataArr[pos:], needleArr)
		if idx < 0 {
			return false
		}
		start := pos + idx
		if moduleBoundary(dataArr, start, start+len(needleArr)) {
			return true
		}
		pos = start + 1
	}
}

func hashSetOf(hashArr []core.HashObj) map[core.HashObj]struct{} {
	set := make(map[core.HashObj]struct{}, len(hashArr))
	for _, hashObj := range hashArr {
		set[hashObj] = struct{}{}
	}
	return set
}

// // // // // // // // // //

type rewritePlanObj struct {
	OldArr  []byte
	NewArr  []byte
	Rewrite bool
}

func (obj *Obj) rewritePlan(key string, version string, detectionObj core.DetectionObj, candidateObj *CandidateObj, listenerCtxObj ListenerCtxObj) rewritePlanObj {
	if !obj.rewriteEnabled || candidateObj == nil || !detectionObj.IsGo || detectionObj.Conflict {
		return rewritePlanObj{}
	}
	targetPath := obj.targetModulePath(key, version, listenerCtxObj)
	if targetPath == "" || targetPath == candidateObj.GoModulePath {
		return rewritePlanObj{}
	}
	return rewritePlanObj{OldArr: []byte(candidateObj.GoModulePath), NewArr: []byte(targetPath), Rewrite: true}
}

// GoPublishable reports whether a version has Go @v routes: unambiguous Go detection, a tree able to form a valid
// go module zip, canonical `vX.Y.Z`, valid module path with matching major suffix, and rewrite either unnecessary
// or allowed. This is the shared Go serve gate.
func (obj *Obj) GoPublishable(key string, version string, detectionObj core.DetectionObj, candidateObj *CandidateObj, listenerCtxObj ListenerCtxObj) bool {
	if !detectionObj.IsGo || detectionObj.Conflict || candidateObj == nil {
		return false
	}
	// Unbuildable module-zip trees are deliberately excluded from Go overlay routes.
	if detectionObj.GoZipBlocked {
		return false
	}
	if !util.IsCanonicalSemver(version) {
		return false
	}
	targetPath := obj.targetModulePath(key, version, listenerCtxObj)
	if module.Check(targetPath, version) != nil {
		return false
	}
	if targetPath == candidateObj.GoModulePath {
		return true
	}
	return obj.rewriteEnabled
}
