#!/usr/bin/env bash
# Builds the iOS probe app into a simulator .app bundle, with no Xcode project.
#
# An .xcodeproj is a generated, merge hostile file that nothing here needs: a
# simulator bundle is a directory holding a Mach-O binary and an Info.plist,
# and swiftc builds the binary directly. So this script is the whole build, it
# is readable, and it produces the same bundle on any machine with Xcode
# installed.
#
# The Info.plist keys below are not decoration. CFBundleSupportedPlatforms is
# the one Xcode injects silently and a hand written plist omits: Appium's
# XCUITest driver reads it to decide whether a bundle belongs on a simulator
# or a device, and refuses the session with "CFBundleSupportedPlatforms is not
# a valid list" when it is missing. That refusal names the plist rather than
# the app, so it is worth the comment.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out="${1:-$here/build}"
app="$out/Probe.app"

# arm64 simulator only: this is what an Apple silicon machine runs. A bundle
# for an Intel host or a real device is a different target triple and a
# different signing story, and neither is what the runner's tests drive.
target="arm64-apple-ios17.0-simulator"

rm -rf "$app"
mkdir -p "$app"

xcrun -sdk iphonesimulator swiftc \
  -parse-as-library \
  -target "$target" \
  -O \
  "$here/App.swift" \
  -o "$app/Probe"

cat > "$app/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>Probe</string>
  <key>CFBundleIdentifier</key><string>dev.antifailure.probe</string>
  <key>CFBundleName</key><string>Probe</string>
  <key>CFBundleDisplayName</key><string>Probe</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>CFBundleSupportedPlatforms</key><array><string>iPhoneSimulator</string></array>
  <key>DTPlatformName</key><string>iphonesimulator</string>
  <key>LSRequiresIPhoneOS</key><true/>
  <key>MinimumOSVersion</key><string>17.0</string>
  <key>UIDeviceFamily</key><array><integer>1</integer></array>
  <key>UILaunchScreen</key><dict/>
</dict>
</plist>
PLIST

plutil -convert binary1 "$app/Info.plist"

# Ad hoc signing. A simulator refuses an unsigned bundle, and an ad hoc
# signature needs no developer account, no provisioning profile and no
# keychain, which is what keeps this script runnable by anyone.
codesign --force --sign - "$app" >/dev/null 2>&1

echo "$app"
