#!/bin/sh
# Renders design/icon.svg to design/icon.png (1024×1024, transparent) with
# headless Chrome.
set -e
cd "$(dirname "$0")/.."
chrome="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
printf '<html><body style="margin:0;background:transparent">%s</body></html>' "$(cat design/icon.svg)" > "$tmp/icon.html"
"$chrome" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
  --default-background-color=00000000 --window-size=1024,1024 \
  --screenshot="$tmp/icon.png" "file://$tmp/icon.html" >/dev/null 2>&1
mv "$tmp/icon.png" design/icon.png
echo "wrote design/icon.png"
