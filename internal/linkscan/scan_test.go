package linkscan

import (
	"strings"
	"testing"
	"time"

	classify "go.privatebychoice.com/pbc-classification"
)

// testDataset is a tiny, deterministic classifier dataset injected in place of
// the shipped seed so the tests don't depend on it. tracker.example sets ad
// cookies AND fails GPC (→ F Invasive); clean.example is fully verified clean
// (→ A Clean).
const testDataset = `{
  "tracker.example": {
    "trust": "audited", "verified": "2026-01-01",
    "signals": { "adTrackingCookies": "yes", "honorsGPC": "no" }
  },
  "clean.example": {
    "trust": "audited", "verified": "2026-01-01",
    "signals": {
      "honorsGPC": "yes", "adTrackingCookies": "no",
      "adsTrackers": "none", "thirdPartyScripts": "none",
      "fingerprinting": "no", "sessionReplay": "no", "sellsSharesData": "no"
    }
  }
}`

func testScanner(t *testing.T) *Scanner {
	t.Helper()
	fixed := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	c, err := classify.New(
		classify.WithoutDefaultData(),
		classify.WithDataBytes([]byte(testDataset)),
		classify.WithFirstParty("owned.example"), // the operator's *other* own site
		classify.WithClock(func() time.Time { return fixed }),
	)
	if err != nil {
		t.Fatalf("build classifier: %v", err)
	}
	return NewScanner(c, mustURL(t, "https://example.org"))
}

func TestScan_ClassifiesThirdPartyAndDropsFirstParty(t *testing.T) {
	// base is https://example.org. Same-origin and non-network references must be
	// dropped; the four distinct third-party hosts must be classified.
	page := `
		<a href="/relative">rel</a>
		<a href="https://example.org/abs">same-origin absolute</a>
		<a href="mailto:x@example.com">mail</a>
		<a href="https://owned.example/x">my other site</a>
		<img src="https://tracker.example/pixel.gif">
		<script src="https://clean.example/s.js"></script>
		<iframe src="https://unknown.example/e"></iframe>`

	results, err := testScanner(t).Scan(strings.NewReader(page))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	byHost := map[string]Result{}
	for _, r := range results {
		if _, dup := byHost[r.Ref.Host]; dup {
			t.Errorf("duplicate host in results: %s", r.Ref.Host)
		}
		byHost[r.Ref.Host] = r
	}

	if len(results) != 4 {
		t.Fatalf("got %d results, want 4\n%+v", len(results), results)
	}
	// Same-origin / relative / mailto must not appear.
	if _, ok := byHost["example.org"]; ok {
		t.Errorf("same-origin host example.org should have been dropped")
	}

	cases := []struct {
		host      string
		wantGrade classify.Grade
		wantTrust classify.Trust
		wantKind  Kind
	}{
		{"owned.example", classify.GradeA, classify.TrustOwn, KindLink},
		{"tracker.example", classify.GradeF, classify.TrustAudited, KindImage},
		{"clean.example", classify.GradeA, classify.TrustAudited, KindScript},
		{"unknown.example", classify.GradeUnclassified, classify.TrustUnknown, KindFrame},
	}
	for _, c := range cases {
		r, ok := byHost[c.host]
		if !ok {
			t.Errorf("no result for %s", c.host)
			continue
		}
		if r.Classification.Grade != c.wantGrade {
			t.Errorf("%s: grade = %s, want %s", c.host, r.Classification.Grade, c.wantGrade)
		}
		if r.Classification.Trust != c.wantTrust {
			t.Errorf("%s: trust = %s, want %s", c.host, r.Classification.Trust, c.wantTrust)
		}
		if r.Ref.Kind != c.wantKind {
			t.Errorf("%s: kind = %s, want %s", c.host, r.Ref.Kind, c.wantKind)
		}
	}

	// The unknown host must be honestly unmatched, never a false pass.
	if u := byHost["unknown.example"]; u.Classification.Matched {
		t.Errorf("unknown.example should be Matched=false")
	}
}

func TestScan_NilBaseClassifiesAllNetworkRefs(t *testing.T) {
	// With no base, nothing is same-origin, so an absolute third-party ref is
	// still classified while a relative ref (no host) is dropped.
	fixed := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	c, err := classify.New(
		classify.WithoutDefaultData(),
		classify.WithDataBytes([]byte(testDataset)),
		classify.WithClock(func() time.Time { return fixed }),
	)
	if err != nil {
		t.Fatal(err)
	}
	s := NewScanner(c, nil)

	results, err := s.Scan(strings.NewReader(`<img src="/rel.png"><img src="https://tracker.example/p.gif">`))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (relative dropped): %+v", len(results), results)
	}
	if got := results[0].Ref.Host; got != "tracker.example" {
		t.Errorf("host = %q, want tracker.example", got)
	}
	if results[0].Classification.Grade != classify.GradeF {
		t.Errorf("grade = %s, want F", results[0].Classification.Grade)
	}
}
