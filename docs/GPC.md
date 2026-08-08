# GPC (Global Privacy Control) in pbcssg

> **Informational, not legal advice.** Privacy law — especially the US state
> landscape — changes every legislative cycle. The specifics below reflect the
> situation in **mid-2026**; confirm the current state list and the EU treatment
> with privacy counsel before relying on them.

Global Privacy Control (GPC) is a browser signal a visitor's user agent sends to
say "don't sell or share my data." It travels two ways:

- the **`Sec-GPC: 1`** HTTP request header (server-visible on every request), and
- the **`navigator.globalPrivacyControl`** boolean (visible to page JavaScript).

It is **stateless** — every request carries it, so there is no opt-out state to
persist per visitor. Browsers/extensions that send it include Firefox, Brave,
DuckDuckGo, and Privacy Badger.

---

## The key idea: contextual application

GPC is **one signal with a context-dependent meaning**. The same header maps to
different legal rights and different required actions depending on:

1. **Where the visitor is** (a US state, the EU, or somewhere with no law),
2. **Whether your business is in scope** of that jurisdiction's law (size/volume
   thresholds), and
3. **What data activity you perform** (do you *sell/share*, or set non-essential
   cookies, or not?).

You honor GPC "to the extent applicable law requires." The two big regimes work
in opposite directions:

| | **United States** | **Europe (EU/EEA/UK)** |
|---|---|---|
| Model | **Opt-out** | **Opt-in (consent)** |
| Default | Tracking allowed until the user opts out | No non-essential tracking until the user consents |
| GPC's role | A recognized **Universal Opt-Out Mechanism (UOOM)** — a binding "do not sell/share/target" request | Evidence of **refusal / withdrawal of consent** or an **Art. 21 objection** — legally *unsettled* as sufficient consent |
| Absence of GPC | **Never** consent | **Never** consent |

---

## United States — the opt-out model

Where a state law applies and your business is in scope, GPC is a **binding
opt-out** you must honor **without** the visitor taking any extra step, applied
to that browser/device immediately (and linked to a known account if the visitor
is signed in).

- **States requiring recognition (as of Jan 1, 2026, ~12 and growing):**
  California, Colorado, Connecticut, Delaware, Maryland, Minnesota, Montana,
  Nebraska, New Hampshire, New Jersey, Oregon, Texas. (Maryland/Minnesota phase
  in through mid-2026.)
- **What it opts out of** varies by state — "sale", "sharing", and/or "targeted
  advertising"; definitions differ (California's "share" = cross-context
  behavioral advertising).
- **Thresholds matter.** Each law only binds businesses over a size/volume
  threshold (e.g. ~100k consumers, or revenue derived from selling data). Below
  it → not covered.
- **Not every state law mandates a UOOM.** Several comprehensive-law states
  (e.g. Utah, Iowa) do **not** require honoring opt-out signals, and roughly 30
  states have **no** comprehensive privacy law at all.
- **Enforcement is active and automated.** California, Colorado, and Connecticut
  run joint sweeps using tools that scan sites for GPC non-compliance at scale.

**"Work for all 50 states" in practice:** because only ~12 mandate it and
coverage depends on thresholds, the clean strategy is to **honor GPC universally
as a default**, regardless of state. It costs nothing when you don't sell/share
and is future-proof as more states adopt UOOM requirements.

---

## Europe — the opt-in (consent) model

The EU is a different context entirely. GDPR + ePrivacy require **consent
*before*** any non-essential tracking or cookies — an *opt-in*, not an opt-out.
So GPC's "opt-out" framing does not map cleanly:

- GPC can serve as **evidence of a visitor's refusal / withdrawal of consent**,
  or an **Article 21 objection** (profiling, direct marketing).
- But whether an automated signal is *sufficient* under GDPR's "informed,
  specific, unambiguous" consent standard is **not settled** — regulators have
  not fully blessed it, and the GDPR predates GPC. The proposed **Digital
  Omnibus** may formalize automated signals, but "the law must catch up first."
- **You cannot rely on GPC alone** to satisfy GDPR. You still need a compliant
  consent mechanism; honoring GPC (fewer/no banners, no tracking) is permitted
  and privacy-forward, but it is not, by itself, a legal safe harbor.

---

## The recommended architecture: strictest posture, no geolocation

Rather than geolocate each visitor to decide which regime applies — geolocation
is itself data processing — **apply the strictest interpretation to everyone**:

1. **Detect** the signal on every request (`Sec-GPC`) and in the page
   (`navigator.globalPrivacyControl`). Stateless — re-check each request.
2. **Treat GPC as a universal opt-out** (US model) for all visitors.
3. **Require opt-in consent before any consent-gated processing** (EU model) for
   all visitors — don't set non-essential cookies or load third-party content
   until the visitor explicitly agrees.
4. **Never treat the absence of GPC as consent** (universal — both regimes).
5. **Persist the *derived* opt-out** for authenticated users (account-level),
   even though the signal itself is per-request.
6. **Declare & document**: serve `/.well-known/gpc.json` and explain the
   per-jurisdiction interpretation in your privacy policy (for the EU, also your
   lawful basis and consent mechanism).

This satisfies every context at once, with no per-visitor location tracking.

---

## What pbcssg does (compliant almost everywhere by construction)

pbcssg is a **static site generator with no tracking, no ad-tech, and no
non-essential cookies**, so most of the matrix above collapses to a no-op:

- **US:** the site **sells/shares nothing**, so in every state there is nothing
  for GPC to switch off. pbcssg **declares** support via
  `/.well-known/gpc.json` (built into the bundle) and never treats absence of the
  signal as consent (SPEC §7.2). Server mode's honoring of `Sec-GPC` is therefore
  a declared no-op — there is no data-disclosing path to gate.
- **EU:** the **click-to-load consent-gated embeds** *are* the opt-in mechanism —
  YouTube and any allowlisted provider render a self-hosted consent card and load
  **nothing** third-party until the visitor explicitly clicks (SPEC §5.8). That is
  exactly the EU "consent before processing" model, applied globally.
- **No geolocation** is performed, so the "strictest posture everywhere" strategy
  above is already the default.

### The `/.well-known/gpc.json` file

Emitted into every bundle. Per the GPC spec, only `gpc` is required; `lastUpdate`
is an optional ISO date:

```json
{ "gpc": true, "lastUpdate": "2026-07-30" }
```

- Set the date in **Settings → GPC lastUpdate** (or the CLI `-gpc` flag). It is
  validated as blank or `YYYY-MM-DD` (`build.ValidateGPCDate`); when blank it is
  **omitted** rather than emitted as an invalid empty string.
- Bump it when your **GPC stance changes** — not on every build.

---

## The two lines not to cross

pbcssg stays out of the hard parts of this matrix only as long as it does not:

1. **Introduce a genuine sale/share** — an ad pixel, a third-party analytics tag
   without a service-provider/processor contract, or any integration that
   discloses personal data to a third party for cross-context advertising. This
   turns on the **US UOOM obligations**: you must then *actually* suppress that
   activity for any visitor sending `Sec-GPC`, link it to their account, and be
   ready for the automated enforcement sweeps.
2. **Load non-essential third-party content without the click-gate** — bypassing
   the consent-gated embed facade (e.g. an auto-loading iframe, web font, or
   remote script). This turns on the **EU consent obligations**: you must obtain
   valid opt-in consent first.

If either is ever added, revisit this document and the requirements in
"strictest posture" above — and treat GPC honoring as a real runtime behavior,
not a declared no-op.

---

## References

- GPC spec: <https://w3c.github.io/gpc/> · implementation guidance:
  <https://globalprivacycontrol.org/implementation>
- SPEC §7.2 (pbcssg's GPC requirements); §5.8 (consent-gated embeds).
- Code: `internal/build` (`gpcJSON`, `ValidateGPCDate`), `internal/server`
  (declaration + no-op honoring), `internal/creator/settings.go` (the setting).
