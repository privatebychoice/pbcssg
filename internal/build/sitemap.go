package build

import (
	"bytes"
	"encoding/xml"
	"sort"
	"strings"
	"time"
)

// sitemapURL is one entry in sitemap.xml: an absolute location and an optional
// last-modified date (omitted when zero).
type sitemapURL struct {
	loc     string
	lastmod time.Time
}

// addSitemap records an indexable page for sitemap.xml. It is a no-op when the
// sitemap is disabled or no base URL is configured (canonical returns ""), so a
// bundle without an absolute base simply collects no entries and writeSitemap
// surfaces one warning.
func (b *builder) addSitemap(pagePath string, lastmod time.Time) {
	if !b.cfg.Sitemap {
		return
	}
	loc := b.canonical(pagePath)
	if loc == "" {
		return
	}
	b.sitemap = append(b.sitemap, sitemapURL{loc: loc, lastmod: lastmod})
}

// addSitemapListing records a generated listing page (tags / feeds index /
// classification). It is gated by the SitemapListings toggle on top of the
// master sitemap switch, and carries no lastmod (these pages have no single
// authored timestamp).
func (b *builder) addSitemapListing(pagePath string) {
	if b.cfg.SitemapListings {
		b.addSitemap(pagePath, time.Time{})
	}
}

// writeSitemap emits sitemap.xml and a robots.txt that advertises it. It runs
// only when the sitemap is enabled; with no Base URL set it warns and skips,
// because sitemap <loc>s and the robots Sitemap directive must be absolute.
func (b *builder) writeSitemap() error {
	if !b.cfg.Sitemap {
		return nil
	}
	base := strings.TrimRight(b.cfg.BaseURL, "/")
	if base == "" {
		b.report.Warnings = append(b.report.Warnings,
			"Sitemap is enabled but no Base URL is set — sitemap.xml and robots.txt were skipped (their links must be absolute).")
		return nil
	}
	if err := b.write("sitemap.xml", sitemapXML(b.sitemap)); err != nil {
		return err
	}
	return b.write("robots.txt", robotsTxt(base))
}

// sitemapXML renders the urlset. Entries are sorted by loc so the output is
// deterministic regardless of the order pages were built in.
func sitemapXML(urls []sitemapURL) []byte {
	sorted := make([]sitemapURL, len(urls))
	copy(sorted, urls)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].loc < sorted[j].loc })

	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, u := range sorted {
		b.WriteString("  <url>\n    <loc>")
		_ = xml.EscapeText(&b, []byte(u.loc))
		b.WriteString("</loc>\n")
		if !u.lastmod.IsZero() {
			b.WriteString("    <lastmod>" + u.lastmod.Format("2006-01-02") + "</lastmod>\n")
		}
		b.WriteString("  </url>\n")
	}
	b.WriteString("</urlset>\n")
	return b.Bytes()
}

// robotsTxt allows all crawling and points at the sitemap. The built bundle has
// no private routes (the editor is a separate process), so there is nothing to
// disallow — robots.txt exists mainly to auto-advertise sitemap.xml.
func robotsTxt(base string) []byte {
	return []byte("User-agent: *\nAllow: /\nSitemap: " + base + "/sitemap.xml\n")
}
