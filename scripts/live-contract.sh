#!/usr/bin/env bash
# Runs the live OpenAPI contract suite (test/contract) against a freshly built engine.
#
# It builds cmd/shadowmock, starts it over a temporary copy of the corpus, waits until it
# is healthy, runs TestLiveContract (which calls every operation in api/openapi.yaml and
# checks each request and response against the spec), and stops the engine whatever happens.
#
# Usage: scripts/live-contract.sh        (PORT overrides the default 18080)
set -euo pipefail

cd "$(dirname "$0")/.."
PORT="${PORT:-18080}"
WORK="$(mktemp -d)"
ENGINE_PID=""

cleanup() {
  if [[ -n "$ENGINE_PID" ]]; then
    kill "$ENGINE_PID" 2>/dev/null || true
    wait "$ENGINE_PID" 2>/dev/null || true # let it release the directory before it is removed
  fi
  rm -rf "$WORK" || true
}
trap cleanup EXIT

go build -o "$WORK/shadowmock" ./cmd/shadowmock

# The engine may write to its root, so give it a copy; the corpus gives it real mappings to list.
mkdir -p "$WORK/root"
cp -r corpus/wiremock/mappings corpus/wiremock/__files corpus/shadow-mock/mapping-sets "$WORK/root/"

"$WORK/shadowmock" --root-dir "$WORK/root" --port "$PORT" >"$WORK/engine.log" 2>&1 &
ENGINE_PID=$!

for _ in $(seq 1 50); do
  if curl -fs "http://localhost:$PORT/__health" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$ENGINE_PID" 2>/dev/null; then
    echo "the engine exited before it became healthy:" >&2
    cat "$WORK/engine.log" >&2
    exit 1
  fi
  sleep 0.2
done
curl -fs "http://localhost:$PORT/__health" >/dev/null || { echo "the engine did not become healthy" >&2; cat "$WORK/engine.log" >&2; exit 1; }

SHADOWMOCK_CONTRACT_BASE_URL="http://localhost:$PORT" go test -count=1 -v -run 'TestLiveContract' ./test/contract/ | tee "$WORK/live.log" | grep -E '^(---|===|ok|FAIL|PASS)' | grep -v '=== RUN' || true

# `go test` prints PASS/FAIL; fail the script unless the suite actually ran and passed.
if grep -q '^--- FAIL' "$WORK/live.log" || ! grep -q '^--- PASS: TestLiveContract' "$WORK/live.log"; then
  echo "the live contract suite failed" >&2
  grep -E 'contract violation|live_test.go' "$WORK/live.log" >&2 || true
  exit 1
fi
echo "live contract suite passed against the built engine"
