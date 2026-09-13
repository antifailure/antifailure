#!/usr/bin/env bash

# The rotation runbook's check command starts the check.
#
# Step 5 of docs/src/content/docs/self-hosting/rotating-secrets.md is the one
# thing an operator runs before deleting a sealing key, and it shipped as
# `az containerapp job start --command "node backup-cli.mjs reseal --check"`.
# The CLI takes --command as a list, so that is one program name with spaces in
# it, and it sends a container named after the job with no image and no
# environment. Nothing ran it before a person did.
#
# This extracts the block from the page as published and runs it against a fake
# az, then reads what it would have sent Azure: the argv the container starts
# with, and the image, environment and resources copied from the job. It also
# runs three blocks that must be refused, so it can say no.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PAGE="$ROOT/docs/src/content/docs/self-hosting/rotating-secrets.md"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

JOB_ID="/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/af-cp-centralus/providers/Microsoft.App/jobs/afcp-reseal"

# The job as a rotation leaves it in step 2: the second key by reference and the
# version as a literal, beside the connection string. A check that dropped either
# would open rows with the first key alone.
cat > "$TMP/job.json" <<JOB
{
  "id": "$JOB_ID",
  "name": "afcp-reseal",
  "properties": {
    "template": {
      "containers": [
        {
          "name": "reseal",
          "image": "ghcr.io/antifailure/control-plane-enterprise@sha256:1111111111111111111111111111111111111111111111111111111111111111",
          "command": ["node", "backup-cli.mjs", "reseal"],
          "env": [
            {"name": "AF_RESEAL_DATABASE_URL", "secretRef": "migration-database-url"},
            {"name": "AF_PROVIDER_KEY_SECRETS", "secretRef": "provider-key-secrets"},
            {"name": "AF_PROVIDER_KEY_VERSION", "value": "v2"}
          ],
          "resources": {"cpu": 0.5, "memory": "1Gi", "ephemeralStorage": "2Gi"}
        }
      ]
    }
  }
}
JOB

cat > "$TMP/bin/az" <<'FAKE_AZ'
#!/usr/bin/env bash
set -euo pipefail
dir="${AF_RUNBOOK_TEST_DIR:?}"
printf '%s\n' "$*" >> "$dir/calls"
args=("$@")
body=""
url=""
query=""
for ((i = 0; i < ${#args[@]}; i++)); do
  case "${args[$i]}" in
    --body) body="${args[$((i + 1))]}" ;;
    --url) url="${args[$((i + 1))]}" ;;
    --query) query="${args[$((i + 1))]}" ;;
  esac
done
case "$*" in
  "containerapp job show"*)
    if [ "$query" = id ]; then
      jq -r .id "$dir/job.json"
    else
      cat "$dir/job.json"
    fi
    ;;
  "rest --method post"*)
    case "$body" in
      @*) cp "${body#@}" "$dir/posted.json" ;;
      *) printf '%s' "$body" > "$dir/posted.json" ;;
    esac
    printf '%s\n' "$url" > "$dir/url"
    printf '{"id":"%s/executions/afcp-reseal-test","name":"afcp-reseal-test"}\n' "${url%%/start*}"
    ;;
  "containerapp job execution list"*)
    printf '{"name":"afcp-reseal-test"}\n'
    ;;
  "containerapp job start"*)
    printf '{"name":"afcp-reseal-test"}\n'
    ;;
  *)
    printf 'fake az: unexpected call: %s\n' "$*" >&2
    exit 64
    ;;
esac
FAKE_AZ
chmod +x "$TMP/bin/az"

# The first sh block under step 5, with the list indentation taken off.
step_five_block() {
  awk '
    /^5\. \*\*Verify before removing anything/ { inside = 1; next }
    inside && /^[0-9]+\. / { exit }
    inside && !fenced && /^ *```sh$/ { fenced = 1; next }
    inside && fenced && /^ *```$/ { exit }
    inside && fenced { sub(/^   /, ""); print }
  ' "$PAGE"
}

# Runs a block in a clean directory against the fake az. Prints nothing; leaves
# calls, url and posted.json behind for the verdict.
run_block() {
  local block="$1" case_dir="$TMP/case.$2"
  rm -rf "$case_dir"
  mkdir -p "$case_dir"
  cp "$TMP/job.json" "$case_dir/job.json"
  : > "$case_dir/calls"
  (cd "$case_dir" && PATH="$TMP/bin:$PATH" AF_RUNBOOK_TEST_DIR="$case_dir" bash -euo pipefail -c "$block") \
    > "$case_dir/stdout" 2> "$case_dir/stderr" || true
  printf '%s' "$case_dir"
}

# Empty when the case started exactly the check the job would run with --check
# added; otherwise one reason per line.
refusals() {
  local d="$1"
  if grep -q '^containerapp job start' "$d/calls"; then
    echo "it calls az containerapp job start, whose --command cannot carry this argv"
  fi
  if [ ! -s "$d/posted.json" ]; then
    echo "nothing was posted to the job's start endpoint"
    return
  fi
  if [ "$(grep -c '^rest --method post' "$d/calls")" != 1 ]; then
    echo "the start endpoint was called $(grep -c '^rest --method post' "$d/calls") times, not once"
  fi
  case "$(cat "$d/url")" in
    "https://management.azure.com$JOB_ID/start?api-version="?*) ;;
    *) echo "posted to $(cat "$d/url"), not the job's start endpoint" ;;
  esac
  jq -e '(.containers | length) == 1' "$d/posted.json" > /dev/null \
    || echo "the body does not hold exactly one container"
  jq -e '.containers[0].command == ["node", "backup-cli.mjs", "reseal", "--check"]' "$d/posted.json" > /dev/null \
    || echo "the container starts with $(jq -c '.containers[0].command' "$d/posted.json"), not node backup-cli.mjs reseal --check"
  jq -e --slurpfile job "$d/job.json" '
      .containers[0].name == $job[0].properties.template.containers[0].name' "$d/posted.json" > /dev/null \
    || echo "the container is named $(jq -c '.containers[0].name' "$d/posted.json"), not the job's own container"
  jq -e --slurpfile job "$d/job.json" '
      .containers[0].image == $job[0].properties.template.containers[0].image' "$d/posted.json" > /dev/null \
    || echo "the image is not the one the job runs"
  jq -e --slurpfile job "$d/job.json" '
      .containers[0].env == $job[0].properties.template.containers[0].env' "$d/posted.json" > /dev/null \
    || echo "the environment is not the job's own, so the check would not hold the keys step 4 had"
  jq -e --slurpfile job "$d/job.json" '
      .containers[0].resources.cpu == $job[0].properties.template.containers[0].resources.cpu
      and .containers[0].resources.memory == $job[0].properties.template.containers[0].resources.memory' "$d/posted.json" > /dev/null \
    || echo "cpu or memory differ from the job's"
}

CHECKED=0
FAILED=0
ok() { CHECKED=$((CHECKED + 1)); printf 'ok  %s\n' "$1"; }
fail() { CHECKED=$((CHECKED + 1)); FAILED=$((FAILED + 1)); printf 'FAIL  %s\n' "$1"; }

block="$(step_five_block)"
if [ -z "$block" ]; then
  fail "step 5 of the rotation runbook has an sh block to extract"
  printf 'runbook check: could not find the block, so nothing was checked\n'
  exit 1
fi
ok "step 5 of the rotation runbook has an sh block to extract"

d="$(run_block "$block" published)"
why="$(refusals "$d")"
if [ -z "$why" ]; then
  ok "the published block starts node backup-cli.mjs reseal --check with the job's own image, environment and resources"
else
  fail "the published block starts node backup-cli.mjs reseal --check with the job's own image, environment and resources"
  printf '%s\n' "$why" | sed 's/^/      /'
  sed 's/^/      stderr: /' "$d/stderr"
fi

# Three blocks that must be refused. If any of them passes, the verdict above
# proves nothing.
# Each is refused for the reason it exists to test, not merely refused: a block
# that no longer parses is refused too, and says nothing about the assertion.
must_refuse() {
  local label="$1" reason="$2" block="$3" d why
  if [ "$block" = "$PUBLISHED" ]; then
    fail "refuses $label (the change to the block did not land)"
    return
  fi
  d="$(run_block "$block" "$label")"
  why="$(refusals "$d")"
  if printf '%s\n' "$why" | grep -Fq -- "$reason"; then
    ok "refuses $label ($reason)"
  else
    fail "refuses $label for the reason \"$reason\""
    printf '%s\n' "${why:-it was not refused at all}" | sed 's/^/      /'
  fi
}

PUBLISHED="$block"
must_refuse "the command this page shipped with" "az containerapp job start" \
  'az containerapp job start -n afcp-reseal -g af-cp-centralus \
  --command "node backup-cli.mjs reseal --check"'
must_refuse "the published block without --check" "not node backup-cli.mjs reseal --check" \
  "${block//, \"--check\"/}"
must_refuse "the published block without the job's environment" "the environment is not the job's own" \
  "${block//env, /}"

printf 'runbook check: %s assertions, %s failed\n' "$CHECKED" "$FAILED"
[ "$FAILED" = 0 ]
