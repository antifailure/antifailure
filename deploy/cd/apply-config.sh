#!/usr/bin/env bash
# The control plane's configuration reaches the container app from its tfvars,
# without a person at a terminal.
#
# THE FAILURE. On 2026-09-05 three configuration changes had to reach
# production: AF_POSTHOG_REGION and AF_POSTHOG_PROJECT_KEY, then the three
# Stripe variables with two Key Vault references. Each was a line in
# production.tfvars, merged, released, and inert: deploy.sh runs
# `az containerapp update --image`, which carries whatever template the app
# already has and sets no variable of its own, and cd.yml ran no Terraform at
# all. Each change reached the container only because somebody ran a targeted
# `terraform apply` by hand behind a one-off guard, then shifted traffic by
# hand. In between, the marketing site went live pointing at /ph and the proxy
# answered 404 for an hour, because the variable that turns it on was in a
# file and not in the app.
#
# WHAT THIS DOES, in order, for one environment:
#
#   1. Initialises the stack against that environment's state blob, in a
#      private TF_DATA_DIR, so it never repoints the operator's own checkout.
#   2. Plans the stack TARGETED at the container app alone, with the
#      environment's tfvars, and writes the plan out.
#   3. Hands the plan's JSON to tools/configguard, which says yes to exactly
#      two shapes: no changes, or one in-place update of the app whose only
#      differences are the container's env list and the app's secret
#      references. Anything else is refused with the reason and the plan
#      summary, and nothing is applied.
#   4. Applies that plan, and only that plan.
#
# WHAT IT DOES NOT DO, AND WHY THE ORDER IN cd.yml MATTERS.
#
# It never shifts traffic. The app runs in Multiple revision mode with the
# traffic weights in ignore_changes, so an apply that changes the template
# creates a new revision at ZERO percent and reports success while the old
# revision keeps serving. The night's hand applies each needed an
# `az containerapp ingress traffic set` afterwards. This script does not do
# that, on purpose: deploy.sh runs immediately after it in the same job, and
# deploy.sh creates its own revision with `az containerapp update --image`,
# which reads the app's CURRENT template, the one this script just wrote, env
# and secret references included, and changes only the image. That revision
# is the one deploy.sh probes at zero traffic and then promotes. So the
# configuration takes effect through the ordinary release path, behind the
# migration and both health gates, and the zero-percent revision this script
# leaves behind is superseded a minute later and reaped by deploy.sh.
#
# Proven on production on 2026-09-06 rather than reasoned: the hand apply made
# afcpprod-app--0000004 with 20 variables including the two PostHog ones, and
# the tag deploy's afcpprod-app--c1381fae1-023945 made by deploy.sh half an
# hour later carries the same 20, with only the image different.
#
# WHAT IT REFUSES AND LEAVES TO A PERSON. Anything in the tfvars that is not
# the container app's configuration: a database SKU, a role assignment, the
# alerting module, a Key Vault change. The plan is targeted, so those never
# enter it, and if one of them is a dependency that does, configguard refuses.
# The runbook's hand apply is still how the rest of the stack moves.
#
# THE INPUTS THAT ARE NOT IN THE TFVARS, and where each comes from:
#
#   subscription_id        TF_VAR_subscription_id, or the signed in account
#   github_client_id       placeholders. The seeded vault secrets that carry
#   github_client_secret   these have ignore_changes = [value], so the value
#                          passed here never reaches Azure and the app reads
#                          the secret by reference. If a plan ever shows one
#                          of those secrets changing, configguard refuses it
#                          as a change to a second resource.
#   alert_emails           read from the state's action group, so the
#   alert_sms_*            receiver list in the plan equals what is applied
#                          and the action group stays a no-op. Refused if
#                          alerting is on and the state holds no receiver.
#   the state backend      AF_TFSTATE_RG and AF_TFSTATE_ACCOUNT, names only,
#                          the same two values infra.yml's plan job reads
#                          from AZURE_TFSTATE_RG and AZURE_TFSTATE_ACCOUNT
#
# Anything else Terraform needs, ARM_* for the credential and TF_VAR_* for a
# pinned input like ci_principal_id, passes through the environment untouched.
#
#   apply-config.sh <staging|production> [plan-only]
#
# plan-only runs every step but the apply and exits 0 when the apply would
# have run or was not needed, 1 when it would have been refused.

set -euo pipefail

ENVIRONMENT="${1:?environment: staging or production}"
MODE="${2:-apply}"

case "$ENVIRONMENT" in
  staging)    STATE_KEY="control-plane.tfstate" ;;
  production) STATE_KEY="control-plane-production.tfstate" ;;
  *)
    echo "apply-config: environment must be staging or production, not '$ENVIRONMENT'" >&2
    exit 2
    ;;
esac
case "$MODE" in
  apply|plan-only) ;;
  *)
    echo "apply-config: mode must be apply or plan-only, not '$MODE'" >&2
    exit 2
    ;;
esac

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STACK="$ROOT/infra/terraform/stacks/control-plane"
VAR_FILE="$ENVIRONMENT.tfvars"
TARGET="module.control_plane.azurerm_container_app.this"

say() { printf '\n=== %s\n' "$*"; }

# Names only, and both. An account with no group fails inside `terraform init`
# with a message about the backend rather than about the missing input, which
# sends the reader to the wrong file. infra.yml makes the same check.
: "${AF_TFSTATE_RG:?the resource group of the state storage account (AZURE_TFSTATE_RG in the repository)}"
: "${AF_TFSTATE_ACCOUNT:?the state storage account name (AZURE_TFSTATE_ACCOUNT in the repository)}"
AF_TFSTATE_CONTAINER="${AF_TFSTATE_CONTAINER:-tfstate}"

for tool in terraform jq go; do
  command -v "$tool" >/dev/null 2>&1 || { echo "apply-config: $tool is not on PATH" >&2; exit 2; }
done
[ -f "$STACK/$VAR_FILE" ] || { echo "apply-config: $STACK/$VAR_FILE does not exist" >&2; exit 2; }

# Everything this run writes lives here and is removed on exit: the provider
# cache, the backend pointer, the plan and its JSON. The plan JSON carries the
# values of sensitive input variables, which is why it is 0700 and why nothing
# below prints it.
WORK="$(mktemp -d)"
chmod 700 "$WORK"
trap 'rm -rf "$WORK"' EXIT
export TF_DATA_DIR="$WORK/tfdata"
export TF_IN_AUTOMATION=1

# ---------------------------------------------------------------------------
# The inputs that are not in the tfvars.
# ---------------------------------------------------------------------------
if [ -z "${TF_VAR_subscription_id:-}" ]; then
  TF_VAR_subscription_id="$(az account show --query id -o tsv 2>/dev/null || true)"
  [ -n "$TF_VAR_subscription_id" ] || { echo "apply-config: no TF_VAR_subscription_id and no signed in account to read one from" >&2; exit 2; }
  export TF_VAR_subscription_id
fi

# Placeholders, deliberately. See the header: the seeded secrets ignore their
# value, the app reads them by reference, and configguard refuses a plan in
# which either secret changes.
export TF_VAR_github_client_id="${TF_VAR_github_client_id:-apply-config-does-not-set-this}"
export TF_VAR_github_client_secret="${TF_VAR_github_client_secret:-apply-config-does-not-set-this}"

# ---------------------------------------------------------------------------
# 1. Init against this environment's state, and nobody else's.
# ---------------------------------------------------------------------------
say "init: $STATE_KEY in $AF_TFSTATE_ACCOUNT/$AF_TFSTATE_CONTAINER ($AF_TFSTATE_RG)"
init_args=(
  -input=false -reconfigure -no-color
  -backend-config="resource_group_name=$AF_TFSTATE_RG"
  -backend-config="storage_account_name=$AF_TFSTATE_ACCOUNT"
  -backend-config="container_name=$AF_TFSTATE_CONTAINER"
  -backend-config="key=$STATE_KEY"
  -backend-config="use_azuread_auth=true"
)
# use_oidc only where a federated token exists. Locally the backend uses the
# signed in Azure CLI, and asking it for OIDC there fails with a message about
# a missing token rather than about this flag.
if [ "${ARM_USE_OIDC:-}" = "true" ]; then
  init_args+=(-backend-config="use_oidc=true")
fi
terraform -chdir="$STACK" init "${init_args[@]}" >"$WORK/init.log" 2>&1 || {
  cat "$WORK/init.log"
  say "INIT FAILED. Nothing was planned or applied."
  exit 1
}

# ---------------------------------------------------------------------------
# The alert receivers, read from what is applied so the plan proposes nothing
# for the action group. production.tfvars turns alerting on and the stack
# refuses alerting with no receiver at variable validation, which runs even
# for a targeted plan. The addresses are not in the repository, and a
# placeholder would plan an in-place change to the action group that no
# apply should ever make. The state's own record is the one value that keeps
# the plan equal to the world.
#
# `terraform state pull` is piped straight into jq and never written to disk:
# the state carries every secret the stack owns, and this needs two lists of
# receivers out of it.
# ---------------------------------------------------------------------------
if grep -Eq '^\s*alerting_enabled\s*=\s*true' "$STACK/$VAR_FILE" && [ -z "${TF_VAR_alert_emails:-}" ]; then
  receivers="$(terraform -chdir="$STACK" state pull 2>/dev/null | jq -c '
    [.resources[]? | select(.type == "azurerm_monitor_action_group") | .instances[]?.attributes]
    | first // {}
    | {
        emails: [(.email_receiver // [])[] | .email_address],
        sms_code: ((.sms_receiver // [])[0].country_code // ""),
        sms_number: ((.sms_receiver // [])[0].phone_number // "")
      }' 2>/dev/null || true)"
  if [ -z "$receivers" ]; then
    say "REFUSING: $VAR_FILE enables alerting and the state could not be read for its receivers."
    echo "Without the applied receiver list the plan would propose a change to the"
    echo "action group that no configuration apply may make. Nothing was applied."
    exit 1
  fi
  emails_json="$(jq -c '.emails' <<<"$receivers")"
  sms_code="$(jq -r '.sms_code' <<<"$receivers")"
  sms_number="$(jq -r '.sms_number' <<<"$receivers")"
  if [ "$emails_json" = "[]" ] && [ -z "$sms_number" ]; then
    say "REFUSING: $VAR_FILE enables alerting and the action group in state has no receiver."
    echo "The stack refuses that at plan time, on purpose, and this script has no"
    echo "address to offer it. Apply the alerting change by hand with TF_VAR_alert_emails"
    echo "set, after which this reads the receivers back from state. Nothing was applied."
    exit 1
  fi
  export TF_VAR_alert_emails="$emails_json"
  if [ -n "$sms_number" ]; then
    export TF_VAR_alert_sms_country_code="$sms_code"
    export TF_VAR_alert_sms_number="$sms_number"
  fi
  # Counts, not addresses. The addresses are sensitive in the stack for a
  # reason and they do not belong in a job log.
  say "alert receivers read from state: $(jq -r 'length' <<<"$emails_json") email(s), sms $([ -n "$sms_number" ] && echo yes || echo no)"
fi

# ---------------------------------------------------------------------------
# 2. Plan, targeted at the app.
# ---------------------------------------------------------------------------
say "plan: $VAR_FILE, targeted at $TARGET"
PLAN="$WORK/config.tfplan"
if ! terraform -chdir="$STACK" plan -input=false -no-color -lock-timeout=5m \
    -target="$TARGET" -var-file="$VAR_FILE" -out="$PLAN" >"$WORK/plan.txt" 2>&1; then
  cat "$WORK/plan.txt"
  say "PLAN FAILED. Nothing was applied."
  echo "This is the stack refusing to be planned, not the guard refusing the plan."
  echo "Until it plans, no configuration change can reach $ENVIRONMENT this way."
  exit 1
fi
terraform -chdir="$STACK" show -json "$PLAN" >"$WORK/plan.json"

# ---------------------------------------------------------------------------
# 3. The guard. Exit 0 is one acceptable update, 3 is nothing to do, anything
#    else is a refusal that has already said why.
# ---------------------------------------------------------------------------
say "guard"
# Built rather than `go run`, because `go run` reports every non-zero exit of
# the program as its own exit status 1, and the branch below needs to tell
# "nothing to apply" (3) from "refused" (1).
(cd "$ROOT/tools" && GOTOOLCHAIN=local go build -o "$WORK/configguard" ./configguard)
set +e
"$WORK/configguard" -plan "$WORK/plan.json" -environment "$ENVIRONMENT" -target "$TARGET"
verdict=$?
set -e
case "$verdict" in
  0) ;;
  3)
    say "NO CONFIGURATION CHANGE for $ENVIRONMENT. Nothing to apply."
    exit 0
    ;;
  *)
    echo
    echo "The plan Terraform produced, for the record:"
    echo
    # Terraform's own rendering hides values it marks sensitive; the env values
    # this app carries by value are non-secret settings, and every secret is a
    # vault reference.
    tail -c 20000 "$WORK/plan.txt"
    say "REFUSED: the plan is not a configuration change. Nothing was applied."
    echo "::error title=Configuration apply refused::terraform plan -var-file=$VAR_FILE targeted at the container app wants to change something other than the environment list and the secret references. Read the guard's verdict above. A person has to plan and apply this stack in full."
    exit 1
    ;;
esac

if [ "$MODE" = "plan-only" ]; then
  say "PLAN ONLY: the apply above would have run. Nothing was applied."
  exit 0
fi

# ---------------------------------------------------------------------------
# 4. Apply that plan and nothing else. A saved plan cannot be applied against
#    a state that moved since it was made, so a concurrent change fails here
#    rather than being applied over.
# ---------------------------------------------------------------------------
say "apply"
if ! terraform -chdir="$STACK" apply -input=false -no-color -lock-timeout=5m "$PLAN"; then
  say "APPLY FAILED. Read the output above; state may have moved under the plan."
  exit 1
fi

say "APPLIED. The new revision is at ZERO percent traffic, on purpose."
echo "deploy.sh runs next and creates the revision that takes traffic from this"
echo "template, behind the migration and both health gates. This script never"
echo "shifts traffic; see the header for why."
exit 0
