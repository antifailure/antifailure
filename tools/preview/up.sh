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
# WHAT IT WILL NOT DO. Touch a database that is not on this machine, or remove a
# container it did not create. Both are checked rather than assumed.

set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"

# A database and a port PER CHECKOUT, derived from its path.
#
# Not the af-cp-test that `just db` runs, and not one fixed name either. Six
# lanes are building this portal in six worktrees at once, and both alternatives
# break in the same way: a shared database collects every branch's unmerged
# migrations, so the second lane to run this applies a migration numbered the
# same as the first lane's and different from it, and the failure reads as a
# bug in the schema rather than as two branches meeting. Deriving the name from
# the path gives each worktree its own, with no coordination and nothing to
# remember. The name and the port are printed at the end, and both can be
# overridden.
slug="$(printf '%s' "$root" | shasum | cut -c1-8)"
offset="$(( 16#${slug:0:3} % 300 ))"
container="${AF_PREVIEW_DB_CONTAINER:-af-preview-$slug}"
db_port="${AF_PREVIEW_DB_PORT:-$(( 55450 + offset ))}"
port="${AF_PREVIEW_PORT:-$(( 8100 + offset ))}"
host="${AF_PREVIEW_HOST:-127.0.0.1}"

state="$root/.preview"
mkdir -p "$state"
log="$state/control-plane.log"
pidfile="$state/control-plane.pid"

case "$host" in
  127.0.0.1|localhost|::1|0.0.0.0) ;;
  *)
    echo "AF_PREVIEW_HOST is $host. This harness seeds a published password and runs on localhost only." >&2
    exit 2
    ;;
esac

say() { printf '%s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }

# ---------------------------------------------------------------------------
# Postgres
# ---------------------------------------------------------------------------
step "database"
if docker inspect "$container" >/dev/null 2>&1; then
  if [ "$(docker inspect -f '{{.State.Running}}' "$container")" != "true" ]; then
    docker start "$container" >/dev/null
    say "  started the existing $container"
  else
    say "  reusing $container, already running"
  fi
else
  # Labelled on creation so tools/preview/down.sh can tell a container it made
  # from one somebody else is depending on, and refuse to remove the second.
  docker run -d \
    --name "$container" \
    --label af-preview=1 \
    -p "$db_port:5432" \
    -e POSTGRES_PASSWORD=test \
    -e POSTGRES_DB=antifailure \
    postgres:17-alpine >/dev/null
  say "  created $container on $db_port"
fi

say "  waiting for it to accept a query"
ready=""
for _ in $(seq 1 90); do
  if docker exec "$container" psql -U postgres -d antifailure -tAc 'select 1' >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
[ -n "$ready" ] || { echo "  $container never accepted a query" >&2; exit 1; }

export AF_PREVIEW_DATABASE_URL="postgres://postgres:test@127.0.0.1:$db_port/antifailure"

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

# AF_INSECURE_COOKIES is what makes the session usable over plain http. Without
# it the operator cookie is written as `__Host-af_admin_session`, which a
# browser refuses to store on an insecure origin: sign-in answers 200, sets a
# cookie nothing keeps, and every page after it is anonymous again.
env \
  AF_DATABASE_URL="postgres://antifailure_app:app-test-password@127.0.0.1:$db_port/antifailure" \
  AF_ADMIN_DATABASE_URL="postgres://antifailure_admin:admin-test-password@127.0.0.1:$db_port/antifailure" \
  AF_MAINTENANCE_DATABASE_URL="$AF_PREVIEW_DATABASE_URL" \
  AF_PORT="$port" \
  AF_APP_BASE_URL="http://$host:$port" \
  AF_CONSOLE_DIR="$root/console/out" \
  AF_INSECURE_COOKIES=1 \
  AF_GITHUB_CLIENT_ID=preview-local-client \
  AF_GITHUB_CLIENT_SECRET=preview-local-secret \
  AF_GITHUB_REDIRECT_URI="http://$host:$port/auth/callback" \
  node "$root/web/apps/api/src/main.ts" >"$log" 2>&1 &
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
cookie="$(curl -fsS -D - -o /dev/null \
  -X POST "http://$host:$port/v1/admin/signin" \
  -H 'content-type: application/json' \
  -d '{"email":"operator@preview.local","password":"preview-only-not-a-secret"}' \
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
  logs           $log

Sign in to the portal at http://$host:$port/admin with

  email          operator@preview.local
  password       preview-only-not-a-secret

That credential is seeded by this script, is written down in it, and is for
this machine only. Both the script and the seeder refuse a database that is
not on localhost.

  screenshots    tools/preview/shots.sh
  stop           tools/preview/down.sh
BANNER
