#!/usr/bin/env bash
# Enforces the coverage targets in docs/CODING_STANDARDS.md §16.
# Expects coverage.out produced by: go test -race -coverprofile=coverage.out ./...
set -euo pipefail

cd "$(dirname "$0")/.."
PROFILE="${1:-coverage.out}"
MODULE="github.com/pwgith/shadow-mock"
OVERALL_MIN=70
CORE_MIN=85
CORE_PACKAGES=(internal/engine internal/match internal/detect internal/counter)

failed=0

overall=$(go tool cover -func="$PROFILE" | awk '/^total:/ { sub("%", "", $3); print $3 }')
printf 'overall coverage: %s%% (minimum %s%%)\n' "$overall" "$OVERALL_MIN"
awk -v c="$overall" -v m="$OVERALL_MIN" 'BEGIN { exit !(c >= m) }' || { echo "FAIL: overall coverage below ${OVERALL_MIN}%"; failed=1; }

for pkg in "${CORE_PACKAGES[@]}"; do
  # With -coverpkg every test binary reports every block, so merge duplicates first: a block
  # is covered if any binary covered it.
  pct=$(awk -v p="${MODULE}/${pkg}/" '
    NR > 1 && index($1, p) == 1 {
      stmts[$1] = $2; if ($3 > 0) hit[$1] = 1
    }
    END {
      for (b in stmts) { total += stmts[b]; if (b in hit) covered += stmts[b] }
      if (total == 0) print "0"; else printf "%.1f", covered * 100 / total
    }' "$PROFILE")
  printf '%-20s %s%% (minimum %s%%)\n' "$pkg" "$pct" "$CORE_MIN"
  awk -v c="$pct" -v m="$CORE_MIN" 'BEGIN { exit !(c >= m) }' || { echo "FAIL: ${pkg} coverage below ${CORE_MIN}%"; failed=1; }
done

exit $failed
