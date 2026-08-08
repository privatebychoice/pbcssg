package manifest

import "sort"

// PageSummary is one page's roll-up in the site manifest.
type PageSummary struct {
	Page       string `json:"page"`
	WorstGrade string `json:"worstGrade,omitempty"`
	External   int    `json:"external"`
	Domains    int    `json:"domains"`
}

// DomainStat is one external domain aggregated across the whole site: its
// classification plus which pages reference it and how many total references.
type DomainStat struct {
	Domain    string   `json:"domain"`
	Matched   bool     `json:"matched"`
	Grade     string   `json:"grade"`
	GradeName string   `json:"gradeName"`
	Trust     string   `json:"trust"`
	Stale     bool     `json:"stale"`
	Count     int      `json:"count"` // total references across the site
	Pages     []string `json:"pages"` // pages that reference this domain
}

// Site is the site-level privacy manifest (emitted as manifest/site.json).
type Site struct {
	Summary Summary       `json:"summary"`
	Pages   []PageSummary `json:"pages"`
	Domains []DomainStat  `json:"domains"`
}

// Builder accumulates per-page manifests and produces the site manifest.
type Builder struct {
	pages   []PageSummary
	domains map[string]*domainStat
}

type domainStat struct {
	entry DomainEntry // classification carried from the first page that saw it
	pages map[string]bool
	count int
}

// NewBuilder returns an empty Builder.
func NewBuilder() *Builder {
	return &Builder{domains: map[string]*domainStat{}}
}

// AddPage records a page manifest into the site aggregate.
func (b *Builder) AddPage(p Page) {
	b.pages = append(b.pages, PageSummary{
		Page:       p.Page,
		WorstGrade: p.Summary.WorstGrade,
		External:   p.Summary.External,
		Domains:    p.Summary.Domains,
	})
	for _, d := range p.Domains {
		ds := b.domains[d.Domain]
		if ds == nil {
			ds = &domainStat{entry: d, pages: map[string]bool{}}
			b.domains[d.Domain] = ds
		}
		ds.pages[p.Page] = true
		ds.count += len(d.Refs)
	}
}

// Site produces the aggregated site manifest. Pages, domains, and each domain's
// page list are sorted for deterministic output.
func (b *Builder) Site() Site {
	pages := append([]PageSummary(nil), b.pages...)
	sort.Slice(pages, func(i, j int) bool { return pages[i].Page < pages[j].Page })

	domains := make([]DomainStat, 0, len(b.domains))
	byGrade := map[string]int{}
	worst := ""
	for dom, ds := range b.domains {
		refPages := make([]string, 0, len(ds.pages))
		for pg := range ds.pages {
			refPages = append(refPages, pg)
		}
		sort.Strings(refPages)
		byGrade[ds.entry.Grade]++
		worst = worseLetter(worst, ds.entry.Grade)
		domains = append(domains, DomainStat{
			Domain:    dom,
			Matched:   ds.entry.Matched,
			Grade:     ds.entry.Grade,
			GradeName: ds.entry.GradeName,
			Trust:     ds.entry.Trust,
			Stale:     ds.entry.Stale,
			Count:     ds.count,
			Pages:     refPages,
		})
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i].Domain < domains[j].Domain })

	sum := Summary{External: totalRefs(domains), Domains: len(domains), WorstGrade: worst}
	if len(byGrade) > 0 {
		sum.ByGrade = byGrade
	}
	return Site{Summary: sum, Pages: pages, Domains: domains}
}

func totalRefs(domains []DomainStat) int {
	n := 0
	for _, d := range domains {
		n += d.Count
	}
	return n
}
