package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// // // // // // // // // //

const (
	// cObjCacheCap caps live object-cache entries.
	// Two generations make the real cap about 2*cap without per-entry eviction.
	cObjCacheCap = 4096

	// cObjBuildBudget caps detached builds so abandoned singleflight calls do not run forever.
	cObjBuildBudget = 60 * time.Second
)

// // // //

type objEntryObj struct {
	value  any
	expiry int64
}

type objCacheObj struct {
	muObj     sync.Mutex
	curMap    map[string]objEntryObj
	prevMap   map[string]objEntryObj
	flightObj singleflight.Group
}

// // // //

func newObjCache() *objCacheObj {
	return &objCacheObj{curMap: make(map[string]objEntryObj, cObjCacheCap), prevMap: map[string]objEntryObj{}}
}

func (obj *objCacheObj) lookup(key string, nowNano int64) (any, bool) {
	obj.muObj.Lock()
	defer obj.muObj.Unlock()
	if entryObj, ok := obj.curMap[key]; ok {
		if nowNano >= entryObj.expiry {
			delete(obj.curMap, key)
			return nil, false
		}
		return entryObj.value, true
	}
	if entryObj, ok := obj.prevMap[key]; ok {
		if nowNano >= entryObj.expiry {
			delete(obj.prevMap, key)
			return nil, false
		}
		obj.curMap[key] = entryObj
		return entryObj.value, true
	}
	return nil, false
}

func (obj *objCacheObj) store(key string, value any, ttl time.Duration, nowNano int64) {
	if ttl <= 0 {
		return
	}
	obj.muObj.Lock()
	defer obj.muObj.Unlock()
	if len(obj.curMap) >= cObjCacheCap {
		obj.prevMap = obj.curMap
		obj.curMap = make(map[string]objEntryObj, cObjCacheCap)
	}
	obj.curMap[key] = objEntryObj{value: value, expiry: nowNano + ttl.Nanoseconds()}
}

func (obj *objCacheObj) getOrBuild(ctx context.Context, key string, ttl time.Duration, build func(context.Context) (any, error)) (any, error) {
	if value, ok := obj.lookup(key, time.Now().UnixNano()); ok {
		return value, nil
	}
	resultChan := obj.flightObj.DoChan(key, func() (resultValue any, resultErr error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				resultValue, resultErr = nil, fmt.Errorf("object cache build panicked: %v", recovered)
			}
		}()
		if value, ok := obj.lookup(key, time.Now().UnixNano()); ok {
			return value, nil
		}
		buildCtx, cancelBuild := context.WithTimeout(context.WithoutCancel(ctx), cObjBuildBudget)
		defer cancelBuild()
		builtValue, buildErr := build(buildCtx)
		if buildErr != nil {
			return nil, buildErr
		}
		obj.store(key, builtValue, ttl, time.Now().UnixNano())
		return builtValue, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resultObj := <-resultChan:
		return resultObj.Val, resultObj.Err
	}
}
