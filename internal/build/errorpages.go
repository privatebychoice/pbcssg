package build

import (
	"fmt"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/hygiene"
	"go.privatebychoice.com/pbcssg/internal/render"
)

// ErrorPage defines one themed error page emitted at the bundle root (SPEC §7.8).
// Name is the emitted file base (<Name>.html) and the settings-key suffix; Codes
// is the HTTP status set it is intended for (documentation / editor hint only,
// used by the operator when wiring the front-end's error_page directives).
type ErrorPage struct {
	Name    string
	Title   string
	Codes   string
	Default string // default Markdown message
}

// ErrorPages is the curated, fixed set of error pages the build emits. Status
// codes with no response body (1xx, 204, 205, 304) are intentionally excluded.
var ErrorPages = []ErrorPage{
	{
		Name: "400", Title: "Bad request", Codes: "400",
		Default: "# Bad request\n\nYour browser sent a request this site couldn't understand.\n\n[← Return home](/)\n",
	},
	{
		Name: "403", Title: "Access denied", Codes: "403",
		Default: "# Access denied\n\nYou don't have permission to view this page.\n\n[← Return home](/)\n",
	},
	{
		Name: "404", Title: "Page not found", Codes: "404",
		Default: "# Page not found\n\nThe page you're looking for doesn't exist or may have moved.\n\n[← Return home](/)\n",
	},
	{
		Name: "429", Title: "Too many requests", Codes: "429",
		Default: "# Slow down a moment\n\nYou've sent a lot of requests in a short time. Please wait a little and try again.\n\n[← Return home](/)\n",
	},
	{
		Name: "50x", Title: "Something went wrong", Codes: "500, 502, 503, 504",
		Default: "# Something went wrong\n\nThe site hit an unexpected problem. Please try again in a little while.\n\n[← Return home](/)\n",
	},
}

// errorPageMessage returns the operator's Markdown for an error page, or its
// built-in default when unset or blank — so a headless `pbcssg build` always emits
// a complete page even before the editor has been used.
func (c Config) errorPageMessage(ep ErrorPage) string {
	if m, ok := c.ErrorPages[ep.Name]; ok && strings.TrimSpace(m) != "" {
		return m
	}
	return ep.Default
}

// emitErrorPages renders each themed error page (SPEC §7.8) and writes it to the
// bundle root as <name>.html. The pages reuse the site layout and theme, are
// noindex, and are not page-tree pages — so they never enter the sitemap or search
// index. Assets are referenced root-absolutely (via the shared page options), so a
// page served under any requested URL still styles correctly.
func (b *builder) emitErrorPages() error {
	for _, ep := range ErrorPages {
		opts := b.pageOpts(ep.Title, "/"+ep.Name+".html")
		// Error pages are noindex and are not canonical / Open Graph targets.
		opts.CanonicalURL = ""
		opts.OpenGraph = false
		opts.OGImage = ""

		content := render.Content{Body: b.cfg.errorPageMessage(ep), NoIndex: true}
		rendered, err := render.RenderContent(content, opts)
		if err != nil {
			return fmt.Errorf("build: render error page %s: %w", ep.Name, err)
		}
		// Same safe-mode hygiene as page bodies (first-party operator Markdown).
		hres, err := hygiene.Apply(rendered.HTML, b.hcfg)
		if err != nil {
			return fmt.Errorf("build: hygiene error page %s: %w", ep.Name, err)
		}
		// Error pages carry no external-references listing; remove the layout slot.
		final := b.injectExtRefList(hres.HTML, nil)
		if err := b.write(ep.Name+".html", final); err != nil {
			return err
		}
	}
	return nil
}
