package creator

import (
	"net/url"
	"strings"
	"testing"

	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/store"
)

func TestSettingsMaintenanceRetention(t *testing.T) {
	h := newHarness(t)
	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"maintInviteDays": "7", "maintOrphanDays": "0", // 0 = disable orphan prune
		"maintAliasReleaseDays": "45", "maintTombstoneDays": "14",
	}))
	if rec.Code != 200 {
		t.Fatalf("save: %d\n%s", rec.Code, rec.Body.String())
	}
	m := h.c.store.Maintenance()
	if m.InviteDays != 7 || m.OrphanDays != 0 {
		t.Errorf("retention = %+v, want InviteDays 7 / OrphanDays 0", m)
	}
	if m.AliasReleaseDays != 45 || m.TombstoneDays != 14 {
		t.Errorf("retention = %+v, want AliasReleaseDays 45 / TombstoneDays 14", m)
	}
	// A field left out of the form falls back to its baked-in default.
	if m.RejectedDays != store.DefaultRejectedRetentionDays {
		t.Errorf("unspecified rejected retention = %d, want default %d", m.RejectedDays, store.DefaultRejectedRetentionDays)
	}
	for _, name := range []string{"Maintenance", `name="maintInviteDays"`, `name="maintAliasReleaseDays"`, `name="maintTombstoneDays"`, `name="aliasDailyCap"`} {
		if !strings.Contains(rec.Body.String(), name) {
			t.Errorf("settings page missing %q", name)
		}
	}
	// A negative retention is rejected without half-saving.
	if bad := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0", "maintInviteDays": "-5",
	})); bad.Code != 400 {
		t.Errorf("negative retention: code %d, want 400", bad.Code)
	}
}

func TestSettingsSaveDrivesConfigAndPersists(t *testing.T) {
	h := newHarness(t)

	// Seed config (from the harness) is TUL / https://tul.example.
	if h.c.state().build.SiteName != "TUL" {
		t.Fatalf("seed site name = %q", h.c.state().build.SiteName)
	}

	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "PBC", "baseURL": "https://pbc.example",
		"firstParty": "cdn.pbc.example, media.pbc.example",
		"version":    "1.0", "gpc": "2026-07-27",
		"lang": "en", "search": "1", "openGraph": "1",
	}))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Settings saved") {
		t.Fatalf("save settings: code=%d\n%s", rec.Code, rec.Body.String())
	}

	// The live runtime reflects the new config immediately. The release number is
	// not an editable field, so it is preserved (the seed "1"), not wiped.
	bc := h.c.state().build
	if bc.SiteName != "PBC" || bc.BaseURL != "https://pbc.example" || bc.BuildNumber != "1" || !bc.Search || !bc.OpenGraph {
		t.Errorf("runtime not updated: %+v", bc)
	}
	if len(bc.FirstParty) != 2 || bc.FirstParty[0] != "cdn.pbc.example" {
		t.Errorf("first-party not parsed: %v", bc.FirstParty)
	}

	// A fresh Creator over the same store loads the saved settings (persistence),
	// overlaying — and overriding — the CLI seed.
	c2, err := New(h.st, Config{OutDir: h.out, Build: build.Config{SiteName: "SEED", BaseURL: "https://seed.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if c2.state().build.SiteName != "PBC" || c2.state().build.BaseURL != "https://pbc.example" {
		t.Errorf("saved settings not reloaded: %+v", c2.state().build)
	}

	// The settings form renders the current values.
	get := h.get("/admin/settings")
	if get.Code != 200 || !strings.Contains(get.Body.String(), `value="PBC"`) || !strings.Contains(get.Body.String(), "cdn.pbc.example") {
		t.Errorf("settings form missing values:\n%s", get.Body.String())
	}
}

func TestSettingsCSRF(t *testing.T) {
	h := newHarness(t)
	rec := h.post("/admin/settings", url.Values{"siteName": {"X"}}) // no csrf
	if rec.Code != 403 {
		t.Errorf("settings save without CSRF should be 403, got %d", rec.Code)
	}
}

func TestConfigFromForm(t *testing.T) {
	f := fakeForm{"siteName": "S", "baseURL": "https://s.example", "search": "1", "firstParty": "a.com,b.com"}
	bc := configFromForm(f)
	if bc.SiteName != "S" || !bc.Search || bc.SearchFullText {
		t.Errorf("parsed wrong: %+v", bc)
	}
	if len(bc.FirstParty) != 2 {
		t.Errorf("first-party: %v", bc.FirstParty)
	}
}

func TestSecurityTxtSettings(t *testing.T) {
	// Expires defaults to a valid RFC 3339 timestamp (one year out) when a contact is
	// set but Expires is left blank; a provided date is normalized.
	bc := configFromForm(fakeForm{"secContact": "mailto:sec@ex.example"})
	if _, ok := build.NormalizeSecurityExpires(bc.SecurityExpires); !ok {
		t.Errorf("blank Expires with a contact should default to a valid timestamp, got %q", bc.SecurityExpires)
	}
	if got := configFromForm(fakeForm{"secContact": "mailto:sec@ex.example", "secExpires": "2027-03-04"}); got.SecurityExpires != "2027-03-04T00:00:00Z" {
		t.Errorf("provided date should normalize to RFC 3339, got %q", got.SecurityExpires)
	}
	// No contact → no defaulted Expires (the file is not emitted).
	if got := configFromForm(fakeForm{}); got.SecurityExpires != "" {
		t.Errorf("no contact should leave Expires blank, got %q", got.SecurityExpires)
	}

	// A bad contact is rejected at save (not persisted).
	h := newHarness(t)
	rec := h.post("/admin/settings", h.form(map[string]string{"siteName": "S", "baseURL": "https://s.example", "secContact": "ftp://nope.example"}))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "security.txt") {
		t.Errorf("bad security contact should be rejected: code=%d", rec.Code)
	}
	// A valid contact persists and round-trips into the form (a successful save
	// re-renders the settings page at 200 with a "Settings saved" notice).
	rec = h.post("/admin/settings", h.form(map[string]string{"siteName": "S", "baseURL": "https://s.example", "secContact": "mailto:sec@ex.example"}))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Settings saved") {
		t.Fatalf("valid save: %d\n%s", rec.Code, rec.Body.String())
	}
	if body := h.get("/admin/settings").Body.String(); !strings.Contains(body, "mailto:sec@ex.example") {
		t.Errorf("saved security contact should show in the form:\n%s", body)
	}
}

// fakeForm is a formValuer for unit tests.
type fakeForm map[string]string

func (f fakeForm) FormValue(k string) string { return f[k] }

func TestEmbedHostsRoundTrip(t *testing.T) {
	h := newHarness(t)
	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example",
		"version": "1.0", "buildNumber": "1",
		"embedHosts": "peertube.example\nplayer.vimeo.com",
	}))
	if rec.Code != 200 {
		t.Fatalf("save: %d\n%s", rec.Code, rec.Body.String())
	}
	bc := h.c.state().build
	if len(bc.EmbedHosts) != 2 || bc.EmbedHosts[0] != "peertube.example" || bc.EmbedHosts[1] != "player.vimeo.com" {
		t.Fatalf("embed hosts not parsed: %v", bc.EmbedHosts)
	}
	// Persisted and re-rendered in the form.
	get := h.get("/admin/settings")
	if !strings.Contains(get.Body.String(), "peertube.example") {
		t.Errorf("settings form missing embed hosts:\n%s", get.Body.String())
	}
}

func TestLoadBuildConfigOverlaysStoredSettings(t *testing.T) {
	h := newHarness(t)
	for _, kv := range [][2]string{
		{keyBaseURL, "https://saved.example"},
		{keyFirstParty, "cdn.saved.example, media.saved.example"},
		{keyEmbedHosts, "peertube.saved"},
		{keyOpenGraph, "1"},
		{keyMetrics, "1"},
	} {
		if err := h.st.SetSetting(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	// LoadBuildConfig is what the headless `pbcssg build` calls: stored settings
	// override the CLI seed; unset keys keep the seed value.
	bc := LoadBuildConfig(h.st, build.Config{SiteName: "SEED", BaseURL: "https://seed.example", BuildNumber: "9"})
	if bc.BaseURL != "https://saved.example" {
		t.Errorf("stored baseURL not overlaid: %q", bc.BaseURL)
	}
	if bc.SiteName != "SEED" || bc.BuildNumber != "9" {
		t.Errorf("unset keys should keep the seed: site=%q build=%q", bc.SiteName, bc.BuildNumber)
	}
	if len(bc.EmbedHosts) != 1 || bc.EmbedHosts[0] != "peertube.saved" {
		t.Errorf("embed-host allowlist not overlaid for headless build: %v", bc.EmbedHosts)
	}
	if len(bc.FirstParty) != 2 || !bc.OpenGraph {
		t.Errorf("first-party/openGraph not overlaid: %v og=%v", bc.FirstParty, bc.OpenGraph)
	}
	if !bc.Metrics {
		t.Errorf("metrics.enabled not overlaid for headless build: %v", bc.Metrics)
	}
}

func TestNavRoundTrip(t *testing.T) {
	got := parseNav("Home | /\nBlog | /blog/\n  \nBadLineNoPipe\n| /orphan\nJust A Label |")
	if len(got) != 3 {
		t.Fatalf("parseNav kept %d, want 3: %+v", len(got), got)
	}
	if got[0].Label != "Home" || got[0].Href != "/" || got[1].Href != "/blog/" {
		t.Errorf("parsed wrong: %+v", got)
	}
	// A pipe with an empty label falls back to the href as the label.
	if got[2].Label != "/orphan" || got[2].Href != "/orphan" {
		t.Errorf("orphan label fallback wrong: %+v", got[2])
	}
	// Round-trips back to text.
	if navToText(got) != "Home | /\nBlog | /blog/\n/orphan | /orphan" {
		t.Errorf("navToText round-trip: %q", navToText(got))
	}
}

func TestNavSettingDrivesBuildConfig(t *testing.T) {
	h := newHarness(t)
	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0", "buildNumber": "1",
		"nav": "Home | /\nPrivacy | /privacy/",
	}))
	if rec.Code != 200 {
		t.Fatalf("save: %d", rec.Code)
	}
	nav := h.c.state().build.Nav
	if len(nav) != 2 || nav[0].Href != "/" || nav[1].Label != "Privacy" {
		t.Fatalf("nav not applied to runtime config: %+v", nav)
	}
	if !strings.Contains(h.get("/admin/settings").Body.String(), "Privacy | /privacy/") {
		t.Errorf("settings form should re-render the nav links")
	}
}

func TestFeedsRoundTrip(t *testing.T) {
	got := parseFeeds("Blog | /blog/* | TUL Blog | list\nnews | /news/*\nquiet | /q/* | | yes\nBadNoGlob\n| /orphan")
	if len(got) != 3 {
		t.Fatalf("parseFeeds kept %d, want 3: %+v", len(got), got)
	}
	// Full line: name slugified, title kept, listed set.
	if got[0].Name != "blog" || got[0].Glob != "/blog/*" || got[0].Title != "TUL Blog" || !got[0].Listed {
		t.Errorf("row 0 parsed wrong: %+v", got[0])
	}
	// No 4th field → not listed.
	if got[1].Name != "news" || got[1].Listed {
		t.Errorf("row 1 should be unlisted: %+v", got[1])
	}
	// Empty title but listed ("yes") → listed with no custom title.
	if got[2].Name != "quiet" || got[2].Title != "" || !got[2].Listed {
		t.Errorf("row 2 should be titleless but listed: %+v", got[2])
	}
	// Round-trips back to text, re-emitting the empty title column before "list".
	want := "blog | /blog/* | TUL Blog | list\nnews | /news/*\nquiet | /q/* |  | list"
	if feedsToText(got) != want {
		t.Errorf("feedsToText round-trip:\n got %q\nwant %q", feedsToText(got), want)
	}
}

func TestReleaseNumberNotWipedBySettingsSave(t *testing.T) {
	h := newHarness(t)
	// The release counter has advanced (as Package release would).
	if err := h.st.SetSetting(keyBuildNumber, "7"); err != nil {
		t.Fatal(err)
	}
	if err := h.c.applyConfig(h.c.loadBuildConfig(h.c.cfg.Build)); err != nil {
		t.Fatal(err)
	}
	if h.c.state().build.BuildNumber != "7" {
		t.Fatalf("precondition: release not loaded")
	}
	// Saving settings (which has no release field) must preserve it, not wipe it.
	if rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "X", "baseURL": "https://x.example", "version": "1.1",
	})); rec.Code != 200 {
		t.Fatalf("save: %d", rec.Code)
	}
	if h.c.state().build.BuildNumber != "7" {
		t.Errorf("settings save wiped the release number: %q", h.c.state().build.BuildNumber)
	}
	if v, _, _ := h.st.Setting(keyBuildNumber); v != "7" {
		t.Errorf("release number not persisted through save: %q", v)
	}
}

func TestSettingsGPCDateValidation(t *testing.T) {
	base := func(gpc string) map[string]string {
		return map[string]string{
			"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0", "gpc": gpc,
		}
	}

	// A blank date is valid — gpc.json simply omits lastUpdate.
	h := newHarness(t)
	if rec := h.post("/admin/settings", h.form(base(""))); rec.Code != 200 {
		t.Fatalf("blank GPC date should save: %d\n%s", rec.Code, rec.Body.String())
	}
	if h.c.state().build.GPCLastUpdate != "" {
		t.Errorf("blank GPC date should persist as empty")
	}

	// A valid ISO date is accepted and applied.
	if rec := h.post("/admin/settings", h.form(base("2026-07-30"))); rec.Code != 200 {
		t.Fatalf("valid GPC date should save: %d\n%s", rec.Code, rec.Body.String())
	}
	if got := h.c.state().build.GPCLastUpdate; got != "2026-07-30" {
		t.Errorf("GPC date not applied: %q", got)
	}

	// A malformed date is rejected inline and does not disturb the stored value.
	rec := h.post("/admin/settings", h.form(base("July 30")))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "YYYY-MM-DD") {
		t.Errorf("malformed GPC date should be rejected: code=%d\n%s", rec.Code, rec.Body.String())
	}
	if got := h.c.state().build.GPCLastUpdate; got != "2026-07-30" {
		t.Errorf("rejected save must not change the stored date, got %q", got)
	}
}

func TestSettingsRejectsBadEmbedHost(t *testing.T) {
	h := newHarness(t)
	// A CSP-breaking embed host is rejected inline and not persisted.
	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"embedHosts": "peertube.example\nevil.com; default-src *",
	}))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "use a bare host") {
		t.Fatalf("bad embed host should be rejected: code=%d\n%s", rec.Code, rec.Body.String())
	}
	if hosts := h.c.state().build.EmbedHosts; len(hosts) != 0 {
		t.Errorf("rejected save must not persist embed hosts, got %v", hosts)
	}
	// A clean allowlist saves.
	ok := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"embedHosts": "peertube.example, *.vimeo.com",
	}))
	if ok.Code != 200 {
		t.Fatalf("clean embed hosts should save: %d\n%s", ok.Code, ok.Body.String())
	}
}

func TestSettingsSitemapToggles(t *testing.T) {
	h := newHarness(t)
	// Enable the sitemap and the listing-pages sub-option.
	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"sitemap": "1", "sitemapListings": "1",
	}))
	if rec.Code != 200 {
		t.Fatalf("save: %d\n%s", rec.Code, rec.Body.String())
	}
	if b := h.c.state().build; !b.Sitemap || !b.SitemapListings {
		t.Errorf("sitemap toggles not applied: sitemap=%v listings=%v", b.Sitemap, b.SitemapListings)
	}
	if body := h.get("/admin/settings").Body.String(); !strings.Contains(body, `name="sitemap" value="1" checked`) {
		t.Errorf("settings page should show the sitemap checkbox checked")
	}
	// Unchecking (fields omitted from the POST) turns both off.
	if rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
	})); rec.Code != 200 {
		t.Fatalf("save off: %d", rec.Code)
	}
	if b := h.c.state().build; b.Sitemap || b.SitemapListings {
		t.Errorf("sitemap toggles should be off when unchecked: sitemap=%v listings=%v", b.Sitemap, b.SitemapListings)
	}
}

func TestEditPagePostFlagRoundTrip(t *testing.T) {
	h := newHarness(t)
	// Create a page marked as a post; the edit page should re-render it checked.
	rec := h.post("/pages", h.form(map[string]string{"title": "Article", "path": "/article", "body": "# A", "isPost": "1"}))
	if rec.Code != 303 {
		t.Fatalf("create: %d\n%s", rec.Code, rec.Body.String())
	}
	if body := h.get(rec.Header().Get("Location")).Body.String(); !strings.Contains(body, `name="isPost" value="1" checked`) {
		t.Errorf("edit page should show isPost checked:\n%s", body)
	}
	// A page created without it stays unchecked.
	rec = h.post("/pages", h.form(map[string]string{"title": "Page", "path": "/page", "body": "# P"}))
	if body := h.get(rec.Header().Get("Location")).Body.String(); strings.Contains(body, `name="isPost" value="1" checked`) {
		t.Errorf("ordinary page should not have isPost checked")
	}
}

func TestEditPageNoIndexRoundTrip(t *testing.T) {
	h := newHarness(t)
	// Create a page with noindex ticked; the edit page should re-render it checked.
	rec := h.post("/pages", h.form(map[string]string{"title": "Secret", "path": "/secret", "body": "# S", "noIndex": "1"}))
	if rec.Code != 303 {
		t.Fatalf("create: %d\n%s", rec.Code, rec.Body.String())
	}
	if body := h.get(rec.Header().Get("Location")).Body.String(); !strings.Contains(body, `name="noIndex" value="1" checked`) {
		t.Errorf("edit page should show noIndex checked:\n%s", body)
	}
	// A page created without it stays unchecked.
	rec = h.post("/pages", h.form(map[string]string{"title": "Public", "path": "/public", "body": "# P"}))
	if body := h.get(rec.Header().Get("Location")).Body.String(); strings.Contains(body, `name="noIndex" value="1" checked`) {
		t.Errorf("public page should not have noIndex checked")
	}
}

func TestSettingsBodyFont(t *testing.T) {
	h := newHarness(t)
	// A valid choice applies and shows in the served theme.css preview.
	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0", "font": "transitional",
	}))
	if rec.Code != 200 {
		t.Fatalf("save font: %d\n%s", rec.Code, rec.Body.String())
	}
	if got := h.c.state().build.Font; got != "transitional" {
		t.Errorf("font not applied: %q", got)
	}
	if css := h.get("/admin/assets/theme.css").Body.String(); !strings.Contains(css, "--font-sans: Charter") {
		t.Errorf("preview theme.css missing chosen font")
	}
	// A bogus value normalizes to the system default (no override).
	ok := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0", "font": "not-a-font",
	}))
	if ok.Code != 200 || h.c.state().build.Font != "system" {
		t.Errorf("bogus font should normalize to system, got %q (code %d)", h.c.state().build.Font, ok.Code)
	}
}
