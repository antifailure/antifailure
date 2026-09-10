#!/usr/bin/env bash
# The harness behind every number in docs/src/content/docs/guides/gcp.md.
#
# It exists because a number this project quotes has to be reproducible by the
# person being quoted it at. Run it and you get your own numbers, on your own
# machine, from the same scripts.
#
# WHAT IT MEASURES
#   1. Whether an UNMODIFIED Google client library reaches the emulated
#      surface with no endpoint override, in JavaScript and in Python.
#   2. Which hosts each client contacts before its first call, which is where
#      the credential gap in the guide came from.
#   3. Whether an unmodified gRPC client can traverse a proxy that terminates
#      TLS and then reads HTTP/1.1, which is what the sidecar does, and what
#      changes when the same proxy forwards HTTP/2 instead.
#   4. The download size of each pinned image, read from the registry.
#
# WHAT IT NEEDS
#   node and npm, python3, openssl, and for the container half, docker.
#
# WHAT IT IS NOT
#   The observers under this directory are NOT emulators and must never be
#   mistaken for one. They answer the smallest shaped response that lets a
#   client continue, so that the question "which hosts does this client need"
#   can be answered. The surface itself is answered by fake-gcs-server and by
#   Google's own emulators, in containers, which is what CONTAINERS=1 runs.
set -uo pipefail
cd "$(dirname "$0")"
out="${AF_PROBE_OUT:-$PWD/probe-report.md}"
say() { printf '%s\n' "$*" | tee -a "$out"; }
: > "$out"

say "# GCP emulator probe, $(date -u +%Y-%m-%d)"
say ""
say "Machine: $(uname -s) $(uname -m), node $(node --version 2>/dev/null || echo absent), python $(python3 --version 2>&1 | awk '{print $2}')"
say ""

command -v node >/dev/null || { say "node is absent, so nothing below was measured."; exit 1; }
command -v openssl >/dev/null || { say "openssl is absent, so nothing below was measured."; exit 1; }

./mkcerts.sh || exit 1
[ -d node_modules ] || npm install --no-audit --no-fund --silent \
  @google-cloud/storage @google-cloud/pubsub || exit 1

say "## Image sizes, from each registry"
say ""
python3 imgsize.py 2>&1 | sed 's/^/    /' | tee -a "$out"
say ""

say "## Reported in full by ./cases.sh"
./cases.sh 2>&1 | tee -a "$out"
