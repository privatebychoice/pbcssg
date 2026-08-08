// Package search builds pbcssg's client-side search index (SPEC §6.2). The index
// is generated at build time from the canonical page content (not by scraping
// the assembled page HTML), and is matched entirely in the browser by a small
// self-hosted script (ClientJS) — so a search query never leaves the user's
// device.
//
// By default a document's searchable text is its title, headings, tags, author
// keywords, a summary, and any youtube/embed-block titles, transcripts, and
// keywords (decision #11); full body text is opt-in via Options.FullText.
//
// The index is a public artifact, so it must not disclose what a page hides:
// members-only (group-gated) blocks are skipped here (SPEC §6.2, §6.10), and the
// build omits noindex pages entirely. Reveal/hidden blocks are never indexed.
package search

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"go.privatebychoice.com/pbcssg/internal/render"
	"golang.org/x/net/html"
)

// IndexPath is where the search index is written in the bundle.
const IndexPath = "search/index.json"

// Document is one page's search record.
type Document struct {
	URL   string   `json:"url"`
	Title string   `json:"title"`
	Tags  []string `json:"tags,omitempty"`
	Date  string   `json:"date,omitempty"`
	Text  string   `json:"text"`
}

// Options tunes index generation.
type Options struct {
	FullText bool // include full body text, not just headings + summary (decision #11)
}

type index struct {
	Docs []Document `json:"docs"`
}

var md = goldmark.New() // safe mode, same as the renderer

// BuildDocument builds the search document for a page from its canonical content.
func BuildDocument(url, title, date, contentJSON string, opts Options) (Document, error) {
	c, err := render.Parse(contentJSON)
	if err != nil {
		return Document{}, err
	}

	parts := []string{title}
	parts = append(parts, c.Tags...)
	parts = append(parts, c.Keywords...)

	headings, paras, all := extractMarkdown(c.Body)
	parts = append(parts, headings...)

	summary := strings.TrimSpace(c.Summary)
	if summary == "" && len(paras) > 0 {
		summary = paras[0]
	}
	parts = append(parts, summary)
	if opts.FullText {
		parts = append(parts, all)
	}

	for _, b := range c.Blocks {
		// Members-only (group-gated) blocks are envelope-encrypted out of the page
		// (SPEC §6.10); their plaintext must never enter the public search index, or
		// the gate could be bypassed by reading search/index.json (SPEC §6.2).
		if len(b.Groups) > 0 {
			continue
		}
		switch b.Type {
		case "", "markdown":
			bh, bp, ball := extractMarkdown(b.Markdown)
			parts = append(parts, bh...)
			switch {
			case opts.FullText:
				parts = append(parts, ball)
			case len(bp) > 0:
				parts = append(parts, bp[0])
			}
		case "youtube":
			if b.YouTube != nil {
				parts = append(parts, b.YouTube.Title)
				parts = append(parts, b.YouTube.Keywords...)
				// Transcripts are first-party text and always indexed (§6.2).
				_, _, transcript := extractMarkdown(b.YouTube.Transcript)
				parts = append(parts, transcript)
			}
		case "embed":
			if b.Embed != nil {
				parts = append(parts, b.Embed.Title)
				parts = append(parts, b.Embed.Keywords...)
				// Notes/transcript are first-party text and always indexed (§6.2).
				_, _, notes := extractMarkdown(b.Embed.Transcript)
				parts = append(parts, notes)
			}
		}
	}

	return Document{
		URL:   url,
		Title: title,
		Tags:  c.Tags,
		Date:  date,
		Text:  normalize(parts),
	}, nil
}

// Encode renders the index as compact, deterministic JSON (documents sorted by
// URL).
func Encode(docs []Document) ([]byte, error) {
	sorted := append([]Document(nil), docs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].URL < sorted[j].URL })
	return json.Marshal(index{Docs: sorted})
}

// extractMarkdown renders markdown (safe mode) and returns its heading texts,
// paragraph texts, and all text — derived from the canonical content, not from a
// rendered page.
func extractMarkdown(markdown string) (headings, paragraphs []string, all string) {
	if strings.TrimSpace(markdown) == "" {
		return nil, nil, ""
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(markdown), &buf); err != nil {
		return nil, nil, ""
	}
	doc, err := html.Parse(&buf)
	if err != nil {
		return nil, nil, ""
	}

	var allText strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				headings = append(headings, nodeText(n))
			case "p":
				paragraphs = append(paragraphs, nodeText(n))
			}
		}
		if n.Type == html.TextNode {
			allText.WriteString(n.Data)
			allText.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return headings, paragraphs, strings.TrimSpace(allText.String())
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}

// normalize joins non-empty parts and collapses runs of whitespace.
func normalize(parts []string) string {
	var b strings.Builder
	for _, p := range parts {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(p)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
