#!/usr/bin/env bash

# The configuration apply does what its header says and nothing more.
#
# This runs the real apply-config.sh against a fake terraform and a fake az,
# with the real guard built from tools/configguard and the real plan fixtures
# it ships with. It checks ORDER and ABSENCE rather than searching source
# text: init before plan before guard before apply, an apply only after the
# guard said yes, never a traffic shift, and nothing at all when an input is
# missing. The guard's own decisions are tested in tools/configguard; this is
# the script around it.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

FIXTURES="$ROOT/tools/configguard/testdata"

# A refusal fixture, derived from the real accepted one by changing the image
# and nothing else, so the only difference between "applied" and "refused" in
# the cases below is the one attribute deploy.sh owns.
jq '(.resource_changes[] | select(.address == "module.control_plane.azurerm_container_app.this")
      | .change.after.template[0].container[0].image)
    |= "ghcr.io/antifailure/control-plane@sha256:1111111111111111111111111111111111111111111111111111111111111111"' \
  "$FIXTURES/env-and-secrets-added.json" > "$TMP/image-changed.json"

# States for the production receiver read. One action group with one email,
# and one with none.
cat > "$TMP/state-with-receiver.json" <<'STATE'
{"resources":[{"type":"azurerm_monitor_action_group","instances":[{"attributes":{
  "email_receiver":[{"name":"ops","email_address":"ops@example.invalid"}],
  "sms_receiver":[]}}]}]}
STATE
cat > "$TMP/state-without-receiver.json" <<'STATE'
{"resources":[{"type":"azurerm_monitor_action_group","instances":[{"attributes":{
  "email_receiver":[],"sms_receiver":[]}}]}]}
STATE

cat > "$TMP/bin/terraform" <<'FAKE_TF'
#!/usr/bin/env bash
set -euo pipefail
log="${AF_APPLY_TEST_LOG:?}"
args=("$@")
# The first argument is -chdir=<stack>; the subcommand follows it.
sub="${args[1]:-}"
printf 'terraform %s\n' "$sub" >> "$log"
case "$sub" in
  init)
    ;;
  "state")
    # `state pull`
    if [ -n "${AF_APPLY_TEST_STATE:-}" ]; then cat "$AF_APPLY_TEST_STATE"; fi
    ;;
  plan)
    printf 'plan-env alert_emails=%s sms=%s\n' "${TF_VAR_alert_emails:-unset}" "${TF_VAR_alert_sms_number:-unset}" >> "$log"
    out=""
    for ((i = 0; i < ${#args[@]}; i++)); do
      case "${args[$i]}" in
        -out=*) out="${args[$i]#-out=}" ;;
        -target=*) printf 'plan-target %s\n' "${args[$i]#-target=}" >> "$log" ;;
        -var-file=*) printf 'plan-varfile %s\n' "${args[$i]#-var-file=}" >> "$log" ;;
      esac
    done
    printf 'fake plan' > "$out"
    echo "Plan: 0 to add, 1 to change, 0 to destroy."
    ;;
  show)
    cat "${AF_APPLY_TEST_PLAN:?}"
    ;;
  apply)
    printf 'apply-plan %s\n' "${args[${#args[@]}-1]}" >> "$log"
    if [ "${AF_APPLY_TEST_APPLY:-ok}" = fail ]; then
      echo "Error: Saved plan is stale" >&2
      exit 1
    fi
    echo "Apply complete! Resources: 0 added, 1 changed, 0 destroyed."
    ;;
  *)
    echo "fake terraform: unexpected subcommand $sub" >&2
    exit 99
    ;;
esac
FAKE_TF

cat > "$TMP/bin/az" <<'FAKE_AZ'
#!/usr/bin/env bash
set -euo pipefail
log="${AF_APPLY_TEST_LOG:?}"
printf 'az %s\n' "$*" >> "$log"
case "$*" in
  "account show"*) printf '00000000-0000-0000-0000-000000000000\n' ;;
esac
FAKE_AZ

chmod +x "$TMP/bin/terraform" "$TMP/bin/az"

failures=0
fail() { echo "FAIL: $*"; failures=$((failures + 1)); }
pass() { echo "ok:   $*"; }

# run <name> <environment> <mode> <expected-exit> [env assignments...]
run_case() {
  local name="$1" environment="$2" mode="$3" want="$4"
  shift 4
  LOG="$TMP/$name.log"
  OUT="$TMP/$name.out"
  : > "$LOG"
  set +e
  # The operator's own TF_VAR_ and ARM_ settings must not leak into a case,
  # or a case passes because of something on this machine.
  env -u TF_VAR_alert_emails -u TF_VAR_alert_sms_number -u TF_VAR_alert_sms_country_code \
    -u TF_VAR_subscription_id -u TF_VAR_github_client_id -u TF_VAR_github_client_secret -u ARM_USE_OIDC \
    PATH="$TMP/bin:$PATH" \
    AF_APPLY_TEST_LOG="$LOG" \
    AF_TFSTATE_RG="af-tfstate-test" AF_TFSTATE_ACCOUNT="aftfstatetest" \
    "$@" \
    "$ROOT/deploy/cd/apply-config.sh" "$environment" "$mode" > "$OUT" 2>&1
  local got=$?
  set -e
  if [ "$got" -ne "$want" ]; then
    fail "$name: exit $got, wanted $want"
    sed 's/^/    /' "$OUT" | tail -30
    return 1
  fi
  return 0
}

line_of() { grep -n -- "$1" "$LOG" | head -1 | cut -d: -f1; }
never()   { if grep -q -- "$1" "$LOG"; then fail "$2: '$1' happened and must never"; else pass "$2: no '$1'"; fi; }
says()    { if grep -q -- "$1" "$OUT"; then pass "$2: says '$1'"; else fail "$2: never says '$1'"; sed 's/^/    /' "$OUT" | tail -20; fi; }

# ---------------------------------------------------------------------------
# 1. The steady state: nothing to do, and nothing done.
# ---------------------------------------------------------------------------
if run_case noop staging apply 0 AF_APPLY_TEST_PLAN="$FIXTURES/no-changes.json"; then
  never "apply-plan" "no-op"
  never "ingress traffic set" "no-op"
  says "NO CONFIGURATION CHANGE" "no-op"
  [ "$(line_of 'terraform init')" -lt "$(line_of 'terraform plan')" ] && pass "no-op: init before plan" || fail "no-op: plan before init"
  grep -q 'plan-target module.control_plane.azurerm_container_app.this' "$LOG" && pass "no-op: plan is targeted at the app" || fail "no-op: plan is not targeted at the app"
  grep -q 'plan-varfile staging.tfvars' "$LOG" && pass "no-op: plan reads staging.tfvars" || fail "no-op: plan does not read staging.tfvars"
fi

# ---------------------------------------------------------------------------
# 2. A configuration change: applied once, after the guard, no traffic shift.
# ---------------------------------------------------------------------------
if run_case update staging apply 0 AF_APPLY_TEST_PLAN="$FIXTURES/env-and-secrets-added.json"; then
  [ "$(grep -c 'apply-plan' "$LOG")" -eq 1 ] && pass "update: exactly one apply" || fail "update: $(grep -c 'apply-plan' "$LOG") applies"
  [ "$(line_of 'terraform show')" -lt "$(line_of 'apply-plan')" ] && pass "update: the plan was read before it was applied" || fail "update: apply happened before the plan was read"
  never "ingress traffic set" "update"
  says "ZERO percent" "update"
  says "deploy.sh runs next" "update"
fi

# ---------------------------------------------------------------------------
# 3. plan-only: everything but the apply.
# ---------------------------------------------------------------------------
if run_case planonly staging plan-only 0 AF_APPLY_TEST_PLAN="$FIXTURES/env-and-secrets-added.json"; then
  never "apply-plan" "plan-only"
  says "PLAN ONLY" "plan-only"
  says "one in-place update" "plan-only"
fi

# ---------------------------------------------------------------------------
# 4. A refused plan: non-zero, the reason, the summary, and no apply.
# ---------------------------------------------------------------------------
if run_case refused staging apply 1 AF_APPLY_TEST_PLAN="$TMP/image-changed.json"; then
  never "apply-plan" "refused"
  says "REFUSED" "refused"
  says "serving image" "refused"
  says "::error title=Configuration apply refused" "refused"
fi

# plan-only reports a refusal the same way, so a dry run cannot read as green.
if run_case refused-planonly staging plan-only 1 AF_APPLY_TEST_PLAN="$TMP/image-changed.json"; then
  never "apply-plan" "refused plan-only"
  says "serving image" "refused plan-only"
fi

# ---------------------------------------------------------------------------
# 5. Production reads its alert receivers out of state, so the action group
#    is planned exactly as applied. With none recorded it refuses before the
#    plan rather than planning a change to the action group.
# ---------------------------------------------------------------------------
if run_case prod production apply 0 AF_APPLY_TEST_PLAN="$FIXTURES/no-changes.json" AF_APPLY_TEST_STATE="$TMP/state-with-receiver.json"; then
  grep -q 'terraform state' "$LOG" && pass "production: state was read for the receivers" || fail "production: state was never read"
  grep -q 'plan-env alert_emails=\["ops@example.invalid"\]' "$LOG" && pass "production: the plan saw the recorded receiver" || fail "production: the plan did not see the recorded receiver: $(grep plan-env "$LOG")"
  grep -q 'plan-varfile production.tfvars' "$LOG" && pass "production: plan reads production.tfvars" || fail "production: plan does not read production.tfvars"
  grep -q 'ops@example.invalid' "$OUT" && fail "production: the receiver address was printed" || pass "production: the receiver address is not printed"
fi

if run_case prod-noreceiver production apply 1 AF_APPLY_TEST_PLAN="$FIXTURES/no-changes.json" AF_APPLY_TEST_STATE="$TMP/state-without-receiver.json"; then
  never "terraform plan" "production without receiver"
  never "apply-plan" "production without receiver"
  says "no receiver" "production without receiver"
fi

# Staging does not enable alerting and must not read state for receivers.
if run_case staging-noreceivers staging apply 0 AF_APPLY_TEST_PLAN="$FIXTURES/no-changes.json"; then
  never "terraform state" "staging"
  grep -q 'plan-env alert_emails=unset' "$LOG" && pass "staging: no receiver variable is passed" || fail "staging: a receiver variable was passed"
fi

# ---------------------------------------------------------------------------
# 6. Missing inputs stop everything before terraform runs.
# ---------------------------------------------------------------------------
LOG="$TMP/missing.log"; OUT="$TMP/missing.out"; : > "$LOG"
set +e
env -u AF_TFSTATE_ACCOUNT PATH="$TMP/bin:$PATH" AF_APPLY_TEST_LOG="$LOG" AF_TFSTATE_RG="af-tfstate-test" \
  "$ROOT/deploy/cd/apply-config.sh" staging apply > "$OUT" 2>&1
got=$?
set -e
if [ "$got" -eq 0 ]; then fail "missing account: exit 0"; else pass "missing account: exit $got"; fi
never "terraform" "missing account"
says "AF_TFSTATE_ACCOUNT" "missing account"

set +e
env PATH="$TMP/bin:$PATH" AF_APPLY_TEST_LOG="$LOG" AF_TFSTATE_RG=x AF_TFSTATE_ACCOUNT=y \
  "$ROOT/deploy/cd/apply-config.sh" elsewhere apply > "$OUT" 2>&1
got=$?
set -e
if [ "$got" -eq 2 ]; then pass "unknown environment: refused with 2"; else fail "unknown environment: exit $got"; fi
never "terraform" "unknown environment"

# ---------------------------------------------------------------------------
# 7. A failed apply is a failed run, said plainly.
# ---------------------------------------------------------------------------
if run_case applyfail staging apply 1 AF_APPLY_TEST_PLAN="$FIXTURES/env-and-secrets-added.json" AF_APPLY_TEST_APPLY=fail; then
  says "APPLY FAILED" "apply failure"
  never "ingress traffic set" "apply failure"
fi

echo
if [ "$failures" -eq 0 ]; then
  echo "apply-config.sh: every case held."
  exit 0
fi
echo "apply-config.sh: $failures check(s) failed."
exit 1
