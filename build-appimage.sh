#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 tokajer <tokajer@tokajer.at>
# SPDX-License-Identifier: GPL-3.0-or-later

# Builds the tdrive-sync AppImage.
#
#   ./build-appimage.sh
#
# rclone, appimagetool and the AppImage runtime are pinned (version + sha256 below) and every download
# is verified. To bump one, change the version and the hashes together.
#
# Environment overrides:
#   GO=/path/to/go            use a specific Go toolchain (default: `go` in PATH)
#   RCLONE_BIN=/path/rclone   use this rclone as-is (no version or hash check)
#   APPIMAGETOOL=/path/tool   use this appimagetool as-is (no hash check)
set -euo pipefail

RCLONE_VERSION=v1.75.2
APPIMAGETOOL_VERSION=1.9.1
RUNTIME_VERSION=20251108

ROOT="$(cd "$(dirname "$0")" && pwd)"
BUILD="$ROOT/build"
DIST="$ROOT/dist"
GO="${GO:-go}"

case "$(uname -m)" in
  x86_64)
    ARCH=x86_64 RC_ARCH=amd64
    RCLONE_SHA256=349ac8fba6ff65d6247043f1750cdcb518ec5d500ef91463a10d37c0ccdf3702
    APPIMAGETOOL_SHA256=ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0
    RUNTIME_SHA256=2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d ;;
  aarch64|arm64)
    ARCH=aarch64 RC_ARCH=arm64
    RCLONE_SHA256=7e1e8d69654941b7b7df84ee74c5f7fb09cce7d5496947fb7bf16b55ff427d10
    APPIMAGETOOL_SHA256=f0837e7448a0c1e4e650a93bb3e85802546e60654ef287576f46c71c126a9158
    RUNTIME_SHA256=00cbdfcf917cc6c0ff6d3347d59e0ca1f7f45a6df1a428a0d6d8a78664d87444 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

mkdir -p "$BUILD" "$DIST"

# Determine the version: explicitly via $VERSION, else the git tag of a GitHub
# build (GITHUB_REF_TYPE=tag), else `git describe`, else local-dev-build.
if [ -z "${VERSION:-}" ]; then
  if [ "${GITHUB_REF_TYPE:-}" = "tag" ] && [ -n "${GITHUB_REF_NAME:-}" ]; then
    VERSION="$GITHUB_REF_NAME"
  else
    VERSION="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo local-dev-build)"
  fi
fi
echo ">> Version: $VERSION"

echo ">> Building tdrive-sync (Go)…"
# cgo is required for the native settings window (WebKitGTK via dlopen).
CGO_ENABLED=1 "$GO" build -trimpath \
  -ldflags "-s -w -X main.version=${VERSION}" \
  -o "$BUILD/tdrive-sync" ./cmd/tdrive-sync

# --- rclone ---
if [ -n "${RCLONE_BIN:-}" ]; then
  [ -x "$RCLONE_BIN" ] || { echo "RCLONE_BIN is not an executable file: $RCLONE_BIN" >&2; exit 1; }
else
  RCLONE_BIN="$BUILD/rclone"
  if [ "$("$RCLONE_BIN" version 2>/dev/null | head -n1)" != "rclone $RCLONE_VERSION" ]; then
    echo ">> Downloading rclone $RCLONE_VERSION…"
    ZIP="rclone-$RCLONE_VERSION-linux-$RC_ARCH.zip"
    curl -fsSL -o "$BUILD/$ZIP" "https://downloads.rclone.org/$RCLONE_VERSION/$ZIP"
    echo "$RCLONE_SHA256  $BUILD/$ZIP" | sha256sum -c -
    ( cd "$BUILD" && unzip -oq "$ZIP" && cp "rclone-$RCLONE_VERSION-linux-$RC_ARCH/rclone" rclone && rm -rf "rclone-$RCLONE_VERSION-linux-$RC_ARCH" "$ZIP" )
    chmod +x "$RCLONE_BIN"
  fi
fi

# --- appimagetool ---
if [ -n "${APPIMAGETOOL:-}" ]; then
  [ -x "$APPIMAGETOOL" ] || { echo "APPIMAGETOOL is not an executable file: $APPIMAGETOOL" >&2; exit 1; }
else
  APPIMAGETOOL="$BUILD/appimagetool"
  if ! echo "$APPIMAGETOOL_SHA256  $APPIMAGETOOL" | sha256sum -c - >/dev/null 2>&1; then
    echo ">> Downloading appimagetool $APPIMAGETOOL_VERSION…"
    curl -fsSL -o "$APPIMAGETOOL" "https://github.com/AppImage/appimagetool/releases/download/$APPIMAGETOOL_VERSION/appimagetool-$ARCH.AppImage"
    echo "$APPIMAGETOOL_SHA256  $APPIMAGETOOL" | sha256sum -c -
    chmod +x "$APPIMAGETOOL"
  fi
fi

# --- AppImage runtime ---
# Without --runtime-file appimagetool fetches the moving "continuous" runtime.
RUNTIME="$BUILD/runtime-$RUNTIME_VERSION-$ARCH"
if ! echo "$RUNTIME_SHA256  $RUNTIME" | sha256sum -c - >/dev/null 2>&1; then
  echo ">> Downloading AppImage runtime $RUNTIME_VERSION…"
  curl -fsSL -o "$RUNTIME" "https://github.com/AppImage/type2-runtime/releases/download/$RUNTIME_VERSION/runtime-$ARCH"
  echo "$RUNTIME_SHA256  $RUNTIME" | sha256sum -c -
fi

# --- assemble AppDir ---
echo ">> Building the AppDir…"
APPDIR="$BUILD/AppDir"
rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/bin" \
         "$APPDIR/usr/lib/tdrive-sync" \
         "$APPDIR/usr/share/applications" \
         "$APPDIR/usr/share/icons/hicolor/scalable/apps" \
         "$APPDIR/usr/share/licenses/tdrive-sync"

cp "$BUILD/tdrive-sync"              "$APPDIR/usr/bin/tdrive-sync"
cp "$RCLONE_BIN"                     "$APPDIR/usr/lib/tdrive-sync/rclone"
cp "$ROOT/packaging/AppRun"          "$APPDIR/AppRun"
chmod +x "$APPDIR/AppRun" "$APPDIR/usr/bin/tdrive-sync" "$APPDIR/usr/lib/tdrive-sync/rclone"

# The GPL requires the licence text to accompany the binary; rclone's MIT
# licence requires the same for the copy of rclone we bundle. rclone's own
# release archive ships no licence file, so we keep a copy of it in packaging/ —
# that also covers builds that bring their own binary via $RCLONE_BIN.
cp "$ROOT/LICENSE"                       "$APPDIR/usr/share/licenses/tdrive-sync/LICENSE"
cp "$ROOT/packaging/LICENSE.rclone"      "$APPDIR/usr/share/licenses/tdrive-sync/LICENSE.rclone"

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
echo ">> Building the AppImage…"
OUT="$DIST/TDrive_Sync-${ARCH}.AppImage"
rm -f "$OUT"
ARCH="$ARCH" "$APPIMAGETOOL" --appimage-extract-and-run --no-appstream --runtime-file "$RUNTIME" "$APPDIR" "$OUT"

echo ""
echo ">> Done: $OUT"
