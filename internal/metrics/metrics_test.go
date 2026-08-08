package metrics

import (
	"sync"
	"testing"
	"time"
)

// clock is a mutable test clock; reassign cur to advance time.
func newTestReg() (*Registry, *time.Time) {
	cur := time.Unix(1_700_000_000, 0) // arbitrary fixed instant
	t := &cur
	return newRegistry(func() time.Time { return *t }), t
}

func cell(a, b byte) uint16 { return uint16(a)<<8 | uint16(b) }

func TestRecordScalarDimensions(t *testing.T) {
	r, _ := newTestReg()
	r.Record(Sample{Path: "/", Known: true, Status: 200, Bytes: 100, Method: "GET", UAClass: "firefox", Net: NetV4, Cell: cell(203, 0)})
	r.Record(Sample{Path: "/about", Known: true, Status: 200, Bytes: 50, Method: "HEAD", UAClass: "bot", Net: NetV4, Cell: cell(203, 0)})
	r.Record(Sample{Path: "/x", Known: false, Status: 404, Method: "GET", UAClass: "chrome", Net: NetV6})
	r.Record(Sample{Path: "/y", Known: true, Status: 304, Bytes: 0, Method: "GET", UAClass: "firefox", Net: NetOther})

	s := r.Snapshot(10)
	if s.Hits != 4 {
		t.Errorf("hits = %d, want 4", s.Hits)
	}
	if s.Bytes != 150 {
		t.Errorf("bytes = %d, want 150", s.Bytes)
	}
	if s.CacheHits != 1 {
		t.Errorf("cacheHits = %d, want 1", s.CacheHits)
	}
	if s.Status["2xx"] != 2 || s.Status["4xx"] != 1 || s.Status["3xx"] != 1 {
		t.Errorf("status = %v, want 2xx:2 4xx:1 3xx:1", s.Status)
	}
	if s.Method["GET"] != 3 || s.Method["HEAD"] != 1 {
		t.Errorf("method = %v, want GET:3 HEAD:1", s.Method)
	}
	if s.UAClass["firefox"] != 2 || s.UAClass["bot"] != 1 || s.UAClass["chrome"] != 1 {
		t.Errorf("uaClass = %v", s.UAClass)
	}
	if s.IPv6 != 1 {
		t.Errorf("ipv6 = %d, want 1", s.IPv6)
	}
	if s.PrivateUnknown != 1 {
		t.Errorf("privateUnknown = %d, want 1", s.PrivateUnknown)
	}
}

func TestUnknownPathBucket(t *testing.T) {
	r, _ := newTestReg()
	for i := 0; i < 5; i++ {
		r.Record(Sample{Path: "/random-scan-path", Known: false, Status: 404, Method: "GET"})
	}
	r.Record(Sample{Path: "/", Known: true, Status: 200, Method: "GET"})

	s := r.Snapshot(10)
	var nf, root uint64
	for _, pc := range s.TopPaths {
		switch pc.Path {
		case "(not found)":
			nf = pc.Count
		case "/":
			root = pc.Count
		}
	}
	if nf != 5 {
		t.Errorf("(not found) = %d, want 5", nf)
	}
	if root != 1 {
		t.Errorf("/ = %d, want 1", root)
	}
}

func TestPathCardinalityBounded(t *testing.T) {
	r, _ := newTestReg()
	// Far more distinct known paths than the cap; the map must not grow unbounded.
	for i := 0; i < maxPaths+500; i++ {
		p := "/p/" + itoa(i)
		r.Record(Sample{Path: p, Known: true, Status: 200, Method: "GET"})
	}
	r.mu.Lock()
	n := len(r.paths)
	r.mu.Unlock()
	if n > maxPaths+1 { // +1 allows the "(other)" overflow bucket
		t.Errorf("paths map grew to %d, want <= %d", n, maxPaths+1)
	}
	if _, ok := r.paths["(other)"]; !ok {
		t.Errorf("expected an (other) overflow bucket")
	}
}

func TestGridCountingAndTopNets(t *testing.T) {
	r, _ := newTestReg()
	for i := 0; i < 3; i++ {
		r.Record(Sample{Path: "/", Known: true, Status: 200, Method: "GET", Net: NetV4, Cell: cell(198, 51)})
	}
	r.Record(Sample{Path: "/", Known: true, Status: 200, Method: "GET", Net: NetV4, Cell: cell(203, 0)})

	grid := r.GridSnapshot()
	if grid[cell(198, 51)] != 3 {
		t.Errorf("grid[198.51] = %d, want 3", grid[cell(198, 51)])
	}
	if grid[cell(203, 0)] != 1 {
		t.Errorf("grid[203.0] = %d, want 1", grid[cell(203, 0)])
	}

	s := r.Snapshot(10)
	if s.GridActive != 2 {
		t.Errorf("gridActive = %d, want 2", s.GridActive)
	}
	if len(s.TopNets) == 0 || s.TopNets[0].Net != "198.51.0.0/16" || s.TopNets[0].Count != 3 {
		t.Errorf("topNets[0] = %+v, want 198.51.0.0/16 count 3", s.TopNets)
	}
}

func TestHourlyExpiry(t *testing.T) {
	r, clk := newTestReg()
	r.Record(Sample{Path: "/", Known: true, Status: 200, Method: "GET", Net: NetV4, Cell: cell(203, 0)})
	if got := r.Snapshot(10).Hits; got != 1 {
		t.Fatalf("hits now = %d, want 1", got)
	}
	// Advance past the 24h window; the old bucket must fall out of the sum.
	*clk = clk.Add(25 * time.Hour)
	if got := r.Snapshot(10).Hits; got != 0 {
		t.Errorf("hits after 25h = %d, want 0", got)
	}
	if got := r.GridSnapshot()[cell(203, 0)]; got != 0 {
		t.Errorf("grid after 25h = %d, want 0", got)
	}
}

func TestWindowRolls(t *testing.T) {
	r, clk := newTestReg()
	// One hit per hour for 30 consecutive hours, leaving the clock on the final
	// record's hour; only the most recent 24 fall inside the rolling window.
	for i := 0; i < 30; i++ {
		r.Record(Sample{Path: "/", Known: true, Status: 200, Method: "GET", Net: NetV4, Cell: cell(10, 10)})
		if i < 29 {
			*clk = clk.Add(time.Hour)
		}
	}
	s := r.Snapshot(10)
	if s.Hits != windowHours {
		t.Errorf("hits = %d, want %d (rolling window)", s.Hits, windowHours)
	}
}

func TestConcurrentRecord(t *testing.T) {
	r, _ := newTestReg()
	const goroutines, per = 16, 1000
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				r.Record(Sample{Path: "/", Known: true, Status: 200, Method: "GET", Net: NetV4, Cell: cell(byte(g), 0)})
			}
		}(g)
	}
	wg.Wait()

	s := r.Snapshot(0)
	if s.Hits != goroutines*per {
		t.Errorf("hits = %d, want %d", s.Hits, goroutines*per)
	}
	if s.GridActive != goroutines {
		t.Errorf("gridActive = %d, want %d", s.GridActive, goroutines)
	}
}

func TestStatusClass(t *testing.T) {
	cases := map[int]string{100: "other", 200: "2xx", 204: "2xx", 301: "3xx", 404: "4xx", 500: "5xx", 599: "5xx", 700: "other"}
	for code, want := range cases {
		if got := statusClass(code); got != want {
			t.Errorf("statusClass(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestNetLabel(t *testing.T) {
	if got := netLabel(cell(203, 0)); got != "203.0.0.0/16" {
		t.Errorf("netLabel = %q, want 203.0.0.0/16", got)
	}
	if got := netLabel(cell(255, 255)); got != "255.255.0.0/16" {
		t.Errorf("netLabel = %q, want 255.255.0.0/16", got)
	}
}

// itoa avoids strconv in the test just to keep the intent obvious; small ints only.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
