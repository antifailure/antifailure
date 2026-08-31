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
#   5. Deactivate every revision this deploy superseded except one, and prove
#      what is left fits in the database's connection budget.
#   6. On any failure after step 2, put traffic back on the revision that was
#      serving before and deactivate the new one.
#
# Step 6 is only fast because the app runs in Multiple revision mode: the old
# revision is still up with no traffic, so the way back is one API call rather
# than a rebuild. Step 5 exists because that is true of exactly ONE old
# revision and this script used to keep every one of them.
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

# What the database keeps back for tools that are not the application: the
# bootstrap job's migration connection, the maintenance job's, a break-glass
# session, and a backup or restore run. Each of those opens with max: 1, and
# all four can overlap during a release.
TOOL_CONNECTIONS=4

# ---------------------------------------------------------------------------
# Reaping the revisions a deploy superseded.
#
# A revision at zero traffic is NOT idle, and this script used to say it was:
# "they cost nothing at zero traffic". In Multiple revision mode an active
# revision keeps min_replicas running whether or not any request reaches it,
# and each of those replicas is a whole control plane process holding a
# Postgres pool of AF_POOL_MAX and running the five minute housekeeping sweep
# against the same database forever.
#
# What that cost, measured: forty six deploys to staging left forty six active
# revisions and forty six running replicas against a B1ms server with thirty
# five non-reserved connection slots, and /readyz answered
#
#   remaining connection slots are reserved for roles with privileges of the
#   "pg_use_reserved_connections" role
#
# One revision is kept, because one is all step 6 ever shifts back onto. Going
# further back than the release before this one is a redeploy either way: the
# image is still in the registry and the schema has moved on.
# ---------------------------------------------------------------------------
reap_superseded() {
  local keep_new="$1" keep_previous="$2" reaped=0 revision

  say "reaping revisions superseded by $keep_new (keeping $keep_previous to roll back onto)"
  # --query on the server rather than a client-side filter, so a listing that
  # grows to the hundred revisions Container Apps retains is still one page.
  for revision in $(az containerapp revision list -n "$APP" -g "$RG" \
    --query "[?properties.active].name" -o tsv 2>/dev/null || true); do
    [ "$revision" = "$keep_new" ] && continue
    [ "$revision" = "$keep_previous" ] && continue
    # Deliberately tolerant. A revision that cannot be deactivated is a
    # connection this deploy did not reclaim, not a reason to fail a release
    # that is already serving; assert_connection_budget below is what decides
    # whether the result is safe.
    if az containerapp revision deactivate -n "$APP" -g "$RG" --revision "$revision" -o none 2>/dev/null; then
      reaped=$((reaped + 1))
    else
      echo "could not deactivate $revision; it is still holding its connections"
    fi
  done
  say "deactivated $reaped superseded revision(s)"
}

# ---------------------------------------------------------------------------
# The connection arithmetic, measured rather than assumed.
#
# Every number here is read back from the running system instead of predicted,
# because every one of them has already been wrong once: the pool size is an
# environment variable somebody can edit in a portal, the replica count is
# whatever the platform scaled to, and the connection budget is a server
# parameter derived from a SKU. The product of three numbers nobody checks is
# how a control plane runs out of connection slots without a single line of
# code changing.
#
# The budget subtracts BOTH reserved settings, which is the part the alert in
# infra/terraform/modules/alerting/database.tf originally got wrong. Postgres
# refuses an ordinary role at max_connections minus reserved_connections minus
# superuser_reserved_connections, not at max_connections, and on a B1ms those
# are 50, 5 and 10: the application gets 35, not 50.
#
# This runs AFTER the reap and after traffic has moved, so it never blocks a
# healthy release from serving. What it does is fail the run loudly, so that a
# shape which cannot fit is an error in a deploy log rather than a 503 at the
# next traffic peak.
# ---------------------------------------------------------------------------
read_db_param() {
  az postgres flexible-server parameter show -g "$RG" -s "$1" -n "$2" \
    --query value -o tsv 2>/dev/null || echo ""
}

assert_connection_budget() {
  local server pool_max replicas used budget max_conn reserved su_reserved

  server="$(az postgres flexible-server list -g "$RG" --query "[0].name" -o tsv 2>/dev/null || true)"
  if [ -z "$server" ] || [ "$server" = "None" ]; then
    # Said out loud rather than skipped silently. An installation whose database
    # is not in this resource group is a supported shape; a check that quietly
    # passes because it found nothing to check is not.
    say "no flexible server in $RG: cannot check the connection budget from here"
    return 0
  fi

  max_conn="$(read_db_param "$server" max_connections)"
  reserved="$(read_db_param "$server" reserved_connections)"
  su_reserved="$(read_db_param "$server" superuser_reserved_connections)"
  if [ -z "$max_conn" ]; then
    say "could not read max_connections from $server: the connection budget is unchecked"
    return 0
  fi
  budget=$((max_conn - ${reserved:-0} - ${su_reserved:-0} - TOOL_CONNECTIONS))

  pool_max="$(az containerapp show -n "$APP" -g "$RG" \
    --query "properties.template.containers[0].env[?name=='AF_POOL_MAX'].value | [0]" -o tsv 2>/dev/null || true)"
  # Unset means the application's own default, which main.ts states as 10. The
  # fallback is written here rather than assumed, so a variable that goes
  # missing cannot make this check compute zero and pass.
  if [ -z "$pool_max" ] || [ "$pool_max" = "None" ]; then
    pool_max=10
  fi

  # Replicas actually RUNNING across every active revision, summed rather than
  # counted, because this is the number that was wrong: one serving revision
  # does not mean one process, and a revision at zero traffic still runs
  # min_replicas of them.
  replicas="$(az containerapp revision list -n "$APP" -g "$RG" \
    --query "[?properties.active].properties.replicas" -o tsv 2>/dev/null |
    awk 'BEGIN { n = 0 } /^[0-9]+$/ { n += $1 } END { print n }')"
  [ -z "$replicas" ] && replicas=0

  used=$((replicas * pool_max))
  say "connection budget: $replicas running replica(s) x $pool_max pooled = $used against $budget usable ($max_conn max_connections, less ${reserved:-0} reserved, ${su_reserved:-0} superuser reserved, $TOOL_CONNECTIONS for tools)"

  if [ "$used" -gt "$budget" ]; then
    echo "::error title=Connection budget exceeded::$APP can open $used connections to $server, which has $budget for it. Lower pool_max in infra/terraform/stacks/control-plane, lower max_replicas, or move to a SKU with more max_connections."
    say "THE DEPLOY IS SERVING AND ITS CONNECTION ARITHMETIC DOES NOT FIT."
    echo "This is the shape that took staging down: replicas x pool_max above what"
    echo "the server will hand out, so the next scale-up answers /readyz with"
    echo "'remaining connection slots are reserved'. Nothing is rolled back here,"
    echo "because rolling back does not shrink the number."
    exit 1
  fi
}

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
  reap_superseded "$NEW" "$PREVIOUS"
  assert_connection_budget
  exit 0
fi

# ---------------------------------------------------------------------------
# 6. Back.
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
