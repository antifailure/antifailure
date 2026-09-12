#!/usr/bin/env bash
set -uo pipefail
R=/private/tmp/af-db-managed-mit
PG="postgres://postgres@127.0.0.1:55731/postgres"
step() { echo; echo "##### $* #####"; }

step "build engine"
(cd "$R/engine" && go build ./...); echo "build exit=$?"

step "cli start rung, after the pgurlHost fix"
(cd "$R/engine" && go test ./internal/cli/ -count=1 -run 'TestStart_PgURL' -v 2>&1 | grep -E "^(=== RUN|--- |ok |FAIL|PASS)" ); echo "cli exit=${PIPESTATUS[0]}"

step "xata provider, fake control plane over a real local Postgres"
(cd "$R/engine" && AF_XATA_TEST_DATABASE_URL="$PG" AF_REQUIRE_DATABASE=1 \
   go test ./internal/db/xata/ -count=1 -v 2>&1 | grep -E "^(=== RUN|--- |ok |FAIL|PASS|\s+xata_test)" | head -120)
echo "xata exit=${PIPESTATUS[0]}"

step "env and manifest still build and pass their own tests"
(cd "$R/engine" && go test ./internal/manifest/ ./internal/env/ -count=1 -run 'TestValidate|TestProviderSelection|Provider' 2>&1 | tail -20)
echo "envmanifest exit=${PIPESTATUS[0]}"

step "generated artifacts"
(cd "$R" && go run ./tools/errgen); echo "errgen exit=$?"
(cd "$R" && go run ./tools/schemadoc .); echo "schemadoc exit=$?"
(cd "$R" && go run ./tools/docsembed); echo "docsembed exit=$?"

step "gates that touch what I changed"
(cd "$R" && go run ./tools/claimcheck .) 2>&1 | tail -3; echo "claimcheck exit=${PIPESTATUS[0]}"
(cd "$R" && go run ./tools/varcheck .) 2>&1 | tail -3; echo "varcheck exit=${PIPESTATUS[0]}"
(cd "$R" && go run ./tools/errcheck .) 2>&1 | tail -3; echo "errcheck exit=${PIPESTATUS[0]}"
(cd "$R" && go run ./tools/sidebarcheck .) 2>&1 | tail -3; echo "sidebarcheck exit=${PIPESTATUS[0]}"
(cd "$R" && go run ./tools/gatecheck .) 2>&1 | tail -3; echo "gatecheck exit=${PIPESTATUS[0]}"

step "MUTATION TABLE"
(cd "$R" && python3 .lane-mutate.py) 2>&1 | tail -200; echo "mutate exit=${PIPESTATUS[0]}"

# The benchmark was cut from this hold. The lead asked that nobody take the
# lock for a measurement while ten lanes are queued behind a possibly hung
# holder, and a measurement taken on a machine in that state is a reading of
# the machine anyway.

echo; echo "##### LOOP2 DONE #####"
