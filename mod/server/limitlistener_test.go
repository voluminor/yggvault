package server

import (
	"net"
	"testing"
	"time"
)

// // // // // // // // // //

func TestLimitListenerZeroReturnsInner(t *testing.T) {
	baseObj, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = baseObj.Close() }()
	if got := newLimitListener(baseObj, 0); got != baseObj {
		t.Fatal("zero limit must return the inner listener unchanged")
	}
}

func TestLimitListenerBoundsConcurrentAccepts(t *testing.T) {
	baseObj, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	limitObj := newLimitListener(baseObj, 1)
	defer func() { _ = limitObj.Close() }()
	addrText := limitObj.Addr().String()

	acceptedChan := make(chan net.Conn, 2)
	go func() {
		for {
			connObj, acceptErr := limitObj.Accept()
			if acceptErr != nil {
				return
			}
			acceptedChan <- connObj
		}
	}()

	c1, err := net.Dial("tcp", addrText)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c1.Close() }()
	var firstObj net.Conn
	select {
	case firstObj = <-acceptedChan:
	case <-time.After(2 * time.Second):
		t.Fatal("first accept timed out")
	}

	// Second connection completes the handshake into the backlog, but Accept must not fire
	// while the single slot is held by the first connection.
	c2, err := net.Dial("tcp", addrText)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	select {
	case <-acceptedChan:
		t.Fatal("second accept fired while the single slot was held")
	case <-time.After(200 * time.Millisecond):
	}

	// Freeing the slot must let the queued accept proceed.
	_ = firstObj.Close()
	select {
	case secondObj := <-acceptedChan:
		_ = secondObj.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("second accept did not proceed after the slot was freed")
	}
}
