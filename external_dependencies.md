# External Dependencies

pbcssg is core-first. It has no runtime third-party network calls of its own; the
build runs offline (the classifier dataset is embedded in pbc-classification, and
no third-party URLs are fetched at build time — SPEC §5.4). All dependencies are
pure Go (no cgo), keeping the tool a single cross-compiled binary.

## Direct

Versions below are the exact `go.mod` requirements; update this table whenever a
dependency version changes (`make check` re-runs `govulncheck` over the graph).

| Name | Version | Project URL | Purpose | Security / privacy notes |
|------|---------|-------------|---------|--------------------------|
| `go.privatebychoice.com/pbc-classification` | `v0.0.0-20260730135944-80efcd7146bf` | https://github.com/privatebychoice/pbc-classification | Privacy-badge classification of external URLs | Own module. Offline, embedded dataset. |
| `golang.org/x/net` | `v0.57.0` | https://pkg.go.dev/golang.org/x/net | `html` (parse rendered pages for link extraction + hygiene) | Go team ("extended standard library"). |
| `modernc.org/sqlite` | `v1.54.0` | https://pkg.go.dev/modernc.org/sqlite | Content store (SPEC §4) — **pure-Go** SQLite, no cgo | Chosen over `mattn/go-sqlite3` to avoid cgo/cross-compile cost (decision #4). Pulls a pure-Go transitive tree (`modernc.org/libc,memory,mathutil`, `golang.org/x/sys`, small helpers — see Indirect below); all Go, govulncheck-clean as of 2026-07-27. Local file DB only, never network. |
| `github.com/yuin/goldmark` | `v1.8.4` | https://github.com/yuin/goldmark | Markdown → HTML for the render layer (decision #5) | Pure-Go CommonMark, zero dependencies. Used in **safe mode** (raw HTML not emitted, dangerous URLs neutralized) with the bundled GFM + footnote extensions enabled (no new module), so rendered output stays sanitized by construction (SPEC §6, §6.12). |
| `go.privatebychoice.com/pbcsvgsanitize` | `v0.1.0` | https://github.com/privatebychoice/pbcsvgsanitize | SVG sanitization in asset ingestion (SPEC §6.1) | Own module. Pure stdlib (`encoding/xml`), deny-by-default allowlist, no external references, no network. JPEG/PNG/WebP metadata stripping is hand-rolled in `internal/asset` (no dependency). |
| `github.com/fxamacker/cbor/v2` | `v2.9.2` | https://github.com/fxamacker/cbor | CBOR decoding for WebAuthn/passkey verification (§2.4): the `attestationObject` and COSE public keys are CBOR | Pure-Go, no cgo. One tiny leaf transitive dep (`github.com/x448/float16`); no network. Chosen (decision: hand-roll WebAuthn verification over `go-webauthn/webauthn`) to keep the supply chain to 2 leaf modules vs 7 — it is the same CBOR library `go-webauthn` uses. Signature verification uses **stdlib** crypto (`crypto/ecdsa`, `crypto/ed25519`, `crypto/sha256`); only the security-critical checklist (challenge/origin/RP-ID hash/flags/signature/counter) is ours, and adversarially tested. |

## Indirect (transitive)

All pure-Go, pulled in only by the direct modules above — none is imported by
pbcssg directly. Versions are the current `go.mod` selections.

| Name | Version | Pulled in by |
|------|---------|--------------|
| `modernc.org/libc` | `v1.74.1` | `modernc.org/sqlite` |
| `modernc.org/memory` | `v1.11.0` | `modernc.org/sqlite` |
| `modernc.org/mathutil` | `v1.7.1` | `modernc.org/sqlite` |
| `github.com/dustin/go-humanize` | `v1.0.1` | `modernc.org/sqlite` |
| `github.com/google/uuid` | `v1.6.0` | `modernc.org/sqlite` |
| `github.com/mattn/go-isatty` | `v0.0.20` | `modernc.org/sqlite` |
| `github.com/ncruces/go-strftime` | `v1.0.0` | `modernc.org/sqlite` |
| `github.com/remyoudompheng/bigfft` | `v0.0.0-20230129092748-24d4a6f8daec` | `modernc.org/sqlite` |
| `golang.org/x/sys` | `v0.47.0` | `modernc.org/sqlite` (low-level syscalls) |
| `github.com/x448/float16` | `v0.8.4` | `github.com/fxamacker/cbor/v2` |

The opt-in server-mode metrics dashboard (SPEC §7.7) adds **no dependency**: it is
pure stdlib (`net/http`, `image/png`, `html/template`, `sync`, `time`), stores
aggregate counters in memory, and never retains a client IP.

## Vetting

`make check` runs `govulncheck` over the whole dependency graph (including the
modernc tree). Re-run after any dependency change.
