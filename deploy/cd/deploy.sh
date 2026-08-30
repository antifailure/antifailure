#!/usr/bin/env bash
# One deployment of the control plane to a Container App, with a way back.
#
# The shape, and why each step is where it is:
#
#   1. Migrate FIRST, in a job, before any traffic moves. Migrations are the
#      irreversible half of a deploy; if they fail, nothing has changed and the
#      old revision is still serving. Rolling traffic first and migrating second
#      means a failed migration has already broken the running app.
#   2. Create the new revision with ZERO traffic. It starts, connects, and is
#      checked on its own address while every real request still goes to the old
#      one. A revision that cannot start never reaches a user.
#   3. Shift traffic.
#   4. Check the public origin, including which commit answers.
#   5. On any failure after step 2, put traffic back on the revision that was
#      serving before and deactivate the new one.
#
# Step 5 is only fast because the app runs in Multiple revision mode: the old
# revision is still up with no traffic, so the way back is one API call rather
# than a rebuild.
#
# WHAT THIS DOES NOT DO. It does not roll migrations back. A schema change that
# applied and a revision that was reverted leave the old code running against
# the new schema, which is why migrations in this project are expected to be
# backward compatible with the previous release. That constraint is real and is
# stated in docs/plan/notes/cd.md rather than pretended away here.
#
#   deploy.sh <resource-group> <app> <bootstrap-job> <image> <commit> <base-url>

set -euo pipefail

RG="${1:?resource group}"
APP="${2:?container app}"
JOB="${3:?bootstrap job}"
IMAGE="${4:?image reference, digest-pinned}"
COMMIT="${5:?commit being deployed}"
BASE_URL="${6:?public origin}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

say() { printf '\n=== %s\n' "$*"; }

# ---------------------------------------------------------------------------
# What is serving right now. Captured BEFORE anything changes, because it is
# the thing we go back to, and reading it after a failure means reading it from
# a system that is already in the state we are trying to escape.
# ---------------------------------------------------------------------------
PREVIOUS="$(az containerapp ingress traffic show -n "$APP" -g "$RG" \
  --query "[?weight>\`0\`] | [0].revisionName" -o tsv 2>/dev/null || true)"
if [ -z "$PREVIOUS" ] || [ "$PREVIOUS" = "None" ]; then
  # In Multiple mode with latestRevision traffic, the weight entry can name no
  # revision. Fall back to whichever revision is actually running.
  PREVIOUS="$(az containerapp show -n "$APP" -g "$RG" \
    --query "properties.latestReadyRevisionName" -o tsv)"
fi
say "currently serving: $PREVIOUS"

# ---------------------------------------------------------------------------
# 1. Migrations, before traffic.
# ---------------------------------------------------------------------------
say "migration pre-check: running $JOB on the new image"

az containerapp job update -n "$JOB" -g "$RG" --image "$IMAGE" -o none

EXEC="$(az containerapp job start -n "$JOB" -g "$RG" --query name -o tsv)"
say "bootstrap execution: $EXEC"

# Poll rather than trusting a single read: the execution is Running for a while
# and its terminal state is the only one worth acting on.
for _ in $(seq 1 60); do
  STATUS="$(az containerapp job execution show -n "$JOB" -g "$RG" --job-execution-name "$EXEC" \
    --query "properties.status" -o tsv 2>/dev/null || echo Unknown)"
  case "$STATUS" in
    Succeeded) break ;;
    Failed|Degraded)
      say "MIGRATION FAILED ($STATUS). No traffic has moved; $PREVIOUS is still serving."
      echo "Read the job's logs before retrying. A partly applied schema is not"
      echo "something this script will paper over by deploying anyway."
      exit 1
      ;;
  esac
  sleep 5
done

if [ "${STATUS:-}" != "Succeeded" ]; then
  say "MIGRATION DID NOT FINISH within the budget (last status: ${STATUS:-unknown})."
  echo "Refusing to move traffic onto a build whose migrations may still be running."
  exit 1
fi
say "migrations applied"

# ---------------------------------------------------------------------------
# 2. The new revision, at zero traffic.
# ---------------------------------------------------------------------------
SUFFIX="c$(printf '%s' "$COMMIT" | cut -c1-8)-$(date +%H%M%S)"
say "creating revision $APP--$SUFFIX at zero traffic"

az containerapp update -n "$APP" -g "$RG" --image "$IMAGE" --revision-suffix "$SUFFIX" -o none

NEW="$APP--$SUFFIX"

# Wait for it to be ready before checking it, so that a slow start is not read
# as a failure.
for _ in $(seq 1 60); do
  STATE="$(az containerapp revision show -n "$APP" -g "$RG" --revision "$NEW" \
    --query "properties.runningState" -o tsv 2>/dev/null || echo Unknown)"
  [ "$STATE" = "Running" ] && break
  case "$STATE" in
    Failed|Degraded)
      say "NEW REVISION FAILED TO START ($STATE). Traffic never moved."
      az containerapp revision deactivate -n "$APP" -g "$RG" --revision "$NEW" -o none || true
      exit 1
      ;;
  esac
  sleep 5
done

REV_FQDN="$(az containerapp revision show -n "$APP" -g "$RG" --revision "$NEW" \
  --query "properties.fqdn" -o tsv 2>/dev/null || true)"

# ---------------------------------------------------------------------------
# 3. Check it before it can hurt anyone.
# ---------------------------------------------------------------------------
if [ -n "$REV_FQDN" ] && [ "$REV_FQDN" != "None" ]; then
  say "smoke test on the new revision only: https://$REV_FQDN"
  if ! "$HERE/health-gate.sh" "https://$REV_FQDN" "$COMMIT" 30 3; then
    say "THE NEW REVISION IS NOT HEALTHY. Traffic never moved; $PREVIOUS is still serving."
    az containerapp revision deactivate -n "$APP" -g "$RG" --revision "$NEW" -o none || true
    exit 1
  fi
else
  say "no per-revision address available; skipping the pre-promotion smoke test"
fi

# ---------------------------------------------------------------------------
# 4. Promote, then check the real origin.
# ---------------------------------------------------------------------------
say "shifting 100% of traffic to $NEW"
az containerapp ingress traffic set -n "$APP" -g "$RG" --revision-weight "$NEW=100" -o none

say "post-deploy health gate on $BASE_URL"
if "$HERE/health-gate.sh" "$BASE_URL" "$COMMIT" 40 3; then
  say "DEPLOYED: $BASE_URL is serving $COMMIT from $NEW"
  # Old revisions are left active rather than deactivated. They cost nothing at
  # zero traffic, and they are what the next rollback shifts back onto.
  exit 0
fi

# ---------------------------------------------------------------------------
# 5. Back.
# ---------------------------------------------------------------------------
say "HEALTH GATE FAILED AFTER PROMOTION. Rolling back to $PREVIOUS"

if [ -z "$PREVIOUS" ] || [ "$PREVIOUS" = "$NEW" ]; then
  # Nothing to go back to: this was the first deploy, or the previous revision
  # is the one that just failed. Say so plainly rather than pretending a
  # rollback happened.
  say "NO PREVIOUS REVISION TO ROLL BACK TO. The deployment is unhealthy and stays that way."
  exit 1
fi

az containerapp ingress traffic set -n "$APP" -g "$RG" --revision-weight "$PREVIOUS=100" -o none
az containerapp revision deactivate -n "$APP" -g "$RG" --revision "$NEW" -o none || true

say "rolled back; verifying $PREVIOUS is serving"
# 'any' because the commit we want is precisely the one we no longer know: the
# previous revision's build is whatever it was, and demanding a specific one
# here would fail a rollback that worked.
if "$HERE/health-gate.sh" "$BASE_URL" any 20 3; then
  say "ROLLED BACK. $BASE_URL is healthy again on $PREVIOUS. The deploy failed."
else
  say "ROLLBACK DID NOT RESTORE HEALTH. $BASE_URL is still unhealthy. This needs a person."
fi

# Non-zero either way. The deploy failed; a successful rollback is the damage
# being contained, not the deploy having worked.
exit 1
