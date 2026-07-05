#!/usr/bin/env bash
# Builds the Linux server packages (.deb + .rpm) for Mini-Tun Asymmetric.
# Requires: go, nfpm (go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest).
#
# Usage:  packaging/build.sh          (from repo root or anywhere)
# Output: dist/packages/*.deb, *.rpm
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="$(tr -d '[:space:]' < VERSION)"
export VERSION
OUT="dist/packages"
mkdir -p "$OUT"

echo "[1/3] Building server binaries (linux/amd64, version $VERSION)..."
LDF="-s -w -X main.version=$VERSION"
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOFLAGS=-mod=mod
go build -ldflags "$LDF" -o dist/mini-tun-asymmetric-master ./master
go build -ldflags "$LDF" -o dist/mini-tun-asymmetric-slave  ./slave
go build -ldflags "$LDF" -o dist/mta-setup                  ./server-tui
echo "  binaries built."

# nfpm lives in GOPATH/bin; make sure it's reachable.
NFPM="$(command -v nfpm || echo "$(go env GOPATH)/bin/nfpm")"
if [ ! -x "$NFPM" ] && [ ! -x "${NFPM}.exe" ]; then
  echo "nfpm not found — install with: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest" >&2
  exit 1
fi

echo "[2/3] Building .deb..."
"$NFPM" pkg --config packaging/nfpm.yaml --packager deb --target "$OUT/"

echo "[3/3] Building .rpm..."
"$NFPM" pkg --config packaging/nfpm.yaml --packager rpm --target "$OUT/"

echo ""
echo "=== Server packages built ==="
ls -la "$OUT"/*.deb "$OUT"/*.rpm 2>/dev/null || true
