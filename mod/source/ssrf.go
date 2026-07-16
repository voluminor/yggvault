package source

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// // // // // // // // // //

var errForbiddenTarget = errors.New("refusing dial to non-public address")

var forbiddenSpecialUseArr = mustPrefixes(
	"100.64.0.0/10",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"240.0.0.0/4",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001::/23",
	"2001:db8::/32",
	"0200::/7",
)

// // // // // // // // // //

func mustPrefixes(cidrArr ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(cidrArr))
	for i := range cidrArr {
		out[i] = netip.MustParsePrefix(cidrArr[i])
	}
	return out
}

func forbiddenIP(ipObj net.IP) bool {
	if ipObj.IsLoopback() ||
		ipObj.IsPrivate() ||
		ipObj.IsLinkLocalUnicast() ||
		ipObj.IsLinkLocalMulticast() ||
		ipObj.IsInterfaceLocalMulticast() ||
		ipObj.IsMulticast() ||
		ipObj.IsUnspecified() {
		return true
	}
	addrObj, ok := netip.AddrFromSlice(ipObj)
	if !ok {
		return true
	}
	addrObj = addrObj.Unmap()
	for i := range forbiddenSpecialUseArr {
		if forbiddenSpecialUseArr[i].Contains(addrObj) {
			return true
		}
	}
	return false
}

func dialTargetError(address string, allowLoopback bool) error {
	hostText, _, err := net.SplitHostPort(address)
	if err != nil {
		hostText = address
	}
	ipObj := net.ParseIP(hostText)
	if ipObj == nil {
		return fmt.Errorf("refusing dial to unresolved address %q", address)
	}
	if !forbiddenIP(ipObj) {
		return nil
	}
	if allowLoopback && ipObj.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%w: %s", errForbiddenTarget, ipObj)
}

func (obj *Obj) denyInternalDial(_ string, address string, _ syscall.RawConn) error {
	return dialTargetError(address, obj.allowLoopback)
}
