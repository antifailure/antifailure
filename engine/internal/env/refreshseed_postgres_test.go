package env

// `af golden refresh` HAS TO RUN THE SEED, AND AN EMPTY GOLDEN HAS TO BE
// REFUSED RATHER THAN ATTESTED.
//
// Two commands make a golden and they did not make the same one. `af up` on a
// project with no golden yet ran database.seed; `af golden refresh` on the
// same project did not, so it published a golden holding no tables, reported
// "0 rows across 0 tables masked", said "Verified 0 columns across 0 tables",
// exited 0 and printed "Bring an environment up from it with: af up". That
// golden's provenance is this project's, so the next `af up` selected it and
// branched an empty database. Reproduced on bf40d7d3a before the fix.
//
// The second test here is the independent guard rather than the same bug
// twice. Running the seed closes the route that produced this one. It closes
// only that route: a seed that exits 0 having written nothing, or a source
// database that turns out to be empty, both arrive at the same published,
// signed, empty golden, and the word this product asks people to rely on is
// verified.
//
// Against a real Postgres for the reason the file beside this one is: a fake
// agrees with whatever the hooks say, and what the hooks said was the problem.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// refreshOrchestrator is a project whose only source of data is its seed
// command, which is the shape the manifest validator allows: it refuses
// database.seed beside database.source_url_env.
func refreshOrchestrator(t *testing.T, root, seed string) *Orchestrator {
	t.Helper()
	o, err := New(Options{
		Root:     root,
		Manifest: &schema.Manifest{Name: "app", Database: &schema.Database{Seed: seed}},
		Branch:   "main",
		Clock:    clock.New(),
		Redactor: redact.New(),
		Getenv: func(k string) string {
			if k == MaskingKeyEnv {
				return "a-project-key-long-enough-to-be-accepted"
			}
			return ""
		},
	})
	require.NoError(t, err)
	return o
}

// refreshHooks builds the spec `af golden refresh` builds and hands back its
// two hooks, the same way seedSpec does for the `af up` path.
func refreshHooks(t *testing.T, o *Orchestrator) (specHooks, *GoldenResult) {
	t.Helper()
	key, err := o.MaskingKey(context.Background(), nil)
	require.NoError(t, err)
	rules, hash, err := o.rules()
	require.NoError(t, err)
	prov, err := o.provenanceOf()
	require.NoError(t, err)
	result := &GoldenResult{}
	// No source URL, because a manifest naming a seed may not name one.
	spec := o.refreshGoldenSpec(nil, key, rules, hash, prov, secrets.Value{}, result)
	require.NotNil(t, spec.Mask, "the refresh must carry a mask hook")
	require.NotNil(t, spec.Verify, "the refresh must carry a verify hook")
	return specHooks{mask: spec.Mask, verify: spec.Verify}, result
}

// requirePsql skips when there is no client for the seed command to use, and
// fails instead when the suite was told a database is required, so that this
// cannot report ok having run nothing.
func requirePsql(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("psql")
	if err == nil {
		return path
	}
	if os.Getenv("AF_REQUIRE_DATABASE") != "" {
		t.Fatalf("AF_REQUIRE_DATABASE is set and there is no psql for the seed command to run: %v", err)
	}
	t.Skipf("no psql for the seed command to run: %v", err)
	return ""
}

// TestRefreshGoldenRunsTheManifestsSeed.
//
// The seed writes a table and a marker file, and both are asserted, because
// the two answer different questions. The marker says the command ran at all
// and against which database, which is the defect: nothing called runSeed on
// this path. The table says the data reached the golden candidate, which is
// what a person branching it actually gets.
func TestRefreshGoldenRunsTheManifestsSeed(t *testing.T) {
	requirePsql(t)
	url := seedCandidate(t)

	root := t.TempDir()
	marker := filepath.Join(root, "seed-ran")
	seed := `printf '%s' "$DATABASE_URL" > seed-ran && ` +
		`psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c ` +
		`"CREATE TABLE customers (id int primary key, email text); ` +
		`INSERT INTO customers VALUES (1, 'ada@example.com'), (2, 'grace@example.org');"`

	o := refreshOrchestrator(t, root, seed)
	hooks, result := refreshHooks(t, o)

	ctx := context.Background()
	require.NoError(t, hooks.mask(ctx, secrets.New(url)),
		"the refresh could not seed and mask its candidate")

	ran, err := os.ReadFile(marker)
	require.NoError(t, err,
		"af golden refresh did not run database.seed, so a project whose only data comes from "+
			"that command gets a golden with nothing in it and no indication of why")
	require.Equal(t, url, string(ran),
		"the seed ran against a database other than the candidate the golden is committed from")

	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	var rows int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM customers`).Scan(&rows))
	require.Equal(t, 2, rows, "the seed ran and its rows are not in the candidate")

	// And the golden is one the run can describe honestly afterwards. Masking
	// reports what it rewrote, and a report of nothing is what the empty
	// golden also produced, which is why the guard below reads the scan
	// instead of this.
	att, err := hooks.verify(ctx, secrets.New(url))
	require.NoError(t, err, "a seeded golden of synthetic addresses must verify")
	require.NotEmpty(t, att)
	require.Greater(t, result.Report.Tables, 0, "the scan read no tables out of a seeded golden")
}

// TestAGoldenThatHoldsNothingIsRefusedRatherThanAttested.
//
// The independent guard, through the real Verify hook against a real empty
// database. The candidate here is what the defect produced: a Postgres with no
// user tables in it. Everything about that database is clean, because there is
// nothing in it to find, so verification passes and signs an attestation over
// zero tables, and every reader downstream sees the word verified.
func TestAGoldenThatHoldsNothingIsRefusedRatherThanAttested(t *testing.T) {
	url := seedCandidate(t)

	o := refreshOrchestrator(t, t.TempDir(), "./seed.sh")
	hooks, _ := refreshHooks(t, o)

	_, err := hooks.verify(context.Background(), secrets.New(url))
	require.Error(t, err,
		"a golden holding no tables at all was published as verified, and the manifest says "+
			"where its contents were supposed to come from")
	require.Equal(t, aferrors.AFDB041, codeOf(err))
	require.Contains(t, err.Error(), "database.seed",
		"the refusal does not name the key that said this golden should hold data")
}

// TestAProjectThatDeclaresNoSourceAndNoSeedStillGetsItsEmptyGolden.
//
// The narrowness, and the reason the guard reads the manifest rather than the
// table count alone. An empty golden is a supported thing to want: a project
// that has not connected production gets the schema its migrations build and
// no rows, `af explain` says so, and provenance.empty names that state. A
// guard that refused every empty golden would make that project impossible to
// bring up, which is a worse failure than the one it was written for.
func TestAProjectThatDeclaresNoSourceAndNoSeedStillGetsItsEmptyGolden(t *testing.T) {
	url := seedCandidate(t)

	o := refreshOrchestrator(t, t.TempDir(), "")
	hooks, _ := refreshHooks(t, o)

	att, err := hooks.verify(context.Background(), secrets.New(url))
	require.NoError(t, err,
		"a project that declares neither a source nor a seed was refused its own empty golden")
	require.NotEmpty(t, att)
}

// TestSeededGolden_AnEmptyGoldenIsRefusedOnTheUpPathToo.
//
// The guard on the other door. `af up` with no golden yet builds one through
// seedGoldenSpec rather than through the refresh, and which of the two a
// project reaches is decided by whether it has a golden already, which is not
// a property anybody chooses. A guard on one of them is a guard that half the
// runs walk past.
func TestSeededGolden_AnEmptyGoldenIsRefusedOnTheUpPathToo(t *testing.T) {
	url := seedCandidate(t)

	o := seedOrchestrator(t, "./seed.sh")
	hooks := seedSpec(t, o, "./seed.sh")

	_, err := hooks.verify(context.Background(), secrets.New(url))
	require.Error(t, err,
		"af up published an empty golden as verified for a project whose manifest names a seed")
	require.Equal(t, aferrors.AFDB041, codeOf(err))
}

// TestRefuseEmptyGoldenReadsTablesAndNotRows.
//
// The cells the two tests above cannot reach cheaply, and the one distinction
// the guard turns on. The masker's row count is 0 for a database full of data
// and no rules, so it cannot tell empty from unmasked; the scan's table count
// is 0 only when there is not one user table. A seed that creates tables and
// inserts nothing is a judgement about somebody's seed and is not refused.
func TestRefuseEmptyGoldenReadsTablesAndNotRows(t *testing.T) {
	seeded := provenance{Project: "app", Seed: "./seed.sh"}
	sourced := provenance{Project: "app", Source: "DATABASE_URL"}
	declared := provenance{Project: "app"}

	require.Error(t, refuseEmptyGolden(seeded, verify.Report{Tables: 0}))
	require.Error(t, refuseEmptyGolden(sourced, verify.Report{Tables: 0}))
	require.Contains(t, refuseEmptyGolden(sourced, verify.Report{Tables: 0}).Error(),
		"database.source_url_env",
		"a source that copied nothing is told to look at database.seed")

	require.NoError(t, refuseEmptyGolden(seeded, verify.Report{Tables: 1}),
		"a seed that made a table and inserted no rows is a judgement about somebody's seed")
	require.NoError(t, refuseEmptyGolden(declared, verify.Report{Tables: 0}),
		"a project that declares neither was refused the empty golden it asked for")
}
