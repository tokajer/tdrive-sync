#!/usr/bin/env bash
# Builds the tdrive-sync AppImage.
#
#   ./build-appimage.sh
#
# Environment overrides:
#   GO=/path/to/go            use a specific Go toolchain (default: `go` in PATH)
#   RCLONE_BIN=/path/rclone   use an existing rclone binary instead of downloading
#   APPIMAGETOOL=/path/tool   use an existing appimagetool instead of downloading
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
BUILD="$ROOT/build"
DIST="$ROOT/dist"
GO="${GO:-go}"

case "$(uname -m)" in
  x86_64)  ARCH=x86_64 ;;
  aarch64|arm64) ARCH=aarch64 ;;
  *) echo "Nicht unterstützte Architektur: $(uname -m)" >&2; exit 1 ;;
esac

mkdir -p "$BUILD" "$DIST"

# Version bestimmen: explizit via $VERSION, sonst der Git-Tag beim GitHub-Build
# (GITHUB_REF_TYPE=tag), sonst `git describe`, sonst local-dev-build.
if [ -z "${VERSION:-}" ]; then
  if [ "${GITHUB_REF_TYPE:-}" = "tag" ] && [ -n "${GITHUB_REF_NAME:-}" ]; then
    VERSION="$GITHUB_REF_NAME"
  else
    VERSION="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo local-dev-build)"
  fi
fi
echo ">> Version: $VERSION"

echo ">> Baue tdrive-sync (Go)…"
# cgo wird für das native Einstellungs-Fenster (WebKitGTK via dlopen) benötigt.
CGO_ENABLED=1 "$GO" build -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o "$BUILD/tdrive-sync" ./cmd/tdrive-sync

# --- rclone ---
RCLONE_BIN="${RCLONE_BIN:-$BUILD/rclone}"
if [ ! -x "$RCLONE_BIN" ]; then
  echo ">> Lade rclone…"
  case "$ARCH" in
    x86_64)  RC_ARCH=amd64 ;;
    aarch64) RC_ARCH=arm64 ;;
  esac
  curl -fsSL -o "$BUILD/rclone.zip" "https://downloads.rclone.org/rclone-current-linux-${RC_ARCH}.zip"
  ( cd "$BUILD" && unzip -oq rclone.zip && cp rclone-*-linux-${RC_ARCH}/rclone rclone && rm -rf rclone-*-linux-${RC_ARCH} rclone.zip )
  chmod +x "$RCLONE_BIN"
fi

# --- appimagetool ---
APPIMAGETOOL="${APPIMAGETOOL:-$BUILD/appimagetool}"
if [ ! -x "$APPIMAGETOOL" ]; then
  echo ">> Lade appimagetool…"
  curl -fsSL -o "$APPIMAGETOOL" "https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-${ARCH}.AppImage"
  chmod +x "$APPIMAGETOOL"
fi

# --- assemble AppDir ---
echo ">> Baue AppDir…"
APPDIR="$BUILD/AppDir"
rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/bin" \
         "$APPDIR/usr/lib/tdrive-sync" \
         "$APPDIR/usr/share/applications" \
         "$APPDIR/usr/share/icons/hicolor/scalable/apps"

cp "$BUILD/tdrive-sync"              "$APPDIR/usr/bin/tdrive-sync"
cp "$RCLONE_BIN"                     "$APPDIR/usr/lib/tdrive-sync/rclone"
cp "$ROOT/packaging/AppRun"          "$APPDIR/AppRun"
chmod +x "$APPDIR/AppRun" "$APPDIR/usr/bin/tdrive-sync" "$APPDIR/usr/lib/tdrive-sync/rclone"

# The app logo is the single source of truth in internal/window/ (embedded into
# the binary for the settings window); reuse the very same file as the AppImage
# icon so both always match.
ICON="$ROOT/internal/window/icon.svg"
cp "$ROOT/packaging/tdrive-sync.desktop" "$APPDIR/tdrive-sync.desktop"
cp "$ROOT/packaging/tdrive-sync.desktop" "$APPDIR/usr/share/applications/tdrive-sync.desktop"
cp "$ICON"                               "$APPDIR/tdrive-sync.svg"
cp "$ICON"                               "$APPDIR/usr/share/icons/hicolor/scalable/apps/tdrive-sync.svg"
ln -sf tdrive-sync.svg "$APPDIR/.DirIcon"

# --- build the AppImage ---
echo ">> Baue AppImage…"
OUT="$DIST/Google_Drive_Sync-${ARCH}.AppImage"
rm -f "$OUT"
ARCH="$ARCH" "$APPIMAGETOOL" --appimage-extract-and-run --no-appstream "$APPDIR" "$OUT"

echo ""
echo ">> Fertig: $OUT"
