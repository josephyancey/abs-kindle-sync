#!/bin/sh
# Builds the KPM package into dist/: the abs-sync binary and tapwait (both in Docker), the Kindle
# files and the hooks in kpm/. A .kpkg is a gzipped tar of the package folder's contents.
set -e
cd "$(dirname "$0")/.."
ROOT=$(pwd -W 2>/dev/null || pwd) # pwd -W: Windows path for Docker under Git Bash
PKG=dist/package
rm -rf "$PKG"
mkdir -p "$PKG/abs-sync/bin"

VERSION=$(python3 -c 'import json; print(".".join(map(str, json.load(open("kpm/manifest.json"))["version"])))')
MSYS_NO_PATHCONV=1 docker run --rm -v "$ROOT/app:/src" -w /src -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=arm -e GOARM=7 \
	golang:1.26-bookworm go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o abs-sync ./cmd/abs-sync
sh helper/build.sh >/dev/null

cp app/abs-sync kindle/bin/tapwait kindle/bin/abs-prompt.sh kindle/bin/abs-install.sh "$PKG/abs-sync/bin/"
cp -r kindle/setup kindle/upstart "$PKG/abs-sync/"
cp "kindle/Audiobookshelf Sync.sh" kpm/manifest.json kpm/install.sh kpm/uninstall.sh kpm/launch.sh kpm/README.md LICENSE "$PKG/"

NAME=$(python3 -c 'import json; m = json.load(open("kpm/manifest.json")); print("%s_%s_%s" % (m["id"], ".".join(map(str, m["version"])), "-".join(m["supported_platforms"])))')
rm -f "dist/$NAME.kpkg"
(cd "$PKG" && COPYFILE_DISABLE=1 tar -czf "../$NAME.kpkg" *)
ls -l "dist/$NAME.kpkg"
