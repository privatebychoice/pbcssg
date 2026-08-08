package server

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/metrics"
)

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return a
}

func mkReq(remoteAddr string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func cellOf(a, b byte) uint16 { return uint16(a)<<8 | uint16(b) }

func TestResolverDirectClient(t *testing.T) {
	r, err := NewResolver(nil) // default loopback trust
	if err != nil {
		t.Fatal(err)
	}
	// Peer is a public IP (not trusted) → it is the client; a spoofed XFF is ignored.
	req := mkReq("8.8.8.8:4444", map[string]string{"X-Forwarded-For": "1.1.1.1"})
	class, cell := r.Classify(req)
	if class != metrics.NetV4 || cell != cellOf(8, 8) {
		t.Errorf("got class=%d cell=%d, want NetV4 8.8.0.0/16", class, cell)
	}
}

func TestResolverBehindTrustedProxy(t *testing.T) {
	r, _ := NewResolver(nil) // loopback trusted
	req := mkReq("127.0.0.1:5555", map[string]string{"X-Forwarded-For": "203.0.113.9, 198.51.100.1, 45.77.12.3"})
	// Rightmost untrusted hop is the real client: 45.77.12.3 (the doc ranges to its
	// left are what earlier proxies inserted; only the rightmost the proxy appended
	// is authoritative, but none of the loopback set is trusted here so we take the
	// rightmost non-trusted = 45.77.12.3).
	class, cell := r.Classify(req)
	if class != metrics.NetV4 || cell != cellOf(45, 77) {
		t.Errorf("got class=%d cell=%d, want NetV4 45.77.0.0/16", class, cell)
	}
}

func TestResolverMultiProxyChain(t *testing.T) {
	// Two trusted proxies: loopback (peer) and an internal 10/8 hop.
	r, err := NewResolver([]string{"127.0.0.0/8", "10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	req := mkReq("127.0.0.1:5555", map[string]string{"X-Forwarded-For": "9.9.9.9, 10.0.0.5"})
	// Right-to-left: 10.0.0.5 trusted (skip) → 9.9.9.9 untrusted = client.
	class, cell := r.Classify(req)
	if class != metrics.NetV4 || cell != cellOf(9, 9) {
		t.Errorf("got class=%d cell=%d, want NetV4 9.9.0.0/16", class, cell)
	}
}

func TestResolverSpoofedXFFFromDirectClient(t *testing.T) {
	r, _ := NewResolver(nil)
	// A non-trusted peer supplying XFF must not move the attribution.
	req := mkReq("45.77.12.3:1234", map[string]string{"X-Forwarded-For": "127.0.0.1"})
	class, cell := r.Classify(req)
	if class != metrics.NetV4 || cell != cellOf(45, 77) {
		t.Errorf("got class=%d cell=%d, want NetV4 45.77.0.0/16 (XFF ignored)", class, cell)
	}
}

func TestResolverXRealIPFallback(t *testing.T) {
	r, _ := NewResolver(nil)
	req := mkReq("127.0.0.1:5555", map[string]string{"X-Real-IP": "77.88.99.11"})
	class, cell := r.Classify(req)
	if class != metrics.NetV4 || cell != cellOf(77, 88) {
		t.Errorf("got class=%d cell=%d, want NetV4 77.88.0.0/16", class, cell)
	}
}

func TestResolverAllTrustedFallsBackToPeerOffGrid(t *testing.T) {
	r, _ := NewResolver(nil)
	// Peer trusted, XFF entirely trusted → fall back to peer (loopback → off-grid).
	req := mkReq("127.0.0.1:5555", map[string]string{"X-Forwarded-For": "127.0.0.2"})
	class, _ := r.Classify(req)
	if class != metrics.NetOther {
		t.Errorf("got class=%d, want NetOther (loopback peer)", class)
	}
}

func TestResolverIPv6Client(t *testing.T) {
	r, _ := NewResolver(nil)
	req := mkReq("127.0.0.1:5555", map[string]string{"X-Forwarded-For": "2606:4700:4700::1111"})
	class, _ := r.Classify(req)
	if class != metrics.NetV6 {
		t.Errorf("got class=%d, want NetV6", class)
	}
}

func TestResolverUnixSocketPeerReadsXFF(t *testing.T) {
	r, _ := NewResolver(nil)
	// A unix-socket peer has no IP; it is inherently local, so XFF is honored.
	req := mkReq("@", map[string]string{"X-Forwarded-For": "8.8.4.4"})
	class, cell := r.Classify(req)
	if class != metrics.NetV4 || cell != cellOf(8, 8) {
		t.Errorf("got class=%d cell=%d, want NetV4 8.8.0.0/16", class, cell)
	}
}

func TestClassifyAddrReserved(t *testing.T) {
	cases := []struct {
		ip    string
		class metrics.NetClass
		cell  uint16
	}{
		{"8.8.8.8", metrics.NetV4, cellOf(8, 8)},
		{"1.2.3.4", metrics.NetV4, cellOf(1, 2)},
		{"203.0.113.5", metrics.NetOther, 0}, // TEST-NET-3
		{"10.1.2.3", metrics.NetOther, 0},    // private
		{"192.168.1.1", metrics.NetOther, 0}, // private
		{"172.16.5.5", metrics.NetOther, 0},  // private
		{"100.64.0.1", metrics.NetOther, 0},  // CGNAT
		{"127.0.0.1", metrics.NetOther, 0},   // loopback
		{"169.254.1.1", metrics.NetOther, 0}, // link-local
		{"224.0.0.1", metrics.NetOther, 0},   // multicast
		{"0.0.0.0", metrics.NetOther, 0},     // this-network
		{"255.255.255.255", metrics.NetOther, 0},
		{"2606:4700:4700::1111", metrics.NetV6, 0}, // public v6
		{"fd00::1", metrics.NetOther, 0},           // ULA (private v6)
		{"::1", metrics.NetOther, 0},               // v6 loopback
	}
	for _, c := range cases {
		a := mustAddr(t, c.ip)
		class, cell := classifyAddr(a)
		if class != c.class || cell != c.cell {
			t.Errorf("classifyAddr(%s) = (%d,%d), want (%d,%d)", c.ip, class, cell, c.class, c.cell)
		}
	}
}
