package source

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const cMaxTrackedHosts = 256

// // // // // // // // // //

type hostLimiterObj struct {
	mu     sync.Mutex
	limit  rate.Limit
	burst  int
	active map[string]*rate.Limiter
	stale  map[string]*rate.Limiter
}

type limitTransportObj struct {
	base  http.RoundTripper
	owner *Obj
}

// // // // // // // // // //

func newHostLimiterObj(configObj stconf.SourceRateLimitObj) *hostLimiterObj {
	if configObj.RequestsPerSecond == 0 {
		return nil
	}
	burstValue := int(configObj.Burst)
	if burstValue < 1 {
		burstValue = 1
	}
	return &hostLimiterObj{
		limit:  rate.Limit(configObj.RequestsPerSecond),
		burst:  burstValue,
		active: make(map[string]*rate.Limiter),
	}
}

func (obj *hostLimiterObj) limiterFor(hostText string) *rate.Limiter {
	hostText = strings.ToLower(hostText)

	obj.mu.Lock()
	defer obj.mu.Unlock()

	if limiterObj, ok := obj.active[hostText]; ok {
		return limiterObj
	}
	if limiterObj, ok := obj.stale[hostText]; ok {
		delete(obj.stale, hostText)
		obj.active[hostText] = limiterObj
		return limiterObj
	}
	if len(obj.active) >= cMaxTrackedHosts {
		obj.stale = obj.active
		obj.active = make(map[string]*rate.Limiter)
	}
	limiterObj := rate.NewLimiter(obj.limit, obj.burst)
	obj.active[hostText] = limiterObj
	return limiterObj
}

func (obj *hostLimiterObj) wait(ctx context.Context, hostText string) error {
	if obj == nil {
		return nil
	}
	return obj.limiterFor(hostText).Wait(ctx)
}

func roundTripBase(base http.RoundTripper) http.RoundTripper {
	if base != nil {
		return base
	}
	return http.DefaultTransport
}

// // // // // // // // // //

func (transportObj *limitTransportObj) RoundTrip(reqObj *http.Request) (*http.Response, error) {
	baseObj := roundTripBase(transportObj.base)
	if transportObj.owner == nil || transportObj.owner.upstreamLimiter == nil {
		return baseObj.RoundTrip(reqObj)
	}

	startObj := time.Now()
	err := transportObj.owner.upstreamLimiter.wait(reqObj.Context(), reqObj.URL.Hostname())
	if err != nil {
		return nil, err
	}
	transportObj.owner.metricsObj.recordUpstreamLimiterWait(time.Since(startObj))
	transportObj.owner.metricsObj.recordUpstreamRequest()
	return baseObj.RoundTrip(reqObj)
}

func (obj *Obj) withLimit(base http.RoundTripper) http.RoundTripper {
	if obj.upstreamLimiter == nil {
		return base
	}
	return &limitTransportObj{base: base, owner: obj}
}
