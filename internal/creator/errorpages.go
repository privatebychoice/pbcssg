package creator

import (
	"net/http"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/build"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// errorPageKey is the settings key holding the operator's Markdown for one themed
// error page (SPEC §7.8), e.g. "errorpage.404".
func errorPageKey(name string) string { return "errorpage." + name }

// loadErrorPages reads the stored per-page error-page messages into the map the
// build overlays onto its defaults. Only explicitly-set (non-blank) messages are
// returned; the build falls back to its built-in default for the rest.
func loadErrorPages(st *store.Store) map[string]string {
	out := map[string]string{}
	for _, ep := range build.ErrorPages {
		if v, ok, err := st.Setting(errorPageKey(ep.Name)); err == nil && ok && strings.TrimSpace(v) != "" {
			out[ep.Name] = v
		}
	}
	return out
}

// errorPageView is one row in the Error pages editor: the stored message, or the
// built-in default when the operator has not set one.
type errorPageView struct {
	Name    string
	Title   string
	Codes   string
	Message string
}

func (c *Creator) errorPageViews() []errorPageView {
	views := make([]errorPageView, 0, len(build.ErrorPages))
	for _, ep := range build.ErrorPages {
		msg := ep.Default
		if v, ok, err := c.store.Setting(errorPageKey(ep.Name)); err == nil && ok && strings.TrimSpace(v) != "" {
			msg = v
		}
		views = append(views, errorPageView{Name: ep.Name, Title: ep.Title, Codes: ep.Codes, Message: msg})
	}
	return views
}

// handleErrorPages renders the Error pages editor.
func (c *Creator) handleErrorPages(w http.ResponseWriter, r *http.Request) {
	c.renderErrorPages(w, http.StatusOK, "")
}

func (c *Creator) renderErrorPages(w http.ResponseWriter, code int, notice string) {
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	c.render(w, "errorpages", map[string]any{
		"CSRF": c.csrf, "Pages": c.errorPageViews(), "Notice": notice,
	})
}

// handleSaveErrorPages persists each page's message. A blank box is stored as
// empty, which the build (and this editor) treat as "use the built-in default".
func (c *Creator) handleSaveErrorPages(w http.ResponseWriter, r *http.Request) {
	if !c.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	for _, ep := range build.ErrorPages {
		msg := strings.TrimSpace(r.FormValue("msg_" + ep.Name))
		if err := c.store.SetSetting(errorPageKey(ep.Name), msg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	c.renderErrorPages(w, http.StatusOK, "Error pages saved.")
}
