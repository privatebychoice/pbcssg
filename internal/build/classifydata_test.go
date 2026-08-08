package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.privatebychoice.com/pbcssg/internal/store"
)

// TestBuildUsesAndPublishesCustomClassifyData covers §5.7: a custom
// pbc-classification dataset supplied via Config.ClassifyData is (a) merged over
// the library defaults so it changes how a page's external references are graded,
// and (b) published into the bundle for transparency.
func TestBuildUsesAndPublishesCustomClassifyData(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// A page referencing a domain the library does not know about.
	pid, _ := s.CreatePage(store.Page{Path: "/", Slug: "home", Title: "Home"})
	rid, _ := s.SaveRevision(pid, `{"body":"# Home\n\n[t](https://customtracker.example/x)"}`, "")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}

	base := Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1"}

	// Without a custom dataset, the unknown domain is Unclassified ("?").
	out1 := t.TempDir()
	if _, err := Run(s, base, out1); err != nil {
		t.Fatal(err)
	}
	if g := domainGrade(t, out1, "customtracker.example"); g != "?" {
		t.Fatalf("without custom data, grade = %q, want ? (unclassified)", g)
	}
	if _, err := os.Stat(filepath.Join(out1, ".well-known", "pbc-classification", "domains.json")); !os.IsNotExist(err) {
		t.Errorf("no dataset should be published when none is configured")
	}

	// A fresh, non-stale verified date keeps the audited entry from being demoted.
	today := time.Now().Format("2006-01-02")
	data := []byte(`{"customtracker.example":{"trust":"audited","verified":"` + today + `",` +
		`"signals":{"adTrackingCookies":"yes","sellsSharesData":"yes","honorsGPC":"no","adsTrackers":"heavy"}}}`)

	// With the report enabled, the dataset is used AND published verbatim.
	cfg := base
	cfg.ClassifyData = data
	cfg.ClassifyReport = true
	out2 := t.TempDir()
	if _, err := Run(s, cfg, out2); err != nil {
		t.Fatal(err)
	}
	if g := domainGrade(t, out2, "customtracker.example"); g != "F" {
		t.Errorf("with custom data, grade = %q, want F (invasive)", g)
	}
	if pub := read(t, out2, ".well-known/pbc-classification/domains.json"); pub != string(data) {
		t.Errorf("published dataset mismatch:\n got %q\nwant %q", pub, data)
	}

	// With the report disabled, the dataset still drives grading but is NOT
	// published (publishing is gated by the report toggle, §5.7).
	cfg.ClassifyReport = false
	out3 := t.TempDir()
	if _, err := Run(s, cfg, out3); err != nil {
		t.Fatal(err)
	}
	if g := domainGrade(t, out3, "customtracker.example"); g != "F" {
		t.Errorf("dataset should still grade even with the report off, got %q", g)
	}
	if _, err := os.Stat(filepath.Join(out3, ".well-known", "pbc-classification", "domains.json")); !os.IsNotExist(err) {
		t.Errorf("dataset must not be published when the report is disabled")
	}
}

// domainGrade reads the home page manifest and returns the grade letter recorded
// for the given external domain (empty if the domain is not listed).
func domainGrade(t *testing.T, dir, domain string) string {
	t.Helper()
	var pm struct {
		Domains []struct {
			Domain string `json:"domain"`
			Grade  string `json:"grade"`
		} `json:"domains"`
	}
	if err := json.Unmarshal([]byte(read(t, dir, "manifest/index.json")), &pm); err != nil {
		t.Fatalf("index manifest invalid: %v", err)
	}
	for _, d := range pm.Domains {
		if d.Domain == domain {
			return d.Grade
		}
	}
	return ""
}

// TestBuildClassificationReport covers the /classification report page (§5.7):
// it is always emitted with the rating-scale legend and disclaimer; with the
// report enabled it lists the dataset and links the published JSON, and with it
// disabled it exposes no dataset. Pages also link to it ("How we rate these").
func TestBuildClassificationReport(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pid, _ := s.CreatePage(store.Page{Path: "/", Slug: "home", Title: "Home"})
	rid, _ := s.SaveRevision(pid, `{"body":"# Home\n\n[t](https://customtracker.example/x)"}`, "")
	if err := s.Publish(pid, rid); err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format("2006-01-02")
	data := []byte(`{"customtracker.example":{"trust":"audited","verified":"` + today + `","signals":{"adTrackingCookies":"yes","sellsSharesData":"yes"}},` +
		`"cleanish.example":{"trust":"audited","verified":"` + today + `","signals":{"adTrackingCookies":"no","adsTrackers":"none","thirdPartyScripts":"none","fingerprinting":"no","sessionReplay":"no","sellsSharesData":"no","honorsGPC":"yes"}}}`)
	base := Config{SiteName: "TUL", BaseURL: "https://tul.example", Version: "1.0", BuildNumber: "1", ClassifyData: data}

	// Report ENABLED: legend + "Classifications used" listing + JSON link.
	on := base
	on.ClassifyReport = true
	on.ClassifyDataRepoURL = "https://example.com/my-dataset"
	outOn := t.TempDir()
	if _, err := Run(s, on, outOn); err != nil {
		t.Fatal(err)
	}
	rep := read(t, outOn, "classification/index.html")
	for _, want := range []string{
		"How we rate external links", "The rating scale", "Clean", "Invasive",
		"pbc-classification", "https://example.com/my-dataset",
		"Classifications used",
		"<code>cleanish.example</code>", "<code>customtracker.example</code>",
		`href="/.well-known/pbc-classification/domains.json"`,
	} {
		if !strings.Contains(rep, want) {
			t.Errorf("enabled report missing %q", want)
		}
	}
	// Alphabetical: cleanish.example before customtracker.example.
	if strings.Index(rep, "cleanish.example") > strings.Index(rep, "customtracker.example") {
		t.Errorf("dataset listing should be alphabetical")
	}
	if _, err := os.Stat(filepath.Join(outOn, ".well-known", "pbc-classification", "domains.json")); err != nil {
		t.Errorf("JSON should be published when the report is enabled: %v", err)
	}
	// Every page links to the report from its external-references block.
	if home := read(t, outOn, "index.html"); !strings.Contains(home, `href="/classification"`) {
		t.Errorf("page external-references should link to /classification")
	}

	// Report DISABLED: page still explains the scale, but exposes no dataset.
	off := base
	off.ClassifyReport = false
	outOff := t.TempDir()
	if _, err := Run(s, off, outOff); err != nil {
		t.Fatal(err)
	}
	lite := read(t, outOff, "classification/index.html")
	if !strings.Contains(lite, "The rating scale") || !strings.Contains(lite, "pbc-classification") {
		t.Errorf("disabled report should still explain the rating system")
	}
	if strings.Contains(lite, "Classifications used") || strings.Contains(lite, "domains.json") {
		t.Errorf("disabled report must not expose the dataset:\n%s", lite)
	}
	if _, err := os.Stat(filepath.Join(outOff, ".well-known", "pbc-classification", "domains.json")); !os.IsNotExist(err) {
		t.Errorf("JSON must not be published when the report is disabled")
	}
}
