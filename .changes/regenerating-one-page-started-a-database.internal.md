# fixed

Regenerating the checked in files started Postgres and ClickHouse containers to
rewrite one documentation page.

`tools/gendrift` ran `go test ./internal/masking -update-transforms` with no
`-run`. Only TestTransformReferenceIsCurrent reads that flag, and the package
holds 91 tests, several of which make a golden, branch it, and start a
ClickHouse server. So `just generate`, `just gate` and the engine job's
generator step all waited on Docker for a page the transform registry alone
decides. One lane's regeneration on 2026-09-13 spent over seven minutes there,
and on 2026-09-09 a wedged daemon hung the whole generate.

The generator now runs that one test. Nothing stopped being tested: the engine
job's Test step already ran the whole masking package a second time, through
`tools/enginetest` with `AF_REQUIRE_DOCKER` and `AF_REQUIRE_DATABASE` set, and
run 34768933346, job 103754836396, shows it passing in both places, 61 seconds
in the generator step and 133 in the Test step.

A scoped generator has a failure of its own. `go test -run` with a pattern that
matches nothing exits 0 and writes nothing, so renaming the test would leave a
generator that always succeeds and never rewrites the page. A new gendrift test
refuses any ledger command whose `-run` is not a single anchored name declared
in its package.
