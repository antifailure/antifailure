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

# The Static Web Apps configuration: the site's own file plus what only this
# script can know.
#
# This step used to OVERWRITE www/public/staticwebapp.config.json, which the
# static export copies to site/staticwebapp.config.json a few lines above, and
# nobody noticed because the result is a valid config that serves the site.
# Everything that file said was silently dropped from every deploy: the
# two year HSTS max-age with preload, the permissions policy, the cross origin
# opener policy, the immutable cache headers on hashed assets, the content
# types for the text files, trailingSlash, and a 301 for a renamed product
# page. antifailure.dev was serving the platform's default 126 day HSTS and no
# permissions policy at all, and /product/crowdi answered 200 instead of
# redirecting. Generated wins over written is the wrong way round: the file is
# now the base and the generated parts are merged onto it.
#
# What is generated, and why it cannot live in that file:
#
# A 404 served Microsoft's page, their logo, their wording, a Bootstrap CDN
# stylesheet, while www/app/not-found.tsx sat in the build at /404.html with
# nothing pointing at it. A designed page nothing routes to is the same dead
# capability as a function with no callers, and it looked like our site had
# been abandoned to a hosting default.
#
# And every page answered on two addresses: /pricing and /pricing.html both
# returned 200, because a static export writes files and SWA additionally
# resolves the extensionless form. Two URLs for one page is a duplicate for a
# crawler and a .html in the address bar for anyone who ever lands on the file
# form. Each page therefore redirects its file form to its clean form,
# generated from what the build actually produced so a page added later is
# covered without anybody remembering this file exists. The docs tree is left
# alone on purpose: Astro serves it from directory indexes with its own
# trailing-slash convention, and rewriting that from here would be guessing at
# another build's contract.
#
# apiRuntime is here rather than in www/public because it is a fact about this
# deploy rather than about the site. deploy.yml passes api_location with
# skip_api_build, and the platform then needs to be told which runtime to start
# the managed function on; the two have to agree, and they are easier to keep
# agreeing when they are in the repository's two deploy files rather than one
# deploy file and one asset folder.
test -f site/staticwebapp.config.json || {
  echo "www/public/staticwebapp.config.json did not reach site/; the export changed"
  exit 1
}

pages="$(mktemp)"
trap 'rm -f "$pages"' EXIT
find site -maxdepth 3 -name '*.html' -not -path 'site/docs/*' -not -name '404.html' \
  | sed 's|^site||' | sort > "$pages"

PAGES="$pages" python3 - <<'PY'
import json
import os
import pathlib

config_path = pathlib.Path("site/staticwebapp.config.json")
config = json.loads(config_path.read_text())

redirects = []
for page in pathlib.Path(os.environ["PAGES"]).read_text().splitlines():
    if not page:
        continue
    clean = page[: -len(".html")]
    redirects.append({"route": page, "redirect": "/" if clean == "/index" else clean, "statusCode": 301})
if not redirects:
    raise SystemExit("no pages found to redirect; the export layout changed")

# The site's own routes first. They are specific and hand written, and SWA
# takes the first match.
config["routes"] = config.get("routes", []) + redirects

# The site's file wins wherever both have an opinion. These are the entries it
# has no reason to carry: /install.sh and /schemas/*.json are copied in here.
config.setdefault("responseOverrides", {}).setdefault(
    "404", {"rewrite": "/404.html", "statusCode": 404}
)
config.setdefault("globalHeaders", {})
for header, value in {
    "x-content-type-options": "nosniff",
    "referrer-policy": "strict-origin-when-cross-origin",
}.items():
    config["globalHeaders"].setdefault(header, value)
config.setdefault("mimeTypes", {})
for suffix, value in {".json": "application/json", ".sh": "text/plain"}.items():
    config["mimeTypes"].setdefault(suffix, value)

# node:20 rather than node:22, which is also supported: 20 is the version every
# current Static Web Apps document agrees on, and an apiRuntime the platform
# does not know is a failed deploy rather than a warning.
#
# If somebody later declares one in www/public/staticwebapp.config.json, which
# is where a reader would look for it, say so instead of quietly winning. A
# value that is load-bearing and lives somewhere other than where it is looked
# for is how the next person spends an afternoon.
declared = config.get("platform", {}).get("apiRuntime")
if declared is not None and declared != "node:20":
    raise SystemExit(
        f"www/public/staticwebapp.config.json sets platform.apiRuntime to {declared!r} and "
        "tools/site/assemble.sh sets it to 'node:20'. Pick one and delete the other; the "
        "runtime has to agree with api_location in .github/workflows/deploy.yml."
    )
config.setdefault("platform", {})["apiRuntime"] = "node:20"

config_path.write_text(json.dumps(config, indent=2) + "\n")
PY

python3 -c 'import json,sys; json.load(open("site/staticwebapp.config.json"))' || {
  echo "the assembled staticwebapp.config.json is not valid JSON"
  exit 1
}

# The parts a person would notice missing, asserted rather than assumed,
# because the failure this replaces was silent for as long as the site existed.
python3 - <<'PY'
import json
import sys

config = json.load(open("site/staticwebapp.config.json"))
problems = []
headers = config.get("globalHeaders", {})
if "strict-transport-security" not in headers:
    problems.append("globalHeaders lost strict-transport-security")
if config.get("platform", {}).get("apiRuntime") != "node:20":
    problems.append("platform.apiRuntime is not node:20, so the managed function will not start")
if not any(r.get("route", "").endswith(".html") for r in config.get("routes", [])):
    problems.append("no page redirects its .html form to its clean form")
if config.get("responseOverrides", {}).get("404", {}).get("rewrite") != "/404.html":
    problems.append("a 404 would serve the platform's page instead of ours")
for problem in problems:
    print(problem)
sys.exit(1 if problems else 0)
PY

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
