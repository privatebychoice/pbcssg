package creator

import (
	"sort"
	"strings"

	classify "go.privatebychoice.com/pbc-classification"
)

// linkBadge summarizes one external domain referenced by the current draft, with
// its pbc-classification grade — surfaced live in the editor so the operator sees
// the privacy picture before publishing (SPEC §5.3 informs, §5.7 records).
type linkBadge struct {
	Domain    string
	Grade     string   // letter A–F or "?"
	GradeName string   // human-readable grade name
	Class     string   // CSS-safe class token, e.g. "grade-d" / "grade-unknown"
	Count     int      // number of references to this domain on the page
	Reasons   []string // classifier reasons

	grade classify.Grade // ordinal, for sorting (worst/unknown first)
}

// linkBadges scans the draft and aggregates its third-party references by domain.
func (c *Creator) linkBadges(cj string) ([]linkBadge, error) {
	results, err := c.scan(cj)
	if err != nil {
		return nil, err
	}
	type agg struct {
		c classify.Classification
		n int
	}
	byDomain := map[string]*agg{}
	var order []string
	for _, r := range results {
		dom := r.Classification.Domain
		if dom == "" {
			dom = r.Ref.Host
		}
		a := byDomain[dom]
		if a == nil {
			a = &agg{c: r.Classification}
			byDomain[dom] = a
			order = append(order, dom)
		}
		a.n++
	}

	out := make([]linkBadge, 0, len(order))
	for _, dom := range order {
		a := byDomain[dom]
		letter := a.c.Grade.Letter()
		out = append(out, linkBadge{
			Domain:    dom,
			Grade:     letter,
			GradeName: a.c.Grade.Name(),
			Class:     gradeClass(letter),
			Count:     a.n,
			Reasons:   a.c.Reasons,
			grade:     a.c.Grade,
		})
	}
	// Worst/unknown first (classify grades are ordinal, lowest = worst), then A–Z.
	sort.Slice(out, func(i, j int) bool {
		if out[i].grade != out[j].grade {
			return out[i].grade < out[j].grade
		}
		return out[i].Domain < out[j].Domain
	})
	return out, nil
}

// gradeClass maps a grade letter to a CSS-safe class token ("?" is not valid in a
// selector, so it becomes grade-unknown).
func gradeClass(letter string) string {
	switch letter {
	case "A", "B", "C", "D", "E", "F":
		return "grade-" + strings.ToLower(letter)
	default:
		return "grade-unknown"
	}
}
