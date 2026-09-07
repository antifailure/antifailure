#!/usr/bin/env bash

# The application and its scheduled DDL job move as one release.
#
# This runs the real deploy script against a fake Azure command and a fake
# readiness origin. It checks the ordering rather than searching source text:
# the maintenance image moves only after the candidate revision and public
# origin are healthy, and every failure before that point leaves it alone.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

cat > "$TMP/bin/az" <<'FAKE_AZ'
#!/usr/bin/env bash
set -euo pipefail

log="${AF_DEPLOY_TEST_LOG:?}"
command="$*"
printf 'az %s\n' "$command" >> "$log"

name=""
image=""
query=""
weight=""
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
  case "${args[$i]}" in
    -n) name="${args[$((i + 1))]}" ;;
    --image) image="${args[$((i + 1))]}" ;;
    --query) query="${args[$((i + 1))]}" ;;
    --revision-weight) weight="${args[$((i + 1))]}" ;;
  esac
done

state="${AF_DEPLOY_TEST_STATE:?}"

# How many times this exact operation has been tried in this case. Kept on disk
# because every attempt is a separate process, and the count is the whole point:
# a retry that fires once and a retry that fires six times are different
# behaviours and an assertion has to be able to tell them apart.
attempt_number() {
  local file="$state/$1" n=0
  if [ -f "$file" ]; then
    n="$(cat "$file")"
  fi
  n=$((n + 1))
  printf '%s' "$n" > "$file"
  printf '%s' "$n"
}

# The message Azure actually produced on 2026-09-06 for a transient ARM lookup
# failure, reproduced verbatim, including the fact that it is word for word what
# a genuinely wrong app name says. The whole point of the case is that the text
# cannot tell you which one it is.
transient_lookup_failure() {
  printf "ERROR: The containerapp '%s' does not exist in group 'group'\n" "$name" >&2
  exit 3
}

case "$command" in
  "containerapp ingress traffic show"*)
    if [ "${AF_DEPLOY_TEST_SERVING:-known}" = unknown ]; then
      :
    elif [ -f "$state/serving" ]; then
      cat "$state/serving"
    else
      printf 'old-revision\n'
    fi
    ;;
  "containerapp show"*)
    # The second question the retry asks. A genuinely wrong app name answers it
    # the same way it answered the shift, and that is what makes the shift fail
    # fast instead of waiting.
    case "$query" in
      name)
        if [ "${AF_DEPLOY_TEST_APP_PRESENT:-yes}" = no ]; then
          transient_lookup_failure
        fi
        printf '%s\n' "$name"
        ;;
      properties.latestReadyRevisionName)
        # An app with nothing serving yet, and an app whose read failed, are the
        # same empty answer here. Both leave the deploy with no rollback target.
        if [ "${AF_DEPLOY_TEST_SERVING:-known}" != unknown ]; then
          printf 'old-revision\n'
        fi
        ;;
    esac
    ;;
  "containerapp job update"*)
    printf 'job-update %s %s\n' "$name" "$image" >> "$log"
    if [ "$name" = maintenance ] && [ "${AF_DEPLOY_TEST_MAINTENANCE:-ok}" = fail ]; then
      exit 41
    fi
    ;;
  "containerapp job show"*)
    if [ "${AF_DEPLOY_TEST_MAINTENANCE_READBACK:-current}" = stale ]; then
      printf 'ghcr.io/example/control-plane@sha256:old\n'
    else
      printf 'ghcr.io/example/control-plane@sha256:tested\n'
    fi
    ;;
  "containerapp job start"*)
    printf 'bootstrap-execution\n'
    ;;
  "containerapp job execution show"*)
    printf 'Succeeded\n'
    ;;
  "containerapp update"*)
    printf 'app-update %s\n' "$image" >> "$log"
    ;;
  "containerapp revision show"*)
    case "$query" in
      properties.runningState)
        if [ "${AF_DEPLOY_TEST_REVISION:-ok}" = fail ]; then
          printf 'Failed\n'
        else
          printf 'Running\n'
        fi
        ;;
      properties.fqdn)
        if [ "${AF_DEPLOY_TEST_ADDRESS:-present}" != missing ]; then
          printf 'candidate.example\n'
        fi
        ;;
      properties.createdTime) printf '2026-09-04T00:00:00Z\n' ;;
    esac
    ;;
  "containerapp ingress traffic set"*)
    printf 'traffic %s\n' "$command" >> "$log"
    target="${weight%%=*}"
    # Which shift this is, decided by where it is pointing rather than by a
    # counter, so that a case can break the promotion without breaking the
    # rollback that follows it.
    if [ "$target" = old-revision ]; then
      mode="${AF_DEPLOY_TEST_ROLLBACK_TRAFFIC:-ok}"
      tries="$(attempt_number rollback-attempts)"
    else
      mode="${AF_DEPLOY_TEST_TRAFFIC:-ok}"
      tries="$(attempt_number promote-attempts)"
    fi
    case "$mode" in
      ok)
        printf '%s\n' "$target" > "$state/serving"
        ;;
      transient-then-ok)
        # Two lookup failures and then the write lands, which is the shape the
        # staging run had: the app was there the whole time.
        if [ "$tries" -lt 3 ]; then
          transient_lookup_failure
        fi
        printf '%s\n' "$target" > "$state/serving"
        ;;
      transient-forever)
        transient_lookup_failure
        ;;
      throttled-then-ok)
        # Names itself transient, so it must be retried WITHOUT the extra read.
        if [ "$tries" -lt 3 ]; then
          printf 'ERROR: (429) TooManyRequests: too many requests\n' >&2
          exit 3
        fi
        printf '%s\n' "$target" > "$state/serving"
        ;;
      permanent)
        printf 'ERROR: (403) AuthorizationFailed: the client does not have authorization\n' >&2
        exit 3
        ;;
      errors-but-moved)
        # The weights are written and the call errors anyway. A script that
        # believes its own error message reports a deploy that never happened.
        printf '%s\n' "$target" > "$state/serving"
        transient_lookup_failure
        ;;
      missing-app)
        transient_lookup_failure
        ;;
    esac
    ;;
  "containerapp revision deactivate"*)
    printf 'deactivate %s\n' "$command" >> "$log"
    ;;
  "postgres flexible-server list"*)
    # No server makes the connection-budget check report itself unchecked. The
    # ordering under test is complete before that independent check.
    ;;
  "containerapp revision list"*)
    # Only the reap's listing is populated. The connection budget asks the same
    # command for a different projection and is a separate check.
    if [ "${AF_DEPLOY_TEST_REVISION_LIST:-empty}" = populated ] &&
       [ "$query" = "[?properties.active].[name, properties.createdTime]" ]; then
      printf 'old-revision\t2026-09-01T00:00:00Z\n'
    fi
    ;;
esac
FAKE_AZ

cat > "$TMP/bin/curl" <<'FAKE_CURL'
#!/usr/bin/env bash
set -euo pipefail

log="${AF_DEPLOY_TEST_LOG:?}"
url="${!#}"
printf 'health %s\n' "$url" >> "$log"

# The public origin answers for whichever revision is carrying traffic, which is
# what makes a rollback provable: `fail-then-recover` is the only shape in which
# rolling back is the right answer, a bad candidate in front of a previous
# revision that was and still is fine.
public_is_unhealthy() {
  case "${AF_DEPLOY_TEST_PUBLIC:-ok}" in
    fail) return 0 ;;
    fail-then-recover)
      if [ -f "${AF_DEPLOY_TEST_STATE:-}/serving" ] &&
         grep -Fqx old-revision "${AF_DEPLOY_TEST_STATE}/serving"; then
        return 1
      fi
      return 0
      ;;
  esac
  return 1
}

if { [[ "$url" == https://public.example/* ]] && public_is_unhealthy; } ||
   { [[ "$url" == https://candidate.example/* ]] &&
     [ "${AF_DEPLOY_TEST_CANDIDATE_HEALTH:-ok}" = fail ]; }; then
  case " $* " in
    *" -f"*) exit 22 ;;
    *" -w "*) printf '503' ;;
    *) printf '{"ready":false,"commit":"old"}' ;;
  esac
  exit 0
fi

printf '{"ready":true,"commit":"abcdef123456"}'
FAKE_CURL

cat > "$TMP/bin/sleep" <<'FAKE_SLEEP'
#!/usr/bin/env bash
exit 0
FAKE_SLEEP

chmod +x "$TMP/bin/az" "$TMP/bin/curl" "$TMP/bin/sleep"

run_deploy() {
  local case_name="$1"
  shift
  CASE_LOG="$TMP/$case_name.events"
  CASE_OUT="$TMP/$case_name.out"
  CASE_STATE="$TMP/$case_name.state"
  : > "$CASE_LOG"
  rm -rf "$CASE_STATE"
  mkdir -p "$CASE_STATE"
  if env PATH="$TMP/bin:$PATH" AF_DEPLOY_TEST_LOG="$CASE_LOG" \
      AF_DEPLOY_TEST_STATE="$CASE_STATE" "$@" \
      "$ROOT/deploy/cd/deploy.sh" group app bootstrap maintenance \
      ghcr.io/example/control-plane@sha256:tested abcdef123456 https://public.example \
      > "$CASE_OUT" 2>&1; then
    CASE_RC=0
  else
    CASE_RC=$?
  fi
}

# Every one of these passes the needle after `--`.
#
# Without it, a needle that begins with a hyphen is read by grep as an option,
# grep exits 2 having matched nothing, and `omits` then reports success because
# the pattern "was not found". That is a check that cannot say no, which is the
# one thing this repository refuses to ship: the assertion that traffic never
# touched `--revision old-revision` passed against a log that contained it.
line_of() {
  grep -nF -- "$1" "$2" | head -1 | cut -d: -f1
}

CHECKED=0
expect() {
  local name="$1"
  shift
  if [ -n "${AF_DEPLOY_TEST_ASSERT:-}" ] && [ "$AF_DEPLOY_TEST_ASSERT" != "$name" ]; then
    return 0
  fi
  CHECKED=$((CHECKED + 1))
  if "$@"; then
    printf 'ok  %s\n' "$name"
  else
    printf 'FAIL  %s\n' "$name" >&2
    if [ -n "${CASE_LOG:-}" ] && [ -f "$CASE_LOG" ]; then
      printf 'events:\n' >&2
      sed 's/^/  /' "$CASE_LOG" >&2
    fi
    if [ -n "${CASE_OUT:-}" ] && [ -f "$CASE_OUT" ]; then
      printf 'output:\n' >&2
      sed 's/^/  /' "$CASE_OUT" >&2
    fi
    exit 1
  fi
}

is_zero() { [ "$1" -eq 0 ]; }
is_nonzero() { [ "$1" -ne 0 ]; }
contains() { grep -Fq -- "$1" "$2"; }
has_line() { grep -Fxq -- "$1" "$2"; }
omits() { ! grep -Fq -- "$1" "$2"; }
later_than() { [ "$(line_of "$1" "$3")" -gt "$(line_of "$2" "$3")" ]; }
count_is() { [ "$(grep -Fc -- "$1" "$3")" -eq "$2" ]; }

expect "staging passes its maintenance job to the deploy" count_is \
  '"${MAINTENANCE_JOB}"' 1 "$ROOT/.github/workflows/cd.yml"
expect "production passes its maintenance job to the deploy" count_is \
  '"${PRODUCTION_MAINTENANCE_JOB}"' 1 "$ROOT/.github/workflows/cd.yml"

run_deploy healthy
expect "a healthy release succeeds" is_zero "$CASE_RC"
expect "bootstrap receives the tested digest" has_line \
  "job-update bootstrap ghcr.io/example/control-plane@sha256:tested" "$CASE_LOG"
expect "maintenance receives the tested digest" has_line \
  "job-update maintenance ghcr.io/example/control-plane@sha256:tested" "$CASE_LOG"
expect "maintenance moves after public health" later_than \
  "job-update maintenance" "health https://public.example/readyz" "$CASE_LOG"
expect "maintenance moves after candidate health" later_than \
  "job-update maintenance" "health https://candidate.example/readyz" "$CASE_LOG"

run_deploy address-missing AF_DEPLOY_TEST_ADDRESS=missing
expect "a missing candidate address refuses the deploy" is_nonzero "$CASE_RC"
expect "a missing candidate address leaves maintenance alone" omits \
  "job-update maintenance" "$CASE_LOG"
expect "a missing candidate address is never promoted" omits \
  "containerapp ingress traffic set" "$CASE_LOG"

run_deploy candidate-unhealthy AF_DEPLOY_TEST_CANDIDATE_HEALTH=fail
expect "an unhealthy candidate refuses the deploy" is_nonzero "$CASE_RC"
expect "an unhealthy candidate leaves maintenance alone" omits \
  "job-update maintenance" "$CASE_LOG"
expect "an unhealthy candidate is never promoted" omits \
  "containerapp ingress traffic set" "$CASE_LOG"

run_deploy revision-failed AF_DEPLOY_TEST_REVISION=fail
expect "a failed candidate refuses the deploy" is_nonzero "$CASE_RC"
expect "a failed candidate leaves maintenance alone" omits "job-update maintenance" "$CASE_LOG"
expect "a failed candidate is never promoted" omits "containerapp ingress traffic set" "$CASE_LOG"

run_deploy public-failed AF_DEPLOY_TEST_PUBLIC=fail
expect "a failed public health gate refuses the deploy" is_nonzero "$CASE_RC"
expect "a failed public health gate leaves maintenance alone" omits \
  "job-update maintenance" "$CASE_LOG"
expect "a failed public health gate restores the old revision" contains \
  "revision-weight old-revision=100" "$CASE_LOG"

run_deploy maintenance-failed AF_DEPLOY_TEST_MAINTENANCE=fail
expect "a refused maintenance update fails the run" is_nonzero "$CASE_RC"
expect "a refused maintenance update does not roll back a healthy app" omits \
  "revision-weight old-revision=100" "$CASE_LOG"
expect "a refused maintenance update names the live state" contains \
  "The application remains on the healthy revision" "$CASE_OUT"

run_deploy maintenance-stale AF_DEPLOY_TEST_MAINTENANCE_READBACK=stale
expect "a stale maintenance read back fails the run" is_nonzero "$CASE_RC"
expect "a stale maintenance read back names both images" contains \
  "maintenance image read back as ghcr.io/example/control-plane@sha256:old; expected ghcr.io/example/control-plane@sha256:tested" \
  "$CASE_OUT"

# ---------------------------------------------------------------------------
# The traffic shift, which was the one Azure call in this script that could not
# be tried again.
#
# Both halves are proved, because a retry that cannot say no is worse than no
# retry: it turns a wrong app name into a five minute wait and then reports the
# same failure anyway.
# ---------------------------------------------------------------------------

run_deploy traffic-transient AF_DEPLOY_TEST_TRAFFIC=transient-then-ok
expect "a transient lookup failure at the shift does not fail the deploy" is_zero "$CASE_RC"
expect "a transient lookup failure at the shift is tried again" count_is \
  "traffic containerapp ingress traffic set" 3 "$CASE_LOG"
expect "a retried shift says which attempt carried traffic" contains \
  "traffic reached app--" "$CASE_OUT"
expect "a retried shift still moves the maintenance job" has_line \
  "job-update maintenance ghcr.io/example/control-plane@sha256:tested" "$CASE_LOG"

run_deploy traffic-throttled AF_DEPLOY_TEST_TRAFFIC=throttled-then-ok
expect "a throttled shift is tried again" is_zero "$CASE_RC"
expect "a throttled shift is retried three times" count_is \
  "traffic containerapp ingress traffic set" 3 "$CASE_LOG"
expect "an error that names itself transient costs no extra read" omits \
  "az containerapp show -n app -g group --query name" "$CASE_LOG"

run_deploy traffic-app-missing AF_DEPLOY_TEST_TRAFFIC=missing-app AF_DEPLOY_TEST_APP_PRESENT=no
expect "a genuinely missing app fails the deploy" is_nonzero "$CASE_RC"
expect "a genuinely missing app is refused on the first attempt" count_is \
  "traffic containerapp ingress traffic set" 1 "$CASE_LOG"
expect "a genuinely missing app is decided by asking, not by reading the message" contains \
  "az containerapp show -n app -g group --query name" "$CASE_LOG"
expect "a genuinely missing app says waiting will not help" contains \
  "WAITING WILL NOT HELP" "$CASE_OUT"
expect "a genuinely missing app is never reported as a completed deploy" omits \
  "DEPLOYED:" "$CASE_OUT"

run_deploy traffic-permanent AF_DEPLOY_TEST_TRAFFIC=permanent
expect "a refused shift fails the deploy" is_nonzero "$CASE_RC"
expect "a refused shift is not tried again" count_is \
  "traffic containerapp ingress traffic set" 1 "$CASE_LOG"
expect "a refused shift does not go looking for the app" omits \
  "az containerapp show -n app -g group --query name" "$CASE_LOG"

run_deploy traffic-exhausted AF_DEPLOY_TEST_TRAFFIC=transient-forever
expect "a shift that never lands fails the deploy" is_nonzero "$CASE_RC"
expect "a shift that never lands is tried the full budget" count_is \
  "traffic containerapp ingress traffic set" 6 "$CASE_LOG"
expect "a shift that never lands says traffic never moved" contains \
  "TRAFFIC NEVER MOVED" "$CASE_OUT"
expect "a shift that never lands names what is still serving" contains \
  "old-revision is still serving" "$CASE_OUT"
expect "a shift that never lands is never reported as a completed deploy" omits \
  "DEPLOYED:" "$CASE_OUT"
expect "a shift that never lands says the commit is not live" contains \
  "::error title=Traffic never moved::" "$CASE_OUT"
expect "a shift that never lands leaves maintenance alone" omits \
  "job-update maintenance" "$CASE_LOG"

run_deploy traffic-errors-but-moved AF_DEPLOY_TEST_TRAFFIC=errors-but-moved
expect "a shift that errored after writing the weights is read back, not believed" contains \
  "is the revision carrying traffic" "$CASE_OUT"
expect "traffic that did move is still put through the public gate" contains \
  "health https://public.example/readyz" "$CASE_LOG"
expect "traffic that did move and passed the gate is a successful deploy" is_zero "$CASE_RC"

run_deploy rollback-transient AF_DEPLOY_TEST_PUBLIC=fail-then-recover \
  AF_DEPLOY_TEST_ROLLBACK_TRAFFIC=transient-then-ok
expect "a transient failure during a rollback still fails the deploy" is_nonzero "$CASE_RC"
expect "a transient failure during a rollback is tried again" count_is \
  "traffic containerapp ingress traffic set -n app -g group --revision-weight old-revision=100" \
  3 "$CASE_LOG"
expect "a retried rollback reports that it restored the previous revision" contains \
  "ROLLED BACK. https://public.example is healthy again on old-revision" "$CASE_OUT"
expect "a retried rollback does not report that it could not move traffic" omits \
  "THE ROLLBACK COULD NOT MOVE TRAFFIC" "$CASE_OUT"

run_deploy rollback-refused AF_DEPLOY_TEST_PUBLIC=fail \
  AF_DEPLOY_TEST_ROLLBACK_TRAFFIC=missing-app AF_DEPLOY_TEST_APP_PRESENT=no
expect "a rollback that cannot move traffic fails the deploy" is_nonzero "$CASE_RC"
expect "a rollback that cannot move traffic is refused on the first attempt" count_is \
  "traffic containerapp ingress traffic set -n app -g group --revision-weight old-revision=100" \
  1 "$CASE_LOG"
expect "a rollback that cannot move traffic says the unhealthy revision is still serving" contains \
  "THE ROLLBACK COULD NOT MOVE TRAFFIC" "$CASE_OUT"
expect "a rollback that cannot move traffic raises it as an error" contains \
  "::error title=Rollback did not move traffic::" "$CASE_OUT"

# ---------------------------------------------------------------------------
# The reap, and the rollback target it must not eat.
#
# The read that captures what is serving tolerates a failing az, because an ARM
# blip on a read must not kill a deploy before it starts. The cost of that lands
# on the reap: with no name to keep, its comparison matches nothing and it would
# deactivate the exact revision step 6 shifts back onto.
# ---------------------------------------------------------------------------

run_deploy reap-with-a-target AF_DEPLOY_TEST_REVISION_LIST=populated
expect "a deploy that knows what it superseded still reaps" contains \
  "reaping revisions superseded by" "$CASE_OUT"
expect "the reap spares the revision it would roll back onto" omits \
  "--revision old-revision -o none" "$CASE_LOG"

run_deploy reap-without-a-target AF_DEPLOY_TEST_REVISION_LIST=populated \
  AF_DEPLOY_TEST_SERVING=unknown
expect "a deploy with no rollback target says so before it starts" contains \
  "currently serving: nothing this script can name" "$CASE_OUT"
expect "a deploy that does not know what it superseded reaps nothing" contains \
  "not reaping anything" "$CASE_OUT"
expect "a deploy that does not know what it superseded leaves the rollback target alone" omits \
  "--revision old-revision -o none" "$CASE_LOG"

# ---------------------------------------------------------------------------
# The number this lane owes, measured from the script rather than asserted about
# it: every Azure call that MUTATES state either retries or says why it does not.
#
# The defect was never that ten operations lacked a loop. It was that the
# absence was invisible, so nobody could tell a considered "this one must not be
# repeated" from "nobody thought about it". This makes the difference a thing
# the build can check.
# ---------------------------------------------------------------------------

# Every az call in the script BODY, reduced to the words before its first flag.
# Comments are stripped first, so the paragraph that names an operation cannot
# be mistaken for a call to it.
mutating_call_sites() {
  sed 's/#.*//' "$ROOT/deploy/cd/deploy.sh" \
    | grep -o 'az [a-z][a-z-]*\( [a-z][a-z-]*\)*' \
    | sed 's/^az //' \
    | grep -E '(^| )(update|start|set|create|delete|deactivate|restart|stop)$'
}

# The header, which is where a stance is written.
stance_text() {
  sed -n '1,/^set -euo pipefail$/p' "$ROOT/deploy/cd/deploy.sh"
}

every_mutation_has_a_stance() {
  local operation tail undeclared=""
  while read -r operation; do
    [ -z "$operation" ] && continue
    # The last two words, which is how the header names an operation: `job
    # start`, `traffic set`, `revision deactivate`, `containerapp update`.
    tail="$(printf '%s' "$operation" | awk '{ print $(NF - 1), $NF }')"
    if ! stance_text | grep -Fq "$tail"; then
      undeclared="$undeclared $tail"
    fi
  done < <(mutating_call_sites | sort -u)
  if [ -n "$undeclared" ]; then
    printf 'no declared retry stance for:%s\n' "$undeclared" >&2
    return 1
  fi
  return 0
}

only_one_traffic_shift_in_the_source() {
  [ "$(mutating_call_sites | grep -Fc 'ingress traffic set')" -eq 1 ]
}

expect "every Azure call that mutates state has a declared retry stance" \
  every_mutation_has_a_stance
expect "the traffic shift exists in exactly one place, so it cannot be added unretried" \
  only_one_traffic_shift_in_the_source

MUTATIONS="$(mutating_call_sites | wc -l | tr -d ' ')"
OPERATIONS="$(mutating_call_sites | sort -u | wc -l | tr -d ' ')"
RETRIED="$(mutating_call_sites | grep -Fc 'ingress traffic set')"
printf 'deploy retries: %s of %s state-changing az call sites retry (%s distinct operations, all %s with a declared stance)\n' \
  "$RETRIED" "$MUTATIONS" "$OPERATIONS" "$OPERATIONS"

if [ "$CHECKED" -eq 0 ]; then
  printf 'FAIL  no deployment assertions matched the selection\n' >&2
  exit 1
fi
printf 'deploy ordering: %s assertions passed\n' "$CHECKED"
