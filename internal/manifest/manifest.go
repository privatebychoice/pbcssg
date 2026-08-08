// Package manifest builds the pbcssg privacy manifests (SPEC §5.7): a per-page
// manifest listing every external domain a page references with its
// classification, and a site-level manifest aggregating those across the whole
// build. It satisfies the standing rule to identify every external network
// request a page makes.
//
// Per resolved decision #2, per-URL classification stays in pbc-classification;
// this package owns the per-page/site aggregation and emission. It consumes the
// linkscan.Result values produced by the extraction+classification stage.
package manifest

import (
	"bytes"
	"encoding/json"
	"sort"

	classify "go.privatebychoice.com/pbc-classification"
	"go.privatebychoice.com/pbcssg/internal/linkscan"
)

// URLRef is a single external reference on a page: the resolved URL and where it
// appeared in the rendered HTML.
type URLRef struct {
	URL     string `json:"url"`
	Kind    string `json:"kind"`
	Element string `json:"element"`
	Attr    string `json:"attr"`
}

// DomainEntry is one external registrable domain a page references, together with
// its shared privacy classification (the CLI result shape) and the specific
// references to it.
type DomainEntry struct {
	Domain    string   `json:"domain"`
	Matched   bool     `json:"matched"`
	Grade     string   `json:"grade"`     // letter A–F or "?"
	GradeName string   `json:"gradeName"` // Clean/Considerate/Mixed/Tracking/Invasive/Unclassified
	Trust     string   `json:"trust"`
	Verified  string   `json:"verified,omitempty"`
	Stale     bool     `json:"stale"`
	Reasons   []string `json:"reasons"`
	Refs      []URLRef `json:"references"`

	grade classify.Grade // retained for ranking; not serialized
}

// Summary is the roll-up for a page or the whole site.
type Summary struct {
	External   int            `json:"external"`             // total external references (URLs)
	Domains    int            `json:"domains"`              // distinct external domains
	WorstGrade string         `json:"worstGrade,omitempty"` // lowest grade present; empty when no external refs
	ByGrade    map[string]int `json:"byGrade,omitempty"`    // distinct-domain count per grade letter
}

// Page is the per-page privacy manifest (emitted as manifest/<page-path>.json).
type Page struct {
	Page    string        `json:"page"`
	Summary Summary       `json:"summary"`
	Domains []DomainEntry `json:"domains"`
}

// BuildPage aggregates a page's scan results into its manifest. References are
// grouped by the classified registrable domain (classification is per-domain);
// domains and their references are sorted for deterministic output.
func BuildPage(pagePath string, results []linkscan.Result) Page {
	type group struct {
		c    classify.Classification
		refs []URLRef
	}
	groups := map[string]*group{}
	order := []string{}
	for _, r := range results {
		dom := r.Classification.Domain
		if dom == "" {
			dom = r.Ref.Host
		}
		g := groups[dom]
		if g == nil {
			g = &group{c: r.Classification}
			groups[dom] = g
			order = append(order, dom)
		}
		g.refs = append(g.refs, URLRef{
			URL:     r.Ref.Resolved,
			Kind:    r.Ref.Kind.String(),
			Element: r.Ref.Element,
			Attr:    r.Ref.Attr,
		})
	}

	domains := make([]DomainEntry, 0, len(order))
	byGrade := map[string]int{}
	external := 0
	worst := ""
	for _, dom := range order {
		g := groups[dom]
		sort.Slice(g.refs, func(i, j int) bool { return g.refs[i].URL < g.refs[j].URL })
		letter := g.c.Grade.Letter()
		byGrade[letter]++
		external += len(g.refs)
		worst = worseLetter(worst, letter)
		domains = append(domains, DomainEntry{
			Domain:    dom,
			Matched:   g.c.Matched,
			Grade:     letter,
			GradeName: g.c.Grade.Name(),
			Trust:     g.c.Trust.String(),
			Verified:  g.c.Verified,
			Stale:     g.c.Stale,
			Reasons:   g.c.Reasons,
			Refs:      g.refs,
			grade:     g.c.Grade,
		})
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i].Domain < domains[j].Domain })

	sum := Summary{External: external, Domains: len(domains), WorstGrade: worst}
	if len(byGrade) > 0 {
		sum.ByGrade = byGrade
	}
	return Page{Page: pagePath, Summary: sum, Domains: domains}
}

// gradeRank maps a grade letter to classify's ordinal severity (Unclassified "?"
// is the lowest/worst, then F, D, C, B, A). It mirrors classify.Grade so "worst"
// means the same thing as the library's own ordering.
func gradeRank(letter string) int {
	switch letter {
	case "A":
		return int(classify.GradeA)
	case "B":
		return int(classify.GradeB)
	case "C":
		return int(classify.GradeC)
	case "D":
		return int(classify.GradeD)
	case "F":
		return int(classify.GradeF)
	default:
		return int(classify.GradeUnclassified)
	}
}

// worseLetter returns whichever of a and b is the worse grade (lower rank). An
// empty string means "no grade yet".
func worseLetter(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if gradeRank(b) < gradeRank(a) {
		return b
	}
	return a
}

// Encode renders v as pretty-printed, deterministic JSON. HTML escaping is off so
// URLs containing & or < read cleanly.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
