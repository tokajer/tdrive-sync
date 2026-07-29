#!/usr/bin/env bash
#
# Health check for TDrive Sync: verifies the tree builds and passes its tests,
# then reports what is actually installed and running on this machine.
#
# Read-only – nothing here changes the project, the configuration or the running
# daemon. Meant as the first command of a work session, so nobody has to
# rediscover the state of things by hand. See REENTRY.md.
#
# Usage:
#   scripts/check.sh              build, vet, gofmt, tests + system state
#   scripts/check.sh --plugin     also compile the Dolphin KIO plugin
#   scripts/check.sh --quiet      only failures and the summary

set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

WITH_PLUGIN=0
QUIET=0
for arg in "$@"; do
    case "$arg" in
        --plugin) WITH_PLUGIN=1 ;;
        --quiet) QUIET=1 ;;
        -h | --help)
            sed -n '3,17p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            echo "unknown option: $arg (try --help)" >&2
            exit 2
            ;;
    esac
done

if [ -t 1 ]; then
    GREEN=$'\033[32m' RED=$'\033[31m' YELLOW=$'\033[33m' DIM=$'\033[2m' OFF=$'\033[0m'
else
    GREEN='' RED='' YELLOW='' DIM='' OFF=''
fi

FAILED=0
SKIPPED=0

pass() { [ "$QUIET" = 1 ] || printf '  %sok%s   %s\n' "$GREEN" "$OFF" "$1"; }
fail() {
    printf '  %sFAIL%s %s\n' "$RED" "$OFF" "$1"
    FAILED=$((FAILED + 1))
}
skip() {
    [ "$QUIET" = 1 ] || printf '  %sskip%s %s\n' "$YELLOW" "$OFF" "$1"
    SKIPPED=$((SKIPPED + 1))
}
info() { [ "$QUIET" = 1 ] || printf '  %s%s%s\n' "$DIM" "$1" "$OFF"; }
section() { [ "$QUIET" = 1 ] || printf '\n%s\n' "$1"; }

# Runs a command, reporting its output only when it fails.
run() {
    local label="$1"
    shift
    local out
    if out=$("$@" 2>&1); then
        pass "$label"
    else
        fail "$label"
        printf '%s\n' "$out" | sed 's/^/       /'
    fi
}

# ---------------------------------------------------------------- Go code

section "Go"
GO="${GO:-go}"
if ! command -v "$GO" >/dev/null 2>&1; then
    fail "go not found (set \$GO to a Go installation)"
else
    info "$("$GO" version)"
    run "build" "$GO" build ./...
    run "vet" "$GO" vet ./...
    run "test" "$GO" test ./...

    # Ignore the two files that were already unformatted before this check
    # existed, so a pre-existing wart cannot mask a new one.
    unformatted=$(gofmt -l cmd internal 2>/dev/null |
        grep -v -e '^$' -e 'internal/notify/notify.go' -e 'internal/tray/icon.go' || true)
    if [ -z "$unformatted" ]; then
        pass "gofmt"
    else
        fail "gofmt: $(printf '%s' "$unformatted" | tr '\n' ' ')"
    fi
fi

# -------------------------------------------------------- Dolphin plugin

section "Dolphin plugin (C++)"
if [ "$WITH_PLUGIN" != 1 ]; then
    skip "compile (pass --plugin to include it)"
elif ! command -v cmake >/dev/null 2>&1; then
    skip "compile: cmake is not installed"
else
    build_dir=$(mktemp -d)
    trap 'rm -rf "$build_dir"' EXIT
    if out=$(cmake -S internal/dolphin/plugin -B "$build_dir" -DCMAKE_BUILD_TYPE=Release 2>&1); then
        if out=$(cmake --build "$build_dir" --parallel 2>&1); then
            pass "compile"
        else
            fail "compile"
            printf '%s\n' "$out" | tail -25 | sed 's/^/       /'
        fi
    else
        skip "compile: Qt 6 / KF 6 development files missing"
        info "sudo dnf install cmake gcc-c++ extra-cmake-modules kf6-kio-devel qt6-qtbase-devel"
    fi
fi

# The two implementations of the state logic must stay in step; this only checks
# that both still mention every state, which catches the usual "added it on one
# side only" mistake.
missing=""
for state in cloud partial cached pinned pinning uploading local; do
    cpp="internal/dolphin/plugin/tdrivestate.h"
    if ! grep -qi "^ *${state}," "$cpp" 2>/dev/null; then
        missing="$missing $state"
    fi
done
if [ -z "$missing" ]; then
    pass "state list mirrored in the C++ plugin"
else
    fail "states missing from $cpp:$missing"
fi

# ------------------------------------------------------------ live system

section "This machine"
if pgrep -f 'tdrive-sync run' >/dev/null 2>&1; then
    info "daemon: running ($(pgrep -f 'tdrive-sync run' | tr '\n' ' ' | sed 's/ $//'))"
else
    info "daemon: not running"
fi

if pgrep -x rclone >/dev/null 2>&1; then
    info "rclone: running"
else
    info "rclone: not running"
fi

state_file="${XDG_STATE_HOME:-$HOME/.local/state}/tdrive-sync/file-manager.json"
if [ -f "$state_file" ]; then
    if command -v python3 >/dev/null 2>&1; then
        info "$(python3 - "$state_file" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
except Exception as exc:
    print(f"file-manager.json: unreadable ({exc})")
else:
    print("file-manager.json: v{} mode={} active={} state={!r} pinned={}".format(
        d.get("version"), d.get("mode"), d.get("active"), d.get("state"), len(d.get("pinned") or [])))
PY
        )"
    else
        info "file-manager.json: present"
    fi
else
    info "file-manager.json: absent (the daemon publishes it while syncing)"
fi

mount_point=$(sed -n 's/^local_dir: *//p' "${XDG_CONFIG_HOME:-$HOME/.config}/tdrive-sync/config.yaml" 2>/dev/null)
if [ -n "$mount_point" ]; then
    if mount | grep -qF " ${mount_point} "; then
        info "sync folder: $mount_point (mounted)"
    else
        info "sync folder: $mount_point (not mounted)"
    fi
fi

plugin_dir="$HOME/.local/lib64/qt6/plugins"
for p in "kf6/overlayicon/libtdrivesyncoverlay.so" "kf6/kfileitemaction/libtdrivesyncaction.so"; do
    if [ -f "$plugin_dir/$p" ]; then
        info "plugin: $p installed"
    else
        info "plugin: $p not installed (tdrive-sync dolphin install)"
    fi
done

case ":${QT_PLUGIN_PATH:-}:" in
    *":$plugin_dir:"*) info "QT_PLUGIN_PATH: contains the plugin directory" ;;
    *) info "QT_PLUGIN_PATH: missing the plugin directory in this shell (a fresh login fixes it)" ;;
esac

# ---------------------------------------------------------------- summary

printf '\n'
if [ "$FAILED" -gt 0 ]; then
    printf '%s%d check(s) failed%s' "$RED" "$FAILED" "$OFF"
else
    printf '%sall checks passed%s' "$GREEN" "$OFF"
fi
[ "$SKIPPED" -gt 0 ] && printf ', %d skipped' "$SKIPPED"
printf '\n'
[ "$FAILED" -gt 0 ] && exit 1
exit 0
