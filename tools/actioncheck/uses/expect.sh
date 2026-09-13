#!/usr/bin/env bash
# The assertions for the job in ci.yml that runs action.yml whole.
#
#   expect.sh calls   <case> [<line>...]  the af calls the case made, exactly
#   expect.sh plane   <case> [<line>...]  the requests the stand in control plane saw
#   expect.sh secrets <case> [<line>...]  what the secret pairing handed each af call
#   expect.sh equal <what> <want> <got>   one value, such as a step output
#   expect.sh file  <path>                a file the case must have written
#
# `calls`, `plane` and `secrets` compare the WHOLE record, in order, and then
# empty it, so each case is judged on its own and a line the action should not
# have produced is as red as one it failed to produce. `calls` also empties the
# secret record and removes every file a case writes into the workspace, because
# the action decides some steps with hashFiles and a report left by the previous
# case would be read as this one's. So a case that checks `secrets` does it
# before `calls`.
set -euo pipefail

temp="${RUNNER_TEMP:?RUNNER_TEMP is not set}"

compare() {
  local what="$1" record="$2"
  shift 2
  local want got
  want="$temp/af-want"
  if [ "$#" -gt 0 ]; then printf '%s\n' "$@" > "$want"; else : > "$want"; fi
  got="$temp/af-got"
  if [ -f "$record" ]; then cp "$record" "$got"; else : > "$got"; fi
  rm -f "$record"
  if ! diff -u "$want" "$got" > "$temp/af-diff"; then
    echo "::error::$what is not what the action should have produced. Lines marked - were expected and lines marked + are what happened:"
    cat "$temp/af-diff"
    return 1
  fi
  echo "$what: as expected"
  sed 's/^/  /' "$got"
}

case "${1:-}" in
  calls)
    case_name="$2"
    shift 2
    status=0
    compare "the af calls in the $case_name case" "$temp/af-calls" "$@" || status=$?
    rm -f "$temp/af-secrets"
    rm -f report.md report.json payload.json rehearsal.md workload-result.json teardown-result.json
    exit "$status"
    ;;
  plane)
    case_name="$2"
    shift 2
    compare "the requests the control plane saw in the $case_name case" "$temp/af-plane" "$@"
    ;;
  secrets)
    case_name="$2"
    shift 2
    compare "what the secret pairing handed af in the $case_name case" "$temp/af-secrets" "$@"
    ;;
  equal)
    if [ "$3" != "$4" ]; then
      echo "::error::$2 is '$4', and the action should have made it '$3'"
      exit 1
    fi
    echo "$2: '$4', as expected"
    ;;
  file)
    if [ ! -s "$2" ]; then
      echo "::error::$2 was not written, so the step that should have written it never ran or ran with other arguments"
      exit 1
    fi
    echo "$2: written"
    ;;
  *)
    echo "usage: expect.sh calls|plane|secrets|equal|file ..." >&2
    exit 2
    ;;
esac
