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
#
# Generated ON TOP OF www/public/staticwebapp.config.json rather than instead of
# it. That file is the site's host policy: the mime types for the markdown twins
# and the AVIF art, immutable caching on hashed assets, the legacy /product/crowdi
# redirect, and the security headers. It is copied into site/ with the rest of
# www/out and this block used to overwrite it, so every one of those directives
# had been dead in production since the day it was written. The file existed, the
# deploy shipped it, Azure read it, and none of it was the file anybody wrote.
#
# So the redirects below are appended to whatever that file declares, and the
# assertions afterwards fail the build if the merge loses something.
python3 - "$(pwd)/site" <<'MERGE'
import json, os, sys

root = sys.argv[1]
config = os.path.join(root, "staticwebapp.config.json")

with open(config, encoding="utf-8") as f:
    base = json.load(f)

pages = []
for dirpath, dirnames, filenames in os.walk(root):
    rel = os.path.relpath(dirpath, root)
    depth = 0 if rel == "." else len(rel.split(os.sep))
    if rel == "docs" or rel.startswith("docs" + os.sep):
        dirnames[:] = []
        continue
    if depth >= 3:
        dirnames[:] = []
    for name in filenames:
        if not name.endswith(".html") or name == "404.html":
            continue
        page = "/" + os.path.normpath(os.path.join(rel, name)).replace(os.sep, "/")
        page = page.replace("/./", "/")
        pages.append(page)

generated = []
for page in sorted(set(pages)):
    clean = page[: -len(".html")]
    if clean == "/index":
        clean = "/"
    generated.append({"route": page, "redirect": clean, "statusCode": 301})

# The file-form redirects go first: they are exact matches on a specific page and
# nothing in the base file wants to answer for a .html address.
base["routes"] = generated + base.get("routes", [])

with open(config, "w", encoding="utf-8") as f:
    json.dump(base, f, indent=2)
    f.write("\n")
MERGE

python3 -c 'import json,sys; json.load(open("site/staticwebapp.config.json"))' || {
  echo "the generated staticwebapp.config.json is not valid JSON"
  exit 1
}

# The merge has to have kept the source file's own declarations. Asserted rather
# than assumed, because the failure mode is silent: a config that is present,
# valid, and missing half of what somebody wrote in it looks exactly like a
# working one until you read a response header.
python3 - <<'ASSERT' || exit 1
import json, sys

with open("www/public/staticwebapp.config.json", encoding="utf-8") as f:
    source = json.load(f)
with open("site/staticwebapp.config.json", encoding="utf-8") as f:
    shipped = json.load(f)

lost = []
for key in ("mimeTypes", "globalHeaders"):
    for name, value in source.get(key, {}).items():
        if shipped.get(key, {}).get(name) != value:
            lost.append(f"{key}.{name}")

source_routes = {(r.get("route"), json.dumps(r, sort_keys=True)) for r in source.get("routes", [])}
shipped_routes = {(r.get("route"), json.dumps(r, sort_keys=True)) for r in shipped.get("routes", [])}
lost += [route for route, _ in source_routes - shipped_routes]

if lost:
    print("the assembled host config lost declarations from")
    print("www/public/staticwebapp.config.json: " + ", ".join(sorted(lost)))
    sys.exit(1)

print(f"host config: {len(shipped['routes'])} routes, "
      f"{len(shipped.get('mimeTypes', {}))} mime types, "
      f"{len(shipped.get('globalHeaders', {}))} global headers")
ASSERT

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
