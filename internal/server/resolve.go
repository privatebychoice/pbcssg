package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/metrics"
)

// DefaultTrustedProxies is the trusted-proxy allowlist used when none is
// configured: loopback only, matching the same-host reverse-proxy topology
// (SPEC §7.5). A forwarded header is honored only when the TCP peer is in this
// set, so a direct client cannot spoof its source /16.
var DefaultTrustedProxies = []string{"127.0.0.0/8", "::1/128"}

// Resolver maps an inbound request to a /16 heat-map cell or an off-grid class
// (SPEC §7.7). It reads the real client address using the trusted-proxy rule and
// then discards it — no address is returned, logged, or retained; only the
// reduced (class, cell) leaves this type.
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver builds a Resolver from CIDR strings. An empty list falls back to
// DefaultTrustedProxies (loopback).
func NewResolver(cidrs []string) (*Resolver, error) {
	if len(cidrs) == 0 {
		cidrs = DefaultTrustedProxies
	}
	pfx := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, err
		}
		pfx = append(pfx, p.Masked())
	}
	return &Resolver{trusted: pfx}, nil
}

// Classify satisfies NetClassifier: it resolves the client address and reduces it
// to a grid class + cell, retaining nothing.
func (r *Resolver) Classify(req *http.Request) (metrics.NetClass, uint16) {
	return classifyAddr(r.clientAddr(req))
}

// clientAddr returns the real client address per the trusted-proxy rule:
//   - If the TCP peer is NOT trusted, the peer IS the client; forwarded headers
//     are ignored (a direct client could spoof them).
//   - If the peer is trusted (or the connection is a local unix socket with no
//     IP), walk X-Forwarded-For right-to-left and take the first untrusted hop;
//     fall back to X-Real-IP, then to the peer.
//
// An invalid/zero Addr is returned when nothing resolves (→ NetOther).
func (r *Resolver) clientAddr(req *http.Request) netip.Addr {
	peer, ok := peerAddr(req.RemoteAddr)
	// A unix-socket peer has no IP; such a connection is inherently local, so it is
	// treated as a trusted hop whose forwarded headers may be read.
	if ok && !r.isTrusted(peer) {
		return peer
	}

	for _, hop := range splitForwarded(req.Header.Get("X-Forwarded-For")) {
		a, err := netip.ParseAddr(hop)
		if err != nil {
			continue
		}
		if a = a.Unmap(); !r.isTrusted(a) {
			return a
		}
	}
	if a, err := netip.ParseAddr(strings.TrimSpace(req.Header.Get("X-Real-IP"))); err == nil {
		return a.Unmap()
	}
	if ok {
		return peer
	}
	return netip.Addr{}
}

func (r *Resolver) isTrusted(a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	a = a.Unmap()
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// peerAddr parses the TCP peer IP from an http.Request RemoteAddr ("ip:port", or
// a bare ip). It reports ok=false for a unix socket or any non-IP peer.
func peerAddr(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr // may be a bare IP with no port
	}
	a, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

func splitForwarded(xff string) []string {
	if xff == "" {
		return nil
	}
	parts := strings.Split(xff, ",")
	// Walk right-to-left: the rightmost entries are the ones the trusted proxy
	// appended and can be believed; client-supplied entries sit on the left.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// classifyAddr reduces a client address to a heat-map class + cell. Only globally
// routable IPv4 unicast lands on the grid; genuine IPv6 is tallied off-grid, and
// everything else (private, loopback, CGNAT, link-local, multicast, reserved,
// documentation, or invalid) is NetOther.
func classifyAddr(a netip.Addr) (metrics.NetClass, uint16) {
	a = a.Unmap()
	if !a.IsValid() {
		return metrics.NetOther, 0
	}
	if a.Is6() {
		if a.IsLoopback() || a.IsUnspecified() || a.IsLinkLocalUnicast() ||
			a.IsLinkLocalMulticast() || a.IsMulticast() || a.IsPrivate() {
			return metrics.NetOther, 0
		}
		return metrics.NetV6, 0
	}
	if !isPublicV4(a) {
		return metrics.NetOther, 0
	}
	b := a.As4()
	return metrics.NetV4, uint16(b[0])<<8 | uint16(b[1])
}

// isPublicV4 reports whether a is a globally routable IPv4 unicast address (i.e.
// belongs on the /16 grid). It excludes the standard non-routable and reserved
// blocks; documentation/benchmarking ranges are excluded precisely (they never
// appear in real traffic) even though they sit inside otherwise-public /16 cells.
func isPublicV4(a netip.Addr) bool {
	if a.IsLoopback() || a.IsUnspecified() || a.IsPrivate() ||
		a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() {
		return false
	}
	b := a.As4()
	switch {
	case b[0] == 0: // 0.0.0.0/8 "this network"
		return false
	case b[0] == 100 && b[1] >= 64 && b[1] <= 127: // 100.64.0.0/10 CGNAT
		return false
	case b[0] >= 240: // 240.0.0.0/4 reserved + 255.255.255.255 broadcast
		return false
	case b[0] == 192 && b[1] == 0 && b[2] == 2: // 192.0.2.0/24 TEST-NET-1
		return false
	case b[0] == 198 && b[1] == 51 && b[2] == 100: // 198.51.100.0/24 TEST-NET-2
		return false
	case b[0] == 203 && b[1] == 0 && b[2] == 113: // 203.0.113.0/24 TEST-NET-3
		return false
	case b[0] == 198 && (b[1] == 18 || b[1] == 19): // 198.18.0.0/15 benchmarking
		return false
	}
	return true
}
