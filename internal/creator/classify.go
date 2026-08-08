package creator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	classify "go.privatebychoice.com/pbc-classification"
)

// This file wires the operator's optional custom pbc-classification dataset
// (domains.json) through the editor (§5.7): it is stored in the content DB, used
// by both the live editor badges and the build (via build.Config.ClassifyData),
// and published into the built bundle for transparency. It merges over the
// library's embedded defaults, so the operator adds to or overrides them.

// storedClassifyData returns the operator's saved custom classification dataset
// (domains.json bytes), or nil when none is set.
func (c *Creator) storedClassifyData() []byte {
	if v, ok, err := c.store.Setting(keyClassifyData); err == nil && ok && strings.TrimSpace(v) != "" {
		return []byte(v)
	}
	return nil
}

// validateClassifyData reports whether b is a dataset pbc-classification accepts.
// An empty dataset is valid (it clears the custom override). Validation reuses the
// library itself — the parser + validator are unexported and there is no
// standalone validator, so a throwaway classifier is the single source of truth
// (it enforces the strict schema and the verified-date rules).
func validateClassifyData(b []byte) error {
	if strings.TrimSpace(string(b)) == "" {
		return nil
	}
	if _, err := classify.New(classify.WithDataBytes(b)); err != nil {
		return err
	}
	return nil
}

// saveClassifyData validates, canonicalizes, persists, and hot-applies a custom
// classification dataset so both the live editor badges and the next build reflect
// it. An empty dataset clears the override, reverting to the library defaults.
// Validation runs first against the library's strict parser (rejecting unknown
// fields / bad dates) so a canonical re-marshal can't silently drop a typo.
func (c *Creator) saveClassifyData(b []byte) error {
	if err := validateClassifyData(b); err != nil {
		return fmt.Errorf("classification dataset rejected: %w", err)
	}
	m, err := parseClassifyDataset(b)
	if err != nil {
		return fmt.Errorf("classification dataset rejected: %w", err)
	}
	canon, err := marshalClassifyDataset(m)
	if err != nil {
		return err
	}
	if err := c.store.SetSetting(keyClassifyData, string(canon)); err != nil {
		return err
	}
	// Rebuild the runtime from the store (now holding the new dataset) so the live
	// classifier picks it up immediately.
	return c.applyConfig(c.loadBuildConfig(c.cfg.Build))
}

// --- editor model (§6.8) ---

// classifyEntry mirrors one pbc-classification dataset entry as plain strings, so
// the editor round-trips the JSON without depending on the library's internal
// (un)marshaling of its enum types. The json tags match the library exactly (it
// decodes with DisallowUnknownFields), and the library validates the result.
type classifyEntry struct {
	Trust    string           `json:"trust,omitempty"`
	Verified string           `json:"verified,omitempty"`
	Signals  *classifySignals `json:"signals,omitempty"`
	Evidence string           `json:"evidence,omitempty"`
	Note     string           `json:"note,omitempty"`
}

// classifySignals mirrors the library's Signals (string enums). A nil *Signals
// omits the key entirely so an entry with no signals stays clean.
type classifySignals struct {
	AdTrackingCookies string `json:"adTrackingCookies,omitempty"`
	HonorsGPC         string `json:"honorsGPC,omitempty"`
	AdsTrackers       string `json:"adsTrackers,omitempty"`
	ThirdPartyScripts string `json:"thirdPartyScripts,omitempty"`
	Fingerprinting    string `json:"fingerprinting,omitempty"`
	SessionReplay     string `json:"sessionReplay,omitempty"`
	SellsSharesData   string `json:"sellsSharesData,omitempty"`
	ThirdPartyDomains *int   `json:"thirdPartyDomains,omitempty"`
}

// Enum option lists for the editor's <select>s (first value = default/unset).
var (
	trustOptions   = []string{"unknown", "imported", "audited", "own"}
	ternaryOptions = []string{"unknown", "no", "yes"}
	levelOptions   = []string{"unknown", "none", "low", "high"}
)

// parseClassifyDataset unmarshals raw domains.json bytes into the editor model.
// Empty input yields an empty (non-nil) map.
func parseClassifyDataset(b []byte) (map[string]classifyEntry, error) {
	m := map[string]classifyEntry{}
	if strings.TrimSpace(string(b)) == "" {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// marshalClassifyDataset renders the editor model back to pretty, deterministic
// domains.json bytes (map keys are sorted by encoding/json). An empty map yields
// empty bytes so the override is cleared rather than stored as "{}".
func marshalClassifyDataset(m map[string]classifyEntry) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// classifyGrade is the live grade the editor shows for one domain (returned by the
// preview endpoint and consumed by classify.js).
type classifyGrade struct {
	Grade   string   `json:"grade"` // letter A–F or "?"
	Name    string   `json:"name"`
	Class   string   `json:"class"` // admin grade CSS class, e.g. "grade-f"
	Reasons []string `json:"reasons,omitempty"`
}

// classifyGrades computes the grade of every domain in a candidate dataset using a
// throwaway classifier that merges it over the library defaults (so the editor's
// live preview matches what a build would produce). Returns a validation error if
// the dataset does not parse.
func (c *Creator) classifyGrades(b []byte) (map[string]classifyGrade, error) {
	m, err := parseClassifyDataset(b)
	if err != nil {
		return nil, err
	}
	copts := []classify.Option{classify.WithFirstParty(c.state().build.FirstParty...)}
	if len(strings.TrimSpace(string(b))) > 0 {
		copts = append(copts, classify.WithDataBytes(b))
	}
	cl, err := classify.New(copts...)
	if err != nil {
		return nil, err
	}
	out := make(map[string]classifyGrade, len(m))
	for d := range m {
		g := cl.Classify("https://" + d + "/")
		out[d] = classifyGrade{
			Grade: g.Grade.Letter(), Name: g.Grade.Name(),
			Class: gradeClass(g.Grade.Letter()), Reasons: g.Reasons,
		}
	}
	return out, nil
}

// --- editor HTTP handlers (§6.8) ---

// handleClassification renders the dataset editor with the stored dataset.
func (c *Creator) handleClassification(w http.ResponseWriter, r *http.Request) {
	c.renderClassification(w, http.StatusOK, "", "", string(c.storedClassifyData()))
}

// renderClassification renders the editor page. raw is the dataset JSON to show
// (the stored value on GET, or the just-submitted value when redisplaying an error).
func (c *Creator) renderClassification(w http.ResponseWriter, code int, notice, errMsg, raw string) {
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	c.render(w, "classification", map[string]any{
		"CSRF": c.csrf, "Raw": raw, "Notice": notice, "Error": errMsg,
	})
}

// handleSaveClassification validates and persists the posted dataset (the hidden
// "dataset" field the structured editor keeps in sync with, or the raw textarea).
func (c *Creator) handleSaveClassification(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	raw := strings.TrimSpace(r.FormValue("dataset"))
	if err := c.saveClassifyData([]byte(raw)); err != nil {
		c.renderClassification(w, http.StatusBadRequest, "", err.Error(), raw)
		return
	}
	// Re-render from the canonicalized stored value so the editor reflects it.
	c.renderClassification(w, http.StatusOK, "Classification dataset saved.", "", string(c.storedClassifyData()))
}

// handleClassificationPreview validates the posted candidate dataset and returns
// each domain's live grade as JSON. No state change, so it is CSRF-exempt like the
// page/scan previews.
func (c *Creator) handleClassificationPreview(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.FormValue("dataset"))
	resp := struct {
		OK     bool                     `json:"ok"`
		Error  string                   `json:"error,omitempty"`
		Grades map[string]classifyGrade `json:"grades,omitempty"`
	}{}
	if err := validateClassifyData([]byte(raw)); err != nil {
		resp.Error = err.Error()
	} else if grades, err := c.classifyGrades([]byte(raw)); err != nil {
		resp.Error = err.Error()
	} else {
		resp.OK, resp.Grades = true, grades
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleClassificationExport downloads the current custom dataset as domains.json.
// When no custom dataset is set it returns an empty object so the operator gets a
// valid file to start from.
func (c *Creator) handleClassificationExport(w http.ResponseWriter, r *http.Request) {
	data := c.storedClassifyData()
	if len(data) == 0 {
		data = []byte("{}\n")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="domains.json"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// handleClassificationImport reads an uploaded domains.json and, if valid, replaces
// the custom dataset with it (validated + canonicalized + hot-applied via
// saveClassifyData). A rejected file leaves the stored dataset untouched and is
// shown back in the editor so the operator can fix it.
func (c *Creator) handleClassificationImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 5<<20) // datasets are small; cap the upload
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		c.renderClassification(w, http.StatusBadRequest, "", "Upload too large or malformed.", string(c.storedClassifyData()))
		return
	}
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		c.renderClassification(w, http.StatusBadRequest, "", "No file uploaded.", string(c.storedClassifyData()))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		c.renderClassification(w, http.StatusBadRequest, "", "Reading the upload failed: "+err.Error(), string(c.storedClassifyData()))
		return
	}
	if err := c.saveClassifyData(data); err != nil {
		// Keep the current dataset; show the rejected upload so it can be corrected.
		c.renderClassification(w, http.StatusBadRequest, "", "Import rejected — "+err.Error(), string(data))
		return
	}
	c.renderClassification(w, http.StatusOK, "Imported classification dataset.", "", string(c.storedClassifyData()))
}
