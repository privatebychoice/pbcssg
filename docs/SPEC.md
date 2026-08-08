# pbcssg — Specification (v1)

**Status:** Ground truth for scope decisions; supersedes the `ssg-project` memory
note where they differ.

`pbcssg` (Privacy By Choice static site generator) is a Go static-site build tool
that **edits like a CMS locally and deploys as a static site**, with privacy
classification wired through the whole authoring-to-serving path. It consumes the
existing [`pbc-classification`](../../pbc-classification) module
(`go.privatebychoice.com/pbc-classification`, package `classify`) as a direct
Go import. Module path: `go.privatebychoice.com/pbcssg`; remote
`github.com/privatebychoice/pbcssg`.

---

## 1. Goals & non-goals

**Goals**
- Author content in a local, Wagtail-style GUI (page tree, drafts, revisions,
  publish) backed by SQLite.
- Build a fully static site (fingerprinted assets, no third-party front-end
  resources) plus a machine-readable **privacy manifest** per page.
- Make privacy a first-class authoring concern: classify every external URL as
  it is placed, warn before publishing anything rated poorly, auto-rewrite
  known-safe swaps, and enforce linking hygiene.
- Serve the built site from a remote Linux host with **limited dynamic
  content**, where new capabilities are added **to the engine itself** (see §2.3
  extensibility — e.g. custom tags), not via an external embedding API.

**Non-goals (v1)**
- Multi-user / concurrent editing, RBAC, or a hosted control panel. Creator mode
  is single-operator, local.
- Live crawling/observation of arbitrary third-party URLs at build time (see the
  honest-data model, §5.4).
- A plugin marketplace or theme ecosystem. Themes are local template sets;
  extensions are compiled into the engine (§2.3).

---

## 2. Execution modes

One binary, three subcommands — `build`, `creator`, and `server` — plus a
**unified launch**: `server` can bind both the public site and a loopback **admin**
surface (editor + dashboard) in a single process on the host (§7.9). Standalone
`creator` (laptop authoring) and `build` (headless/CI) remain; unification is
additive.

### 2.1 Creator mode (`pbcssg creator`)
- Runs locally on any desktop OS (macOS, Linux, Windows) — the toolchain is
  pure-Go and cross-platform (see §9). Serves a local web admin (browser UI) from
  Go `net/http` + `html/template` + self-hosted vanilla JS (no framework, no CDN).
- Owns the SQLite editing store: page tree, drafts, revisions, publish workflow.
- Runs the **privacy pipeline** interactively (annotate links, warn, rewrite).
- Runs **builds**: SQLite → static output bundle + privacy manifests.
- Never mounted on the **public** origin. It may also run on the host behind the
  loopback **admin listener** (§7.9) — the same editor mounted there — fronted by the
  TLS-terminating reverse proxy on a **separate admin origin and port**, so authoring
  can run locally *or* on the host; it is never mounted on the public origin.

### 2.2 Server mode (`pbcssg server`)
Runs on the remote Linux host and binds up to **two listeners in one process**:

- **Public listener** — serves the **immutable built bundle** (§7.1) plus a small,
  whitelisted set of dynamic endpoints (§7.3). This is the only surface the
  TLS-terminating reverse proxy fronts. Its serving path **never opens the editing
  store** — the public site stays bundle-backed and static.
- **Admin listener** (loopback-bound, opt-in) — hosts the **editor** (the same
  creator UI, §2.1) and the **private metrics dashboard** (§7.7). Binds loopback on
  its **own port**, distinct from the public port, and is fronted by the same
  TLS-terminating reverse proxy on a **dedicated admin origin** — protected by an **IP
  allowlist and/or firewall** at the proxy/host and gated by **WebAuthn** (§2.4). It
  requires the content DB. Multiple instances may share one host: each uses its own
  public and admin ports/origins. See §7.9.
- Honors GPC server-side (`Sec-GPC`), serves `/.well-known/gpc.json`, exposes
  `/version`.

**Why unify.** Running the editor on the host (against the host DB, over the tunnel)
dissolves the content-sync problem: authoring — and any future dynamic state such as
Community Member accounts or chat (§2.4) — lives on the host and is edited in place,
with no laptop↔host round-trip. Edits produce a **new bundle** that the public
listener swaps to atomically on an explicit **Publish**, in-process (§7.9); the
bundle stays the contract between authoring
and serving (§3), and the public listener stays immutable and DB-free in its serving
path.

**Data at rest.** Because the host now holds the editing DB and the **runtime store**
(pseudonymous account/comment data — no passwords, no email, no real name; §2.4), that
data sits **at rest on the host** — a surface the laptop-only model never had. It is
protected by a **sealed, key-in-RAM volume** unlocked over SSH (§7.9), so host snapshots
and backups stay ciphertext. `pbcssg server` **degrades gracefully** while the volume is
sealed: it serves the immutable public bundle and disables the dynamic layer + editor
(a loud `WARN`, not a fatal) until the volume is unlocked and the server restarted —
runbook and rationale in `docs/DATA-AT-REST.md`.

### 2.3 Extensibility — in-engine, not embedded
New capabilities are added **to the `pbcssg` engine itself**, in native Go, and
ship in the one binary. The primary extension point is a **custom tag /
shortcode registry**: named, Go-implemented tags usable from templates and
content — e.g. a `date`/`time` tag.

- A tag is a registered Go function `func(ctx, args) (html, error)` compiled into
  the engine and invoked by name during rendering.
- **Render-time matters for honesty:** a tag resolved at **build time** produces
  a *static, frozen* value (a date tag renders the build date and never changes
  until the next build). A value that must be *live* (current server time per
  request) is **server-mode dynamic rendering**, not static output. The spec
  keeps these distinct so "show date/time" can't accidentally imply a live clock
  on a static page. Decide per tag whether it is build-time or request-time.
- Tags never introduce third-party requests; they render locally.

There is no external "mount pbcssg into my app" API — that earlier idea is
dropped. Growth happens in-tree.

### 2.4 Roles & accounts
These roles, the WebAuthn auth mechanism, and the per-origin RP-ID constraint
below are **implemented** and normative.

- **Roles.** *Community Members* (comment), *moderators* (comment **and** moderate — a
  session-gated review surface on the public origin, §7.3, with per-moderator,
  creator-granted **Can invite** / **Can ban** powers, both default off; ban is soft and
  members-only, invites are member-only/30-day/capped), and *creators* (authors/admins who
  post, with full moderation). A moderator invite is un-redeemable on the member endpoints
  and vice-versa; login is role-gated, so the two credential sets on the shared public
  RP-ID never cross.
- **Auth mechanism.** WebAuthn / passkeys with **user verification required**
  (`userVerification: "required"`). The authenticator gesture (biometric or PIN)
  plus possession of the key is itself two-factor, so **no TOTP/OTP second factor
  is added** — TOTP would reintroduce a stored shared secret and bolt a phishable
  factor onto a phishing-resistant one, lowering assurance, not raising it. The
  session-transport reasoning lives in `docs/SESSION-COOKIES-STORAGE.md`.
- **Design constraint already in force.** Passkey **RP-ID is per-origin.** *Creators*
  authenticate on the **admin origin** (the proxied admin listener, §7.9); *Community
  Members* and *moderators* on the **public origin.** These are distinct credential
  domains by construction — the public/admin listener split (§2.2) keeps them separate
  from day one, which is why the split is worth getting right now. The RP-ID is the
  exact admin host, so where several instances share a VPS, each instance's creator
  credentials are scoped to that instance's admin origin and cannot cross over.
- **Where auth will attach.** Creator auth → the admin listener (§7.9);
  member/moderator auth → the public dynamic endpoints (§7.3), under the reserved
  prefix. Until creator auth lands, the admin listener is protected by its network
  controls alone (loopback bind + proxy IP allowlist/firewall — §7.9); once it lands,
  WebAuthn is the gate on top of those controls.
- **Identity is a passkey, not PII.** No password, no email, no real name. The server
  stores only per-credential public material — credential ID, COSE **public** key,
  signature counter — plus a **random, opaque user handle** (WebAuthn requires the
  handle carry no PII). The **private key and any biometric never leave the
  authenticator**, so they are never stored. Defaults: **discoverable credentials**
  (usernameless login → no account-enumeration oracle), **`attestation: "none"`** (no
  device-model / AAGUID fingerprint), and a **generated non-identifying label** for the
  passkey chooser (`user.name` / `displayName`), never an identifier.
- **Multiple authenticators per account.** One opaque user handle maps to many
  credentials (the per-credential storage above already accommodates it).
  *Creators, admins, and moderators* **SHOULD register ≥2** authenticators (e.g. a
  primary key plus an offline backup) before elevated privileges are granted —
  with no email recovery, one lost key is otherwise a permanent lockout. A signed-in
  creator manages this at **`/admin/passkeys`** (add a key via an authenticated,
  invite-less registration ceremony bound to the account's handle; label and remove
  keys, with the last one un-removable), and mints/lists/revokes registration invites
  at **`/admin/invites`** — the editor equivalent of the bootstrap CLI.
  *Community Members* MAY register several; if they don't, invite-gated re-entry
  and the inactivity auto-purge are the fallback. `attestation: "none"` is requested for **every role** (Community Members,
  moderators, and creators) — no AAGUID/device-model fingerprint is ever collected.
  Role-dependent hardware-key attestation (an AAGUID allowlist for admin/moderator
  enrolment) was **considered and declined**: invite-gating, the ≥2-key rule, and the
  network-restricted admin origin already gate elevated enrolment, so attestation
  would add device-model lock-in and trust-anchor machinery with no matching security
  gain. Signature verification accepts **ES256 (ECDSA P-256) and EdDSA (Ed25519)**
  only — modern elliptic-curve algorithms every current authenticator provides; RSA
  (RS256) is **deliberately not implemented**.
  Multiple credentials on one account are linked to each other by the shared user
  handle — the same account, so cross-account unlinkability is untouched. Enforce
  the signature counter for single-device keys (clone detection); tolerate a
  0/absent counter from synced passkeys rather than hard-failing.
- **Sessions are cookies, deliberately.** After a WebAuthn assertion the server
  issues a server-side session (opaque id, stored **hashed** at rest) carried by a
  single first-party **`__Host-`, `HttpOnly`, `Secure`, `SameSite=Strict`** cookie
  on **both** origins, so a signed-in visitor is not re-authenticated per page
  load. `HttpOnly` keeps the id out of script's reach (XSS cannot exfiltrate it);
  `SameSite=Strict` closes most CSRF, with a double-submit / custom-header token on
  state-changing POSTs for defense in depth; a short, **fixed** TTL with a cheap WebAuthn
  re-tap on expiry — deliberately no sliding renewal or long-lived refresh token (the
  re-tap is one gesture, and creator drafts persist server-side, so nothing is lost). This is a **strictly-necessary**
  cookie — first-party, opaque, never a tracking identifier, and **consent-exempt**
  (no banner; disclosed in the privacy policy). Mechanism vs. compliance reasoning:
  `docs/SESSION-COOKIES-STORAGE.md`. A `sessionStorage` bearer token is used only
  where a flow serves gated content exclusively by fetch-and-inject (like the
  client-side reveal, §6.9), which needs no navigation auth; the reveal/unlock keys
  themselves stay in `localStorage`, client-side only and never sent to the server.
- **Alias is a display name, not a login.** The human-chosen alias is a public display name
  for comments, kept in the runtime store — **not** a login identifier and never part of the
  WebAuthn ceremony. Authn identity and comment identity are decoupled by design. It is
  **account-level**: one alias per account (not per comment), **unique across accounts
  case-insensitively** when non-empty so no two people hold the same name at once
  (anti-impersonation); `""` is "anonymous" and shared. Changing it back-fills every comment
  the account authors — including pending ones, so a rename can't show a moderator two names —
  and frees the old name immediately (the uniqueness index is on the live account, so a
  deleted/anonymized account releases its name too). The same rule governs members,
  moderators, and the creator.
- **Three stores, three lifecycles.** (1) `content.db` — the authoring store, admin
  listener only (§2.1). (2) the **immutable bundles** — public read-only output. (3) a
  **runtime store** (`app.db`) for accounts, sessions, and comments — written by the
  public dynamic endpoints (§7.3) and by moderation. Runtime data never lives in the
  bundle (a Publish would destroy it) nor in `content.db`. This re-scopes §7.1's
  "public path is DB-free": the public path never opens the *editing* store, but it will
  open the *runtime* store once accounts exist.
- **Minimize, then protect.** Most runtime data is public by design (approved comments +
  aliases are displayed); sessions are stored **hashed**; passkey public keys are not
  secret. So the privacy work is **data minimization** (no client IP, no user-agent,
  coarse timestamps, and a deliberate choice about whether to link account→comment at
  rest) and **real erasure** (a "forget me" hard-delete — with no email/name, deletion is
  complete; mind backups). Encryption at rest then only has to cover the small sensitive
  slice (the pending-moderation queue, any linkage); mechanism in §7.9.
- **Invite-gated registration.** Accounts are created only by redeeming a **single-use
  invite code** (stored **hashed** in the runtime store, redeemed atomically). The
  account records which invite created it (an opaque **lineage** ID) — never who the code
  was sent to; distribution is tracked out-of-band so no recipient identity enters the
  DB. Invites are the Sybil / re-entry control that makes bans durable without PII.
- **Bootstrap the first creator.** Invite-gating raises a bootstrap problem: minting an
  invite is an admin action, but the first admin has no invite. It is resolved from the
  **host-access root of trust** — running the CLI on the host (which also unlocks the
  sealed `app.db` volume, §7.9). A `pbcssg admin bootstrap` command, run on the host,
  mints a single-use **`role=creator`** invite and prints the code to **stdout** (never
  the log — §9); the operator opens the admin origin, redeems it, and registers the
  first passkey. This special-cases nothing — the admin
  account is still created by redeeming an invite — and the invite requirement stays the
  **public-origin** Sybil control it was meant to be. A CLI-issued one-time code (visible
  only on the operator's terminal) also closes the race a naive "open first-run
  enrollment" would leave, where a local process on the host could register as admin
  first. **Recovery corollary:** because host access can always re-run bootstrap, the
  operator can **never be permanently locked out of admin**, even if every admin passkey
  is lost — the opposite of the member rule ("lost passkey = no recovery") and correct,
  since SSH/host access is the ultimate root. The **≥2-authenticators** guidance for
  creators is therefore convenience, not survival. The command stays available after the
  first creator exists (it is the break-glass path and the way to enroll a second admin);
  using it while creators already exist logs a `WARN`.
- **Erasure ("forget me").** Self-service while authenticated — the passkey is the proof
  of ownership (there is no email recovery). Re-authenticate, then hard-delete the
  account, its credentials, sessions, and comments (anonymize-or-delete, member's choice)
  from `app.db`. The passkey is left orphaned on the member's own authenticator — they
  remove it locally; the server cannot. Encrypted backups age the data out within a short
  retention window, and there are no request logs to scrub (no IP / UA / path logging). A
  **lost passkey means no recovery**; an **inactivity auto-purge** (idle > N months) is
  the backstop and a privacy win. It runs as a background maintenance ticker in
  `pbcssg server` (`-inactive-after`, which also prunes expired sessions via
  `-maintenance-interval`); **only members are auto-purged** — moderators and creators
  are staff and never removed by it.
- **Runtime-store retention (bounded growth).** On the same ticker, the store keeps
  itself from growing without bound, with retention windows editable in **Settings →
  Maintenance** (each `0` disables that prune): **spent invites** (unredeemed, revoked or
  expired past the window) are deleted — but **redeemed** invites are kept as the
  "invited by" provenance record; **rejected** comments are deleted past their window;
  **orphaned** comments (whose page no longer exists — comments are keyed by path, with no
  link to the page tree) are deleted past their window; **dormant members' display names**
  are released back to the pool once the member has been idle past the window (the name is
  cleared on the account **and** blanked on their old comments, so a released name never
  lingers where a new claimant could exploit it — staff names are never auto-released);
  **empty deleted-comment placeholders** (a tombstoned root whose replies are all gone) are
  reclaimed past their window (one that still has replies is never touched); and the file is
  periodically `VACUUM`ed to reclaim freed space. Defaults are 30 / 30 / 90 / 90 / 30 / 30
  days. Nothing that still matters is removed: **pending and approved comments, and redeemed
  invites, are never touched**. The orphan prune reads the live page list from the content
  store, so it runs only on the admin-enabled server, and a failed read is a no-op (it never
  wipes on an empty list). Separately, an account may change its **display name at most N
  times per day** (default 3; a Settings knob stored in `app.db` so the public origin enforces
  it in real time) — anti-churn, so a member can't rapidly rename to dodge reputation.
- **Ban = account + invite, never the device.** WebAuthn is deliberately unlinkable —
  each registration mints a fresh credential ID and key pair, and `attestation: "none"`
  zeroes the AAGUID, so the relying party has **no stable hardware identifier**: a
  physical key cannot be banned (the same property that stops members being tracked
  across accounts). Ban is therefore **account-level** — covering every key registered to
  it: flag the account by user handle, revoke its sessions, remove its posts (via the
  internal account↔comment link — instant, dynamic render), and **burn the creating
  invite**. Re-entry needs a fresh invite, which only the operator issues. **No IP bans,
  ever.** Because registration is invited (vetted at the door), a heavy trust-tier queue
  may be unnecessary — content-removal + account-flag + invite-burn, with optional
  invite-tree pruning, is the baseline.

---

## 3. High-level architecture

```
                         creator mode (local)
  ┌───────────────────────────────────────────────────────────────┐
  │  Web admin (html/template + vanilla JS)                        │
  │        │                                                       │
  │        ▼                                                       │
  │  Content store  ──►  Privacy pipeline  ──►  Build engine       │
  │  (SQLite tree)       (classify + rules)     (render + emit)    │
  │                                              ▲                 │
  │                                   custom tag registry (§2.3)   │
  └───────────────────────────────────────────────────────────────┘
                                   │
                                   ▼   versioned static bundle
                        ┌────────────────────────┐
                        │  site/                 │
                        │   ├── <fingerprinted    │
                        │   │    html/css/js/img> │
                        │   ├── manifest/*.json   │  ← per-page privacy manifests
                        │   ├── search/index.json │  ← client-side search index
                        │   ├── .well-known/gpc.json
                        │   └── build.json        │  ← version, build number, hashes
                        └────────────────────────┘
                                   │  deploy
                                   ▼
                   server mode (remote host — one process)
  ┌───────────────────────────────────────────────────────────────┐
  │  Public listener (proxy-fronted)                              │
  │    static bundle + limited dynamic (§7.3) + request-time tags │
  │                                                               │
  │  Admin listener (loopback, proxied admin origin, opt-in — §7.9)│
  │    editor (creator UI)  +  metrics dashboard (§7.7)           │
  │        │ opens                                                │
  │        ▼                                                      │
  │  host content DB ──► build ──► new bundle ──(publish §7.9)   │──► public listener
  └───────────────────────────────────────────────────────────────┘
```

The **static bundle is the contract** between authoring and serving. Creator — or,
in a unified launch, the admin listener — writes it; the public listener reads it.
It is self-describing (`build.json`), and the **public serving path never requires
the SQLite DB**: even unified, only the admin listener opens the DB.

---

## 4. Content model (SQLite page tree)

Wagtail-inspired but deliberately minimal for v1. Pure-Go SQLite driver
(`modernc.org/sqlite`, no cgo) to preserve single-binary cross-compilation.

**First-run seeding.** The very first time the editor opens a database it seeds
three normal, editable **draft** starter pages — `/` (Home), `/about`, and
`/privacy` (a Privacy Policy template) — plus safe default navigation (primary nav
`Home → /`; footer nav Privacy/Classification/About/Tags), to streamline a new
site. The settings live in the key/value `settings` table (which has no per-key
SQL default), so the defaults are seeded as rows rather than schema defaults, and
stay fully editable in Settings. It is gated by a marker (`install.seeded`), so
later launches never re-seed, and it is invoked only by the `creator` command
(`creator.SeedDefaults`, before `New` so the seeded nav loads into the runtime),
never by build or server, nor by `New` itself (so the test suite is unaffected).
Nothing is published: the operator reviews/edits — especially the Privacy Policy —
and publishes when ready.

**Path validation (issue #1).** Page paths are validated on create/save to be
URL-clean: a leading slash plus slug-like `/`-separated segments (lowercase
letters, digits, hyphens). This rejects spaces (which broke several editor links
and the built URL), uppercase, dots (so `.`/`..` can never appear), and control
/encoding characters. Trailing slashes are canonicalized away. As defense in
depth the build also refuses to write outside the output directory (`withinDir`),
so a stored traversal path can never escape the bundle even if it bypassed the
editor.

### 4.1 Tables (sketch)
- `pages` — the tree. `id`, `parent_id`, `path` (materialized path or
  treebeard-style ordered key), `slug`, `title`, `type`, `status`
  (`draft|published`), `live_revision_id`, `created_at`, `updated_at`.
- `revisions` — every save. `id`, `page_id`, `content_json` (typed block/field
  payload), `author`, `created_at`, `is_published`. Draft = latest revision not
  yet promoted to `live_revision_id`.
- `assets` — uploaded, self-hosted media. `id`, `sha256`, `original_name`,
  `mime`, `bytes`, `alt_text`. Content-addressed → natural fingerprinting.
- `external_links` — **classification cache** (derived, rebuildable). `page_id`,
  `url`, `domain`, `grade`, `trust`, `stale`, `reasons_json`, `checked_at`.
  Populated by the privacy pipeline; drives editor annotations and the manifest
  without reclassifying on every keystroke.
- `settings` — site config (site name, base URL, first-party domains, GPC
  `lastUpdate`, build number).

### 4.2 Field / block model
- v1: page **types** are defined in Go config, each with a small set of typed
  fields (`text`, `richtext`, `image`, `url`, `embed`, `block-list`,
  `youtube`).
- The **`youtube` fieldblock** is a first-class, consent-gated block (see §5.8):
  it stores a video id/name, transcript, and description links, and renders as an
  inline consent card that links out to a generated `/external/youtube/<name>`
  page rather than embedding YouTube on the host page.
- **Realized v1 block types** (in the one repeatable block list): `markdown`,
  `image` (figure — with optional **layout options**: `align` left/right to float
  it so body text wraps beside it, and a preset `maxWidth` small/medium/large;
  both are allowlisted and emitted as CSS classes, floats align to the reading
  column and collapse to full-width on narrow screens), `media`
  (self-hosted `<video>`/`<audio>`), `callout`
  (note/tip/warning/info admonition), `citation` (quotation), `youtube`, the
  generic consent-gated `embed` (§5.8), the route-based `index` page-list
  (§6.7), and the deferred-reveal `reveal` (hidden/obfuscated content, §6.9).
  Each serializes to `content_json` and renders via `html/template` at build.
  Any text-shaped block (`markdown`/`callout`/`citation`, plus caveated
  `image`/`media`) may additionally be **group-gated** via an optional `groups`
  list — envelope-encrypted and unlocked by a browser keyring, not a distinct
  block type (§6.10).
- **Long-form authoring blocks** (§6.12, implemented): `code` (simple, no
  highlighting, self-hosted copy button), `details` (disclosure/FAQ), and `toc`
  (table of contents), plus markdown GFM + footnotes, auto heading IDs, and heading
  permalink anchors.
- **Posts & discovery** (§6.13, implemented): a per-page **post** flag, a Settings
  **reading-time** estimate on posts, and a `related` (related posts) block.
- **Media & sharing** (§6.14–§6.15, implemented): free-form media-library tags, a
  `gallery` block (manual or media-tag-driven, CSS-only lightbox), and a
  privacy-preserving `share` block.
- **Site metadata** (implemented): a per-page **og:image** with a Settings site
  default (§6.3), and `security.txt` (§7.6).
- `richtext` and `block-list` bodies serialize to `content_json`; rendering to
  HTML happens at build via `html/template` (auto-escaping) + a Markdown/HTML
  sanitizing step. Custom tags (§2.3) are expanded during this render.
- **Decision (open):** full StreamField-style nested blocks vs. flat typed
  fields for v1. Recommendation: flat typed fields + one repeatable block list;
  defer deep nesting. See §11.

---

## 5. Privacy pipeline (v1 focus)

This is the differentiator and the v1 priority. It consumes `classify` and adds
the editor-side and build-side behavior around it.

### 5.1 Classifier construction
A single `*classify.Classifier` per site, built once from site settings:

```go
c, err := classify.New(
    classify.WithFirstParty(settings.FirstPartyDomains...), // e.g. privatebychoice.com
    // classify.WithDataFile(operatorOverridesPath),        // optional local overrides
)
```

First-party trust is per-deployment (never shipped in the dataset) — exactly how
`WithFirstParty` is intended to be used.

### 5.2 URL extraction
At edit time (on save) and again at build time, walk each page's rendered HTML
with `golang.org/x/net/html` and extract every URL that causes an external
request: `a[href]`, `img/script/iframe[src]`, `link[href]` (stylesheets,
preconnect, favicons), `source[srcset]`, embed URLs, and inline `url()` in
styles. Resolve to origin, drop first-party/self-hosted origins, classify the
rest.

### 5.3 Editor annotations & pre-publish warnings
- Each external link/embed in the editor shows its **badge**: `Grade.Letter()` +
  `Grade.Name()` + `Grade.Icon()`, plus the `Trust.Marker()`. Color is **never
  load-bearing** — the glyph + letter carry meaning (accessibility; matches the
  library's contract).
- Reasons (`Classification.Reasons`) are shown on hover/expand.
- **Publish gate (decided: warn + acknowledge):** before a build/publish, surface
  every external link rated `D`, `F`, or `?` Unclassified. The author must
  **explicitly acknowledge** to proceed — not a hard block. Rationale: honesty
  over silent failure; the author decides, but never unknowingly. (A hard-block-
  by-threshold mode may be added later as an opt-in; v1 is warn+ack.)
- **Consent-gated exemption:** a facade domain that a **consent-gated fieldblock
  introduces on its own generated `/external/...` page** (e.g. `youtube-nocookie`
  on `/external/youtube/<name>`, §5.8) is treated as **pre-acknowledged** — the
  block type *is* the consent, so the gate does not demand a manual click for it.
  Two guardrails keep this honest:
  1. **Never hidden** — it still appears in that page's privacy manifest (§5.7)
     and is shown in the gate UI as a read-only "consent-gated (pre-acknowledged)"
     line; the exemption suppresses the *nag*, never the *disclosure*.
  2. **Surgical** — it applies only to the facade domain the fieldblock itself
     owns, only on that block's own generated page. The **description links** on
     that page, and any raw third-party link an author drops into ordinary body
     content, get **no** exemption and go through the normal warn+ack gate.
  On the **host page** no exemption is even needed: the fieldblock emits only an
  internal link to `/external/youtube/<name>`, which is first-party and never
  classified as external — so nothing there trips the gate by construction.

### 5.4 Honest-data model (important)
Cookies, scripts, and fingerprinting of an arbitrary pasted URL **cannot** be
observed at build time without fetching it, so those signals come only from the
curated dataset inside `classify`. Consequences the pipeline must respect:
- Unknown domains render as `? Unclassified` — **never** a false pass.
- First-party / self-hosted assets the SSG emits itself are marked
  authoritatively (they are first-party by construction).
- The pipeline does not fetch third-party URLs at build time (no crawling,
  no leaking the build host's IP to third parties).

### 5.5 Auto-rewrites (known-safe swaps)
A local, auditable **rewrite registry** applies deterministic, privacy-improving
transforms at build (and offers them in the editor):
- `youtube.com` embed → `youtube-nocookie.com` **and** wrap in a click-to-load
  facade so nothing third-party loads until the user opts in (per
  `embed-privacy-tips`). Classify the two YouTube domains as distinct so the
  badge can honestly point at the less-bad option.
- Each rule is explicit, reversible, and logged; no silent "fixing" that changes
  author intent beyond the declared swap.

### 5.6 Linking hygiene (enforced at render/build)
- External `a` → `rel="noopener noreferrer"`, appropriate `referrerpolicy`.
- Embeds/iframes → lazy facades (`loading="lazy"` at minimum), click-to-load.
- No third-party favicons, no third-party `preconnect`/`dns-prefetch`.
- Emit warnings for any construct that would contact a third party on page load.

### 5.7 Privacy manifest emitter
- For each built page, emit `manifest/<page-path>.json`: every external domain
  the page references + its classification. Per-URL entries reuse the shape the
  CLI already previews (`cmd/classify/main.go` `result`: `url`, `domain`,
  `matched`, `grade`, `gradeName`, `trust`, `verified`, `stale`, `reasons`).
- Also emit a site-level `manifest/site.json` aggregating unique domains + worst
  grade per page. This satisfies the standing rule to *identify every external
  network request a page makes*.
- **On-page external-references listing.** When a built page references ≥1
  external domain, the build surfaces a self-hosted **"External references"**
  listing just before the footer — the same per-domain privacy picture the editor
  shows live (§5.3): each domain with its grade letter + `Grade.Name()` + the
  classifier's **reasons**, ordered worst-grade-first. It reuses the layout's
  `<div data-pbcssg-extref>` slot, replaced after the scan; pages with no external
  references have the slot removed and show nothing. Because classification is only
  known after the render + scan, the listing is injected in `build.emitPage` rather
  than the render pass; it is trusted internal markup added after the link scan, so
  it never affects the page's own classification. The grade meaning is carried by
  the letter and name (colour is decorative reinforcement, never load-bearing).
- **Custom classification dataset.** The operator may supply a custom
  `pbc-classification` dataset (a `domains.json`: `{ "<domain>": { trust, signals,
  verified, evidence, note } }`) that is **merged over** the library's embedded
  defaults (later entries win) so they can add or override domain classifications.
  It is stored in the content DB (`classify.domainsJSON`), threaded through
  `build.Config.ClassifyData`, and used by **both** the live editor badges and the
  headless build via `classify.WithDataBytes`. When present it is **published** into
  the bundle at `/.well-known/pbc-classification/domains.json` for transparency, so
  the data behind the grades is inspectable. Validation reuses the library itself
  (a throwaway `classify.New(WithDataBytes)` — the only source of truth, since the
  parser/validator are unexported): a malformed dataset is rejected on save, and a
  bad stored value falls back to the library defaults rather than bricking the
  editor. Edited via the in-editor dataset editor (§6.8).
  - **When it is published is gated.** `/.well-known/pbc-classification/domains.json`
    and the report details (below) are published only when the operator enables
    **Publish Classification Report Details** in Settings; the dataset always drives
    grading regardless. `ClassifyDataRepoURL` is an optional Settings link to the
    operator's dataset repo shown on the report.
- **User-facing `/classification` report page.** The build always emits a
  human-readable report at `/classification` (a reserved route): it explains the
  rating system in plain language, shows the **rating scale** legend (pulled from
  the library so it can't drift), links the `pbc-classification` module (static)
  and the operator's dataset repo (optional), and carries an *editorial-opinion /
  as-is* disclaimer. When **Publish Classification Report Details** is on, it also
  shows a summary (per-grade tallies), a **Classifications used** listing of every
  domain in the operator's dataset (classified live, alphabetical, with reasons —
  the same grade UI as the per-page listing), and a link to the published JSON.
  Every page's external-references block links to it ("How we rate these →"). Only
  the operator's *custom* dataset is enumerable (the library's built-in defaults
  live in the linked module).
- **Boundary decision (open):** per-URL classification belongs to `classify`
  (possibly a small `Manifest`/`Classify`-batch helper there); per-page and
  per-site **aggregation + file emission** belong to `pbcssg`. Confirm whether
  any helper is added to the library vs. kept entirely in the SSG. See §11.

### 5.8 Consent-gated external embeds (`youtube` fieldblock)
The `youtube` fieldblock (§4.2) never embeds YouTube on the page it appears on.
It implements **two-stage consent**, so a content page keeps its top privacy
grade and the user makes an informed choice before any third party is contacted.

**Stage 1 — inline consent card (on the host page).**
- Renders as a self-hosted card/link. It contacts **no** third party: no
  `youtube.com`/`youtube-nocookie.com` request, no thumbnail fetched from Google
  (use a self-hosted poster image or none). The host page's manifest therefore
  lists no external domain for this block, and the page stays clean/grade A.
- The card is **explicit** that activating it navigates the user to a different
  page that *may* load third-party content and introduce tracking (copy below).
- The card links to the generated page `/external/youtube/<name>`.

**Stage 2 — generated `/external/youtube/<name>` page.**
- A static page pbcssg generates at build time from the block's stored data:
  the **transcript**, the **description links** (each classified + hygiene-
  treated like any other external link, §5.2/§5.6), and the actual video.
- The video here is still **click-to-load facade + `youtube-nocookie.com`** (per
  `embed-privacy-tips`): nothing from Google loads until the user clicks play on
  this page — a *second* explicit action. So even the external page is clean
  until playback; its manifest honestly lists `youtube-nocookie.com`.
- **Publish gate:** this facade domain is **pre-acknowledged** on its own
  `/external/...` page (the consent-gated exemption, §5.3) — it is still listed
  in the manifest and shown read-only in the gate, but needs no per-publish ack.
  The page's description links are gated normally.
- **Slug (`<name>`):** author-chosen, **defaulted from the title**, uniqueness
  enforced at build, and **stable after publish** (changing a published slug
  warns, since it breaks existing links). The raw YouTube `videoId` is stored in
  the block for the embed but is **never** used in the URL. Route namespace is
  `/external/<provider>/<name>` so other providers can follow the same pattern.

**Data stored in the block:** `videoId`, `name`/slug, `title`, `transcript`
(richtext), `descriptionLinks` (list of URLs, classified), optional self-hosted
poster image, optional `keywords` (contributed to the page's search index via the
§6.2 index-contribution hook). **All of it is manually authored** in the editor
— pbcssg never
fetches transcripts, links, or thumbnails from YouTube at build time, keeping the
build fully offline and honest (§5.4). Transcripts are first-party text and feed
the search index (§6.2).

**Accessibility:** the card is a real link/button, keyboard-focusable, with the
warning in visible text (not color/icon alone); the destination page has a proper
heading and the transcript is real page text (good for a11y *and* SEO).

**Default copy** (`{title}` = the video title; the site owner may revise, but this
is the shipped default, not a placeholder):

*Stage 1 — inline consent card:*
> **External video** · {title}
> This video is on YouTube. To keep this page private, nothing from YouTube loads
> here. Open the video page to watch it, read the transcript, and see the links
> from the description. YouTube may set cookies and track you once you choose to
> play.
> `Open video page →`

*Stage 2 — `/external/youtube/<name>` page intro (above the facade):*
> You're on the video page for "{title}." You can read the transcript and links
> below without loading anything from YouTube. Press play only when you're ready
> — that's when YouTube loads and may begin tracking you.

*Stage 2 — facade play control (before click):*
> Button: `▶ Play — loads YouTube`
> Fine print: "Pressing play loads youtube-nocookie.com. YouTube may set cookies
> and track your viewing from that point."

*Stage 2 — section headings:* `Transcript` · `Links from this video`

**Generic embed (`embed` fieldblock) — same pattern, any provider.** The `embed`
fieldblock generalizes the two-stage consent flow to any provider (PeerTube, a
self-hosted player, Vimeo, …). It stores a `provider` slug, `name`/slug, `title`,
the `embedUrl` (the iframe src the visitor consents to load), optional
transcript/notes, description links, poster, and keywords. Stage 1 is the same
self-hosted card (no third-party contact); Stage 2 is the generated
`/external/<provider>/<name>` page whose facade frames `embedUrl` only on a second
click. It differs from `youtube` in one security-critical way:

- **Host allowlist → CSP.** The operator maintains an **allowlist of embed hosts**
  in Settings. The build **refuses** (skips + warns) any embed whose URL host is
  not on the list, and writes the allowlisted `https` origins into `build.json`;
  server mode composes its `frame-src` from them, so the served site's CSP permits
  **only** those hosts to be framed (defense in depth over the build-time refusal).
  The editor also requires `embedUrl` to be absolute `https` (no mixed content).
  Each allowlist entry is **validated to be a clean `host[:port]`** (optional `*.`
  wildcard) on save (`build.ValidEmbedHost`) and dropped by the build otherwise, so
  a stray space or `;` can never inject a directive into the CSP header; server
  mode independently skips any malformed `frame-src` origin as a final gate.
- Everything else — the manifest disclosure of the facade target, the pre-
  acknowledged gate exemption on the `/external/...` page, the a11y treatment, and
  search-index contribution of title/keywords/notes — matches the `youtube` block.

---

## 6. Build engine

- Input: published revisions from SQLite. Output: the static bundle (§3).
- Rendering: `html/template` (auto-escaping) → HTML; Markdown via `goldmark`
  (pure Go, CommonMark) where richtext is authored as Markdown; custom tags
  (§2.3) expanded during render; output HTML is sanitized before the
  link-extraction/hygiene pass.
- **Fingerprinted filenames** for every asset (content hash in the name) so
  clients never receive stale assets; long-lived `Cache-Control` on hashed
  assets, short/`no-cache` on HTML.
- Writes `build.json`: semantic version + **build number** (third component of
  `1.0.x`, incremented per deploy), file hashes, and the classifier dataset
  version used.
- **Build number surfaced in the UI footer** of the built site (and the creator
  admin), per standing convention.

### 6.1 Asset ingestion, metadata stripping & SVG sanitization
Uploaded media routinely carries embedded metadata — **EXIF (incl. GPS
coordinates), XMP, IPTC, device/camera identifiers, timestamps, thumbnails** —
and SVGs can carry active/tracking content. Ingestion cleans every asset so
nothing but pixels (or safe vector markup) ships.

**Supported formats: JPEG, PNG, SVG** (primary). WebP is **allowed with an
editor warning** (see below), not a primary format.

**When:** on ingest in creator mode, *before* the content-address hash (§4.1
`assets.sha256`) is computed, so the stored blob and every emitted file are
already clean. The build re-verifies and **fail-closes** — an asset that still
carries metadata (or an SVG that still contains active content) is rejected, not
silently shipped.

**Raster (JPEG/PNG):**
- Strip all metadata; preserve displayed quality. For PNG the strip is lossless
  (drop `tEXt/iTXt/zTXt/eXIf/tIME`, keep image + color chunks). For JPEG, prefer
  a lossless segment strip (drop `APPn`/EXIF/XMP/IPTC) over a lossy re-encode.
- Preserve an ICC color profile where present (it is color management, not
  tracking).
- With the format set now JPEG/PNG, a **near-zero-dependency** path is viable
  (stdlib chunk/segment handling, or `image/*` re-encode for PNG); a 3rd-party
  module is optional — see §9/§11.

**SVG (security-critical — delegated to the `pbcsvgsanitize` module):** SVG is
XML and can execute script and phone home, and Go has no maintained SVG-specific
sanitizer, so this lives in its own reusable module,
[`pbcsvgsanitize`](../../pbcsvgsanitize) (`go.privatebychoice.com/pbcsvgsanitize`,
remote `github.com/privatebychoice/pbcsvgsanitize`) — kept separate so it can be
audited and reused independently. It implements a **deny-by-default allowlist
sanitizer on stdlib `encoding/xml`** (case-preserving, so `viewBox` survives; no
external-entity/DTD resolution): parse → keep only a tiny allowlist of
presentational elements/attributes → re-serialize, dropping everything else —
`<script>`, all `on*` handlers, `<foreignObject>`, `<style>`, SMIL `<animate>`,
DOCTYPE/entities/PIs, unknown namespaces, embedded metadata, and **any external
reference** (only local `#id` fragment refs permitted; zero phone-home). An SVG
that cannot be sanitized safely (not an SVG, malformed, custom entities, too
large/deep) is **rejected** — there is **no rasterize-to-PNG fallback** (the
candidate pure-Go rasterizers have open security issues, so that dependency was
deliberately dropped; a rejected SVG must be fixed/re-exported by the author).
- **pbcssg's responsibilities** (not the module's): orchestrate ingest, enforce
  **fail-closed** at build, and **serve SVGs isolated** — embed via `<img src>`
  (never inline into the page DOM) under a restrictive response CSP +
  `Content-Type: image/svg+xml`, per OWASP ("sanitize *and* isolate", since not
  every SVG vector can be filtered).
- Rationale for a hand-rolled allowlist: the surface is a *tiny deny-by-default
  allowlist with no external refs*, parsed case-correctly and re-serialized —
  small and auditable, unlike a feature-preserving sanitizer; bluemonday's HTML
  tokenizer would lowercase camelCase attributes and isn't built for standalone
  SVG. (bluemonday remains a candidate for the separate **richtext HTML**
  sanitizer, §5.6/§6.)
- The module owns its own tests (malicious-SVG corpus + fuzzing) and its own
  `external_dependencies.md` (currently **zero** third-party deps — pure stdlib).

**WebP (allowed + warned):** the editor shows a non-blocking warning when a WebP
is uploaded (recommend JPEG/PNG/SVG) but still accepts it. Metadata is stripped
best-effort by dropping EXIF/XMP RIFF chunks losslessly (no re-encode; stdlib has
no WebP encoder). Full WebP coverage is a smaller open item (§11).

**Audio / video.** Self-hosted A/V (the native `<video>`/`<audio>` block, §4.2)
is ingested with the same fail-closed, metadata-stripped, content-addressed
discipline as images, by hand-rolled format handlers (no third-party deps):
- **ISO-BMFF (MP4/M4A/MOV):** an atom-tree rewrite drops the `udta` and `meta`
  boxes (GPS `©xyz`, iTunes `ilst` tags, XMP), copying sample data verbatim.
- **MP3:** the leading ID3v2 tag(s) and a trailing ID3v1 tag are removed.
- **WebM/Matroska (EBML):** the `Tags` element and `Info`'s `Title`/`DateUTC` are
  overwritten in place with a zeroed `Void` element of equal length — so
  `Cues`/`SeekHead` byte offsets stay valid and the identifying text is scrubbed.
- **Ogg (Vorbis/Opus):** the comment header's user comments are zeroed and its
  count set to 0 (vendor kept), preserving byte lengths so only each touched
  page's CRC-32 is recomputed (no re-pagination).
- **WAV (RIFF):** metadata chunks (LIST/INFO, `bext`, `iXML`, `_PMX`, embedded
  `id3`) are dropped; audio/structural chunks are kept.
Every handler is **idempotent**, so the build's fail-closed re-verify passes on
already-clean bytes. Containers that can't yet be fully scrubbed (FLAC, AVI, FLV)
are **rejected** with a precise message, never stored with metadata intact.

**Storage split.** Images are small and stored as SQLite blobs (the content
model, §4.1). Audio/video can be large, so their cleaned bytes are **filesystem-
backed**: written to a content-addressed file under a `media/` directory beside
the database, with only a metadata row in SQLite. Server mode streams them via
`http.ServeContent` (Range requests / seeking); the build copies them into the
bundle, re-verified clean. The editor's media library is split by type
(images / video / audio) with per-type search and pagination.

**Broken-reference detection.** A page can reference a self-hosted
`/media/<sha>.<ext>` (image, video, or audio) that is not — or not yet — in the
library. Because `render.MediaRefs` is the single definition of a local media
reference, the same check runs everywhere. The **build** emits exactly the
referenced assets and records a broken reference twice: once build-wide (a single
`Broken media reference …` line per file) and once in each referencing page's
report row (`Broken Media: /path`), so the per-page **Warnings** column traces
each broken file to its page(s) without repeating the page list build-wide. The
**editor** flags the same references **live** in a dedicated *Broken media* panel
(separate from external references) and again on **save**, and — because the
check is grouped by source — marks the exact impacted field: the *Body (Markdown)*
label and each content-block legend gain a **— Broken Media** flag. It is
advisory, never blocking — an author may reference a file before uploading it — so
the reference and its upload can be wired up in either order.

**Testing:** table-driven tests with fixtures carrying known EXIF/GPS tags,
malicious SVGs (script, external `href`, `onload`), and crafted A/V containers
(with embedded titles/GPS/comments) assert the strip/sanitize, length/CRC
invariants, and idempotency.

**Library management (creator).** The library lists items by type (image / video
/ audio), searchable by filename **or note** and paginated server-side (the note
is LEFT JOINed into the search, so it never inflates counts). Beyond copy-path /
copy-Markdown and delete, each row offers:
- **Full-size preview** — the image thumbnail is a real link (`target="_blank"`)
  to the content-addressed file, so a small thumbnail can be inspected at native
  resolution in the browser's own zoomable viewer. No lightbox, no JS.
- **Admin note** — a free-text field per item ("what this file is for", e.g.
  *"hero image for the privacy page"*), edited via an inline no-JS form and saved
  with a plain POST. Notes are kept in a dedicated `media_notes` table keyed by
  the same content address as `assets`/`media`, so one mechanism covers images
  and audio/video; a note is capped (`store.MaxMediaNote`), an empty submit clears
  it, and it is deleted together with the item it annotates. The library search
  matches notes as well as filenames. Notes are editor-only context and never
  ship to the built site.

### 6.2 Search (build-time index, fully client-side)
Search is **pre-indexed by the builder and executed entirely in the browser** —
there is **no search server endpoint**. This is a privacy feature, not just an
implementation choice: the query is matched against a local index, so **search
terms never leave the user's browser** (no query logs, nothing to leak), and it
works on plain static hosting.

- **Single source of truth:** the index is built from the canonical SQLite
  content, **not** by scraping rendered HTML. The same content also produces the
  per-page `<head>` metadata (§6.3); the two are parallel outputs of one source.
- **Artifact:** a global `search/index.json` at a fixed (non-fingerprinted) path
  in the bundle, **lazy-loaded only when the user opens search**, so normal page
  loads pay nothing for it. Because it is rewritten every build, it is served
  **`no-cache`** (revalidated via ETag → a cheap 304 when unchanged) so a newly
  built page appears in search immediately, never masked by a stale cached index.
  (The self-hosted search *script* is fingerprinted and cached immutably.)
- **Index contents (per document):** `url`, `title`, `tags`, `keywords`,
  published date, and a searchable text field. **Default scope = title +
  headings + tags + a summary/excerpt** (small, fast); **full-body text is a
  per-site config option** when deeper recall is wanted. YouTube transcripts
  (§5.8) are included as first-party text, making video content discoverable by
  what was said.
- **Disclosure boundary (critical):** `search/index.json` is a **public** file, so
  it must never contain what the page hides. Two exclusions are enforced, in both
  default and full-body scope: **Community-Members-only (group-gated) blocks are skipped**
  (their plaintext is encrypted out of the page — indexing it would let anyone
  bypass the gate by reading the index, §6.10), and **`noindex` pages are omitted
  entirely** ("hide from search engines" applies to the site's own search too).
  Reveal/hidden blocks (§6.9) are not indexed either. By contrast the page **Body
  and summary are always public** (and searchable — full body only when the option
  is on), so Community-Members-only text belongs in a gated block, never the body.
- **Author + fieldblock keywords:** a page may carry an optional author-supplied
  `keywords` field, folded into its index entry — useful for boosting
  discoverability where the visible text doesn't contain the term. In addition,
  **fieldblocks may contribute extra indexable metadata** to their host page's
  index entry via an optional *index-contribution* hook: a block returns keywords
  and/or searchable text (e.g. the `youtube` and generic `embed` blocks contribute
  their title, keywords, and transcript/notes). Contributions are merged into the
  page's `keywords` / searchable text; they never pull in third-party data (all
  author-entered).
- **Index shape:** start with a **compact document list tokenized/scored in the
  client** by a small self-hosted vanilla-JS matcher (no framework, no CDN);
  move to a prebuilt inverted index only if index size/perf demands it. (Decision
  §11.)
- **Client UI:** self-hosted JS/CSS, keyboard-accessible, ARIA-correct, and CSP-
  friendly (external script file, no inline handlers). Degrades gracefully when
  JS is off (search simply unavailable; the rest of the static site works).
- **Server role:** none beyond serving the static `search/index.json` like any
  other file. (This is why search left the §7.3 dynamic list.)

### 6.3 Per-page metadata (SEO / previews)
Each page's `<head>` carries self-hosted, semantic metadata derived from the same
SQLite source: `title`, meta `description`, tags/keywords, published/updated
dates, canonical URL. No third-party Open Graph/analytics calls. This is distinct
from the global search index (§6.2) — page metadata is for humans/crawlers/link
previews; the index is for in-browser search. No per-page embedded search blob
(the global index already covers every page).

**Per-page noindex.** The editor's edit page has a **"Hide from search engines
(noindex)"** checkbox (default off). When set, the page emits
`<meta name="robots" content="noindex">` and is dropped from `sitemap.xml`. The
directive is stored in the page content (`Content.NoIndex`), so it applies in
both the build and the editor preview.

**Social preview image (`og:image`) — implemented.** A page may set a preview
image for link/social cards: an **optional per-page image** (chosen from the media
library, `/media/<sha>.<ext>` — self-hosted, no third party) plus a **Settings →
SEO site-default** used when a page sets none. When present it emits
`<meta property="og:image" content="<absolute URL>">` (absolute, from the Base URL,
so it needs one) alongside the existing OG tags, and `twitter:card = summary_large_image`
+ `twitter:image`. No auto-generation and **no new dependency** — the operator
supplies the image; a page with neither its own nor a site default simply omits the
tag. (Auto-generated branded cards are deferred — they would add `golang.org/x/image`.)

**sitemap.xml + robots.txt.** A **Settings → SEO** toggle *"Generate sitemap.xml +
robots.txt"* (default off) emits both at the bundle root. The sitemap lists
absolute `<loc>`s (with `<lastmod>` from each page's updated date for content
pages) and is deterministic (sorted by loc). `robots.txt` allows all crawling and
advertises the sitemap (`Sitemap:` directive) — the bundle has no private routes,
so nothing is disallowed. Both are self-hosted static files (no third-party
requests). Because sitemap/robots links must be absolute, an enabled sitemap with
**no Base URL is skipped with a build warning**. A nested toggle *"Include
generated listing pages"* (default on) controls whether the tag pages, feeds
index, and `/classification` are listed; **published content pages are always
included** (minus any marked noindex). **Always excluded:** the `/external/…`
consent facades and every non-HTML artifact (feeds, manifests, assets, media,
`.well-known`).

### 6.4 Theming (built-in default + editor override)
The build ships a **built-in default stylesheet** (`internal/theme`, self-hosted,
system fonts only, light/dark, accessible) that always serves as the **fallback
baseline**. It is emitted as a fingerprinted asset and linked from every page.
Full operator/admin reference: `docs/THEME.md`.

**Header brand.** Every page's header can show a brand at the start, linking to
home, configured in Settings (`build.Config.Brand()` resolves it to a
`render.Brand`): mode `text` (a wordmark — the default, from the site name or a
`Brand text` override), `logo` (a self-hosted image from the Media library),
`logotext` (both, a lockup), or `none`; alignment `start` (brand left, nav right)
or `center` (brand above nav); logo height small/medium/large. The header renders
when a brand OR nav OR search is present. Logos are Media-library paths only
(self-hosted, metadata-stripped, SVG-sanitised, emitted by the normal media
scan). **Accessibility:** text mode is real text (scales, screen-reader friendly);
logo-only requires alt text (validated on save) so the home link is named;
`logotext` renders the image as decorative (`alt=""`) so the wordmark isn't
announced twice; `center` is visual only (source order stays brand → nav).

*Optional dark-mode logo.* A logo brand may carry a second, dark-mode logo
(`LogoSrcDark`, optional; empty = the one logo is used for both themes). When set,
the header emits both images (`pbcssg-logo--light` / `--dark`) and CSS shows one
per theme, keyed to **both** theme signals — `@media (prefers-color-scheme)` for
Auto mode and attribute-qualified `:root[data-theme=…]` so the footer toggle wins
over the OS (mirroring the colour-token model below). `<picture media>` is
deliberately *not* used because it would ignore the toggle. The hidden logo is
`display:none` (removed from the a11y tree, so alt is not announced twice), and
both images are Media-library paths emitted by the normal media scan.

**Light / dark colour model + toggle.** Each scheme's palette is defined once as
`--pbc-light-*` / `--pbc-dark-*` constants; the semantic tokens (`--bg`, `--fg`,
`--accent`, …) are mapped from one palette per mode. The default is **Auto** —
the page follows the visitor's OS via `@media (prefers-color-scheme)` (no state
stored). Every page also carries a footer **toggle** that cycles Auto → Light →
Dark → Auto: an explicit choice sets `<html data-theme="light|dark">` (whose
rules override the media query) and persists in first-party `localStorage`
(`pbcssg-theme`), never sent to a server. The toggle script (`render.ThemeJS`,
fingerprinted `assets/pbcssg-theme.js`) loads **blocking in `<head>` before the
stylesheet** so a stored choice applies before first paint (no flash); it is
same-origin so `default-src 'self'` allows it with no CSP change. It is
progressive-enhancement (the button is `hidden` until the script reveals it) and
wraps storage in `try/catch`, so no-JS / storage-blocked visitors simply get
Auto. Rationale: privacy-hardened browsers (Firefox `resistFingerprinting`,
LibreWolf, Tor Browser) force `prefers-color-scheme` to light, so a manual,
`prefers-color-scheme`-independent override is the only way those visitors can
choose dark. Operator variable overrides apply to the Light/Auto baseline; the
media rule stays plain `:root` so those overrides keep winning in Auto mode, and
operators re-colour a forced mode via `:root[data-theme="dark"]{…}` in Custom CSS.

**Body font.** Text is driven by `--font-sans` (code by `--font-mono`). Settings
exposes a **Body font** dropdown of curated **system-font stacks** (`theme.Fonts`);
the operator picks an ID and the build layers the mapped stack over the default
via `theme.FontCSS`. Because the value is only ever an allowlisted stack — never
operator free-text — it adds **no web-font download / third-party request** (SPEC
§8) and cannot inject CSS. A genuinely custom font needs a self-hosted
`@font-face` (`url(/media/…)`) via the Custom CSS box; external `url()`/`@import`
stay rejected.

**Content width.** Flowing text is held at a comfortable reading measure
(`--measure`, ~80 chars/line) centred in the page, while rich blocks — images,
media players, tables, and code blocks — break out to a wider `--measure-wide`
so they use the available space without stretching text lines. Header, footer,
and all textual blocks share the reading column's edges; both measures are CSS
custom properties an operator can override (§6.4 theme settings).

**The editor can customize the theme** (decided 2026-07-27; to be built with the
creator editor). The customization is stored in the `settings` table and layered
*over* the built-in default (default first, override second), so a site never
ends up unstyled:

- **Theme settings (default UI):** a form over the CSS custom properties the
  default theme already exposes (`--bg`, `--accent`, fonts, measure, …); the
  build injects a `:root{…}` override. Safe; can't break layout.
- **Custom-CSS block (power users):** a raw operator CSS field layered on top.

**Privacy guardrail (required):** any operator-supplied CSS is validated to
**forbid external `url()` and `@import`** — a third-party web font or background
image would silently reintroduce a tracking / supply-chain vector, exactly what
the rest of pbcssg prevents. Custom CSS stays self-hosted-only, consistent with
the hygiene pass (§5.6) and the self-hosted posture (§8). The built-in
`theme.CSS` remains the fallback if the override is empty or rejected.

### 6.5 Syndication feeds (RSS 2.0 + Atom)
Feeds are **defined by path glob in Settings** — a rule is `name | /glob/* |
Optional Title | list`. At build, published pages whose path matches the glob (a
trailing `*` is a prefix match, so `/blog/*` covers everything under `/blog/`) are
collected into **both** `/feeds/<name>.rss` (RSS 2.0) and `/feeds/<name>.atom`
(Atom 1.0), newest-first, capped at 50 items. Each item is title + absolute link
(permalink guid/id) + the page summary + published date. Matching pages get
`<link rel="alternate" type="application/rss+xml"|"application/atom+xml">`
auto-discovery in their `<head>`.

- **Browsable `/feeds/` index**: the optional 4th rule field (`list`, or any of
  `listed`/`yes`/`1`/`true`/`show`) marks a feed to appear on a generated
  `/feeds/` index page, which lists each such feed's title with its RSS + Atom
  links so a visitor can discover them without reading `<head>`. To list a feed
  with no custom title, leave the title column empty: `name | /glob/* | | list`.
  The index is only emitted when at least one feed is marked to be listed (and,
  like the feeds themselves, only when a base URL is set).
- **Deterministic + offline** (§5.4): dates are formatted in UTC and the channel's
  `updated`/`lastBuildDate` is the **newest item's** date, never the wall clock —
  so rebuilding unchanged content yields byte-identical feeds. Feeds are
  self-hosted static XML with absolute links (no third-party requests, no
  tracking); they are skipped with a warning when no base URL is set.
- `/feeds` is a reserved page path (both the `/feeds/` index and the per-feed
  files live under it); the server pins the `application/rss+xml` and
  `application/atom+xml` content types. The `feed` package (RSS/Atom via
  `encoding/xml`) owns its encoder tests. A **video/embed-source feed** (feeds of
  `/external/*` consent pages) is a deferred follow-up; v1 ships pages-by-glob.

### 6.6 Primary navigation
A site-wide nav bar is configured in Settings as a `Label | /path` list and
rendered as a semantic, accessible `<nav>` in the base-layout header of **every**
built page (content, tag pages, `/external/*`). Self-hosted and theme-styled; no
third-party resources. It is carried through `render.Options` so the editor
preview shows the same nav the build emits.

### 6.7 Route-based page-index block
The `index` block (§4.2) lists published pages under a **base route**, resolved at
build time — reusable for a blog index or a multi-page-article table of contents.
Unlike every other block it depends on **other** pages, so the build passes a
site-wide page index (path, title, summary, date, flags) into `render.Options`;
the block filters it, and the editor preview feeds the same index from the store
so preview matches the build. Pure static HTML, no third-party requests.

- **Per-block config:** base route (defaults to the host page's own path), depth
  (1 = direct children, 0 = all descendants), sort (date-desc default / date-asc /
  path / title), item style (titles-only, or title + date + summary), an optional
  heading, and a cap that renders "showing the first N of M" when it truncates.
- **Gating & exclusion (two independent page-level flags in `content_json`):*
  *"This is an index page"* is purely the gate — index blocks render **only** on a
  page marked as an index. *"Exclude from page-index listings"* is the only thing
  that hides a page from listings. They are decoupled, so an index page still
  appears in a parent's listing unless it also sets the exclude flag. The host
  page itself and non-descendants are never listed.
- **Overflow:** v1 caps the count; generated paginated index pages
  (`/blog/page/2`) are a deferred follow-up.

### 6.8 Classification dataset editor
The custom `pbc-classification` dataset (§5.7) is edited in the creator at
`/admin/classification`. It is a **structured per-domain editor**: a card per
domain with a `trust` dropdown, a `verified` date, dropdowns for all eight
signals (Ternary `unknown/no/yes`, Level `unknown/none/low/high`), a third-party
domain count, and evidence/note fields — plus add/remove and a **live grade
badge** per domain.

- **Live grade** comes from `POST /admin/classification/preview`, which validates
  the candidate dataset and returns each domain's grade computed by the library
  (grading stays in Go, so the preview matches a build). It is CSRF-exempt (no
  state change), like the page/scan previews.
- **Raw-JSON escape hatch + no-JS fallback:** the structured editor is a
  self-hosted vanilla-JS layer (`/admin/assets/classify.js`) over a raw-JSON
  `<textarea>` it keeps in sync; the textarea is the submitted field and works
  without JavaScript. Save (`POST /admin/classification`) validates against the
  library's strict parser (rejecting unknown fields / bad dates *before* any
  canonical re-marshal, so a typo is flagged not silently dropped), then persists
  the **canonical** form and hot-applies it (§5.7). No third-party resources.

### 6.9 Deferred-reveal (hidden) block

A `reveal` block hides a short piece of first-party content from the served HTML
until the visitor explicitly acts to show it. Use cases: email addresses (out of
reach of naive harvesters), spoilers, small blurbs that should not appear in
search results or `view-source`, and — with an optional author-set **code** —
lightly gating content without sessions or logins (e.g. a code announced in a
YouTube video).

**Status.** All three modes are **implemented**. Mode A (obfuscation): the `reveal`
block, the per-page key with editor **Rekey**, AES-256-GCM encoding (HKDF
per-block key, deterministic plaintext-derived nonce), and the self-hosted
Web-Crypto decode script. Mode B (the optional **code gate**): an authored `code`
switches the block to a PBKDF2-SHA256 key derived from the visitor's typed code
over a shipped per-block salt — a wrong code fails GCM authentication and is
announced as "Incorrect code," and neither the plaintext nor the code is ever
emitted. **Mode C (Community-Members-only)**: authoring one or more key-group aliases
(§6.10) envelope-encrypts the content (`reveal.EncodeGate`) so it reveals only to a
reader holding a listed group key in their keyring — a real key, not obfuscation —
unlocked on click; it takes precedence over a Mode B code, and the editor preview
shows the content with a Community-Members-only label. Neither the plaintext nor any key is
emitted. Content is `text`, `email` (→ `mailto:`), or `markdown`; markdown is
rendered goldmark-safe HTML, hardened by hygiene and classified into the page
manifest before encryption (see *Markdown kind* below) so it stays inside the
privacy pipeline.

**Two modes.** The optional code is what separates obfuscation from real gating,
so the spec treats them as distinct modes:

| Mode | Secret in the page? | What it actually is |
|------|---------------------|---------------------|
| **A — key only** (no code) | Decoding key ships in the page | **Obfuscation.** Keeps the payload out of source/search/harvesters. No secrecy — any JS-running client recovers it. |
| **B — key + code** | Code is **not** in the page | **Soft gate.** Real protection, but only as strong as the code's entropy and how private it stays. |

**Honest framing (§5.4).** In Mode A the lock and its key travel together — the
key must ship so a single click can decode — so it must be described as
**deferred, obfuscated rendering**, never as "encrypted"/"protected." In Mode B
the page-key is **public** (it ships as a salt), so **secrecy rests entirely on
the code**; the two "halves" are not both secret, and the docs must say so. Mode B
is a *speed bump*, not access control: never present it as login-grade security.
Anything implying a confidentiality guarantee the block does not have is a bug
(§5.4, capability honesty).

**What it delivers (and native elements do not).** `<details>`/`<summary>` and
CSS blur hide content *visually* but leave the plaintext fully present in the
HTML — searchable, copyable, harvestable. The `reveal` block's payload is
**absent from the DOM until reveal**, so it is:

- not present in `view-source`, find-in-page, or a select-all copy before reveal;
- not indexed by search engines (crawlers do not click reveal controls); and
- invisible to regex-over-HTML email harvesters (the common case).

Mode A does **not** defend against a determined scraper that executes the page's
JS — nothing client-side can. Mode B additionally withholds the plaintext from
anyone without the code, bounded by the honest limits in *Threat model* below.

**Data model — page-level key, block-level content/code.**
- **Page level:** a random AES key (`revealKey`), **auto-generated when the page
  is created** and stored in the editor DB. A **Rekey** control in the page editor
  regenerates it. Copying/duplicating a page **generates a fresh key** — key
  material is never inherited across pages.
- **Block level (`content_json`):** the plaintext `content` (authored in the
  editor; the only place plaintext lives at rest), a **required visible `label`**
  for the reveal control (e.g. `Reveal email address`, `Show spoiler: book
  ending`), a `kind` hint (`text` | `email` | `markdown` — `email` reveals the
  value as a `mailto:` link, `markdown` reveals rendered goldmark-safe HTML via
  `innerHTML`), an **optional `code`** (empty ⇒ Mode A; present ⇒ Mode B), and an
  optional `noscriptFallback` string.
- Plaintext is stored server-side in the editor DB like any other block content;
  only ciphertext reaches the built output. The stored `revealKey` is **not a
  secret to guard** (it ships publicly in Mode A / is a public salt in Mode B) —
  the sensitive value is the plaintext, which the DB already holds for all blocks.

**A stored per-page key preserves reproducible builds.** pbcssg builds are
deterministic and `build.json` carries per-file content hashes (§6, §7.1). A
*random per-build* key would churn hashes and break reproducibility; a random key
**stored in the DB** is just another stable input, so the same DB builds
byte-identically. Rekey is an explicit, intended input change.

**Crypto (both sides, stdlib-only, no new dependency).**
- **AES-256-GCM** for the payload. GCM's authentication tag doubles as free code
  validation in Mode B: a wrong code fails the tag and reveals nothing, so the
  code is **never stored or compared in JS** (storing a code hash would just be
  another brute-force target). *Mode A* emits base64 `data-ct` (ciphertext + tag),
  `data-iv`, `data-kind`, and `data-key` (the per-block key). *Mode B* drops
  `data-key` and adds `data-salt`, `data-iters`, and a `data-gated` flag, so the
  decode script prompts for the code and derives the key instead.
- **Key derivation.** Mode A: the AES key is derived per block via
  `HKDF-SHA256(revealKey, info = block_index)` from the page `revealKey`, and the
  derived key ships as `data-key` (the obfuscation is public by design). The GCM
  nonce is `HMAC-SHA256(blockKey, plaintext)` truncated to 96 bits — deterministic
  (stable build to build) yet a function of the message, so editing content
  changes the nonce and no (key, nonce) pair is ever reused for two different
  messages. Mode B: the AES key becomes `PBKDF2(password = code, salt = revealKey
  ‖ block_index, high-iteration, SHA-256)` — the code is required to reconstruct
  it, and `revealKey` serves only as a public per-page salt. **PBKDF2 is the
  default** because Web Crypto supports it natively and Go 1.26 ships
  `crypto/pbkdf2` + `crypto/hkdf` in the **standard library** (added in 1.24), so
  build side and client side run the same KDF with no third-party front-end
  resource and no CSP relaxation. Argon2id would be memory-hard but needs a
  self-hosted, vetted WASM module on the client and `x/crypto` on the Go side; it
  is deferred as optional hardening (the primary use case — a public/low-entropy
  code — does not justify it).
- No plaintext of the payload appears in the built HTML, JSON manifests, or search
  index (a `reveal` block **does not** contribute to the §6.2 index — indexing it
  would defeat the purpose).

**Threat model for Mode B (state these limits plainly).**
- **Offline brute force.** Ciphertext + salt are in the page, so an attacker
  grinds codes locally without needing the video/channel. Strength ≈ code entropy
  × per-guess KDF cost. A short code (a word, a 6-digit number) falls quickly even
  with PBKDF2; a high-entropy passphrase held privately is genuinely strong.
- **No secrecy for a public code.** A code announced in a public video is a
  "you-watched-it"/non-watcher gate and a search/scraper deterrent — not a secret.
- **No revocation without a rebuild.** Once published, ciphertext+salt are public
  and permanent; the first viewer to repost the code makes it public forever.
- **Rekey ≠ code rotation (two different rotations).** *Rekey* rotates the page
  `revealKey`, producing new ciphertext while the **same code still works** — it
  refreshes the obfuscation layer, it does **not** close a leaked gate. To revoke
  a leaked code you **change the `code` and rebuild+redeploy**. The editor UI must
  make this distinction explicit so an operator doesn't click Rekey expecting to
  lock out a shared code.

**Client decode (self-hosted, CSP-clean).** Decoding lives in a self-hosted
script (e.g. `/assets/reveal.js`, alongside the existing theme/facade JS), so it
needs **no CSP relaxation**: `crypto.subtle` is a JS API, not a network request,
and needs no `unsafe-eval`. Server-mode CSP (§7.1) is unchanged. Mode A: on
activation the script decodes `data-ct` with `data-key` and injects the plaintext
(or a `mailto:` link for `kind=email`). Mode B: the control prompts for the code,
derives the key via PBKDF2, attempts GCM decrypt, injects on success, and shows an
inline "incorrect code" message on auth failure (no lockout, no network). Either
way this is the §5.8 facade interaction minus the third-party request — an
explicit reveal contacting no external host — so a `text`/`email` reveal keeps
grade A and its manifest lists no external domain for the block. The client
injects `markdown`-kind payloads with `innerHTML` (safe: the payload is
goldmark-safe, build-hardened HTML authenticated by GCM — no scripts, no raw HTML).

**Markdown kind — stays inside the privacy pipeline.** The `markdown` kind hides
*rendered* content, so it must not become a hole in the model. The build renders
the block's markdown to goldmark-safe HTML, then, before encrypting it, runs the
same two build passes it applies to page HTML:

- **Hygiene on the fragment** (`hygiene.ApplyFragment`): external links/images in
  the revealed markdown get `rel="noopener noreferrer"`, a referrer policy, and
  lazy loading — so a revealed link behaves like any other external link.
- **Classification into the manifest**: the fragment's external references are
  scanned and classified at build (the plaintext is available pre-encryption) and
  folded into the page's privacy manifest and its on-page external-references
  list. A hidden markdown link therefore **cannot smuggle an unclassified
  third-party request past the model** — the domain is disclosed exactly as if the
  link were in the visible page. The editor's live badges and the pre-publish gate
  include these references too (shared `scan`), so the operator sees and
  acknowledges them before publishing. (Rendering + hardening are stripped back to
  goldmark-only in the editor *preview*, which is not the source of truth.)

Note that a revealed external image still loads on reveal — that is an
explicit, user-initiated request (like the §5.8 facades), now honestly disclosed
in the manifest rather than hidden.

**Accessibility (WCAG; hard requirements).**
- The trigger is a real `<button>` (keyboard-focusable; Enter/Space activate),
  never a styled `<span>`/`<div>`.
- It carries `aria-expanded` (`false` → `true` on reveal) and the required
  visible `label`, so the control's purpose is stated in text (not icon/colour
  alone) and the user chooses knowingly.
- The revealed content is injected into a container marked `aria-live="polite"`
  (or focus is moved to it) so a screen reader announces the newly shown text.
- Any reveal animation (e.g. blur→sharp) is gated behind
  `@media (prefers-reduced-motion: reduce)`.
- Mode B: the code prompt is a labelled `<input>` (`type="password"` is optional,
  since the code is often not truly secret) associated with its `<label>`; an
  incorrect-code message is announced via `aria-live` and referenced by
  `aria-describedby` on the input.

**No-JS degradation (explicit tradeoff).** Because the payload is deliberately
absent from the HTML, **no-JS visitors cannot read it** — you cannot have both
"absent from source" and "readable without JS." The build emits a `<noscript>`
note ("This content is hidden until revealed; JavaScript is required to show
it"), substituting the author's `noscriptFallback` when provided (e.g. a
contact-form link in place of an email). The editor surfaces this tradeoff so the
author chooses per block.

**Editor fields:** the hidden `content`, the required visible `label`, the `kind`
selector, the optional `noscriptFallback`, and the **optional `code`** (blank ⇒
Mode A obfuscation; set ⇒ Mode B gate, with the editor hint switching to note it is
a soft gate, not a login). Page-level: the auto-generated `revealKey` with a
**Rekey** button (whose confirm dialog notes that Rekey refreshes obfuscation but
does **not** revoke a shared code — change the code for that). Standard block
validation applies; the `label` defaults to a non-empty value when blank so an
unlabeled reveal control never breaks the a11y contract, and the `code` is capped
at `render.MaxRevealCode` runes — enforced both as a `maxlength` on the editor
field and server-side in the sanitizer.

**Open questions (see §11):** PBKDF2 iteration count (balance client latency vs.
brute-force cost) and whether to expose Argon2id as opt-in hardening later;
whether the `code` is per-block (assumed) or optionally per-page; whether Rekey
should offer "rotate key only" vs. an explicit "revoke code" affordance so the two
rotations are not confused; and whether to offer a whole-block "blur preview"
variant for spoilers that keeps the payload hidden but shows its shape.

---

### 6.10 Group-gated content — key-group envelope + browser keyring — **implemented**

A site-wide access model that gates content **blocks** to one or more named **key
groups**, unlocked by a browser-held **keyring** rather than a per-page code. A
visitor who opens a *gate link* once unlocks every block authorized for that group
across the whole site, on this and later visits.

**Status.** Implemented. The `key_groups` store table + editor manager
(`/admin/keygroups`: create/rename/rotate/delete, splash association, copy gate
link); the per-block `groups` field on the gateable subset; the build's envelope
wrapping (`reveal.EncodeGate`, per-block DEK from the stored page key, wrapped under
each group KEK, unlabeled) with options B/C applied to gated fragments; the
self-hosted `pbcssg-gate.js` keyring (splash deposit, trial-unwrap, lock/forget);
per-group splash pages plus a generic `/unlock/<alias>` fallback deposit page; and
the editor preview showing gated blocks with a group label. All crypto is Web Crypto,
self-hosted, no CSP relaxation.

**Gateable blocks.** The per-block `groups` field applies to the honest, text-shaped
subset plus (caveated) media: `markdown/callout/citation/image/media/code/details/
gallery/index` (`render.IsGateable`). A gated tag-mode gallery is resolved before it is
encrypted (PrepareGallery runs before PrepareGated). Caveats surfaced in the editor: a
gated code block's Copy button does not work (the content is injected after the copy
script runs); a gated gallery/index still links publicly-fetchable /media bytes / public
pages — gating hides placement, not the underlying targets. The **reveal** block is not
in this set (it carries its own encryption); it instead honors group aliases as a native
**Mode C** (see §6.9), so it is never double-encrypted.

**Relationship to §6.9.** This generalizes the reveal block's modes via **envelope
encryption**: the reveal modes are special cases — Mode A ships the data key in the
clear (obfuscation); Mode B derives the key from a typed code; **Mode C** (and the
general block gating here) wraps a per-block data key under one or more group keys
delivered by link into a keyring. If link-gating is built, it should be built as this
model, not as another one-off mode.

**Honest framing (§5.4).** Still **not per-user authentication**. A group key (KEK)
is a **shared bearer key**: anyone who receives a gate link holds that group's key
until it is rotated — no identity, no expiry, no audit, and evicting one Community Member
means rotating the whole group. But because KEKs are **link-delivered, never typed**,
they are full-strength random keys, so — unlike the typed-code gate — the
cryptography is not the weak point: exposure is a leaked link or a readable keyring,
not a guessable key. Describe it as *group gating*, never login/auth.

**Envelope model (DEK/KEK).**
- Each protected block has a random 256-bit **DEK** that encrypts its content
  (AES-256-GCM).
- Each key group (alias) has a random 256-bit **KEK**.
- **Wrapping:** for a block authorizing groups G₁…Gₙ, the build encrypts that block's
  DEK under each Gᵢ's KEK, producing n **wrapped-DEK** blobs.
- The page ships, per block: the content ciphertext (+ iv) and the set of
  wrapped-DEK blobs (+ ivs) — **unlabeled** (no group names or count semantics), so a
  crawler learns nothing about the group structure. **No KEK and no DEK is ever
  emitted in the clear** — only ciphertext and wrapped-DEKs reach the page.

**Runtime unwrap (trial decryption).** The visitor's **keyring** is the set of KEKs
they hold, in first-party `localStorage` (per origin). On load, for each protected
block the reveal script **trial-unwraps** every wrapped-DEK with every keyring KEK; a
GCM authentication success yields the DEK, which decrypts and injects the content
(auto-reveal, no button). No match ⇒ the block stays absent. Trial decryption is why
the page needs no group labels — nothing about who-can-unlock leaks to a non-holder.

**Key delivery — per-group splash page + fragment.** Each key group has its own
**splash page**: an ordinary authored page (title, body, blocks — themed like any
other) that serves as the group's welcome/landing *and* deposits the key. The gate
link is that page's path with the KEK in the URL **fragment**, which browsers never
send to the server: `/members-a#k=<KEK>` (base64url). On load the splash page reads
`location.hash`, stores `{alias: KEK}` in the keyring (the alias comes from the
page's group association, so the fragment carries only the secret), strips the
fragment from the address bar, and shows its welcome content. Because the fragment
is client-only, no server, proxy, log, CDN, or crawler ever sees a KEK — only a
link-holder's browser does. Visited **without** the fragment the splash is just a
normal public page (a welcome/teaser that reveals no key), so it can be shared or
even indexed freely. The keyring persists, so a visitor unlocks once and authorized
blocks render site-wide thereafter.

Making the splash a real page is deliberately **forward-compatible**: once the
key-deposit mechanism is in place, per-tier welcome copy, "here's what you get"
teasers, a proceed-to-Community-Members link, or even group-gated preview blocks *on the
splash itself* are just page content added later — no change to the crypto or the
keyring. A group's splash is **optional**; a group with none falls back to a
generic built-in deposit page the build emits at **`/unlock/<alias>`** (a themed,
`noindex`, key-free public page carrying the deposit marker + gate script), so a
group always has a working gate link. The `/unlock` prefix is reserved so an
authored page cannot collide with it.

**Keys, storage, determinism.** KEKs (per group) and DEKs (per block) are generated
once and **stored in the editor DB** (a new key-groups table + per-block DEK), so the
build stays deterministic and reproducible (stored keys, not per-build random — the
§6.9 reasoning). Wrap/encrypt nonces are derived deterministically (HMAC over the key
+ content / stable ids). **Rotating** a group's KEK re-wraps every DEK authorized for
that group and re-issues the gate link; the content ciphertext (under the DEK) is
unchanged, so rotation is conceptually a KEK-only, wrapped-DEK-only operation.

**Editor — key-group manager** (managed state like §6.8 classification / the media
library). A `/admin/keygroups` panel creates/renames/deletes named groups, shows and
**rotates** each group's KEK, optionally associates each group with a **splash page**
(any page in the tree — that page becomes the group's welcome/landing and key-deposit
point), and copies the group's gate link (`<splash-path>#k=<KEK>`). Per content block,
an optional **`groups`** field lists the group aliases authorized to unlock it —
comma-separated, authored like tags. **Empty ⇒ public** (not gated); one or more
aliases ⇒ the build wraps that block's DEK for each named group. Block-to-group logic
is **any-of (OR)** — holding any listed group's key unlocks the block. This is the
decided default (and the natural behaviour of multi-recipient wrapping); an **all-of
(AND)** option, which would need nested wrapping, is parked (see open questions).

**Which blocks may be group-gated** (the honest subset from §6.9): text-shaped blocks
— `callout`, `citation`, and the reveal/`markdown` block. `image`/`media` may opt in
**with the explicit caveat that the file at `/media/<sha>.<ext>` stays publicly
fetchable** (gating hides the reference/placement, not the bytes; the build must still
emit the referenced media from the pre-encryption plaintext). `youtube`/`embed`/`index`
are **excluded** — their linked/external pages are public, so gating the on-page
element hides nothing real.

**Accessibility, no-JS, CSP.** Auto-reveal on a keyring match injects into the same
`aria-live` region as §6.9. A **"Lock / forget my keys"** control clears the keyring
(essential on shared machines). Non-holders and no-JS visitors get the block's absence
(or an optional authored fallback), as in §6.9. All crypto is Web Crypto
(`crypto.subtle`), self-hosted, no third party — no CSP relaxation (§7.1); a secure
context (https/localhost) is required.

**Privacy pipeline.** A group-gated external link or image is still rendered,
hardened, and **classified into the page manifest at build** (§6.9 options B/C, on the
pre-encryption plaintext), so a hidden group-gated link cannot smuggle an unclassified
third-party request past the model. This is deliberate and protects the *Community Member*: a
holder who unlocks the block will make that third-party request, so the page's privacy
rating must account for it — excluding gated references would both understate the
Community Member's real exposure (a capability-honesty violation, §5.4) and open a hole for a
tracker to be smuggled into gated content undisclosed. The **page rating therefore
includes gated references and is correct precisely because it does** (see the
metadata-disclosure limit below).

**Threat model (state the limits plainly).**
- **Shared bearer keys, not identity** — a KEK unlocks for anyone holding it; no
  per-user auth, expiry, or audit. Evicting one Community Member = rotating the whole group.
- **Strong crypto, link-bound secrecy** — full-random link-delivered KEKs are not
  brute-forceable; the exposure is a leaked link or a readable keyring.
- **localStorage exposure** — XSS could exfiltrate the keyring (mitigated by the
  strict CSP + no inline scripts, §7.1); a shared/public machine keeps it until the
  lock control is used; storage-capable extensions can read it.
- **No revocation without rotation + rebuild + redistribution**, and already-revealed
  content cannot be un-seen.
- **Media bytes stay public** unless the optional, heavier encrypt-the-file extension
  (ship ciphertext, decrypt to a blob URL client-side; loses CDN caching/range
  requests) is used.
- **External references in gated content disclose their domain (metadata leak).** By
  the privacy-pipeline rule above, a third-party link/image inside a gated block has
  its **domain (FQDN only)** listed in the page's external-references section and
  manifest — never the full URL, the path/query, the hidden text, or a marker that the
  reference came from a gated block. Domains are aggregated per-page and merged with
  the visible page's references, so a domain that *also* appears in public content is
  indistinguishable; a domain that appears **only** via gated content does reveal that
  the hidden content references it (bare FQDN, nothing more). This is the honest
  tradeoff — the Community Member's request must be disclosed and rated — not a bug. An admin who
  needs **zero** metadata leakage should **self-host the resource** (first-party, so no
  external reference exists) or avoid third-party links in gated blocks.

**Decided:** any-of (OR) block-to-group logic; a customizable **per-group splash
page** (optional, falling back to a generic confirmation) as the key-deposit point,
so each link is single-group by design (no multi-alias links needed).

**Open questions (see §11):** wrap-nonce derivation and keyring schema/versioning;
whether to add **all-of** (AND) group logic per block (needs nested wrapping);
**per-person** KEKs as an advanced option (wrap the DEK per recipient — real per-user
revocation at the cost of hand-managing a keyserver); the encrypt-the-media-bytes
extension; whether a splash may host its own group-gated preview blocks (forward-compat
teasers); and how KEK rotation is surfaced in the editor (warn that outstanding gate
links die).

---

### 6.11 Favicons / app icons — **implemented**

A dedicated favicon manager, rather than pasting content-addressed media URLs into
Settings: favicons have fixed, browser-expected names served from the site **root**
(`/favicon.ico` is auto-requested; iOS probes `/apple-touch-icon.png`), so they are
stored and served differently from the content-addressed media library.

**Model.** A `favicons` store table keyed by canonical filename holds the cleaned
bytes for each slot: `favicon.svg`, `favicon.ico`, `apple-touch-icon.png`,
`icon-192.png`, `icon-512.png`. All slots are optional.

**Editor** (`/admin/favicon`). Upload panel with one slot per file + a live preview,
plus an optional **theme colour** (`<meta name="theme-color">` / manifest colour).
Uploads reuse the media pipeline: **SVG is sanitized and PNG metadata stripped**
(`asset.Ingest`) before storage; the `.ico` is validated by its magic bytes and kept
as-is (a container of already-clean PNGs from the branding kit's `build-favicons.py`).
pbcssg does **not** rasterize or generate the raster icons (pure-Go, no cgo/librsvg);
it accepts the pre-generated set.

**Build.** Each present asset is emitted at its canonical root path; when a PWA icon
(192/512) is present the build **generates `site.webmanifest`** from the site name +
theme colour + icons (marked `any maskable`). The matching `<head>` links are injected
on **every page** — only for assets that exist:

```
<link rel="icon" href="/favicon.ico" sizes="any">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="/apple-touch-icon.png">
<link rel="manifest" href="/site.webmanifest">
<meta name="theme-color" content="…">
```

**Server / pipeline.** Content types are pinned for `.ico`
(`image/x-icon`) and `.webmanifest` (`application/manifest+json`); any served `.svg`
(including `/favicon.svg`) gets the sandbox CSP as defense in depth. The root favicon
paths are reserved so an authored page cannot collide. First-party favicon links are
same-origin, so hygiene keeps them (it only strips *third-party* favicons) and the
privacy scan sees no external request.

---

### 6.12 Long-form authoring blocks — **implemented**

A cohesive bundle for technical/long-form writing. All build-time, dependency-light,
CSP-clean, and honoring light/dark theming.

**Markdown extensions.** Enable goldmark's **GFM** (tables, strikethrough, task
lists, autolinks) + **footnotes** + heading auto-IDs, still in **safe mode** (no raw
HTML; external refs continue through hygiene + classification). No new dependency —
goldmark is already vendored. Footnotes suit the citation-heavy style; tables are
routinely expected. GFM autolinks (Linkify) promote a bare prose URL to a real
anchor that flows through the same hygiene (rel/referrer hardening) + classification
passes as an authored link; a URL inside a code span or code block stays literal.

**Code block (`code`) — simple, no highlighting, no dependency.** A verbatim code
listing rendered as semantic `<pre><code>`, deliberately *without* syntax
highlighting (keeps it dependency-free). Fields: the **code** text; an optional
**filename** label (shown as a caption bar, e.g. `main.go`); an optional **caption/
comment**; a **line-numbers** toggle (rendered with CSS counters or a gutter, so the
numbers are non-selectable and don't corrupt copy/paste); and a **copy button** —
a small self-hosted script (like the editor's `copy.js` / the reveal script) that
copies the raw code to the clipboard, no third party. Code is HTML-escaped
(never interpreted); language is an optional label only (informs the filename/aria,
not a highlighter).

**Disclosure / FAQ block (`details`).** A native `<details>/<summary>` collapsible:
a **summary** (question/label) + a **markdown body**. No JavaScript; keyboard-
accessible by default. Distinct from the reveal block — this is *visible-but-
collapsed* content (present in source, indexable), not content hidden from source.
A repeatable use (an FAQ list) is just several `details` blocks; consider an optional
`open` default and a grouped variant later.

**Heading anchors + Table of contents (`toc`).** The build assigns stable, slugified
**`id`s to headings** (h2–h4; existing goldmark auto-IDs are kept, block headings get
a de-duplicated slug) and adds a self-anchor link on hover/focus (accessible name
included). A `toc` block renders an auto-generated, nested list of the page's headings
(from the rendered HTML, so it covers markdown + block headings), with an optional
title and a depth option capped to the anchored range (1 → h2 only … 3 → h2–h4, the
default). Pure build-time; no JS. The pass (`render.AnchorsAndTOC`) runs after hygiene
but **before** the external-references listing is injected, so the build-added
"External references" heading is never anchored or pulled into a TOC; it is applied in
both the build and the editor preview so they match.

---

### 6.13 Posts, reading time & related posts — **implemented**

Turns the flat page tree into a blog when the author wants it, without forcing it.

**Post flag.** A per-page **"This is a post/article"** checkbox (stored in
`content_json`, like `IsIndex`/`NoIndex`). It marks a page as a dated article and is
the filter for post-only features below; ordinary pages are unaffected. It does not
change routing.

**Reading time.** A **Settings** toggle *"Show reading time on posts"* (default off).
When on, the build computes an estimate from the page's word count (a fixed **200 wpm**
constant) over the visible authored words (body + block text; hidden reveal/gate
content excluded), ceil'd to at least 1 minute, and renders "~N min read" **just after
the post's first heading** — **posts only**. Placement is done by the existing
post-hygiene anchors/TOC pass (render emits the meta with a marker; the pass relocates
it after the first `<h1>`), so it needs no extra parse and applies to build + preview
alike. Deterministic (word count is a pure function of content).

**Related posts (`related`).** A build-time block that lists other **posts** sharing
the most tags with the current page (excluding itself, dropped when a post is
noindex/excluded), capped by a count option (default **5**, 1–10), ranked by
shared-tag overlap then recency (tags compared by slug so matching agrees with the
tag-page URLs). Reuses the existing tag data (`PageRef`); no per-page config beyond the
block + optional title + count. Renders as an internal-links list (no external
requests) and is **omitted entirely** when the page has no tags or nothing matches.

---

### 6.14 Gallery block + media-library tags — **implemented**

**Media tags.** The media library gains **free-form tags** per item (a new
`media_tags` table keyed by the item's content address, edited in the library like
the existing per-item note). Tags are first-party metadata only; they never affect
the content address or the served bytes.

**Gallery block (`gallery`).** A responsive grid of **self-hosted** images with two
selection modes (author's choice per block):
- **Manual** — a hand-picked, ordered list of media items (curated).
- **By-tag** — every image carrying a chosen media tag, in a defined order
  (e.g. newest-first), so adding a tagged image to the library updates the gallery on
  the next build.

Each image keeps alt text; an optional caption; lazy-loading and the standard image
hygiene apply. A **CSS-only** lightbox (`:target`, no JavaScript) is the default
enlarge affordance. Images route through the normal media-emission + hygiene path, so
the gallery adds no third-party request.

*Implementation.* Manual items are authored as `/media/… | alt | caption` lines. Tag
mode is resolved at build (and in preview) by `build.PrepareGallery` via
`store.AssetsByTag`, with each image's **alt text taken from its library note**; the
resolved `<img src="/media/…">` are then emitted like any other media. Each thumbnail
links to a per-item `#pbcssg-lb-<block>-<n>` overlay; the backdrop/× close by linking
to a non-existent `#…-x`, which clears `:target` without a scroll jump. Columns are
2–4 (default 3) with a 2-column mobile fallback.

---

### 6.15 Privacy-preserving share block (`share`) — **implemented**

A share affordance that fits the no-tracking posture: **no third-party scripts,
buttons, or pixels.** It renders plain first-party controls —
- **Copy link** (self-hosted `copy.js`, the page's canonical URL),
- **Email** (`mailto:` with the title + URL prefilled),
- **Mastodon** (a user-entered instance → the standard `/share?text=…` intent, opened
  only on click; the instance is the visitor's own, so no fixed third party is
  embedded), and optionally a **Fediverse/RSS** pointer.

All actions are user-initiated navigations/`mailto:`; nothing loads on page view, so
the block introduces no external request and needs no classification entry. Honors
light/dark theming and is fully keyboard-accessible.

*Implementation.* Each control is individually toggleable (copy-link/email/Mastodon,
all on by default) plus an optional RSS pointer; a block with nothing enabled renders
nothing. Copy-link and the Mastodon intent are wired by the self-hosted, fingerprinted
`ShareJS` (emitted once, linked only on pages with a share block) reading the live URL
on click; Email is a plain `mailto:` with the page title (subject) and canonical URL
(body) query-escaped at build. Confirmed to add **no external-reference entry**.

### 6.16 Unlisted (hidden) pages — capability-URL Community-Members content — **implemented**

A page can be marked **Unlisted (hidden)**: it is built and served like any page but
is **never referenced by any generated listing or manifest**, so its URL cannot be
found by enumeration. Combined with an **unguessable path** and **gated content
blocks** (§6.10) this gives a Community Members area with defense in depth — the URL is
unlisted (not enumerable) *and* high-entropy (not guessable), and the content is
encrypted (needs the group key); any one failing still leaves two.

**One flag, everything suppressed.** Setting *Unlisted* implies `noindex` and removes
the page from **every** generated reference:
- `sitemap.xml` and the on-site search index (`noindex` behaviour);
- Page-index blocks and related-posts blocks;
- **`/tags/…` pages** and **RSS/Atom feeds** — a hidden page is never listed even if
  it is tagged or matches a feed glob;
- the **privacy manifest** — no per-page `manifest/<path>.json` is emitted (its path
  would itself reveal the page) and the page is left out of the site aggregate
  (`manifest/index.json`).

**On-page transparency is kept.** The page still renders its own **External
References** section (the per-page classification list, §5.7) *in its HTML*, so a
Community Member viewing the page still sees the privacy ratings of its external resources.
Only the *separately published, enumerable* manifest artifacts are suppressed — not
the in-page transparency.

**Unguessable path.** Unlisting stops enumeration, but the page is still served at its
path, so the path must not be guessable. The editor appends a high-entropy random
suffix to the page path on request (e.g. `/members/dispatch-7f3a9c2e5b8d1046`), making
the URL itself a capability — a secret shared with Community Members (or delivered via the
gate-unlock link).

**Caveats (capability-URL model).** A secret URL can leak via `Referer` (mitigated:
the site sends `Referrer-Policy: no-referrer`), browser history, and **server access
logs** — treat request paths as secret (don't log them, or scrub). The content gate is
the backstop. Existence can still leak through artifacts pbcssg does not control
(backups, the deploy tarball, the operator's own analytics).

---

## 7. Server mode

### 7.1 Static serving
Serves the immutable bundle. Correct `Content-Type`, `Cache-Control` per §6,
ETags from content hashes. No directory listing. The consent-gated
`/external/<provider>/<name>` pages (§5.8) are ordinary static pages in the
bundle — served like any other, no special server logic in v1.

**Path confinement.** URL paths are pretty-URL-normalized (`..` rejected) and every
file is opened through an `os.Root` bound to the bundle directory, so opens cannot
escape it via `..`, an absolute path, or a symlink pointing outside — a kernel/runtime
guarantee, not a string check. The deploy's `current` symlink (§7.4) is the root
itself and is resolved when the root is opened, so the atomic-symlink-swap deploy is
unaffected; the confinement applies only to paths beneath the root.

**`build.json` is not served.** The bundle's `build.json` (build metadata plus a
manifest of every file with content hashes) is read by server mode **at startup** for
ETags/CSP, but is **not served to clients** (returns 404) — it has no client purpose,
and its file map would otherwise let anyone enumerate every path in the bundle,
including unlisted pages (§6.16). For direct static serving, block it at the front-end
too: `location = /build.json { return 404; }`.

**Public path is DB-free.** Even in a unified launch (§7.9), the public listener
serves only the immutable bundle and never opens the editing store; the DB belongs
to the admin listener alone. A fault or panic in the editor/build subsystem can
therefore never interrupt public serving.

### 7.2 GPC (full support)
Contextual-application reference (US opt-out vs EU opt-in, the state matrix, and
pbcssg's posture): `docs/GPC.md`.

- **Detect** server-side via the `Sec-GPC: 1` request header (and document the
  client-side `navigator.globalPrivacyControl` for any dynamic JS).
- **Honor**: since the site sells/shares nothing, honoring is a no-op switch, but
  the signal is respected wherever any future data-disclosing path exists; never
  treat absence of the signal as consent.
- **Declare**: serve `/.well-known/gpc.json` → `{ "gpc": true, "lastUpdate":
  "YYYY-MM-DD" }`. Per the GPC spec only `gpc` is required; `lastUpdate` is an
  optional ISO date the operator maintains in settings (bumped when the GPC
  *stance* changes, not per build). It is **validated** at every entry point —
  the editor Settings form and the CLI `-gpc` flag — (blank or `YYYY-MM-DD`, via
  `build.ValidateGPCDate`) and, when blank, **omitted** from the file rather than
  emitted as an invalid empty-string date, so the published JSON is always
  spec-valid.
- **Document**: privacy-policy page content explains the interpretation.

### 7.3 Limited dynamic content (v1 candidate set — confirm)
Kept intentionally small and privacy-preserving; grows in-tree (§2.3):
- `/version` — build/version endpoint for deploy verification.
- `/.well-known/gpc.json` — served from settings.
- **Request-time custom tags** — e.g. a live date/time tag rendered per request
  (as opposed to the build-time frozen variant).
- **Contact/form submission** endpoint (server-side, no third parties) — *if*
  wanted in v1.
- **Decided:** v1 ships `/version` + GPC only; forms/live-tags → v1.1.

**Reserved prefix — implemented (scaffold).** Dynamic routes live under the reserved
path prefix **`/_pbc/`** (`server.ReservedPrefix`), kept out of the static page
namespace, so the public bundle stays fully cacheable and a dynamic route can never
shadow a content page. The public listener dispatches this prefix to an injected
handler (`internal/publicapi`) **before** the GET/HEAD-only static path — the dynamic
layer owns its own methods (it accepts POST) and the static serving path never opens
the runtime store (§7.1). It is mounted only when `-app-db` is set; otherwise a
reserved path is a plain 404. Shipped: `GET /_pbc/health` (liveness, reflects the
`Sec-GPC` signal); `GET /_pbc/comments?path=` (approved comments as JSON,
input-validated); and — when `-public-origin` is set — **Community Member auth**
(`POST /_pbc/auth/register/options|verify`, `POST /_pbc/auth/login/options|verify`,
`POST /_pbc/auth/logout`, `GET /_pbc/auth/me`) plus **member self-service**
(`POST /_pbc/account/alias` sets the account's single public display name — **unique
across accounts, case-insensitively** (anti-impersonation; `""` = anonymous, shared),
back-filled onto all one's comments including pending ones, and freed the moment it is
changed; `POST /_pbc/account/forget` self-erases, anonymize-or-delete). Member
auth mirrors the creator
ceremonies on the **public origin's RP-ID** (the per-origin split, §2.4): usernameless
discoverable credentials, UV required, invite-gated registration (member invites only —
a creator invite is rejected here), a `__Host-`/dev member session cookie, and an
**Origin-header CSRF** check on the state-changing POSTs (the `/verify` steps are also
origin-bound by WebAuthn itself). **Comment posting** is implemented:
`POST /_pbc/comments` (member/moderator-authenticated, Origin-CSRF, length-bounded)
records a comment or a **one-level reply** (`parentId`; the store derives the reply's
page from its parent). A member's comment starts **pending**; a **staff** (moderator or
creator) comment is **auto-approved** (they already hold moderation power). The stored
alias is always the poster's account alias — the client-sent alias is ignored, so the
one-name rule can't be bypassed per post. An author may remove their own comment
(`POST /_pbc/comments/{id}/delete`, ownership-enforced → 404 otherwise): a leaf
hard-deletes, a root that still has replies **tombstones** (blanked + detached, slot
kept so replies keep context). A self-hosted widget (`/_pbc/assets/comments.js` + `.css`)
renders approved comments **threaded one level deep, indented** (XSS-safe via
`textContent`), with a compose box, per-root Reply, a "You" marker and Delete on the
viewer's own comments (a per-viewer flag, never stored), and a single "change name"
control (409 on a taken name). The
on-page **placement block** is implemented too: a `comments` content block
(`render.CommentsJSPath`/`CommentsCSSPath`) emits a `<section data-pbc-comments="/path">`
mount point keyed by the page path, and a page carrying it links the widget from its
fixed same-origin `/_pbc/assets/…` paths — served live by the dynamic layer, never
bundled or fingerprinted (a static-only deploy simply 404s them, and the block shows a
no-JavaScript placeholder). The strict CSP (`default-src 'self'`) already permits the
same-origin script, its fetches, and the stylesheet. At most one comments block renders
per page. **Moderation** is done in the admin editor, not on the public origin: a
creator-gated view at `/admin/moderation` is one filterable, sortable, paginated table
over all comments. A status filter (pending / approved / rejected) selects the working
set; substring search by page path, author alias, and comment body plus a posted-date
range narrow it — all server-side SQL (`QueryComments`/`CountComments`, `LIKE` with
wildcards escaped, whitelisted `ORDER BY`, `LIMIT`/`OFFSET`), never client-side, so it
scales past a single page (default 25/page). Row actions are status-aware: a **pending**
comment approves (public immediately — the widget reads approved comments live, no
rebuild), rejects (kept, hidden), or deletes; an **approved** comment can be unpublished
(→ rejected) or deleted after the fact; a **rejected** comment can be restored (→ approved)
or deleted. The pending-backlog count stays visible on the tab regardless of the active
filter. Its **Accounts** tab (`/admin/moderation/accounts`)
moderates member/moderator accounts — ban (flag + revoke sessions + optional post
removal + burn the creating invite), un-ban, or erase (anonymize-or-delete) — with
creators excluded and refused server-side so the operator cannot lock themselves out. The
same tab manages **moderators**: a private staff **label** to tell them apart, per-moderator
**Can invite / Can ban** grants (default off — the base moderator role is comment
moderation only), a one-click **revoke** of every invite a moderator has outstanding, and
an **"invited by"** line on each member row (the issuing operator, via `issued_by`) so a
wave of accounts traces back to whoever let them in.

**The moderator surface.** Moderators authenticate on the **public** origin (the RP-ID
split puts members and moderators there — §2.4), so their review UI lives there too: a
**session-gated, server-rendered** page at `/_pbc/moderate` plus JSON ceremonies under
`/_pbc/mod/…` (register/login/logout, the passkey manager, the invite mint, member
soft-ban). This is the **one deliberate exception** to the rule that the public origin
serves only JSON and never a live-rendered page: it is not part of the static bundle,
reachable only with a live moderator session, and ships its own strict `default-src 'self'`
CSP. It reuses the shared comment queries, so a moderator gets the same
filter/search/sort/pagination and status-aware actions. A moderator with **Can ban**
soft-bans **members only** (flag + revoke sessions — never a moderator or the creator, and
never erase, which stays with the creator). The same members-only boundary governs comment
moderation: a moderator's approve/reject/delete apply to **member**-authored comments only —
a **staff** comment (another moderator's, or the creator's **Author** post) is shown for
context but **not moderatable** on `/_pbc/moderate` (the creator moderates staff from
`/admin/moderation`). A staff reply can still be removed as `ON DELETE CASCADE` collateral when
the member root it hangs under is deleted — that is an action on the member comment, not the
staff one. With **Can invite** they mint **member-only**,
**30-day**, attributed invites, **capped** at `ModeratorOutstandingInviteCap` outstanding
(the anti-bot control). A signed-in moderator may also comment and reply; their comment is
snapshotted `author_role=moderator`, auto-approved, and rendered with a **Moderator** badge
(a creator's is badged **Author**) — a role snapshot, never an identity (the private label
is never public). The creator, who authenticates on the admin origin and so cannot use the
public widget, comments and replies from **`/admin/moderation`** instead: a per-row Reply and
a page-path composer, both auto-approved and Author-badged, with the creator's display name
under the same account-level uniqueness. Deleting a **root** comment from either moderation
UI cascades to its replies (`ON DELETE CASCADE`), so no reply is orphaned; the delete confirm
warns with the reply count first when a row has replies. Everything else on the public
surface stays bundle-backed; dynamic features remain explicitly enumerated,
input-validated, GPC-aware, and **additive**.

> **Implementation note.** The member and creator ceremonies share one core,
> `internal/authflow`: a `Flow` parameterized by store, verifier, role,
> RP/user labels, and cookie/TTL config owns register/login, the session cookie +
> resolution, and the challenge store. Each origin keeps only a thin wrapper — route
> wiring, its CSRF style (the creator's header token vs the member Origin check), and any
> divergent response — over the same `internal/webauthn` + `internal/appstore` primitives.

> **Note:** search is **not** here — it is a build-time index served as a static
> file and run client-side (§6.2), so it needs no dynamic endpoint.

### 7.4 Deploy (tarball + atomic symlink swap)
- Each release is a **versioned tarball** of the static bundle (§3), built by
  creator mode. It carries `build.json` (version + build number + hashes).
- On the host: unpack to a per-release directory (e.g. `releases/<version>`),
  then **atomically swap a `current` symlink** to it and reload/restart
  `pbcssg server`. Atomic cutover, instant rollback (repoint the symlink).
- **Verify after swap:** compare `/version` against the intended build number
  before considering the deploy done (standing convention).
- **Prune:** keep at most 3 releases; remove older release dirs. In a unified launch
  this is automatic — after a successful **Publish**, release directories beyond the
  retention count (editor Settings → Releases → *Keep releases*, default 3; 0 = keep
  all) are removed, and the live release (whatever `current` resolves to) is never
  deleted regardless of age.
- **Cache freshness:** asset filenames are content-fingerprinted (§6), so a new
  release never serves stale assets; HTML is served `no-cache`/short-TTL.
- No container layer in v1; the server is a single static Go binary + the bundle.
- **Unified launch:** when the editor runs on the host (§7.9), **Publish** performs
  the cutover **in-process** (new `os.Root` + atomic pointer swap) instead of a
  reload/restart. It still writes the versioned `releases/<version>` dir and repoints
  `current`, so this tarball/symlink layout remains the on-disk model and the fallback
  for a cold start or a standalone `pbcssg server`.

### 7.5 Running behind a reverse proxy
Server mode is designed to run **behind a TLS-terminating reverse proxy** that
forwards requests to it; it binds to loopback (or a unix socket) and does **not**
terminate TLS itself. Implications:
- **Trusted forwarded headers:** honor `X-Forwarded-Proto` / `X-Forwarded-For` /
  `X-Real-IP` **only** from a configured `trustedProxies` allowlist, so client
  scheme/IP can't be spoofed by clients. Never trust these from arbitrary sources.
- **GPC passthrough (critical):** the `Sec-GPC` request header must survive the
  proxy hop or server-side GPC detection (§7.2) silently fails. Document that the
  proxy must forward request headers unmodified (the default for a plain reverse
  proxy); a header-stripping config would break GPC. Add a startup self-check/log
  if `Sec-GPC` handling looks misconfigured.
- **Privacy note:** a real client IP arriving via `X-Forwarded-For` is personal
  data — do not log or retain it beyond what's strictly needed (privacy-first;
  §8). Prefer not logging client IPs at all.
- Proxy-agnostic by design: no assumption about *which* proxy is used.

### 7.6 `security.txt` (RFC 9116) — **implemented**

A **Settings** section emits `/.well-known/security.txt` (RFC 9116) — the same
build-and-serve pattern as `/.well-known/gpc.json` (§7.2). Fields: a required
**Contact** (a `mailto:` or https URL) and an **Expires** date (RFC 9116 requires it;
the editor validates it and can default it to, e.g., one year out), plus optional
**Encryption** (a PGP key URL), **Policy**, **Acknowledgments**, and
**Preferred-Languages**. Emitted only when a Contact is set; deterministic; served
with `Content-Type: text/plain`. Fitting for a security-focused operator and trivially
consistent with the existing `.well-known` machinery.

*Implementation.* Contact accepts one entry per line (each a `mailto:`, `tel:`, or
`https://` URI); Expires accepts a date or RFC 3339 timestamp, and when left blank is
**defaulted to one year out at save time** (`build.NormalizeSecurityExpires`), so the
build never reads the wall clock and stays reproducible. Fields are emitted in a fixed
order.

### 7.7 Private metrics dashboard (opt-in, admin-listener) — **implemented**

An **opt-in** operational dashboard exposing **aggregate** traffic metrics — including
a **/16 network heat map** — to the operator on the **admin listener** (§7.9): the
proxied admin origin, network-restricted, or `wget` on the host. It answers "is the
site healthy and what's popular" **without
tracking anyone**: it stores **counters, never events**, and **never retains a client
IP**. Off by default; when off there is zero collection overhead and zero added surface.

The dashboard is an **admin page in the editor** (`/admin/metrics`), rendered in the
admin chrome with a nav link — not a separate handler or port. It is therefore reached
through the **admin listener** (§7.9): the same loopback/SSH-tunnel exposure as the
editor, never the public proxy. `internal/dashboard` is a pure data/image library
(`BuildView`, `RenderHeatmap`); the editor owns the routes and HTML.

**Two independent switches** (both must be on):
- **Master switch** — a build setting `metrics.enabled` (editor Settings → overlaid by
  `LoadBuildConfig` → baked into `build.json`). A bundle built with it off can never
  expose metrics.
- **Admin listener** — the operational `-admin-addr` (with `-db`). Metrics are shown on
  an editor admin page, so they need the admin listener running; a bundle with metrics on
  but no `-admin-addr` collects nothing (the server logs a `WARN`). The public reverse
  proxy has **no vhost** for the admin port — that non-routing *is* the isolation.

When both are on, a **counting middleware** wraps the public site handler and the shared
registry is handed to the editor, which renders `/admin/metrics` (+ `heatmap.png` /
`metrics.json`). The admin listener runs in its own goroutine; a bind failure there is
**non-fatal** (log `WARN`, keep serving the site). The **trusted-proxy allowlist** for
client-IP resolution is an editor Setting (`metrics.trustedProxies`), read at startup to
build the resolver; blank falls back to loopback.

**Data model — counters, not events.** Independent per-dimension counters mean no two
fields are ever linked to one request, so nothing it holds can be correlated back to a
person. The metrics package has **no IP in its API at all**: the middleware resolves the
address to a /16 cell (or an off-grid class) and passes only that, so an address never
crosses into the aggregation layer.
- **Scalar dimensions:** hits, status class (2xx/3xx/4xx/5xx), cache-hit (304), bytes,
  method, and a **coarse User-Agent class** (browser-family/bot/other — classified by the
  middleware, raw UA discarded; a raw UA string is a fingerprint and is never stored).
- **Path popularity:** counted **only for paths present in `build.json`'s file map**;
  every unknown path collapses into one `(not found)` bucket, so scanner noise cannot
  explode cardinality.
- **/16 heat map:** a fixed `[24][65536]uint32` ring — 24 hourly frames, one cell per /16
  (`octet1<<8 | octet2`). ~6.3 MB, **bounded regardless of traffic**. The client IP's top
  16 bits index the cell; the address is discarded in the same call — the grid only ever
  holds /16 counts.
- **Retention:** hourly buckets on a rolling 24-hour window, expired lazily (a slot whose
  stored hour is stale is zeroed on next touch — no background ticker). **In-memory only**
  — nothing at rest, which also matches the fresh-release deploy model (metrics reset each
  deploy; expected). An aggregates-only local store is a possible later continuity upgrade
  — still no events, still no IPs.

**Client-IP trust boundary.** The /16 map is the only feature needing the client IP.
Behind the proxy (§7.5) `RemoteAddr` is the proxy, so the real client is resolved from the
trusted forwarded header **only when the TCP peer is in the `trustedProxies` allowlist**
(default: loopback, the proxy being same-host): walk `X-Forwarded-For` right-to-left, skip
trusted hops, take the first untrusted address; else fall back to `RemoteAddr`.
**Fail-safe:** a misconfigured allowlist yields wrong *attribution* (everything lands in
the proxy's /16 or in `private/unknown`) but **never a privacy leak**, because the address
is discarded regardless.

**Heat-map render.** 256×256 PNG via stdlib `image/png` (x = first octet, y = second):
**log-scale** palette (a linear ramp leaves one hot pixel and 65k black), reserved /
non-routable blocks (RFC 1918, loopback, CGNAT 100.64/10, link-local, multicast, …) masked
a neutral grey. Raster layout in v1; a Hilbert-curve layout is a later cosmetic swap.
Rendered **on demand** (the dashboard has ~1 viewer; an optional few-second cache covers
scripted polling). **IPv6** and **private/unknown** requests do not fit the v4 grid and are
surfaced as **separate honest tallies** ("N% IPv6, not shown"), never forced onto it.

**Surface.** Read-only, **GET/HEAD only**, self-hosted, in the admin chrome, no
third-party anything, no state. Served by the editor on the admin listener:
- `/admin/metrics` — HTML dashboard (scalar tables + embedded heat map).
- `/admin/metrics/heatmap.png` — the rendered grid (the public server never mounts it).
- `/admin/metrics/metrics.json` — the same aggregates for `wget` / scripting on the host.

**Access model.** Metrics live on the **admin listener** (§7.9) and inherit its access
model: loopback bind, fronted by the TLS proxy on the admin origin, network-restricted
(IP allowlist/firewall) and — once §2.4 lands — WebAuthn-gated. **Caveat:** the loopback
port is reachable by any local user/process on the host — fine for a single-operator box;
on a shared host rely on the creator-auth gate (or a bearer token in the interim).

**Privacy invariants (non-negotiable).**
- No client IP is ever logged or persisted (preserves the §7.5 posture); the grid holds
  /16 counts only.
- No cookies, no per-visitor identifier, no cross-request linkage — counters only, so
  there is nothing to re-identify.
- Collects **no personal data**, so GPC (§7.2) has nothing to suppress; documented as such.
- Pure stdlib (`net/http`, `image/png`, `sync`, `time`, `html/template`) — **no new
  dependency**.

**Non-goals (v1).** No historical database, no per-visitor anything, no geo/country
resolution (dropped in favour of the /16 map), no public exposure, no alerting.
Deliberately a small, ephemeral, operator-only view.

### 7.8 Themed error pages (build-emitted, editable) — **implemented**

The build emits **themed static error pages** at the bundle root, served by the
front-end (typically the reverse proxy's `error_page`, §7.5) so a 404/403/429/5xx
keeps the site's look and offers a way back. They reuse the normal page layout
(header brand, nav, footer, theme + toggle, self-hosted CSS), carry `noindex`, and
are **not** in the sitemap or search index. Codes with no response body (1xx, 204,
205, 304) are excluded; the emitted set is:

| File | HTTP codes | Default title |
|------|------------|---------------|
| `400.html` | 400 | Bad request |
| `403.html` | 403 | Access denied |
| `404.html` | 404 | Page not found |
| `429.html` | 429 | Too many requests |
| `50x.html` | 500 / 502 / 503 / 504 | Something went wrong |

- **Editable messages.** Each page's body is **operator Markdown**, edited in a
  dedicated **Error pages** editor section (like Favicons/Classification) and
  **pre-populated** with a sensible default; a blank message falls back to the
  built-in default at build time, so a headless `pbcssg build` always emits
  complete pages. It is rendered through the same safe-mode Markdown + hygiene
  pipeline as page bodies (first-party text, no third-party requests), and each
  page includes a home link.
- **Absolute references (critical).** An error page renders under the *originally
  requested* URL, so every asset/link must be root-absolute (`/assets/…`, `/`).
  pbcssg's fingerprinted asset hrefs already are (`fingerprint` returns `/<name>`),
  so a 404 served at any depth styles correctly.
- **Serving (nginx, direct static).** With the bundle served from disk, map the
  front-end's error handling to the emitted files and keep the real status:
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
  ```
  pbcssg emits only the HTML bodies; the front-end sets the status (`error_page 404
  /404.html` returns 404 — don't rewrite to `=200`).
- **`pbcssg server` on a miss.** The Go server serves the bundle's `/404.html` with
  a 404 status on its own not-found (so a direct/edge-facing server shows the same
  themed page, not Go's plain text), falling back to plain text for a bundle built
  before §7.8. It looks the page up directly (no recursion); behind a proxy this is
  moot — the proxy's `error_page` intercepts first. The Go static server never
  produces 403/429/50x, so those remain purely front-end.
- **Proxy note.** If the proxy instead forwards to `pbcssg server`, the `50x` page
  is shown when that backend is *down*, so its assets could be unavailable — keep
  that page self-contained in that topology. Not a concern for direct static
  serving (assets are always on disk).

**Non-goals.** No per-request detail (attempted URL, request id) — error pages stay
static and privacy-preserving.

### 7.9 Unified launch — admin listener (opt-in, proxied on its own port) — **implemented**

A single `pbcssg server` process may bind, alongside the public listener, a second
**admin listener** that hosts the **editor** (the creator UI, §2.1) and the **metrics
dashboard** (§7.7). This lets authoring run on the host itself, against the host DB,
and gives future dynamic state (Community Member accounts, chat — §2.4) a place to
live. Standalone `pbcssg creator` and `pbcssg build` are unchanged; the admin listener
mounts the same editor handler.

- **Exposure — like the public listener, but on its own origin and network-restricted.**
  The admin listener binds **loopback on its own configurable port** (distinct from the
  public port) and is fronted by the same **TLS-terminating reverse proxy** on a
  **dedicated admin origin** (e.g. `https://admin.<domain>`). It is *not* the public
  origin. Access is restricted at the proxy/host by an **IP allowlist and/or firewall**,
  and — once §2.4 creator auth lands — gated by **WebAuthn** on top. The listener never
  binds a public interface directly; the proxy is the only front door. Opt-in: it binds
  only when an admin address is configured *and* the DB is provided. **Multiple instances
  per host:** each `pbcssg server` instance uses its own public and admin ports/origins
  (nothing is hardcoded), so several sites coexist on one VPS with per-instance,
  per-origin passkey scoping (§2.4). The old dashboard-only listener is generalized into
  this admin listener; standalone metrics keep the separate dashboard listener.
  **SSH/host access remains an operations channel** (unlock the sealed `app.db` volume,
  run `admin bootstrap`, deploy) — it is no longer the admin *auth*.
- **DB required, public path unaffected.** The admin listener opens the SQLite editing
  store; the **public listener never does** (§7.1). The two run as separate
  `http.Server`s in separate goroutines, so an editor/build fault cannot take the
  public site down (recover middleware; the public listener keeps serving the last-good
  bundle) — mirroring the dashboard's existing isolation.
- **Build and Publish are two explicit admin actions; cutover is an in-process
  reload.** *Build* emits a new, immutable `releases/<version>` bundle dir
  (complete-then-swap — never built in place); that dir is also the backup/rollback
  artifact. *Publish* flips the live site over **in-process**: the public listener
  opens a **new `os.Root`** on the new dir, atomically stores it (`atomic.Pointer`),
  and closes the old root — zero-downtime, no restart, no external command. A file
  already opened through the old root keeps its **own** descriptor, so in-flight
  requests finish against the old bundle undisturbed (verified in code, not assumed).
  The `current` symlink is still repointed — atomically, by the process — so a cold
  restart or a **standalone** `pbcssg server` lands on the latest release, but the
  symlink is **no longer the cutover signal.** Because the metrics registry lives in
  the process (§7.7), a Publish does **not** reset the counters. A failed build simply
  never publishes, so the current bundle keeps serving.
- **Rollback.** Publish pointed at an earlier `releases/<version>` dir (and repoint
  `current` back). If the process is down it degrades to today's manual symlink
  repoint + start (§7.4).
- **Data at rest — sealed volume, no full-disk encryption assumed.** The host runs on a
  virtualized VPS whose disk is not physically controlled, and provider-managed
  encryption uses the provider's keys — it will **not** protect a snapshot or backup
  image that leaks. So the runtime store (`app.db`) lives on a **sealed volume**: a
  container/volume encrypted with a key **the operator holds**, unlocked out-of-band
  over the SSH tunnel at start and kept **in RAM only** (never written to the
  unencrypted root, or a snapshot captures it too). Host snapshots/backups then contain
  only ciphertext. **Graceful degradation:** after a reboot the public listener serves
  the bundle immediately (no DB needed) while the comment/login subsystem stays sealed
  until unlocked — the §7.1 split working in the operator's favour. Also **disable or
  encrypt swap** (so the in-RAM key can't page to disk) and **keep backup retention
  short** (so a hard-deleted row can't resurrect from an old image). Encryption is at
  the volume layer, so the Go app stays crypto-free and pure-Go. Baseline regardless:
  restrictive filesystem permissions, no public route, loopback binding. Concrete,
  provider-agnostic setup: `docs/DATA-AT-REST.md`.
- **Auth & network controls.** The admin origin is protected in layers: the proxy's
  **IP allowlist and/or firewall** (a network control the operator configures — the
  primary gate before creator auth exists), the **loopback bind** (never a public
  interface directly), and — once §2.4 lands — **WebAuthn** as the login gate, enforced
  from the moment the origin is exposed (with a public origin there is no SSH backstop,
  so the gate is load-bearing, not defense-in-depth). Optionally add **mTLS** at the
  proxy for the admin vhost. The first creator account is bootstrapped from **host
  access** (`pbcssg admin bootstrap` mints a one-time `role=creator` invite — §2.4);
  because it can always be re-run, the operator is never permanently locked out of admin.

---

## 8. Privacy & security posture (applies throughout)
- **Self-hosted only**: all JS/CSS/fonts/images/APIs local. No CDNs, no external
  frameworks, no third-party front-end resources — in both the creator admin and
  the built site.
- **Strict but deliberate CSP** on both the admin and served site — self-hosted
  sources only, yet not so narrow it breaks extension-injected placeholder UI
  (Privacy Badger compatibility): no `style-src 'none'` or over-tight
  `img-src`/`frame-src`.
- **No analytics/telemetry/tracking** anywhere unless explicitly requested.
- **Secrets** via env vars / secrets manager; never committed, never client-side,
  never logged.
- **Input validation & sanitization** on all editor input and dynamic endpoints;
  output auto-escaped via `html/template`.
- **Logging** at `ERROR/WARN/INFO/DEBUG` on the meaningful flows (build steps,
  classification decisions, publish-gate outcomes, deploy verification); never
  log secrets or personal data.
- **Accessibility**: semantic HTML, keyboard-navigable admin, visible focus,
  contrast, scalable text; badges never rely on color.

---

## 9. Dependencies (to be recorded in `external_dependencies.md`)
Core-first; each vetted before use.
- `go.privatebychoice.com/pbc-classification` — the classifier (own module).
- `golang.org/x/net` — `publicsuffix` (transitive via classify) + `html`
  (link extraction). Already in the tree.
- `modernc.org/sqlite` — **pure-Go** SQLite (no cgo; keeps single-binary
  cross-compile). *Alternative: `mattn/go-sqlite3` (cgo) — rejected for the cgo
  cross-compile cost.* Confirm.
- `github.com/yuin/goldmark` — pure-Go CommonMark for richtext. Confirm vs.
  authoring richtext as sanitized HTML directly (one fewer dependency).
- **Raster metadata stripping (§6.1)** — with the format set now JPEG/PNG, a
  lossless segment/chunk strip is preferable to a lossy re-encode and is simple
  enough to do with stdlib (`image/*`, plus direct JPEG-marker / PNG-chunk
  handling). A 3rd-party module (e.g. the `dsoprea/go-*-image-structure` family)
  is *optional* if a vetted lossless helper is preferred over hand-rolled. WebP
  decode (for the warned-but-allowed path) uses `golang.org/x/image/webp`.
  Decision in §11.
- `go.privatebychoice.com/pbcsvgsanitize` — **own module** (repo
  `github.com/privatebychoice/pbcsvgsanitize`): stdlib-`encoding/xml` deny-by-
  default SVG allowlist sanitizer (§6.1), **zero third-party deps**. Unsanitizable
  SVGs are rejected; no rasterizer (dropped for security).
- Stdlib for everything else: `net/http`, `html/template`, `database/sql`,
  `crypto/sha256`, `encoding/json`, `image/*`, `encoding/xml`.

Target Go **1.26.x**. Creator mode runs on any desktop OS (pure-Go, no cgo);
server mode targets Linux.

---

## 10. v1 milestones (privacy pipeline first)
1. **Classifier integration + URL extraction** — build the `classify.Classifier`
   from settings; extract external URLs from rendered HTML (`x/net/html`);
   populate the `external_links` cache. Table-driven tests.
2. **Privacy manifest emitter** — per-page + site-level JSON alongside a build.
   (Memory-noted "next likely build target.")
3. **Editor annotations + publish gate** — badges/reasons in the admin; warn on
   `D/F/?` before publish.
4. **Auto-rewrites + linking hygiene** — YouTube→nocookie+facade, `rel`,
   `referrerpolicy`, lazy facades, no third-party favicons/preconnect.
5. **Asset ingestion pipeline** — JPEG/PNG metadata strip + SVG allowlist
   sanitizer + WebP warn-but-allow, fail-closed at build (§6.1).
6. **`youtube` consent fieldblock** — inline consent card + generated
   `/external/youtube/<name>` page (transcript, classified description links,
   click-to-load facade) (§5.8).
7. **Client-side search** — build-time `search/index.json` + self-hosted vanilla-
   JS matcher; queries stay in the browser (§6.2).
8. **Minimal build + server** — enough content model + build (incl. a first
   build-time custom tag, e.g. `date`, and per-page `<head>` metadata §6.3) to
   exercise the pipeline end-to-end; server mode serves the bundle with GPC +
   `/version`.

Editor richness, dynamic endpoints (forms/live-tags), and theming deepen in
v1.1+.

---

## 11. Decisions log

### Resolved (v1)
1. **Field/block model** → flat typed fields + one repeatable block list; nested
   StreamField deferred to v1.1. (§4.2)
2. **Library boundary** → per-URL classification stays in `classify` (optional
   small batch helper there); all per-page/site aggregation + emission in
   `pbcssg`. (§5.7)
3. **v1 dynamic surface** → `/version` + GPC only; forms/live-tags to v1.1;
   search is client-side, not a dynamic endpoint. (§7.3)
4. **SQLite driver** → pure-Go `modernc.org/sqlite` (no cgo). (§9)
5. **Richtext** → Markdown via `goldmark`, output HTML sanitized. (§9)
6. **Publish gate** → warn + explicit acknowledge; **consent-gated exemption**
   for a fieldblock's own facade on its `/external/...` page (pre-acknowledged,
   still in manifest, description links gated normally). Optional hard-block mode
   is post-v1. (§5.3)
7. **Deploy** → versioned **tarball + atomic `current` symlink swap**; verify
   `/version` after swap; keep ≤3 releases; no container layer in v1. (§7.4)
8. **Raster strip** → hand-rolled stdlib lossless segment/chunk strip, ICC
   preserved; no 3rd-party dep. (§6.1)
11. **Search index scope** → title + headings + tags + summary + transcripts;
    **plus author- and fieldblock-supplied `keywords`**; full-body text is a
    per-site config toggle. (§6.2)
12. **Search shape** → compact document list + hand-rolled vanilla-JS matcher
    (no dep); inverted index only if size/perf demands. (§6.2)
9. **SVG sanitization** → dedicated **`pbcsvgsanitize`** module (built, zero deps):
   stdlib `encoding/xml` deny-by-default allowlist (no external refs); unsanitizable
   SVGs are **rejected** (no rasterizer — dropped for security). pbcssg fail-closes
   and serves SVGs isolated via `<img>` + CSP. (§6.1/§9)
10. **YouTube consent wording + slug** → Stage-1 card + Stage-2 page default copy
    shipped in §5.8; slug is author-chosen, defaulted from title, unique, stable
    after publish (`videoId` never in the URL). (§5.8)

### Resolved after the first draft
1. **Unified launch** → `server` binds a public listener (bundle-backed, DB-free) plus
   an opt-in, loopback **admin listener** hosting the editor + dashboard; standalone
   `creator`/`build` retained; the host holds the DB. **Implemented.**
   (§2.2, §7.9)
2. **Community Members** → the gated-content audience role is named *Community Members*
   throughout (was "members"). (§6.2, §6.9, §6.10, §6.16)
3. **Accounts & auth** → WebAuthn/passkeys for Community Members, moderators, and
   creators; **implemented**, incl. the per-origin RP-ID split (creators on the admin
   origin, members/moderators on the public origin). (§2.4)
4. **Encryption at rest** → protecting the host DB and credentials (the DEK question) —
   resolved via **volume-layer encryption**, which keeps the app crypto-free (no
   app-managed DEK, no cgo); operator runbook in `docs/DATA-AT-REST.md`. (§7.9)
```
