#!/usr/bin/env bash
# Boots a throwaway Keycloak the conformance suite can actually talk to.
#
# Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
#
# This script exists because the invocation this suite used to document could
# never have worked. It said to run Keycloak on plain HTTP and point
# AF_KEYCLOAK_URL at it, and the product refuses a non-https provider in two
# separate places on purpose: parseIdentityProviderMetadata will not accept an
# http single sign-on URL, and discover() will not accept an http token
# endpoint. Both refusals are correct. A token exchange over plain HTTP carries
# a client secret in clear text, and weakening that to make a test pass would be
# testing a product nobody ships.
#
# So the provider gets TLS. The certificate is generated here, at run time, into
# a temporary directory outside the repository, and the private key never
# touches the tree. That is the same rule the SAML signing key follows in
# idp.ts, and it has no "but it is only a test key" exception.
#
# Usage:
#   eval "$(ee/web/sso/test/keycloak-up.sh)"
#   node --test ee/web/sso/test/keycloak.test.ts
#   ee/web/sso/test/keycloak-up.sh --down
#
# The eval matters: the script prints the two environment variables the suite
# needs, because one of them is the path to a certificate that did not exist
# until the script ran.

set -euo pipefail

NAME="${AF_KEYCLOAK_CONTAINER:-af-keycloak}"
PORT="${AF_KEYCLOAK_PORT:-8443}"
IMAGE="${AF_KEYCLOAK_IMAGE:-quay.io/keycloak/keycloak:26.0}"
STATE="${TMPDIR:-/tmp}/af-keycloak-${NAME}"

if [ "${1:-}" = "--down" ]; then
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  rm -rf "$STATE"
  echo "removed $NAME and $STATE" >&2
  exit 0
fi

# A certificate for the name the suite will dial. Subject alternative names are
# not optional here: Node verifies them and a common name alone has not been
# accepted for years.
mkdir -p "$STATE"
chmod 700 "$STATE"
# Regenerated when it is missing OR about to expire. A one day certificate in a
# state directory somebody kept is an expired certificate tomorrow, and Node
# reports that as a bare handshake failure that looks like the container is
# broken rather than like the clock moved.
if [ -f "$STATE/tls.crt" ] && ! openssl x509 -checkend 3600 -noout -in "$STATE/tls.crt" >/dev/null 2>&1; then
  echo "the certificate in $STATE has expired; regenerating and recreating the container" >&2
  rm -f "$STATE/tls.crt" "$STATE/tls.key"
  docker rm -f "$NAME" >/dev/null 2>&1 || true
fi

if [ ! -f "$STATE/tls.crt" ]; then
  openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout "$STATE/tls.key" -out "$STATE/tls.crt" \
    -days 1 -subj '/CN=localhost' -sha256 \
    -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1' >/dev/null 2>&1
  chmod 600 "$STATE/tls.key"
  chmod 644 "$STATE/tls.crt"
  echo "generated a one day certificate in $STATE" >&2
fi

# A container that exists but is stopped is STARTED, not recreated.
#
# Keycloak's first start runs a Quarkus build whose result lives in the
# container's writable layer, so recreating throws it away and pays for it
# again: on a loaded machine that was 703 seconds, measured. Only recreate when
# there is nothing to reuse.
if docker ps -a --filter "name=^${NAME}$" --format '{{.Names}}' | grep -q . &&
   ! docker ps --filter "name=^${NAME}$" --format '{{.Names}}' | grep -q .; then
  echo "restarting the existing $NAME rather than rebuilding it" >&2
  docker start "$NAME" >/dev/null
fi

if ! docker ps --filter "name=^${NAME}$" --format '{{.Names}}' | grep -q .; then
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  # The bootstrap password of a container this script creates and destroys. It
  # is not a credential anybody can reuse and it is overridable so that nothing
  # here hardcodes one that could be.
  # The dev database lives in memory, and a WRONG DIAGNOSIS is recorded here
  # because the right one is only visible next to it.
  #
  # start-dev keeps an H2 file under /opt/keycloak/data. The observed failure
  # was `IO Exception: "/opt/keycloak/data/h2/keycloakdb.mv.db" [90028-230]`,
  # and I read that as disk contention on the container's overlay filesystem,
  # since a dozen other containers were competing for the same disk. So I moved
  # it to a tmpfs. IT FAILED THE SAME WAY WITH THE DATABASE IN MEMORY, which is
  # what disproves the theory: there is no disk to contend for in a tmpfs.
  #
  # The real cause is one frame up the stack, in the lines that PRECEDE the H2
  # error every time: `Acquisition timeout while waiting for new connection`,
  # from io.agroal.pool.ConnectionPool. The JVM is starved badly enough that the
  # connection pool's own timeout expires, the waiting thread is interrupted,
  # and H2's file channel write fails BECAUSE of the interruption. The IO
  # exception is the symptom and the pool timeout is the cause, which is why the
  # acquisition timeout is raised below and why that is the setting that matters.
  #
  # The tmpfs stays because it is harmless and a throwaway realm has no reason to
  # touch a disk, not because it fixed anything. It did not.
  #
  # CPU pinned, and the JVM told about it.
  #
  # Keycloak's first start runs a Quarkus build, and the JVM sizes its ForkJoin
  # pools from the number of processors it believes it has. On a machine running
  # a dozen other containers it believes it has all of them, oversubscribes, and
  # spends its time context switching. The observed symptom that prompted this:
  # a build that completed in 85 seconds on a quiet machine was still running
  # after 21 minutes on a loaded one, with the JVM at 237% CPU throughout.
  #
  # So the cgroup gets a limit and the JVM is told the same number through
  # ActiveProcessorCount, which is the part that actually changes how it sizes
  # its pools.
  #
  # What this comment does NOT claim, because I could not demonstrate either:
  # that it is faster (the build took 85 seconds on a quiet machine and 703 on a
  # loaded one, and machine load changed at the same time as the limit, so
  # nothing here separates the two), or that the cgroup limit is enforced as set
  # (NanoCpus reads back as 4000000000, and `docker stats` on Docker Desktop
  # still reported 701% for this container, which I have not resolved).
  # Bound to 127.0.0.1 only, and addressed by IP everywhere below.
  #
  # Two reasons, both learned the hard way. Publishing on 0.0.0.0 puts a
  # Keycloak whose admin password is "admin" on every interface of the machine,
  # which is not a thing to leave running on somebody's laptop. And Docker
  # publishes IPv4 while Node resolves "localhost" to ::1 first, so addressing
  # it by name produced an intermittent UND_ERR_CONNECT_TIMEOUT against
  # ::1:8443 partway through a run. The certificate carries IP:127.0.0.1 in its
  # subject alternative names precisely so the IP can be used directly.
  #
  # Only 8443 is published, and that is what actually keeps this on TLS.
  #
  # KC_HTTP_ENABLED=false used to be here and did nothing: start-dev turns plain
  # HTTP on regardless, and the startup line says so, "Listening on:
  # http://0.0.0.0:8080 and https://0.0.0.0:8443". A setting that reads like a
  # guarantee and enforces nothing is worse than no setting, so it is gone and
  # the port mapping is the guarantee.
  docker run -d --name "$NAME" -p "127.0.0.1:${PORT}:8443" \
    --cpus="${AF_KEYCLOAK_CPUS:-4}" \
    -e JAVA_OPTS_APPEND="-XX:ActiveProcessorCount=${AF_KEYCLOAK_CPUS:-4} -Dquarkus.datasource.jdbc.acquisition-timeout=${AF_KEYCLOAK_DB_TIMEOUT:-120}" \
    -e KC_BOOTSTRAP_ADMIN_USERNAME="${AF_KEYCLOAK_USER:-admin}" \
    -e KC_BOOTSTRAP_ADMIN_PASSWORD="${AF_KEYCLOAK_PASSWORD:-admin}" \
    -e KC_HTTPS_CERTIFICATE_FILE=/etc/x509/tls.crt \
    -e KC_HTTPS_CERTIFICATE_KEY_FILE=/etc/x509/tls.key \
    --tmpfs /opt/keycloak/data:rw,size=512m,mode=1777 \
    -e KC_HOSTNAME_STRICT=false \
    -v "$STATE/tls.crt:/etc/x509/tls.crt:ro" \
    -v "$STATE/tls.key:/etc/x509/tls.key:ro" \
    "$IMAGE" start-dev >/dev/null
  echo "started $NAME on https://127.0.0.1:${PORT}" >&2
fi

# Readiness is polled rather than slept on, and the wait is generous because
# Keycloak's first start builds its Quarkus image and a loaded machine can take
# minutes over it. Failing to come up prints the log rather than a bare timeout,
# because "it did not start" is not a diagnosis.
DEADLINE="${AF_KEYCLOAK_TIMEOUT:-600}"
ELAPSED=0
until curl -sk -o /dev/null --max-time 5 "https://127.0.0.1:${PORT}/realms/master"; do
  if ! docker ps --filter "name=^${NAME}$" --format '{{.Names}}' | grep -q .; then
    echo "$NAME exited before it listened:" >&2
    docker logs --tail 40 "$NAME" >&2 || true
    exit 1
  fi
  if [ "$ELAPSED" -ge "$DEADLINE" ]; then
    echo "$NAME did not listen within ${DEADLINE}s. Last log lines:" >&2
    docker logs --tail 40 "$NAME" >&2 || true
    exit 1
  fi
  sleep 5
  ELAPSED=$((ELAPSED + 5))
done

echo "ready after ${ELAPSED}s" >&2
echo "export AF_KEYCLOAK_URL=https://127.0.0.1:${PORT}"
echo "export NODE_EXTRA_CA_CERTS=$STATE/tls.crt"
