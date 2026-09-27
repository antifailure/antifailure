package pgcrash_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fault"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// These crash real databases with the manifest's own invariants declared, so
// that the arm is proved through the statements Postgres actually ran rather
// than through a table of values. The judgement is driven from values in
// invariants_test.go for the branches a healthy machine never reaches; this is
// the other half, and neither one stands on its own: a judgement that is never
// handed a real answer is a judgement nobody has shown the plumbing reaches.

// exec runs one statement against the database and fails the test if it does
// not land, because a fixture that silently did not apply makes every
// assertion after it meaningless.
func exec(t *testing.T, url, sql string) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	_, err = conn.Exec(t.Context(), sql)
	require.NoErrorf(t, err, "the fixture statement did not run: %s", sql)
}

// checkNamed finds one invariant in the arm by name, and fails rather than
// returning a zero value, because a missing entry would otherwise be asserted
// against as if it had held.
func checkNamed(t *testing.T, res pgcrash.Result, name string) pgcrash.InvariantCheck {
	t.Helper()
	for _, c := range res.Invariants {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("the invariant %q is not in the arm at all, which reads as an invariant with nothing to report", name)
	return pgcrash.InvariantCheck{}
}

// TestVerify_TheManifestsInvariantsAreAskedOnBothSidesOfACrash is the gap this
// arm closes.
//
// `af ci` runs the invariants early and the chaos run last, so until this
// existed the rules a project writes about its own data were never once
// evaluated against a database that had just been crashed. The crash proof's
// own assertions are all about a schema of the engine's, for the reason
// workload.go gives, and that reason stops applying the moment the writers
// stop and the database answers again.
//
// Four invariants, one per answer the arm has to be able to give, all four
// against a real Postgres that really crashed:
//
//   - one that holds on both sides, which must produce nothing at all
//   - one that is violated on both sides, which the run may not blame on the
//     fault
//   - one that holds before and is violated after, which is the one and only
//     answer the run can attribute to the fault
//   - one whose statement the server refuses, which is unverified and is never
//     a pass
func TestVerify_TheManifestsInvariantsAreAskedOnBothSidesOfACrash(t *testing.T) {
	cli := requireDocker(t)
	envID := "pgi" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "")

	// The user's own table, with one row that already breaks the rule about
	// it. A project's pre-existing defect is the case that must NOT be
	// reported as something the crash did.
	exec(t, db.url, "CREATE TABLE accounts (id int PRIMARY KEY, balance int NOT NULL)")
	exec(t, db.url, "INSERT INTO accounts VALUES (1, 10), (2, -3)")

	invs := []schema.Invariant{
		{Name: "every-account-exists", Description: "every account has an id",
			SQL: "SELECT id FROM accounts WHERE id IS NULL"},
		{Name: "no-negative-balance", Description: "no account may go negative",
			SQL: "SELECT id FROM accounts WHERE balance < 0"},
		// The one rule this test can be certain changes across the fault. The
		// proof drops and recreates its own schema before anything else runs,
		// so this is empty when the invariants are asked the first time and
		// holds every commit the writers made when they are asked the second.
		// A user table cannot give a deterministic held to violated
		// transition, because a correct Postgres does not break one.
		{Name: "chaos-table-untouched", Description: "the engine's own table is empty",
			SQL: "SELECT id FROM antifailure_chaos.commits"},
		{Name: "asks-a-missing-table", Description: "a statement the server refuses",
			SQL: "SELECT id FROM no_such_table_anywhere"},
	}

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh := shell(t, cli, db)
	f := fault.Fault{
		Name: "postgres-crash", Kind: fault.KindProcessKill,
		Target: fault.Target{Role: fault.RoleDatabase}, Process: "postgres: checkpointer",
	}

	res, err := pgcrash.Verify(t.Context(), pgcrash.Options{
		URL: db.url, Runner: runner{sh}, DataDir: pgData,
		Workload:    pgcrash.WorkloadOptions{Writers: 4},
		Invariants:  invs,
		WarmCommits: 200, WarmTimeout: 60 * time.Second,
		Settle: time.Second, ReadyTimeout: 2 * time.Minute,
		ExpectCrash: true, FaultName: f.Name,
		Inject: func(ctx context.Context) (pgcrash.Injected, error) {
			in, err := inj.Inject(ctx, f)
			if err != nil {
				return pgcrash.Injected{}, err
			}
			return pgcrash.Injected{Evidence: in.Evidence, KilledSignal: in.KilledSignal}, nil
		},
	})
	require.NoError(t, err)
	require.True(t, res.Crashed(), "nothing crashed, so nothing here is about a recovered database")
	require.Len(t, res.Invariants, len(invs), "the arm lost an invariant")

	// Held on both sides. One assertion per side, because a compound one
	// stops at the first and would say nothing about the second.
	ok := checkNamed(t, res, "every-account-exists")
	require.True(t, ok.Before.Held, "a rule that holds did not hold before the fault")
	require.True(t, ok.After.Held, "a rule that holds did not hold after the recovery")

	// Violated on both sides, and therefore inherited rather than caused.
	neg := checkNamed(t, res, "no-negative-balance")
	require.True(t, neg.Before.Violated(), "the row that already broke this rule was not seen before the fault")
	require.True(t, neg.After.Violated(), "the row that already broke this rule was not seen after the recovery")
	require.Equal(t, [][]string{{"2"}}, neg.After.Rows, "the violating row was not carried as evidence")

	// Held before and violated after, which is the only answer the run
	// attributes to the fault. This is also what proves the before side was
	// really taken BEFORE the workload ran: the table it reads is empty at
	// that moment and holds hundreds of rows at the other.
	broke := checkNamed(t, res, "chaos-table-untouched")
	require.True(t, broke.Before.Held, "the before side was taken after the writers had already committed")
	require.True(t, broke.After.Violated(), "the after side did not see the rows the writers committed")

	// A statement the server refused, on both sides, which is unverified and
	// never a pass.
	bad := checkNamed(t, res, "asks-a-missing-table")
	require.False(t, bad.Before.Evaluated(), "a statement the server refused was read as a verdict")
	require.False(t, bad.After.Evaluated(), "a statement the server refused was read as a verdict")
	require.Contains(t, bad.After.Error, "no_such_table_anywhere")

	// And the verdict the whole arm feeds.
	require.Contains(t, problemRules(res.Problems), pgcrash.RuleInvariantBroken,
		"an invariant that held before the crash and is violated after it did not fail the run")
	require.Contains(t, problemRules(res.Unverified), pgcrash.RuleInvariantAlreadyViolated,
		"an invariant that was already violated was not reported")
	require.Contains(t, problemRules(res.Unverified), pgcrash.RuleInvariantUnevaluated,
		"an invariant the server refused was not reported as unverified")
	require.False(t, res.Verified(), "a run with an invariant it could not ask reported itself as verified")

	// The inherited violation must NOT be a failure. A gate that stops a
	// merge for a rule the change did not break teaches a project to switch
	// the whole arm off.
	require.NotContains(t, problemRules(res.Problems), pgcrash.RuleInvariantAlreadyViolated)
	require.NotContains(t, problemRules(res.Problems), pgcrash.RuleInvariantUnevaluated)
}

// TestVerify_ADatabaseThatNeverComesBackLeavesTheInvariantsUnasked is the
// ordering that would be a silent pass.
//
// The database is frozen and never thawed, so it does not answer a query
// again. There is no recovered database to ask the project's rules of, and the
// honest answer is that they were not asked. A run that returned an empty arm
// here would read as a run whose invariants all held, which is the exact
// defect this whole feature exists to catch in somebody else's system.
func TestVerify_ADatabaseThatNeverComesBackLeavesTheInvariantsUnasked(t *testing.T) {
	cli := requireDocker(t)
	envID := "pgu" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "")
	exec(t, db.url, "CREATE TABLE accounts (id int PRIMARY KEY, balance int NOT NULL)")
	exec(t, db.url, "INSERT INTO accounts VALUES (1, 10)")

	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh := shell(t, cli, db)
	inject, thaw := pauseInjector(t, inj)
	// Thawed on the way out whatever happens, so the container can be removed
	// and so a failure here does not leave a frozen Postgres on this machine.
	t.Cleanup(func() { _ = thaw(context.Background()) })

	// A connect timeout, because the poll that waits for the database to come
	// back has none of its own and a frozen container answers the TCP
	// handshake and then nothing. Without this the first attempt blocks
	// forever, the ready timeout is never consulted, and a test about a
	// database that does not come back hangs instead of failing. The product
	// reaches the same branch through its own context, and the value is only
	// this test declining to wait for that.
	url := db.url + "&connect_timeout=2"

	res, err := pgcrash.Verify(t.Context(), pgcrash.Options{
		URL: url, Runner: runner{sh}, DataDir: pgData,
		Workload: pgcrash.WorkloadOptions{Writers: 2},
		Invariants: []schema.Invariant{{
			Name: "no-negative-balance", Description: "no account may go negative",
			SQL: "SELECT id FROM accounts WHERE balance < 0",
		}},
		WarmCommits: 50, WarmTimeout: 60 * time.Second,
		Settle: time.Second,
		// Short on purpose. Recover is nil, so nothing thaws the container and
		// the database cannot come back however long this waits.
		ReadyTimeout: 5 * time.Second,
		ExpectCrash:  false, FaultName: "freeze-and-leave-it",
		Inject: inject,
	})
	require.Error(t, err, "a database that never answered again was reported as recovered")

	require.Len(t, res.Invariants, 1, "the arm was dropped on the path where it matters most")
	c := res.Invariants[0]
	require.True(t, c.Before.Held, "the invariant was not asked before the fault")
	require.False(t, c.After.Evaluated(), "an invariant nobody could ask was read as a verdict")
	require.Contains(t, c.After.Error, "did not answer a query after the fault")

	require.Contains(t, problemRules(res.Unverified), pgcrash.RuleInvariantUnevaluated,
		"a database that never came back reported its invariants as nothing to see")
	require.NotContains(t, problemRules(res.Problems), pgcrash.RuleInvariantBroken,
		"an invariant nobody asked was reported as one the fault broke")
	require.False(t, res.Verified())
}
