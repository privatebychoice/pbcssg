package publicapi

import (
	"net/http"
	"testing"
)

// TestMemberAliasRateLimited: once the daily cap is reached, a further alias change is 429.
func TestMemberAliasRateLimited(t *testing.T) {
	h, app := newMemberAPI(t)
	app.SetAliasDailyCap(1)
	_, _, _, accID := seedMember(t, app)
	ck := memberCookie(t, app, accID)

	if rec := memberPostAuthed(t, h, "/_pbc/account/alias", map[string]string{"alias": "One"}, testMemberOrigin, ck); rec.Code != http.StatusOK {
		t.Fatalf("first change: %d %s", rec.Code, rec.Body.String())
	}
	if rec := memberPostAuthed(t, h, "/_pbc/account/alias", map[string]string{"alias": "Two"}, testMemberOrigin, ck); rec.Code != http.StatusTooManyRequests {
		t.Errorf("second change same day: status %d, want 429", rec.Code)
	}
}
