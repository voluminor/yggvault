package mesh

import (
	"context"
	"fmt"
	"net"
)

// // // // // // // // // //

// DialContext resolves host (<hex>.pk.ygg or IPv6 literal) into a 200::/7 transport address and opens a connection
// through the node netstack. It can be used as http.Transport.DialContext and net/rpc dialer.
func (obj *Obj) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if !obj.enabled || obj.node == nil {
		return nil, ErrDisabled
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split ygg address %q: %w", address, err)
	}

	ctx, ip, err := obj.resolver.Resolve(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve ygg host %q: %w", host, err)
	}

	return obj.node.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
}

// //

// ListenerFor returns the ygg-entry listener on the node address, port 80.
func (obj *Obj) ListenerFor(transport TransportType) (net.Listener, error) {
	if !obj.enabled || obj.node == nil {
		return nil, ErrDisabled
	}

	switch transport {
	case TransportYgg:
		return obj.node.Listen("tcp", net.JoinHostPort(obj.addr.String(), cYggPort))
	default:
		return nil, fmt.Errorf("unknown mesh transport: %d", transport)
	}
}
