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

# Next emits the not-found route twice, and only one of them is wired.
#
# app/not-found.tsx builds to /404.html, which is what a mistyped URL actually
# serves: responseOverrides below rewrites every 404 to it. The App Router also
# writes the same render to /_not-found.html with its .txt payload, and nothing
# routes there, nothing links there, and it is in no sitemap. What shipped was
# an 84KB second copy of the 404 page answering 200 at an address only a
# crawler guessing at framework internals would find.
#
# Removed here rather than in next.config, because the export has no option for
# it, and before the merge below so no file-form redirect is generated for a
# page that is no longer in the tree. Verified rather than assumed, because the
# risk was that the client router fetches it: with these files gone, /404.html
# renders and a client-side navigation off it into /product completes, with no
# request for _not-found and no console error. The only two references to the
# name anywhere in the build are a segment name inside 404.html's own inline
# payload and a string constant in the router chunk, neither of which is a URL.
rm -rf site/_not-found.html site/_not-found.txt site/_not-found

# The documentation under /docs. astro.config.mjs sets base: "/docs", so dist
# usually already contains docs/. Copy whichever shape it produced rather than
# assuming one.
if [ -d docs/dist/docs ]; then
  cp -R docs/dist/docs site/docs
else
  mkdir -p site/docs && cp -R docs/dist/. site/docs/
fi

# The documentation's sitemap, canonical, OpenGraph URL and TechArticle used
# to agree on a trailing slash that the live host immediately removed with a
# 301. Parse the assembled output and require one identity that is also the URL
# the sitemap publishes. Presence-only checks cannot see that mismatch.
python3 - <<'DOCIDENTITY' || exit 1
import glob, json, os, sys
import xml.etree.ElementTree as ET
from html.parser import HTMLParser


class Head(HTMLParser):
    def __init__(self):
        super().__init__()
        self.canonical = None
        self.og_url = None
        self.in_json = False
        self.json_text = []
        self.json_blocks = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "link" and attrs.get("rel") == "canonical":
            self.canonical = attrs.get("href")
        if tag == "meta" and attrs.get("property") == "og:url":
            self.og_url = attrs.get("content")
        if tag == "script" and attrs.get("type") == "application/ld+json":
            self.in_json = True
            self.json_text = []

    def handle_data(self, data):
        if self.in_json:
            self.json_text.append(data)

    def handle_endtag(self, tag):
        if tag == "script" and self.in_json:
            self.in_json = False
            self.json_blocks.append("".join(self.json_text))


problems = []
canonicals = set()
for filename in glob.glob("site/docs/**/*.html", recursive=True):
    if filename.endswith("/404.html"):
        continue
    parser = Head()
    with open(filename, encoding="utf-8") as f:
        parser.feed(f.read())
    if not parser.canonical:
        problems.append(f"{filename}: no canonical")
        continue
    canonicals.add(parser.canonical)
    if parser.canonical.endswith("/"):
        problems.append(f"{filename}: canonical redirects at the live host: {parser.canonical}")
    if parser.og_url != parser.canonical:
        problems.append(f"{filename}: og:url {parser.og_url!r} != canonical {parser.canonical!r}")
    articles = []
    for raw in parser.json_blocks:
        try:
            value = json.loads(raw)
        except json.JSONDecodeError as err:
            problems.append(f"{filename}: JSON-LD does not parse: {err}")
            continue
        nodes = value.get("@graph", []) if isinstance(value, dict) else []
        nodes = nodes if nodes else [value]
        articles.extend(node for node in nodes if isinstance(node, dict) and node.get("@type") == "TechArticle")
    if not any(
        article.get("url") == parser.canonical
        and article.get("@id") == parser.canonical + "#techarticle"
        for article in articles
    ):
        problems.append(f"{filename}: no TechArticle matching {parser.canonical}")

sitemap = ET.parse("site/docs/sitemap-0.xml")
published = {node.text for node in sitemap.iter() if node.tag.endswith("loc")}
if canonicals != published:
    problems.append(
        "documentation sitemap and page canonicals differ: "
        f"not published={sorted(canonicals - published)}, no page={sorted(published - canonicals)}"
    )

if problems:
    print("documentation identity is inconsistent:")
    for problem in problems:
        print("  " + problem)
    sys.exit(1)
print(f"documentation identity: {len(canonicals)} pages match their sitemap and TechArticle")
DOCIDENTITY

# The addresses that live outside both builds.
cp install.sh site/install.sh
mkdir -p site/schemas && cp schemas/*.json site/schemas/

# The Static Web Apps configuration, generated rather than written.
#
# Two things it fixes, both of which were live for as long as the site was.
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

# The runtime the managed function starts on, in one place because it is read by
# two separate python blocks below and a value that lives in five literals is a
# value somebody will change in four of them. The post-publish assertion reads
# this same variable, so it cannot end up guarding the version we stopped using.
#
# node:22 rather than node:20, measured rather than inferred: a staging
# environment of af-site deployed on this value and process.version came back
# v22.23.2. node:20 is what the documents agree on and it is also past upstream
# end of life since April 2026, so it is not the safer choice it looks like. An
# apiRuntime the platform does not recognise is a failed deploy rather than a
# warning, which is why this was measured before it was pinned.
#
# One discrepancy, checked rather than waved at, because it would matter if it
# were enforced. The schema www/public/staticwebapp.config.json names in its own
# $schema, schemastore's, stops at node:20 and does not know node:22. Nothing in
# this repository validates against it: the only two JSON Schema gates are
# manifestcheck, over antifailure manifests, and sbomcheck, over SPDX, and this
# file is only ever parsed for syntax. schemastore is a community-maintained
# convenience for editors that lags Microsoft's own runtime table, which lists
# node:22, and it is not what Azure enforces.
#
# It also cannot bite today, because the checked-in file has no platform key at
# all: this script writes the runtime into site/staticwebapp.config.json, which
# is generated and gitignored, so no file anybody opens in an editor contains
# this value. It would bite the moment somebody takes the invitation below and
# moves apiRuntime into www/public, and that is who this paragraph is for. Point
# $schema at a version that knows node:22, or drop the pointer; do not lower the
# runtime to satisfy an editor.
#
# The deploy log is the thing to believe either way: it prints
# "Function Runtime Information ... node version" on every publish, so what
# actually started is readable rather than assumed.
api_runtime="node:22"

API_RUNTIME="$api_runtime" python3 - "$(pwd)/site" <<'MERGE'
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

# apiRuntime is generated here rather than written in www/public because it is a
# fact about this deploy rather than about the site. deploy.yml passes
# api_location with skip_api_build, which makes the runtime required rather than
# optional: the platform will not start a managed function it has not been told
# how to run. The two settings have to agree, and they agree more reliably when
# both live in the repository's deploy files than when one lives in a deploy
# file and the other in an asset folder.
#
# The value comes from api_runtime in this script, above. See the reasoning
# there, including why it was measured rather than read off a table.
#
# If somebody later declares one in www/public/staticwebapp.config.json, which
# is where a reader would look for it, say so instead of quietly winning. A
# value that is load-bearing and lives somewhere other than where it is looked
# for is how the next person spends an afternoon.
runtime = os.environ["API_RUNTIME"]
declared = base.get("platform", {}).get("apiRuntime")
if declared is not None and declared != runtime:
    raise SystemExit(
        f"www/public/staticwebapp.config.json sets platform.apiRuntime to {declared!r} and "
        f"tools/site/assemble.sh sets it to {runtime!r}. Pick one and delete the other; the "
        "runtime has to agree with api_location in .github/workflows/deploy.yml."
    )
base.setdefault("platform", {})["apiRuntime"] = runtime

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
API_RUNTIME="$api_runtime" python3 - <<'ASSERT' || exit 1
import json, os, sys

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

# The generated half, asserted for the same reason as the copied half. Each of
# these was live on antifailure.dev as a defect: the API answered 500 on every
# path for two days because the platform was never told a runtime, a 404 served
# Microsoft's page, and every page answered on both its clean and its .html
# address. A config that is valid JSON and missing any of them looks exactly
# like a working one.
missing = []
runtime = os.environ["API_RUNTIME"]
if shipped.get("platform", {}).get("apiRuntime") != runtime:
    missing.append(f"platform.apiRuntime is not {runtime}, so the managed function will not start")
if not any(r.get("route", "").endswith(".html") for r in shipped.get("routes", [])):
    missing.append("no page redirects its .html form to its clean form")
if shipped.get("responseOverrides", {}).get("404", {}).get("rewrite") != "/404.html":
    missing.append("a 404 would serve the platform's page instead of ours")

if missing:
    for problem in missing:
        print(problem)
    sys.exit(1)

print(f"host config: {len(shipped['routes'])} routes, "
      f"{len(shipped.get('mimeTypes', {}))} mime types, "
      f"{len(shipped.get('globalHeaders', {}))} global headers")
ASSERT

# A page this build produced that nobody can open.
#
# The redirects live in one file and the pages whose addresses they claim are
# built from another, so nothing ever compared the two. Six retired product
# pages are shadowed that way today, deliberately: components/layout/MovedPage.tsx
# still answers /product/fidelity and the five beside it, because `output:
# "export"` has no server to evaluate a next.config redirect and a preview host
# serves the files without this route table. Correct for those six. For a
# seventh it would be a page that builds, deploys, passes every check on the
# site and cannot be reached, and nothing anywhere would say so. That is the
# same dead capability as a function with no callers, wearing a working page.
#
# Read from www/public/staticwebapp.config.json rather than from the merged
# result, so the file-form redirects generated above are excluded structurally
# instead of by pattern. Every one of those claims the address of a page it
# built, which is the entire point of them, and a pattern loose enough to skip
# them is a pattern loose enough to skip a real one.
#
# What separates a deliberate shadow from a broken page is that the page agrees
# with the host: a MovedPage stub writes location.replace(<target>) naming the
# same address the 301 names, and declares noindex so the two addresses are not
# one page twice to a crawler. A real page carries neither, and a stub whose
# target has drifted from the config's carries the wrong one.
python3 - <<'SHADOW' || exit 1
import json, os, sys

with open("www/public/staticwebapp.config.json", encoding="utf-8") as f:
    declared = json.load(f)["routes"]


def built(route):
    """The file the site would serve at this address, if it built one."""
    for candidate in (f"site{route}.html", f"site{route}/index.html"):
        if os.path.isfile(candidate):
            return candidate
    return None


problems = []
shadowed = 0
for rule in declared:
    route, target = rule.get("route", ""), rule.get("redirect")
    # A wildcard cannot be resolved to one page, and a header-only rule serves
    # the page rather than answering instead of it.
    if not target or "*" in route:
        continue
    page = built(route)
    if page is None:
        continue
    shadowed += 1
    with open(page, encoding="utf-8") as f:
        html = f.read()
    if f'location.replace("{target}")' not in html:
        problems.append(
            f"{route} is built as {page}, and this config answers that address "
            f"with a 301 to {target}, so nobody reaches the page. Either drop "
            f"the redirect, or make the page a MovedPage stub naming {target}."
        )
    elif 'name="robots" content="noindex' not in html:
        problems.append(
            f"{page} answers an address that 301s to {target} and is still "
            f"indexable, so a crawler has one page on two addresses. "
            f"movedMetadata sets the noindex this needs."
        )

if problems:
    print("a redirect claims the address of a page this build produced:")
    for problem in problems:
        print(f"  {problem}")
    sys.exit(1)

print(f"host config: {shadowed} redirects shadow a built page, every one a moved-page stub")
SHADOW

# One trailing-slash policy, enforced across both builds.
#
# The host decides this for the whole site: www/public/staticwebapp.config.json
# sets "trailingSlash", and Azure answers the other spelling with a 301. Each
# build then has its own idea. www/next.config.ts said `trailingSlash: false`
# and agreed. docs/astro.config.mjs said "ignore", and Astro's default under
# "ignore" is the slashed form, so all 82 documentation pages declared a
# canonical the host does not serve, the documentation sitemap offered 81 URLs
# that redirect, and the Docs link in the shared header 301'd on every page.
# Documentation is roughly seventy percent of this site, so the majority of it
# pointed engines at addresses it then had to be told again were elsewhere.
#
# Nothing could have caught it. www/scripts/check-seo.mjs reads www/out and has
# never opened docs/dist, and neither build can see the other's URLs or the
# host config that overrules them both. This script is the only step that has
# all three, which is why the assertion is here.
#
# The policy is READ from the config rather than written down again here. A
# check with its own copy of the answer is a check that passes after somebody
# changes the real one, and this file already carries that lesson twice.
#
# Fragments and query strings are cut before comparing: a browser drops them
# before it asks for anything, so /docs/reference/cli/#af-init is a request for
# /docs/reference/cli/ and redirects exactly like its bare form.
python3 - <<'SLASH' || exit 1
import glob, json, os, re, sys
from collections import Counter

with open("www/public/staticwebapp.config.json", encoding="utf-8") as f:
    policy = json.load(f).get("trailingSlash", "auto")

if policy not in ("never", "always"):
    print(f"host config: trailingSlash is {policy!r}, so no one spelling is enforced")
    sys.exit(0)

want_slash = policy == "always"


def disagrees(path):
    """Whether this site-relative path is spelled the way the host serves it."""
    path = path.split("#")[0].split("?")[0]
    # The root is "/" under either policy, and a file with an extension is a
    # file rather than a page: /og.png never grows a slash.
    if path in ("", "/") or os.path.splitext(path)[1]:
        return False
    return path.endswith("/") != want_slash


def local(url):
    """A site-relative path, or None for somebody else's host."""
    if url.startswith(("http://", "https://")):
        rest = url.split("://", 1)[1]
        host, _, tail = rest.partition("/")
        if host != "antifailure.dev":
            return None
        return "/" + tail
    return url if url.startswith("/") else None


canonical = []
links = Counter()
locs = []

for page in sorted(glob.glob("site/**/*.html", recursive=True)):
    with open(page, encoding="utf-8", errors="replace") as f:
        html = f.read()
    tag = re.search(r'<link rel="canonical" href="([^"]*)"', html)
    if tag:
        path = local(tag.group(1))
        if path and disagrees(path):
            canonical.append((page, tag.group(1)))
    for href in re.findall(r'href="([^"]*)"', html):
        path = local(href)
        if path and disagrees(path):
            links[path] += 1

for sitemap in sorted(glob.glob("site/**/sitemap*.xml", recursive=True)):
    with open(sitemap, encoding="utf-8") as f:
        for loc in re.findall(r"<loc>([^<]*)</loc>", f.read()):
            path = local(loc)
            if path and disagrees(path):
                locs.append((sitemap, loc))

spelling = "a trailing slash" if want_slash else "no trailing slash"
if canonical or locs or links:
    print(f'the host serves every page with {spelling} ("trailingSlash": "{policy}"),')
    print("and these ask for the other spelling, which it answers with a 301:")
    for page, url in canonical[:10]:
        print(f"  canonical  {page} -> {url}")
    if len(canonical) > 10:
        print(f"  canonical  ... and {len(canonical) - 10} more pages")
    for sitemap, url in locs[:10]:
        print(f"  sitemap    {sitemap} -> {url}")
    if len(locs) > 10:
        print(f"  sitemap    ... and {len(locs) - 10} more URLs")
    for path, count in links.most_common(10):
        print(f"  link       {path}  ({count} occurrences)")
    if len(links) > 10:
        print(f"  link       ... and {len(links) - 10} more addresses")
    sys.exit(1)

print(
    f"host config: {spelling} everywhere, across "
    f"{len(glob.glob('site/**/*.html', recursive=True))} pages, "
    "their canonicals, their sitemaps and every link between them"
)
SLASH

# One origin, across the whole assembled site.
#
# THE FAILURE. This assertion lived in www/scripts/check-seo.mjs, where it read
# www/out and said, in its own words, "no built file spells the site any way but
# https://antifailure.dev". The published site is not www/out. It is this tree,
# and 192 of its 499 served files are the documentation, the installer and the
# schemas, none of which that gate had ever opened. The sentence was true about
# three fifths of the site and was written about all of it.
#
# Nothing was wrong in the unread three fifths on the day this moved, which is
# the reason to move it rather than the reason not to: a gate whose claim is
# wider than its reach is not a gate that has been lucky, it is a gate that has
# never been asked the question. The deploy step that does read site/docs
# resolves hrefs and has no opinion about which host a page names, so a
# documentation page could tell a visitor to go to the wrong address and every
# stage would stay green.
#
# WHY HERE. Same argument the trailing-slash block above makes: this script is
# the only step that has both builds plus the files neither build produces. The
# marketing half is not checked twice; check-seo.mjs now points here, and the
# comment there says why a narrower copy must not come back.
#
# THE SPELLINGS ARE DERIVED, NOT LISTED. The old check carried three literals.
# A list is a survey of the wrong answers somebody thought of, and www is the
# one that arrived after it was written. This matches any http-or-https,
# apex-or-www spelling and refuses everything that is not the canonical one, so
# a fourth spelling is caught by a check nobody had to remember to update.
# Other subdomains are deliberately untouched: blog.antifailure.dev is a
# different site, not a different spelling of this one.
#
# MENTION VERSUS USE, and this is the fourth instrument in this repository to
# meet it, so it is worth naming rather than rediscovering. prosecheck refuses
# an exemption list for em dashes; cspell needed the real tool name tfsec added
# to a dictionary; gatecheck's action-pin pattern matched `statuses: read`
# because `statuses:` ends in those five characters; and the origin check could
# not tell a planning note discussing the www hostname from a page telling a
# visitor to go there.
#
# They are one root cause and three different answers, and pretending they are
# one answer is how the wrong one gets applied. The root cause: each check
# matched TEXT while its claim was about a POSITION in a structure. The answers,
# in the order to try them:
#
#   1. Parse the position. gatecheck's was this: an action reference is the
#      value of a `uses:` key, and a pattern anchored there cannot be fooled by
#      a word that ends in the same five characters. No exemption was needed
#      because the mention was never in the position.
#   2. Narrow the input until every occurrence in it IS a use. This check is
#      this one. Walking the served tree instead of the repository is what makes
#      docs/plan/notes/www-tls.md a non-question: it is a planning note, it is
#      not published, and a gate over published files never sees it. The
#      distinction the old check could not draw is one it no longer has to.
#   3. A closed list with a stated reason per row, LAST. cspell's dictionary and
#      tools/docs/wiring-exemptions.tsv are this, legitimately: spelling and
#      deploy wiring have no structure left to appeal to, the sets are small,
#      and a row that has stopped being needed is reported so the list cannot
#      rot. prosecheck refuses one because for punctuation there is no true
#      exception to state, and a list with nothing true in it is a place to put
#      findings nobody understood.
#
# Reach for 3 only when 1 and 2 are genuinely unavailable. An exemption is a
# sentence somebody has to keep being right about; a narrower input is not.
#
# THE REACH IS ASSERTED, not assumed, and that is the half that would have
# caught the original defect. Content assertions cannot: they were green while
# the gate read three fifths of the site, and they would be green again if
# somebody narrowed the walk back tomorrow. So this requires the canonical
# origin to appear BOTH under site/docs and outside it. A run that has stopped
# reading either half fails by name instead of passing over an empty set.
python3 - <<'ORIGIN' || exit 1
import os, re, sys

CANONICAL = "https://antifailure.dev"
SERVED = re.compile(r"\.(html|md|txt|xml|json|js|webmanifest)$", re.I)
SPELLING = re.compile(r"https?://(?:www\.)?antifailure\.dev", re.I)

offenders = {}
served = 0
canonical_in = {"docs": 0, "www": 0}

for dirpath, _, filenames in os.walk("site"):
    for name in filenames:
        if not SERVED.search(name):
            continue
        path = os.path.join(dirpath, name)
        rel = os.path.relpath(path, "site")
        served += 1
        with open(path, encoding="utf-8", errors="replace") as f:
            body = f.read()
        half = "docs" if rel == "docs" or rel.startswith("docs" + os.sep) else "www"
        for match in SPELLING.finditer(body):
            if match.group(0) == CANONICAL:
                canonical_in[half] += 1
            else:
                offenders.setdefault(rel, set()).add(match.group(0))

problems = []
if served < 200:
    problems.append(
        f"read only {served} served files out of site/, which is below the floor. "
        "A walk that has stopped matching looks exactly like a site with nothing "
        "in it, and both pass"
    )
for half, where in (("www", "outside site/docs"), ("docs", "under site/docs")):
    if canonical_in[half] == 0:
        problems.append(
            f"no file {where} names {CANONICAL} at all, so this check is not "
            f"reading the {half} half of the assembled site. That is the exact "
            "defect it was moved here to end: it used to read www/out only, and "
            "claimed to have read every built file"
        )

if problems:
    print("the one-origin check cannot make the claim it makes:")
    for problem in problems:
        print("  " + problem)
    sys.exit(1)

if offenders:
    total = sum(len(s) for s in offenders.values())
    print(f"{len(offenders)} served files spell the site as something other than {CANONICAL}:")
    for rel in sorted(offenders)[:20]:
        print(f"  {rel}: {', '.join(sorted(offenders[rel]))}")
    if len(offenders) > 20:
        print(f"  ... and {len(offenders) - 20} more files")
    print(
        f"{total} wrong spellings. Every absolute URL this site emits has to be "
        f"{CANONICAL}: www and the http form are both live and both split every "
        "signal that points here between two spellings of one page."
    )
    sys.exit(1)

print(
    f"one origin: {served} served files, {canonical_in['www'] + canonical_in['docs']} "
    f"occurrences of {CANONICAL}, {canonical_in['docs']} of them under /docs, "
    "and no other spelling anywhere"
)
ORIGIN

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
