package brother

import (
	"io"
	"net"
	"net/http"
	"time"
)

// // // // // // // // // //

type idleConnObj struct {
	net.Conn
	idle time.Duration
}

// Read sets an idle read deadline before every read.
func (c *idleConnObj) Read(p []byte) (int, error) {
	if c.idle > 0 {
		_ = c.Conn.SetReadDeadline(time.Now().Add(c.idle))
	}
	return c.Conn.Read(p)
}

// Write sets an idle write deadline before every write.
func (c *idleConnObj) Write(p []byte) (int, error) {
	if c.idle > 0 {
		_ = c.Conn.SetWriteDeadline(time.Now().Add(c.idle))
	}
	return c.Conn.Write(p)
}

func (obj *ServerObj) registerConn(conn net.Conn) (release func(), accepted bool) {
	obj.connMu.Lock()
	defer obj.connMu.Unlock()
	if obj.closing {
		return nil, false
	}
	if obj.connSet == nil {
		obj.connSet = make(map[net.Conn]struct{})
	}
	obj.connSet[conn] = struct{}{}
	return func() {
		obj.connMu.Lock()
		delete(obj.connSet, conn)
		obj.connMu.Unlock()
	}, true
}

// // // // // // // // // //

// Handler returns the `/rpc` handler: CONNECT only, limits, then hijack and gob ServeCodec.
// The front handler mounts it on each listener where brother RPC is enabled.
func (obj *ServerObj) Handler() http.Handler {
	return http.HandlerFunc(obj.serveRPC)
}

func (obj *ServerObj) serveRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "rpc requires CONNECT", http.StatusMethodNotAllowed)
		return
	}
	if obj.limiterObj != nil && !obj.limiterObj.Allow() {
		http.Error(w, "rpc rate limit exceeded", http.StatusServiceUnavailable)
		return
	}
	// Enforce the per-peer cap before the global slot so one peer cannot occupy sessionSem.
	peerHost := peerHostFromAddr(r.RemoteAddr)
	if !obj.acquirePeerSlot(peerHost) {
		http.Error(w, "rpc per-peer concurrency limit exceeded", http.StatusServiceUnavailable)
		return
	}
	defer obj.releasePeerSlot(peerHost)
	if obj.sessionSem != nil {
		select {
		case obj.sessionSem <- struct{}{}:
			defer func() { <-obj.sessionSem }()
		default:
			http.Error(w, "rpc concurrency limit exceeded", http.StatusServiceUnavailable)
			return
		}
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "rpc requires a hijackable connection", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	release, accepted := obj.registerConn(conn)
	if !accepted {
		_ = conn.Close()
		return
	}
	defer release()
	defer func() { _ = conn.Close() }()

	if _, err := io.WriteString(conn, "HTTP/1.0 200 Connected to Go RPC\n\n"); err != nil {
		return
	}

	idleConn := &idleConnObj{Conn: conn, idle: obj.idleTimeout}
	codecObj := newBoundedCodec(idleConn, cRequestHeaderCap, cRequestBodyCap)
	for {
		if err := obj.rpcServerObj.ServeRequest(codecObj); err != nil {
			break
		}
	}
	_ = codecObj.Close()
}
