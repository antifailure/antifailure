#!/usr/bin/env bash
# Stop this checkout's preview.
#
# Stops the control plane it started and removes the database it created, and
# NOTHING else. The container is checked for the label up.sh puts on the ones it
# makes, because a dozen agents share this machine and removing a database
# somebody's test suite is using is a worse outcome than leaving one running.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
state="$root/.preview"
pidfile="$state/control-plane.pid"

slug="$(printf '%s' "$root" | shasum | cut -c1-8)"
container="${AF_PREVIEW_DB_CONTAINER:-af-preview-$slug}"

if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
  kill "$(cat "$pidfile")" 2>/dev/null || true
  echo "stopped the control plane"
fi
rm -f "$pidfile" "$state/url"

if [ "${AF_PREVIEW_KEEP_DB:-}" = "1" ]; then
  echo "kept $container, AF_PREVIEW_KEEP_DB is set"
  exit 0
fi

if ! docker inspect "$container" >/dev/null 2>&1; then
  echo "no $container to remove"
  exit 0
fi
if [ "$(docker inspect -f '{{index .Config.Labels "af-preview"}}' "$container")" != "1" ]; then
  echo "$container is not labelled af-preview=1, so this script did not create it and will not remove it." >&2
  exit 1
fi
docker rm -f "$container" >/dev/null
echo "removed $container"
