# Sessions, cookies & local storage — privacy and compliance rationale

This note records *why* the project handles client-side state the way it does, so
the reasoning lives next to the spec (see SPEC §2.4 for the normative auth intent
and §6.9 for the client-side reveal feature). It is the regulatory understanding
the design relies on — not formal legal advice; confirm specifics for the
jurisdictions you actually serve.

## The one idea that drives everything

**The law regulates the *purpose* of client-side storage, not the *mechanism*.**

A cookie and a `localStorage` entry are treated identically. Choosing one over
the other changes nothing about consent obligations — it only changes the
security properties. So the mechanism is a *security* decision, and on that axis
an `HttpOnly` session cookie beats a script-readable token.

The common belief that moving auth state from a cookie into `localStorage`
"avoids the cookie law" is a **misconception**. It does not, and it costs you
`HttpOnly`.

## What the rules actually say

### ePrivacy (EU Directive 2002/58/EC Art. 5(3); UK PECR reg. 6) — the "cookie law"

This is the instrument that requires *consent to store or access information on a
user's device*. Two facts matter:

1. **It is technology-neutral.** The EDPB's 2023 *Guidelines on the technical
   scope of Art. 5(3)* confirm it covers **any** storage or access — cookies,
   `localStorage`, `sessionStorage`, IndexedDB, cache, and device fingerprinting
   alike. `localStorage` is squarely in scope; it is not an escape hatch.
2. **Strictly-necessary storage is exempt from consent.** The exemption (Art.
   5(3), second sentence) applies when storage is *strictly necessary for a
   service the user explicitly requested*. The Article 29 Working Party's Opinion
   04/2012 lists **authentication cookies and user-centric security cookies** as
   exempt by name, alongside session-id / user-input storage.

So: an authentication session — cookie **or** token — is the textbook exempt
case. **No consent banner, no opt-in.**

### What *does* require consent

Only **non-exempt** storage: analytics, advertising, cross-site tracking,
profiling, or persistent identifiers used beyond the requested service. This
project uses none of these, so the consent banner never fires — for cookies or
for `localStorage`.

### Privacy policy ≠ consent banner

Transparency (GDPR Art. 13/14) requires a **privacy policy** the moment any
personal data is processed — which happens as soon as there are accounts or
comments, independent of cookies. A strictly-necessary session then needs **one
sentence of disclosure** in that policy, *not* a consent UI. Disclosure and
consent are different obligations; auth sessions need the former, not the latter.

### US / CCPA / GPC

CCPA/CPRA has no cookie-consent regime; its lever is *sale/sharing* and the
**GPC** signal (already honoured — see `docs/GPC.md`). A strictly-necessary,
first-party auth session is not a "sale" or "share," so it adds no obligation
here.

## How the project handles each surface

### A. Client-side reveal / unlock keys — *shipping today* (SPEC §6.9)

Gated content is decrypted **in the browser**; the unlock key is held in
`localStorage` so the visitor need not re-enter it.

- The key is a **content capability, not an identifier** — it identifies no one
  and enables no tracking.
- It is **used entirely client-side and never transmitted to the server**, so the
  server learns nothing from it. This is a best-case privacy story.
- Legal basis: **strictly necessary** for a feature the user actively invoked →
  exempt.
- Residual risk is **security, not compliance**: an XSS bug could read the
  keyring. Mitigated by the strict CSP (SPEC §8) and by the fact that no
  server-side session or identity is exposed by it.

**Decision: unchanged.** Disclose it in the privacy policy for transparency.

### B. Moderators & admin

Per the per-origin RP-ID split (SPEC §2.4), these land on **two origins**:

- **Creators / admin → the proxied admin origin** (loopback listener on its own
  port, fronted by the TLS proxy, IP-allowlisted/firewalled — SPEC §7.9). Single
  operator, no third parties, no tracking optics.
  **Decision:** `__Host-` + `HttpOnly` + `Secure` + `SameSite=Strict` session
  cookie. `HttpOnly` gives the highest-value account real XSS resistance, and on a
  network-restricted admin origin there is no privacy cost to a first-party cookie.
  (The `__Host-`/`Secure` attributes require HTTPS — satisfied by the TLS proxy on
  the admin origin.)

  **CSRF decision (per-process token, deliberately):** `SameSite=Strict` is the
  *primary* CSRF defense — a cross-site forged POST does not carry the admin session
  cookie, so the session gate rejects it (401) before any token check. On top of that,
  every state-changing admin request carries a CSRF token (a hidden `csrf` form field, or
  an `X-CSRF-Token` header on fetch ceremonies). That token is **per-process**, not
  per-session: making it per-session (deriving it from the session cookie) was considered
  and **declined** as disproportionate — it would thread the request through the whole
  render path for a marginal gain, given `SameSite=Strict` already carries the defense.
  Revisit only if the admin ever drops `SameSite=Strict` or gains a same-site sibling
  origin.
- **Moderators → public origin** — they share surface C.

### C. Community members — public origin

With compliance neutral between mechanisms, this is a pure security choice:

| | `__Host-` `HttpOnly` cookie | `localStorage` / `sessionStorage` bearer |
|---|---|---|
| XSS can steal the token | **No** (script can't read it) | Yes (one XSS = stolen session) |
| Works on plain page navigation | Yes | No (fetch + `Authorization` only) |
| CSRF | Needs `SameSite=Strict` (+ token on POSTs) | None (no ambient auth) |
| Consent required | No (exempt) | No (exempt — identical) |
| Tracking optics | None (first-party, opaque, session-scoped) | None |

**Decision:** `__Host-` + `HttpOnly` + `Secure` + `SameSite=Strict` session
cookie carrying only an **opaque** session id (stored **hashed** at rest, per
§2.4), short TTL, with a WebAuthn re-tap on expiry. `SameSite=Strict` closes most
CSRF; add a double-submit / custom-header token on state-changing POSTs for
defense in depth. A `sessionStorage` bearer is used **only** if a given flow
delivers gated content exclusively by fetch-and-inject (like reveal), where there
are no navigations to authenticate.

## What we deliberately never do

- No tracking, analytics, advertising, or profiling cookies or storage.
- No third-party cookies or third-party front-end resources of any kind.
- No device fingerprinting, and no fingerprint used as a session fallback.
- No persistent cross-site identifier; session ids are opaque, first-party, and
  scoped to the authenticated session.

## Summary

| Surface | Mechanism | Legal basis | Why |
|---|---|---|---|
| Reveal unlock keys | `localStorage`, never sent to server | Strictly necessary | Capability, not identity; client-only |
| Admin / creators | `__Host-` `HttpOnly` `SameSite=Strict` cookie | Strictly necessary | XSS resistance; loopback, no optics |
| Community members / moderators | `__Host-` `HttpOnly` `SameSite=Strict` cookie | Strictly necessary | Most secure session transport; exempt |

The rule to remember: **avoid *tracking* storage, not *strictly-necessary*
storage.** Everything above is first-party, purpose-limited, and consent-exempt;
each is disclosed in the privacy policy for transparency.
