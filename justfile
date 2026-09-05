# The task runner.
#
# CONTRIBUTING.md opens by promising `just gate`, and for a while there was no
# justfile at all. That is the worst kind of documentation bug: the first
# command in the first document a contributor reads did not exist.
#
# The promise this file has to keep is narrow and load bearing: `just gate`
# runs what CI runs, in the order CI runs it. If it drifts from
# .github/workflows/ci.yml, the promise quietly becomes a lie, so
# tools/gatecheck compares the two and fails the build when they disagree.

set shell := ["bash", "-uc"]
set positional-arguments

# Go is pinned by engine/go.mod. GOTOOLCHAIN=local means the pinned version is
# the version used, rather than Go silently downloading a different one.
export GOTOOLCHAIN := "local"
export CGO_ENABLED := "0"

reports := ".gate-reports"

# What to run when you do not know what to run.
default:
    @just --list --unsorted

# ---------------------------------------------------------------------------
# The one command
# ---------------------------------------------------------------------------

# Every gate CI runs, in CI's order. Green here means green there.
gate: _reports
    #!/usr/bin/env bash
    set -uo pipefail
    failed=()
    run() {
      local name="$1"; shift
      printf '  %-38s' "$name"
      if "$@" > "{{reports}}/${name// /-}.log" 2>&1; then
        echo "ok"
      else
        echo "FAILED"
        failed+=("$name")
      fi
    }

    # Several gates assert a wall clock budget: a manifest parses in under
    # 250ms, ten thousand runs plan in under a second, a container build
    # finishes inside its deadline. Those are real guards and they are worth
    # keeping, but they measure the machine as much as the code. On a box that
    # is oversubscribed they fail while nothing is wrong, and the failure reads
    # exactly like a regression, so say so before the run rather than after.
    cores=$(getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)
    load=$(uptime | sed 's/.*averages*: *//' | awk '{print $1}' | tr -d ',')
    busy=$(awk -v l="$load" -v c="$cores" 'BEGIN { print (l > c * 1.5) ? 1 : 0 }')
    if [ "$busy" = "1" ]; then
      echo "Load average is $load on $cores cores."
      echo "Timing gates can fail here while nothing is wrong. Re-run a failure on"
      echo "its own before believing it."
      echo
    fi

    echo "Gates"
    run "installs match their lockfiles" just installcheck
    run "generated files are current" just _generated
    run "release stamps a real version"  just ldcheck
    run "release publishes what it signs" just releasecheck
    run "release notes exist for the tag" just relnotes
    run "version pins name real tags"    just tagsync
    run "error catalog and code agree"   just errcheck
    run "lint findings keep their ids"   just lintcheck
    run "the event stream keeps its shape" just eventcheck
    run "the stable Go surface holds"    just surfacecheck
    run "no credential in the tree"      just scanrepo
    run "commands in the docs exist"     just docexamples
    run "documented paths exist"         just claimcheck
    run "the license is detectable"      just licensecheck
    run "the sidebar order is chosen"    just sidebarcheck
    run "runbook numbers agree"          just runbookcheck
    run "spoken variables are documented" just varcheck
    run "STATUS keeps its own rule"      just statuscheck
    run "documented manifests are valid" just manifestcheck
    run "closed sets are counted right"  just constcheck
    run "self-hosting inputs are stable" just inputcheck
    run "documented config can be set"   just wirecheck
    run "the site calls routes that exist" just routecheck
    run "every hostname has an origin"   just origincheck
    run "the smoke waits for real sentences" just sitesmoke
    run "payment secrets reach the app"  just test-infra-config
    run "no unfinished merge"            just conflictcheck
    run "prose reads like a person"      just prosecheck
    run "every figure has a source"      just figurecheck
    run "the mode lists are the real one" just modecheck
    run "every contact route resolves"   just contactcheck
    run "no forbidden tokens in docs"    just forbidden
    run "spelling"                       just spell
    run "prose style"                    just vale
    run "every link resolves"            just links
    run "no class that never applies"    just classcheck
    run "no animation that never stops" just motioncheck
    run "the built docs carry their head" just docscheck
    run "the site's own claims"          just seo
    run "prose stays readable"           just readability
    run "the examples still compile"     just examples
    run "gate matches CI"                just gatecheck
    run "every script can be executed"   just execcheck
    run "deploy keeps jobs on one image" just deploycheck
    run "no yaml key is shadowed"        just keycheck
    run "vet"                            just vet
    run "typecheck"                      just typecheck
    run "format"                         just fmt-check
    run "lint"                           just lint
    run "the gates themselves"           just test-tools
    run "coverage"                       just coverage
    run "engine"                         just test-engine
    run "this platform's keyring"        just keyring
    run "the other platforms lint"       just lint-platforms
    run "control plane"                  just test-web
    run "the site API"                   just test-site-api
    run "the console"                    just test-console
    run "the site beacon"                just test-site-beacon
    run "runner"                         just test-runner
    run "edition boundary"               just edition
    run "enterprise"                     just test-ee
    run "builds are reproducible"        just reproducible
    run "license parser fuzz"            just fuzz-license
    run "engine parser fuzz"             just fuzz-engine
    run "authorship and sign-off"        just authorship
    run "every change says what changed" just changecheck

    echo
    if [ ${#failed[@]} -eq 0 ]; then
      echo "All gates green. Reports in {{reports}}/"
      exit 0
    fi
    echo "${#failed[@]} gates failed:"
    for f in "${failed[@]}"; do
      echo "  $f    {{reports}}/${f// /-}.log"
      tail -n 15 "{{reports}}/${f// /-}.log" | sed 's/^/      /'
    done
    exit 1

_reports:
    @mkdir -p {{reports}}

# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------

# Check every tool this repository needs, and say how to install what is missing.
setup:
    #!/usr/bin/env bash
    set -uo pipefail
    missing=0
    need() {
      local cmd="$1" want="$2" install="$3"
      printf '  %-12s' "$cmd"
      if ! command -v "$cmd" > /dev/null 2>&1; then
        echo "missing        $install"
        missing=$((missing+1))
        return
      fi
      local have
      have=$("${@:4}" 2>&1 | head -1)
      echo "$have"
      if [ -n "$want" ] && ! grep -q "$want" <<< "$have"; then
        echo "               wanted $want. $install"
        missing=$((missing+1))
      fi
    }

    echo "Toolchain"
    need go     "go1.26"  "https://go.dev/dl/ , or: brew install go"           go version
    need node   "v24"     "https://nodejs.org/ , or: brew install node@24"     node --version
    need npm    ""        "ships with node"                                    npm --version
    need docker ""        "https://docs.docker.com/get-docker/"                docker --version
    need git    ""        "brew install git"                                   git --version
    need helm   ""        "https://helm.sh/docs/intro/install/ , or: brew install helm" helm version

    echo
    echo "Documentation gates"
    need vale   ""        "brew install vale , or https://vale.sh/docs/install"   vale --version
    need lychee ""        "brew install lychee , or https://lychee.cli.rs"        lychee --version

    echo
    echo "Repository"
    printf '  %-12s' "hooks"
    if [ "$(git config core.hooksPath || true)" = ".githooks" ]; then
      echo "on"
    else
      echo "off            run: just hooks"
      missing=$((missing+1))
    fi
    printf '  %-12s' "identity"
    email=$(git config user.email || true)
    if [ -z "$email" ]; then
      echo "unset          run: git config user.email you@example.com"
      missing=$((missing+1))
    else
      echo "$email"
    fi

    echo
    printf '  %-12s' "test db"
    if nc -z 127.0.0.1 55432 > /dev/null 2>&1; then
      echo "up on 55432"
    else
      echo "down           run: just db"
      missing=$((missing+1))
    fi

    echo
    if [ "$missing" -eq 0 ]; then
      echo "Everything this repository needs is here. Next: just gate"
    else
      echo "$missing things to fix, each with its command above."
      exit 1
    fi

# CONTRIBUTING.md has always required a Developer Certificate of Origin
# trailer, and `just setup` has always been able to tell you the hook was off.
# Neither turned it on, and the gap between knowing and doing is how an
# unsigned commit reached main: the author's clone had never run the one line,
# nothing local objected, and the check that would have caught it is a check
# that runs after the commit exists.
#
# Local config rather than anything global, so it cannot affect another
# repository on this machine.
[doc("Point git at this repository's hooks, so commits carry a sign-off.")]
hooks:
    #!/usr/bin/env bash
    set -euo pipefail
    git config core.hooksPath .githooks
    echo "hooks       on   .githooks"
    printf 'identity    '
    if email=$(git config user.email) && [ -n "$email" ]; then
      echo "$email"
    else
      echo "UNSET -- run: git config user.email you@example.com"
      echo
      echo "The sign-off trailer is built from this, so a commit made without" >&2
      echo "one cannot be signed off." >&2
      exit 1
    fi

# Squash merge a pull request so the commit it creates carries a sign-off.
#
# `gh pr merge --squash --body ""` makes a squash commit with no
# `Signed-off-by` trailer. Six pull requests were merged that way on
# 2026-09-05. Main's `commits are attributed to their author` context failed on
# the result, cd.yml's gate read that failure and skipped its build, staging
# and production jobs, and staging sat six merges behind at cb3f30f1 until the
# next merge, 597b3819, carried the trailer and let it move again. The commit
# hooks cannot help: a squash commit is created on GitHub's side, so nothing
# local runs at the moment the button is pressed.
#
# See tools/prmerge for what it refuses and why. Three read only modes, all of
# which merge nothing: `-dry-run` checks everything and prints the command it
# would run, `-check-fields` asks the real API whether the field names this
# command reads still exist, and `-confirm-only` reads an already merged pull
# request and proves its commit carries the sign-off.
[doc("Squash merge a pull request with a Developer Certificate of Origin sign-off.")]
merge pr *args:
    go run ./tools/prmerge -pr {{pr}} {{args}}

# Start the Postgres the control plane suites need.
#
# pg_stat_statements is preloaded because the query regression work needs it and
# because of how it fails without it: `CREATE EXTENSION pg_stat_statements`
# succeeds on a server that never loaded the library, the view exists, and it
# records nothing. The tests then skip rather than fail, and a suite that skips
# reports ok. Preloading costs nothing for anybody who does not use it, and
# `track=all` counts statements inside functions and procedures, which is where
# an N+1 usually hides.
#
# The readiness loop below waits for a query to succeed rather than for the port
# to open. An open port is not an accepting database: the postgres image runs
# initdb against a temporary server and shuts it down before starting the real
# one, so both `nc -z` and `pg_isready` answer yes during a window when the next
# query fails with "the database system is shutting down". Getting this wrong is
# how a suite ends up skipping and reporting ok.
db:
    @docker rm -f af-cp-test > /dev/null 2>&1 || true
    docker run -d --name af-cp-test -p 55432:5432 \
      -e POSTGRES_PASSWORD=test -e POSTGRES_DB=antifailure postgres:17-alpine \
      -c shared_preload_libraries=pg_stat_statements \
      -c pg_stat_statements.track=all
    @echo "waiting for postgres"
    @for i in $(seq 1 90); do \
      docker exec af-cp-test psql -U postgres -d antifailure -tAc 'select 1' > /dev/null 2>&1 && break; \
      sleep 1; \
    done
    @docker exec af-cp-test psql -U postgres -d antifailure -tAc 'select 1' > /dev/null 2>&1 \
      || { echo "postgres never accepted a query"; exit 1; }
    @echo "up on 55432"

# Remove it again.
db-down:
    @docker rm -f af-cp-test > /dev/null 2>&1 || true
    @echo "removed"

# Install the JavaScript dependencies.
deps:
    #!/usr/bin/env bash
    set -euo pipefail

    # Every workspace in the tree, found rather than listed. This named web and
    # runner, which is two of the eight lockfiles here, so `just deps` on a
    # fresh clone left www, docs, console, api, ee/web and examples/next-app
    # uninstalled and said nothing. `just installcheck` points people at this
    # recipe, and advice that installs a quarter of the tree is worse than none.
    #
    # ee/web last, because its four packages resolve @antifailure/db and
    # @antifailure/api out of web/ with file: dependencies. `npm ci` there
    # before web exists SUCCEEDS and leaves a tree that does not work: the links
    # resolve to source directories whose own dependencies are absent, and
    # `npm run typecheck` then reports five implicit-any errors inside
    # web/packages/db/src/schema.ts, in a file nobody touched. The order is read
    # from the lockfile rather than from this comment: a workspace whose
    # lockfile links outside its own directory goes last.
    #
    # `ci` for all of them, including ee/web. ci.yml uses `install` there and
    # this followed it until running both showed the difference: `ci` works, and
    # `install` rewrites ee/web/package-lock.json and chmods two files in web/.
    # The verb was never the point. The order always was.
    linked=()
    plain=()
    while IFS= read -r lock; do
      dir=${lock#./}; dir=${dir%/package-lock.json}
      if node -e "const p=require('./$lock').packages||{};process.exit(Object.values(p).some(e=>e.link&&String(e.resolved||'').startsWith('..'))?0:1)"; then
        linked+=("$dir")
      else
        plain+=("$dir")
      fi
    done < <(find . -name package-lock.json -not -path '*/node_modules/*' | sort)

    for dir in "${plain[@]}"; do
      echo "  $dir"
      npm --prefix "$dir" ci --no-audit --no-fund
    done
    for dir in "${linked[@]}"; do
      echo "  $dir (last, because it links into another workspace)"
      npm --prefix "$dir" ci --no-audit --no-fund
    done

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

# Build the af binary into bin/af.
build:
    @mkdir -p bin
    cd engine && go build -o ../bin/af ./cmd/af
    @echo "bin/af"
    @./bin/af version

# Build it the way a release does, so the version is stamped.
#
# Through the same script the release workflow calls, rather than a copy of the
# build command. The copy is what broke it: this recipe and release.yml both
# stamped BuildDate with $(date -u), the two drifted apart in every other
# respect over time, and `just reproducible` could not see the shipping build
# because it was comparing this one.
#
# It produces the real artifact, archive and all, so that what a developer
# builds is what a user downloads.
build-release version="dev":
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p bin
    bare="{{version}}"; bare="${bare#v}"
    ./tools/release/build.sh "$(go env GOOS)" "$(go env GOARCH)" \
      "$bare" "$(git rev-parse HEAD)" "$(git show -s --format=%cI HEAD)" \
      dist stage
    cp "stage/antifailure_${bare}_$(go env GOOS)_$(go env GOARCH)/af" bin/af
    ./bin/af version

# ---------------------------------------------------------------------------
# Test
# ---------------------------------------------------------------------------

# The unit and property tests, everywhere.
test: test-engine test-tools test-web test-runner test-site-api

test-engine:
    cd engine && go test ./... -race -count=1 -timeout 30m

# G4. The coverage thresholds in the build plan's C.5, per package.
#
# Two recipes rather than one, because producing the profile needs the whole
# engine suite with a Docker daemon and a Postgres and takes the better part of
# an hour, while checking it takes a moment. A single recipe would mean nobody
# could look at the numbers without paying for the run again.
#
# -coverpkg over the whole module on purpose: without it a package's number
# counts only what its OWN tests reached, so a package exercised end to end by
# the conformance suite reads as untested. C.5 says the integration tests count.
coverage-profile:
    cd engine && go test ./... -count=1 -coverpkg=./... -coverprofile=../{{reports}}/coverage.out -timeout 60m

coverage:
    go run ./tools/coverage -profile {{reports}}/coverage.out

# -count=1 because the cache cannot see what these read.
#
# go test caches on the files a test opens UNDER ITS OWN MODULE. Several gates
# here read the repository root, which is outside tools/, and tools/installsh
# runs install.sh through sh, so nothing in the package opens it at all. A
# deliberately broken install.sh was reported ok from cache: the only gate
# protecting the installer went green without running.
test-tools:
    cd tools && go test ./... -count=1 -timeout 5m

test-web:
    go run ./tools/installcheck . web || npm --prefix web ci --no-audit --no-fund
    npm --prefix web test --workspaces --if-present

test-runner:
    go run ./tools/installcheck . runner || npm --prefix runner ci --no-audit --no-fund
    npm --prefix runner test

# The marketing site's own backend: api/, one anonymous write endpoint and the
# catch-all that answers everything else. Its own package rather than a
# workspace, because Static Web Apps deploys that directory as it stands.
test-site-api:
    npm --prefix api ci --no-audit --no-fund
    npm --prefix api test

# The console's own unit tests.
#
# console/ is a separate npm project with its own lockfile, not one of web/'s
# workspaces, so `npm test --workspaces` never reaches it and a test file added
# there would be decoration. This recipe is what makes it a gate.
#
# It exists because gatecheck refused the ci.yml step that introduced it, which
# is this file's promise working: a gate is the command AND the directory it
# runs in, so CI running `npm test` in console while nothing here did was the
# drift the comment at the top of this file says cannot happen. Before the
# build, as in CI, because it is seconds against minutes and a failure here is
# about the code rather than about Next.
test-console:
    go run ./tools/installcheck . console || npm --prefix console ci --no-audit --no-fund
    npm --prefix console test

# The marketing site's beacon.
#
# www/ is a separate npm project too, not one of web/'s workspaces, so the same
# argument the console recipe makes applies here: `npm test --workspaces` never
# reaches it and a test file there would be decoration. The beacon decides what
# is measured, how long a session lasts, when a failed request is retried and
# who is a crawler, and until this suite existed not one of those rules could be
# loaded by a runner at all, because the file imported next/navigation.
#
# Before the build, as in CI, and for the same reason: seconds against minutes,
# and a failure here is about the beacon rather than about Next.
test-site-beacon:
    go run ./tools/installcheck . www || npm --prefix www ci --no-audit --no-fund
    npm --prefix www test

# Fanned out over ee/web's workspaces rather than naming each package, so an
# enterprise package added later is covered without editing this or CI. Naming
# them by hand is how two of them ended up untested.
test-ee:
    cd ee/engine && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -race -count=1 -timeout 15m
    # web first, because ee/web's packages resolve @antifailure/db and
    # @antifailure/api out of web/ with file: dependencies, and `npm ci` in
    # ee/web with web absent succeeds and leaves a tree whose typecheck fails
    # inside web/packages/db/src/schema.ts.
    go run ./tools/installcheck . web || npm --prefix web ci --no-audit --no-fund
    go run ./tools/installcheck . ee/web || npm --prefix ee/web ci --no-audit --no-fund
    npm --prefix ee/web run typecheck
    npm --prefix ee/web test

# The fast ones, for a tight loop.
test-short:
    cd engine && go test ./... -short -count=1 -timeout 10m

# ---------------------------------------------------------------------------
# The individual gates
# ---------------------------------------------------------------------------

vet:
    cd engine && go vet ./...

fmt:
    gofmt -w engine tools

fmt-check:
    #!/usr/bin/env bash
    unformatted=$(gofmt -l engine tools)
    if [ -n "$unformatted" ]; then
      echo "These files are not formatted. Run: just fmt"
      echo "$unformatted"
      exit 1
    fi
    echo "formatted"

# Every error code is documented and every documented code is reachable.
errcheck:
    go run ./tools/errcheck .

# Every lint finding has an identifier, and every identifier ever handed out is
# still spoken for.
#
# The rule name is prose and moves as the rules sharpen. The number does not,
# and this is what keeps that true: a rule with no entry, an entry for a rule
# that is gone, and an identifier that has left the catalogue since it was
# registered are all failures here.
lintcheck:
    go run ./tools/lintcheck .

# No event type has been taken away and the envelope still has its fields.
#
# The catalog gains a type most weeks and that is compatible. Losing one is not,
# and nothing was watching for it: the schema is generated from the Go type and
# the catalog, so deleting a type or a field regenerates cleanly and the diff is
# green. engine/internal/events/stream.register.json is what version 1 promised,
# and this refuses to let any of it go.
eventcheck:
    go run ./tools/eventcheck .
# The Go packages version 1 promised, and the ones it deliberately did not.
# Also the leak that makes the difference meaningless: a stable signature
# naming a type an outside caller has no way to write.
surfacecheck:
    go run ./tools/surfacecheck .

# The release stamps version variables that exist, and stamps every one it
# declares.
ldcheck:
    go run ./tools/ldcheck .

# The release publishes every asset it signs, and the job that signs holds the
# token that keyless signing needs. The signing half of release.yml has never
# run, so the first tag is its first execution.
releasecheck:
    go run ./tools/releasecheck .

# Every changelog section has something under it, so no tag can publish a
# release whose notes are a heading and nothing else.
relnotes:
    go run ./tools/relnotes .

# No version pin names a tag nobody has published. The Terraform image_tag
# defaults are live, so bumping them with the tag rather than after it points
# the next apply at an image that does not exist.
#
# It also holds the four version literals in the verification page to the
# release being cut, and holds them strictly: naming an older tag that really
# was published is the defect that shipped, since the page then tells a reader
# to fetch a bundle that release does not carry.
tagsync:
    go run ./tools/tagsync .

# Nothing in the tree looks like a live credential.
scanrepo:
    go run ./tools/scanrepo .

# Every af command shown in the docs is a command that exists.
# -count=1 for the same reason test-tools needs it, proven the same way.
#
# This test reads docs/src/content/docs and examples/, both outside the engine
# module, so nothing it depends on is anything the cache watches. Measured: a
# documentation page was edited to read `af init --wat`, a flag that does not
# exist, and `just docexamples` answered "ok (cached)". The same test with
# -count=1 failed on it immediately. CI already passes -count=1 through
# `go test ./...`, so this was a local-only lie, and a local-only lie is the
# worst kind here: CONTRIBUTING promises a green `just gate` means a green CI.
docexamples:
    cd engine && go test ./internal/cli -run TestEveryCommandInTheDocsExists -count=1

# The punctuation this project does not use.
licensecheck:
    go run ./tools/licensecheck .

prosecheck:
    go run ./tools/prosecheck .

# Every number on the site that reads as a measurement has a stated source.
#
# The site rendered an invented "fid 87%" fidelity score on two product pages.
# It was drawn client side, so curl found no "87" in the HTML and every cheap
# audit came back clean. This reads the source instead.
figurecheck:
    go run ./tools/figurecheck .

# Every sentence that enumerates the egress modes enumerates the real ones,
# read from schemas/manifest.v1.json rather than from a second copy of the list.
modecheck:
    go run ./tools/modecheck .

# No address published anywhere in this tree sends a reader into silence.
#
# CODE_OF_CONDUCT.md named conduct@antifailure.dev and the legal pages named
# security@antifailure.dev. The domain has no mail exchanger, an SPF policy
# authorising no sender and a revoked DKIM key, so a harassment report and a
# security finding went to the same nowhere. Every address in the tree now
# needs a row in tools/docs/contact-routes.tsv saying who reads it.
contactcheck:
    go run ./tools/contactcheck .

# No class on a rendered element that another class on the same element beats,
# so it is written, reviewed, and does nothing.
#
# `cn` is a plain join, not tailwind-merge, so a className passed to a component
# lands beside the component's own class rather than replacing it, and the
# cascade picks whichever Tailwind emitted last. The site header marked the
# current page with text-black over a text-black/70 default, lost, and marked
# nothing at all. Reads the built HTML, so it needs a built www AND a built
# console, and it refuses rather than skipping when either is missing.
classcheck:
    go run ./tools/classcheck .

# No UI that animates forever while the reader does nothing.
#
# Reads the built stylesheet and the built HTML, never the source. Two people
# read globals.css on the same day, both saw the one infinite rule left in it,
# and both called it harmless because nothing in that file used it. It was
# rendered on the front page by HeroFilm.tsx. The source says which rules
# exist; only the render says which land on an element. Needs a built www AND
# a built console, and it refuses rather than skipping when either is missing,
# because skipping is how the console went unchecked for the whole life of
# this gate.
motioncheck:
    go run ./tools/motioncheck .

# Spelling, with the project dictionary in tools/docs/dictionary.txt.
spell:
    npx --yes cspell --no-progress "docs/src/content/docs/**/*.md" "examples/**/*.md" README.md CONTRIBUTING.md SECURITY.md

# Prose style: the Google developer documentation style, plus the rule about
# em dashes. `vale sync` fetches the style package named in .vale.ini.
vale:
    vale sync
    vale docs/src/content/docs examples README.md CONTRIBUTING.md SECURITY.md CODE_OF_CONDUCT.md

# Every link on the assembled site, including fragments.
#
# Against the built site rather than the markdown, because the addresses a
# reader follows are the built ones: /docs/reference/cli/#af-init is a heading
# on a page, not a file in the tree, and only the built site knows whether it
# exists. Offline, so a slow upstream cannot fail somebody's local run; the
# external addresses are checked on the daily schedule.
links:
    #!/usr/bin/env bash
    set -euo pipefail
    (cd www && npm run build)
    (cd docs && npm run build)
    # tools/site/assemble.sh, rather than a third hand-rolled copy of it.
    #
    # This recipe assembled the tree itself, which is the mistake assemble.sh
    # was written to end: that script exists because CI and deploy had each
    # grown their own copy and the two drifted, one learning to handle both
    # shapes of the Astro output while the other never did. A third copy was
    # free to drift the same way, and had: it copied www/out and docs/dist and
    # nothing else, so install.sh and schemas/ were absent from the tree this
    # checked while CI published them, and it skipped the host config merge
    # entirely, so every assertion that merge makes could not fail here and
    # first spoke up in CI twenty minutes later. The shadowed-page check is one
    # of those, and it is the one a developer most wants before pushing,
    # because the page it fails on is a page they just wrote.
    #
    # The link count does not move: the files this now adds are not HTML, and
    # the absolute addresses that point at them are external links, which an
    # offline run does not resolve either way.
    tools/site/assemble.sh
    lychee --config lychee.toml --no-progress --offline --root-dir site 'site/**/*.html'

# The assertions the marketing site makes about itself, against the built site.
#
# Sitemap, robots, canonicals, OpenGraph, structured data, the markdown twins
# and the skip link. Every one of them was absent from production at the time
# it was written and nothing said so, because a missing meta tag breaks no page
# and fails no type check. ci.yml has run this on every pull request since; the
# justfile never has, so `just gate` was green on a tree that CI refused, on the
# one surface a customer sees first. gatecheck could not say so either, because
# `npm run <script>` matched no pattern it had.
#
# Its own recipe rather than a line inside `links`, for the reason `typecheck`
# learned the hard way: a gate that takes an hour and is named after link
# resolution is not where anybody looks for a missing canonical tag.
#
# It builds rather than reusing whatever is in www/out, even though `links`
# built the same thing a moment earlier in `gate`. A check that runs against
# whatever happened to be on disk can pass against last week's site, which is
# the same class of defect it exists to catch, and Next's cache makes the
# second build the cheap part of an hour long gate.
seo:
    #!/usr/bin/env bash
    set -euo pipefail
    # Not `[ -d www/node_modules ]`. That condition installs when nothing is
    # there and does nothing when what is there is wrong, which is how a week of
    # www work got verified against Next 15 while the lockfile pinned 16.
    go run ./tools/installcheck . www || npm --prefix www ci --no-audit --no-fund --silent
    (cd www && npm run build)
    (cd www && npm run check:seo)

# Not in `just gate`, because it asks the live internet a question and a gate
# that fails when the wifi drops teaches people to rerun a gate rather than read
# it. It runs after a publish in .github/workflows/deploy.yml, which is the
# moment a lapsed certificate is worth knowing about, and by hand any time
# somebody reports that the site will not load for them.
#
# The apex and www are two custom domains with two separate managed
# certificates and two separate renewal lifecycles, and .dev is an HSTS
# preloaded top level domain, so a certificate fault on either name is a hard
# failure a reader cannot click past.
#
# Every hostname the site answers on presents a valid certificate for its name.
check-tls:
    tools/site/check-tls.sh

# The live half of `just origincheck`: what Azure really serves, and what the
# deployed control plane really answers.
#
# Not in `just gate`, for the same reason as `check-tls`. Its answer is not a
# function of the tree: it asks Azure which custom domains are bound to af-site
# and asks a deployed control plane to answer a real CORS preflight from each of
# them, so the same commit is green today and red the morning somebody binds a
# hostname in the portal. That is exactly the case it exists for, and it needs
# both a signed in Azure CLI and the network, which `just gate` must not.
#
# It exits 2, not 0, when it cannot reach Azure. A check that reports success
# for a question it never asked is how the next hostname somebody binds becomes
# invisible all over again.
#
# Point it somewhere else with --api, which is how a local plane carrying a
# change is checked before it is deployed:
#
#     go run ./tools/origincheck live --api http://127.0.0.1:8791
check-origins:
    go run ./tools/origincheck all --api https://app.antifailure.dev

# The getting started path, run in order and timed.
#
# Not in `just gate`. It needs a Docker daemon and it takes minutes, because it
# really does build an image, branch a database, and wait for a service to
# answer. The daily schedule in .github/workflows/walkthrough.yml is where it
# runs unattended; run it here before changing anything a new user touches.
walkthrough:
    go run ./tools/walkthrough .

# Prove the backup restores, and record how long it took.
#
# Not in `just gate`, for the same reason as `walkthrough`: it needs a Docker
# daemon and it takes minutes, because it really does take a dump, create a
# database, restore into it and then interrogate the result through the
# unprivileged role. The weekly schedule in .github/workflows/drill.yml is
# where it runs unattended, and that workflow runs this recipe rather than its
# own copy of these commands, so the thing CI proves and the thing you can run
# here cannot drift apart.
#
# A Postgres of its own, on a port nothing else uses. This creates databases,
# restores into them and drops them, which is antisocial on the shared
# development container and fragile against anyone recreating it mid-run.
#
# The scratch database is seeded with two organizations before the drill runs,
# and that is not decoration. Against an empty database every comparison passes
# over nothing and the cross-tenant read has no other tenant to be refused: a
# green run that examined nothing, which is the failure this repository keeps
# finding. Two, because one tenant cannot be isolated from anybody.
#
# 300 seconds is a backstop, not a performance target, and the number was
# chosen from measurements rather than picked. This same drill restores in
# under two seconds on a continuous integration runner with nothing else on it.
# On a laptop running a dozen other containers, two runs of this recipe an hour
# apart reported 14.9 seconds and 46.3, and this repository has recorded a
# restore of the same database taking 160 on a busy machine. A budget set near
# the CI number, or near the first laptop number, would spend most of its life
# failing on machine load, and a gate that fails for a reason the author cannot
# fix is one people learn to re-run until it passes.
#
# So this fires on a change in kind: a restore that has stopped working rather
# than one on a slow morning. What actually detects a regression is the series,
# not the threshold, which is why the workflow publishes the measured time to
# the run summary and keeps it as an artifact for ninety days. Compare against
# last week's number; the budget is only there so a restore that has fallen off
# a cliff cannot pass quietly.
#
# The objective the recovery time is held against is two hours, and it lives in
# docs/src/content/docs/self-hosting/operations.md where an operator reads it.
#
# One command, and the one the weekly workflow runs.
drill: _reports
    #!/usr/bin/env bash
    set -euo pipefail
    url="postgres://postgres:test@127.0.0.1:55434/antifailure"
    cleanup() { docker rm -f af-drill-test > /dev/null 2>&1 || true; }
    trap cleanup EXIT
    cleanup
    docker run -d --name af-drill-test -p 55434:5432 \
      -e POSTGRES_PASSWORD=test -e POSTGRES_DB=antifailure postgres:17-alpine > /dev/null
    # A real query against the target database rather than pg_isready. The
    # image runs initdb against a temporary server and pg_isready answers on
    # that one, before POSTGRES_DB exists.
    for _ in $(seq 1 90); do
      docker exec af-drill-test psql -U postgres -d antifailure -tAc 'select 1' \
        > /dev/null 2>&1 && break
      sleep 1
    done
    docker exec af-drill-test psql -U postgres -d antifailure -tAc 'select 1' > /dev/null 2>&1 \
      || { docker logs af-drill-test; echo "postgres never accepted a query"; exit 1; }
    rm -rf {{reports}}/drill && mkdir -p {{reports}}/drill
    node web/apps/api/src/backup-scratch.ts --url "$url" --app-password drill-password
    node web/apps/api/src/backup-cli.ts drill \
      --url "$url" \
      --out {{reports}}/drill \
      --database af_drill \
      --app-password drill-password \
      --max-restore-seconds 300 \
      --report {{reports}}/drill.json

# The examples build and their manifests are valid.
#
# An example that does not compile is worse than no example: it is the first
# thing a user copies. They are separate modules, outside the workspace on
# purpose, so that an example's dependencies never enter the engine's module
# graph, which means `go build ./...` at the root does not see them and this
# recipe is the only thing that does.
examples:
    #!/usr/bin/env bash
    set -euo pipefail
    for dir in examples/*/; do
      [ -f "$dir/go.mod" ] || continue
      echo "  $dir"
      # -o /dev/null, because a bare `go build ./...` writes the binary into
      # the example directory and the next `git add -A` stages it. That has
      # happened here twice.
      (cd "$dir" && GOWORK=off go build -o /dev/null ./... && GOWORK=off go vet ./...)
    done
    # The same rule for the examples that are not Go. An example that does not
    # build is worse than no example whatever it is written in, and checking
    # only the compiled ones would have left the newest one unchecked by the
    # gate that exists to say exactly this.
    for dir in examples/*/; do
      [ -f "$dir/package.json" ] || continue
      echo "  $dir"
      (cd "$dir" && npm ci --no-audit --no-fund --silent && npm run build)
    done
    # And the ones written in Python. There is no compile step, so the check
    # that means something is the framework's own: `manage.py check` loads the
    # settings, the URL conf and every model, which is where a typo in any of
    # them shows up. DATABASE_URL is a placeholder because check does not
    # connect; the example refuses to start without one, deliberately, and this
    # gate should not be the thing that discovers that.
    for dir in examples/*/; do
      [ -f "$dir/requirements.txt" ] || continue
      echo "  $dir"
      (
        cd "$dir"
        python3 -m venv .venv-gate
        ./.venv-gate/bin/pip install -q --disable-pip-version-check -r requirements.txt
        ./.venv-gate/bin/python -m compileall -q . -x '\.venv-gate'
        if [ -f manage.py ]; then
          DATABASE_URL="postgres://gate:gate@127.0.0.1:5432/gate" \
            ./.venv-gate/bin/python manage.py check
        fi
        rm -rf .venv-gate
      )
    done
    go build -o /tmp/af-examples ./engine/cmd/af
    # The repository's own manifest is in this loop, and it was in no gate at
    # all until it was. Every example was validated and the one file this
    # product is dogfooded with was not.
    for manifest in ./antifailure.yaml examples/*/antifailure.yaml; do
      [ -f "$manifest" ] || continue
      (cd "$(dirname "$manifest")" && /tmp/af-examples explain > /dev/null)
      echo "  $manifest is valid"
    done

# How hard each page is to read, worst first.
#
# The threshold is a regression guard rather than a style rule. The hardest
# page today averages 23 words a sentence, so 28 leaves five words of headroom:
# it fires on a page that drifted, not on a page whose subject needs long
# sentences. Run it with no argument to read the whole report.
readability:
    go run ./tools/readability --max 28 .

# The G8 forbidden token scan: notes to the author, unfilled slots, names that
# belong to a person rather than the product, addresses that resolve only on
# somebody's private network, and identifiers that name a real cloud tenant.
forbidden:
    ./tools/docs/forbidden.sh

# Every repository path our documents point at exists.
claimcheck:
    go run ./tools/claimcheck .

# Every variable the product names at a user is one the documentation explains.
#
# `af license install` tells a paying customer to set AF_LICENSE_KEY and AF_ORG,
# then points them at the licensing page, and that page named neither. The
# product asked for two things and sent the reader to the one page that should
# have said what they are. `af doctor` had the same shape with
# AF_PORT_RANGE_START. The control plane has had this check since
# config-docs.test.ts; the engine, which is the half a customer runs on their
# own machine, never did.
#
# It parses rather than greps, because the first version was line oriented and
# returned a clean zero over AF_PORT_RANGE_START while looking straight at it:
# `r.Remediation = fmt.Sprintf(` and the string naming the variable sit on
# different lines.
varcheck:
    go run ./tools/varcheck .

# The sidebar order is a decision rather than an accident.
#
# Starlight breaks a tie in sidebar.order on FILE NAME, which is invisible to
# somebody editing a page and silent everywhere else. 27 of the 78 ordered
# pages shared a number with a sibling, so a third of the sidebar was
# alphabetised by slug while reading like a designed order: "Watching a run"
# split the two runtime guides, "Provider limits" split the three provider
# pages, and On-call came before Standing up production.
sidebarcheck:
    go run ./tools/sidebarcheck .

# A numbered runbook still numbers itself.
#
# Step headings are visible while editing and cross references to them are
# not, so inserting or removing a step renumbers one and silently invalidates
# the other, and the operator who follows the stale one lands on the wrong
# step of a production stand-up. It reads the numbers only: a step deleted and
# every later step renumbered to close the hole is internally consistent and
# passes, which is recorded in the tool rather than hoped about.
runbookcheck:
    go run ./tools/runbookcheck .

# Create the first operator using the deployment's existing database credential.
operator-init environment:
    node deploy/cd/operator-init.mjs {{quote(environment)}}

# STATUS.md keeps the rule it states about itself.
#
# That file opens by saying every component carries one of a fixed set of
# states and nothing else, and four rows carried a word outside the set, in
# three different spellings. It is the page this project points at when
# somebody asks whether a thing works yet, so a word in it that nobody defined
# is an answer nobody can check, and nothing read it before this.
statuscheck:
    go run ./tools/statuscheck .

# The built documentation carries its head, and its entity graph resolves.
#
# check-seo.mjs reads www/out and never opens docs/dist, so the documentation,
# which is 76 of the site's roughly 90 pages, had no gate with an opinion about
# what it renders. Six head entries went missing for every one of those pages
# and every stage stayed green, because no stage was looking.
#
# Needs the docs built. It fails rather than skipping when docs/dist is absent,
# because a gate that is green about nothing is the gap it closes.
docscheck:
    go run ./tools/docscheck .

# Every manifest shown in the documentation is one the engine would accept.
#
# The gates already read style, spelling, links and repository paths, and none
# of them knows what a manifest is. A getting started page shipped telling
# readers to set control_plane.url, which the engine refuses with AF-MAN-002
# because the schema closes itself so a typo cannot silently change an
# environment.
manifestcheck:
    go run ./tools/manifestcheck .

# A count of a closed set that lives only in Go constants must be its real
# size.
#
# The schema, the error catalogue, the transform registry and the command tree
# all have a gate because each is declared in a machine readable file. The Go
# constants had none, and that is where the worst instance was: seventeen DDL
# lint rules described in eight places as six. Fourteen wrong counts, every one
# of them an understatement, which is what documentation written accurately at
# one version and never revisited looks like.
constcheck:
    go run ./tools/constcheck .

# The Helm values and the Terraform variables still say what v1.0.0 promised
# they would say.
#
# A self hoster's values file and tfvars file ARE their configuration, kept in
# their repository and applied by their pipeline. Until v1.0.0 the release
# notes said both surfaces could be rearranged in a minor release, which makes
# every upgrade a hand migration nobody can automate. The promise replaces
# that sentence and this holds the tree to it: an input is not removed, not
# renamed, does not change type, and does not become required.
#
# Adding an optional input is compatible and allowed. It fails here only until
# `go run ./tools/inputcheck -update .` records it in the snapshot.
inputcheck:
    go run ./tools/inputcheck .

# A documented variable can actually be DELIVERED by the supported deploy path.
#
# The reference documented 45 variables the hosted control plane reads and the
# Terraform module that is the only route onto that container could set 16.
# `just varcheck` above and web/apps/api/test/config-docs.test.ts both proved
# every one of the 45 was DOCUMENTED, and stayed green the whole time, because
# they answer a nearby question: a variable that is documented, read by the
# application, and unreachable by every apply satisfies both of them exactly.
#
# For every variable the reference documents, either an env block in the module
# sets it or tools/docs/wiring-exemptions.tsv says why it cannot, and a row
# that has stopped being needed is reported so the file cannot rot.
wirecheck:
    go run ./tools/wirecheck .

# The site does not call a control plane route that is not there.
#
# Somebody filled in the careers form on antifailure.dev and was told "Could not
# reach the server". The form was right, the route was right, and
# `POST https://app.antifailure.dev/v1/applications` answered 404: the site
# publishes on every merge to main and the control plane only moves on a `v*`
# tag, so the page was live against an API twenty two commits behind it.
#
# This is the OFFLINE half, which is what a pull request can prove. It checks
# that every control plane URL the site builds is declared in
# www/lib/control-plane-routes.ts and that every route declared there is one
# this repository's control plane actually mounts. It CANNOT see the failure
# above, and says so when it finishes: on the day the careers form broke, main's
# API did declare the route. The half that would have caught it needs a live
# origin and runs in deploy.yml against the control plane the site is about to
# be published in front of.
routecheck:
    go run ./tools/routecheck -root .

# The same command against a control plane that is actually running.
#
# Not part of `just gate`, because it asks the internet and a gate that blocks a
# merge on a network timeout is a gate people learn to re-run rather than read.
# deploy.yml runs it before the publish step, which is where the answer matters:
# the question is not what main declares, it is what the origin this build is
# about to point browsers at will answer them.
routecheck-deployed origin="https://app.antifailure.dev":
    go run ./tools/routecheck -root . -origin {{origin}} -allow-write-probes

# Every hostname the marketing site is served on is one the control plane will
# answer a browser from.
#
# THE FAILURE. antifailure.dev and www.antifailure.dev are two custom domains on
# one Azure Static Web App, both Ready, both serving every page, and neither
# redirects to the other because a Static Web Apps route rule matches on PATH
# and its schema has no hostname condition at all. site_origin in
# production.tfvars held one value, the apex. So every call the site makes was
# refused 403 whenever a visitor had arrived on www: the analytics beacon, the
# enterprise contact form, the careers application form. It was found on a
# phone, by a person, on the live site, and every check anybody had run was
# green because they all asked the apex.
#
# This is the offline half and it is the one that runs on every branch: the
# hostnames in tools/site/hostnames.txt against site_origin in each control
# plane tfvars, in both directions. `just check-origins` is the other half, and
# it is what keeps hostnames.txt from being a list somebody typed.
origincheck:
    go run ./tools/origincheck origins

# The sentences the production smoke waits for are still the ones we produce.
#
# THE OFFLINE HALF of tools/sitesmoke, and it is deliberately modest about what
# it proves. It cannot see a deployment, and on the day the careers form broke
# the tree was perfect: main declared the route and production was serving a
# version from before it existed. What it CAN prove is the one way the online
# half could go quietly wrong. The smoke waits for the control plane's own
# refusal, "Use a public http or https link without credentials", and for the
# site's own confirmation, "It is written down." An expectation waiting for a
# sentence this repository no longer produces fails every deployment forever;
# one satisfied by the wrong page passes every deployment forever. So the
# sentences are read out of the files that are supposed to render them.
sitesmoke:
    go run ./tools/sitesmoke -root .

# The same command against the site that is actually deployed.
#
# Not part of `just gate`, for the reason routecheck-deployed gives: it asks the
# internet, and a gate that blocks a merge on somebody's bad afternoon is a gate
# people learn to re-run rather than read. It runs on a schedule and after a
# deploy in .github/workflows/sitesmoke.yml, which is where the answer matters.
#
# It files no job applications. Add -allow-writes to run the workflow that
# does, which is the only way to prove through a browser that a valid
# application actually reaches the database.
sitesmoke-deployed origin="https://antifailure.dev":
    go run ./tools/sitesmoke -root . -origin {{origin}}

# Mocked providers exercise the rendered payment references without cloud access.
test-infra-config:
    terraform -chdir=infra/terraform/modules/control-plane init -backend=false -input=false
    terraform -chdir=infra/terraform/modules/control-plane test

# No file in the tree carries a merge conflict marker.
#
# One reached main inside a documentation table and every other gate was green
# about it, because markdown does not fail to parse and a row inside a conflict
# block still reads as documented to everything that asks whether a variable is
# documented.
conflictcheck:
    go run ./tools/conflictcheck .

# This justfile runs what CI runs.
gatecheck:
    go run ./tools/gatecheck .

# Every script something runs by path is one git records as executable.
#
# tools/site/check-tls.sh was committed at mode 100644 and both of its call
# sites name it as a bare path, so it died with "Permission denied" and status
# 126 the first time a push to main reached the deploy job. It had never run
# anywhere, because the only step that runs it fires on a push and not on a
# pull request.
execcheck:
    go run ./tools/execcheck .

# The serving application and the scheduled DDL job stay on one tested image.
# The test runs the real deploy script against fake Azure and health endpoints,
# including every failure ordering that must leave maintenance unchanged.
deploycheck:
    ./deploy/cd/deploy_test.sh

# No YAML key is defined twice in one mapping.
#
# Charts are rendered through three valid profiles before they are read: a
# template is Go template source that no YAML parser can take, and helm lint
# returns clean on a chart whose service.yaml defines `type` twice. Helm's
# source markers prove that every authored YAML template was reached.
keycheck:
    go run ./tools/keycheck .

# The TypeScript that ships: the control plane packages, the agent runner, and
# the console.
typecheck:
    #!/usr/bin/env bash
    set -euo pipefail

    # Every TypeScript project in the tree, found rather than listed.
    #
    # This recipe named its projects by hand and the list was wrong twice, the
    # second time expensively. console had to be added after a type error in it
    # passed here and failed the www job twenty minutes later; that comment is
    # kept below because it is the same story. www was never added at all, and
    # a merge that left contentLastModified declared twice in www/lib/lastmod.ts
    # passed this recipe and failed CI.
    #
    # `just gate` would have caught it, through `just links`, which builds www
    # before it checks the links. That is not a defence. The briefing tells
    # every agent not to run the full gate and to run the targeted gates for
    # what they touched, so somebody who edits TypeScript runs THIS, gets green,
    # and has checked nothing. A gate that takes an hour and is named after link
    # resolution is not where anybody looks for a type error.
    #
    # So the projects come from the tree. One that is checked somewhere else is
    # named with its reason; one that is neither checked here nor named fails
    # this recipe rather than being skipped in silence. Forgetting a new
    # TypeScript project is no longer possible, which is the property the hand
    # written list did not have. Same shape as tools/docs/manifest-exemptions.tsv,
    # and a reason that stops being true fails too.
    checked_elsewhere() {
      case "$1" in
        console)           echo "next build, at the end of this recipe" ;;
        docs)              echo "just links, which builds it; Astro, and tsc does not read .astro" ;;
        ee/web/*)          echo "just test-ee, via npm --prefix ee/web run typecheck" ;;
        examples/next-app) echo "just examples, which builds every example that has a package.json" ;;
        *) return 1 ;;
      esac
    }

    # The npm project a tsconfig belongs to is the nearest directory above it
    # holding a lockfile. web/ has one lockfile and three tsconfigs under it,
    # so the prefix cannot be derived from the tsconfig's own directory.
    npm_root() {
      d=$1
      while [ "$d" != "." ] && [ "$d" != "/" ]; do
        if [ -f "$d/package-lock.json" ]; then echo "$d"; return 0; fi
        d=$(dirname "$d")
      done
      return 1
    }

    checked=0
    excused=0
    while IFS= read -r cfg; do
      dir=${cfg#./}
      dir=${dir%/tsconfig.json}
      if reason=$(checked_elsewhere "$dir"); then
        echo "  $dir: $reason"
        excused=$((excused + 1))
        continue
      fi
      root=$(npm_root "$dir") || { echo "  $dir: no lockfile above it, so there is no project to check it in"; exit 1; }
      echo "  $dir"
      # Absent OR stale. This asked only whether the directory existed, in the
      # recipe an agent is most likely to trust after editing TypeScript, so a
      # drifted install was typechecked against the wrong versions and passed.
      go run ./tools/installcheck . "$root" || npm --prefix "$root" ci --no-audit --no-fund --silent
      npx --prefix "$root" tsc --noEmit -p "$cfg"
      checked=$((checked + 1))
    done < <(find . -name tsconfig.json \
      -not -path '*/node_modules/*' -not -path '*/.next/*' -not -path '*/dist/*' | sort)

    # A reason that has stopped being true. Without this the excuses rot: a
    # project gets deleted or renamed and its entry sits here claiming another
    # gate covers something that no longer exists.
    for named in console docs ee/web/audit ee/web/rbac ee/web/scim ee/web/sso examples/next-app; do
      [ -f "$named/tsconfig.json" ] || { echo "  $named is named as checked elsewhere and has no tsconfig.json; remove it"; exit 1; }
    done

    # The console, which this file was not checking and ci.yml was.
    #
    # That gap is the interesting part rather than the console itself. The
    # comment at the top of this file says green here means green there, and a
    # type error in console/ passed `just gate` and failed the www job twenty
    # minutes later. It is a separate npm project with its own lockfile, so it
    # is in neither loop above and had to be named.
    #
    # A build rather than a typecheck, because `next build` runs the typecheck
    # too and then does the part a typecheck cannot: an import written as
    # ../lib/guard from a page two directories deep resolves through baseUrl
    # and fails in webpack with "Module not found".
    go run ./tools/installcheck . console || npm --prefix console ci --no-audit --no-fund
    NEXT_TELEMETRY_DISABLED=1 npm --prefix console run build
    echo "typecheck: $checked projects typechecked, $excused checked by another gate"

# Every installed node_modules is the tree its lockfile describes.
#
# First in `gate`, and cheap enough to be, because it compares two files rather
# than installing anything. It answers in milliseconds and needs no network, so
# finding out at second two beats finding out after fifty minutes of gates that
# were all answering about the wrong versions.
#
# The failure it exists for: a week of www work was verified with
# www/node_modules holding Next 15.5.23 against a lockfile pinning 16.3.3.
# Every build, every SEO assertion and a whole prose sweep ran against a
# different Next major from the one CI uses, and every one of them reported
# success in good faith. It also explains `next build` rewriting
# www/tsconfig.json for some people and not others, which several agents chased
# as flakiness: it is Next 16 behaviour and a stale 15 install does not do it.
#
# --drift-only, so a workspace nobody has installed is reported and does not
# fail. It cannot have answered about the wrong versions, every recipe below
# installs what it uses, and a gate that went red on a fresh worktree for a
# directory it was about to create anyway is the false alarm that gets a check
# deleted.
installcheck:
    go run ./tools/installcheck --drift-only .

# G11. Two builds of one commit produce the same release artifact.
#
# The artifact, not the binary. This compared bin/af, which was already
# identical every time, and passed while all four .tar.gz files it stood in for
# differed between builds. The binaries were reproducible; the thing a person
# downloads never was.
#
# The two causes were both in the packaging. tar takes each entry's mtime from
# the filesystem and cp had just set those to now, and gzip writes its own
# timestamp into the header, so the archive carried the wall clock twice.
# tools/reltar writes the archive instead, which is why this can now assert the
# property that matters.
#
# It also runs in a CI job of its own. A gate that only ever runs on a
# workstation is a gate that stops running.
reproducible version="v0.0.0-gate":
    ./tools/release/reproducible.sh {{version}}

# The license parser has to survive arbitrary input, because a licence token
# arrives from outside and a parser that panics on one is a denial of service
# with extra steps. Sixty seconds here, longer in a nightly run.
fuzz-license seconds="60":
    cd ee/engine && GOWORK=off go test ./license -run FuzzParse -fuzz FuzzParse -fuzztime {{seconds}}s

# G6, for the two parsers that read untrusted input from the customer's side.
#
# The manifest is a file from the repository under test and the detection
# engine reads that repository's contents, so both are parsing bytes somebody
# else wrote. C.7 says the customer's repository is data and never code; a
# parser that panics on a crafted file is the cheapest way to break that.
#
# These targets already existed and were never fuzzed. `go test ./...` runs a
# fuzz target against its committed seed corpus only, which is a unit test
# wearing a fuzzer's name: it proves the seeds still pass and explores nothing.
# Sixty seconds each here, matching the licence parser, longer in a nightly.
fuzz-engine seconds="60":
    cd engine && go test ./internal/manifest -run FuzzParse -fuzz FuzzParse -fuzztime {{seconds}}s
    cd engine && go test ./internal/detect -run FuzzAnalyzers -fuzz FuzzAnalyzers -fuzztime {{seconds}}s

# Regenerate everything that is generated, then prove nothing changed.
#
# Scoped to the generated files rather than the whole tree. CI can diff
# everything because it runs on a clean checkout; you cannot, because you are
# always in the middle of an edit, and a gate that fails whenever you have
# uncommitted work is a gate you learn to skip. The property either way is the
# same: what is generated matches what it is generated from.
_generated:
    #!/usr/bin/env bash
    set -euo pipefail
    go run ./tools/errgen
    go run ./tools/lintgen
    go run ./tools/proxysrc
    go run ./tools/schemadoc .
    go run ./tools/notices -out THIRD_PARTY_NOTICES.md
    (cd engine && go test ./internal/policy -update-vectors)
    (cd engine && go test ./internal/mockpack -update-vectors)
    (cd engine && go test ./internal/webhook -update-vectors)
    (cd engine && go test ./internal/cli -update-reference)
    (cd engine && go test ./internal/events -update-schema)
    go run ./tools/eventcheck -freeze .
    (cd engine && go test ./internal/masking -update-transforms)
    (cd engine && go test ./internal/hud -update-frames)
    # The OpenAPI artifact is generated too, and its generator is TypeScript
    # rather than Go. Its own --check mode is the comparison, so it is run in
    # the same form and the same directory CI runs it in: a gate is the command
    # AND the directory, and two spellings of it are what tools/gatecheck
    # exists to catch.
    go run ./tools/installcheck . web || npm --prefix web ci --no-audit --no-fund
    npm --prefix web run openapi:check --workspace apps/api
    git diff --exit-code -- \
      THIRD_PARTY_NOTICES.md \
      www/public/errors.v1.json \
      engine/internal/errors/codes.gen.go \
      docs/src/content/docs/reference/errors.md \
      www/public/lint-findings.v1.json \
      engine/internal/insights/findings.gen.go \
      engine/internal/insights/findings.register.json \
      docs/src/content/docs/reference/lint-findings.md \
      engine/internal/proxyimage/sources.gen.go \
      schemas/policy-vectors.json \
      schemas/mockpack-vectors.json \
      schemas/webhook-vectors.json \
      schemas/events.v1.json \
      engine/internal/events/stream.register.json \
      docs/src/content/docs/reference/cli.md \
      docs/src/content/docs/reference/transforms.md \
      docs/src/content/docs/guides/dashboard.md \
      engine/internal/hud/testdata \
      docs/src/content/docs/reference/schemas

# Regenerate and keep the result.
generate:
    go run ./tools/errgen
    go run ./tools/lintgen
    go run ./tools/installcheck . web || npm --prefix web ci --no-audit --no-fund
    npm --prefix web run openapi --workspace apps/api
    go run ./tools/proxysrc
    go run ./tools/schemadoc .
    go run ./tools/notices -out THIRD_PARTY_NOTICES.md
    cd engine && go test ./internal/policy -update-vectors
    cd engine && go test ./internal/mockpack -update-vectors
    cd engine && go test ./internal/webhook -update-vectors
    cd engine && go test ./internal/cli -update-reference
    cd engine && go test ./internal/events -update-schema
    go run ./tools/eventcheck -freeze .
    cd engine && go test ./internal/masking -update-transforms
    cd engine && go test ./internal/hud -update-frames

# This machine's own credential store, against the real thing.
#
# The same command the keyring workflow runs, which is what lets `just gate` and
# CI agree about it. What it actually exercises differs per platform and that is
# the point: macOS runs the keychain tests here, Linux runs the Secret Service
# ones, and Windows runs the Credential Manager ones. No single machine can run
# all three, which is why that workflow has a job per platform, and why running
# the local one here is the most a developer's gate can honestly do.
#
# A machine with no keyring daemon skips rather than fails. That is correct: the
# chain's whole design is that an unavailable source is named and stepped over.
#
# -count=1 because it was NOT the same command, which made the sentence above
# false. keyring.yml:68 has always passed -count=1 and this did not, and the
# test reads the operating system's credential store, which is as far outside
# the module as a dependency gets, so nothing it touches is anything the cache
# watches. Measured: 20.677s, then `ok (cached)` on the second run. A gate that
# certifies this machine's keychain works, by not looking at the keychain.
keyring:
    cd engine && go test ./internal/secrets/ -count=1

# Lint the code the other platforms compile.
#
# The main lint runs on one machine, so it only ever sees the files that
# machine's build tags select. keyring_windows.go and keyring_darwin.go are
# invisible to it, and a file nothing lints drifts: GOOS=windows found an
# unchecked return in the Windows keyring that had been merged and green for a
# day, because no linter on any runner had ever compiled it.
#
# Cross compiling for the lint costs nothing. It needs no runner of that
# platform, since it type checks rather than runs.
lint-platforms:
    cd engine && GOOS=windows golangci-lint run --timeout 15m
    cd engine && GOOS=darwin golangci-lint run --timeout 15m

# The community build does not contain or need the enterprise edition.
edition:
    #!/usr/bin/env bash
    set -euo pipefail
    if grep -rn --include='*.go' 'antifailure/antifailure/ee' engine tools; then
      echo "an engine package imports ee, which the community build does not have"
      exit 1
    fi
    if grep -rn --include='*.ts' --include='*.json' -e 'antifailure-ee' -e 'ee/web' web/apps web/packages; then
      echo "the community web references enterprise code"
      exit 1
    fi
    cd engine && CGO_ENABLED=0 go build -o /tmp/af-community ./cmd/af
    if strings /tmp/af-community | grep -E 'antifailure/(antifailure/)?ee/'; then
      echo "the community binary contains enterprise package paths"
      exit 1
    fi
    echo "no enterprise symbols in the community binary"

# New commits are attributed correctly and signed off.
authorship:
    #!/usr/bin/env bash
    set -uo pipefail
    base=$(git merge-base origin/main HEAD 2>/dev/null || echo "HEAD~1")
    bad=$(git log --format='%H %an <%ae>' "$base..HEAD" | grep -F 'potatogreenbean204@gmail.com' || true)
    if [ -n "$bad" ]; then
      echo "These commits are authored by an address belonging to a different GitHub account:"
      echo "$bad"
      echo "  git config user.email 67278851+VirSanghavi@users.noreply.github.com"
      echo "  git commit --amend --reset-author --no-edit"
      exit 1
    fi
    missing=0
    for sha in $(git rev-list "$base..HEAD"); do
      [ "$(git rev-list --no-walk --count --merges "$sha")" = "1" ] && continue
      if ! git log -1 --format='%B' "$sha" | grep -q '^Signed-off-by: '; then
        echo "no sign-off: $(git log -1 --format='%h %s' "$sha")"
        missing=$((missing+1))
      fi
    done
    if [ "$missing" -gt 0 ]; then
      echo "Add one with 'git commit -s', or turn the hook on once: just hooks"
      exit 1
    fi
    echo "attributed and signed off"

# Anything a user can see says what changed, and the fragments still parse.
#
# CONTRIBUTING.md has promised this gate since the first week and there was
# none. The sign-off rule went the same way: required by the same document,
# unchecked, and 65 of the first 80 commits had no trailer. Eight of the twenty
# product changes since the fragment convention began landed without one.
#
# Runs the same range CI does, so a contributor finds out here rather than
# twenty minutes later. Locally that range is where this branch left main.
changecheck:
    go run ./tools/changecheck .

# Nothing this repository created is still running.
leaks:
    #!/usr/bin/env bash
    left=$(docker ps -aq --filter label=dev.antifailure.managed | wc -l | tr -d ' ')
    nets=$(docker network ls -q --filter label=dev.antifailure.managed | wc -l | tr -d ' ')
    echo "containers: $left, networks: $nets"
    if [ "$left" != "0" ] || [ "$nets" != "0" ]; then
      docker ps -a --filter label=dev.antifailure.managed
      echo "something was left behind"
      exit 1
    fi

# Remove build output and gate reports.
clean:
    rm -rf bin {{reports}}
    @echo "clean"

# Known vulnerabilities that our code can actually reach.
#
# Not part of `just gate`, and that is deliberate rather than an oversight. The
# answer comes from the Go vulnerability database, so it is not a function of
# this tree: the same commit is clean today and not clean tomorrow. `just gate`
# promises that green here means green in CI, and a scan whose input moves under
# it cannot keep that promise in either direction. It also needs the network.
#
# security.yml runs it on every pull request and again every morning, which is
# where a check like this belongs. Run it here whenever you change a dependency.
vuln:
    go run ./tools/vulncheck .

# The same question for the JavaScript, which nothing was asking.
#
# govulncheck covers the Go modules. Every `npm ci` in ci.yml passes --no-audit,
# so the seven lockfiles here, one of them the control plane that faces the
# internet, had no advisory check at all. tools/npmaudit runs `npm audit`
# against each and holds the result to .npmaudit.yaml the way vulncheck holds
# govulncheck's to .govulncheck.yaml: an advisory with no written decision fails,
# and so does a decision past its expiry or one that matches nothing.
#
# Out of `just gate` for exactly vuln's reason: the answer comes from the
# registry's advisory database rather than from this tree, and `just gate` has
# to work on a plane. security.yml runs it beside vuln, on every pull request
# and again every morning.
npmaudit:
    go run ./tools/npmaudit .

# The linter set CONTRIBUTING describes.
#
# Part of `just gate` since the count reached zero. It was kept out while there
# were findings that predated the config, because a gate that fails every
# branch for something none of them did is one people learn to route around.
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v golangci-lint > /dev/null; then
      echo "golangci-lint is not installed. brew install golangci-lint"
      exit 1
    fi
    cd engine
    # verify before run. `run` does not validate the config, so an invalid one
    # passes locally and fails the moment CI's action verifies it, which is
    # exactly what happened with an empty misspell key.
    golangci-lint config verify
    golangci-lint run --timeout 15m ./...
