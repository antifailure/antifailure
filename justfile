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
    run "generated files are current" just _generated
    run "release stamps a real version"  just ldcheck
    run "error catalog and code agree"   just errcheck
    run "no credential in the tree"      just scanrepo
    run "commands in the docs exist"     just docexamples
    run "documented paths exist"         just claimcheck
    run "documented manifests are valid" just manifestcheck
    run "prose reads like a person"      just prosecheck
    run "no forbidden tokens in docs"    just forbidden
    run "spelling"                       just spell
    run "prose style"                    just vale
    run "every link resolves"            just links
    run "prose stays readable"           just readability
    run "the examples still compile"     just examples
    run "gate matches CI"                just gatecheck
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
    run "runner"                         just test-runner
    run "edition boundary"               just edition
    run "enterprise"                     just test-ee
    run "builds are reproducible"        just reproducible
    run "license parser fuzz"            just fuzz-license
    run "engine parser fuzz"             just fuzz-engine
    run "authorship and sign-off"        just authorship

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
    need go     "go1.25"  "https://go.dev/dl/ , or: brew install go"           go version
    need node   "v24"     "https://nodejs.org/ , or: brew install node@24"     node --version
    need npm    ""        "ships with node"                                    npm --version
    need docker ""        "https://docs.docker.com/get-docker/"                docker --version
    need git    ""        "brew install git"                                   git --version

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
    npm --prefix web ci --no-audit --no-fund
    npm --prefix runner ci --no-audit --no-fund

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
build-release version="dev":
    @mkdir -p bin
    cd engine && go build -trimpath -ldflags "-s -w \
      -X github.com/antifailure/antifailure/engine/internal/cli.Version={{version}} \
      -X github.com/antifailure/antifailure/engine/internal/cli.Commit=$(git rev-parse HEAD) \
      -X github.com/antifailure/antifailure/engine/internal/cli.BuildDate=$(git show -s --format=%cI HEAD)" \
      -o ../bin/af ./cmd/af
    @./bin/af version

# ---------------------------------------------------------------------------
# Test
# ---------------------------------------------------------------------------

# The unit and property tests, everywhere.
test: test-engine test-tools test-web test-runner

test-engine:
    cd engine && go test ./... -race -timeout 30m

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
    cd engine && go test ./... -coverpkg=./... -coverprofile=../{{reports}}/coverage.out -timeout 60m

coverage:
    go run ./tools/coverage -profile {{reports}}/coverage.out

test-tools:
    cd tools && go test ./... -timeout 5m

test-web:
    npm --prefix web test --workspaces --if-present

test-runner:
    npm --prefix runner test

# Fanned out over ee/web's workspaces rather than naming each package, so an
# enterprise package added later is covered without editing this or CI. Naming
# them by hand is how two of them ended up untested.
test-ee:
    cd ee/engine && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -race -timeout 15m
    npm --prefix ee/web run typecheck
    npm --prefix ee/web test

# The fast ones, for a tight loop.
test-short:
    cd engine && go test ./... -short -timeout 10m

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

# The release stamps version variables that exist.
ldcheck:
    go run ./tools/ldcheck .

# Nothing in the tree looks like a live credential.
scanrepo:
    go run ./tools/scanrepo .

# Every af command shown in the docs is a command that exists.
docexamples:
    cd engine && go test ./internal/cli -run TestEveryCommandInTheDocsExists

# The punctuation this project does not use.
prosecheck:
    go run ./tools/prosecheck .

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
    rm -rf site && mkdir -p site
    cp -R www/out/. site/
    cp -R docs/dist site/docs
    lychee --config lychee.toml --no-progress --offline --root-dir site 'site/**/*.html'

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
    go run ./tools/readability . --max 28

# The G8 forbidden token scan: notes to the author, unfilled slots, names that
# belong to a person rather than the product, addresses that resolve only on
# somebody's private network, and identifiers that name a real cloud tenant.
forbidden:
    ./tools/docs/forbidden.sh

# Every repository path our documents point at exists.
claimcheck:
    go run ./tools/claimcheck .

# Every manifest shown in the documentation is one the engine would accept.
#
# The gates already read style, spelling, links and repository paths, and none
# of them knows what a manifest is. A getting started page shipped telling
# readers to set control_plane.url, which the engine refuses with AF-MAN-002
# because the schema closes itself so a typo cannot silently change an
# environment.
manifestcheck:
    go run ./tools/manifestcheck .

# This justfile runs what CI runs.
gatecheck:
    go run ./tools/gatecheck .

# The TypeScript that ships: the control plane packages, the agent runner, and
# the console.
typecheck:
    #!/usr/bin/env bash
    set -euo pipefail
    for p in packages/db packages/policy apps/api; do
      npx --prefix web tsc --noEmit -p "web/$p/tsconfig.json"
    done
    npx --prefix runner tsc --noEmit -p runner/tsconfig.json
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
    [ -d console/node_modules ] || npm --prefix console ci --no-audit --no-fund
    NEXT_TELEMETRY_DISABLED=1 npm --prefix console run build

# G11. Two builds of one commit produce the same binary.
#
# It did not. The release recipe stamped BuildDate with $(date -u), so every
# build of the same commit differed, and release.yml carried the identical
# line: the shipping path was non-reproducible, not just the local one. The
# date comes from the commit now, which is deterministic and still means
# something to whoever reads `af version`.
#
# A gate rather than a note, because this is the kind of property that is true
# until somebody adds one more -X flag.
reproducible version="v0.0.0-gate":
    #!/usr/bin/env bash
    set -euo pipefail
    just build-release {{version}} > /dev/null
    a=$(shasum -a 256 bin/af | cut -d' ' -f1)
    just build-release {{version}} > /dev/null
    b=$(shasum -a 256 bin/af | cut -d' ' -f1)
    if [ "$a" != "$b" ]; then
      echo "two builds of this commit differ:"
      echo "  $a"
      echo "  $b"
      echo
      echo "Something in the build embeds the moment it ran. The usual cause is a"
      echo "timestamp in an -X flag; derive it from the commit instead."
      exit 1
    fi
    echo "two builds of this commit are identical: $a"

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
    go run ./tools/proxysrc
    go run ./tools/schemadoc .
    (cd engine && go test ./internal/policy -update-vectors)
    (cd engine && go test ./internal/cli -update-reference)
    (cd engine && go test ./internal/events -update-schema)
    (cd engine && go test ./internal/masking -update-transforms)
    (cd engine && go test ./internal/hud -update-frames)
    git diff --exit-code -- \
      engine/internal/errors/codes.gen.go \
      docs/src/content/docs/reference/errors.md \
      engine/internal/proxyimage/sources.gen.go \
      schemas/policy-vectors.json \
      schemas/events.v1.json \
      docs/src/content/docs/reference/cli.md \
      docs/src/content/docs/reference/transforms.md \
      docs/src/content/docs/guides/dashboard.md \
      engine/internal/hud/testdata \
      docs/src/content/docs/reference/schemas

# Regenerate and keep the result.
generate:
    go run ./tools/errgen
    go run ./tools/proxysrc
    go run ./tools/schemadoc .
    cd engine && go test ./internal/policy -update-vectors
    cd engine && go test ./internal/cli -update-reference
    cd engine && go test ./internal/events -update-schema
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
keyring:
    cd engine && go test ./internal/secrets/

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
