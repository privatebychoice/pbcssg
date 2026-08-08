package manifest

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	classify "go.privatebychoice.com/pbc-classification"
	"go.privatebychoice.com/pbcssg/internal/linkscan"
)

// TestEndToEnd exercises the full milestone 1→2 pipeline: rendered HTML →
// linkscan extract+classify → manifest build+encode, using a real classifier
// with an injected dataset.
func TestEndToEnd(t *testing.T) {
	const dataset = `{
      "tracker.example": {"trust":"audited","verified":"2026-01-01","signals":{"adTrackingCookies":"yes","honorsGPC":"no"}},
      "clean.example":   {"trust":"audited","verified":"2026-01-01","signals":{"honorsGPC":"yes","adTrackingCookies":"no","adsTrackers":"none","thirdPartyScripts":"none","fingerprinting":"no","sessionReplay":"no","sellsSharesData":"no"}}
    }`
	fixed := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	c, err := classify.New(
		classify.WithoutDefaultData(),
		classify.WithDataBytes([]byte(dataset)),
		classify.WithFirstParty("owned.example"),
		classify.WithClock(func() time.Time { return fixed }),
	)
	if err != nil {
		t.Fatalf("build classifier: %v", err)
	}
	base, _ := url.Parse("https://example.org")
	s := linkscan.NewScanner(c, base)

	const page = `
		<a href="/self">same-origin, dropped</a>
		<img src="https://tracker.example/p.gif">
		<link rel="stylesheet" href="https://clean.example/s.css">
		<a href="https://owned.example/y">my other site</a>`

	results, err := s.Scan(strings.NewReader(page))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	p := BuildPage("/index", results)

	if p.Summary.Domains != 3 {
		t.Errorf("domains = %d, want 3 (tracker, clean, owned; self-origin dropped)", p.Summary.Domains)
	}
	if p.Summary.WorstGrade != "F" {
		t.Errorf("worstGrade = %q, want F", p.Summary.WorstGrade)
	}

	byDomain := map[string]DomainEntry{}
	for _, d := range p.Domains {
		byDomain[d.Domain] = d
	}
	if byDomain["owned.example"].Trust != "own" || byDomain["owned.example"].Grade != "A" {
		t.Errorf("owned.example = %s/%s, want A/own", byDomain["owned.example"].Grade, byDomain["owned.example"].Trust)
	}
	if byDomain["tracker.example"].Grade != "F" {
		t.Errorf("tracker.example grade = %s, want F", byDomain["tracker.example"].Grade)
	}

	out, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	var round Page
	if err := json.Unmarshal(out, &round); err != nil {
		t.Errorf("manifest is not valid JSON: %v", err)
	}
}
