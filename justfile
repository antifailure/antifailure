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

    echo "Gates"
    run "generated files are current" just _generated
    run "release stamps a real version"  just ldcheck
    run "error catalog and code agree"   just errcheck
    run "no credential in the tree"      just scanrepo
    run "commands in the docs exist"     just docexamples
    run "gate matches CI"                just gatecheck
    run "vet"                            just vet
    run "typecheck"                      just typecheck
    run "format"                         just fmt-check
    run "the gates themselves"           just test-tools
    run "engine"                         just test-engine
    run "control plane"                  just test-web
    run "runner"                         just test-runner
    run "edition boundary"               just edition
    run "enterprise"                     just test-ee
    run "license parser fuzz"            just fuzz-license
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
    echo "Repository"
    printf '  %-12s' "hooks"
    if [ "$(git config core.hooksPath || true)" = ".githooks" ]; then
      echo "on"
    else
      echo "off            run: git config core.hooksPath .githooks"
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

# Start the Postgres the control plane suites need.
db:
    @docker rm -f af-cp-test > /dev/null 2>&1 || true
    docker run -d --name af-cp-test -p 55432:5432 \
      -e POSTGRES_PASSWORD=test -e POSTGRES_DB=antifailure postgres:17-alpine
    @echo "waiting for postgres"
    @for i in $(seq 1 60); do nc -z 127.0.0.1 55432 && break || sleep 1; done
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
      -X github.com/antifailure/antifailure/engine/internal/cli.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      -o ../bin/af ./cmd/af
    @./bin/af version

# ---------------------------------------------------------------------------
# Test
# ---------------------------------------------------------------------------

# The unit and property tests, everywhere.
test: test-engine test-tools test-web test-runner

test-engine:
    cd engine && go test ./... -race -timeout 30m

test-tools:
    cd tools && go test ./... -timeout 5m

test-web:
    npm --prefix web test --workspaces --if-present

test-runner:
    npm --prefix runner test

test-ee:
    cd ee/engine && GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./... -race -timeout 15m
    npx --prefix ee/web tsc --noEmit -p ee/web/rbac/tsconfig.json
    npx --prefix ee/web tsc --noEmit -p ee/web/audit/tsconfig.json
    node --test ee/web/rbac/test/*.test.ts ee/web/audit/test/*.test.ts

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

# This justfile runs what CI runs.
gatecheck:
    go run ./tools/gatecheck .

# The TypeScript that ships: the control plane packages and the agent runner.
typecheck:
    #!/usr/bin/env bash
    set -euo pipefail
    for p in packages/db packages/policy apps/api; do
      npx --prefix web tsc --noEmit -p "web/$p/tsconfig.json"
    done
    npx --prefix runner tsc --noEmit -p runner/tsconfig.json

# The license parser has to survive arbitrary input, because a licence token
# arrives from outside and a parser that panics on one is a denial of service
# with extra steps. Sixty seconds here, longer in a nightly run.
fuzz-license seconds="60":
    cd ee/engine && GOWORK=off go test ./license -run FuzzParse -fuzz FuzzParse -fuzztime {{seconds}}s

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
    (cd engine && go test ./internal/policy -update-vectors)
    (cd engine && go test ./internal/cli -update-reference)
    git diff --exit-code -- \
      engine/internal/errors/codes.gen.go \
      engine/internal/proxyimage/sources.gen.go \
      schemas/policy-vectors.json \
      docs/src/content/docs/reference/cli.md

# Regenerate and keep the result.
generate:
    go run ./tools/errgen
    go run ./tools/proxysrc
    cd engine && go test ./internal/policy -update-vectors
    cd engine && go test ./internal/cli -update-reference

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
      echo "Add one with 'git commit -s', or: git config core.hooksPath .githooks"
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
