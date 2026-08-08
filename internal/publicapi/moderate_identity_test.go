package publicapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// TestModIdentitySetsAlias: a moderator sets their comment display name from /_pbc/moderate,
// under the same account-level uniqueness (a taken name is refused, alias unchanged).
func TestModIdentitySetsAlias(t *testing.T) {
	h, app := newMemberAPI(t)
	ck, modID := seedBannerSession(t, app)

	if rec := modFormPost(t, h, "/_pbc/moderate/identity", url.Values{"alias": {"NightMod"}}, ck); rec.Code != http.StatusSeeOther {
		t.Fatalf("set name: %d %s", rec.Code, rec.Body.String())
	}
	if got, _, _ := app.AccountByID(modID); got.Alias != "NightMod" {
		t.Fatalf("mod alias = %q, want NightMod", got.Alias)
	}

	// A name a member already holds is refused (redirect carries the nametaken notice).
	m, _ := app.CreateAccount(appstore.RoleMember, "")
	app.SetAccountAlias(m.ID, "Taken")
	rec := modFormPost(t, h, "/_pbc/moderate/identity", url.Values{"alias": {"taken"}}, ck)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "nametaken") {
		t.Errorf("taken name: code %d loc %q, want 303 → nametaken", rec.Code, rec.Header().Get("Location"))
	}
	if got, _, _ := app.AccountByID(modID); got.Alias != "NightMod" {
		t.Errorf("alias changed to %q despite the collision", got.Alias)
	}

	// The field is present on the page.
	if body := getModerate(t, h, "", ck).Body.String(); !strings.Contains(body, "Your comment name") {
		t.Error("moderate page missing the display-name field")
	}
}
