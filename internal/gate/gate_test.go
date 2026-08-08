package gate

import (
	"strings"
	"testing"

	classify "go.privatebychoice.com/pbc-classification"
	"go.privatebychoice.com/pbcssg/internal/linkscan"
)

func res(domain string, grade classify.Grade) linkscan.Result {
	return linkscan.Result{
		Ref: linkscan.Reference{
			Kind: linkscan.KindLink, Element: "a", Attr: "href",
			RawURL: "https://" + domain + "/x", Resolved: "https://" + domain + "/x", Host: domain,
		},
		Classification: classify.Classification{
			Input: "https://" + domain + "/x", Domain: domain,
			Matched: grade != classify.GradeUnclassified, Grade: grade,
			Reasons: []string{"r"},
		},
	}
}

func domains(flags []Flag) []string {
	var out []string
	for _, f := range flags {
		out = append(out, f.Domain)
	}
	return out
}

func TestEvaluate_DefaultThresholdFlagsDFAndUnclassified(t *testing.T) {
	results := []linkscan.Result{
		res("a.example", classify.GradeA),
		res("b.example", classify.GradeB),
		res("c.example", classify.GradeC),
		res("d.example", classify.GradeD),
		res("f.example", classify.GradeF),
		res("u.example", classify.GradeUnclassified),
	}
	r := Evaluate(results, Config{})

	got := strings.Join(domains(r.Flags), ",")
	want := "d.example,f.example,u.example" // sorted; A/B/C pass
	if got != want {
		t.Errorf("flags = %q, want %q", got, want)
	}
	if n := len(r.NeedsAcknowledgement()); n != 3 {
		t.Errorf("NeedsAcknowledgement = %d, want 3", n)
	}
	if r.Blocked() {
		t.Errorf("warn+ack mode should never be Blocked")
	}
}

func TestEvaluate_ConsentGatedExemption(t *testing.T) {
	results := []linkscan.Result{
		res("youtube-nocookie.com", classify.GradeC), // C normally passes...
		res("tracker.example", classify.GradeF),
	}
	// On an /external/youtube page the facade is pre-acknowledged. Use a low
	// threshold so the C-grade facade is flagged, to prove the exemption path.
	r := Evaluate(results, Config{
		FlagAtOrBelow: classify.GradeC,
		ExemptDomains: map[string]bool{"youtube-nocookie.com": true},
	})

	var facade, tracker Flag
	for _, f := range r.Flags {
		switch f.Domain {
		case "youtube-nocookie.com":
			facade = f
		case "tracker.example":
			tracker = f
		}
	}
	if !facade.PreAcknowledged {
		t.Errorf("facade should be pre-acknowledged")
	}
	if tracker.PreAcknowledged {
		t.Errorf("tracker must not be exempt")
	}
	// The facade is still reported but must not require acknowledgement.
	ackDomains := strings.Join(domains(r.NeedsAcknowledgement()), ",")
	if ackDomains != "tracker.example" {
		t.Errorf("NeedsAcknowledgement = %q, want only tracker.example", ackDomains)
	}
}

func TestEvaluate_HardBlock(t *testing.T) {
	results := []linkscan.Result{res("tracker.example", classify.GradeF)}

	if r := Evaluate(results, Config{HardBlock: true}); !r.Blocked() {
		t.Errorf("hard-block with a non-exempt F should be Blocked")
	}
	// If the only flag is exempt, hard-block does not block.
	exempt := Evaluate(results, Config{HardBlock: true, ExemptDomains: map[string]bool{"tracker.example": true}})
	if exempt.Blocked() {
		t.Errorf("hard-block should not block when all flags are pre-acknowledged")
	}
}

func TestEvaluate_CustomThresholdAndGrouping(t *testing.T) {
	// FlagAtOrBelow = F flags only F and ? (D passes at this threshold).
	results := []linkscan.Result{
		res("d.example", classify.GradeD),
		res("f.example", classify.GradeF),
		// two references to the same flagged domain must group into one flag
		res("f.example", classify.GradeF),
	}
	r := Evaluate(results, Config{FlagAtOrBelow: classify.GradeF})
	if got := strings.Join(domains(r.Flags), ","); got != "f.example" {
		t.Errorf("flags = %q, want f.example only (D passes at threshold F)", got)
	}
	if len(r.Flags) != 1 || len(r.Flags[0].References) != 2 {
		t.Errorf("expected one grouped flag with 2 references, got %+v", r.Flags)
	}
}
