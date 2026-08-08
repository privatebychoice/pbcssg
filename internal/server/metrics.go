package server

import (
	"net/http"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/metrics"
)

// NetClassifier maps a request's source address to a /16 grid cell or an off-grid
// class (SPEC §7.7). It runs inside the middleware and MUST NOT log or retain the
// address — it returns only the reduced (class, cell) so no IP crosses into the
// metrics layer. A nil classifier is treated as metrics.NetOther for every
// request (used before the trusted-proxy resolver is wired in).
type NetClassifier func(*http.Request) (metrics.NetClass, uint16)

// responseRecorder wraps http.ResponseWriter to capture the status code and the
// number of body bytes written, so the middleware can record them without
// changing the response.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	if rr.status == 0 {
		rr.status = http.StatusOK // Write without an explicit WriteHeader implies 200
	}
	n, err := rr.ResponseWriter.Write(b)
	rr.bytes += int64(n)
	return n, err
}

// Instrument returns a handler that serves via s and records privacy-preserving
// aggregate metrics into reg (SPEC §7.7). It adds no request logging and passes
// no client address into the metrics layer: classify reduces the source to a /16
// cell or off-grid class, and the raw User-Agent is reduced to a coarse class and
// discarded here. A path served with a client-error/redirect-miss status is folded
// into the shared "(not found)" bucket so scanner noise can't explode cardinality.
func (s *Server) Instrument(reg *metrics.Registry, classify NetClassifier) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rr := &responseRecorder{ResponseWriter: w}
		s.ServeHTTP(rr, r)

		net, cell := metrics.NetOther, uint16(0)
		if classify != nil {
			net, cell = classify(r)
		}
		reg.Record(metrics.Sample{
			Path:    r.URL.Path,
			Known:   rr.status >= 200 && rr.status < 400,
			Status:  rr.status,
			Bytes:   rr.bytes,
			Method:  r.Method,
			UAClass: classifyUA(r.Header.Get("User-Agent")),
			Net:     net,
			Cell:    cell,
		})
	})
}

// classifyUA reduces a raw User-Agent to a coarse family/bot class. The raw string
// (a fingerprinting vector) is never stored — only this label. Bots are matched
// first because crawler UAs often embed browser tokens; Edge before Chrome and
// Chrome before Safari for the same reason (each embeds the next).
func classifyUA(ua string) string {
	if ua == "" {
		return "none"
	}
	l := strings.ToLower(ua)
	switch {
	case containsAny(l, "bot", "crawl", "spider", "slurp", "curl", "wget", "python-", "go-http", "httpclient", "libwww", "scan"):
		return "bot"
	case containsAny(l, "firefox", "fxios"):
		return "firefox"
	case strings.Contains(l, "edg"):
		return "edge"
	case containsAny(l, "chrome", "chromium", "crios"):
		return "chrome"
	case strings.Contains(l, "safari"):
		return "safari"
	default:
		return "other"
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
