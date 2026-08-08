# pbcssg

A privacy-first static site generator. `pbcssg` has three modes in one binary:

- **creator** — a local, loopback-only web editor (Wagtail-style) to author
  content, manage media, preview through the real pipeline, publish, and build.
- **build** — turns published content into an immutable static bundle.
- **server** — serves a built bundle over HTTP, designed to sit behind a
  TLS-terminating reverse proxy. It can optionally bind a second, loopback-only
  **admin listener** (the editor + metrics dashboard) so you author on the host and
  publish live in-process — see [Unified launch](#unified-launch--edit-on-the-host).

Everything is self-hosted by design: no third-party front-end resources, no CDNs,
no analytics or trackers. External references in your content are classified and
you are warned before you publish anything that would leak data. Global Privacy
Control (GPC) is supported out of the box.

---

## Requirements

- **Go 1.26+** (pure Go; no cgo, single cross-compiled binary).
- No system libraries. SQLite is the pure-Go `modernc.org/sqlite` driver.

`pbcssg` depends on two sibling modules published under a vanity import path, so
set `GOPRIVATE` before building from source (this skips the public checksum
database for that path):

```bash
export GOPRIVATE=go.privatebychoice.com/*
```

---

## Install

Install the `pbcssg` binary onto your `PATH` straight from the vanity import
path:

```bash
go install go.privatebychoice.com/pbcssg/cmd/pbcssg@latest
```

Pin a specific release instead of tracking the latest tag by swapping `@latest`
for a version, e.g. `@v0.4.0`. A tagged install populates the build metadata, so
`pbcssg version` reports the real version.

`go install` places the binary in `$(go env GOBIN)`, falling back to
`$(go env GOPATH)/bin` (default `$HOME/go/bin`). Make sure that directory is on
your `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

Then `pbcssg -h` works from anywhere.

---

## Build

```bash
go build -o pbcssg ./cmd/pbcssg
```

For a stripped, reproducible Linux server binary (build on any OS):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o pbcssg ./cmd/pbcssg
```

Run `./pbcssg -h` for usage, or `./pbcssg <subcommand> -h` for a mode's flags.

---

## Quick start

Author locally, build a bundle, then serve it.

```bash
# 1. Start the editor (loopback only). Creates content.db if missing.
./pbcssg creator -db content.db -base https://example.com -site "My Site"
#    → open http://127.0.0.1:8080

# 2. In the editor: create pages, add media, preview, and Publish.
#    Then click "Build site" (or "Package release" for a deployable tarball).

# 3. Serve the built bundle locally to check it.
./pbcssg server -content ./site -addr 127.0.0.1:8083
#    → open http://127.0.0.1:8083
```

You can also build from the command line without the editor:

```bash
./pbcssg build -db content.db -out ./site -base https://example.com \
  -site "My Site" -build 1 -gpc 2026-07-27 -search
```

---

## Content

A page has a **Markdown body** plus an ordered list of **blocks**. Markdown is
rendered with goldmark in safe mode (no raw HTML), with **GFM** (tables,
strikethrough, task lists, autolinks) + **footnotes** and auto heading IDs.

### Content blocks

| Block | What it does |
|-------|--------------|
| **Markdown** | A Markdown fragment (same GFM + footnotes as the body). |
| **Callout** | A note / tip / warning / info admonition with an optional title. |
| **Citation** | A block quotation with an optional source + link. |
| **Code** | A verbatim `<pre><code>` listing — no syntax highlighting (dependency-free), optional filename caption, language label, comment, line numbers, and a self-hosted copy button. |
| **Details / FAQ** | A native `<details>/<summary>` collapsible (visible-but-collapsed, indexable), no JavaScript. |
| **Table of contents** | An auto-generated nested list of the page's `h2`–`h4` headings; headings also get permalink anchors. |
| **Related posts** | Other **posts** sharing the most tags with this page, ranked by tag overlap then recency (internal links only). |
| **Gallery** | A responsive image grid — a manual ordered list *or* every image carrying a chosen media tag — with a CSS-only `:target` lightbox (no JavaScript). |
| **Share** | Privacy-preserving share controls: copy-link, `mailto:`, and a Mastodon-instance intent. No third-party buttons or pixels; nothing loads on view. |
| **Image** | A self-hosted `<figure>` with alt text, optional caption, and float/size layout. |
| **Video / audio** | A self-hosted native `<video>`/`<audio>` player — no third-party player, no external request. |
| **YouTube** | Consent-gated: a self-hosted card that contacts nothing; the embed lives on a separate `/external/youtube/<name>` page behind a click-to-load facade. |
| **Embed** | The same two-stage consent pattern for any provider host you allowlist in Settings. |
| **Page index** | A list of child pages (on a page marked as an index page). |
| **Hidden (reveal)** | Content encrypted at build and absent from the source until the reader acts — **Mode A** obfuscation (click to reveal), **Mode B** code gate (PBKDF2 from a typed code), or **Mode C** members-only (keyring group key, see below). |

**Members-only (group-gated) blocks.** Most blocks — `markdown`, `callout`,
`citation`, `image`, `video/audio`, `code`, `details`, `gallery`, and `index` —
take an optional **groups** list. A gated block is envelope-encrypted at build and
revealed only to a visitor holding a matching **key group** key in their browser
keyring (delivered by a one-time *gate link*, never typed). It's a shared bearer
key, not per-user auth. The `reveal` block does the same via its native Mode C.

### Page & site features

- **Posts.** Mark a page as a post/article to enable a **reading-time** estimate
  (a Settings toggle, ~200 wpm) and **related-posts** blocks.
- **SEO.** Per-page **social-preview image** (`og:image`) with a Settings
  site-default, Open Graph + Twitter card tags, an optional **sitemap.xml +
  robots.txt**, `noindex`, and canonical URLs.
- **Unlisted (hidden) pages.** One flag removes a page from **every** generated
  listing and manifest — search, sitemap, page-index, related-posts, tags, feeds,
  and the published privacy manifest — while still serving it and keeping its
  in-page External References section. Pair it with the editor's **Random suffix**
  (a capability URL) and **gated blocks** for a members area whose URL can't be
  enumerated *or* guessed, and whose content is encrypted.
- **Header brand.** A wordmark, a self-hosted logo, or both. A logo can carry an
  optional **dark-mode variant** that auto-swaps with the theme — honoring both
  the OS preference *and* the footer light/dark toggle (pure CSS, no script).
- **Favicons.** Upload a favicon set (SVG / ICO / apple-touch / PWA icons) in the
  editor; the build emits them at the canonical root paths + a `site.webmanifest`.
- **Error pages.** Themed `404` / `403` / `429` / `50x` pages emitted at the site
  root, reusing the site layout with a link home. Edit each page's Markdown in the
  editor's **Error pages** section (pre-populated with defaults); serve them from
  your reverse proxy's `error_page` (see [Deploy → Error pages](#error-pages)).
- **Media tags.** Tag library items with free-form tags (used by by-tag galleries).
- **`security.txt`.** A Settings section emits `/.well-known/security.txt`
  (RFC 9116) — Contact required, Expires defaulted to a year out.
- **Feeds & nav.** Optional RSS 2.0 + Atom feeds by path glob, plus a configurable
  nav bar and footer nav.

---

## Command reference

### `pbcssg creator` — local editor

Binds **loopback only**; it is the only mode that opens the SQLite editing store.

| Flag        | Default            | Description                                    |
|-------------|--------------------|------------------------------------------------|
| `-db`       | *(required)*       | Content database path (created if missing).    |
| `-addr`     | `127.0.0.1:8080`   | Listen address (keep it on loopback).          |
| `-out`      | `site`             | Build output directory.                        |
| `-releases` | `releases`         | Directory for packaged release tarballs.       |
| `-base`     | `http://localhost` | Site base URL (seed; editable in Settings).    |
| `-site`     | *(empty)*          | Site name (seed; editable in Settings).        |
| `-build`    | `1`                | Build number seed.                             |
| `-gpc`      | *(empty)*          | GPC `lastUpdate` date, `YYYY-MM-DD`.           |
| `-search`   | `false`            | Enable the client-side search index + widget.  |

Settings entered in the editor are stored in the database and override these
seed flags on the next run.

### `pbcssg build` — build a bundle

| Flag               | Default | Description                                          |
|--------------------|---------|------------------------------------------------------|
| `-db`              | *(req)* | Content database path.                               |
| `-out`             | *(req)* | Output bundle directory.                             |
| `-base`            | *(req)* | Site base URL, e.g. `https://example.com`.           |
| `-site`            | *(empty)* | Site name (title/footer).                          |
| `-version`         | `1.0`   | Semantic version.                                    |
| `-build`           | *(empty)* | Build number (increment per deploy).               |
| `-gpc`             | *(empty)* | GPC `lastUpdate` date, `YYYY-MM-DD`.               |
| `-search`          | `false` | Emit the client-side search index + widget.          |
| `-search-fulltext` | `false` | Index full body text (default: headings + summary).  |

The bundle contains the rendered pages, per-page + site privacy manifests,
fingerprinted assets, `.well-known/gpc.json`, and a self-describing `build.json`
(version, build number, per-file content hashes).

### `pbcssg server` — serve a bundle

| Flag               | Default          | Description                                            |
|--------------------|------------------|-------------------------------------------------------|
| `-content`         | *(required)*     | Path to a built bundle (must contain `build.json`).   |
| `-addr`            | `127.0.0.1:8083` | Listen address (bind loopback; put TLS in front).     |
| `-admin-addr`      | *(empty)*        | Loopback address for the admin listener — the editor **and** metrics dashboard in one process; requires `-db`. Empty disables. |
| `-db`              | *(empty)*        | Content database the admin listener opens (required with `-admin-addr`; the public listener never opens it). |

With `-admin-addr` the editor also accepts `-out`/`-releases` (build and release
directories) and `-base`/`-site` seeds (stored Settings win over these).

The server never opens the editing database — it serves only the immutable
bundle. It provides pretty URLs, ETag/`304` handling, correct `Content-Type`,
`nosniff`, a Content-Security-Policy on HTML, immutable caching for fingerprinted
assets, `/.well-known/gpc.json`, and a `/version` endpoint for deploy
verification. It reads `build.json` once at startup, so **restart it after
swapping in a new bundle** — unless you run the unified admin listener (below),
whose **Publish** reloads the new bundle in-process with no restart.

**Private metrics dashboard (opt-in).** When the bundle is built with metrics enabled
(Settings → Metrics) and the server runs the admin listener (`-admin-addr`), the server
records **aggregate** traffic counters — request volume, status classes, top pages, a
coarse browser/bot split, and a **`/16` network heat map** — and the editor shows them
read-only on its **Metrics** admin page (`/admin/metrics`). Reach it on the admin origin
(or an SSH tunnel to the loopback port); it is served only on the admin listener, never
the public origin. `/admin/metrics/metrics.json` gives the raw counters for `wget`.
**No client IP is ever stored** — an address is reduced to its
`/16` (or an off-grid IPv6/private tally) and discarded; the dashboard holds counters,
not events, in memory only (they reset on restart). Behind a reverse proxy, set the
**Trusted proxies** CIDR allowlist in Settings so the real client `/16` is read from a
header you can trust, not spoofed. Metrics settings take effect after a rebuild + server
restart.

### Unified launch — edit on the host

Instead of authoring on your laptop and copying tarballs up, `pbcssg server` can bind a
second, **loopback-bound admin listener** on its own port, hosting the editor (the same
`creator` UI) and, when metrics are on, the Metrics page at `/admin/metrics`:

```bash
pbcssg server \
  -content /srv/pbcssg/current \    # a `current` symlink (see Deploy)
  -addr 127.0.0.1:8083 \            # public listener (behind your TLS proxy)
  -admin-addr 127.0.0.1:8085 \      # admin listener (editor + dashboard), own port
  -db /srv/pbcssg/content.db \      # the authoring database, on the host
  -releases /srv/pbcssg/releases
```

The admin listener binds loopback on its **own port**, distinct from the public port —
so several instances can share a host. In production, front it with your TLS proxy on a
**dedicated admin origin** (e.g. `https://admin.example.com`), restricted by an **IP
allowlist and/or firewall**, and — once creator passkey auth lands — WebAuthn-gated
(spec §2.4, §7.9). For quick host access you can also reach the loopback port over an
SSH tunnel:

```bash
ssh -L 8085:127.0.0.1:8085 host    # then open http://localhost:8085
```

The **public listener never opens the database** and keeps serving the bundle even if
the editor faults (the admin listener runs in its own goroutine). The authoring database
— and any future account data — now lives on the host; protect it with a sealed,
key-in-RAM encrypted volume ([docs/DATA-AT-REST.md](docs/DATA-AT-REST.md), spec §7.9).
The public site keeps serving from the bundle whether or not that volume is unlocked.

**Build vs Publish vs Release** — the editor's three actions:

- **Generate site** (Build) — rebuilds the bundle into the staging `-out` directory. A
  dry run to inspect the output; it does not touch the live site.
- **Publish live** *(unified only)* — builds an immutable `releases/v<ver>-build<n>`
  directory, atomically repoints the `current` symlink to it, and **reloads the public
  listener in-process** — live with no restart. `-content` must be that `current`
  symlink. Rollback is the next Publish (or repoint `current` and restart). Old release
  directories are pruned to a retention count (Settings → Releases → *Keep releases*,
  default 3; 0 = keep all); the live release is never removed.
- **Package release** — writes a versioned `.tar.gz` for the manual tarball-copy deploy
  (below); the path for a standalone `pbcssg creator` on your laptop.

---

## Deploy

`pbcssg` produces a **versioned tarball** of the static bundle. Deployment is a
tarball copy plus an atomic symlink swap — no container layer, just the static
binary and the bundle. The editor packages the tarball; copying it to the host
stays your manual step (the editor never touches production).

### 1. Package a release

In the editor click **Package release** (or `POST /admin/release`). This
auto-increments the build number, builds the bundle, and writes:

```
releases/pbcssg-v<version>-build<n>.tar.gz
```

The tarball carries `build.json`, so `/version` on the live server tells you
exactly which build is running.

### 2. Copy it to the host

```bash
scp releases/pbcssg-v1.0-build2.tar.gz  deploy@host:/srv/pbcssg/incoming/
```

### 3. Unpack and swap `current` atomically

On the host (assuming the layout `/srv/pbcssg/{releases,current}`):

```bash
set -euo pipefail
REL=/srv/pbcssg/releases/1.0-build2
mkdir -p "$REL"
tar -xzf /srv/pbcssg/incoming/pbcssg-v1.0-build2.tar.gz -C "$REL"

# Atomic cutover: create the new symlink then rename it over `current`.
ln -sfn "$REL" /srv/pbcssg/current.new
mv -Tf /srv/pbcssg/current.new /srv/pbcssg/current

# The server caches build.json at startup, so restart it to pick up the swap.
sudo systemctl restart pbcssg

# Verify the running build matches what you shipped.
curl -s http://127.0.0.1:8083/version    # → {"version":"1.0","buildNumber":"2"}

# Keep at most 3 releases.
ls -1dt /srv/pbcssg/releases/*/ | tail -n +4 | xargs -r rm -rf
```

Rollback is instant: point `current` back at the previous release directory and
restart. Because asset filenames are content-fingerprinted, a new release never
serves stale assets; HTML is served `no-cache`.

---

## systemd service (Ubuntu 26+)

Run the server as an unprivileged, sandboxed unit bound to loopback. Put a
TLS-terminating reverse proxy in front of it (see below).

Create a dedicated system user and layout once:

```bash
sudo useradd --system --home-dir /srv/pbcssg --shell /usr/sbin/nologin pbcssg
sudo mkdir -p /srv/pbcssg/{releases,incoming}
sudo cp pbcssg /usr/local/bin/pbcssg
sudo chown -R pbcssg:pbcssg /srv/pbcssg
```

`/etc/systemd/system/pbcssg.service`:

```ini
# /etc/systemd/system/pbcssg.service
# Sample — pbcssg unified launch (public + admin listeners).
# Edit the <YOURDOMAIN.COM> placeholders to your real domains before installing.
# Ports: public 127.0.0.1:8082, admin 127.0.0.1:9082. Runs as the unprivileged `pbcssg` user.

[Unit]
Description=pbcssg — Your PBC Launcher (PBCSSG)
After=network-online.target
Wants=network-online.target
# The data dir is the dm-crypt mount. Refuse to start unless it is actually
# mounted, so a reboot before you manually unlock does NOT let pbcssg create a
# fresh, UNENCRYPTED app.db on the underlying root disk. systemd adds an implicit
# After=/Requires= on the mount unit covering this path.
RequiresMountsFor=/srv/pbcssg/data

[Service]
Type=simple
User=pbcssg
Group=pbcssg
WorkingDirectory=/srv/pbcssg

ExecStart=/usr/local/bin/pbcssg server \
  -content /srv/pbcssg/site/current \
  -addr 127.0.0.1:8082 \
  -public-origin https://www.YOURDOMAIN.COM \
  -admin-addr 127.0.0.1:9082 \
  -db /srv/pbcssg/data/content.db \
  -admin-origin https://admin.YOURDOMAIN.COM \
  -app-db /srv/pbcssg/data/app.db \
  -out /srv/pbcssg/site/out \
  -releases /srv/pbcssg/site/releases \
  -base https://www.YOURDOMAIN.COM \
  -site "Your Domain" \
  -inactive-after 4320h

Restart=on-failure
RestartSec=3

# --- Hardening -------------------------------------------------------------
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
LockPersonality=true
RestrictRealtime=true
CapabilityBoundingSet=
AmbientCapabilities=
SystemCallFilter=@system-service
SystemCallErrorNumber=EPERM
# The only writable tree (its data/ subdir is the encrypted mount; mount it BEFORE start).
ReadWritePaths=/srv/pbcssg

[Install]
WantedBy=multi-user.target
```

> **Unified launch on the host.** To also run the editor + dashboard here
> ([Unified launch](#unified-launch--edit-on-the-host)), add
> `-admin-addr 127.0.0.1:9082 -db /srv/pbcssg/content.db` to `ExecStart`. Because the
> editor writes (the DB, `releases/`, media), the hardened unit above then also needs a
> `ReadWritePaths=/srv/pbcssg` line — and the DB should live on a sealed, key-in-RAM
> volume you unlock over SSH, so host snapshots never capture it in plaintext. The
> public listener keeps serving from the bundle even while that volume is locked.

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pbcssg
sudo systemctl status pbcssg
journalctl -u pbcssg -f
```

> **Port note:** binding a privileged port (< 1024) directly would need
> `AmbientCapabilities=CAP_NET_BIND_SERVICE`. The example binds `127.0.0.1:8083`
> and terminates TLS at the proxy, so no extra capabilities are required.

### Reverse proxy

`pbcssg server` does not terminate TLS and binds loopback. Front it with a
TLS-terminating reverse proxy that forwards to `127.0.0.1:8082`. Two things
matter for privacy features to work:

- **Pass the `Sec-GPC` request header through** to the server (GPC detection
  depends on it).
- Do not log client IPs if your privacy posture forbids it.
- **Forward the dynamic endpoints unchanged.** Member sign-in and comments live under
  `/_pbc/` on the *same* public listener (`:8083`), so no extra `location` is needed — but
  the proxy must allow `POST` and **preserve the browser's `Origin` header**, which those
  endpoints check against `-public-origin` (CSRF). Serving the public site on the exact
  origin you pass to `-public-origin` is what makes member passkeys and the Origin check
  line up.

### Admin origin

The admin listener (`-admin-addr` — the editor + metrics) binds loopback on its **own
port** and is reached only through a **dedicated admin origin**: a *different* hostname
from the public site. This is the per-origin RP-ID split — creator passkeys are scoped to
this origin and cannot cross to the public one (SPEC §2.4/§7.9). Pass that exact origin as
`-admin-origin`; the WebAuthn RP ID is derived from it.

Front it with a TLS-terminating vhost that forwards to the loopback admin port and is
**restricted at the network layer** — an **IP allowlist** and/or **mutual TLS** (client
certificates). Passkey auth is the application-layer gate; the network restriction is
defence in depth so the editor is never openly reachable. Example with nginx
(`admin.example.com` and the `203.0.113.0/24` range are placeholders — use your own):

```nginx
server {
    listen 443 ssl;
    server_name admin.example.com;        # your dedicated admin origin (= -admin-origin)

    # Defence in depth: only your networks reach the editor at all.
    allow 127.0.0.0/24;                   # your office / VPN range(s)
    deny  all;
    # …or require a client certificate (mTLS), instead of or alongside the allowlist:
    # ssl_client_certificate /etc/pbcssg/admin-ca.pem;
    # ssl_verify_client on;

    location / {
        proxy_pass http://127.0.0.1:8085;    # this instance's admin listener
        proxy_set_header Host $host;         # preserve the admin origin (Origin / RP-ID binding)
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

Never expose the admin origin publicly without these controls. Running several instances
on one host? Give each its **own** admin port and admin origin. If the writable volume is
sealed at boot, the admin origin simply returns a gateway error until you unlock it and
restart — the public site keeps serving (see [Protecting data at rest](docs/DATA-AT-REST.md)).

### Error pages

The build emits themed `400/403/404/429/50x.html` at the bundle root. Point your
front-end's `error_page` at them and keep the real status. For nginx serving the
bundle directly from disk:

```nginx
server_tokens off;                  # don't leak the server version on errors
limit_req_status  429;              # rate-limited requests → the 429 page, not 503
limit_conn_status 429;

error_page 400 /400.html;
error_page 403 /403.html;
error_page 404 /404.html;
error_page 429 /429.html;
error_page 500 502 503 504 /50x.html;

location = /404.html { internal; }  # repeat per page: not directly requestable

location = /build.json { return 404; }  # internal build metadata: enumerates paths
```

`build.json` is bundle build metadata (a manifest of every file); `pbcssg server`
never serves it, and for direct static serving the rule above blocks it too — its
file map would otherwise let anyone enumerate every path, including unlisted pages.

Assets are referenced root-absolutely, so an error page served under any URL still
styles correctly. `pbcssg server` also serves the themed `/404.html` (404 status) on
its own misses, so a direct/edge-facing server matches the proxy; behind a proxy the
`error_page` intercepts first. If you proxy to `pbcssg server`, note the `50x` page is
shown when that backend is *down* — keep it self-contained in that topology.

---

## Privacy behaviour

- **Self-hosted only.** All CSS/JS/fonts/images are served from your origin.
  The build applies linking hygiene to external references (`rel="noopener
  noreferrer"`, `referrerpolicy`, lazy loading, and a `youtube.com` →
  `youtube-nocookie.com` rewrite), classifies every external reference, and the
  editor warns you about them before you publish.
- **GPC.** `/.well-known/gpc.json` is served from the bundle. Only the required
  `{ "gpc": true }` is emitted by default; the optional `lastUpdate` ISO date is
  added when you set it in Settings (and omitted — never an empty string — when
  you don't), so the file is always spec-valid. Because the site sells/shares
  nothing, honoring the `Sec-GPC` signal is a no-op with nothing to switch off;
  absence of the signal is never treated as consent. Contextual application
  across US states and the EU is documented in [docs/GPC.md](docs/GPC.md).
- **Media metadata stripping.** Every uploaded file is cleaned on ingest, before
  it is stored or served. Images: EXIF/GPS, XMP, IPTC, and text chunks are
  removed (SVGs are sanitized by a deny-by-default allowlist). Audio/video:
  MP4/M4A/MOV (GPS/`udta`/iTunes tags), MP3 (ID3), WebM/Matroska (Tags, Title,
  timestamp), Ogg (Vorbis/Opus comments), and WAV (LIST/INFO, bext, embedded
  ID3) are stripped. Formats that can't yet be fully scrubbed (FLAC, AVI, FLV)
  are rejected rather than stored with metadata intact.
- **Self-hosted audio/video.** A/V blocks render a native `<video>`/`<audio>`
  element served from your own origin — no third-party player, no external
  request. Large media is stored on the filesystem beside the database; images
  stay in the database.
- **Consent-gated embeds (opt-in).** YouTube blocks — and generic embed blocks
  for any provider you allowlist — render a self-hosted consent card that
  contacts no third party; the actual embed lives on a separate
  `/external/<provider>/<name>` page behind an explicit click-to-load facade.
  Only hosts you list in Settings can be framed: the build writes them into the
  served site's `Content-Security-Policy` `frame-src`, and refuses any embed
  whose host isn't allowlisted.
- **Hidden & members-only content.** Reveal blocks are AES-256-GCM encrypted at
  build and absent from the page source until the reader acts (Mode A/B), and
  group-gated blocks (and reveal Mode C) are envelope-encrypted so only a keyring
  key holder can decrypt them — all with Web Crypto, self-hosted, no CSP
  relaxation. This is real encryption, but the framing stays honest: a group key
  is a *shared bearer key* delivered by link, not per-user authentication.
- **`security.txt`.** When you set a Contact in Settings, the build serves
  `/.well-known/security.txt` (RFC 9116) as `text/plain` — the same
  build-and-serve pattern as `gpc.json`.
- **Metrics dashboard.** Opt-in and off by default. It stores **counters, not
  events**, and **never retains a client IP**: an address is reduced to its `/16`
  (or an off-grid IPv6/private tally) and discarded in the same call, and the raw
  User-Agent is reduced to a coarse class and dropped. No cookies, no per-visitor
  identifier, no cross-request linkage — so there is nothing to re-identify and
  nothing for GPC to suppress. It is shown only on the editor's **Metrics** admin page
  (loopback admin listener, SSH-tunnel access), never on the public site, in memory only.
- **Search.** The client-side search index is built from your content and matched
  entirely in the browser (the query never leaves the device); it is served
  `no-cache` so new pages appear in search immediately after a rebuild.
- **Feeds & navigation.** Optional RSS 2.0 + Atom feeds are defined by path glob
  in Settings (e.g. `/blog/*` → `/feeds/blog.rss` + `.atom`) — self-hosted static
  XML with absolute links, deterministic (no wall-clock timestamps), no tracking.
  A configurable nav bar renders on every built page.
- **Theme.** The built-in theme is the always-present baseline; operator theme
  overrides are validated to forbid external `url()` and `@import`, keeping the
  site self-hosted. Pages default to the visitor's OS light/dark preference and
  carry a self-hosted footer **toggle** (Auto → Light → Dark) that persists the
  choice in first-party `localStorage` — nothing is sent to a server, and it
  works even in browsers that force `prefers-color-scheme` (e.g. Firefox
  `resistFingerprinting`). Colours and the toggle are documented in
  [docs/THEME.md](docs/THEME.md).

---

## Development

```bash
make check      # gofmt check + go vet + go test + govulncheck
make test       # tests only
make cover      # coverage report
```
