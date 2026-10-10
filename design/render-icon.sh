#!/bin/sh
# Renders design/icon.svg and design/icon-windows.svg to PNGs (1024×1024,
# transparent) with headless Chrome.
set -e
cd "$(dirname "$0")/.."
chrome="${CHROME:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for name in icon icon-windows; do
  printf '<html><body style="margin:0;background:transparent">%s</body></html>' "$(cat design/$name.svg)" > "$tmp/$name.html"
  "$chrome" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
    --default-background-color=00000000 --window-size=1024,1024 \
    --screenshot="$tmp/$name.png" "file://$tmp/$name.html" >/dev/null 2>&1
  mv "$tmp/$name.png" design/$name.png
  echo "wrote design/$name.png"
done
