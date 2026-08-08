# Makefile — local quality gate. Requires Go 1.26+.
#
# Run `make check` before pushing. Targets that need a Go module no-op cleanly in
# a repo that doesn't have one yet (e.g. a docs-only repo), so the same Makefile
# is safe to drop into every project.
#
# GitHub Actions CI (push/PR + a scheduled govulncheck) is a separate, later
# addition — this file is the local gate.

GO ?= go
# govulncheck is a tool run via `go run <pkg>@version`; this never adds it to the
# module's go.mod / go.sum. Needs network to fetch the tool and the vuln DB.
GOVULNCHECK ?= golang.org/x/vuln/cmd/govulncheck@latest

.DEFAULT_GOAL := help
.PHONY: check fmt fmt-check vet test race cover vuln tidy help

## check: full local gate — fmt-check, vet, test, vuln
check: fmt-check vet test vuln

## fmt: format all Go code in place
fmt:
	gofmt -w .

## fmt-check: fail if any Go code is not gofmt-clean
fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then \
		echo "gofmt: these files need formatting:"; echo "$$out"; exit 1; fi

## vet: run go vet
vet:
	@if [ -f go.mod ]; then $(GO) vet ./...; else echo "vet: no go.mod yet — skipping"; fi

## test: run the test suite
test:
	@if [ -f go.mod ]; then $(GO) test ./...; else echo "test: no go.mod yet — skipping"; fi

## race: run tests with the race detector (needs a C toolchain)
race:
	@if [ -f go.mod ]; then $(GO) test -race ./...; else echo "race: no go.mod yet — skipping"; fi

## cover: run tests and print coverage
cover:
	@if [ -f go.mod ]; then $(GO) test -cover ./...; else echo "cover: no go.mod yet — skipping"; fi

## vuln: scan for known vulnerabilities (Go vuln DB; needs network)
vuln:
	@if [ -f go.mod ]; then $(GO) run $(GOVULNCHECK) ./...; else echo "vuln: no go.mod yet — skipping"; fi

## tidy: sync go.mod / go.sum
tidy:
	@if [ -f go.mod ]; then $(GO) mod tidy; else echo "tidy: no go.mod yet — skipping"; fi

## help: list available targets
help:
	@echo "Targets:"; grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'
