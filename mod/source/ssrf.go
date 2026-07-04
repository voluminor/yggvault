package source

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// // // // // // // // // //

// errForbiddenTarget rejects clearnet dials to non-public addresses for SSRF protection.
// Untrusted upstreams and redirects must not reach local, private, metadata, CGNAT, or NAT64 targets.
var errForbiddenTarget = errors.New("refusing dial to non-public address")

// forbiddenSpecialUseArr covers IANA special-use ranges not fully handled by net.IP helpers.
// NAT64 is included because well-known prefixes can map internal IPv4 targets into IPv6.
var forbiddenSpecialUseArr = mustPrefixes(
	"100.64.0.0/10",   // CGNAT (RFC 6598)
	"192.0.0.0/24",    // IETF protocol assignments (RFC 6890)
	"192.0.2.0/24",    // TEST-NET-1 (RFC 5737)
	"198.18.0.0/15",   // benchmarking (RFC 2544)
	"198.51.100.0/24", // TEST-NET-2 (RFC 5737)
	"203.0.113.0/24",  // TEST-NET-3 (RFC 5737)
	"240.0.0.0/4",     // reserved for future use (RFC 1112)
	"64:ff9b::/96",    // NAT64 well-known prefix (RFC 6052)
	"64:ff9b:1::/48",  // NAT64 local-use (RFC 8215)
	"100::/64",        // discard-only (RFC 6666)
	"2001::/23",       // IETF protocol assignments incl Teredo/ORCHIDv2 (RFC 2928)
	"2001:db8::/32",   // documentation (RFC 3849)
	"0200::/7",        // Yggdrasil mesh range; the clearnet dialer must never touch it
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
