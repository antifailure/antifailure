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

# The Static Web Apps configuration, generated rather than written.
#
# Two things it fixes, both of which were live for as long as the site was.
#
# A 404 served Microsoft's page -- their logo, their wording, a Bootstrap CDN
# stylesheet -- while www/app/not-found.tsx sat in the build at /404.html with
# nothing pointing at it. A designed page nothing routes to is the same dead
# capability as a function with no callers, and it looked like our site had
# been abandoned to a hosting default.
#
# And every page answered on two addresses: /pricing and /pricing.html both
# returned 200, because a static export writes files and SWA additionally
# resolves the extensionless form. Two URLs for one page is a duplicate for a
# crawler and a .html in the address bar for anyone who ever lands on the file
# form. Each page therefore redirects its file form to its clean form.
#
# Generated from what the build actually produced, so a page added later is
# covered without anybody remembering this file exists. The docs tree is left
# alone on purpose: Astro serves it from directory indexes with its own
# trailing-slash convention, and rewriting that from here would be guessing at
# another build's contract.
{
  echo '{'
  echo '  "routes": ['
  first=1
  while IFS= read -r page; do
    clean="${page%.html}"
    [ "$clean" = "/index" ] && clean="/"
    [ $first -eq 1 ] || echo ','
    first=0
    printf '    { "route": "%s", "redirect": "%s", "statusCode": 301 }' "$page" "$clean"
  done < <(cd site && find . -maxdepth 3 -name '*.html' -not -path './docs/*' -not -name '404.html' | sed 's|^\.||' | sort)
  echo ''
  echo '  ],'
  echo '  "responseOverrides": {'
  echo '    "404": { "rewrite": "/404.html", "statusCode": 404 }'
  echo '  },'
  echo '  "globalHeaders": {'
  echo '    "x-content-type-options": "nosniff",'
  echo '    "referrer-policy": "strict-origin-when-cross-origin"'
  echo '  },'
  echo '  "mimeTypes": {'
  echo '    ".json": "application/json",'
  echo '    ".sh": "text/plain"'
  echo '  }'
  echo '}'
} > site/staticwebapp.config.json

python3 -c 'import json,sys; json.load(open("site/staticwebapp.config.json"))' || {
  echo "the generated staticwebapp.config.json is not valid JSON"
  exit 1
}

# Assert the promises, rather than trusting the copies above. Each of these is
# an address something already shipped points at.
for required in \
  index.html \
  404.html \
  staticwebapp.config.json \
  install.sh \
  schemas/manifest.v1.json \
  schemas/events.v1.json
do
  test -f "site/$required" || { echo "the assembled site is missing /$required"; exit 1; }
done
test -d site/docs || { echo "no /docs in the assembled site"; exit 1; }

echo "assembled $(find site -type f | wc -l | tr -d ' ') files"
