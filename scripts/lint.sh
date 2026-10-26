#!/usr/bin/env bash
# Runs every check in docs/CODING_STANDARDS.md that golangci-lint does not cover,
# then golangci-lint itself. Exits non-zero if any check fails.
#
# Usage: scripts/lint.sh [--fix]
set -uo pipefail

GOLANGCI_LINT_VERSION="${GOLANGCI_LINT_VERSION:-v2.4.0}"
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.1.4}"
FIX="${1:-}"

cd "$(dirname "$0")/.."
failed=0

section() { printf '\n== %s ==\n' "$1"; }
fail() { printf 'FAIL: %s\n' "$1"; failed=1; }

go_files() {
  find . -type f -name '*.go' \
    -not -path './vendor/*' -not -path './.git/*' -not -name '*.pb.go' -not -name '*_gen.go'
}

if [[ ! -f go.mod ]]; then
  echo "go.mod not found; run from the repository root." >&2
  exit 2
fi

golangci() {
  go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}" "$@"
}

# §3 File names: lower_snake_case.go only.
section "file names"
bad_names=$(go_files | grep -vE '/[a-z0-9]+(_[a-z0-9]+)*\.go$' || true)
if [[ -n "$bad_names" ]]; then
  fail "file names must be lower_snake_case.go:"
  echo "$bad_names"
fi

# §4 Package names: no grab-bag packages.
section "package names"
bad_pkgs=$(go_files | xargs grep -lE '^package (util|utils|common|helpers|helper|misc|base|shared)$' 2>/dev/null || true)
if [[ -n "$bad_pkgs" ]]; then
  fail "package names util/common/helpers/misc/base/shared are not allowed:"
  echo "$bad_pkgs"
fi

# §6 TODO and FIXME must name an owner or an issue: TODO(name) or TODO(#123).
section "TODO owners"
bad_todos=$(go_files | xargs grep -nE '//[[:space:]]*(TODO|FIXME)([^(]|$)' 2>/dev/null || true)
if [[ -n "$bad_todos" ]]; then
  fail "TODO/FIXME must be written as TODO(owner): or TODO(#issue):"
  echo "$bad_todos"
fi

# Lint configuration is valid.
section "golangci-lint config"
golangci config verify || fail "invalid .golangci.yml"

# §3 Formatting, and every linter rule.
section "golangci-lint"
if [[ "$FIX" == "--fix" ]]; then
  golangci fmt ./... || fail "golangci-lint fmt"
  golangci run --fix ./... || fail "golangci-lint findings"
else
  golangci fmt --diff ./... || fail "code is not formatted; run make fmt"
  golangci run ./... || fail "golangci-lint findings"
fi

# §17 Modules are tidy.
section "go mod tidy"
go mod tidy -diff || fail "go.mod/go.sum are not tidy; run go mod tidy"

# §17 Known-vulnerable dependencies.
section "govulncheck"
go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" ./... || fail "govulncheck found vulnerabilities"

echo
if [[ $failed -ne 0 ]]; then
  echo "Lint FAILED. See docs/CODING_STANDARDS.md."
  exit 1
fi
echo "Lint passed."
