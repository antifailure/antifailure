#!/usr/bin/env bash
# The edition check against a fake Azure and a fake origin, in every answer the
# real ones give.
#
# A CHECK THAT CANNOT SAY NO IS WORSE THAN NO CHECK, and this one guards the
# deploy path, so each refusal is exercised rather than reasoned about. The
# codes are not invented: every one of them was measured against the real
# enterprise entry point, running as the image that will be deployed.
#
#   configured  a template with all four variables, and one missing each of
#               them, and an az that fails outright
#   serving     200 and 400 from a licensed edition; 404, which is the
#               community image; 402, which is mounted and unlicensed; a
#               licence permitting only one of the two features, each way
#               round; and a body that is the wrong document under a right code
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# The four the entry point reads before it listens, as the app's template names
# them. AF_DATABASE_URL is here so the fake answers something real-shaped.
cat > "$TMP/bin/az" <<'FAKE_AZ'
#!/usr/bin/env bash
set -euo pipefail
if [ "${AF_EDITION_TEST_AZ:-ok}" = fail ]; then
  echo "ERROR: (ResourceNotFound) The Resource 'afcp-app' was not found" >&2
  exit 3
fi
for name in AF_DATABASE_URL ${AF_EDITION_TEST_ENV:-AF_EE_SSO_KEY AF_LICENSE_KEY AF_ORG AF_LICENSE_PUBLIC_KEYS}; do
  printf '%s\n' "$name"
done
FAKE_AZ

# One body and one code per path, from the table this check was built on.
cat > "$TMP/bin/curl" <<'FAKE_CURL'
#!/usr/bin/env bash
set -euo pipefail
url="${!#}"
want_code=0
case " $* " in *" -w "*) want_code=1 ;; esac
state="${AF_EDITION_TEST_SERVING:-licensed}"
case "$url" in
  */scim/v2/ServiceProviderConfig)
    case "$state" in
      licensed)    code=200; body='{"schemas":["urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"]}' ;;
      community)   code=404; body='<!doctype html><title>Antifailure</title>' ;;
      unlicensed)  code=402; body='{"error":"not_licensed","feature":"scim","licenseState":"none"}' ;;
      wrongbody)   code=200; body='{"error":"something else entirely"}' ;;
      sso_only)    code=402; body='{"error":"not_licensed","feature":"scim","licenseState":"active"}' ;;
      scim_only)   code=200; body='{"schemas":["urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"]}' ;;
      audit_unlicensed) code=200; body='{"schemas":["urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"]}' ;;
    esac ;;
  */sso/start)
    case "$state" in
      licensed|wrongbody|sso_only|audit_unlicensed) code=400; body='{"error":"Give an email address to find the right identity provider."}' ;;
      community)                   code=404; body='<!doctype html><title>Antifailure</title>' ;;
      unlicensed|scim_only)        code=402; body='{"error":"not_licensed","feature":"sso","licenseState":"none"}' ;;
    esac ;;
  */enterprise/audit-stream)
    case "$state" in
      licensed|wrongbody|sso_only|scim_only) code=401; body='{"error":"Sign in first."}' ;;
      community)                             code=404; body='<!doctype html><title>Antifailure</title>' ;;
      unlicensed|audit_unlicensed)           code=402; body='{"error":"not_licensed","feature":"audit_stream","licenseState":"active"}' ;;
    esac ;;
  *) code=500; body='unexpected path' ;;
esac
if [ "$want_code" = 1 ]; then printf '%s' "$code"; else printf '%s' "$body"; fi
FAKE_CURL

cat > "$TMP/bin/sleep" <<'FAKE_SLEEP'
#!/usr/bin/env bash
exit 0
FAKE_SLEEP

chmod +x "$TMP/bin/az" "$TMP/bin/curl" "$TMP/bin/sleep"

CHECKED=0
run_check() {
  CASE_OUT="$TMP/out"
  if env PATH="$TMP/bin:$PATH" "$@" > "$CASE_OUT" 2>&1; then CASE_RC=0; else CASE_RC=$?; fi
}

expect() {
  local name="$1"; shift
  CHECKED=$((CHECKED + 1))
  if "$@"; then
    printf 'ok  %s\n' "$name"
  else
    printf 'FAIL  %s\n' "$name" >&2
    sed 's/^/  /' "$CASE_OUT" >&2
    exit 1
  fi
}
is_zero() { [ "$1" -eq 0 ]; }
is_nonzero() { [ "$1" -ne 0 ]; }
says() { grep -Fq -- "$1" "$CASE_OUT"; }

CHECK="$ROOT/deploy/cd/edition-check.sh"

run_check "$CHECK" configured group afcp-app
expect "a template with all four passes" is_zero "$CASE_RC"

for missing in AF_EE_SSO_KEY AF_LICENSE_KEY AF_ORG AF_LICENSE_PUBLIC_KEYS; do
  # One break per assertion. A single case missing one variable would pass a
  # check that looked for any of the four rather than all of them.
  kept=""
  for name in AF_EE_SSO_KEY AF_LICENSE_KEY AF_ORG AF_LICENSE_PUBLIC_KEYS; do
    [ "$name" = "$missing" ] || kept="$kept $name"
  done
  run_check env AF_EDITION_TEST_ENV="$kept" "$CHECK" configured group afcp-app
  expect "a template missing $missing refuses the deploy" is_nonzero "$CASE_RC"
  expect "the refusal names $missing" says "$missing"
done

run_check env AF_EDITION_TEST_ENV="$missing" "$CHECK" configured group afcp-app
expect "the refusal sends the reader to the procedure" says "self-hosting/production.md"

run_check env AF_EDITION_TEST_AZ=fail "$CHECK" configured group afcp-app
expect "an unreadable app is a refusal, not a pass" is_nonzero "$CASE_RC"
expect "and it says it checked nothing" says "checked nothing"

run_check "$CHECK" serving https://app.example 1 0
expect "a licensed edition serves both routes" is_zero "$CASE_RC"
expect "and says all three are mounted and licensed" says "MOUNTED AND LICENSED"

run_check env AF_EDITION_TEST_SERVING=community "$CHECK" serving https://app.example 1 0
expect "the community image is refused" is_nonzero "$CASE_RC"
expect "and is named as the community image" says "COMMUNITY IMAGE IS DEPLOYED"

run_check env AF_EDITION_TEST_SERVING=unlicensed "$CHECK" serving https://app.example 1 0
expect "an unlicensed edition is refused" is_nonzero "$CASE_RC"
expect "and is told apart from an absent route" says "the licence does not permit it"

run_check env AF_EDITION_TEST_SERVING=sso_only "$CHECK" serving https://app.example 1 0
expect "a licence naming one feature of the two is refused" is_nonzero "$CASE_RC"

# The other half. Each probe is required on its own, so each needs a case in
# which only the other one passes, or dropping it goes unnoticed.
run_check env AF_EDITION_TEST_SERVING=scim_only "$CHECK" serving https://app.example 1 0
expect "a licence naming the other feature of the two is refused" is_nonzero "$CASE_RC"

# The third route. A licence that permits single sign-on and provisioning and
# not the audit stream is mounted everywhere and sold short in one place.
run_check env AF_EDITION_TEST_SERVING=audit_unlicensed "$CHECK" serving https://app.example 1 0
expect "a licence without the audit stream is refused" is_nonzero "$CASE_RC"
expect "and the audit stream route is the one named" says "/enterprise/audit-stream answered 402"

run_check env AF_EDITION_TEST_SERVING=wrongbody "$CHECK" serving https://app.example 1 0
expect "a 200 that is not the document it claims is refused" is_nonzero "$CASE_RC"
expect "and says what it wanted" says "is not ServiceProviderConfig"

run_check "$CHECK" nonsense
expect "an unknown mode refuses" is_nonzero "$CASE_RC"

printf '\n%d assertions, all passed\n' "$CHECKED"
