package feed

import (
	"strings"
	"testing"
	"time"
)

func sampleChannel() Channel {
	return Channel{
		Title: "TUL — Blog", Link: "https://tul.example/blog/",
		SelfLink: "https://tul.example/feeds/blog.rss", Description: "Latest posts",
		Items: []Item{
			{Title: "Older", Link: "https://tul.example/blog/old", Description: "first & <b>oldest</b>",
				Published: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)},
			{Title: "Newer", Link: "https://tul.example/blog/new", Description: "second",
				Published: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)},
		},
	}
}

func TestRSS(t *testing.T) {
	out, err := RSS(sampleChannel())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<rss version="2.0">`,
		"<title>TUL — Blog</title>",
		"<link>https://tul.example/blog/</link>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("RSS missing %q", want)
		}
	}
	// Newest item comes first (sorted), and dates are RFC1123Z in UTC.
	iNew := strings.Index(s, "https://tul.example/blog/new")
	iOld := strings.Index(s, "https://tul.example/blog/old")
	if iNew < 0 || iOld < 0 || iNew > iOld {
		t.Errorf("items not sorted newest-first (new=%d old=%d)", iNew, iOld)
	}
	if !strings.Contains(s, "Mon, 27 Jul 2026 12:00:00 +0000") {
		t.Errorf("RSS pubDate not RFC1123Z UTC:\n%s", s)
	}
	// lastBuildDate is the newest item, not the wall clock (reproducible).
	if !strings.Contains(s, "<lastBuildDate>Mon, 27 Jul 2026 12:00:00 +0000</lastBuildDate>") {
		t.Errorf("lastBuildDate should equal newest item date")
	}
	// XML escaping of item description.
	if !strings.Contains(s, "first &amp; &lt;b&gt;oldest&lt;/b&gt;") {
		t.Errorf("description not XML-escaped:\n%s", s)
	}
	if !strings.Contains(s, `<guid isPermaLink="true">https://tul.example/blog/new</guid>`) {
		t.Errorf("guid permalink missing")
	}
}

func TestAtom(t *testing.T) {
	out, err := Atom(sampleChannel())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`xmlns="http://www.w3.org/2005/Atom"`,
		"<updated>2026-07-27T12:00:00Z</updated>",
		`<link href="https://tul.example/feeds/blog.rss" rel="self">`,
		"<id>https://tul.example/blog/new</id>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Atom missing %q:\n%s", want, s)
		}
	}
}

func TestDeterministic(t *testing.T) {
	a, _ := RSS(sampleChannel())
	b, _ := RSS(sampleChannel())
	if string(a) != string(b) {
		t.Errorf("RSS not deterministic")
	}
}
