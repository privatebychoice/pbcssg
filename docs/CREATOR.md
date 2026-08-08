# pbcssg Creator (Editor) — Authoring Guide

`pbcssg creator` is the local, single-operator, browser-based admin for authoring
content, managing media, previewing through the real pipeline, publishing, and
building. It binds **loopback only**. The same editor also runs on `pbcssg server`'s
loopback admin listener in a unified launch (`-admin-addr`, on its own port, fronted by
the TLS proxy on a dedicated admin origin — §7.9); either way it is the only surface that
opens the SQLite editing store — never the public listener. The admin is itself self-hosted — no CDN or third-party resources — and every
state-changing action is CSRF-protected.

```bash
pbcssg creator -db content.db -base https://example.com -site "My Site"
#   → open http://127.0.0.1:8080
```

Settings you enter in the editor are stored in the database and override the CLI
seed flags on the next run. See the [README](../README.md) for install/flags and
the [SPEC](SPEC.md) for the authoritative design.

---

## The save → publish → generate flow

Three distinct steps, in order:

1. **Save** stores your draft (a new revision). Drafts never affect the built site.
2. **Publish** makes the saved revision *live* — the version the build reads. It
   runs the **pre-publish gate** first (below).
3. **Generate site** (top of the page) rebuilds the immutable static bundle from
   every published page. Draft edits — and newly published changes — don't reach
   the built bundle until you Generate.

**Package release** does a build and writes a versioned tarball for deployment;
copying it to the host stays your manual step (the editor never touches production).

**Publish live** appears only in a **unified launch** — when the editor is mounted on
`pbcssg server`'s admin listener (`-admin-addr` + `-db`, on the proxied admin origin; see
the README). It builds an immutable versioned release directory, atomically repoints the
server's `current` symlink to it, and reloads the public listener **in-process** — the
site goes live with no restart. Use it when you author on the host itself; a standalone
laptop editor uses **Package release** + the manual copy instead. The public site keeps
serving the previous bundle if a build fails, so a bad Publish never takes it down.

---

## Screens

### Dashboard — page tree
The nested page tree (by parent): title, path, draft/published status, last
updated, and a worst-privacy-grade badge from the last build/preview. Actions:
New page, Edit, Preview, Publish, Delete, plus **Generate site**, **Package
release**, and — in a unified launch — **Publish live**.

### Page editor
- **Title / path / slug / parent / tags / keywords / summary.**
- **Page flags:** *This is a post/article* (enables reading time + related-posts),
  *This is an index page* (lets a Page-index block list children), *Exclude from
  page-index listings*, *Hide from search engines (noindex)*, and *Unlisted (hidden
  page)*.
- **Unlisted (hidden page)** removes the page from **every** generated listing and
  manifest — search, sitemap, page-index, related-posts, tags, feeds, and the
  published privacy manifest (it implies noindex) — while still serving it and
  keeping its own External References section for members. A **Random suffix**
  button beside the Path field appends a high-entropy token so the URL becomes an
  unguessable capability; combine that with gated blocks for a members area that
  can't be enumerated or guessed and whose content is encrypted (SPEC §6.16).
- **Social preview image** (`og:image`) — an optional `/media/…` path; falls back
  to the Settings site default.
- **Body** — a Markdown textarea with **live preview** rendered by the real
  server-side pipeline, so hygiene, badges, and block output match the build.
- **Blocks** — add / reorder / remove content blocks (see the block list in the
  [README](../README.md#content-blocks)). Each block has its own fields; gateable
  blocks carry a *Members-only groups* field.
- **Live privacy annotations** — external links/images in the preview show their
  classification badge (grade + name + reason) via the `pbc-classification` model.

### Publish
Publishing runs the gate over the draft's external references and surfaces every
`D`/`F`/`?` link for you to acknowledge; consent-gated embeds are pre-acknowledged.
On acknowledge, the draft becomes the live revision.

### Media library
- **Upload** images (BLOB-stored) and audio/video (filesystem-backed). Every file
  is **metadata-stripped on ingest** (EXIF/GPS/XMP/IPTC for images, container tags
  for A/V; SVGs sanitized by a deny-by-default allowlist). Formats that can't be
  fully scrubbed are rejected.
- Each item has a **note** (used as the alt text for by-tag galleries) and
  free-form **tags** (used to gather by-tag galleries). Content-addressed paths
  (`/media/<sha>.<ext>`) are identical in preview and build.

### Favicons
A dedicated panel to upload the favicon set (SVG, ICO, apple-touch, PWA 192/512)
and a theme colour. The build emits them at the canonical root paths and generates
`site.webmanifest`; `<head>` links are injected only for the assets present.

### Error pages
Edit the themed error pages (`404` / `403` / `429` / `50x`, plus `400`) the build
emits at the site root (SPEC §7.8). Each has a **Markdown** box, pre-populated with
a sensible default; leave a box blank to keep the default. The pages reuse the site
theme and include a link home, are `noindex`, and are excluded from the sitemap and
search index. Wire your reverse proxy's `error_page` to them (see the
[README](../README.md#error-pages)); pbcssg emits only the HTML bodies — the proxy
sets the status.

### Key groups
Create/rename/rotate/delete named **key groups** and copy their **gate link**.
Opening a gate link once deposits that group's key into the visitor's browser
keyring, unlocking every block gated to that group across the whole site. Each
group can point at a splash page, or fall back to a generic `/unlock/<alias>`
page. A "Local Test" link tests the gate against a loopback build.

### Settings
Site name, base URL, first-party domains, language, GPC `lastUpdate`, build
number; search (+ full-text); SEO (Open Graph, default og:image, sitemap +
robots.txt); posts (reading-time toggle); **Metrics** (opt-in private dashboard —
see below); `security.txt` (Contact/Expires/…); embed-host allowlist; nav +
footer nav; syndication feeds; header brand/logo (with an optional **dark-mode
logo** that auto-swaps with the theme — OS preference or the footer toggle; leave
blank to use one logo for both); body font; **theme editing**
(CSS-variable form + optional custom CSS, validated to forbid external
`url()`/`@import`); **Releases** (*Keep releases* — how many versioned release
directories a unified **Publish live** keeps on the host, default 3, `0` = keep all;
the live release is never removed); and the custom classification dataset.

### Metrics dashboard (opt-in)
The **Metrics** toggle opts the built bundle into server mode's private,
loopback-only metrics. It changes no page output; it lets `pbcssg server` — when run
with the admin listener (`-admin-addr`) — record **aggregate** counters (request
volume, status classes, top pages, a coarse browser/bot split, and a **`/16` network
heat map**), shown read-only on the editor's **Metrics** admin page (`/admin/metrics`,
in the nav when enabled). Reach it on the admin origin (or an SSH tunnel to the loopback
port); `/admin/metrics/metrics.json` gives the raw counters for `wget`. It is served only
on the admin listener, never the public origin. No client IP is stored — addresses are
reduced to a `/16` and
discarded — and the data is in-memory only (it resets on restart). Behind a reverse
proxy, set the **Trusted proxies** CIDR allowlist (in this Metrics section) so the real
client `/16` is read from a trusted header rather than a spoofable one. Metrics settings
take effect after a rebuild + server restart.

---

## Security

- **Loopback bind on a separate port, proxied on its own origin.** Standalone
  `pbcssg creator` binds loopback and is reached directly. In a unified
  `pbcssg server`, the admin listener binds loopback on its own port and is fronted
  by the TLS proxy on a **dedicated admin origin**, distinct from the public origin
  and port, restricted by an **IP allowlist and/or firewall** (SPEC §7.9).
  Host/SSH access is an operations channel, not the admin auth.
- **Creator passkey sign-in (WebAuthn).** Enabled with `-app-db` + `-admin-origin`
  on `pbcssg server`. Bootstrap the first creator with `pbcssg admin bootstrap`
  (prints a one-time invite), then **register** a passkey at `/admin/register` and
  **sign in** at `/admin/login`; **Sign out** is in the nav. Sessions are a first-
  party `__Host-`, `HttpOnly`, `SameSite=Strict` cookie (a plain-named cookie over
  `http://localhost` for local dev), carrying only an opaque token stored **hashed**.
  When enabled, every editor route requires a live creator session (banned or
  non-creator sessions are refused); the ceremony pages, assets, and logout stay
  open so you can sign in. Standalone `pbcssg creator` (no `-app-db`) has no accounts
  and relies on its network controls only. See SPEC §2.4 and
  `docs/SESSION-COOKIES-STORAGE.md`.
- **CSRF tokens** on every state-changing POST — a form field for page actions, an
  `X-CSRF-Token` header for the JSON ceremony endpoints (a malicious page in your
  browser could otherwise reach the admin origin).
- **Self-hosted admin assets**, no directory listing. Uploaded SVGs are served
  sandboxed (isolated `<img>` + CSP) as defense-in-depth over sanitization.
