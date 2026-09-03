#!/usr/bin/env bash
# Screenshot every operator page, signed in, at 320 and at 1440.
#
# A thin wrapper: the work is in shots.mjs, which needs a browser and a running
# preview and says so by name when either is missing. Run tools/preview/up.sh
# first.
#
# Exits non zero when a page overflows horizontally at 320 or when a page came
# back showing signed-out content, because a run of forty six pictures that
# nobody reads is not verification.
#
# The operator password comes from the state directory up.sh writes, which is
# outside the repository. There is no password in this file.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=tools/preview/common.sh
source "$here/common.sh"

# Where up.sh put this checkout's preview. Read from the file it writes rather
# than defaulted to a port, because the port is derived per checkout so that six
# lanes can run at once, and a default here would send every one of them at the
# first lane's console.
if [ -n "${AF_PREVIEW_URL:-}" ]; then
  base="$AF_PREVIEW_URL"
elif [ -f "$preview_state/url" ]; then
  base="$(cat "$preview_state/url")"
else
  echo "no preview in $preview_state. Run tools/preview/up.sh first, or set AF_PREVIEW_URL." >&2
  exit 2
fi
export AF_PREVIEW_URL="$base"

if ! curl -fsS "$base/health" >/dev/null 2>&1; then
  echo "nothing is answering at $base. Run tools/preview/up.sh first." >&2
  exit 2
fi

# The operator password up.sh generated for this run, from outside the working
# tree. No default and nothing in the repository to fall back to.
if [ -z "${AF_PREVIEW_PASSWORD:-}" ]; then
  if [ -f "$preview_state/operator-password" ]; then
    AF_PREVIEW_PASSWORD="$(cat "$preview_state/operator-password")"
  else
    echo "no operator password in $preview_state. Run tools/preview/up.sh, or set AF_PREVIEW_PASSWORD." >&2
    exit 2
  fi
fi
export AF_PREVIEW_PASSWORD
export AF_PREVIEW_EMAIL="${AF_PREVIEW_EMAIL:-$preview_operator_email}"
export AF_PREVIEW_SHOTS="${AF_PREVIEW_SHOTS:-$preview_state/shots}"

node "$here/shots.mjs" "$@"
echo "screenshots are in $AF_PREVIEW_SHOTS"
