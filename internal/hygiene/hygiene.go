// Package hygiene applies pbcssg's linking-hygiene and auto-rewrite rules to a
// page's rendered HTML (SPEC §5.5, §5.6). It:
//
//   - rewrites known-unsafe embeds to safer equivalents via an explicit,
//     auditable registry (default: youtube.com → youtube-nocookie.com);
//   - adds rel="noopener noreferrer" and a referrer policy to external anchors;
//   - lazy-loads external iframes/embeds/images (loading="lazy" — the minimum
//     "facade" per embed-privacy-tips; the full click-to-load JS facade is the
//     build engine / youtube-fieldblock layer's job, §5.8);
//   - strips third-party favicons and preconnect/dns-prefetch hints; and
//   - warns about residual constructs that still contact a third party on load
//     (third-party scripts and stylesheets).
//
// Every change is deterministic and recorded — no silent "fixing" beyond the
// declared transforms. First-party (same-origin, or operator-owned via the
// FirstParty predicate) references are left untouched.
package hygiene

import (
	"bytes"
	"net/url"
	"sort"
	"strings"

	"go.privatebychoice.com/pbcssg/internal/linkscan"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Rewrite is one domain-swap rule. From matches a host exactly or as a parent
// domain (so youtube.com matches www.youtube.com); the matched From suffix is
// replaced with To, preserving any subdomain.
type Rewrite struct {
	From string
	To   string
	Note string
}

// DefaultRewrites is the built-in rewrite registry.
var DefaultRewrites = []Rewrite{
	{From: "youtube.com", To: "youtube-nocookie.com", Note: "privacy-enhanced YouTube embed domain"},
}

// Config tunes the transform.
type Config struct {
	Base           *url.URL               // page base URL, to tell same-origin from third-party
	FirstParty     func(host string) bool // optional: operator-owned hosts to treat as first-party
	ReferrerPolicy string                 // applied to external refs; default "no-referrer"
	Rewrites       []Rewrite              // domain rewrites; nil uses DefaultRewrites
}

// Change records one applied transform.
type Change struct {
	Kind    string // "rewrite" | "rel" | "referrerpolicy" | "lazy" | "remove-favicon" | "remove-preconnect"
	Element string
	Detail  string
}

// Warning records a residual third-party construct that hygiene cannot neutralize.
type Warning struct {
	Element string
	Host    string
	Message string
}

// Result is the transformed HTML plus the log of what changed and what to warn about.
type Result struct {
	HTML     []byte
	Changes  []Change
	Warnings []Warning
}

// Apply parses in as HTML, applies the hygiene/rewrite rules, and returns the
// re-rendered HTML with a change log and warnings.
func Apply(in []byte, cfg Config) (Result, error) {
	if cfg.ReferrerPolicy == "" {
		cfg.ReferrerPolicy = "no-referrer"
	}
	if cfg.Rewrites == nil {
		cfg.Rewrites = DefaultRewrites
	}

	doc, err := html.Parse(bytes.NewReader(in))
	if err != nil {
		return Result{}, err
	}

	res := &Result{}
	harden(doc, cfg, res)

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return Result{}, err
	}
	res.HTML = buf.Bytes()
	return *res, nil
}

// ApplyFragment applies the same hygiene rules to an HTML *fragment* (e.g. a
// reveal block's rendered-markdown body, SPEC §6.9) rather than a whole document.
// It parses in a body context, so no <html>/<head>/<body> wrapper is added, and
// serializes just the fragment nodes back out. Used to harden external
// links/images inside deferred-reveal content before it is encrypted.
func ApplyFragment(in []byte, cfg Config) (Result, error) {
	if cfg.ReferrerPolicy == "" {
		cfg.ReferrerPolicy = "no-referrer"
	}
	if cfg.Rewrites == nil {
		cfg.Rewrites = DefaultRewrites
	}

	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(bytes.NewReader(in), body)
	if err != nil {
		return Result{}, err
	}
	for _, n := range nodes {
		body.AppendChild(n)
	}

	res := &Result{}
	harden(body, cfg, res)

	var buf bytes.Buffer
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&buf, c); err != nil {
			return Result{}, err
		}
	}
	res.HTML = buf.Bytes()
	return *res, nil
}

// harden walks the tree rooted at root, applying process() to every element and
// removing those it drops (dropping is deferred so the walk isn't disturbed).
func harden(root *html.Node, cfg Config, res *Result) {
	var remove []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if drop := process(n, cfg, res); drop {
				remove = append(remove, n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	for _, n := range remove {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
	}
}

// process applies the rules to a single element. It returns true if the element
// should be removed from the document.
func process(n *html.Node, cfg Config, res *Result) (drop bool) {
	switch strings.ToLower(n.Data) {
	case "a":
		hardenAnchor(n, cfg, res)
	case "iframe", "embed":
		src := "src"
		hardenEmbed(n, src, cfg, res)
	case "img":
		if isThirdParty(getAttr(n, "src"), cfg) {
			if setIfAbsent(n, "loading", "lazy") {
				res.Changes = append(res.Changes, Change{"lazy", n.Data, "loading=lazy"})
			}
			if setIfAbsent(n, "referrerpolicy", cfg.ReferrerPolicy) {
				res.Changes = append(res.Changes, Change{"referrerpolicy", n.Data, cfg.ReferrerPolicy})
			}
		}
	case "script":
		if host := hostOf(getAttr(n, "src"), cfg); host != "" && isThirdPartyHost(host, cfg) {
			res.Warnings = append(res.Warnings, Warning{"script", host, "third-party script runs on page load"})
		}
	case "link":
		return processLink(n, cfg, res)
	}
	return false
}

func hardenAnchor(n *html.Node, cfg Config, res *Result) {
	if !isThirdParty(getAttr(n, "href"), cfg) {
		return
	}
	if addRelTokens(n, "noopener", "noreferrer") {
		res.Changes = append(res.Changes, Change{"rel", "a", "noopener noreferrer"})
	}
	if setIfAbsent(n, "referrerpolicy", cfg.ReferrerPolicy) {
		res.Changes = append(res.Changes, Change{"referrerpolicy", "a", cfg.ReferrerPolicy})
	}
}

func hardenEmbed(n *html.Node, attrName string, cfg Config, res *Result) {
	raw := getAttr(n, attrName)
	if raw == "" {
		return
	}
	// Auto-rewrite the embed domain first (e.g. youtube.com -> youtube-nocookie.com).
	if newURL, rw, ok := rewriteURL(raw, cfg); ok {
		setAttr(n, attrName, newURL)
		raw = newURL
		res.Changes = append(res.Changes, Change{"rewrite", n.Data, rw.From + " -> " + rw.To})
	}
	if isThirdParty(raw, cfg) {
		if setIfAbsent(n, "loading", "lazy") {
			res.Changes = append(res.Changes, Change{"lazy", n.Data, "loading=lazy"})
		}
		if setIfAbsent(n, "referrerpolicy", cfg.ReferrerPolicy) {
			res.Changes = append(res.Changes, Change{"referrerpolicy", n.Data, cfg.ReferrerPolicy})
		}
	}
}

func processLink(n *html.Node, cfg Config, res *Result) (drop bool) {
	href := getAttr(n, "href")
	host := hostOf(href, cfg)
	if host == "" || !isThirdPartyHost(host, cfg) {
		return false
	}
	rels := strings.Fields(strings.ToLower(getAttr(n, "rel")))
	for _, rel := range rels {
		switch rel {
		case "icon", "shortcut", "apple-touch-icon", "apple-touch-icon-precomposed", "mask-icon":
			res.Changes = append(res.Changes, Change{"remove-favicon", "link", host})
			return true
		case "preconnect", "dns-prefetch":
			res.Changes = append(res.Changes, Change{"remove-preconnect", "link", host})
			return true
		case "stylesheet":
			res.Warnings = append(res.Warnings, Warning{"link", host, "third-party stylesheet loads on page load"})
		}
	}
	return false
}

// rewriteURL applies the first matching rewrite rule to raw, returning the new
// URL. Matching is on the resolved host; the subdomain is preserved.
func rewriteURL(raw string, cfg Config) (string, Rewrite, bool) {
	resolved, host := linkscan.Resolve(raw, cfg.Base)
	if host == "" {
		return raw, Rewrite{}, false
	}
	for _, rule := range cfg.Rewrites {
		if host == rule.From || strings.HasSuffix(host, "."+rule.From) {
			newHost := strings.TrimSuffix(host, rule.From) + rule.To
			u, err := url.Parse(resolved)
			if err != nil {
				return raw, Rewrite{}, false
			}
			if port := u.Port(); port != "" {
				u.Host = newHost + ":" + port
			} else {
				u.Host = newHost
			}
			return u.String(), rule, true
		}
	}
	return raw, Rewrite{}, false
}

// isThirdParty reports whether raw resolves to a third-party network host.
func isThirdParty(raw string, cfg Config) bool {
	host := hostOf(raw, cfg)
	return host != "" && isThirdPartyHost(host, cfg)
}

func hostOf(raw string, cfg Config) string {
	if raw == "" {
		return ""
	}
	_, host := linkscan.Resolve(raw, cfg.Base)
	return host
}

// isThirdPartyHost reports whether host is neither same-origin nor operator-owned.
func isThirdPartyHost(host string, cfg Config) bool {
	if cfg.Base != nil && strings.EqualFold(host, cfg.Base.Hostname()) {
		return false
	}
	if cfg.FirstParty != nil && cfg.FirstParty(host) {
		return false
	}
	return true
}

// --- attribute helpers ---

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func setAttr(n *html.Node, key, val string) {
	for i := range n.Attr {
		if strings.EqualFold(n.Attr[i].Key, key) {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// setIfAbsent sets key=val only if key is absent, returning true if it set it.
func setIfAbsent(n *html.Node, key, val string) bool {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return false
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
	return true
}

// addRelTokens ensures the rel attribute contains each token, returning true if
// it changed anything.
func addRelTokens(n *html.Node, tokens ...string) bool {
	existing := strings.Fields(getAttr(n, "rel"))
	have := map[string]bool{}
	for _, t := range existing {
		have[strings.ToLower(t)] = true
	}
	added := false
	for _, t := range tokens {
		if !have[strings.ToLower(t)] {
			existing = append(existing, t)
			have[strings.ToLower(t)] = true
			added = true
		}
	}
	if added {
		sort.Strings(existing)
		setAttr(n, "rel", strings.Join(existing, " "))
	}
	return added
}
