package masking_test

// A ROW IS ADDRESSED BY ITS WHOLE PRIMARY KEY, AND THE ADDRESS USES THE INDEX.
//
// The executor used to read, page and rewrite a table through the first column
// of its primary key alone, compared as text. For a table whose key is
// (tenant_id, id) that has two consequences, and these tests are about both.
//
// The first is a privacy and correctness defect. The per row UPDATE matched on
// tenant_id alone, so writing one row's masked value rewrote every row of that
// tenant with it, and the page after a chunk resumed after the tenant, so the
// rest of that tenant's rows were never visited on their own. Every masked
// value in the table stops meaning the row it is in.
//
// The second is speed. A key cast to text is an expression no btree serves, so
// every chunk read and every per row UPDATE was a sequential scan.

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// compositeSchema holds three tenants of twelve contacts each, keyed on
// (tenant_id, id), and a twin keyed on one unique column holding the same
// addresses. Twelve rather than nine, so an id past 9 sorts differently as text
// ("10" before "2") than as a number: a read ordered by the text of the key and
// bounded by the key as stored pages past real rows, and only data where the two
// orders differ can show it. The twin is the oracle: its key is a single unique column, which
// the executor has always addressed correctly, and the email link masks an
// equal input to an equal output, so a contact row masked exactly once holds
// exactly what its twin holds.
const compositeSchema = `
CREATE TABLE tenant_contacts (
  tenant_id integer NOT NULL,
  id        integer NOT NULL,
  email     text NOT NULL,
  PRIMARY KEY (tenant_id, id)
);
CREATE TABLE contact_twins (
  uid   text PRIMARY KEY,
  email text NOT NULL
);
INSERT INTO tenant_contacts (tenant_id, id, email)
  SELECT t, i, 'person' || t || '.' || i || '@realcorp' || t || '.com'
  FROM generate_series(1, 3) t, generate_series(1, 12) i;
INSERT INTO contact_twins (uid, email)
  SELECT tenant_id || '/' || id, email FROM tenant_contacts;
`

func TestApply_ACompositeKeyMasksEveryRowExactlyOnce(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	_, err := conn.Exec(ctx, compositeSchema)
	require.NoError(t, err)

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "test")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))

	// A chunk smaller than one tenant's rows, so a chunk boundary falls inside a
	// tenant and the next page has to resume in the middle of one.
	found := false
	for i := range plan.Tables {
		if plan.Tables[i].Table.Name == "tenant_contacts" {
			require.Equal(t, []string{"tenant_id", "id"}, plan.Tables[i].OrderBy)
			plan.Tables[i].ChunkSize = 3
			found = true
		}
	}
	require.True(t, found, "the plan does not mask tenant_contacts, so nothing below is measured")

	key, err := masking.NewKeyFromBytes([]byte("a-test-master-key-for-masking-000"))
	require.NoError(t, err)
	exec, err := masking.NewExecutor(masking.ExecutorOptions{Key: key, Clock: clock.New()})
	require.NoError(t, err)
	_, err = exec.Apply(ctx, conn, plan)
	require.NoError(t, err)

	report, verr := verify.Scan(ctx, conn, verify.Options{})
	t.Logf("golden verify on the masked tables: err=%v, findings=%d", verr, len(report.Findings))
	for _, f := range report.Findings {
		t.Logf("  finding %s.%s.%s by %s, %d rows", f.Schema, f.Table, f.Column, f.Detector, f.Rows)
	}

	require.Equal(t, int64(36), queryOne(t, conn, `SELECT count(*) FROM tenant_contacts`))

	require.Zero(t, queryOne(t, conn,
		`SELECT count(*) FROM tenant_contacts WHERE email LIKE '%@realcorp%'`),
		"a row still holds the address production had, so the golden ships real data")

	require.Zero(t, queryOne(t, conn,
		`SELECT count(*) FROM tenant_contacts c JOIN contact_twins w ON w.uid = c.tenant_id || '/' || c.id
		 WHERE c.email <> w.email`),
		"a row holds a masked value that is not its own, so it was rewritten with another row's "+
			"value and every join on it now finds the wrong person")
	// No count of distinct addresses after this. Every twin is a distinct person
	// masked once, so a table that matches its twins row for row cannot hold fewer
	// distinct addresses than they do, and a check no break can redden on its own
	// is not a check.
}

// memoryCheckpoints is a Checkpointer held in a map, which is all the executor's
// resume path needs to be exercised against a real database.
type memoryCheckpoints struct{ saved map[string]string }

func (m *memoryCheckpoints) Save(_ context.Context, table, key string) error {
	m.saved[table] = key
	return nil
}

func (m *memoryCheckpoints) Load(_ context.Context, table string) (string, bool, error) {
	v, ok := m.saved[table]
	return v, ok, nil
}

func (m *memoryCheckpoints) Clear(context.Context) error {
	m.saved = map[string]string{}
	return nil
}

// planWithSmallChunks builds the plan for the composite schema with a chunk of
// three contacts, so a boundary falls inside a tenant.
func planWithSmallChunks(t *testing.T, conn *pgx.Conn) masking.Plan {
	t.Helper()
	ctx := context.Background()
	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "test")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))
	for i := range plan.Tables {
		if plan.Tables[i].Table.Name == "tenant_contacts" {
			plan.Tables[i].ChunkSize = 3
		}
	}
	return plan
}

func requireEveryContactMaskedExactlyOnce(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	require.Zero(t, queryOne(t, conn,
		`SELECT count(*) FROM tenant_contacts WHERE email LIKE '%@realcorp%'`),
		"a row still holds the address production had")
	require.Zero(t, queryOne(t, conn,
		`SELECT count(*) FROM tenant_contacts c JOIN contact_twins w ON w.uid = c.tenant_id || '/' || c.id
		 WHERE c.email <> w.email`),
		"a row holds a masked value that is not its own single masking")
}

// ORDERING: the run fails partway through a chunk, and a second run resumes.
//
// A trigger refuses the rewrite of one row in the middle of the second chunk.
// That chunk's transaction rolls back with the rows before it, the checkpoint
// still names the end of the first chunk, and a resumed run has to read the
// second chunk again from inside the tenant and mask every row exactly once.
func TestApply_ResumesAfterAFailureInsideAChunk(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()
	_, err := conn.Exec(ctx, compositeSchema+`
CREATE FUNCTION refuse_one_contact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF current_setting('masking_test.refuse', true) = 'on' AND NEW.tenant_id = 1 AND NEW.id = 5 THEN
    RAISE EXCEPTION 'refused contact 1/5 on purpose';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER refuse_one_contact BEFORE UPDATE ON tenant_contacts
  FOR EACH ROW EXECUTE FUNCTION refuse_one_contact();
SET masking_test.refuse = 'on';`)
	require.NoError(t, err)

	plan := planWithSmallChunks(t, conn)
	key, err := masking.NewKeyFromBytes([]byte("a-test-master-key-for-masking-000"))
	require.NoError(t, err)
	checkpoints := &memoryCheckpoints{saved: map[string]string{}}

	first, err := masking.NewExecutor(masking.ExecutorOptions{Key: key, Clock: clock.New(), Checkpoints: checkpoints})
	require.NoError(t, err)
	_, err = first.Apply(ctx, conn, plan)
	require.ErrorContains(t, err, "refused contact 1/5 on purpose", "the injected failure never happened")
	require.NotEmpty(t, checkpoints.saved["public.tenant_contacts"],
		"no chunk was checkpointed before the failure, so the resume below starts from nothing")

	_, err = conn.Exec(ctx, `SET masking_test.refuse = 'off'`)
	require.NoError(t, err)
	second, err := masking.NewExecutor(masking.ExecutorOptions{Key: key, Clock: clock.New(), Checkpoints: checkpoints})
	require.NoError(t, err)
	res, err := second.Apply(ctx, conn, plan)
	require.NoError(t, err)
	require.True(t, res.Resumed, "the second run started over rather than resuming")

	requireEveryContactMaskedExactlyOnce(t, conn)
}

// ORDERING: a row is written while the run is between chunks.
//
// A contact inserted with an address after the current position is inside the
// part of the key the run has not reached, so the same run masks it.
//
// Its twin is inserted BEFORE the run, not beside the contact. The plan masks
// tables in name order, so contact_twins is finished before tenant_contacts
// starts, and a twin written mid run is never masked: comparing the late contact
// with it measured the oracle, not the executor.
func TestApply_MasksARowInsertedAheadOfThePosition(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()
	_, err := conn.Exec(ctx, compositeSchema+`
INSERT INTO contact_twins (uid, email) VALUES ('3/99', 'late.arrival@realcorp3.com');`)
	require.NoError(t, err)

	plan := planWithSmallChunks(t, conn)
	key, err := masking.NewKeyFromBytes([]byte("a-test-master-key-for-masking-000"))
	require.NoError(t, err)

	inserted := false
	exec, err := masking.NewExecutor(masking.ExecutorOptions{
		Key: key, Clock: clock.New(),
		Progress: func(p masking.Progress) {
			if inserted || p.Finished || p.Table != "public.tenant_contacts" {
				return
			}
			inserted = true
			_, insertErr := conn.Exec(ctx,
				`INSERT INTO tenant_contacts (tenant_id, id, email) VALUES (3, 99, 'late.arrival@realcorp3.com')`)
			require.NoError(t, insertErr)
		},
	})
	require.NoError(t, err)
	_, err = exec.Apply(ctx, conn, plan)
	require.NoError(t, err)
	require.True(t, inserted, "no progress was reported for tenant_contacts, so nothing was inserted mid run")
	require.Equal(t, int64(37), queryOne(t, conn, `SELECT count(*) FROM tenant_contacts`))

	requireEveryContactMaskedExactlyOnce(t, conn)
}

// TestMaskingStatements_UseTheKeyIndex reads the plan Postgres chooses for the
// three statements a masking run repeats: the read that resumes a chunk and
// the per row UPDATE on a table with a composite key, and the UPDATE that
// addresses a row by ctid on a table with none.
//
// A sequential scan is made as expensive as the planner allows. A statement
// that can use the key's index then uses it, and one that cannot, because it
// compares the key through a cast, still scans. So the assertion does not
// depend on how many rows the table holds.
func TestMaskingStatements_UseTheKeyIndex(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	_, err := conn.Exec(ctx, `
CREATE TABLE wide_contacts (
  tenant_id integer NOT NULL,
  id        integer NOT NULL,
  email     text NOT NULL,
  PRIMARY KEY (tenant_id, id)
);
CREATE TABLE keyless_contacts (email text NOT NULL);
INSERT INTO wide_contacts SELECT t, i, 'p' || t || '.' || i || '@realcorp.com'
  FROM generate_series(1, 20) t, generate_series(1, 100) i;
INSERT INTO keyless_contacts SELECT email FROM wide_contacts;
ANALYZE wide_contacts;
ANALYZE keyless_contacts;`)
	require.NoError(t, err)

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "test")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))
	wide := tablePlanNamed(t, plan, "wide_contacts")
	keyless := tablePlanNamed(t, plan, "keyless_contacts")
	d, err := masking.DialectFor("postgres")
	require.NoError(t, err)

	_, err = conn.Exec(ctx, `SET enable_seqscan = off`)
	require.NoError(t, err)

	// The read has to come out of the index already in order. A Sort above an
	// Index Scan means the order is not the key's own, which is how a read that
	// sorts the text it selects looks with sequential scans priced out.
	resume := d.SelectChunk(wide, []string{"7", "7"})
	requireIndexed(t, conn, "the chunk read that resumes after a key", resume.SQL, resume.Args, "Index", true, true)

	update := wide.Compile().SQL
	requireIndexed(t, conn, "the per row UPDATE on a composite key", update, argsFor(update, "7"), "Index", false, true)

	byTid := keyless.Compile().SQL
	requireIndexed(t, conn, "the UPDATE that addresses a row by ctid", byTid, argsFor(byTid, "(0,1)"), "Tid Scan", false, false)
}

func tablePlanNamed(t *testing.T, plan masking.Plan, name string) masking.TablePlan {
	t.Helper()
	for _, tp := range plan.Tables {
		if tp.Table.Name == name {
			return tp
		}
	}
	t.Fatalf("the plan does not mask %s, so nothing about it is measured", name)
	return masking.TablePlan{}
}

// argsFor supplies one text argument per placeholder the statement carries,
// the first as the key and the rest as masked values. Planning needs their
// types and not meaningful values.
func argsFor(sql, key string) []any {
	n := 0
	for i := 1; strings.Contains(sql, "$"+strconv.Itoa(i)); i++ {
		n = i
	}
	args := make([]any, n)
	for i := range args {
		args[i] = key
	}
	return args
}

// requireIndexed explains a statement inside a transaction it rolls back, so an
// UPDATE is planned and never run.
// boundByIndex is the assertion this test was missing. An index in the plan is
// not the guarantee: the read orders by the key, so Postgres reaches for the
// index whatever the WHERE says, and a bound it cannot serve simply moves into
// a Filter above the scan. The plan then still shows an Index Scan, no Seq Scan
// and no Sort, and the whole page is read before the bound is applied. So the
// bound itself has to be the index condition, which is what keyset pagination
// on the row value buys and what a key compared as text loses.
func requireIndexed(
	t *testing.T, conn *pgx.Conn, what, sql string, args []any, want string, sortFree, boundByIndex bool,
) {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	var planJSON string
	require.NoError(t, tx.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql, args...).Scan(&planJSON), sql)
	require.NotContains(t, planJSON, `"Node Type": "Seq Scan"`,
		"%s scans the whole table even with sequential scans priced out, so no index can serve "+
			"how it compares the key: %s\n%s", what, sql, planJSON)
	require.Contains(t, planJSON, want, "%s does not use the %s it should: %s\n%s", what, want, sql, planJSON)
	if boundByIndex {
		require.Contains(t, planJSON, `"Index Cond"`,
			"%s does not compare the key as the index condition, so the index is read for its order alone "+
				"and the bound is applied to every row it returns: %s\n%s", what, sql, planJSON)
		require.NotContains(t, planJSON, `"Filter"`,
			"%s filters after the index read instead of bounding it, which is what a key compared as text "+
				"does to a page: %s\n%s", what, sql, planJSON)
	}
	if sortFree {
		require.NotContains(t, planJSON, `"Node Type": "Sort"`,
			"%s sorts after reading, so its order is not the key's own and a page limited in that order "+
				"skips rows the bound will never revisit: %s\n%s", what, sql, planJSON)
	}
}
