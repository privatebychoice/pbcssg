package creator

import (
	"net/url"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/appstore"
)

// TestAccountsPermissionsSeparation: the moderator grants live in a distinct Permissions
// column (not mixed with the Ban button), and Ban/Erase share one "also delete their comments"
// wording instead of the old "remove posts" / "delete comments" split.
func TestAccountsPermissionsSeparation(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	app.CreateAccount(appstore.RoleModerator, "") // a moderator row shows the grants
	app.CreateAccount(appstore.RoleMember, "")

	body := authedGet(t, c, app, creatorID, "/admin/moderation/accounts").Body.String()
	for _, want := range []string{"Permissions", "can invite", "can ban", "also delete their comments"} {
		if !strings.Contains(body, want) {
			t.Errorf("accounts page missing %q", want)
		}
	}
	if strings.Contains(body, "remove posts") {
		t.Error("old 'remove posts' wording should be gone")
	}
}

// TestSettingsPersistsAliasCap: the alias daily-change cap round-trips through the Settings
// form into the runtime store (app.db), where the public origin reads it.
func TestSettingsPersistsAliasCap(t *testing.T) {
	c, app := newAuthCreator(t)
	_, _, _, creatorID := seedCreator(t, app)
	form := map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"aliasDailyCap": "5",
	}
	vals := formValues(form)
	rec := authedForm(t, c, creatorID, "/admin/settings", vals)
	if rec.Code != 200 {
		t.Fatalf("save settings: %d\n%s", rec.Code, rec.Body.String())
	}
	if got := app.AliasDailyCap(); got != 5 {
		t.Errorf("persisted alias cap = %d, want 5", got)
	}
}

// formValues builds url.Values from a map (csrf is added by authedForm).
func formValues(kv map[string]string) url.Values {
	v := url.Values{}
	for k, val := range kv {
		v.Set(k, val)
	}
	return v
}
