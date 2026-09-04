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
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
  case "${args[$i]}" in
    -n) name="${args[$((i + 1))]}" ;;
    --image) image="${args[$((i + 1))]}" ;;
    --query) query="${args[$((i + 1))]}" ;;
  esac
done

case "$command" in
  "containerapp ingress traffic show"*)
    printf 'old-revision\n'
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
    ;;
  "containerapp revision deactivate"*)
    printf 'deactivate %s\n' "$command" >> "$log"
    ;;
  "postgres flexible-server list"*)
    # No server makes the connection-budget check report itself unchecked. The
    # ordering under test is complete before that independent check.
    ;;
  "containerapp revision list"*)
    ;;
esac
FAKE_AZ

cat > "$TMP/bin/curl" <<'FAKE_CURL'
#!/usr/bin/env bash
set -euo pipefail

log="${AF_DEPLOY_TEST_LOG:?}"
url="${!#}"
printf 'health %s\n' "$url" >> "$log"

if { [[ "$url" == https://public.example/* ]] &&
     [ "${AF_DEPLOY_TEST_PUBLIC:-ok}" = fail ]; } ||
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
  : > "$CASE_LOG"
  if env PATH="$TMP/bin:$PATH" AF_DEPLOY_TEST_LOG="$CASE_LOG" "$@" \
      "$ROOT/deploy/cd/deploy.sh" group app bootstrap maintenance \
      ghcr.io/example/control-plane@sha256:tested abcdef123456 https://public.example \
      > "$CASE_OUT" 2>&1; then
    CASE_RC=0
  else
    CASE_RC=$?
  fi
}

line_of() {
  grep -nF "$1" "$2" | head -1 | cut -d: -f1
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
contains() { grep -Fq "$1" "$2"; }
has_line() { grep -Fxq "$1" "$2"; }
omits() { ! grep -Fq "$1" "$2"; }
later_than() { [ "$(line_of "$1" "$3")" -gt "$(line_of "$2" "$3")" ]; }
count_is() { [ "$(grep -Fc "$1" "$3")" -eq "$2" ]; }

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

if [ "$CHECKED" -eq 0 ]; then
  printf 'FAIL  no deployment assertions matched the selection\n' >&2
  exit 1
fi
printf 'deploy ordering: %s assertions passed\n' "$CHECKED"
