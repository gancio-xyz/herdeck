#!/bin/sh
# Builds dist/herdeck.app: a menu bar app (LSUIElement, no Dock icon) wrapping
# the herdeck binary. Pass BIN=path to wrap an existing binary (used by the
# release workflow); otherwise builds for the host architecture.
set -eu
cd "$(dirname "$0")/.."
VERSION=${VERSION:-dev}
APP=dist/herdeck.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
if [ -n "${BIN:-}" ]; then
	cp "$BIN" "$APP/Contents/MacOS/herdeck"
else
	CGO_ENABLED=1 go build -ldflags "-s -w -X main.version=$VERSION" -o "$APP/Contents/MacOS/herdeck" ./cmd/herdeck
fi
sed "s/__VERSION__/${VERSION#v}/g" scripts/Info.plist > "$APP/Contents/Info.plist"
codesign --force --sign - "$APP" >/dev/null 2>&1 || echo "warning: ad-hoc codesign failed" >&2
echo "$APP"
