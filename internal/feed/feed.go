// Package feed renders syndication feeds (RSS 2.0 and Atom 1.0) from a page list.
// It is deterministic and offline: dates are formatted in UTC and the channel's
// "updated" time is the newest item's date (never the wall clock), so a rebuild
// of unchanged content produces byte-identical feeds. Feeds are self-hosted
// static XML with absolute links — no third-party requests, no tracking.
package feed

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
	"time"
)

// Item is one entry in a feed.
type Item struct {
	Title       string
	Link        string // absolute URL (permalink)
	Description string // plain-text summary
	Published   time.Time
}

// Channel is a feed's metadata plus its items.
type Channel struct {
	Title       string
	Link        string // absolute site/section URL
	SelfLink    string // absolute URL of the feed file itself (Atom rel=self)
	Description string
	Items       []Item
}

// sorted returns the items newest-first and the newest item time (or zero).
func (c Channel) sorted() ([]Item, time.Time) {
	items := append([]Item(nil), c.Items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Published.After(items[j].Published) })
	var updated time.Time
	if len(items) > 0 {
		updated = items[0].Published
	}
	return items, updated
}

func rfc1123Z(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC1123Z)
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// --- RSS 2.0 ---

type rssRoot struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}
type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	LastBuildDate string    `xml:"lastBuildDate,omitempty"`
	Items         []rssItem `xml:"item"`
}
type rssItem struct {
	Title       string  `xml:"title"`
	Link        string  `xml:"link"`
	GUID        rssGUID `xml:"guid"`
	PubDate     string  `xml:"pubDate,omitempty"`
	Description string  `xml:"description"`
}
type rssGUID struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

// RSS renders the channel as an RSS 2.0 document.
func RSS(ch Channel) ([]byte, error) {
	items, updated := ch.sorted()
	root := rssRoot{Version: "2.0", Channel: rssChannel{
		Title: ch.Title, Link: ch.Link, Description: ch.Description, LastBuildDate: rfc1123Z(updated),
	}}
	for _, it := range items {
		root.Channel.Items = append(root.Channel.Items, rssItem{
			Title: it.Title, Link: it.Link,
			GUID:        rssGUID{IsPermaLink: true, Value: it.Link},
			PubDate:     rfc1123Z(it.Published),
			Description: it.Description,
		})
	}
	return marshal(root)
}

// --- Atom 1.0 ---

type atomRoot struct {
	XMLName xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Title   string      `xml:"title"`
	ID      string      `xml:"id"`
	Updated string      `xml:"updated"`
	Links   []atomLink  `xml:"link"`
	Entries []atomEntry `xml:"entry"`
}
type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
}
type atomEntry struct {
	Title   string   `xml:"title"`
	ID      string   `xml:"id"`
	Updated string   `xml:"updated"`
	Link    atomLink `xml:"link"`
	Summary string   `xml:"summary"`
}

// Atom renders the channel as an Atom 1.0 document.
func Atom(ch Channel) ([]byte, error) {
	items, updated := ch.sorted()
	root := atomRoot{
		Title: ch.Title, ID: ch.Link, Updated: rfc3339(updated),
		Links: []atomLink{{Href: ch.Link}},
	}
	if ch.SelfLink != "" {
		root.Links = append(root.Links, atomLink{Href: ch.SelfLink, Rel: "self"})
	}
	for _, it := range items {
		root.Entries = append(root.Entries, atomEntry{
			Title: it.Title, ID: it.Link, Updated: rfc3339(it.Published),
			Link: atomLink{Href: it.Link}, Summary: it.Description,
		})
	}
	return marshal(root)
}

func marshal(v any) ([]byte, error) {
	body, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("feed: marshal: %w", err)
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header) // <?xml version="1.0" encoding="UTF-8"?>\n
	buf.Write(body)
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
