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
#   deploy.sh <resource-group> <app> <bootstrap-job> <maintenance-job> <image> <commit> <base-url>

set -euo pipefail

RG="${1:?resource group}"
APP="${2:?container app}"
BOOTSTRAP_JOB="${3:?bootstrap job}"
MAINTENANCE_JOB="${4:?maintenance job}"
IMAGE="${5:?image reference, digest-pinned}"
COMMIT="${6:?commit being deployed}"
BASE_URL="${7:?public origin}"

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
  local keep_new="$1" keep_previous="$2" reaped=0 revision created born

  # WHEN THIS DEPLOY'S REVISION WAS BORN, and why a name list is not enough.
  #
  # "Superseded" means older than the revision this deploy just made. It does
  # NOT mean "everything except the two names I am holding", which is what this
  # loop used to mean, and the difference is a revision belonging to somebody
  # else's deploy that is running at the same time as this one.
  #
  # That is not hypothetical. On 2026-09-02 the v1.0.0 tag and the merge to main
  # named the same commit, and cd.yml's concurrency group is keyed on the ref,
  # so the tag run and the branch run deployed to this app together. The branch
  # run finished first and reaped, at 17:55:03, a revision created at 17:54:32
  # by the tag run: a container that had pulled its image, started, and printed
  # "control plane listening on :8080" three seconds earlier. Its own log ends
  # "SIGTERM: draining". The tag run then spent ninety seconds asking a dead
  # address, was told "This Container App is stopped or does not exist", and
  # reported the release as a boot failure. Nothing had failed to boot.
  #
  # So the age is read and compared. A revision created at or after ours belongs
  # to a deploy we did not supersede and is left alone, whoever started it.
  born="$(az containerapp revision show -n "$APP" -g "$RG" --revision "$keep_new" \
    --query "properties.createdTime" -o tsv 2>/dev/null || true)"
  if [ -z "$born" ] || [ "$born" = "None" ]; then
    # Said out loud rather than reaping blind. Without our own timestamp there
    # is no way to tell a superseded revision from a concurrent one, and
    # deactivating the wrong one kills a deploy somebody is watching.
    say "could not read when $keep_new was created: nothing is reaped, and the connection budget below is what decides whether that is safe"
    return 0
  fi

  say "reaping revisions superseded by $keep_new (created $born, keeping $keep_previous to roll back onto)"
  # --query on the server rather than a client-side filter, so a listing that
  # grows to the hundred revisions Container Apps retains is still one page.
  # Name and creation time together, because the decision needs both.
  while read -r revision created; do
    [ -z "$revision" ] && continue
    [ "$revision" = "$keep_new" ] && continue
    [ "$revision" = "$keep_previous" ] && continue
    # Lexicographic on the ISO 8601 the API returns, which sorts correctly
    # because every value carries the same offset and the same field widths.
    if [ -z "$created" ] || [[ ! "$created" < "$born" ]]; then
      echo "leaving $revision alone: created ${created:-unknown}, not older than $keep_new"
      continue
    fi
    # Deliberately tolerant. A revision that cannot be deactivated is a
    # connection this deploy did not reclaim, not a reason to fail a release
    # that is already serving; assert_connection_budget below is what decides
    # whether the result is safe.
    if az containerapp revision deactivate -n "$APP" -g "$RG" --revision "$revision" -o none 2>/dev/null; then
      reaped=$((reaped + 1))
    else
      echo "could not deactivate $revision; it is still holding its connections"
    fi
  done < <(az containerapp revision list -n "$APP" -g "$RG" \
    --query "[?properties.active].[name, properties.createdTime]" -o tsv 2>/dev/null || true)
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
  #
  # Read into a variable and summed separately rather than piped straight into
  # awk. Under `set -o pipefail` a pipeline takes the worst status in it, and a
  # failing `az` inside a command substitution then kills this script through
  # `set -e` with no message at all and the deploy's exit code, which is how a
  # transient API error would turn into an unexplained red release.
  local listing
  listing="$(az containerapp revision list -n "$APP" -g "$RG" \
    --query "[?properties.active].properties.replicas" -o tsv 2>/dev/null || true)"
  if [ -z "$listing" ]; then
    say "could not list $APP's revisions: the connection budget is unchecked"
    return 0
  fi
  replicas="$(printf '%s\n' "$listing" | awk 'BEGIN { n = 0 } /^[0-9]+$/ { n += $1 } END { print n }')"
  if [ -z "$replicas" ] || [ "$replicas" -eq 0 ]; then
    say "no running replicas reported for $APP: the connection budget is unchecked"
    return 0
  fi

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
say "migration pre-check: running $BOOTSTRAP_JOB on the new image"

az containerapp job update -n "$BOOTSTRAP_JOB" -g "$RG" --image "$IMAGE" -o none

EXEC="$(az containerapp job start -n "$BOOTSTRAP_JOB" -g "$RG" --query name -o tsv)"
say "bootstrap execution: $EXEC"

# Poll rather than trusting a single read: the execution is Running for a while
# and its terminal state is the only one worth acting on.
for _ in $(seq 1 60); do
  STATUS="$(az containerapp job execution show -n "$BOOTSTRAP_JOB" -g "$RG" --job-execution-name "$EXEC" \
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

# The budget running out is a verdict too, and this loop used to have no way to
# say so. It breaks on Running and exits on Failed or Degraded, and every other
# state fell out of the bottom silently after five minutes: Activating, Unknown,
# Deactivating, or a revision that somebody else stopped while we waited. The
# script then smoke tested an address with nothing behind it and reported a
# health gate failure, which reads as "the build is broken" and is a different
# claim from "the platform never gave us a running revision".
#
# The migration loop above already asserts after its own budget for exactly this
# reason. This is the same assertion in the same shape.
if [ "${STATE:-}" != "Running" ]; then
  say "NEW REVISION NEVER REACHED Running (last state: ${STATE:-unknown}). Traffic never moved; $PREVIOUS is still serving."
  echo "This is the platform's answer about the revision, not the application's"
  echo "answer about itself. Read the revision in the portal and its container"
  echo "logs before assuming the image is at fault."
  az containerapp revision deactivate -n "$APP" -g "$RG" --revision "$NEW" -o none 2>/dev/null || true
  exit 1
fi

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
  say "NO CANDIDATE ADDRESS. Cannot prove the revision is healthy. Traffic never moved; $PREVIOUS is still serving."
  az containerapp revision deactivate -n "$APP" -g "$RG" --revision "$NEW" -o none || true
  exit 1
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

  # The scheduled job must run the same code that is serving. Terraform creates
  # it from a release tag, but continuous deployment moves the application by
  # digest. Before this update, every app deploy left maintenance on the tag
  # Terraform last applied. Production reached v1.1.0 while the partition job
  # still ran v1.0.0.
  #
  # This is after both health gates. Moving the job before the candidate proves
  # itself would let a failed release change the process that runs DDL later.
  # A failure here does not roll back a healthy application. It makes the run
  # fail with the app serving and the scheduled job unchanged, which is the
  # exact state an operator has to repair.
  maintenance_updated=true
  say "updating scheduled maintenance job $MAINTENANCE_JOB to the tested image"
  if az containerapp job update -n "$MAINTENANCE_JOB" -g "$RG" --image "$IMAGE" -o none; then
    maintenance_image="$(az containerapp job show -n "$MAINTENANCE_JOB" -g "$RG" \
      --query "properties.template.containers[0].image" -o tsv 2>/dev/null || true)"
    if [ "$maintenance_image" = "$IMAGE" ]; then
      say "maintenance job now uses $IMAGE"
    else
      maintenance_updated=false
      echo "maintenance image read back as ${maintenance_image:-nothing}; expected $IMAGE"
    fi
  else
    maintenance_updated=false
  fi
  if [ "$maintenance_updated" != true ]; then
    echo "::error title=Maintenance image stayed behind::$APP is healthy on $COMMIT, but $MAINTENANCE_JOB is not on $IMAGE. The application remains on the healthy revision."
  fi

  assert_connection_budget
  if [ "$maintenance_updated" != true ]; then
    exit 1
  fi
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
