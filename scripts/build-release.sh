#!/usr/bin/env bash
# Cross-compiles cmd/shadowmock for every supported OS and CPU, packs each binary, and writes
# SHA256SUMS. The engine is pure Go (no cgo), so one machine builds every target.
#
# Usage: scripts/build-release.sh [--list] [VERSION]
#   VERSION  a semantic version without the leading v (0.2.0, 0.2.0-rc.1, 0.2.0-edge.abc12345);
#            defaults to `git describe --tags --always --dirty` with any leading v removed
#   output   dist/shadowmock_<version>_<os>_<arch>.tar.gz (.zip for Windows) and dist/SHA256SUMS
set -euo pipefail

# os/arch[/goarm] — the main operating systems and chipsets.
TARGETS=(
  linux/amd64
  linux/arm64
  linux/arm/7
  darwin/amd64
  darwin/arm64
  windows/amd64
  windows/arm64
)

if [[ "${1:-}" == "--list" ]]; then
  printf '%s\n' "${TARGETS[@]}"
  exit 0
fi

cd "$(dirname "$0")/.."
VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
DIST=dist
rm -rf "$DIST"
mkdir -p "$DIST"

for target in "${TARGETS[@]}"; do
  IFS=/ read -r goos goarch goarm <<<"$target"
  arch="$goarch"
  [[ -n "${goarm:-}" ]] && arch="${goarch}v${goarm}"
  name="shadowmock_${VERSION}_${goos}_${arch}"
  bin="shadowmock"
  [[ "$goos" == "windows" ]] && bin="shadowmock.exe"

  stage="$DIST/$name"
  mkdir -p "$stage"
  echo "building $name"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="${goarm:-}" \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "$stage/$bin" ./cmd/shadowmock

  if [[ "$goos" == "windows" ]]; then
    if command -v zip >/dev/null; then
      (cd "$DIST" && zip -qr "$name.zip" "$name")
    elif command -v powershell.exe >/dev/null; then # Git Bash on Windows has no zip
      powershell.exe -NoProfile -Command "Compress-Archive -Path '$DIST/$name' -DestinationPath '$DIST/$name.zip'"
    else
      echo "zip (or PowerShell) is required to pack Windows builds" >&2
      exit 1
    fi
  else
    tar -C "$DIST" -czf "$DIST/$name.tar.gz" "$name"
  fi
  rm -rf "$stage"
done

(cd "$DIST" && sha256sum shadowmock_* > SHA256SUMS)
echo
echo "built ${#TARGETS[@]} targets in $DIST/ (version $VERSION)"
cat "$DIST/SHA256SUMS"
