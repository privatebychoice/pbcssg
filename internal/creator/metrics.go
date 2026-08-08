package creator

import (
	"encoding/json"
	"net/http"
	"time"

	"go.privatebychoice.com/pbcssg/internal/dashboard"
)

// metricsPNGTTL reuses a rendered heat map briefly so rapid polling (e.g. a wget
// loop over the tunnel) doesn't re-encode the PNG on every request.
const metricsPNGTTL = 3 * time.Second

// handleMetrics renders the private metrics dashboard (§7.7) as an admin page inside
// the editor chrome. Available only in a unified launch with metrics enabled (a
// registry is wired in); otherwise 404, like any unknown admin path.
func (c *Creator) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if c.cfg.Metrics == nil {
		http.NotFound(w, r)
		return
	}
	view := dashboard.BuildView(c.cfg.Metrics.Snapshot(dashboard.TopN))
	c.render(w, "metrics", map[string]any{"CSRF": c.csrf, "M": view})
}

// handleMetricsJSON serves the raw aggregate snapshot as JSON (counters only, no
// client IP), for scripted checks over the tunnel.
func (c *Creator) handleMetricsJSON(w http.ResponseWriter, r *http.Request) {
	if c.cfg.Metrics == nil {
		http.NotFound(w, r)
		return
	}
	body, err := json.MarshalIndent(c.cfg.Metrics.Snapshot(dashboard.TopN), "", "  ")
	if err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(append(body, '\n'))
}

// handleMetricsHeatmap serves the /16 network heat-map PNG (§7.7), briefly cached.
func (c *Creator) handleMetricsHeatmap(w http.ResponseWriter, r *http.Request) {
	if c.cfg.Metrics == nil {
		http.NotFound(w, r)
		return
	}
	png, err := c.heatmapPNG()
	if err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		w.Write(png)
	}
}

// heatmapPNG renders the current heat map, reusing a recent render within metricsPNGTTL.
func (c *Creator) heatmapPNG() ([]byte, error) {
	c.metricsMu.Lock()
	if c.metricsPNG != nil && time.Since(c.metricsAt) < metricsPNGTTL {
		b := c.metricsPNG
		c.metricsMu.Unlock()
		return b, nil
	}
	c.metricsMu.Unlock()

	// Render outside the lock (GridSnapshot + PNG encode are the slow parts).
	b, err := dashboard.RenderHeatmap(c.cfg.Metrics.GridSnapshot())
	if err != nil {
		return nil, err
	}
	c.metricsMu.Lock()
	c.metricsPNG, c.metricsAt = b, time.Now()
	c.metricsMu.Unlock()
	return b, nil
}
