#!/usr/bin/env bash
#
# Build the WiFiChat Go engine into an Android AAR via gomobile.
#
# Output: android/app/libs/wifichat.aar (+ wifichat-sources.jar)
#
# Requires: Go 1.26+, Android NDK, gomobile
#   go install golang.org/x/mobile/cmd/gomobile@latest
#   go install golang.org/x/mobile/cmd/gobind@latest
#   gomobile init
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ANDROID_DIR="$ROOT/android"
OUT_DIR="$ANDROID_DIR/app/libs"
ABIS="${WIFICHAT_ABIS:-arm64-v8a,amd64}"

# Map Android ABI names to gomobile -android flags (comma-separated GOARCHes).
# gomobile uses GOARCH names: arm, arm64, 386, amd64
GOMOBILE_TARGETS="${WIFICHAT_GOMOBILE_TARGETS:-android/arm64,android/amd64}"

NDK="${ANDROID_NDK_HOME:-}"
if [ -z "$NDK" ]; then
  NDK=$(ls -d "${ANDROID_HOME:-$HOME/Android/Sdk}/ndk/"* 2>/dev/null | sort -V | tail -1 || true)
fi
[ -n "$NDK" ] || { echo "error: Android NDK not found (set ANDROID_NDK_HOME)" >&2; exit 1; }
export ANDROID_NDK_HOME="$NDK"

if ! command -v gomobile >/dev/null 2>&1; then
  echo "error: gomobile not in PATH; run: go install golang.org/x/mobile/cmd/gomobile@latest" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"
cd "$ROOT"

echo "==> gomobile bind ($GOMOBILE_TARGETS)"
# -javapkg prefixes the Go package name, producing Java package wifichat.wifichat.
gomobile bind \
  -target="$GOMOBILE_TARGETS" \
  -androidapi 26 \
  -javapkg=wifichat \
  -o "$OUT_DIR/wifichat.aar" \
  ./mobile

echo "done:"
ls -lh "$OUT_DIR"/wifichat.aar
