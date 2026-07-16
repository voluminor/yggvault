package server

import (
	"net"
	"sync"
)

// // // // // // // // // //

type limitListenerObj struct {
	net.Listener
	semChan   chan struct{}
	doneChan  chan struct{}
	closeOnce sync.Once
}

type limitConnObj struct {
	net.Conn
	releaseFunc func()
}

// //

func newLimitListener(innerObj net.Listener, maxConns uint) net.Listener {
	if maxConns == 0 {
		return innerObj
	}
	return &limitListenerObj{
		Listener: innerObj,
		semChan:  make(chan struct{}, maxConns),
		doneChan: make(chan struct{}),
	}
}

// //

// Close closes the connection and frees its slot exactly once.
func (obj *limitConnObj) Close() error {
	err := obj.Conn.Close()
	obj.releaseFunc()
	return err
}

// //

func (obj *limitListenerObj) acquire() bool {
	select {
	case <-obj.doneChan:
		return false
	case obj.semChan <- struct{}{}:
		return true
	}
}

func (obj *limitListenerObj) releaseOnce() func() {
	var onceObj sync.Once
	return func() {
		onceObj.Do(func() { <-obj.semChan })
	}
}

func (obj *limitListenerObj) Accept() (net.Conn, error) {
	if !obj.acquire() {
		return nil, net.ErrClosed
	}
	connObj, err := obj.Listener.Accept()
	if err != nil {
		<-obj.semChan
		return nil, err
	}
	return &limitConnObj{Conn: connObj, releaseFunc: obj.releaseOnce()}, nil
}

func (obj *limitListenerObj) Close() error {
	err := obj.Listener.Close()
	obj.closeOnce.Do(func() { close(obj.doneChan) })
	return err
}
