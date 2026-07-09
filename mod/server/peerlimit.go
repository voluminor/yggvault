package server

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"

	"github.com/voluminor/yggvault/mod/internal/util"
)

// // // // // // // // // //

// cPeerLimiterShards is the number of limiter map shards; it must be a power of two.
const cPeerLimiterShards = 16

// // // //

func clientKey(r *http.Request) string {
	hostText, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return hostText
}

// // // // // // // // // //

type peerShardObj struct {
	muObj  sync.Mutex
	genMap *util.GenMapObj[*rate.Limiter]
}

func (obj *peerShardObj) limiter(keyText string, rateValue rate.Limit, burstValue int) *rate.Limiter {
	obj.muObj.Lock()
	defer obj.muObj.Unlock()
	if limiterObj, ok := obj.genMap.Get(keyText); ok {
		return limiterObj
	}
	limiterObj := rate.NewLimiter(rateValue, burstValue)
	obj.genMap.Put(keyText, limiterObj)
	return limiterObj
}

// // // // // // // // // //

type peerLimiterObj struct {
	rateValue  rate.Limit
	burstValue int
	shardArr   []*peerShardObj
}

func newPeerLimiter(ratePerSecond uint, burst uint, maxTracked uint) *peerLimiterObj {
	if ratePerSecond == 0 {
		return nil
	}
	burstVal := int(burst)
	if burstVal < 1 {
		burstVal = int(ratePerSecond)
	}
	perShardCap := int(maxTracked) / cPeerLimiterShards
	if perShardCap < 1 {
		perShardCap = 1
	}
	shardArr := make([]*peerShardObj, cPeerLimiterShards)
	for i := range shardArr {
		shardArr[i] = &peerShardObj{genMap: util.NewGenMap[*rate.Limiter](perShardCap)}
	}
	return &peerLimiterObj{rateValue: rate.Limit(ratePerSecond), burstValue: burstVal, shardArr: shardArr}
}

func (obj *peerLimiterObj) allow(keyText string) bool {
	if obj == nil {
		return true
	}
	shardObj := obj.shardArr[util.FNV32a(keyText)&uint32(len(obj.shardArr)-1)]
	return shardObj.limiter(keyText, obj.rateValue, obj.burstValue).Allow()
}
