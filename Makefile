# Shadow Mock developer targets. See docs/CODING_STANDARDS.md §19.
GOLANGCI_LINT_VERSION ?= v2.4.0
GOVULNCHECK_VERSION   ?= v1.1.4
GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

export GOLANGCI_LINT_VERSION GOVULNCHECK_VERSION

.PHONY: fmt lint lint-fix test acceptance cover-check bench ci

fmt:
	$(GOLANGCI) fmt ./...

lint:
	bash scripts/lint.sh

lint-fix:
	bash scripts/lint.sh --fix

test:
	go test -race -shuffle=on -coverprofile=coverage.out ./...

cover-check: test
	bash scripts/coverage.sh coverage.out

acceptance:
	go test -race ./test/acceptance/... -args -godog.tags='~@wip && ~@perf'

bench:
	go test -run='^$$' -bench=. -benchmem ./internal/engine/... ./internal/match/... ./internal/detect/... ./internal/respond/...

ci: lint cover-check acceptance
