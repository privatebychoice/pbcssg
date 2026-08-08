# Security Policy

Thanks for helping keep this project and the people who run it safe. pbcssg is a
privacy-first, self-hosted static site generator — security and privacy are core
goals, so vulnerability reports are genuinely welcome.

## Supported versions

This project is pre-1.0 and moves quickly. Security fixes are released against the
**latest tagged version only**; there are no backports to older `0.x` releases.
Please reproduce on the latest release before reporting.

| Version              | Supported |
| -------------------- | :-------: |
| Latest `0.x` release |    ✅     |
| Any older release    |    ❌     |

## Reporting a vulnerability

**Please do not report security issues in public GitHub issues, pull requests, or
discussions.** Public disclosure before a fix is available puts users at risk.

Instead, use **GitHub's private vulnerability reporting**:

1. Open the **Security** tab of this repository.
2. Click **Report a vulnerability** (under *Advisories*).
3. Complete the private advisory form.

The report stays private between you and the maintainer until a fix is ready.

If private reporting is unavailable, open a public issue containing **only** the
words "security issue — please open a private channel" with **no technical
details**, and wait to be contacted.

### What to include

- The affected version or commit (the output of `pbcssg version` helps).
- A clear description of the issue and its impact.
- Step-by-step reproduction, and a proof-of-concept if you have one.
- Any suggested remediation.

### What to expect

This is a small, best-effort open-source project maintained in spare time:

- **Acknowledgement** — within about 5 business days.
- **Triage and severity assessment** — shortly after acknowledgement.
- **Fix and release** — as quickly as practical for the severity; you will be
  kept updated on progress.
- **Credit** — with your permission you will be credited in the advisory and
  release notes. Anonymous reports are welcome.

This project follows **coordinated disclosure**: please allow a reasonable window
(typically up to 90 days) to ship a fix before publishing any write-up.

## Scope

In scope: code in this repository — the content build pipeline; the admin editor
("creator") mode and its passkey (WebAuthn) authentication; the runtime store
(accounts, credentials, sessions, single-use invites, comments); the public dynamic
endpoints under `/_pbc/` (member sign-in and comment posting, and the session-gated
**moderator** surface at `/_pbc/moderate` + `/_pbc/mod/…`) and the creator moderation
tools; the static `server` mode; media ingestion and sanitization; and the generated
output (CSP, response headers, HTML escaping).

Issues that are especially valued:

- path traversal in the build or the server,
- SVG or media sanitization bypasses (e.g. active content surviving ingest),
- HTML/JS injection into generated pages, or Content-Security-Policy bypasses,
- **authentication, session, or CSRF bypass** in the creator, member, or moderator
  passkey flows, invite-gating or per-origin/role bypass, cross-account/session forgery,
  or **moderator privilege escalation** — e.g. a moderator banning staff or the creator,
  **moderating (approve/reject/delete/unpublish) a staff-authored comment** — another
  moderator's or the creator's — on `/_pbc/moderate` (members-only; the creator moderates
  staff from `/admin/moderation`), acting without the granted capability, exceeding the
  outstanding-invite cap, or a member invite redeeming on the moderator endpoints (or
  vice-versa),
- stored injection via **member-submitted comment content** reaching a page as markup,
- **comment-identity or authorization breaks** in the account-level alias / threading model:
  claiming an alias another account already holds (the case-insensitive uniqueness is the
  anti-impersonation control), a rename that fails to back-fill pending comments (letting one
  member show a moderator two names at once), deleting or replying to a comment that is not
  the caller's own (the self-delete endpoint is ownership-enforced and returns 404 for both a
  missing id and someone else's, so comment ids can't be probed), forging the per-viewer
  "mine" flag for another account, escaping the one-level reply cap, or a member obtaining the
  staff **auto-approve** path (only a `moderator`/`creator` role snapshot may post approved),
- anything that causes a built page to make an **unexpected third-party network
  request** — this project treats that privacy regression as a real security bug.

Generally **not** treated as vulnerabilities:

- The admin editor binds to loopback and is meant to be reached only through the
  operator's own reverse proxy on a dedicated, access-restricted **admin origin**
  (IP allowlist/firewall plus passkey auth — §7.9). Issues that require an
  already-authenticated creator or moderator to attack their own instance, or that
  assume the admin origin is exposed to the public without those controls, are usually
  out of scope. (The **public** dynamic surface — member sign-in, comments, and the
  moderator surface under `/_pbc/` — is internet-facing and multi-user, so it *is* in
  scope, including moderator privilege boundaries.)
- Vulnerabilities in third-party dependencies — please report those upstream. You
  are welcome to also file a private advisory here if this project needs to pin or
  patch around the issue.
- Findings that require a compromised host or an unrealistic configuration.

## Security posture

For context when assessing a report, this project deliberately:

- self-hosts all front-end assets (no third-party CDNs, JS, CSS, or fonts),
- sanitizes uploaded SVGs and strips media metadata on ingest,
- serves a strict Content-Security-Policy on pages in `server` mode,
- supports Global Privacy Control, and
- treats an unexpected external network request from a built page as a bug.

A report demonstrating a break in any of these is valued.
