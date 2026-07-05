#!/usr/bin/env bash
# Builds the Windows installer (.exe) from the Flutter client bundle using Inno
# Setup. Requires Inno Setup 6 (https://jrsoftware.org/isdl.php) and a bundle at
# dist/flutter-app/ (run packaging/build-windows.sh first).
#
# Usage:  packaging/build-installer.sh
# Output: dist/packages/mini-tun-asymmetric-client-<ver>-windows-x64-setup.exe
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="$(tr -d '[:space:]' < VERSION)"

if [ ! -d dist/flutter-app ]; then
  echo "dist/flutter-app not found — run packaging/build-windows.sh first." >&2
  exit 1
fi

# Locate ISCC (Inno Setup command-line compiler).
ISCC=""
for c in \
  "$(command -v iscc 2>/dev/null || true)" \
  "/c/Program Files (x86)/Inno Setup 6/ISCC.exe" \
  "/c/Program Files/Inno Setup 6/ISCC.exe"; do
  if [ -n "$c" ] && [ -x "$c" ]; then ISCC="$c"; break; fi
done

if [ -z "$ISCC" ]; then
  echo "Inno Setup (ISCC.exe) not found." >&2
  echo "Install it from https://jrsoftware.org/isdl.php, then re-run." >&2
  exit 1
fi

echo "Building installer with $ISCC (version $VERSION)..."
"$ISCC" "//DMyAppVersion=$VERSION" "$(cygpath -w packaging/windows/installer.iss)"

echo ""
echo "=== Installer built ==="
ls -la dist/packages/*setup.exe 2>/dev/null || true
