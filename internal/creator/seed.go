package creator

import (
	"go.privatebychoice.com/pbcssg/internal/render"
	"go.privatebychoice.com/pbcssg/internal/store"
)

// defaultPage is one first-run starter page.
type defaultPage struct {
	path, slug, title, summary, body string
	tags                             []string
}

// defaultPages are the starter routes seeded once, right after a database is
// created: / (Home), /about, and /privacy. They are normal, editable pages —
// created as drafts, so the operator reviews and publishes each (especially the
// Privacy Policy) when ready. The Privacy Policy carries a "privacy" tag so that,
// once published, /tags has at least one entry (it 404s with no tagged pages).
var defaultPages = []defaultPage{
	{path: "/", slug: "home", title: "Home", summary: "Welcome to your new privacy-first site.", body: indexBody},
	{path: "/about", slug: "about", title: "About", summary: "About this site.", body: aboutBody},
	{path: "/privacy", slug: "privacy", title: "Privacy Policy", summary: "How this site handles your data and privacy.", body: privacyBody, tags: []string{"privacy"}},
}

// Default navigation seeded on first run (raw "Label | /path" per line, the same
// form the Settings textareas store). They reference the seeded pages plus the
// always-emitted engine pages (/tags, /classification); footer links to the
// draft pages resolve once the operator publishes them. All are editable in
// Settings.
const (
	defaultNav       = "Home | /"
	defaultFooterNav = "Privacy | /privacy\nClassification | /classification\nAbout | /about\nTags | /tags"
)

// SeedDefaults populates a database the first time an editor opens it: the
// starter pages (drafts) plus safe default primary/footer navigation. It records
// a marker (keySeeded) so later launches skip it — a no-op once the marker is
// set, safe to call on every editor launch.
//
// It is invoked only by the `creator` command (and before New, so loadBuildConfig
// picks up the seeded nav on the first launch), never by New itself, so the test
// suite (which constructs Creator directly on fresh databases and asserts empty
// state) is unaffected. Seeding is non-destructive: pages are drafts, nothing is
// published. Returns the number of pages created (0 when already seeded).
func SeedDefaults(st *store.Store) (int, error) {
	if v, ok, err := st.Setting(keySeeded); err != nil {
		return 0, err
	} else if ok && v == "1" {
		return 0, nil
	}
	n := 0
	for _, p := range defaultPages {
		pid, err := st.CreatePage(store.Page{Path: p.path, Slug: p.slug, Title: p.title})
		if err != nil {
			return n, err
		}
		cj, err := contentJSON(render.Content{Body: p.body, Summary: p.summary, Tags: p.tags})
		if err != nil {
			return n, err
		}
		if _, err := st.SaveRevision(pid, cj, ""); err != nil {
			return n, err
		}
		n++
	}
	for _, kv := range []struct{ key, val string }{
		{keyNav, defaultNav},
		{keyFooterNav, defaultFooterNav},
	} {
		if err := st.SetSetting(kv.key, kv.val); err != nil {
			return n, err
		}
	}
	if err := st.SetSetting(keySeeded, "1"); err != nil {
		return n, err
	}
	return n, nil
}

const indexBody = `# Welcome

This is your site's home page. Edit it in the editor (Pages → Home), then
publish it when you're ready.

Everything here is self-hosted and privacy-first: no third-party scripts, no
tracking, and no cookies you didn't ask for.
`

const aboutBody = `# About

Tell visitors who you are and what this site is for. Replace this text in the
editor (Pages → About), then publish the page.
`

const privacyBody = `# Privacy Policy

_Last updated: [add a date]_

**This is a starter template — review and customise it for your site before you
publish it.**

## What we collect

This site is static and self-hosted. It sets no tracking cookies, loads no
third-party analytics or advertising, and does not sell or share personal
information.

## Cookies and local storage

This site uses **no tracking cookies**, no third-party storage, and no
fingerprinting. It stores information on your device only when it is *strictly
necessary* for a feature you actively use — never for tracking or profiling.
Depending on which features this site has enabled, that may include:

- **Unlocking protected content.** If you unlock a protected page (with a code or
  a private link), the key is kept in your browser so you don't have to re-enter
  it. This key stays on your device and is never sent to us.
- **Signing in.** If this site offers member accounts and you sign in, we set a
  single first-party session cookie so you stay signed in. It holds only an
  opaque session identifier — no personal data — is never used for tracking or
  shared with anyone, and is cleared when you sign out.

Because these are strictly necessary for features you request, they do not
require a consent banner under EU/UK e-privacy rules; we describe them here for
transparency. Remove any bullet above that does not apply to your site.

## Global Privacy Control (GPC)

We support Global Privacy Control (https://globalprivacycontrol.org/). Because we
never sell or share personal data, there is nothing to switch off — but the
signal is respected, and its absence is never treated as consent. Our declaration
is served at /.well-known/gpc.json.

## Embedded content

Some pages may offer consent-gated embeds (for example, video). Nothing
third-party loads until you explicitly click to load it.

## Contact

[Add your contact details here.]
`
