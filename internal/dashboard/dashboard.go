// Package dashboard renders the data and image for the private, loopback-only
// metrics view (SPEC §7.7): a display View over the aggregate counters plus the
// /16 network heat-map PNG (heatmap.go). It is a pure library — pure stdlib, no
// HTTP, no state. The editor (internal/creator) serves it as an admin page inside
// the admin chrome; the registry is populated by the server's request middleware.
package dashboard

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/metrics"
)

// TopN bounds how many paths/networks a View surfaces.
const TopN = 100

// View is the display model the admin metrics page renders: aggregate counters with
// percentages pre-formatted as strings, plus the top paths/networks. It holds no raw
// identifiers — no client IP, no full user-agent (SPEC §7.7).
type View struct {
	Window     string
	Hits       uint64
	BytesHuman string
	CacheRatio string // percent, one decimal, ".0" trimmed
	OnGrid     uint64
	IPv6       uint64
	IPv6Pct    string
	Private    uint64
	PrivatePct string
	GridActive int
	Status     []Row
	Method     []Row
	UAClass    []Row
	TopPaths   []metrics.PathCount
	TopNets    []metrics.NetCount
}

// Row is one labelled count with its share of the total, for the class tables.
type Row struct {
	Label string
	Count uint64
	Pct   string
}

// BuildView projects a metrics snapshot into the display View.
func BuildView(s metrics.Snapshot) View {
	return View{
		Window:     s.Window,
		Hits:       s.Hits,
		BytesHuman: humanBytes(s.Bytes),
		CacheRatio: pctStr(s.CacheHits, s.Hits),
		OnGrid:     s.Hits - s.IPv6 - s.PrivateUnknown,
		IPv6:       s.IPv6,
		IPv6Pct:    pctStr(s.IPv6, s.Hits),
		Private:    s.PrivateUnknown,
		PrivatePct: pctStr(s.PrivateUnknown, s.Hits),
		GridActive: s.GridActive,
		Status:     toRows(s.Status, s.Hits),
		Method:     toRows(s.Method, s.Hits),
		UAClass:    toRows(s.UAClass, s.Hits),
		TopPaths:   s.TopPaths,
		TopNets:    s.TopNets,
	}
}

func toRows(m map[string]uint64, total uint64) []Row {
	rows := make([]Row, 0, len(m))
	for k, v := range m {
		rows = append(rows, Row{Label: k, Count: v, Pct: pctStr(v, total)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].Label < rows[j].Label
	})
	return rows
}

// pctStr formats n/total as a percentage with one decimal, dropping a trailing ".0".
func pctStr(n, total uint64) string {
	if total == 0 {
		return "0"
	}
	s := strconv.FormatFloat(float64(n)/float64(total)*100, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
