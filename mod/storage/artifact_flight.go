package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

// //

func (obj *Obj) joinArtifactFlight(flightKey string) (*artifactFlightObj, bool) {
	obj.flightMu.Lock()
	defer obj.flightMu.Unlock()

	if obj.flightMap == nil {
		obj.flightMap = make(map[string]*artifactFlightObj)
	}
	if flightObj := obj.flightMap[flightKey]; flightObj != nil {
		flightObj.waiters++
		return flightObj, false
	}

	ctx, cancel := context.WithCancel(obj.rootCtx)
	flightObj := &artifactFlightObj{
		ctx:      ctx,
		cancel:   cancel,
		doneChan: make(chan struct{}),
		waiters:  1,
	}
	obj.flightMap[flightKey] = flightObj
	return flightObj, true
}

func (obj *Obj) leaveArtifactFlight(flightObj *artifactFlightObj) {
	var cleanupObj *hotSharedFileObj

	obj.flightMu.Lock()
	if flightObj.waiters > 0 {
		flightObj.waiters--
	}
	if !flightObj.doneFlag && flightObj.waiters == 0 {
		flightObj.cancel()
	}
	if flightObj.doneFlag {
		if sharedObj, ok := flightObj.result.(*hotSharedFileObj); ok && sharedObj != nil && sharedObj.cleanup != nil {
			if sharedObj.refs.Add(-1) == 0 {
				cleanupObj = sharedObj
			}
		}
	}
	obj.flightMu.Unlock()

	if cleanupObj != nil {
		_ = cleanupObj.cleanupUnused()
	}
}

func (obj *Obj) finishArtifactFlight(flightKey string, flightObj *artifactFlightObj, resultObj any, err error) {
	var cleanupObj *hotSharedFileObj

	obj.flightMu.Lock()
	flightObj.result = resultObj
	flightObj.err = err
	flightObj.doneFlag = true
	if currentObj := obj.flightMap[flightKey]; currentObj == flightObj {
		delete(obj.flightMap, flightKey)
	}
	if sharedObj, ok := resultObj.(*hotSharedFileObj); ok && sharedObj != nil && sharedObj.cleanup != nil {
		if flightObj.waiters == 0 {
			cleanupObj = sharedObj
		} else {
			sharedObj.refs.Add(int64(flightObj.waiters))
		}
	}
	close(flightObj.doneChan)
	obj.flightMu.Unlock()

	if cleanupObj != nil {
		_ = cleanupObj.cleanupUnused()
	}
}

func (obj *Obj) runArtifactFlight(flightKey string, flightObj *artifactFlightObj, keyObj core.ArtifactKeyObj, builderObj ArtifactBuilderInterface) {
	var resultObj any
	var err error
	defer func() {
		if panicObj := recover(); panicObj != nil {
			resultObj = nil
			err = newArtifactBuildErr(keyObj, fmt.Errorf("artifact builder panic: %v", panicObj), cArtifactCheckPanic, "", "", 0, 0)
		}
		obj.finishArtifactFlight(flightKey, flightObj, resultObj, err)
	}()

	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return
	}
	defer releaseFunc()

	resultObj, err = obj.buildArtifactFlight(flightObj.ctx, keyObj, builderObj)
}

func (obj *Obj) buildArtifactFlight(ctx context.Context, keyObj core.ArtifactKeyObj, builderObj ArtifactBuilderInterface) (any, error) {
	artifactObj, ok, err := obj.getArtifact(ctx, keyObj)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("artifact metadata not found")
	}
	if fileObj, exists, openErr := obj.openValidHotFile(ctx, artifactObj); openErr != nil {
		return nil, openErr
	} else if exists {
		sharedObj := &hotSharedFileObj{
			path:      fileObj.Path,
			sizeBytes: fileObj.SizeBytes,
			bodyHash:  fileObj.BodyHash,
			retain: func() func() error {
				return obj.retainHotPath(fileObj.Path)
			},
		}
		_ = fileObj.Close()
		return sharedObj, nil
	}
	return obj.buildHotFile(ctx, keyObj, artifactObj, builderObj)
}

func artifactResultFile(resultObj any) (*HotFileObj, error) {
	sharedObj, ok := resultObj.(*hotSharedFileObj)
	if !ok || sharedObj == nil {
		return nil, errors.New("unexpected artifact build result")
	}
	return sharedObj.hotFileObj()
}
