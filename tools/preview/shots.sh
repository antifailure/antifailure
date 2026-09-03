#!/usr/bin/env bash
# Screenshot every operator page, signed in, at 320 and at 1440.
#
# A thin wrapper: the work is in shots.mjs, which needs a browser and a running
# preview and says so by name when either is missing. Run tools/preview/up.sh
# first.
#
# Exits non zero when a page overflows horizontally at 320 or when a page came
# back showing signed-out content, because a run of forty four pictures that
# nobody reads is not verification.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

# Where up.sh put this checkout's preview. Read from the file it writes rather
# than defaulted to a port, because the port is derived per checkout so that six
# lanes can run at once, and a default here would send every one of them at the
# first lane's console.
urlfile="$root/.preview/url"
if [ -n "${AF_PREVIEW_URL:-}" ]; then
  base="$AF_PREVIEW_URL"
elif [ -f "$urlfile" ]; then
  base="$(cat "$urlfile")"
else
  echo "no $urlfile. Run tools/preview/up.sh first, or set AF_PREVIEW_URL." >&2
  exit 2
fi
export AF_PREVIEW_URL="$base"
if ! curl -fsS "$base/health" >/dev/null 2>&1; then
  echo "nothing is answering at $base. Run tools/preview/up.sh first." >&2
  exit 2
fi

export AF_PREVIEW_SHOTS="${AF_PREVIEW_SHOTS:-$root/.preview/shots}"
node "$here/shots.mjs" "$@"
