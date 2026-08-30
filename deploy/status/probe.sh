#!/usr/bin/env bash
# One external check of every target in targets.json.
#
# This is the whole point of the status page: the signal has to come from
# somewhere other than the thing it reports on. Running this from a laptop or
# from the control plane's own process would mean a total outage of the
# control plane also takes down the thing that is supposed to say so. Running
# it from GitHub Actions means the check keeps happening on a different
# vendor's infrastructure, on its own schedule, whether or not anything at
# Azure is answering at all.
#
# Reads readiness the same way deploy/cd/health-gate.sh does and for the same
# reason: /health is a static literal that answers even when the database is
# unreachable, so it would report an outage as healthy. /readyz runs a real
# query, which is the only answer worth reporting on a status page.
#
#   probe.sh <targets.json>
#
# Prints one JSON object per line to stdout, one per target, and never fails
# the run on a target being down: a target that does not answer is a status to
# report, not a reason to stop reporting it.

set -uo pipefail

TARGETS="${1:?usage: probe.sh <targets.json>}"

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

jq -c '.[]' "$TARGETS" | while read -r target; do
  name="$(jq -r '.name' <<<"$target")"
  url="$(jq -r '.url' <<<"$target")"

  body="$(curl -fsS -m 10 "${url%/}/readyz" 2>/dev/null)"
  code="$(curl -sS -m 10 -o /dev/null -w '%{http_code}' "${url%/}/readyz" 2>/dev/null || echo 000)"

  ready=false
  commit=""
  if [ -n "$body" ]; then
    if grep -q '"ready"[[:space:]]*:[[:space:]]*true' <<<"$body"; then
      ready=true
    fi
    commit="$(sed -n 's/.*"commit"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' <<<"$body")"
  fi

  jq -nc \
    --arg checked_at "$(now)" \
    --arg name "$name" \
    --arg url "$url" \
    --argjson http_status "${code:-0}" \
    --argjson ready "$ready" \
    --arg commit "${commit:-}" \
    '{checked_at: $checked_at, name: $name, url: $url, http_status: $http_status, ready: $ready, commit: $commit}'
done
