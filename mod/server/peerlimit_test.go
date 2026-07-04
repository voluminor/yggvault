package server

import "testing"

// // // // // // // // // //

func TestPeerLimiter(t *testing.T) {
	if newPeerLimiter(0, 0, 100) != nil {
		t.Fatal("rps=0 must disable (nil)")
	}
	var disabled *peerLimiterObj
	if !disabled.allow("x") {
		t.Fatal("nil limiter must allow")
	}

	lim := newPeerLimiter(1, 1, 64)
	if !lim.allow("a") {
		t.Fatal("first request for peer a must pass")
	}
	if lim.allow("a") {
		t.Fatal("second request for peer a must be limited")
	}
	if !lim.allow("b") {
		t.Fatal("peer b must be independent of peer a")
	}
}
