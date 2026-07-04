package server

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

// // // // // // // // // //

// cPeerLimiterShards is the number of limiter map shards; it must be a power of two.
const cPeerLimiterShards = 16

// // // //

func shardHash(keyText string) uint32 {
	hashValue := uint32(2166136261)
	for i := 0; i < len(keyText); i++ {
		hashValue ^= uint32(keyText[i])
		hashValue *= 16777619
	}
	return hashValue
}

func clientKey(r *http.Request) string {
	hostText, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return hostText
}

// // // // // // // // // //

type peerShardObj struct {
	muObj   sync.Mutex
	curMap  map[string]*rate.Limiter
	prevMap map[string]*rate.Limiter
	capVal  int
}

func (obj *peerShardObj) limiter(keyText string, rateValue rate.Limit, burstValue int) *rate.Limiter {
	obj.muObj.Lock()
	defer obj.muObj.Unlock()
	if limiterObj, ok := obj.curMap[keyText]; ok {
		return limiterObj
	}
	if limiterObj, ok := obj.prevMap[keyText]; ok {
		obj.curMap[keyText] = limiterObj
		return limiterObj
	}
	limiterObj := rate.NewLimiter(rateValue, burstValue)
	if len(obj.curMap) >= obj.capVal {
		obj.prevMap = obj.curMap
		obj.curMap = make(map[string]*rate.Limiter, obj.capVal)
	}
	obj.curMap[keyText] = limiterObj
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
		shardArr[i] = &peerShardObj{
			curMap:  make(map[string]*rate.Limiter, perShardCap),
			prevMap: map[string]*rate.Limiter{},
			capVal:  perShardCap,
		}
	}
	return &peerLimiterObj{rateValue: rate.Limit(ratePerSecond), burstValue: burstVal, shardArr: shardArr}
}

func (obj *peerLimiterObj) allow(keyText string) bool {
	if obj == nil {
		return true
	}
	shardObj := obj.shardArr[shardHash(keyText)&uint32(len(obj.shardArr)-1)]
	return shardObj.limiter(keyText, obj.rateValue, obj.burstValue).Allow()
}
