#!/usr/bin/env bash
# Builds the Windows client: the Flutter GUI + the Go sidecar agent + wintun,
# assembled into dist/flutter-app/, then zipped as a portable release.
# Run from Git Bash on Windows with flutter + go on PATH.
#
# Usage:  packaging/build-windows.sh
# Output: dist/flutter-app/ (unpacked) and
#         dist/packages/mini-tun-asymmetric-client-<ver>-windows-x64-portable.zip
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="$(tr -d '[:space:]' < VERSION)"
BUNDLE="dist/flutter-app"
OUT="dist/packages"
mkdir -p "$OUT"

echo "[1/4] Building Go sidecar agent (windows/amd64, version $VERSION)..."
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOFLAGS=-mod=mod \
  go build -ldflags "-H=windowsgui -s -w -X main.version=$VERSION" \
  -o dist/mini-tun-asymmetric-agent.exe ./cmd/mini-tun-asymmetric-agent

echo "[2/4] Building Flutter GUI (release)..."
export PATH="$PATH:/c/flutter/bin"
( cd client-flutter && flutter build windows --release --dart-define=ARROW_SCALE_X100=200 )

echo "[3/4] Assembling bundle -> $BUNDLE ..."
REL="client-flutter/build/windows/x64/runner/Release"
rm -rf "$BUNDLE"
mkdir -p "$BUNDLE"
cp -r "$REL"/* "$BUNDLE/"
cp dist/mini-tun-asymmetric-agent.exe "$BUNDLE/"
# wintun.dll: reuse the one already in dist/ (checked in), else find it.
if [ -f dist/wintun.dll ]; then
  cp dist/wintun.dll "$BUNDLE/"
else
  find . -name wintun.dll -not -path '*/build/*' | head -1 | xargs -I{} cp {} "$BUNDLE/"
fi

echo "[4/4] Zipping portable release..."
ZIP="$OUT/mini-tun-asymmetric-client-${VERSION}-windows-x64-portable.zip"
rm -f "$ZIP"
# Use PowerShell Compress-Archive (always present on Windows).
powershell.exe -NoProfile -Command \
  "Compress-Archive -Path '$(cygpath -w "$BUNDLE")\*' -DestinationPath '$(cygpath -w "$ZIP")' -Force"

echo ""
echo "=== Windows client built ==="
echo "  bundle: $BUNDLE"
echo "  portable zip: $ZIP"
ls -la "$ZIP" 2>/dev/null || true
