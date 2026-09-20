#!/usr/bin/env bash
# Builds the Android probe app into a signed, installable APK, with no Gradle.
#
# Gradle would pull a daemon, a wrapper, a lockfile and a network fetch into a
# repository that needs none of them. An APK is a zip holding a binary
# manifest, a resource table and a dex file, and the Android SDK ships every
# tool that makes those. So this script is the whole build: link, compile,
# dex, align, sign.
#
# It needs an Android SDK with build-tools and a platform android.jar, and a
# JDK. Both are found rather than assumed, and a missing one is named.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
out="${1:-$here/build}"

sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Library/Android/sdk}}"
[ -d "$sdk" ] || { echo "no Android SDK at $sdk (set ANDROID_HOME)" >&2; exit 1; }

# The newest build-tools and the newest platform, rather than a pinned pair.
# A pinned version is a version somebody has to install; the tools here are
# stable across releases for a build this small.
buildtools="$(ls -d "$sdk"/build-tools/*/ 2>/dev/null | sort -V | tail -1)"
[ -n "$buildtools" ] || { echo "no build-tools under $sdk" >&2; exit 1; }
platform="$(ls -d "$sdk"/platforms/*/ 2>/dev/null | sort -V | tail -1)"
[ -n "$platform" ] || { echo "no platform under $sdk" >&2; exit 1; }
androidjar="$platform/android.jar"
[ -f "$androidjar" ] || { echo "no android.jar at $androidjar" >&2; exit 1; }

rm -rf "$out"
mkdir -p "$out/classes"

# 1. Link the manifest into a base APK. No res/ directory: every view is built
#    in code, so there is no layout or string table to compile, and that keeps
#    aapt2's two stage compile-then-link down to just the link.
"$buildtools/aapt2" link \
  --manifest "$here/AndroidManifest.xml" \
  -I "$androidjar" \
  --min-sdk-version 24 \
  --target-sdk-version 36 \
  -o "$out/base.apk"

# 2. Compile against the framework, not against a runtime. android.jar carries
#    only signatures, which is exactly right: the real implementations live on
#    the device.
#
#    android.jar goes on the CLASSPATH rather than the bootclasspath. A JDK 9
#    and later javac refuses -bootclasspath alongside -source/-target ("option
#    --boot-class-path not allowed with target 11"), and the classpath is
#    sufficient here: every type this app names is an Android framework type,
#    and d8 reads the real framework from --lib at dex time anyway.
javac -nowarn -source 11 -target 11 \
  -classpath "$androidjar" \
  -d "$out/classes" \
  $(find "$here/src" -name '*.java')

# 3. Dex it. d8 desugars the lambda in MainActivity down to something a
#    minSdk 24 device runs.
"$buildtools/d8" \
  --lib "$androidjar" \
  --min-api 24 \
  --output "$out" \
  $(find "$out/classes" -name '*.class')

# 4. Add the dex to the APK. `zip -j` so it lands at the archive root, which is
#    where the runtime looks for it.
(cd "$out" && zip -q -j base.apk classes.dex)

# 5. Align, then sign. Alignment must come BEFORE signing: zipalign rewrites
#    entry offsets, so aligning a signed APK invalidates the signature it just
#    moved. This ordering is the one people get backwards.
"$buildtools/zipalign" -f 4 "$out/base.apk" "$out/probe-aligned.apk"

# A throwaway keystore generated on the spot. A debug signature is all an
# emulator install requires, and generating it here means no key is committed
# and no key has to be found.
keystore="$out/debug.keystore"
keytool -genkeypair \
  -keystore "$keystore" -storepass android -keypass android \
  -alias probe -keyalg RSA -keysize 2048 -validity 10000 \
  -dname "CN=Antifailure Probe, OU=Examples, O=Antifailure, C=US"

"$buildtools/apksigner" sign \
  --ks "$keystore" --ks-pass pass:android --key-pass pass:android \
  --ks-key-alias probe \
  --out "$out/probe.apk" \
  "$out/probe-aligned.apk"

rm -f "$out/base.apk" "$out/probe-aligned.apk" "$out/classes.dex"
rm -rf "$out/classes"
echo "$out/probe.apk"
