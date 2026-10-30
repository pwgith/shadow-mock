#!/usr/bin/env bash
# Starts real WireMock servers to compare Shadow Mock against.
#
#   scripts/wiremock-reference.sh start   downloads WireMock (once), then starts five servers:
#                                           :18080  root = a copy of corpus/wiremock
#                                           :18081  no stubs, for the client suite
#                                           :18082  root = a copy of corpus/wiremock, with --enable-stub-cors
#                                           :18083  no stubs, with HTTPS on :18443 and --admin-api-require-https
#                                           :18084  no stubs, with --enable-browser-proxying --trust-all-proxy-targets
#   scripts/wiremock-reference.sh stop    stops them
#
# Then, from the repository root:
#
#   SHADOWMOCK_WIREMOCK_URL=http://localhost:18080 SHADOWMOCK_WIREMOCK_CORS_URL=http://localhost:18082 \
#   SHADOWMOCK_WIREMOCK_TLS_PLAIN_URL=http://localhost:18083 SHADOWMOCK_WIREMOCK_TLS_URL=https://localhost:18443 \
#     go test ./test/conformance -run 'TestDifferentialAgainstRealWireMock|TestAdminAPIMatchesWireMock|TestWireBehaviourMatchesWireMock|TestRecordingMatchesWireMock|TestTLSMatchesWireMock|TestBrowserProxyMatchesWireMock|TestProxyingCorpusMatchesWireMock|TestProxyHeaderRulesDifferFromWireMock'
#   BASE_URL=http://localhost:18081 node --test test/client/compat.test.mjs
#
# Needs Java 11 or later and curl. The jar is checked against the SHA-1 Maven Central publishes
# and is kept outside the repository (WIREMOCK_HOME, default a temp directory).
set -euo pipefail

VERSION="${WIREMOCK_VERSION:-3.13.2}"
SHA1="${WIREMOCK_SHA1:-8bfbad28c85f7b4927694491f2c8030c898c72a9}" # of wiremock-standalone 3.13.2
HOME_DIR="${WIREMOCK_HOME:-${TMPDIR:-/tmp}/shadowmock-wiremock}"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
JAR="$HOME_DIR/wiremock-standalone-$VERSION.jar"
URL="https://repo1.maven.org/maven2/org/wiremock/wiremock-standalone/$VERSION/wiremock-standalone-$VERSION.jar"

sha1_of() { if command -v sha1sum >/dev/null; then sha1sum "$1" | cut -d' ' -f1; else shasum -a 1 "$1" | cut -d' ' -f1; fi; }

fetch() {
  mkdir -p "$HOME_DIR"
  if [[ -f "$JAR" && "$(sha1_of "$JAR")" == "$SHA1" ]]; then return; fi
  echo "downloading WireMock $VERSION"
  curl -fsSL -o "$JAR" "$URL"
  if [[ "$(sha1_of "$JAR")" != "$SHA1" ]]; then
    echo "the downloaded jar does not match the expected SHA-1; refusing to run it" >&2
    rm -f "$JAR"
    exit 1
  fi
}

launch() { # name port root [extra WireMock flags]
  local name="$1" port="$2" root="$3"
  shift 3
  java -jar "$JAR" --port "$port" --root-dir "$root" "$@" >"$HOME_DIR/$name.log" 2>&1 &
  echo $! >"$HOME_DIR/$name.pid"
  for _ in $(seq 1 60); do
    # Any answer will do: a server that requires HTTPS for its admin API answers this with a 403.
    if curl -s -o /dev/null "http://localhost:$port/__admin/version" 2>/dev/null; then
      echo "$name is up on :$port (WireMock $VERSION)"
      return
    fi
    sleep 1
  done
  echo "$name did not start; its log:" >&2
  cat "$HOME_DIR/$name.log" >&2
  exit 1
}

start() {
  fetch
  stop
  rm -rf "$HOME_DIR/corpus-root" "$HOME_DIR/cors-root" "$HOME_DIR/empty-root" "$HOME_DIR/tls-root" "$HOME_DIR/proxy-root"
  cp -r "$REPO/corpus/wiremock" "$HOME_DIR/corpus-root"
  cp -r "$REPO/corpus/wiremock" "$HOME_DIR/cors-root"
  mkdir -p "$HOME_DIR/empty-root" "$HOME_DIR/tls-root" "$HOME_DIR/proxy-root"
  launch corpus 18080 "$HOME_DIR/corpus-root"
  launch empty 18081 "$HOME_DIR/empty-root"
  launch cors 18082 "$HOME_DIR/cors-root" --enable-stub-cors
  launch tls 18083 "$HOME_DIR/tls-root" --https-port 18443 --admin-api-require-https
  launch proxy 18084 "$HOME_DIR/proxy-root" --enable-browser-proxying --trust-all-proxy-targets
}

stop() {
  local f
  for f in "$HOME_DIR"/corpus.pid "$HOME_DIR"/empty.pid "$HOME_DIR"/cors.pid "$HOME_DIR"/tls.pid "$HOME_DIR"/proxy.pid; do
    [[ -f "$f" ]] || continue
    kill "$(cat "$f")" 2>/dev/null || true
    rm -f "$f"
  done
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  *) echo "usage: $0 start|stop" >&2; exit 2 ;;
esac
