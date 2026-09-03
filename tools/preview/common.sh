# Where this checkout's preview lives, and how its secrets are made.
#
# Sourced by up.sh, shots.sh and down.sh so the three agree without any of them
# repeating a hash or a port number.
#
# THE STATE DIRECTORY IS OUTSIDE THE REPOSITORY, deliberately. It holds a
# generated operator password, and a file inside the tree that carries a
# credential is a default credential however plainly it is labelled. This
# product ships none, and a harness that plants one is the shape of thing it
# refuses. Nothing this harness writes lands in the working tree, so there is
# nothing to gitignore and nothing for a stray `git add -A` to pick up.

preview_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# A database and a port PER CHECKOUT, derived from its path.
#
# Not the af-cp-test that `just db` runs, and not one fixed name either. Six
# lanes build this portal in six worktrees at once, and both alternatives break
# in the same way: a shared database collects every branch's unmerged
# migrations, so the second lane to run this applies a migration numbered the
# same as the first lane's and different from it, and the failure reads as a bug
# in the schema rather than as two branches meeting.
preview_slug="$(printf '%s' "$preview_root" | shasum | cut -c1-8)"
preview_offset="$(( 16#${preview_slug:0:3} % 300 ))"

preview_container="${AF_PREVIEW_DB_CONTAINER:-af-preview-$preview_slug}"
preview_db_port="${AF_PREVIEW_DB_PORT:-$(( 55450 + preview_offset ))}"
preview_port="${AF_PREVIEW_PORT:-$(( 8100 + preview_offset ))}"
preview_host="${AF_PREVIEW_HOST:-127.0.0.1}"

preview_state="${AF_PREVIEW_STATE:-${TMPDIR:-/tmp}/af-preview-$preview_slug}"
preview_log="$preview_state/control-plane.log"
preview_pidfile="$preview_state/control-plane.pid"
preview_operator_email="operator@preview.local"

# Fresh every run, from the same source the product's own tokens come from.
# Printed once by up.sh and never written into the repository.
preview_newsecret() {
  node -e 'process.stdout.write(require("node:crypto").randomBytes(24).toString("base64url"))'
}

# Refuses anything but this machine.
#
# Kept after the credential stopped being committed, because it is still worth
# having: this seeds a database by truncating every tenant table. It is simply
# no longer the thing carrying the argument.
preview_refuse_remote() {
  case "$preview_host" in
    127.0.0.1|localhost|::1|0.0.0.0) ;;
    *)
      echo "AF_PREVIEW_HOST is $preview_host. This harness runs against localhost only." >&2
      exit 2
      ;;
  esac
}
