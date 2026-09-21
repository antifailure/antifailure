#!/usr/bin/env bash
# Assemble AfLedger.app from main.swift into the directory given as $1.
#
# A script rather than a step inside the test, for the reason the accessibility
# reader's own build gives: the compiler is a prerequisite a machine either has
# or does not, and a caller that cannot run this has to be told which of those
# it is rather than shown a stack trace.
#
# THE NAME IS THE POINT, and it is why three spellings of it are set here and
# asserted to agree. A manifest that writes `kind: macos` and no `process` has
# the name derived from the bundle by normalisation, and the runner then FINDS
# the running application by that derived name. If the bundle is AfLedger.app
# and macOS calls the process anything else, the launch succeeds and the lookup
# fails, and the run is blocked for a reason that looks like a slow
# application. So CFBundleName, CFBundleExecutable and the bundle's own
# filename are all AfLedger, which is what makes the derivation's answer the
# right one rather than a coincidence.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out="${1:?usage: build.sh <output directory>}"
name="AfLedger"
app="$out/$name.app"

if ! command -v swiftc >/dev/null 2>&1; then
  echo "swiftc is not on PATH. It ships with the Xcode command line tools:" >&2
  echo "  xcode-select --install" >&2
  exit 2
fi

rm -rf "$app"
mkdir -p "$app/Contents/MacOS"

cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>$name</string>
  <key>CFBundleDisplayName</key><string>$name</string>
  <key>CFBundleExecutable</key><string>$name</string>
  <key>CFBundleIdentifier</key><string>dev.antifailure.fixture.$name</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

swiftc -O -o "$app/Contents/MacOS/$name" "$here/main.swift"
echo "$app"
