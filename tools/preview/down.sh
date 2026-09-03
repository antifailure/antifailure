#!/usr/bin/env bash
# Stop this checkout's preview.
#
# Stops the control plane it started, removes the database it created and the
# generated passwords it wrote outside the tree, and NOTHING else. The container is checked for the label up.sh puts on the ones it
# makes, because a dozen agents share this machine and removing a database
# somebody's test suite is using is a worse outcome than leaving one running.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=tools/preview/common.sh
source "$here/common.sh"

if [ -f "$preview_pidfile" ] && kill -0 "$(cat "$preview_pidfile")" 2>/dev/null; then
  kill "$(cat "$preview_pidfile")" 2>/dev/null || true
  echo "stopped the control plane"
fi
rm -f "$preview_pidfile" "$preview_state/url" "$preview_state/operator-password"

if [ "${AF_PREVIEW_KEEP_DB:-}" = "1" ]; then
  echo "kept $preview_container, AF_PREVIEW_KEEP_DB is set"
  exit 0
fi

if ! docker inspect "$preview_container" >/dev/null 2>&1; then
  echo "no $preview_container to remove"
  rm -f "$preview_state/postgres-password"
  exit 0
fi
if [ "$(docker inspect -f '{{index .Config.Labels "af-preview"}}' "$preview_container")" != "1" ]; then
  echo "$preview_container is not labelled af-preview=1, so this script did not create it and will not remove it." >&2
  exit 1
fi
docker rm -f "$preview_container" >/dev/null
rm -f "$preview_state/postgres-password"
echo "removed $preview_container"
