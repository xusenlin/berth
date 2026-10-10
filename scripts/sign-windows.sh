#!/bin/sh
# Signs a Windows executable or installer for `mygo build` (windows.signCommand
# in mygo.config.ts) with the code signing certificate in WINDOWS_PFX, whose password
# is WINDOWS_PFX_PASSWORD. Without WINDOWS_PFX the files stay unsigned, as in
# everyday builds. Needs osslsigncode (brew install osslsigncode).
set -e
[ -n "$WINDOWS_PFX" ] || exit 0
osslsigncode sign -pkcs12 "$WINDOWS_PFX" -pass "$WINDOWS_PFX_PASSWORD" \
  -n Berth -h sha256 -ts http://timestamp.digicert.com \
  -in "$1" -out "$1.signed"
mv "$1.signed" "$1"
