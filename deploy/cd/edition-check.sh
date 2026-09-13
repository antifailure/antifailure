#!/usr/bin/env bash
# Is this deployment the edition the hosted plan sells, and does it behave like
# it?
#
# TWO QUESTIONS AT TWO MOMENTS, AND NEITHER IS THE OTHER.
#
#   configured  before deploy.sh: does the app's CURRENT template carry the
#               four variables the enterprise entry point needs?
#   serving     after the traffic shift: does the public origin actually answer
#               a single sign-on route, a directory provisioning route and the
#               audit stream route?
#
# WHY THE FIRST ONE EXISTS. Measured against the entry point rather than read
# off it: without AF_EE_SSO_KEY the process EXITS BEFORE IT LISTENS, whatever
# the licence says, and that is the state the hosted app was in before the
# edition was configured. deploy.sh creates its revision with
# `az containerapp update --image`, which inherits the template the
# configuration apply just wrote, so the variables are either in that template
# or the revision cannot start. Without this check the symptom is a candidate
# that never reaches Running, five minutes of waiting, and a message about the
# platform rather than about the tfvars file that is missing a line.
#
# WHY THE SECOND ONE EXISTS, AND WHY IT IS NOT THE HEALTH GATE. health-gate.sh
# proves the origin is ready on the expected commit. A community image is ready
# on the expected commit too: it answers /readyz perfectly and 404s every
# enterprise route, which is what the hosted control plane did for its whole
# life while the plan sold three features. Ready is not the same claim as
# licensed and mounted, and only one of them is what was sold.
#
# WHAT THE CODES MEAN, each measured against the real entry point:
#
#   200  mounted, licensed, and the route ran
#   404  the community image is deployed, or the routes are not mounted
#   402  mounted and refused: the licence does not permit that feature, and the
#        body names which state it is in
#
# A failure here NEVER rolls anything back, and that is deliberate. The
# application is healthy; what is wrong is the edition or the licence, and
# taking a working control plane away from every customer because a licence
# lapsed would be a worse outage than the one it reports.
#
#   edition-check.sh configured <resource-group> <app>
#   edition-check.sh serving <base-url> [attempts] [interval-seconds]

set -uo pipefail

MODE="${1:?usage: edition-check.sh configured <resource-group> <app> | serving <base-url> [attempts] [interval]}"

# The four the entry point reads before it listens. AF_EE_SSO_KEY and
# AF_LICENSE_KEY arrive as secret references and AF_ORG and
# AF_LICENSE_PUBLIC_KEYS as values; the template names all four either way,
# which is what this can see without reading a single secret.
REQUIRED_ENV="AF_EE_SSO_KEY AF_LICENSE_KEY AF_ORG AF_LICENSE_PUBLIC_KEYS"

say() { printf '%s\n' "$*"; }

configured() {
  local rg="${1:?resource group}" app="${2:?app}" names rc missing=""
  names="$(az containerapp show -n "$app" -g "$rg" \
    --query "properties.template.containers[0].env[].name" -o tsv 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    say "EDITION CHECK COULD NOT READ $app in $rg, so it checked nothing:"
    say "  $names"
    return 1
  fi
  for want in $REQUIRED_ENV; do
    grep -Fxq "$want" <<<"$names" || missing="${missing} ${want}"
  done
  if [ -n "$missing" ]; then
    say "THE ENTERPRISE EDITION IS NOT CONFIGURED ON $app."
    say "  missing from the template:${missing}"
    say ""
    say "This deploy would put the enterprise image on an app that cannot start"
    say "it: without AF_EE_SSO_KEY the process exits before it listens, whatever"
    say "the licence says. Nothing has been deployed and the app is untouched."
    say ""
    say "Set enterprise_edition, license_org and license_public_keys in that"
    say "environment's tfvars, and put the two secrets in its Key Vault first."
    say "docs/src/content/docs/self-hosting/production.md, under Turning on the"
    say "enterprise edition, is the procedure in order."
    return 1
  fi
  say "the template carries all four enterprise variables: $(printf '%s' "$REQUIRED_ENV")"
}

# One route, its expected code, and what each other answer means.
probe() {
  local base="$1" path="$2" want_code="$3" want_body="$4" body code
  body="$(curl -sS -m 15 "$base$path" 2>/dev/null)"
  code="$(curl -sS -m 15 -o /dev/null -w '%{http_code}' "$base$path" 2>/dev/null)"
  case "$code" in
    "$want_code")
      case "$body" in
        *"$want_body"*) say "  $path answered $code and is the route it claims to be"; return 0 ;;
        *)
          say "  $path answered $code and the body is not $want_body: $(printf '%s' "$body" | head -c 200)"
          return 1 ;;
      esac
      ;;
    404)
      say "  $path answered 404. THE COMMUNITY IMAGE IS DEPLOYED, or the extensions are not mounted."
      return 1 ;;
    402)
      say "  $path answered 402, so the route is mounted and the licence does not permit it: $(printf '%s' "$body" | head -c 240)"
      return 1 ;;
    *)
      say "  $path answered ${code:-000}: $(printf '%s' "$body" | head -c 200)"
      return 1 ;;
  esac
}

serving() {
  local base="${1:?base url}" attempts="${2:-10}" interval="${3:-6}" attempt
  base="${base%/}"
  for attempt in $(seq 1 "$attempts"); do
    say "attempt $attempt/$attempts: the edition this origin serves"
    # Both, because one of them passing is not the claim. Single sign-on and
    # directory provisioning are licensed per feature, so a licence naming one
    # and not the other answers 200 here and 402 there, which is the state the
    # hosted plan must never be in and which one probe cannot see.
    #
    # The audit stream's route asks who is signed in only after the gate has
    # asked whether the installation is licensed for it, so an unauthenticated
    # 401 is the licensed answer and a 402 is the refusal, the same split as
    # the other two.
    if probe "$base" /scim/v2/ServiceProviderConfig 200 ServiceProviderConfig &&
       probe "$base" /sso/start 400 "email address" &&
       probe "$base" /enterprise/audit-stream 401 "Sign in first"; then
      say "SINGLE SIGN-ON, DIRECTORY PROVISIONING AND THE AUDIT STREAM ARE MOUNTED AND LICENSED on $base"
      return 0
    fi
    [ "$attempt" -lt "$attempts" ] && sleep "$interval"
  done
  say ""
  say "THE DEPLOYED EDITION DOES NOT SERVE WHAT THE HOSTED PLAN SELLS."
  say "  origin: $base"
  say "The application is healthy and is left serving: this is the edition or the"
  say "licence, not the build. Read the container's startup lines, which say what"
  say "it mounted and what the licence permits right now."
  return 1
}

case "$MODE" in
  configured) shift; configured "$@" ;;
  serving)    shift; serving "$@" ;;
  *)
    say "edition-check.sh: mode must be configured or serving, not '$MODE'"
    exit 2 ;;
esac
