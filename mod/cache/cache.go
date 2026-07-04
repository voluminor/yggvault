package cache

import (
	"context"
	"fmt"
	"time"
)

// // // // // // // // // //

// cBuildBudget caps detached single-key builds after the caller context is stripped. It is a defensive ceiling
// against runaway work, not the normal expected build duration.
const cBuildBudget = 60 * time.Second

// // // // // // // // // //

func (obj *Obj) lookup(key string) (EntryObj, bool) {
	value, found, freed := obj.shardFor(key).get(key, time.Now().UnixNano())
	if freed > 0 {
		obj.curBytes.Add(-freed)
	}
	return value, found
}

// Get returns a lazy-TTL cache hit and records hit/miss counters.
func (obj *Obj) Get(key string) (EntryObj, bool) {
	value, found := obj.lookup(key)
	if found {
		obj.hits.Add(1)
	} else {
		obj.misses.Add(1)
	}
	return value, found
}

// Set stores a value with TTL. ttl<=0 is a no-op; after insertion it evicts back to budget.
func (obj *Obj) Set(key string, value EntryObj, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	itemObj := &cacheItemObj{
		key:    key,
		value:  value,
		size:   itemSize(key, value),
		expiry: time.Now().Add(ttl).UnixNano(),
	}
	obj.curBytes.Add(obj.shardFor(key).insert(itemObj))
	obj.evictToBudget()
}

// GetOrBuild does lookup, builds exactly once through singleflight on miss, stores the result, and returns it to all
// waiters. Build errors are not cached. hit=true only means a fast-path cache hit.
func (obj *Obj) GetOrBuild(ctx context.Context, key string, ttl time.Duration, buildFn func(context.Context) (EntryObj, error)) (EntryObj, bool, error) {
	if value, found := obj.Get(key); found {
		return value, true, nil
	}

	resultChan := obj.flightObj.DoChan(key, func() (resultValue any, resultErr error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				resultValue, resultErr = EntryObj{}, fmt.Errorf("cache build panicked: %v", recovered)
			}
		}()
		if value, found := obj.lookup(key); found {
			return value, nil
		}
		buildCtx, cancelBuild := context.WithTimeout(context.WithoutCancel(ctx), cBuildBudget)
		defer cancelBuild()
		obj.builds.Add(1)
		builtObj, buildErr := buildFn(buildCtx)
		if buildErr != nil {
			if buildCtx.Err() == context.DeadlineExceeded {
				obj.buildsAborted.Add(1)
			}
			return EntryObj{}, buildErr
		}
		obj.Set(key, builtObj, ttl)
		return builtObj, nil
	})

	select {
	case <-ctx.Done():
		return EntryObj{}, false, ctx.Err()
	case resultObj := <-resultChan:
		if resultObj.Shared {
			obj.shared.Add(1)
		}
		if resultObj.Err != nil {
			return EntryObj{}, false, resultObj.Err
		}
		return resultObj.Val.(EntryObj), false, nil
	}
}

// // // // // // // // // //

func (obj *Obj) evictToBudget() {
	emptyStreak := 0
	for obj.curBytes.Load() > obj.budget {
		idx := obj.evictCursor.Add(1) % cShardCount
		freed := obj.shardArr[idx].evictTail()
		if freed == 0 {
			emptyStreak++
			if emptyStreak >= cShardCount {
				return
			}
			continue
		}
		emptyStreak = 0
		obj.curBytes.Add(-freed)
		obj.evictions.Add(1)
	}
}

// Clear removes all cache entries for lifecycle or restart invalidation.
func (obj *Obj) Clear() {
	for i := range obj.shardArr {
		obj.curBytes.Add(-obj.shardArr[i].clear())
	}
}
