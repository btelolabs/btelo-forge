#!/bin/sh
# Builds the macOS QuickLook thumbnail extension bundle (ForgeThumbnailer.appex) that reads the BTFPIC01 footer
# written by internal/thumb. Embed the resulting .appex into Btelo Forge.app/Contents/PlugIns/ before codesigning.
set -eu

DIR="$(cd "$(dirname "$0")" && pwd)"
OUT="${1:-$DIR/ForgeThumbnailer.appex}"
VERSION="${2:-0.0.0}"

mkdir -p "$OUT/Contents/MacOS"
sed "s/VERSION/$VERSION/g" "$DIR/Info.plist" > "$OUT/Contents/Info.plist"

clang -fobjc-arc -O2 -Wall -Wextra \
	-e _NSExtensionMain \
	-fapplication-extension \
	-framework Foundation \
	-framework CoreGraphics \
	-framework ImageIO \
	-framework QuickLookThumbnailing \
	"$DIR/ThumbnailProvider.m" \
	-o "$OUT/Contents/MacOS/ForgeThumbnailer"

echo "Built $OUT"
