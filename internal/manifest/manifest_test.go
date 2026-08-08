package manifest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	classify "go.privatebychoice.com/pbc-classification"
	"go.privatebychoice.com/pbcssg/internal/linkscan"
)

// result builds a linkscan.Result directly (no classifier needed) so the
// aggregation logic is tested in isolation.
func result(domain, url string, grade classify.Grade, trust classify.Trust, kind linkscan.Kind, element, attr string) linkscan.Result {
	return linkscan.Result{
		Ref: linkscan.Reference{
			Kind: kind, Element: element, Attr: attr,
			RawURL: url, Resolved: url, Host: domain,
		},
		Classification: classify.Classification{
			Input:   url,
			Domain:  domain,
			Matched: grade != classify.GradeUnclassified,
			Grade:   grade,
			Trust:   trust,
			Reasons: []string{"test reason"},
		},
	}
}

func TestBuildPage(t *testing.T) {
	results := []linkscan.Result{
		result("tracker.example", "https://tracker.example/b.gif", classify.GradeF, classify.TrustAudited, linkscan.KindImage, "img", "src"),
		result("tracker.example", "https://tracker.example/a.js", classify.GradeF, classify.TrustAudited, linkscan.KindScript, "script", "src"),
		result("clean.example", "https://clean.example/s.css", classify.GradeA, classify.TrustAudited, linkscan.KindStylesheet, "link", "href"),
		result("unknown.example", "https://unknown.example/e", classify.GradeUnclassified, classify.TrustUnknown, linkscan.KindFrame, "iframe", "src"),
	}

	p := BuildPage("/blog/post", results)

	if p.Page != "/blog/post" {
		t.Errorf("Page = %q", p.Page)
	}
	if p.Summary.External != 4 {
		t.Errorf("External = %d, want 4", p.Summary.External)
	}
	if p.Summary.Domains != 3 {
		t.Errorf("Domains = %d, want 3", p.Summary.Domains)
	}
	// Grades present: F, A, ? — ? (Unclassified) is the worst by classify's ordinal.
	if p.Summary.WorstGrade != "?" {
		t.Errorf("WorstGrade = %q, want ?", p.Summary.WorstGrade)
	}
	wantByGrade := map[string]int{"F": 1, "A": 1, "?": 1}
	for k, v := range wantByGrade {
		if p.Summary.ByGrade[k] != v {
			t.Errorf("ByGrade[%q] = %d, want %d (%v)", k, p.Summary.ByGrade[k], v, p.Summary.ByGrade)
		}
	}

	// Domains sorted alphabetically.
	gotOrder := []string{}
	for _, d := range p.Domains {
		gotOrder = append(gotOrder, d.Domain)
	}
	wantOrder := []string{"clean.example", "tracker.example", "unknown.example"}
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("domain order = %v, want %v", gotOrder, wantOrder)
	}

	// tracker.example groups both references, sorted by URL.
	var tracker DomainEntry
	for _, d := range p.Domains {
		if d.Domain == "tracker.example" {
			tracker = d
		}
	}
	if len(tracker.Refs) != 2 {
		t.Fatalf("tracker refs = %d, want 2", len(tracker.Refs))
	}
	if tracker.Grade != "F" || tracker.GradeName != "Invasive" {
		t.Errorf("tracker grade = %q/%q, want F/Invasive", tracker.Grade, tracker.GradeName)
	}
	if tracker.Refs[0].URL > tracker.Refs[1].URL {
		t.Errorf("tracker refs not sorted by URL: %v", tracker.Refs)
	}
}

func TestBuildPage_NoExternal(t *testing.T) {
	p := BuildPage("/clean", nil)
	if p.Summary.External != 0 || p.Summary.Domains != 0 {
		t.Errorf("expected empty summary, got %+v", p.Summary)
	}
	if p.Summary.WorstGrade != "" {
		t.Errorf("WorstGrade = %q, want empty for a clean page", p.Summary.WorstGrade)
	}
	if p.Summary.ByGrade != nil {
		t.Errorf("ByGrade = %v, want nil", p.Summary.ByGrade)
	}
}

func TestSiteBuilder(t *testing.T) {
	page1 := BuildPage("/p1", []linkscan.Result{
		result("tracker.example", "https://tracker.example/x", classify.GradeF, classify.TrustAudited, linkscan.KindImage, "img", "src"),
		result("clean.example", "https://clean.example/s.css", classify.GradeA, classify.TrustAudited, linkscan.KindStylesheet, "link", "href"),
	})
	page2 := BuildPage("/p2", []linkscan.Result{
		result("tracker.example", "https://tracker.example/y", classify.GradeF, classify.TrustAudited, linkscan.KindScript, "script", "src"),
		result("owned.example", "https://owned.example/a", classify.GradeA, classify.TrustOwn, linkscan.KindLink, "a", "href"),
	})

	b := NewBuilder()
	b.AddPage(page1)
	b.AddPage(page2)
	site := b.Site()

	if len(site.Pages) != 2 {
		t.Fatalf("site pages = %d, want 2", len(site.Pages))
	}
	if site.Summary.Domains != 3 {
		t.Errorf("site domains = %d, want 3", site.Summary.Domains)
	}
	// Grades across the site: tracker F, clean A, owned A — worst is F (no ? here).
	if site.Summary.WorstGrade != "F" {
		t.Errorf("site WorstGrade = %q, want F", site.Summary.WorstGrade)
	}

	var tracker DomainStat
	for _, d := range site.Domains {
		if d.Domain == "tracker.example" {
			tracker = d
		}
	}
	if got := strings.Join(tracker.Pages, ","); got != "/p1,/p2" {
		t.Errorf("tracker pages = %q, want /p1,/p2", got)
	}
	if tracker.Count != 2 {
		t.Errorf("tracker count = %d, want 2 (one ref on each page)", tracker.Count)
	}
}

func TestEncodeDeterministicAndUnescaped(t *testing.T) {
	p := BuildPage("/p", []linkscan.Result{
		result("t.example", "https://t.example/track?a=1&b=2", classify.GradeD, classify.TrustAudited, linkscan.KindImage, "img", "src"),
	})

	a, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encode(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("Encode is not deterministic")
	}
	// URLs with & must not be HTML-escaped to &.
	if !strings.Contains(string(a), "a=1&b=2") {
		t.Errorf("expected unescaped & in output:\n%s", a)
	}
	// Output must be valid JSON.
	var round Page
	if err := json.Unmarshal(a, &round); err != nil {
		t.Errorf("output is not valid JSON: %v", err)
	}
}
