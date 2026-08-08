package linkscan

import (
	"io"
	"net/url"
	"strings"

	classify "go.privatebychoice.com/pbc-classification"
)

// Result pairs an external Reference with its privacy classification. Its fields
// map onto the external_links cache row (SPEC §4.1) that later milestones persist
// to SQLite, and feed the per-page privacy manifest (SPEC §5.7).
type Result struct {
	Ref            Reference
	Classification classify.Classification
}

// Scanner extracts and classifies the third-party references on a page. It wraps
// a *classify.Classifier (which knows the operator's first-party domains, set via
// classify.WithFirstParty) and the page's base URL (used to drop same-origin,
// self-hosted references). It is safe for concurrent use.
type Scanner struct {
	classifier *classify.Classifier
	base       *url.URL
}

// NewScanner builds a Scanner. base is the page's (or site's) absolute URL and
// may be nil; when nil, only absolute references carry a host and nothing is
// treated as same-origin.
func NewScanner(c *classify.Classifier, base *url.URL) *Scanner {
	return &Scanner{classifier: c, base: base}
}

// Scan extracts the references from a page's rendered HTML and returns the
// classified third-party ones, in document order. It drops:
//
//   - references with no network host (relative, fragment, data:, mailto:,
//     javascript:, tel:, ...), which cause no off-origin request; and
//   - same-origin references (same host as the base URL), which are first-party
//     self-hosted assets.
//
// The operator's *other* own domains remain in the results but classify as
// first-party (grade A, trust own) when registered via classify.WithFirstParty —
// the honest "this is mine, but it is a distinct origin" case.
func (s *Scanner) Scan(r io.Reader) ([]Result, error) {
	refs, err := Extract(r, s.base)
	if err != nil {
		return nil, err
	}
	var out []Result
	for _, ref := range refs {
		if ref.Host == "" || s.sameOrigin(ref.Host) {
			continue
		}
		out = append(out, Result{
			Ref:            ref,
			Classification: s.classifier.Classify(ref.Resolved),
		})
	}
	return out, nil
}

// sameOrigin reports whether host is the base URL's own host (first-party,
// self-hosted). Subdomains and other operator-owned hosts are not matched here;
// register those via classify.WithFirstParty so they classify as first-party.
func (s *Scanner) sameOrigin(host string) bool {
	return s.base != nil && strings.EqualFold(host, s.base.Hostname())
}
