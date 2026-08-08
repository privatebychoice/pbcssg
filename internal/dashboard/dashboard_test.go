package dashboard

import (
	"testing"

	"go.privatebychoice.com/pbcssg/internal/metrics"
)

func seededReg() *metrics.Registry {
	r := metrics.New()
	r.Record(metrics.Sample{Path: "/", Known: true, Status: 200, Bytes: 1200, Method: "GET", UAClass: "firefox", Net: metrics.NetV4, Cell: uint16(203)<<8 | 0})
	r.Record(metrics.Sample{Path: "/about", Known: true, Status: 304, Method: "GET", UAClass: "chrome", Net: metrics.NetV4, Cell: uint16(8)<<8 | 8})
	r.Record(metrics.Sample{Path: "/x", Known: false, Status: 404, Method: "GET", UAClass: "bot", Net: metrics.NetV6})
	return r
}

func TestBuildView(t *testing.T) {
	v := BuildView(seededReg().Snapshot(TopN))

	if v.Hits != 3 {
		t.Errorf("Hits = %d, want 3", v.Hits)
	}
	if v.IPv6 != 1 {
		t.Errorf("IPv6 = %d, want 1", v.IPv6)
	}
	// 2 of 3 requests are on-grid IPv4 (the IPv6 one is not).
	if v.OnGrid != 2 {
		t.Errorf("OnGrid = %d, want 2", v.OnGrid)
	}
	// Percentages are pre-formatted strings (no float in the template).
	if v.IPv6Pct != "33.3" {
		t.Errorf("IPv6Pct = %q, want 33.3", v.IPv6Pct)
	}
	// Status rows are present, sorted, and carry a percent string.
	if len(v.Status) == 0 || v.Status[0].Pct == "" {
		t.Errorf("status rows not built: %+v", v.Status)
	}
}

func TestBuildViewEmpty(t *testing.T) {
	v := BuildView(metrics.New().Snapshot(TopN))
	if v.Hits != 0 || v.CacheRatio != "0" {
		t.Errorf("empty view = %+v, want zero hits and 0%% cache", v)
	}
	if len(v.TopPaths) != 0 {
		t.Errorf("empty view should have no top paths, got %v", v.TopPaths)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPctStr(t *testing.T) {
	cases := []struct {
		n, total uint64
		want     string
	}{
		{0, 0, "0"},
		{1, 0, "0"},
		{1, 2, "50"},
		{1, 3, "33.3"},
		{2, 3, "66.7"},
		{3, 3, "100"},
	}
	for _, c := range cases {
		if got := pctStr(c.n, c.total); got != c.want {
			t.Errorf("pctStr(%d,%d) = %q, want %q", c.n, c.total, got, c.want)
		}
	}
}
