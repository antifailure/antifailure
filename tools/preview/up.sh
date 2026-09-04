#!/usr/bin/env bash
# Bring up a browsable control plane with an operator portal you can sign into.
#
# WHAT THIS SOLVES. The console is a Next static export. Opened on its own it
# has no control plane to talk to and renders its error state, and the operator
# portal underneath cannot be reached at all because a fresh database has no
# operator and nothing in the product creates the first one. So there was no way
# to LOOK at any of these pages, which is a problem when the pixels are the
# deliverable.
#
# WHAT IT DOES. Starts its own Postgres, applies every migration, seeds real
# rows in real tables, seeds one local operator, builds the console and serves
# it from the control plane's own process so the two share an origin, then
# prints the address and the sign-in.
#
# THE ORIGIN IS THE WHOLE DESIGN. console/next.config.ts says it plainly: the
# session is a SameSite=Lax cookie on the control plane's origin, so a console
# served from a second port would need SameSite=None and credentialed CORS and
# the cookie simply would not ride the fetch. That is why this builds the export
# and points AF_CONSOLE_DIR at it rather than running `next dev` on 3100.
#
# THE OPERATOR PASSWORD IS GENERATED, never stored in this repository. A file in
# the tree that creates a credential IS a default credential however plainly it
# is labelled, and this product ships none anywhere. So it is made from a
# cryptographic random source on every run, printed once here, and written only
# under a state directory outside the working tree for shots.sh to read. The two
# Postgres role passwords are made the same way for the same reason.
#
# WHAT IT WILL NOT DO. Touch a database that is not on this machine, or remove a
# container it did not create. Both are checked rather than assumed.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=tools/preview/common.sh
source "$here/common.sh"
root="$preview_root"
container="$preview_container"
db_port="$preview_db_port"
port="$preview_port"
host="$preview_host"
state="$preview_state"
log="$preview_log"
pidfile="$preview_pidfile"

preview_refuse_remote
mkdir -p "$state"
chmod 700 "$state"

say() { printf '%s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }

# ---------------------------------------------------------------------------
# Postgres
# ---------------------------------------------------------------------------
step "database"
# The container's own superuser password, generated when the container is
# created and kept beside it in the state directory. Kept rather than
# regenerated because POSTGRES_PASSWORD is only read at initialisation: a new
# value on a second run would simply be wrong. If the file is gone and the
# container is one this harness made, the container is rebuilt rather than
# guessed at.
pgpass_file="$state/postgres-password"
if docker inspect "$container" >/dev/null 2>&1 && [ ! -f "$pgpass_file" ]; then
  if [ "$(docker inspect -f '{{index .Config.Labels "af-preview"}}' "$container")" = "1" ]; then
    say "  $container exists but its password is not on disk, rebuilding it"
    docker rm -f "$container" >/dev/null
  else
    echo "  $container exists, was not created by this harness, and its password is unknown." >&2
    echo "  Set AF_PREVIEW_DB_CONTAINER to a name this harness may own." >&2
    exit 1
  fi
fi

if docker inspect "$container" >/dev/null 2>&1; then
  if [ "$(docker inspect -f '{{.State.Running}}' "$container")" != "true" ]; then
    docker start "$container" >/dev/null
    say "  started the existing $container"
  else
    say "  reusing $container, already running"
  fi
else
  ( umask 077; preview_newsecret >"$pgpass_file" )
  # Labelled on creation so tools/preview/down.sh can tell a container it made
  # from one somebody else is depending on, and refuse to remove the second.
  # The password reaches docker on stdin rather than on the command line, so it
  # is not in the process table for the life of the run.
  docker run -d \
    --name "$container" \
    --label af-preview=1 \
    -p "$db_port:5432" \
    --env-file /dev/stdin \
    -e POSTGRES_DB=antifailure \
    postgres:17-alpine >/dev/null <<ENVFILE
POSTGRES_PASSWORD=$(cat "$pgpass_file")
ENVFILE
  say "  created $container on $db_port"
fi
pgpass="$(cat "$pgpass_file")"

say "  waiting for it to accept a query"
ready=""
for _ in $(seq 1 90); do
  if docker exec -e PGPASSWORD="$pgpass" "$container" psql -U postgres -d antifailure -tAc 'select 1' >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
[ -n "$ready" ] || { echo "  $container never accepted a query" >&2; exit 1; }

export AF_PREVIEW_DATABASE_URL="postgres://postgres:$pgpass@127.0.0.1:$db_port/antifailure"

# ---------------------------------------------------------------------------
# Dependencies. Installed rather than assumed, because a worktree cut from a
# branch has no node_modules and the failure otherwise is a stack trace naming
# a package rather than a sentence naming the fix.
# ---------------------------------------------------------------------------
step "dependencies"
if [ -d "$root/web/node_modules/postgres" ]; then
  say "  web workspace already installed"
else
  say "  installing the web workspace"
  npm --prefix "$root/web" ci >/dev/null
fi
if [ -d "$root/console/node_modules/next" ]; then
  say "  console already installed"
else
  say "  installing the console"
  npm --prefix "$root/console" ci >/dev/null
fi

# ---------------------------------------------------------------------------
# Schema and rows
# ---------------------------------------------------------------------------
step "seed"
# Generated here, used by the seeder and by the control plane below, and never
# written into the working tree. The operator's copy goes to a file outside the
# repository so shots.sh can sign in without being handed it by hand.
app_password="$(preview_newsecret)"
admin_password="$(preview_newsecret)"
operator_password="$(preview_newsecret)"
( umask 077; printf '%s' "$operator_password" >"$state/operator-password" )

AF_PREVIEW_APP_PASSWORD="$app_password" \
AF_PREVIEW_ADMIN_PASSWORD="$admin_password" \
AF_PREVIEW_OPERATOR_EMAIL="$preview_operator_email" \
AF_PREVIEW_OPERATOR_PASSWORD="$operator_password" \
  node "$here/seed.ts"

# ---------------------------------------------------------------------------
# The console's static export
# ---------------------------------------------------------------------------
step "console"
if [ "${AF_PREVIEW_SKIP_BUILD:-}" = "1" ] && [ -f "$root/console/out/index.html" ]; then
  say "  keeping the existing export, AF_PREVIEW_SKIP_BUILD is set"
else
  say "  building the static export, which takes a minute"
  ( cd "$root/console" && npm run build >"$state/console-build.log" 2>&1 ) || {
    echo "  the console build failed. Its output is in $state/console-build.log" >&2
    tail -30 "$state/console-build.log" >&2
    exit 1
  }
  say "  built $(find "$root/console/out" -name '*.html' | wc -l | tr -d ' ') pages"
fi

# ---------------------------------------------------------------------------
# The control plane
# ---------------------------------------------------------------------------
step "control plane"
if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
  say "  stopping the one this harness started earlier"
  kill "$(cat "$pidfile")" 2>/dev/null || true
  sleep 1
fi

# Anything still on the port is refused HERE rather than discovered later.
#
# Without this the failure is genuinely misleading: the old server answers
# /health, so the readiness loop passes, and the run then fails on the sign in
# with a 500, because the process that is listening was seeded with a different
# generated password. That reads as a broken credential and is a stale process.
# Watched happening once, after a pid file was lost.
if lsof -nP -iTCP:"$port" -sTCP:LISTEN -t >/dev/null 2>&1; then
  echo "  something is already listening on $port, and it is not one this harness is tracking." >&2
  echo "  Stop it, or set AF_PREVIEW_PORT to a free port." >&2
  lsof -nP -iTCP:"$port" -sTCP:LISTEN >&2
  exit 1
fi

# AF_INSECURE_COOKIES is what makes the session usable over plain http. Without
# it the operator cookie is written as `__Host-af_admin_session`, which a
# browser refuses to store on an insecure origin: sign-in answers 200, sets a
# cookie nothing keeps, and every page after it is anonymous again.
#
# Exported into a subshell rather than passed as `env VAR=... node`, so the two
# connection strings, which carry the generated role passwords, are in the
# process environment and not in its command line.
(
  export AF_DATABASE_URL="postgres://antifailure_app:$app_password@127.0.0.1:$db_port/antifailure"
  export AF_ADMIN_DATABASE_URL="postgres://antifailure_admin:$admin_password@127.0.0.1:$db_port/antifailure"
  export AF_MAINTENANCE_DATABASE_URL="$AF_PREVIEW_DATABASE_URL"
  export AF_PORT="$port"
  export AF_APP_BASE_URL="http://$host:$port"
  export AF_CONSOLE_DIR="$root/console/out"
  export AF_INSECURE_COOKIES=1
  export AF_GITHUB_CLIENT_ID=preview-local-client
  export AF_GITHUB_CLIENT_SECRET=preview-local-secret
  export AF_GITHUB_REDIRECT_URI="http://$host:$port/auth/callback"
  exec node "$root/web/apps/api/src/main.ts" >"$log" 2>&1
) &
echo $! >"$pidfile"

say "  waiting for it to answer"
up=""
for _ in $(seq 1 60); do
  if curl -fsS "http://$host:$port/health" >/dev/null 2>&1; then
    up=1
    break
  fi
  if ! kill -0 "$(cat "$pidfile")" 2>/dev/null; then
    echo "  the control plane exited. Its output:" >&2
    tail -30 "$log" >&2
    exit 1
  fi
  sleep 1
done
[ -n "$up" ] || { echo "  it never answered. Its output is in $log" >&2; tail -30 "$log" >&2; exit 1; }

# The one assertion that separates "a server is listening" from "the portal
# works". A running process that serves the sign-in page forever is exactly what
# this harness existed to stop shipping, so the credential is exercised here
# rather than left for whoever opens the browser.
#
# The body goes in on stdin. On the command line it would be in the process
# table for as long as the request took.
cookie="$(printf '{"email":"%s","password":"%s"}' "$preview_operator_email" "$operator_password" \
  | curl -fsS -D - -o /dev/null \
    -X POST "http://$host:$port/v1/admin/signin" \
    -H 'content-type: application/json' \
    --data-binary @- \
  | awk 'tolower($1) == "set-cookie:" { print $2 }' | tr -d '\r;')" || cookie=""
if [ -z "$cookie" ]; then
  echo "  the seeded operator could not sign in. Its output is in $log" >&2
  exit 1
fi
say "  the seeded operator signs in and the portal answers"

# Written down so tools/preview/shots.sh finds this checkout's preview without
# being handed a port. Two lanes running at once is the normal case here.
printf '%s\n' "http://$host:$port" >"$state/url"

cat <<BANNER

The console is up.

  console        http://$host:$port/
  portal         http://$host:$port/admin
  api            http://$host:$port/v1
  database       $container on $db_port
  state          $state
  logs           $log

Sign in to the portal at http://$host:$port/admin with

  email          $preview_operator_email
  password       $operator_password

That password was generated for this run and is printed here once. It is not
written anywhere in the repository: a copy for tools/preview/shots.sh sits in
$state, which is outside the working tree. Running up.sh again generates a new
one. Both scripts refuse a database that is not on localhost.

  screenshots    tools/preview/shots.sh
  stop           tools/preview/down.sh
BANNER
