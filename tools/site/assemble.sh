#!/usr/bin/env bash
#
# One assembly of the published site, used by CI and by Deploy.
#
# It exists because those two steps were copy-pasted and then drifted, in the
# way copies always do: deploy.yml learned to handle both shapes of the Astro
# output and CI never did, and NEITHER of them ever copied the two addresses
# the product hard-codes into things people have already installed.
#
#   - README.md tells a reader to pipe https://antifailure.dev/install.sh
#     into sh.
#   - Every antifailure.yaml names
#     https://antifailure.dev/schemas/manifest.v1.json as its $schema, and
#     every event the engine emits carries
#     https://antifailure.dev/schemas/events.v1.json as its $id.
#
# Neither site build produces those files. The live site serves the installer
# and the manifest schema only because somebody once assembled the tree by hand
# and pushed it with the SWA CLI, and it serves events.v1.json not at all: that
# address is a 404 today, in every event envelope the engine has ever written.
# A workflow that publishes what the builds produce, and nothing else, would
# have taken the installer down with it.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

test -d www/out || {
  echo "www did not produce out/; next.config output: export may have changed"
  exit 1
}
test -d docs/dist || { echo "docs did not produce dist/"; exit 1; }

rm -rf site && mkdir -p site

# The marketing site at the root.
cp -R www/out/. site/

# The documentation under /docs. astro.config.mjs sets base: "/docs", so dist
# usually already contains docs/. Copy whichever shape it produced rather than
# assuming one.
if [ -d docs/dist/docs ]; then
  cp -R docs/dist/docs site/docs
else
  mkdir -p site/docs && cp -R docs/dist/. site/docs/
fi

# The addresses that live outside both builds.
cp install.sh site/install.sh
mkdir -p site/schemas && cp schemas/*.json site/schemas/

# Assert the promises, rather than trusting the copies above. Each of these is
# an address something already shipped points at.
for required in \
  index.html \
  install.sh \
  schemas/manifest.v1.json \
  schemas/events.v1.json
do
  test -f "site/$required" || { echo "the assembled site is missing /$required"; exit 1; }
done
test -d site/docs || { echo "no /docs in the assembled site"; exit 1; }

echo "assembled $(find site -type f | wc -l | tr -d ' ') files"
