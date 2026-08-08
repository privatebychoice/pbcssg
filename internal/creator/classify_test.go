package creator

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

// customDataset returns a valid dataset that grades customtracker.example as
// Invasive, with a fresh verified date so it is never stale.
func customDataset() []byte {
	today := time.Now().Format("2006-01-02")
	return []byte(`{"customtracker.example":{"trust":"audited","verified":"` + today + `",` +
		`"signals":{"adTrackingCookies":"yes","sellsSharesData":"yes","honorsGPC":"no","adsTrackers":"heavy"}}}`)
}

func gradeFor(h *harness, domain string) string {
	badges, err := h.c.linkBadges(`{"body":"[t](https://` + domain + `/x)"}`)
	if err != nil {
		return "err:" + err.Error()
	}
	for _, b := range badges {
		if b.Domain == domain {
			return b.Grade
		}
	}
	return ""
}

// TestCustomClassifyDataDrivesLiveBadges covers §5.7: saving a custom dataset in
// the editor immediately changes the live in-editor classification, and clearing
// it reverts to the library defaults.
func TestCustomClassifyDataDrivesLiveBadges(t *testing.T) {
	h := newHarness(t)

	// Baseline: an unknown domain is Unclassified ("?").
	if g := gradeFor(h, "customtracker.example"); g != "?" {
		t.Fatalf("baseline grade = %q, want ? (unclassified)", g)
	}

	if err := h.c.saveClassifyData(customDataset()); err != nil {
		t.Fatalf("saveClassifyData: %v", err)
	}
	if g := gradeFor(h, "customtracker.example"); g != "F" {
		t.Errorf("after custom data, grade = %q, want F", g)
	}
	// Persisted for the headless build too.
	if got := string(h.c.storedClassifyData()); !strings.Contains(got, "customtracker.example") {
		t.Errorf("dataset not persisted: %q", got)
	}

	// Clearing reverts to library defaults.
	if err := h.c.saveClassifyData(nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if g := gradeFor(h, "customtracker.example"); g != "?" {
		t.Errorf("after clearing, grade = %q, want ? again", g)
	}
	if h.c.storedClassifyData() != nil {
		t.Errorf("cleared dataset should read back as nil")
	}
}

func TestValidateClassifyData(t *testing.T) {
	if err := validateClassifyData(nil); err != nil {
		t.Errorf("empty dataset should be valid: %v", err)
	}
	if err := validateClassifyData(customDataset()); err != nil {
		t.Errorf("valid dataset rejected: %v", err)
	}
	if err := validateClassifyData([]byte(`{ this is not json `)); err == nil {
		t.Errorf("malformed JSON should be rejected")
	}
	// The library rejects an unknown field (strict decode) ...
	if err := validateClassifyData([]byte(`{"x.example":{"bogusField":1}}`)); err == nil {
		t.Errorf("unknown field should be rejected")
	}
	// ... and an audited entry without a verified date.
	if err := validateClassifyData([]byte(`{"x.example":{"trust":"audited"}}`)); err == nil {
		t.Errorf("audited entry without a verified date should be rejected")
	}
}

// TestBadStoredClassifyDataFallsBack: an invalid dataset already in the store must
// not brick the editor — applyConfig falls back to the library defaults.
func TestBadStoredClassifyDataFallsBack(t *testing.T) {
	h := newHarness(t)
	if err := h.st.SetSetting(keyClassifyData, `{ not valid json`); err != nil {
		t.Fatal(err)
	}
	if err := h.c.applyConfig(h.c.loadBuildConfig(h.c.cfg.Build)); err != nil {
		t.Fatalf("a bad stored dataset must not fail applyConfig: %v", err)
	}
	// The classifier still works (library defaults): a known invasive domain grades.
	if g := gradeFor(h, "youtube.com"); g == "" || g == "?" {
		t.Errorf("fallback classifier should still classify youtube.com, got %q", g)
	}
}

// TestSettingsSavePreservesClassifyData: the general Settings form has no dataset
// field, so saving it must not wipe the stored dataset.
func TestSettingsSavePreservesClassifyData(t *testing.T) {
	h := newHarness(t)
	if err := h.c.saveClassifyData(customDataset()); err != nil {
		t.Fatal(err)
	}
	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.1",
	}))
	if rec.Code != 200 {
		t.Fatalf("settings save: %d", rec.Code)
	}
	if got := string(h.c.storedClassifyData()); !strings.Contains(got, "customtracker.example") {
		t.Errorf("settings save wiped the classification dataset: %q", got)
	}
	if g := gradeFor(h, "customtracker.example"); g != "F" {
		t.Errorf("runtime classifier lost the dataset after settings save: grade %q", g)
	}
}

// TestClassificationEditor covers the in-UI dataset editor (§6.8): the page
// renders, the preview endpoint returns live grades (and rejects invalid data),
// and saving validates, canonicalizes, persists, and hot-applies the dataset.
func TestClassificationEditor(t *testing.T) {
	h := newHarness(t)

	if get := h.get("/admin/classification"); get.Code != 200 || !strings.Contains(get.Body.String(), "/admin/assets/classify.js") {
		t.Fatalf("GET editor: code=%d, missing script", get.Code)
	}

	// Preview (CSRF-exempt) returns each domain's grade.
	pv := h.post("/admin/classification/preview", url.Values{"dataset": {string(customDataset())}})
	var pr struct {
		OK     bool `json:"ok"`
		Grades map[string]struct {
			Grade string `json:"grade"`
		} `json:"grades"`
	}
	if err := json.Unmarshal(pv.Body.Bytes(), &pr); err != nil {
		t.Fatalf("preview json: %v (%s)", err, pv.Body.String())
	}
	if !pr.OK || pr.Grades["customtracker.example"].Grade != "F" {
		t.Fatalf("preview grades wrong: %s", pv.Body.String())
	}
	// An invalid dataset (audited without a verified date) previews as not-ok.
	if bad := h.post("/admin/classification/preview", url.Values{"dataset": {`{"y.example":{"trust":"audited"}}`}}); strings.Contains(bad.Body.String(), `"ok":true`) {
		t.Errorf("invalid dataset should preview ok:false, got %s", bad.Body.String())
	}

	// Save persists + hot-applies (live grade changes).
	save := h.post("/admin/classification", h.form(map[string]string{"dataset": string(customDataset())}))
	if save.Code != 200 || !strings.Contains(save.Body.String(), "saved") {
		t.Fatalf("save: code=%d\n%s", save.Code, save.Body.String())
	}
	if g := gradeFor(h, "customtracker.example"); g != "F" {
		t.Errorf("after save, live grade = %q, want F", g)
	}
	// Stored form is canonical: pretty-printed and free of redundant "unknown".
	stored := string(h.c.storedClassifyData())
	if !strings.Contains(stored, "\n") || strings.Contains(stored, "unknown") {
		t.Errorf("stored dataset not canonicalized:\n%s", stored)
	}

	// Invalid save is rejected (400) and does not change the stored dataset.
	if inv := h.post("/admin/classification", h.form(map[string]string{"dataset": `{"y.example":{"trust":"audited"}}`})); inv.Code != 400 {
		t.Errorf("invalid save code = %d, want 400", inv.Code)
	}
	if g := gradeFor(h, "customtracker.example"); g != "F" {
		t.Errorf("rejected save must not disturb the stored dataset; grade = %q", g)
	}

	// CSRF is required for save.
	if nc := h.post("/admin/classification", url.Values{"dataset": {"{}"}}); nc.Code != 403 {
		t.Errorf("save without CSRF = %d, want 403", nc.Code)
	}
}

func TestClassificationExport(t *testing.T) {
	h := newHarness(t)
	// With no custom dataset, export is a valid empty object, sent as a download.
	rec := h.get("/admin/classification/export")
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Disposition"), `filename="domains.json"`) {
		t.Fatalf("export headers wrong: code=%d cd=%q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	if strings.TrimSpace(rec.Body.String()) != "{}" {
		t.Errorf("empty export should be {}, got %q", rec.Body.String())
	}
	// With a dataset, export returns it.
	if err := h.c.saveClassifyData(customDataset()); err != nil {
		t.Fatal(err)
	}
	if got := h.get("/admin/classification/export").Body.String(); !strings.Contains(got, "customtracker.example") {
		t.Errorf("export should contain the stored dataset:\n%s", got)
	}
}

func TestClassificationImport(t *testing.T) {
	h := newHarness(t)
	// A valid file is imported (saved + hot-applied).
	rec := h.upload("/admin/classification/import", "domains.json", customDataset(), true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Imported") {
		t.Fatalf("valid import: code=%d\n%s", rec.Code, rec.Body.String())
	}
	if g := gradeFor(h, "customtracker.example"); g != "F" {
		t.Errorf("imported dataset not applied: grade %q", g)
	}
	// An invalid file is rejected and the stored dataset is preserved.
	bad := h.upload("/admin/classification/import", "domains.json", []byte(`{"x.example":{"trust":"audited"}}`), true)
	if bad.Code != 400 || !strings.Contains(bad.Body.String(), "rejected") {
		t.Errorf("invalid import should be rejected: code=%d\n%s", bad.Code, bad.Body.String())
	}
	if g := gradeFor(h, "customtracker.example"); g != "F" {
		t.Errorf("rejected import must not disturb the stored dataset; grade %q", g)
	}
	// CSRF is required.
	if nc := h.upload("/admin/classification/import", "domains.json", customDataset(), false); nc.Code != 403 {
		t.Errorf("import without CSRF should be 403, got %d", nc.Code)
	}
}

func TestClassifyReportSettingRoundTrip(t *testing.T) {
	h := newHarness(t)
	rec := h.post("/admin/settings", h.form(map[string]string{
		"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0",
		"classifyReport": "1", "classifyDataRepo": "https://example.com/data",
	}))
	if rec.Code != 200 {
		t.Fatalf("save: %d", rec.Code)
	}
	if bc := h.c.state().build; !bc.ClassifyReport || bc.ClassifyDataRepoURL != "https://example.com/data" {
		t.Errorf("report settings not applied: report=%v repo=%q", bc.ClassifyReport, bc.ClassifyDataRepoURL)
	}
	if get := h.get("/admin/settings").Body.String(); !strings.Contains(get, `name="classifyReport" value="1" checked`) || !strings.Contains(get, "https://example.com/data") {
		t.Errorf("settings form should reflect the saved report config")
	}
	// Unchecking clears it (checkbox absent from the form post).
	if h.post("/admin/settings", h.form(map[string]string{"siteName": "TUL", "baseURL": "https://tul.example", "version": "1.0"})); h.c.state().build.ClassifyReport {
		t.Errorf("unchecking should disable the report")
	}
}
