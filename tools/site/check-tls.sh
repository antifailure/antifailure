#!/usr/bin/env bash
#
# Every hostname the site answers on completes a verified TLS handshake, is
# served a certificate that actually covers that name, and is not inside the
# renewal window.
#
# The reason this exists: antifailure.dev and www.antifailure.dev are two
# separate custom domains on one Static Web App, and Azure provisions and
# renews a separate managed certificate for each one. The apex certificate
# carries SAN DNS:antifailure.dev and nothing else; the www certificate carries
# SAN DNS:www.antifailure.dev and nothing else. They are two independent
# lifecycles, and until this script there was nothing anywhere that would
# notice if one of them lapsed while the other stayed healthy. The site would
# look completely fine to anyone who typed the apex.
#
# That failure is worse here than it is on most domains, and the reason is
# permanent and outside this repository's control. antifailure.dev sits under
# .dev, and the whole .dev top level domain is on the browsers' built in HSTS
# preload list with includeSubDomains:
#
#   curl "https://hstspreload.org/api/v2/status?domain=antifailure.dev"
#   {"name":"antifailure.dev","status":"preloaded","bulk":false,"preloadedDomain":"dev"}
#
# preloadedDomain is "dev", not "antifailure.dev". The rule is compiled into
# the browser binary, it applies on a cold profile with no prior visit, and no
# header of ours turns it on or off. The consequence: a certificate fault on
# any *.antifailure.dev name is a hard ERR_SSL_PROTOCOL_ERROR with no "proceed
# anyway" for the reader to click. There is no degraded mode to fall back to,
# so the only acceptable state is a working certificate on every name, always.
#
# What this does NOT establish, and the distinction matters. A handshake that
# succeeds from one machine says one edge node answered one client correctly.
# It is not proof that every node in the anycast fleet is healthy, and it
# cannot reproduce a fault that only some clients see. Reachability from here
# is not identity everywhere. A green run means "no lapsed certificate as of
# this run from this vantage point", which is worth having and is not the same
# claim as "nobody is seeing a TLS error".
#
# Run: tools/site/check-tls.sh          (or: just check-tls)

set -uo pipefail

hosts=${AF_TLS_HOSTS:-"antifailure.dev www.antifailure.dev"}

# Azure renews a managed certificate well before expiry, so a certificate still
# inside this window is not yet an outage. It is the signal that a renewal that
# should already have happened has not, which is the point at which somebody
# has time to act instead of finding out from a reader.
renew_window_days=${AF_TLS_RENEW_WINDOW_DAYS:-21}

failures=0
leaf=$(mktemp)
trap 'rm -f "$leaf"' EXIT

note() { printf '  ok   %s\n' "$1"; }

bad() {
  failures=$((failures + 1))
  printf '  FAIL %s\n' "$1" >&2
  [ -n "${GITHUB_ACTIONS:-}" ] && printf '::error::%s\n' "$1"
  return 0
}

for host in $hosts; do
  printf '\n%s\n' "$host"

  # curl verifies the chain, the hostname, and the expiry the way a browser
  # does, and its exit code is the gate. Nothing here is piped into anything:
  # a pipeline would hand back the exit code of the tail of the pipe, and tail
  # and head and grep all exit 0 on input that describes a failure.
  code=$(curl -sS -m 20 -o /dev/null -w '%{http_code}' "https://$host/")
  curl_status=$?
  if [ "$curl_status" -ne 0 ]; then
    bad "$host: TLS request failed, curl exit $curl_status (35 and 60 are handshake and certificate faults)"
    continue
  fi
  note "$host: verified TLS handshake, HTTP $code (curl exit 0)"

  # The leaf certificate the server actually presents for this SNI name, which
  # is the thing being asserted about. Redirecting stdin rather than piping
  # into s_client keeps the exit code being read the one s_client returned.
  openssl s_client -servername "$host" -connect "$host:443" >"$leaf" 2>/dev/null </dev/null
  sclient_status=$?
  if [ "$sclient_status" -ne 0 ] || [ ! -s "$leaf" ]; then
    bad "$host: could not retrieve a certificate, openssl s_client exit $sclient_status"
    continue
  fi

  # checkhost is the real name match against SAN, not a substring search of the
  # subject. It exits non zero when the served certificate does not cover the
  # name it was served for, which is exactly the state a half provisioned
  # custom domain leaves behind.
  openssl x509 -in "$leaf" -noout -checkhost "$host" >/dev/null
  host_status=$?
  if [ "$host_status" -ne 0 ]; then
    san=$(openssl x509 -in "$leaf" -noout -ext subjectAltName 2>/dev/null | tr -d '\n')
    bad "$host: served a certificate that does not cover this name (openssl checkhost exit $host_status). Served SAN: ${san:-none}"
    continue
  fi
  note "$host: certificate covers this name"

  openssl x509 -in "$leaf" -noout -checkend $((renew_window_days * 86400)) >/dev/null
  expiry_status=$?
  not_after=$(openssl x509 -in "$leaf" -noout -enddate 2>/dev/null)
  if [ "$expiry_status" -ne 0 ]; then
    bad "$host: certificate expires within $renew_window_days days ($not_after). Azure should have renewed it already."
    continue
  fi
  note "$host: ${not_after:-expiry unknown}, more than $renew_window_days days out"
done

printf '\n'
if [ "$failures" -ne 0 ]; then
  printf '%s\n' "$failures TLS check(s) failed. A reader on a .dev name cannot click past this." >&2
  exit 1
fi
printf '%s\n' "every hostname presents a valid certificate for its own name"
