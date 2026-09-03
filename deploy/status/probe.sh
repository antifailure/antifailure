#!/usr/bin/env bash
# One external check of every component in targets.json.
#
# This is the whole point of the status page: the signal has to come from
# somewhere other than the thing it reports on. Running this from a laptop or
# from the control plane's own process would mean a total outage of the
# control plane also takes down the thing that is supposed to say so. Running
# it from GitHub Actions means the check keeps happening on a different
# vendor's infrastructure, on its own schedule, whether or not anything at
# Azure is answering at all.
#
#   probe.sh <targets.json>
#
# Prints one JSON object per line to stdout, one per component, and never
# fails the run on a component being down: a component that does not answer is
# a status to report, not a reason to stop reporting it.
#
# Two check kinds, because two things are worth proving and a 200 proves
# neither on its own.
#
#   readyz  reads the same endpoint deploy/cd/health-gate.sh reads, and for
#           the same reason: /health is a static literal that answers even
#           when the database is unreachable, so a page built on it would
#           report an outage as healthy. /readyz runs a real query. A 200 with
#           `"ready": false` is a failure here, which is the distinction the
#           whole endpoint exists to make.
#
#   http    a 200 whose body contains a required marker. The marker is not
#           decoration. Every surface below is a static file published by a
#           deploy that has already, once, produced a live origin serving the
#           wrong thing: the site went out with no /install.sh and with a
#           managed function that answered every request with a 500. A check
#           that only reads the status line would have called both of those
#           healthy. Standard 19 in the fleet contract is the general form of
#           it: a green gate over a subject it never examined is worse than no
#           gate.
#
# The markers are chosen to be structural rather than editorial. `/_next/static/`
# and `/docs/_astro/` are build output paths, `https://app.antifailure.dev` is
# the product API origin the site API publishes, and `#!/bin/sh` is the
# installer's shebang. None of them moves when somebody rewrites a headline,
# because a marker that tracks copy turns a prose edit into a false outage, and
# a false outage is the one thing this page must never publish.
#
# WHAT THIS PAGE DELIBERATELY DOES NOT CHECK: whether sign-up is open. A control
# plane serving traffic perfectly can answer 403 to a sign-in, with a correct
# page saying why, and /readyz is green either way, so it looks like a gap worth
# a third check kind. It is not a gap in THIS page. Sign-up being closed is a
# DECISION rather than an outage, and publishing a decision as an incident is
# how a status page stops being believed. .github/workflows/signup.yml checks it
# every morning against `signupsOpen` and `selfServeSignup` on /auth/session,
# which is more than a redirect could ever have told anybody, and it fails to us
# rather than to a customer.

set -uo pipefail

TARGETS="${1:?usage: probe.sh <targets.json>}"

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# One temporary file, reused, rather than a command substitution holding a
# response body: the installer is 16 KB of shell and the marketing home page is
# 340 KB of HTML, and passing either through a shell variable to grep costs
# more than writing it down.
BODY="$(mktemp)"
trap 'rm -f "$BODY"' EXIT

jq -c '.[]' "$TARGETS" | while read -r target; do
  id="$(jq -r '.id' <<<"$target")"
  name="$(jq -r '.name' <<<"$target")"
  group="$(jq -r '.group' <<<"$target")"
  url="$(jq -r '.url' <<<"$target")"
  check="$(jq -r '.check' <<<"$target")"
  expect_body="$(jq -r '.expect_body // ""' <<<"$target")"

  # -L, because antifailure.dev redirects a trailing slash and a probe that
  # called a 301 an outage would report one every five minutes forever.
  # --max-redirs bounds a redirect loop, which is itself a failure worth
  # reporting rather than hanging on.
  metrics="$(curl -sSL -m 20 --max-redirs 5 -o "$BODY" \
    -w '%{http_code} %{time_total}' "$url" 2>/dev/null || echo "000 0")"
  code="${metrics%% *}"
  seconds="${metrics##* }"
  case "$code" in ''|*[!0-9]*) code=0 ;; esac

  # awk rather than bash arithmetic, because curl prints a decimal and bash
  # has no floats. LC_ALL=C so a comma decimal separator on a differently
  # configured runner cannot silently produce a duration of zero.
  duration_ms="$(LC_ALL=C awk -v s="$seconds" 'BEGIN { printf "%d", (s * 1000) + 0.5 }' 2>/dev/null || echo 0)"

  ok=false
  ready=null
  commit=""
  detail=""

  if [ "$code" = "000" ] || [ "$code" = "0" ]; then
    detail="no response"
  elif [ "$code" != "200" ]; then
    detail="HTTP $code"
  else
    case "$check" in
      readyz)
        if grep -q '"ready"[[:space:]]*:[[:space:]]*true' "$BODY"; then
          ok=true
          ready=true
        else
          ready=false
          detail="answered 200 but not ready"
        fi
        commit="$(sed -n 's/.*"commit"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$BODY" | head -1)"
        ;;
      http)
        if [ -z "$expect_body" ]; then
          # A configuration mistake, not an outage. Saying which it is here is
          # the difference between fixing targets.json and paging somebody.
          detail="no expect_body configured for an http check"
        elif grep -qF -- "$expect_body" "$BODY"; then
          ok=true
        else
          detail="answered 200 without $expect_body"
        fi
        ;;
      *)
        detail="unknown check kind: $check"
        ;;
    esac
  fi

  jq -nc \
    --arg checked_at "$(now)" \
    --arg id "$id" \
    --arg name "$name" \
    --arg group "$group" \
    --arg url "$url" \
    --arg check "$check" \
    --argjson http_status "${code:-0}" \
    --argjson ok "$ok" \
    --argjson ready "$ready" \
    --argjson duration_ms "${duration_ms:-0}" \
    --arg commit "${commit:-}" \
    --arg detail "$detail" \
    '{checked_at: $checked_at, id: $id, name: $name, group: $group, url: $url,
      check: $check, http_status: $http_status, ok: $ok, ready: $ready,
      duration_ms: $duration_ms, commit: $commit, detail: $detail}'
done
