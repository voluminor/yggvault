package source

import (
	"net"
	"testing"
)

// // // // // // // // // //

func TestForbiddenIP(t *testing.T) {
	for _, ipText := range []string{"127.0.0.1", "::1", "10.0.0.1", "192.168.1.1", "172.16.0.1", "169.254.169.254", "fc00::1", "fe80::1", "0.0.0.0", "::", "224.0.0.1", "ff02::1"} {
		if !forbiddenIP(net.ParseIP(ipText)) {
			t.Errorf("%s must be forbidden", ipText)
		}
	}
	for _, ipText := range []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"} {
		if forbiddenIP(net.ParseIP(ipText)) {
			t.Errorf("%s must be allowed", ipText)
		}
	}
}

func TestDenyInternalDial(t *testing.T) {
	for _, addr := range []string{"169.254.169.254:80", "10.0.0.1:80", "[fc00::1]:443"} {
		if err := dialTargetError(addr, true); err == nil {
			t.Errorf("dial to %s must be refused", addr)
		}
	}
	if err := dialTargetError("8.8.8.8:443", false); err != nil {
		t.Errorf("public dial blocked: %v", err)
	}
	if err := dialTargetError("127.0.0.1:8080", false); err == nil {
		t.Errorf("loopback without allowLoopback must be refused")
	}
	if err := dialTargetError("127.0.0.1:8080", true); err != nil {
		t.Errorf("loopback with allowLoopback must pass: %v", err)
	}
}
