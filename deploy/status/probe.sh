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
# Three check kinds, because three things are worth proving and a 200 proves
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
#   redirect  follows nothing and reads the Location header. It exists for one
#             subject neither of the others can see: whether pressing the
#             sign-in button starts an exchange at all. A control plane serving
#             traffic perfectly can answer 403 here, with a correct page saying
#             why, and /readyz is green either way because the origin is fine.
#
#             WHAT IT CANNOT SEE, said here rather than discovered later. The
#             403 arrives only when the allowlist names NOBODY. A plane whose
#             allowlist names two people redirects to GitHub exactly like an
#             open one and refuses at the callback, which is after the visitor
#             has authorised an application, and this probe cannot tell the two
#             apart. The check that can is in .github/workflows/signup.yml,
#             which reads `signupsOpen` off /auth/session; it is a scheduled
#             workflow rather than a row here because it goes red the moment a
#             decision changes, and the audience for that is us rather than
#             somebody reading a status page.
#
# The markers are chosen to be structural rather than editorial. `/_next/static/`
# and `/docs/_astro/` are build output paths, `https://app.antifailure.dev` is
# the product API origin the site API publishes, the authorize URL is GitHub's
# own, and `#!/bin/sh` is the installer's shebang. None of them moves when
# somebody rewrites a headline, because a marker that tracks copy turns a prose
# edit into a false outage, and a false outage is the one thing this page must
# never publish.

set -uo pipefail

TARGETS="${1:?usage: probe.sh <targets.json>}"

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# One temporary file, reused, rather than a command substitution holding a
# response body: the installer is 16 KB of shell and the marketing home page is
# 340 KB of HTML, and passing either through a shell variable to grep costs
# more than writing it down.
BODY="$(mktemp)"
# A second file for the response headers, because a redirect check reads
# Location and curl will not write headers and body to the same place usefully.
HEADERS="$(mktemp)"
trap 'rm -f "$BODY" "$HEADERS"' EXIT

jq -c '.[]' "$TARGETS" | while read -r target; do
  id="$(jq -r '.id' <<<"$target")"
  name="$(jq -r '.name' <<<"$target")"
  group="$(jq -r '.group' <<<"$target")"
  url="$(jq -r '.url' <<<"$target")"
  check="$(jq -r '.check' <<<"$target")"
  expect_body="$(jq -r '.expect_body // ""' <<<"$target")"
  expect_location="$(jq -r '.expect_location // ""' <<<"$target")"

  # -L for everything except a redirect check, because antifailure.dev redirects
  # a trailing slash and a probe that called a 301 an outage would report one
  # every five minutes forever. --max-redirs bounds a redirect loop, which is
  # itself a failure worth reporting rather than hanging on.
  #
  # A redirect check must NOT follow, and that is the whole of why it is a
  # separate kind rather than a flag. Following the hop to GitHub would make the
  # subject GitHub's availability rather than ours, and a page that reported our
  # sign-up as down because github.com was slow would be reporting somebody
  # else's outage under our name.
  location=""
  if [ "$check" = "redirect" ]; then
    metrics="$(curl -sS -m 20 -o "$BODY" -D "$HEADERS" \
      -w '%{http_code} %{time_total}' "$url" 2>/dev/null || echo "000 0")"
    # Case-insensitive, because a header name is, and trimmed of the carriage
    # return HTTP puts at the end of every line.
    location="$(sed -n 's/^[Ll]ocation:[[:space:]]*//p' "$HEADERS" | tr -d '\r' | head -1)"
  else
    metrics="$(curl -sSL -m 20 --max-redirs 5 -o "$BODY" \
      -w '%{http_code} %{time_total}' "$url" 2>/dev/null || echo "000 0")"
  fi
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
  elif [ "$check" = "redirect" ]; then
    # Handled before the 200 rule, because a 200 is the FAILURE here: this
    # subject is healthy when it sends somebody somewhere, and answering the
    # request itself means it did not.
    if [ -z "$expect_location" ]; then
      detail="no expect_location configured for a redirect check"
    elif [ "$code" != "302" ] && [ "$code" != "301" ] && [ "$code" != "303" ] && [ "$code" != "307" ]; then
      # 403 is the one worth naming, because it is not an outage: the origin is
      # up and is deliberately refusing. Saying which it is here is the
      # difference between paging somebody and reading the allowlist.
      if [ "$code" = "403" ]; then
        detail="HTTP 403: sign-in is closed on this control plane, so nobody can sign up"
      else
        detail="HTTP $code, expected a redirect"
      fi
    elif [ "${location#"$expect_location"}" != "$location" ]; then
      ok=true
    else
      detail="redirected to ${location:-nowhere} rather than $expect_location"
    fi
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
