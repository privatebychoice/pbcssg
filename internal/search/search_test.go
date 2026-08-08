package search

import (
	"encoding/json"
	"strings"
	"testing"
)

const sample = `{
  "body":"# Heading One\n\nFirst para intro.\n\nSecondpara uniqueword deepbody.",
  "tags":["privacy","howto"],
  "keywords":["degoogle"],
  "blocks":[
    {"type":"youtube","youtube":{
      "videoId":"x","name":"n","title":"Video Title",
      "transcript":"transcriptword here","keywords":["ytkeyword"]}},
    {"type":"embed","embed":{
      "provider":"peertube","name":"e","title":"Embed Title",
      "embedUrl":"https://peertube.example/e","transcript":"embednotes here",
      "keywords":["embedkeyword"]}}
  ]
}`

func TestBuildDocument_DefaultScope(t *testing.T) {
	d, err := BuildDocument("/post", "My Post", "2026-07-27", sample, Options{})
	if err != nil {
		t.Fatalf("BuildDocument: %v", err)
	}
	if d.URL != "/post" || d.Title != "My Post" || d.Date != "2026-07-27" {
		t.Errorf("metadata = %+v", d)
	}
	if len(d.Tags) != 2 || d.Tags[0] != "privacy" {
		t.Errorf("tags = %v", d.Tags)
	}
	// Default scope: the page title, headings, tags, keywords, summary (first
	// paragraph), and youtube/embed titles, transcripts, and keywords are indexed.
	for _, want := range []string{
		"My Post", // the page title is searchable text (also weighted higher client-side)
		"Heading One", "First para intro", "privacy", "degoogle",
		"Video Title", "ytkeyword", "transcriptword",
		"Embed Title", "embedkeyword", "embednotes",
	} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("default text missing %q:\n%s", want, d.Text)
		}
	}
	// ...but deep body text (a later paragraph) is NOT in the default scope.
	if strings.Contains(d.Text, "uniqueword") {
		t.Errorf("default scope should not include full body text:\n%s", d.Text)
	}
}

func TestBuildDocument_FullText(t *testing.T) {
	d, err := BuildDocument("/post", "My Post", "", sample, Options{FullText: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Text, "uniqueword") {
		t.Errorf("full-text scope should include body text:\n%s", d.Text)
	}
}

// A members-only (group-gated) block is encrypted out of the page; its plaintext
// must never reach the public search index in ANY scope, or the gate could be
// bypassed by reading search/index.json (SPEC §6.2, §6.10).
func TestBuildDocument_SkipsGatedBlocks(t *testing.T) {
	const gated = `{
	  "body":"# Public Heading\n\nPublic intro.",
	  "blocks":[
	    {"type":"markdown","markdown":"# PublicHeadingYYY\n\npublicbodyYYY visible."},
	    {"type":"markdown","groups":["members"],"markdown":"# GatedHeadingZZZ\n\ngatedbodyZZZ membersonly."}
	  ]
	}`
	for _, ft := range []bool{false, true} {
		d, err := BuildDocument("/p", "T", "", gated, Options{FullText: ft})
		if err != nil {
			t.Fatalf("FullText=%v: %v", ft, err)
		}
		// The non-gated block is still indexed (heading always; body when full-text).
		if !strings.Contains(d.Text, "PublicHeadingYYY") {
			t.Errorf("FullText=%v: non-gated block heading missing:\n%s", ft, d.Text)
		}
		// The gated block must be absent entirely — heading and body, both scopes.
		if strings.Contains(d.Text, "GatedHeadingZZZ") {
			t.Errorf("FullText=%v: gated block HEADING leaked into the index:\n%s", ft, d.Text)
		}
		if strings.Contains(d.Text, "gatedbodyZZZ") {
			t.Errorf("FullText=%v: gated block BODY leaked into the index:\n%s", ft, d.Text)
		}
	}
}

func TestEncodeDeterministicSorted(t *testing.T) {
	docs := []Document{{URL: "/b", Title: "B"}, {URL: "/a", Title: "A"}}
	a, err := Encode(docs)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Encode(docs)
	if string(a) != string(b) {
		t.Errorf("Encode not deterministic")
	}
	var idx struct {
		Docs []Document `json:"docs"`
	}
	if err := json.Unmarshal(a, &idx); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(idx.Docs) != 2 || idx.Docs[0].URL != "/a" {
		t.Errorf("docs not sorted by URL: %+v", idx.Docs)
	}
}

func TestBuildDocument_BadContent(t *testing.T) {
	if _, err := BuildDocument("/x", "X", "", "{bad json", Options{}); err == nil {
		t.Errorf("invalid content JSON should error")
	}
}
