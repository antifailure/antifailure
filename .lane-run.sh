#!/usr/bin/env bash
set -uo pipefail
R=/private/tmp/af-db-managed-mit
PG="postgres://postgres@127.0.0.1:55731/postgres"
step() { echo; echo "##### $* #####"; }

step errgen
(cd "$R" && go run ./tools/errgen) ; echo "errgen exit=$?"

step "build engine"
(cd "$R/engine" && go build ./...) ; echo "build exit=$?"

step vet
(cd "$R/engine" && go vet ./internal/db/managed/ ./internal/db/pgurl/ ./internal/cli/) ; echo "vet exit=$?"

step "managed registry tests"
(cd "$R/engine" && go test ./internal/db/managed/ -count=1 -v 2>&1 | tail -40) ; echo "managed exit=${PIPESTATUS[0]}"

step "errors package"
(cd "$R/engine" && go test ./internal/errors/ -count=1) ; echo "errors exit=$?"

step "pgurl vendor refusal"
(cd "$R/engine" && AF_PGURL_ADMIN_URL="$PG" AF_REQUIRE_DATABASE=1 \
  go test ./internal/db/pgurl/ -count=1 -v -run 'TestARefusal|TestAnUnverified|TestTheRoleProbe' 2>&1 | tail -40) ; echo "pgurl exit=${PIPESTATUS[0]}"

step "cli start rung"
(cd "$R/engine" && go test ./internal/cli/ -count=1 -run 'TestStart_PgURL' -v 2>&1 | tail -40) ; echo "cli exit=${PIPESTATUS[0]}"

echo; echo "##### LOOP DONE #####"
