package insights_test

// The lock report names relations the change could have named, and nothing else.
//
// WHY THIS FILE EXISTS. The sampler decided what was a system relation by
// listing the namespaces to leave out, `pg_catalog` and `information_schema`.
// A whole family of relations lives in a namespace that list does not mention.
// Every TOAST table in Postgres lives in `pg_toast`, so every one of them
// passed the filter, and a temporary relation lives in `pg_temp_N` with its own
// TOAST in `pg_toast_temp_N`, so those passed too. Three of them are reproduced
// below. The fourth, `pg_toast_temp_N`, is covered by the same predicate and is
// not claimed here, because none of the migration shapes tried put a lock on one
// into pg_locks.
//
// That put `pg_toast_2618` on a pull request as a relation the change locked.
// It is the TOAST table of `pg_rewrite`, reached by any migration that creates
// a view whose rule is big enough to toast. Noise about our own scaffolding, in
// the same shape as the bookkeeping table this package already had to stop
// reporting.
//
// The worse face is the one nobody had looked for. A USER table's TOAST table
// lives in `pg_toast` as well, named `pg_toast_<oid>`, and a table rewrite
// takes ACCESS EXCLUSIVE on it. So a migration that rewrote `orders` reported a
// relation called `pg_toast_16388`, at the STRONGEST lock mode there is, which
// is the key the report sorts by, so the name the author cannot recognise sorts
// above the name they changed. Both faces are reproduced here on a real server
// rather than argued from the catalogue.
//
// The filter now states what a reportable relation IS: one in a namespace the
// project owns. Postgres reserves the `pg_` prefix for its own schemas and
// refuses CREATE SCHEMA on any name that begins with it, which is what makes
// that a closed statement rather than another list of the members somebody
// happened to think of. TestPostgres_TheSystemSchemaPrefixIsReserved measures
// that reservation from the server, because the whole design rests on it.
//
// The second half of the same defect was found while reproducing the first and
// is worse, because it does not look like noise. pg_locks is cluster wide and
// names a relation by OID alone, the query never filtered on the database, and
// two rehearsal branches copied from one golden agree on every OID in it. So one
// branch reported the other's lock as its own, on the author's real table name,
// at the mode and the duration that fail the check.
//
// Every test here carries its falsification: the relation the change really
// locked must still be reported. A filter that excluded too much would be a
// worse defect than the one being fixed and an invisible one, because the
// finding would simply not appear.

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
)

// toastPrefix is how Postgres names every TOAST table and every TOAST index.
// Matching on it rather than on one resolved name keeps the assertion from
// going vacuous: a rewrite can move a relation to a new OID, and an assertion
// naming the OID the test read beforehand would then be checking for a relation
// that no longer exists.
const toastPrefix = "pg_toast"

// lockNames is what the report would print in the relation column.
func lockNames(locks []insights.LockHold) []string {
	out := make([]string, 0, len(locks))
	for _, l := range locks {
		out = append(out, l.Table)
	}
	return out
}

func holdOn(t *testing.T, locks []insights.LockHold, table string) insights.LockHold {
	t.Helper()
	for _, l := range locks {
		if l.Table == table {
			return l
		}
	}
	require.FailNowf(t, "the lock on the relation the migration named was not reported at all",
		"wanted %q, the report has %v", table, lockNames(locks))
	return insights.LockHold{}
}

// catalogToastChunks counts the rows in pg_rewrite's TOAST relation, which is
// how this suite tells a migration that reached it from one that did not.
func catalogToastChunks(t *testing.T, ctx context.Context, db testDB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.conn.QueryRow(ctx,
		`SELECT count(*) FROM pg_toast.pg_toast_2618`).Scan(&n))
	return n
}

// TestRehearse_ATableRewriteDoesNotReportTheTablesToastRelation is the second
// and worse face: the opaque name belongs to the author's own table.
//
// `status` is text and `orders` therefore has a TOAST table. Changing the type
// rewrites the table, which takes ACCESS EXCLUSIVE on the heap, on every index
// and on the TOAST relation. The author wrote one relation name and the report
// used to carry three, one of which they have no way to recognise.
func TestRehearse_ATableRewriteDoesNotReportTheTablesToastRelation(t *testing.T) {
	db, done := requireDatabase(t, "locksrewritetoast")
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Read the name before the migration, for the failure message only. The
	// assertion below is on the prefix, so it cannot be satisfied by the
	// rewrite having moved the relation.
	var toast string
	require.NoError(t, db.conn.QueryRow(ctx,
		`SELECT t.relname FROM pg_class c JOIN pg_class t ON t.oid = c.reltoastrelid
		 WHERE c.oid = 'orders'::regclass`).Scan(&toast),
		"orders has no TOAST relation, so this test would prove nothing")
	t.Logf("the TOAST relation of orders on this branch is %s", toast)

	// pg_sleep inside the same transaction holds every lock the rewrite took
	// for long enough that the sampler, which asks every 250 milliseconds,
	// cannot miss the window.
	r := rehearse(t, db, map[string]string{
		"001_rewrite.sql": "ALTER TABLE orders ALTER COLUMN status TYPE varchar(64);\n" +
			"SELECT pg_sleep(1);\n",
	})
	require.False(t, r.Failed, r.Error)
	t.Logf("relations the report would name: %v", lockNames(r.Locks))

	// Falsification: the relation the author actually changed is still there,
	// at the mode the rewrite really took. Without this, a filter that dropped
	// everything would pass the assertion below.
	hold := holdOn(t, r.Locks, "orders")
	require.Equal(t, "AccessExclusiveLock", hold.Mode,
		"a table rewrite takes the strongest lock there is, and that is the finding")

	for _, l := range r.Locks {
		require.False(t, strings.HasPrefix(l.Table, toastPrefix),
			"the report names %q, which is a TOAST relation. orders has one and it is %s, "+
				"so this is a name the author never wrote and cannot look up. The report "+
				"reads %v", l.Table, toast, lockNames(r.Locks))
	}
}

// TestRehearse_AToastedViewRuleDoesNotReportACatalogsToastRelation is the
// originally observed finding, pg_toast_2618, reproduced from its cause.
//
// A view's rule is stored in pg_rewrite.ev_action. A rule past the toast
// threshold is written to pg_rewrite's own TOAST table, which is named after
// pg_rewrite's OID of 2618, and the write holds RowExclusiveLock on it until
// the migration commits. Creating a view is ordinary migration content, so this
// is not an exotic path.
func TestRehearse_AToastedViewRuleDoesNotReportACatalogsToastRelation(t *testing.T) {
	db, done := requireDatabase(t, "lockscatalogtoast")
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var catalogToast string
	require.NoError(t, db.conn.QueryRow(ctx,
		`SELECT t.relname FROM pg_class c JOIN pg_class t ON t.oid = c.reltoastrelid
		 WHERE c.oid = 'pg_rewrite'::regclass`).Scan(&catalogToast))
	require.Equal(t, "pg_toast_2618", catalogToast,
		"pg_rewrite's OID is 2618 and its TOAST relation is named after it; a different "+
			"name here means the relation this test is about is not the one it found")

	// The chunk count before, because the count after is not evidence on its
	// own: a database fresh from initdb already has 278 chunks in this relation,
	// from the rules of the catalogue's own views, so an assertion that the
	// relation is not empty would pass whatever this migration did.
	before := catalogToastChunks(t, ctx, db)

	// Wide enough for the rule to exceed the toast threshold. The growth is
	// asserted below rather than assumed, because a more compact node
	// serialisation in a later Postgres would quietly stop exercising the case
	// and the test would pass having watched nothing.
	var cols []string
	for i := 0; i < 400; i++ {
		cols = append(cols, "id AS c"+strconv.Itoa(i))
	}
	r := rehearse(t, db, map[string]string{
		"001_view.sql": "CREATE VIEW wide_orders AS SELECT " + strings.Join(cols, ", ") +
			" FROM orders;\nSELECT pg_sleep(1);\n",
	})
	require.False(t, r.Failed, r.Error)
	t.Logf("relations the report would name: %v", lockNames(r.Locks))

	after := catalogToastChunks(t, ctx, db)
	require.Greater(t, after, before,
		"the view's rule did not reach pg_rewrite's TOAST relation, so this migration never "+
			"locked the relation the test is about and the absence below proves nothing")

	// Falsification: the sampler was awake during that transaction. Creating a
	// view takes AccessShareLock on the table it reads, so orders being
	// reported is the evidence that the window was sampled at all.
	holdOn(t, r.Locks, "orders")

	for _, l := range r.Locks {
		require.NotEqual(t, catalogToast, l.Table,
			"the report names %s, the TOAST relation of a system catalogue, as a relation "+
				"the change locked, among %v", catalogToast, lockNames(r.Locks))
		require.False(t, strings.HasPrefix(l.Table, toastPrefix),
			"the report names the TOAST relation %q among %v", l.Table, lockNames(r.Locks))
	}
}

// TestRehearse_ATemporaryRelationIsNotReported covers the other half of the
// family the old list omitted, and the shape of it is the measurement.
//
// A temporary table lives in pg_temp_N, which the old list did not name either.
// Getting one into the sample took establishing something first: Postgres
// records no heavyweight relation lock for a relation created inside the
// transaction that is still running, so the obvious migration, one file that
// creates a temporary table and uses it, produces no pg_locks row at all and
// would have been a test that passed before the fix as well as after it. The
// lock appears once the temporary relation outlives the transaction that made
// it, which for the SQL applier means a later migration file in the same run,
// since it holds one connection across all of them. That is what these two
// files are.
//
// It matters because a temporary relation belongs to one session and is gone
// when that session disconnects, so nothing in production can ever queue behind
// a lock on one. Reporting it tells the author to split a statement over a lock
// that blocked nobody, under a schema name they did not write.
func TestRehearse_ATemporaryRelationIsNotReported(t *testing.T) {
	db, done := requireDatabase(t, "lockstemp")
	defer done()

	r := rehearse(t, db, map[string]string{
		"001_stage.sql": "CREATE TEMP TABLE staged_notes (id bigint, note text);\n" +
			"INSERT INTO staged_notes SELECT id, repeat('n', 4000) FROM orders LIMIT 500;\n",
		"002_backfill.sql": "UPDATE orders SET note = s.note FROM staged_notes s " +
			"WHERE s.id = orders.id;\nSELECT pg_sleep(1);\n",
	})
	require.False(t, r.Failed, r.Error)
	t.Logf("relations the report would name: %v", lockNames(r.Locks))

	// Falsification: the real table the backfill updated is still reported.
	holdOn(t, r.Locks, "orders")

	for _, l := range r.Locks {
		require.NotEqual(t, "staged_notes", l.Table,
			"the report names a temporary table, which no session but the migration's own "+
				"could see and which no query could queue behind, among %v", lockNames(r.Locks))
		require.False(t, strings.HasPrefix(l.Table, toastPrefix),
			"the report names the TOAST relation %q among %v", l.Table, lockNames(r.Locks))
	}
}

// TestRehearse_ARelationInAnotherSchemaIsStillReported is the falsification of
// the new filter's shape rather than of one case.
//
// The predicate is about the namespace, so the way to get it wrong in the other
// direction is to report only `public`. A project that keeps its tables in a
// schema of its own would then get a rehearsal that found no locks at all, and
// nothing on the pull request would say so.
//
// The table is created in one file and altered in the next for the reason the
// temporary case above establishes: Postgres records no relation lock for a
// relation created inside the transaction that is still running, so a single
// file doing both would report nothing and this test would fail for a reason
// that has nothing to do with the namespace.
func TestRehearse_ARelationInAnotherSchemaIsStillReported(t *testing.T) {
	db, done := requireDatabase(t, "locksotherschema")
	defer done()

	r := rehearse(t, db, map[string]string{
		"001_schema.sql": "CREATE SCHEMA shop;\n" +
			"CREATE TABLE shop.invoices (id bigserial PRIMARY KEY, note text);\n",
		"002_settled.sql": "ALTER TABLE shop.invoices ADD COLUMN settled_at timestamp;\n" +
			"SELECT pg_sleep(1);\n",
	})
	require.False(t, r.Failed, r.Error)
	t.Logf("relations the report would name: %v", lockNames(r.Locks))

	hold := holdOn(t, r.Locks, "invoices")
	require.Equal(t, "AccessExclusiveLock", hold.Mode,
		"the lock on a table outside public must be reported at the mode it was taken")
}

// TestRehearse_ALockInAnotherBranchIsNotReportedAsThisOnes is the other half of
// the same defect, and the half that does not look like noise.
//
// pg_locks is cluster wide and names a relation by OID alone, so the join to
// pg_class resolves it against whichever database the sampler is connected to. A
// rehearsal branch is a CREATE DATABASE ... TEMPLATE copy of the golden, and a
// template copy carries pg_class verbatim, so two branches of one golden agree
// on the OID of every table in it. The assertion below is that they agree, first,
// because the whole case rests on it.
//
// What that produced was not an opaque name. It was the author's own table, at
// the mode and the duration that fail the check, from a lock another rehearsal
// took in its own database. Two rehearsals on one cluster is the normal case.
func TestRehearse_ALockInAnotherBranchIsNotReportedAsThisOnes(t *testing.T) {
	mine, done := requireDatabase(t, "locksmybranch")
	defer done()
	theirs, doneTheirs := requireDatabase(t, "lockstheirbranch")
	defer doneTheirs()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// The premise, measured rather than assumed. Without matching OIDs the lock
	// below could not be misattributed and this test would pass having exercised
	// nothing at all.
	var mineOID, theirsOID uint32
	require.NoError(t, mine.conn.QueryRow(ctx,
		`SELECT 'orders'::regclass::oid`).Scan(&mineOID))
	require.NoError(t, theirs.conn.QueryRow(ctx,
		`SELECT 'orders'::regclass::oid`).Scan(&theirsOID))
	require.Equal(t, mineOID, theirsOID,
		"the two branches disagree about the OID of orders, so nothing here could be "+
			"misattributed and this test would prove nothing")
	t.Logf("both branches call orders OID %d", mineOID)

	// The other branch holds the strongest lock there is on ITS orders, for
	// longer than this rehearsal takes.
	tx, err := theirs.conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	_, err = tx.Exec(ctx, "LOCK TABLE orders IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)

	// This branch's migration touches users and nothing else.
	r := rehearse(t, mine, map[string]string{
		"001_nickname.sql": "ALTER TABLE users ADD COLUMN nickname text;\n" +
			"SELECT pg_sleep(1);\n",
	})
	require.False(t, r.Failed, r.Error)
	t.Logf("relations the report would name: %v", lockNames(r.Locks))

	// Falsification: the table this migration really did lock is reported, so
	// the sampler was awake through the window in which the foreign lock was
	// held and the absence below is a decision rather than a blank result.
	hold := holdOn(t, r.Locks, "users")
	require.Equal(t, "AccessExclusiveLock", hold.Mode)

	// On the mode rather than on the name, and deliberately. A lock on this
	// branch's own orders is possible for an honest reason: autovacuum takes
	// ShareUpdateExclusiveLock, and a report naming it would be correct and
	// would be answering a different question. Nothing in this database can
	// produce ACCESS EXCLUSIVE on orders, because no statement here touched it,
	// so that pair is the foreign lock and nothing else.
	for _, l := range r.Locks {
		if l.Table != "orders" {
			continue
		}
		require.NotEqual(t, "AccessExclusiveLock", l.Mode,
			"the report names an ACCESS EXCLUSIVE lock on orders, which this migration never "+
				"touched; the only session holding one is in another database that happens to "+
				"share the OID, among %v", lockNames(r.Locks))
	}
}

// TestRehearse_ContentionInAnotherBranchDoesNotMarkThisOneBlocking is the third
// face, and the one with teeth, because Blocking is not cosmetic.
//
// The sampler decides Blocking from an EXISTS over pg_locks matching a waiter on
// w.relation alone. pg_locks being cluster wide, a session queued behind a
// foreign relation of the same OID set Blocking on this branch's hold. What that
// buys is not a cosmetic column: gate.MigrationFindings reports a hold whose
// Blocking is set EVEN WHEN ITS DURATION IS BELOW THE IGNORE THRESHOLD, and adds
// "Another session was seen waiting on it" to the finding. So a foreign waiter
// could conjure a finding out of a lock too brief to report, and then explain it
// with a sentence that was not true about anything.
//
// Two sessions in the other branch: one holding, one queued behind it. The queued
// one is what the old subquery saw.
//
// The contention and this branch's own migration are BOTH on orders, and that is
// the whole test rather than a detail. The EXISTS looks for an ungranted waiter
// on the same relation OID as the hold it is judging, so a waiter queued on
// orders could never be mistaken for a waiter on users. The first version of this
// test locked users here and the mutation SURVIVED: it could not see the predicate
// it was written for. Two rehearsals both touching the busiest table in the golden
// is also the likeliest way this happens for real.
func TestRehearse_ContentionInAnotherBranchDoesNotMarkThisOneBlocking(t *testing.T) {
	mine, done := requireDatabase(t, "locksmyblocking")
	defer done()
	theirs, doneTheirs := requireDatabase(t, "lockstheirblocking")
	defer doneTheirs()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const ordersOID = `SELECT 'orders'::regclass::oid`
	var mineOID, theirsOID uint32
	require.NoError(t, mine.conn.QueryRow(ctx, ordersOID).Scan(&mineOID))
	require.NoError(t, theirs.conn.QueryRow(ctx, ordersOID).Scan(&theirsOID))
	require.Equal(t, mineOID, theirsOID,
		"the two branches disagree about the OID of orders, so no foreign waiter could be "+
			"mistaken for one here and this test would prove nothing")

	// The holder, on the other branch's own orders.
	holder, err := theirs.conn.Begin(ctx)
	require.NoError(t, err)
	_, err = holder.Exec(ctx, "LOCK TABLE orders IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)

	// The waiter, which will block until the holder rolls back. It needs its own
	// connection, because a blocked statement blocks its session. The goroutine
	// is joined before the test returns, which this package's TestMain requires:
	// it runs goleak.Find over the whole suite.
	waiter, err := pgx.Connect(ctx, theirs.url.Reveal())
	require.NoError(t, err)
	queued := make(chan struct{})
	go func() {
		defer close(queued)
		tx, err := waiter.Begin(context.WithoutCancel(ctx))
		if err != nil {
			return
		}
		// Expected to block, then to succeed once the holder rolls back.
		_, _ = tx.Exec(context.WithoutCancel(ctx), "LOCK TABLE orders IN ACCESS EXCLUSIVE MODE")
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()
	defer func() {
		_ = holder.Rollback(context.WithoutCancel(ctx))
		<-queued
		_ = waiter.Close(context.WithoutCancel(ctx))
	}()

	// Poll for the waiter to be queued rather than sleeping, and require it:
	// with nothing waiting there is no foreign waiter to be mistaken for one
	// here, and the assertion at the end would pass having watched nothing.
	require.Eventually(t, func() bool {
		var ungranted int
		if err := theirs.conn.QueryRow(ctx,
			`SELECT count(*) FROM pg_locks
			 WHERE NOT granted AND locktype = 'relation' AND relation = 'orders'::regclass`,
		).Scan(&ungranted); err != nil {
			return false
		}
		return ungranted > 0
	}, 30*time.Second, 100*time.Millisecond,
		"no session ever queued behind the other branch's lock, so this test would prove nothing")

	// This branch's migration takes its own lock on ITS orders. Nothing in this
	// database waits for it.
	r := rehearse(t, mine, map[string]string{
		"001_settled.sql": "ALTER TABLE orders ADD COLUMN settled_at timestamp;\n" +
			"SELECT pg_sleep(1);\n",
	})
	require.False(t, r.Failed, r.Error)
	for _, l := range r.Locks {
		t.Logf("hold: %s %s blocking=%v", l.Table, l.Mode, l.Blocking)
	}

	// Falsification: the hold this migration really took is reported, so the
	// sampler was awake while the foreign waiter was queued.
	hold := holdOn(t, r.Locks, "orders")
	require.Equal(t, "AccessExclusiveLock", hold.Mode)
	require.False(t, hold.Blocking,
		"the report says another session was seen waiting on orders. Nothing in this database "+
			"is queued behind it; the only waiter is in another branch, behind that branch's "+
			"own orders, which merely shares the OID")

	for _, l := range r.Locks {
		require.False(t, l.Blocking,
			"the report marks %s as contended on a branch nothing else is using, which "+
				"promotes a hold below the ignore threshold into a finding and explains it "+
				"with a sentence that is not true", l.Table)
	}
}

// TestPostgres_TheSystemSchemaPrefixIsReserved measures the claim the filter
// rests on, from the server.
//
// The filter is inclusive rather than a list of exceptions only because no
// relation a project owns can ever sit in a namespace beginning with `pg_`. If
// that were not true the new filter would silently drop a user's own table and
// the pull request would carry no lock finding at all, which is the failure
// mode this repository keeps finding in its own instruments. So it is checked
// here rather than remembered, and the two halves of the family are enumerated
// from pg_namespace rather than from memory.
func TestPostgres_TheSystemSchemaPrefixIsReserved(t *testing.T) {
	db, done := requireDatabase(t, "locksreserved")
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for _, name := range []string{"pg_shop", `"pg_Shop"`, "pg_"} {
		_, err := db.conn.Exec(ctx, "CREATE SCHEMA "+name)
		require.Error(t, err,
			"Postgres accepted the schema %s, so a relation a project owns can sit in a "+
				"namespace this filter treats as the server's and would be dropped from "+
				"every lock report", name)
		require.Contains(t, err.Error(), "unacceptable schema name", name)
	}

	// Every TOAST relation on this server is in a reserved namespace, so the
	// namespace predicate reaches all of them rather than the ones a test
	// happened to create.
	rows, err := db.conn.Query(ctx,
		`SELECT DISTINCT n.nspname FROM pg_class c
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE c.relkind = 't'`)
	require.NoError(t, err)
	var namespaces []string
	for rows.Next() {
		var ns string
		require.NoError(t, rows.Scan(&ns))
		namespaces = append(namespaces, ns)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, namespaces, "no TOAST relation exists, so this measured nothing")
	for _, ns := range namespaces {
		require.True(t, strings.HasPrefix(ns, "pg_"),
			"a TOAST relation lives in %q, which the filter treats as a namespace the "+
				"project owns, so it would be reported as a relation the change locked", ns)
	}
	t.Logf("namespaces holding TOAST relations on this server: %v", namespaces)
}
