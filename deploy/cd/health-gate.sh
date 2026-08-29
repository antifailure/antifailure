#!/usr/bin/env bash
# Is this deployment actually serving, and is it serving the build we deployed?
#
# Two questions, and the second one is the half that gets skipped. A gate that
# only asks "does the origin answer" passes when the rollout silently did not
# happen and the previous build is answering every request perfectly. So this
# checks the commit as well, and treats a healthy wrong build as a failure.
#
# WHY NOT /health. It is a static literal that touches nothing, deliberately, so
# that a liveness probe cannot turn a slow database into a restart loop. On this
# deployment's first day it answered 200 for thirteen minutes while the database
# had no schema and every real endpoint returned 500. /readyz is the one that
# runs a query, and it is the one this gate reads.
#
#   health-gate.sh <base-url> <expected-commit> [attempts] [interval-seconds]
#
# Exit 0 when the origin is ready on the expected commit within the budget,
# 1 otherwise. Prints one line per attempt so a CI log shows the recovery rather
# than only the verdict.

set -uo pipefail

BASE="${1:?usage: health-gate.sh <base-url> <expected-commit> [attempts] [interval]}"
WANT_COMMIT="${2:?expected commit is required; pass 'any' only for a smoke test of an unbuilt image}"
ATTEMPTS="${3:-40}"
INTERVAL="${4:-3}"

BASE="${BASE%/}"

say() { printf '%s\n' "$*"; }

# A deploy is not finished the instant the revision reports provisioned: the
# first request still pays for the pool opening its connections. Retrying is
# therefore normal and is not the same as tolerating failure, because the budget
# is finite and the last attempt is fatal.
last_reason="no attempt completed"

for attempt in $(seq 1 "$ATTEMPTS"); do
  body="$(curl -fsS -m 10 "$BASE/readyz" 2>/dev/null)"
  rc=$?

  if [ "$rc" -ne 0 ]; then
    # curl -f makes a 503 an error, which is what we want: not ready is not a
    # pass. Read the body anyway so the reason reaches the log.
    body="$(curl -sS -m 10 "$BASE/readyz" 2>/dev/null)"
    code="$(curl -sS -m 10 -o /dev/null -w '%{http_code}' "$BASE/readyz" 2>/dev/null)"
    last_reason="HTTP ${code:-000}: ${body:-no body}"
    say "attempt $attempt/$ATTEMPTS: not ready - $last_reason"
    sleep "$INTERVAL"
    continue
  fi

  ready="$(printf '%s' "$body" | grep -o '"ready"[[:space:]]*:[[:space:]]*true' || true)"
  if [ -z "$ready" ]; then
    last_reason="ready was not true: $body"
    say "attempt $attempt/$ATTEMPTS: $last_reason"
    sleep "$INTERVAL"
    continue
  fi

  got_commit="$(printf '%s' "$body" | sed -n 's/.*"commit"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"

  if [ "$WANT_COMMIT" != "any" ]; then
    # Compared on the prefix, because the image is stamped with the full sha and
    # a short sha is what a person pastes. Anchored at the start so one is a
    # prefix of the other rather than merely containing it.
    case "$WANT_COMMIT" in
      "$got_commit"*) ;;
      *)
        case "$got_commit" in
          "$WANT_COMMIT"*) ;;
          *)
            # THIS IS THE CHECK THAT CATCHES A ROLLOUT THAT DID NOT HAPPEN.
            # The origin is healthy. It is healthy on the wrong build, which
            # means traffic never moved, and reporting success here would mean
            # every future deploy inherits a lie about what is running.
            last_reason="serving commit '$got_commit', expected '$WANT_COMMIT'"
            say "attempt $attempt/$ATTEMPTS: healthy but wrong build - $last_reason"
            sleep "$INTERVAL"
            continue
            ;;
        esac
        ;;
    esac
  fi

  say "ready on commit ${got_commit:-unknown} after $attempt attempt(s)"
  say "$body"
  exit 0
done

say ""
say "HEALTH GATE FAILED after $ATTEMPTS attempts over $((ATTEMPTS * INTERVAL))s."
say "  origin: $BASE"
say "  wanted commit: $WANT_COMMIT"
say "  last: $last_reason"
exit 1
