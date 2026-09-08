#!/usr/bin/env bash
# Does the enterprise IMAGE mount the licensed products, and does it refuse
# rather than merely fail to answer when there is no licence.
#
# Not a rerun of ee/web/server/test/entrypoint.test.ts, which proves the same
# three answers about a process started with `node src/main.ts` from a
# checkout. This proves them about the artifact, which is a different claim and
# is the one that was missing: the entry point ran and nothing shipped it, so
# "it works" was a statement about a laptop.
#
# THREE PROCESSES, AND THE THIRD IS WHAT MAKES THE OTHER TWO MEAN ANYTHING.
#
#   licensed enterprise image    200, answered by the ee/web package
#   unlicensed enterprise image  402, refused by the gate, naming the feature
#   community image              404, which is what absence actually looks like
#
# Without the community control, 402 and 404 are two numbers and "refused by
# the gate rather than by absence" is an assertion about a code path nobody
# watched. The community image is the before-state of this whole row,
# reproduced on demand from a published artifact.
#
# It also proves the layout, which is the thing this image could get wrong in a
# way no unit suite would see. The enterprise workspace reaches the community
# one through relative symlinks that npm wrote; a flattened or shifted image
# layout leaves those dangling, and a dangling symlink is not a build error. If
# the licensed request is answered by ee/web/sso then the link resolved, the
# pool opened, the extension registered before the router was built, and the
# route reached a socket.
#
# usage: enterprise-image-proof.sh <enterprise-image> <community-image>
set -euo pipefail

enterprise="${1:?the enterprise image to prove}"
community="${2:?the community image to compare it against}"

# Unique per run. This script is run on a machine that holds other people's
# containers, so every name it creates carries a suffix and it removes only
# what it created. Nothing here stops or prunes anything it did not start.
run="afee$$"
net="net-${run}"
pg="pg-${run}"

password_migrator='devpassword'
password_app='apppassword'
migrator_url="postgres://af_migrator:${password_migrator}@${pg}:5432/antifailure"
app_url="postgres://af_app:${password_app}@${pg}:5432/antifailure"
base_url='https://enterprise.test'

# 44 characters of the alphabet sso_connections_handle_shape allows, which is
# 40 to 64 of [A-Za-z0-9_-]. Constant rather than random: the database is
# created and destroyed by this script, so the unique index on it cannot be
# hit twice, and a constant is one less thing to print when something fails.
handle='proofhandle0000000000000000000000000000000ab'
org_slug="ee-proof-${run}"

started=()

cleanup() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    echo
    echo "=== the proof failed, so here is what every container said ==="
    for c in "${started[@]:-}"; do
      [ -n "$c" ] || continue
      echo "--- $c"
      docker logs "$c" 2>&1 | tail -60 || true
    done
  fi
  for c in "${started[@]:-}"; do
    [ -n "$c" ] || continue
    docker rm -f "$c" >/dev/null 2>&1 || true
  done
  docker network rm "$net" >/dev/null 2>&1 || true
  return "$status"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# ---------------------------------------------------------------------------
# A licence, minted here, trusted by nothing else.
#
# The signing key is generated per run and never written to the repository. An
# installation trusts a key because AF_LICENSE_PUBLIC_KEYS names it, so a key
# that exists for ninety seconds inside one CI job can mint a licence this
# process will honour and no other installation ever will. That is the same
# rule ee/web/server/test/harness.ts follows and it is why there is no key
# material in the tree or in either image.
# ---------------------------------------------------------------------------
license_json=$(node -e '
const { generateKeyPairSync, sign } = require("node:crypto")
const kid = "proof-" + Math.random().toString(36).slice(2, 10)
const { publicKey, privateKey } = generateKeyPairSync("ed25519")
// 32 raw bytes: the DER SubjectPublicKeyInfo prefix for Ed25519 is 12 bytes
// and constant, which is the same slice license.ts undoes when it reads one.
const raw = publicKey.export({ format: "der", type: "spki" }).subarray(12)
const claims = {
  id: "lic-proof",
  org: process.argv[1],
  plan: "enterprise",
  features: ["sso", "scim"],
  seats: 25,
  issued_at: new Date(Date.now() - 60_000).toISOString(),
  expires_at: new Date(Date.now() + 365 * 24 * 3600 * 1000).toISOString(),
  trial: false,
  kid,
}
const payload = Buffer.from(JSON.stringify(claims), "utf8")
const signature = sign(null, payload, privateKey)
process.stdout.write(JSON.stringify({
  publicKeys: kid + "=" + raw.toString("base64"),
  key: "aflic_" + payload.toString("base64url") + "." + signature.toString("base64url"),
}))
' "$org_slug")

public_keys=$(node -e 'process.stdout.write(JSON.parse(process.argv[1]).publicKeys)' "$license_json")
license_key=$(node -e 'process.stdout.write(JSON.parse(process.argv[1]).key)' "$license_json")
# 32 bytes, assembled here. keyFromEnv refuses a single repeated byte, which is
# the shape of a placeholder somebody meant to replace, so this cannot be a
# constant even in a proof.
sso_key=$(node -e 'process.stdout.write(require("node:crypto").randomBytes(32).toString("base64"))')

# ---------------------------------------------------------------------------
# A database, and the schema applied by the image's own bootstrap entrypoint.
#
# `node bootstrap.mjs` rather than a migration run from the checkout, because
# that is the command the Helm chart and the Terraform job actually issue
# against this image. It applies the migrations and grants the application role
# its membership, and running it here means the enterprise image's entrypoint
# is exercised rather than assumed to have survived the layout change.
# ---------------------------------------------------------------------------
echo "== a database"
docker network create "$net" >/dev/null
started+=("$pg")
docker run -d --name "$pg" --network "$net" \
  -e POSTGRES_USER=af_migrator \
  -e POSTGRES_DB=antifailure \
  -e "POSTGRES_PASSWORD=${password_migrator}" \
  postgres:17-alpine@sha256:18cfe3ef5e6815560c98237d6216d1e5119702fb0f3894c8785dd58b8bbe5d73 >/dev/null

for _ in $(seq 1 60); do
  if docker exec "$pg" pg_isready -U af_migrator -d antifailure >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "$pg" pg_isready -U af_migrator -d antifailure >/dev/null \
  || fail "the database never became ready"

echo "== the enterprise image bootstraps its own schema"
docker run --rm --network "$net" \
  -e "AF_DATABASE_URL=${app_url}" \
  -e "AF_MIGRATION_DATABASE_URL=${migrator_url}" \
  "$enterprise" node bootstrap.mjs

# ---------------------------------------------------------------------------
# One organization, on the enterprise plan, with a SAML connection.
#
# The plan is not decoration. ee/web/sso resolves a connection through an
# entitlement check whose authority is the control plane's own catalogue rather
# than the licence key, and its default is no. An organization with no plan is
# refused 403, which would turn the 402 case below into a pass for the wrong
# reason: a licence gate that never ran because an entitlement gate refused
# first is a gate nobody tested.
#
# The connection row exists so that a 404 from the metadata route means the
# route is absent rather than the row.
# ---------------------------------------------------------------------------
echo "== one organization with a connection"
docker exec -i -e PGPASSWORD="$password_migrator" "$pg" \
  psql -v ON_ERROR_STOP=1 -U af_migrator -d antifailure >/dev/null <<SQL
INSERT INTO organizations (slug, name, plan)
VALUES ('${org_slug}', 'Enterprise proof', 'enterprise');
INSERT INTO sso_connections (
  org_id, handle, kind, display_name, enabled, default_role,
  idp_entity_id, idp_sso_url, idp_certificates)
SELECT id, '${handle}', 'saml', 'Directory', true, 'member',
       'https://idp.test/${org_slug}/metadata', 'https://idp.test/sso', '{}'
FROM organizations WHERE slug = '${org_slug}';
SQL

# ---------------------------------------------------------------------------
# The three processes.
# ---------------------------------------------------------------------------

# Starts one control plane and waits for it to answer /health.
#
# /health rather than a log line, because this is a claim about the image: a
# container whose process printed a listening line and whose published port
# answers nothing has failed in exactly the way an image can fail and a
# checkout cannot.
start_plane() {
  local name="$1" image="$2" port="$3"
  shift 3
  started+=("$name")
  docker run -d --name "$name" --network "$net" -p "127.0.0.1:${port}:8080" \
    -e "AF_DATABASE_URL=${app_url}" \
    -e AF_GITHUB_CLIENT_ID=id \
    -e AF_GITHUB_CLIENT_SECRET=secret \
    -e AF_GITHUB_REDIRECT_URI=https://app.test/auth/github/callback \
    -e "AF_APP_BASE_URL=${base_url}" \
    -e AF_INSECURE_COOKIES=1 \
    -e AF_MIGRATE=0 \
    "$@" "$image" >/dev/null

  for _ in $(seq 1 90); do
    if curl -fsS -o /dev/null "http://127.0.0.1:${port}/health" 2>/dev/null; then
      return 0
    fi
    # A container that exited is never going to answer, and waiting the full
    # ninety seconds for it turns a refusal to start into a timeout, which
    # reads as a slow machine rather than as the failure it is.
    if [ "$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null)" != "true" ]; then
      fail "$name exited before answering /health"
    fi
    sleep 1
  done
  fail "$name never answered /health"
}

# The status and the body, so an assertion can be made about both. A 200 from
# something that is not the SAML metadata document proves nothing.
probe() {
  local port="$1" path="$2"
  curl -sS -o /tmp/"${run}".body -w '%{http_code}' "http://127.0.0.1:${port}${path}"
}

echo "== licensed enterprise"
start_plane "licensed-${run}" "$enterprise" 18081 \
  -e "AF_ORG=${org_slug}" \
  -e "AF_LICENSE_PUBLIC_KEYS=${public_keys}" \
  -e "AF_LICENSE_KEY=${license_key}" \
  -e "AF_EE_SSO_KEY=${sso_key}"

# Deliberately not "no keys and no licence". The keys ARE trusted here and
# there is simply no licence, which is the state a customer who has pulled this
# image and not yet pasted a key is in, and it is the state that must refuse
# rather than 404.
echo "== unlicensed enterprise"
start_plane "unlicensed-${run}" "$enterprise" 18082 \
  -e "AF_ORG=${org_slug}" \
  -e "AF_LICENSE_PUBLIC_KEYS=${public_keys}" \
  -e "AF_EE_SSO_KEY=${sso_key}"

echo "== community, the control"
start_plane "community-${run}" "$community" 18083

# --- the positive -----------------------------------------------------------

status=$(probe 18081 "/sso/saml/${handle}/metadata")
[ "$status" = "200" ] || fail "the licensed image answered ${status} for SAML metadata, want 200"
grep -q 'EntityDescriptor' /tmp/"${run}".body \
  || fail "the licensed 200 is not a SAML metadata document: $(head -c 400 /tmp/"${run}".body)"
# The entity id is built from the base URL this container was configured with,
# so this is the assertion that the route ran inside THIS process with THIS
# configuration rather than that something somewhere answered 200.
grep -q "${base_url}/sso/saml/${handle}/acs" /tmp/"${run}".body \
  || fail "the assertion consumer URL is not this container's own: $(head -c 400 /tmp/"${run}".body)"
echo "ok: the licensed image answers SAML metadata from ee/web/sso"

status=$(probe 18081 "/scim/v2/ServiceProviderConfig")
[ "$status" = "200" ] || fail "the licensed image answered ${status} for SCIM, want 200"
grep -q 'ServiceProviderConfig' /tmp/"${run}".body \
  || fail "the licensed 200 is not a SCIM ServiceProviderConfig: $(head -c 400 /tmp/"${run}".body)"
echo "ok: the licensed image answers SCIM from ee/web/scim"

# The startup lines are part of the claim, not decoration. A community log and
# an enterprise log whose registration silently failed are identical documents,
# which is how four packages went unmounted with no evidence anywhere.
said=$(docker logs "licensed-${run}" 2>&1)
grep -q 'extension sso is mounted' <<<"$said" || fail "the licensed image never said it mounted sso"
grep -q 'extension scim is mounted' <<<"$said" || fail "the licensed image never said it mounted scim"
grep -q 'enterprise features permitted right now: scim, sso' <<<"$said" \
  || fail "the licensed image did not report scim and sso as permitted"
echo "ok: the licensed image says what it mounted and what the licence permits"

# --- the negative, and that it is a refusal --------------------------------

status=$(probe 18082 "/sso/saml/${handle}/metadata")
[ "$status" = "402" ] || fail \
  "the unlicensed image answered ${status}. 404 would mean the route was never mounted, which is
  refusal by absence and is the thing the gate exists not to do."
grep -q '"error":"not_licensed"' /tmp/"${run}".body \
  || fail "the 402 is not the licence gate's refusal: $(head -c 400 /tmp/"${run}".body)"
grep -q '"feature":"sso"' /tmp/"${run}".body \
  || fail "the refusal does not name sso: $(head -c 400 /tmp/"${run}".body)"
# The sentence has to name what to do. A refusal an operator cannot act on
# sends them to read the source.
grep -q 'AF_LICENSE_KEY' /tmp/"${run}".body \
  || fail "the refusal does not say how to fix it: $(head -c 400 /tmp/"${run}".body)"
echo "ok: the unlicensed image refuses sso with 402 and names the variable"

# Per feature, not per edition. A gate refusing everything under one name would
# be indistinguishable from a licence check nobody wired per package, and would
# tell a customer who bought single sign-on and not provisioning the wrong
# thing.
status=$(probe 18082 "/scim/v2/ServiceProviderConfig")
[ "$status" = "402" ] || fail "the unlicensed image answered ${status} for SCIM, want 402"
grep -q '"feature":"scim"' /tmp/"${run}".body \
  || fail "the SCIM refusal does not name scim: $(head -c 400 /tmp/"${run}".body)"
echo "ok: the unlicensed image refuses scim under its own name"

# --- the control ------------------------------------------------------------

status=$(probe 18083 "/sso/saml/${handle}/metadata")
[ "$status" = "404" ] || fail \
  "the community image answered ${status} for an enterprise route, want 404. Anything else means
  the community image is carrying enterprise routes, which is an edition boundary failure."
echo "ok: the community image answers 404, which is what absence looks like"

status=$(probe 18083 "/scim/v2/ServiceProviderConfig")
[ "$status" = "404" ] || fail "the community image answered ${status} for SCIM, want 404"
echo "ok: the community image answers 404 for SCIM too"

# Both images have to be able to serve the ordinary product, or the comparison
# above is between two broken things.
for port in 18081 18083; do
  status=$(probe "$port" "/health")
  [ "$status" = "200" ] || fail "the control plane on ${port} answered ${status} for /health"
done
echo "ok: both images serve the control plane itself"

echo
echo "PASS: the enterprise image mounts the licensed products, refuses them without a licence,"
echo "PASS: and the community image built from the same tree mounts neither."
