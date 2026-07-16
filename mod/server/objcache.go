package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/voluminor/yggvault/mod/cache"
	"github.com/voluminor/yggvault/mod/internal/util"
)

// // // // // // // // // //

const (
	cObjCacheCap = 4096

	cObjBuildBudget = 60 * time.Second
)

// // // //

type objEntryObj struct {
	value  any
	expiry int64
}

type objCacheObj struct {
	muObj     sync.Mutex
	genMap    *util.GenMapObj[objEntryObj]
	flightObj singleflight.Group
	buildGate *cache.BuildGateObj
}

// // // //

func newObjCache(gateObj *cache.BuildGateObj) *objCacheObj {
	return &objCacheObj{
		genMap:    util.NewGenMap[objEntryObj](cObjCacheCap),
		buildGate: gateObj,
	}
}

func (obj *objCacheObj) lookup(key string, nowNano int64) (any, bool) {
	obj.muObj.Lock()
	defer obj.muObj.Unlock()
	entryObj, ok := obj.genMap.Get(key)
	if !ok {
		return nil, false
	}
	if nowNano >= entryObj.expiry {
		obj.genMap.Delete(key)
		return nil, false
	}
	return entryObj.value, true
}

func (obj *objCacheObj) store(key string, value any, ttl time.Duration, nowNano int64) {
	if ttl <= 0 {
		return
	}
	obj.muObj.Lock()
	defer obj.muObj.Unlock()
	obj.genMap.Put(key, objEntryObj{value: value, expiry: nowNano + ttl.Nanoseconds()})
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
		if err := obj.buildGate.Acquire(buildCtx); err != nil {
			return nil, err
		}
		defer obj.buildGate.Release()
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
