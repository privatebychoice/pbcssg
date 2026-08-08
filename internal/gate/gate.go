// Package gate implements the pbcssg pre-publish privacy gate (SPEC §5.3,
// resolved decision #6): warn + explicit acknowledge. Given a page's classified
// external references it surfaces every domain graded at or below a threshold
// (by default D "Tracking", F "Invasive", and ? "Unclassified"; C/B/A pass) so
// the author must knowingly acknowledge them before publishing.
//
// It honours the consent-gated exemption: a facade domain that a consent-gated
// fieldblock introduces on its own /external/... page (e.g. youtube-nocookie.com,
// SPEC §5.8) is pre-acknowledged — still reported, but requiring no per-publish
// acknowledgement. The exemption is supplied by the caller (the fieldblock layer)
// via Config.ExemptDomains, keeping this package decoupled from the content model.
//
// The gate is pure decision logic: no UI, no I/O. The editor renders its Report.
package gate

import (
	"sort"

	classify "go.privatebychoice.com/pbc-classification"
	"go.privatebychoice.com/pbcssg/internal/linkscan"
)

// Config tunes the gate.
type Config struct {
	// FlagAtOrBelow: domains graded at or below this are surfaced. The zero value
	// defaults to GradeD, which flags D, F, and ? (Unclassified). classify grades
	// are ordinal (A high … Unclassified low), so "at or below" is a direct
	// comparison.
	FlagAtOrBelow classify.Grade

	// ExemptDomains are consent-gated, pre-acknowledged domains for this page
	// (SPEC §5.3/§5.8). They are still reported, but need no acknowledgement.
	ExemptDomains map[string]bool

	// HardBlock switches from the v1 default (warn + acknowledge) to blocking a
	// publish when any non-exempt domain is flagged. Post-v1 / opt-in.
	HardBlock bool
}

// Flag is one flagged external domain and where it appears on the page.
type Flag struct {
	Domain          string
	Grade           string // letter A–F or "?"
	GradeName       string
	Reasons         []string
	References      []linkscan.Reference
	PreAcknowledged bool // consent-gated exemption (reported, no ack required)
}

// Report is the gate's verdict for a page.
type Report struct {
	Flags     []Flag // every flagged domain, sorted by domain
	hardBlock bool
}

// Evaluate applies the gate to a page's classified references.
func Evaluate(results []linkscan.Result, cfg Config) Report {
	threshold := cfg.FlagAtOrBelow
	if threshold == classify.GradeUnclassified {
		threshold = classify.GradeD // treat the zero value as the default
	}

	type group struct {
		c    classify.Classification
		refs []linkscan.Reference
	}
	groups := map[string]*group{}
	var order []string
	for _, r := range results {
		if r.Classification.Grade > threshold {
			continue // C/B/A (or above threshold) pass the gate
		}
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
		g.refs = append(g.refs, r.Ref)
	}

	flags := make([]Flag, 0, len(order))
	for _, dom := range order {
		g := groups[dom]
		sort.Slice(g.refs, func(i, j int) bool { return g.refs[i].Resolved < g.refs[j].Resolved })
		flags = append(flags, Flag{
			Domain:          dom,
			Grade:           g.c.Grade.Letter(),
			GradeName:       g.c.Grade.Name(),
			Reasons:         g.c.Reasons,
			References:      g.refs,
			PreAcknowledged: cfg.ExemptDomains[dom],
		})
	}
	sort.Slice(flags, func(i, j int) bool { return flags[i].Domain < flags[j].Domain })

	return Report{Flags: flags, hardBlock: cfg.HardBlock}
}

// NeedsAcknowledgement returns the flags the author must explicitly acknowledge —
// every flagged domain except the consent-gated, pre-acknowledged ones.
func (r Report) NeedsAcknowledgement() []Flag {
	var out []Flag
	for _, f := range r.Flags {
		if !f.PreAcknowledged {
			out = append(out, f)
		}
	}
	return out
}

// Blocked reports whether the publish is blocked. In the v1 default
// (warn + acknowledge) it is always false; in HardBlock mode it is true when any
// non-exempt domain is flagged.
func (r Report) Blocked() bool {
	return r.hardBlock && len(r.NeedsAcknowledgement()) > 0
}
