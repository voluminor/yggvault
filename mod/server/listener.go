package server

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"

	"github.com/voluminor/yggvault/mod/mesh"
	"github.com/voluminor/yggvault/target/api"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type listenerCtxKeyObj struct{}

type requestIDKeyObj struct{}

// // // // // // // // // //

type listenerObj struct {
	label       string
	httpServer  *http.Server
	netListener net.Listener
}

type listenerCtxObj struct {
	listenerID             stcode.ListenerType
	entryHost              string
	healthSource           api.HealthObjSource
	limiter                *rate.Limiter
	peerLimiter            *peerLimiterObj
	publicMetricsEnabled   bool
	internalMetricsEnabled bool
	secure                 bool
}

func (lc listenerCtxObj) scheme() string {
	if lc.secure {
		return "https"
	}
	return "http"
}

func (lc listenerCtxObj) rateLimited(r *http.Request) bool {
	if lc.limiter != nil && !lc.limiter.Allow() {
		return true
	}
	if lc.peerLimiter == nil {
		return false
	}
	return !lc.peerLimiter.allow(clientKey(r))
}

// // // // // // // // // //

func newLimiter(ratePerSecond uint, burst uint) *rate.Limiter {
	if ratePerSecond == 0 {
		return nil
	}
	burstVal := int(burst)
	if burstVal < 1 {
		burstVal = int(ratePerSecond)
	}
	return rate.NewLimiter(rate.Limit(ratePerSecond), burstVal)
}

func listenerCtxFrom(ctx context.Context) listenerCtxObj {
	lc, _ := ctx.Value(listenerCtxKeyObj{}).(listenerCtxObj)
	return lc
}

func (obj *Obj) webListenerCtx(protoHTTPS bool) listenerCtxObj {
	limiterObj := newLimiter(obj.cfg.RateLimit.Web.Http.RequestsPerSecond, obj.cfg.RateLimit.Web.Http.Burst)
	peerObj := newPeerLimiter(obj.cfg.RateLimit.Web.Http.PerPeer.RequestsPerSecond, obj.cfg.RateLimit.Web.Http.PerPeer.Burst, obj.cfg.RateLimit.Web.Http.PerPeer.MaxTracked)
	if protoHTTPS {
		limiterObj = newLimiter(obj.cfg.RateLimit.Web.Https.RequestsPerSecond, obj.cfg.RateLimit.Web.Https.Burst)
		peerObj = newPeerLimiter(obj.cfg.RateLimit.Web.Https.PerPeer.RequestsPerSecond, obj.cfg.RateLimit.Web.Https.PerPeer.Burst, obj.cfg.RateLimit.Web.Https.PerPeer.MaxTracked)
	}
	return listenerCtxObj{
		listenerID:             stcode.ListenerWeb,
		entryHost:              obj.cfg.Web.Server.Domain,
		healthSource:           api.HealthObjSourceWeb,
		limiter:                limiterObj,
		peerLimiter:            peerObj,
		publicMetricsEnabled:   obj.cfg.Metrics.Web.Public,
		internalMetricsEnabled: obj.cfg.Metrics.Web.Internal,
		secure:                 obj.cfg.Web.Server.Mode == stconf.WebServerModeShared || protoHTTPS,
	}
}

func (obj *Obj) yggListenerCtx() listenerCtxObj {
	return listenerCtxObj{
		listenerID:             stcode.ListenerYgg,
		entryHost:              obj.meshObj.Host(),
		healthSource:           api.HealthObjSourceYggdrasil,
		limiter:                newLimiter(obj.cfg.RateLimit.Ygg.RequestsPerSecond, obj.cfg.RateLimit.Ygg.Burst),
		peerLimiter:            newPeerLimiter(obj.cfg.RateLimit.Ygg.PerPeer.RequestsPerSecond, obj.cfg.RateLimit.Ygg.PerPeer.Burst, obj.cfg.RateLimit.Ygg.PerPeer.MaxTracked),
		publicMetricsEnabled:   obj.cfg.Metrics.Ygg.Public,
		internalMetricsEnabled: obj.cfg.Metrics.Ygg.Internal,
		secure:                 false,
	}
}

func (obj *Obj) newHTTPServer(lc listenerCtxObj) *http.Server {
	return &http.Server{
		Handler:           obj.Handler(lc),
		MaxHeaderBytes:    int(obj.cfg.Web.Ingress.ReadBufferSize),
		ReadHeaderTimeout: obj.cfg.Web.Ingress.ReadHeaderTimeout,
		IdleTimeout:       obj.cfg.Web.Ingress.IdleTimeout,
	}
}

func (obj *Obj) addListener(label string, serverObj *http.Server, netListener net.Listener, httpsListener bool) {
	if httpsListener && obj.tlsConfig != nil {
		netListener = tls.NewListener(netListener, obj.tlsConfig)
	}
	obj.listenerArr = append(obj.listenerArr, &listenerObj{label: label, httpServer: serverObj, netListener: netListener})
}

func (obj *Obj) closeListeners() {
	for _, entryObj := range obj.listenerArr {
		if entryObj.netListener != nil {
			_ = entryObj.netListener.Close()
		}
	}
	obj.listenerArr = nil
}

func (obj *Obj) buildListeners() (err error) {
	defer func() {
		if err != nil {
			obj.closeListeners()
		}
	}()
	switch obj.cfg.Web.Server.Mode {
	case stconf.WebServerModeShared:
		netListener, listenErr := net.Listen("tcp", obj.cfg.Web.Server.Shared.Listen)
		if listenErr != nil {
			return listenErr
		}
		obj.addListener("web-shared", obj.newHTTPServer(obj.webListenerCtx(false)), netListener, false)
	case stconf.WebServerModeSplit:
		httpListener, listenErr := net.Listen("tcp", obj.cfg.Web.Server.Split.Http.Listen)
		if listenErr != nil {
			return listenErr
		}
		obj.addListener("web-http", obj.newHTTPServer(obj.webListenerCtx(false)), httpListener, false)
		httpsListener, listenErr := net.Listen("tcp", obj.cfg.Web.Server.Split.Https.Listen)
		if listenErr != nil {
			return listenErr
		}
		obj.addListener("web-https", obj.newHTTPServer(obj.webListenerCtx(true)), httpsListener, true)
	case stconf.WebServerModeSingle:
		httpsFlag := obj.cfg.Web.Server.Single.Proto == stconf.WebProtoHttps
		netListener, listenErr := net.Listen("tcp", obj.cfg.Web.Server.Single.Listen)
		if listenErr != nil {
			return listenErr
		}
		obj.addListener("web-single", obj.newHTTPServer(obj.webListenerCtx(httpsFlag)), netListener, httpsFlag)
	}

	if obj.meshObj != nil && obj.meshObj.Enabled() {
		yggListener, listenErr := obj.meshObj.ListenerFor(mesh.TransportYgg)
		if listenErr != nil {
			return listenErr
		}
		obj.addListener("ygg", obj.newHTTPServer(obj.yggListenerCtx()), yggListener, false)
	}
	return nil
}

// // // // // // // // // //

// Start binds sockets and serves each listener in its own goroutine.
func (obj *Obj) Start() error {
	if err := obj.buildListeners(); err != nil {
		return err
	}
	for i := range obj.listenerArr {
		entryObj := obj.listenerArr[i]
		obj.logObj.Info().
			Str("component", "server").
			Str("listener", entryObj.label).
			Str("addr", entryObj.netListener.Addr().String()).
			Msg("listener started")
		go func(entry *listenerObj) {
			if err := entry.httpServer.Serve(entry.netListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				obj.logObj.Error().Err(err).Str("listener", entry.label).Msg("listener stopped")
			}
		}(entryObj)
	}
	return nil
}

func (obj *Obj) shutdownListeners(ctx context.Context) error {
	var wgObj sync.WaitGroup
	errChan := make(chan error, len(obj.listenerArr))
	for i := range obj.listenerArr {
		wgObj.Add(1)
		go func(entryObj *listenerObj) {
			defer wgObj.Done()
			if err := entryObj.httpServer.Shutdown(ctx); err != nil {
				_ = entryObj.httpServer.Close()
				errChan <- err
			}
		}(obj.listenerArr[i])
	}
	wgObj.Wait()
	close(errChan)
	for err := range errChan {
		if err != nil {
			return err
		}
	}
	return nil
}
