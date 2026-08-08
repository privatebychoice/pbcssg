// Package metrics aggregates server-mode request metrics as independent,
// per-dimension counters over a rolling 24-hour window (SPEC §7.7).
//
// It is privacy-preserving by construction: it stores COUNTS, never events, and
// its API contains no client IP. The caller resolves a request's source address
// to a /16 grid cell (or an off-grid class) and passes only that, so an address
// never crosses into this package. Because every dimension is counted
// independently, no two fields are ever linked to a single request and nothing
// held here can be correlated back to an individual.
//
// Retention is lazy: each counter is a 24-slot hourly ring keyed by absolute
// unix-hour; a slot whose stored hour is stale is zeroed the next time it is
// touched, so old buckets expire with no background goroutine. Everything is
// in-memory — nothing is persisted.
package metrics

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	gridCells   = 1 << 16 // one cell per /16: index = octet1<<8 | octet2
	windowHours = 24      // rolling retention window
	maxPaths    = 4096    // hard bound on distinct tracked paths (defense in depth)
)

// NetClass says how a request's source maps to the /16 heat-map grid.
type NetClass uint8

const (
	// NetV4 means Cell holds the /16 index (octet1<<8 | octet2).
	NetV4 NetClass = iota
	// NetV6 is an IPv6 source, which does not fit the 256×256 v4 grid.
	NetV6
	// NetOther is a private / loopback / reserved / unknown source.
	NetOther
)

// Sample is one request's contribution, already reduced by the caller. It carries
// no IP: Cell is the /16 index and the address has been discarded upstream.
type Sample struct {
	Path    string   // request path (only counted when Known)
	Known   bool     // Path is a real bundle path; otherwise counted as "(not found)"
	Status  int      // HTTP status code
	Bytes   int64    // response body bytes
	Method  string   // "GET" / "HEAD" / other
	UAClass string   // coarse UA class, e.g. "firefox"/"chrome"/"bot"/"other"; "" to skip
	Net     NetClass // how the source maps to the grid
	Cell    uint16   // /16 index; meaningful only when Net == NetV4
}

// counter is a 24-slot hourly ring for one label. Each slot records the absolute
// unix-hour it represents; a slot whose hour is stale is zeroed on next touch, so
// buckets older than the window expire lazily.
type counter struct {
	hour [windowHours]int64
	val  [windowHours]uint64
}

func (c *counter) add(h int64, n uint64) {
	i := int(h % windowHours)
	if c.hour[i] != h {
		c.hour[i], c.val[i] = h, 0
	}
	c.val[i] += n
}

func (c *counter) sum(now int64) uint64 {
	var s uint64
	for h := now - windowHours + 1; h <= now; h++ {
		i := int(h % windowHours)
		if c.hour[i] == h {
			s += c.val[i]
		}
	}
	return s
}

// Registry aggregates request samples. Safe for concurrent use.
type Registry struct {
	mu  sync.Mutex
	now func() time.Time

	hits     counter
	bytes    counter
	cacheHit counter // 304 responses
	ipv6     counter // IPv6 sources (off-grid tally)
	other    counter // private/reserved/unknown sources (off-grid tally)

	status map[string]*counter
	method map[string]*counter
	ua     map[string]*counter
	paths  map[string]*counter

	// /16 heat map: 24 hourly frames, one cell per /16. Frames rotate together on
	// wall-clock hour (gridHour tracks each slot's absolute hour); a stale frame is
	// zeroed on next write. ~6.3 MB, bounded regardless of traffic.
	gridHour [windowHours]int64
	grid     [windowHours][gridCells]uint32
}

// New returns a Registry using the wall clock.
func New() *Registry { return newRegistry(time.Now) }

func newRegistry(now func() time.Time) *Registry {
	return &Registry{
		now:    now,
		status: map[string]*counter{},
		method: map[string]*counter{},
		ua:     map[string]*counter{},
		paths:  map[string]*counter{},
	}
}

func unixHour(t time.Time) int64 { return t.Unix() / 3600 }

// Record folds one request sample into the counters.
func (r *Registry) Record(s Sample) {
	h := unixHour(r.now())

	r.mu.Lock()
	defer r.mu.Unlock()

	r.hits.add(h, 1)
	if s.Bytes > 0 {
		r.bytes.add(h, uint64(s.Bytes))
	}
	if s.Status == 304 {
		r.cacheHit.add(h, 1)
	}
	addLabel(r.status, statusClass(s.Status), h, 0)
	addLabel(r.method, normMethod(s.Method), h, 0)
	if s.UAClass != "" {
		addLabel(r.ua, s.UAClass, h, 0)
	}
	path := s.Path
	if !s.Known {
		path = "(not found)"
	}
	addLabel(r.paths, path, h, maxPaths)

	switch s.Net {
	case NetV4:
		r.gridAdd(h, s.Cell)
	case NetV6:
		r.ipv6.add(h, 1)
	default:
		r.other.add(h, 1)
	}
}

// gridAdd increments the /16 cell for hour h, zeroing the slot's frame first if it
// now represents a new hour (lazy expiry).
func (r *Registry) gridAdd(h int64, cell uint16) {
	i := int(h % windowHours)
	if r.gridHour[i] != h {
		r.gridHour[i] = h
		clear(r.grid[i][:])
	}
	r.grid[i][cell]++
}

// addLabel increments the counter for key in m, creating it on first use. When cap
// > 0 and the map is already full of distinct keys, a new key is folded into an
// "(other)" bucket so cardinality stays bounded.
func addLabel(m map[string]*counter, key string, h int64, cap int) {
	c := m[key]
	if c == nil {
		if cap > 0 && len(m) >= cap {
			key = "(other)"
			c = m[key]
		}
		if c == nil {
			c = &counter{}
			m[key] = c
		}
	}
	c.add(h, 1)
}

func statusClass(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	default:
		return "other"
	}
}

func normMethod(m string) string {
	switch m {
	case "GET", "HEAD":
		return m
	default:
		return "other"
	}
}

// PathCount is one path's hit count over the window.
type PathCount struct {
	Path  string `json:"path"`
	Count uint64 `json:"count"`
}

// NetCount is one /16 network's hit count over the window.
type NetCount struct {
	Net   string `json:"net"` // e.g. "203.0.0.0/16"
	Count uint64 `json:"count"`
}

// Snapshot is a point-in-time aggregate view over the rolling window.
type Snapshot struct {
	Window         string            `json:"window"`
	Hits           uint64            `json:"hits"`
	Bytes          uint64            `json:"bytes"`
	CacheHits      uint64            `json:"cacheHits"`
	Status         map[string]uint64 `json:"status"`
	Method         map[string]uint64 `json:"method"`
	UAClass        map[string]uint64 `json:"uaClass"`
	TopPaths       []PathCount       `json:"topPaths"`
	TopNets        []NetCount        `json:"topNets"`
	IPv6           uint64            `json:"ipv6"`
	PrivateUnknown uint64            `json:"privateUnknown"`
	GridActive     int               `json:"gridActiveCells"`
}

// Snapshot computes the aggregate view, keeping the top topN paths and networks.
func (r *Registry) Snapshot(topN int) Snapshot {
	now := unixHour(r.now())

	r.mu.Lock()
	defer r.mu.Unlock()

	snap := Snapshot{
		Window:         "24h",
		Hits:           r.hits.sum(now),
		Bytes:          r.bytes.sum(now),
		CacheHits:      r.cacheHit.sum(now),
		Status:         sumMap(r.status, now),
		Method:         sumMap(r.method, now),
		UAClass:        sumMap(r.ua, now),
		TopPaths:       topPaths(r.paths, now, topN),
		IPv6:           r.ipv6.sum(now),
		PrivateUnknown: r.other.sum(now),
	}

	grid := r.gridSumLocked(now)
	snap.TopNets, snap.GridActive = topNets(grid, topN)
	return snap
}

// GridSnapshot returns the summed /16 counts over the window, length gridCells,
// indexed by (octet1<<8 | octet2). For the heat-map renderer.
func (r *Registry) GridSnapshot() []uint32 {
	now := unixHour(r.now())
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gridSumLocked(now)
}

func (r *Registry) gridSumLocked(now int64) []uint32 {
	out := make([]uint32, gridCells)
	for i := 0; i < windowHours; i++ {
		h := r.gridHour[i]
		if h == 0 || h <= now-windowHours || h > now {
			continue // empty or expired frame
		}
		frame := &r.grid[i]
		for c := 0; c < gridCells; c++ {
			out[c] += frame[c]
		}
	}
	return out
}

func sumMap(m map[string]*counter, now int64) map[string]uint64 {
	out := make(map[string]uint64, len(m))
	for k, c := range m {
		if v := c.sum(now); v > 0 {
			out[k] = v
		}
	}
	return out
}

func topPaths(m map[string]*counter, now int64, topN int) []PathCount {
	pcs := make([]PathCount, 0, len(m))
	for k, c := range m {
		if v := c.sum(now); v > 0 {
			pcs = append(pcs, PathCount{Path: k, Count: v})
		}
	}
	sort.Slice(pcs, func(i, j int) bool {
		if pcs[i].Count != pcs[j].Count {
			return pcs[i].Count > pcs[j].Count
		}
		return pcs[i].Path < pcs[j].Path // stable tie-break
	})
	if topN > 0 && len(pcs) > topN {
		pcs = pcs[:topN]
	}
	return pcs
}

// topNets returns the busiest /16 cells (up to topN) and the count of active cells.
func topNets(grid []uint32, topN int) ([]NetCount, int) {
	ncs := make([]NetCount, 0, 64)
	active := 0
	for cell, v := range grid {
		if v == 0 {
			continue
		}
		active++
		ncs = append(ncs, NetCount{Net: netLabel(uint16(cell)), Count: uint64(v)})
	}
	sort.Slice(ncs, func(i, j int) bool {
		if ncs[i].Count != ncs[j].Count {
			return ncs[i].Count > ncs[j].Count
		}
		return ncs[i].Net < ncs[j].Net
	})
	if topN > 0 && len(ncs) > topN {
		ncs = ncs[:topN]
	}
	return ncs, active
}

func netLabel(cell uint16) string {
	return fmt.Sprintf("%d.%d.0.0/16", cell>>8, cell&0xff)
}
