package cache

import (
	"container/list"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/singleflight"

	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	// cShardCount is the shard count and a power of two for get/set lock locality at up to 1e5 entries.
	cShardCount = 32

	// cEntryOverheadBytes charges fixed map/list/struct overhead to the byte budget so many tiny entries cannot hide
	// RAM usage behind payload length only.
	cEntryOverheadBytes = 64
)

// // // // // // // // // //

// EntryObj is a cached value: payload bytes plus the ETag validator for server-side 304 handling.
type EntryObj struct {
	Payload []byte
	ETag    string
}

type cacheItemObj struct {
	key    string
	value  EntryObj
	size   int64
	expiry int64 // Unix nanoseconds; now >= expiry is a cache miss.
}

func itemSize(key string, value EntryObj) int64 {
	return int64(len(key)+len(value.Payload)+len(value.ETag)) + cEntryOverheadBytes
}

// // // // // // // // // //

type shardObj struct {
	mu      sync.Mutex
	itemMap map[string]*list.Element
	lru     *list.List
	entries atomic.Int64
	bytes   atomic.Int64
}

// // // // // // // // // //

// Obj is the cache: shards, global atomic byte budget, singleflight, and metric counters.
type Obj struct {
	shardArr    [cShardCount]*shardObj
	budget      int64
	curBytes    atomic.Int64
	evictCursor atomic.Uint64
	flightObj   singleflight.Group
	buildGate   *BuildGateObj

	hits          atomic.Uint64
	misses        atomic.Uint64
	builds        atomic.Uint64
	buildsAborted atomic.Uint64
	evictions     atomic.Uint64
	shared        atomic.Uint64
}

// // // // // // // // // //

// New builds a cache with the byte budget from cache.metadata_max_size, validated in config.
func New(cacheCfgObj stconf.CacheObj, gateArr ...*BuildGateObj) *Obj {
	budget := int64(cacheCfgObj.MetadataMaxSize)
	if budget < cShardCount {
		budget = cShardCount
	}
	var gateObj *BuildGateObj
	if len(gateArr) > 0 {
		gateObj = gateArr[0]
	} else {
		gateObj = NewBuildGate(cacheCfgObj.BuildMaxParallel)
	}
	obj := &Obj{budget: budget, buildGate: gateObj}
	for i := range obj.shardArr {
		obj.shardArr[i] = &shardObj{
			itemMap: make(map[string]*list.Element),
			lru:     list.New(),
		}
	}
	return obj
}

func (obj *Obj) shardFor(key string) *shardObj {
	return obj.shardArr[util.FNV32a(key)&(cShardCount-1)]
}
