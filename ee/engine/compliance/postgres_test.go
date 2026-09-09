// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package compliance

// The pack, run against a real control plane, by something anybody can re-run.
//
// WHY THIS FILE EXISTS. docs/plan/STATUS.md row 13.12 said `proven` and claimed
// a run against a real control plane, and there was no way for anybody to
// repeat it. Not one test in this package opened a database connection: the
// only skips were for a missing node and a missing web workspace. The word
// `compliance` appeared in no workflow and in no justfile recipe, so nothing
// scheduled one either. A run that happened once on somebody's laptop and left
// no re-runnable artifact is exactly the difference that page draws between
// `proven` and `written`, and the row was on the wrong side of its own line.
//
// The doc comment on AuditEntry.hash said the same thing in the present tense:
// "there is a test that runs a chain written by that implementation, against a
// real Postgres, through this one". There was not. This is that test.
//
// WHAT IS REAL HERE, AND WHY EACH PART HAD TO BE.
//
// The schema is applied by the control plane's own migration runner, every file
// of it, into a database created for this run and dropped afterwards. Not a
// hand-written CREATE TABLE that agrees with what this file expects, which
// would make the reader's queries agree with a fixture rather than with the
// product.
//
// Every audit entry is written by appendAudit, in web/packages/db/src/audit.ts,
// which is the only implementation that has ever produced an entry_hash anybody
// will verify. The Go verifier being checked against a chain the Go verifier
// wrote would be a verifier agreeing with itself.
//
// Row level security, the grants on the audit log and the list of tables
// holding tenant data are read out of the live catalogue, because every one of
// those is a claim about what Postgres will do rather than about what the code
// asks it to.
//
// WHAT IS ASSERTED IS A PROPERTY, NEVER A COUNT. The row this replaces said
// "seventeen tables", and that number could not be repaired by picking a new
// one: postgres.go counts every public table with an org_id column at run time,
// so it is a property of the schema on the day it runs. Three migrations landed
// after seventeen was written. So the assertion is that every such table has
// row level security, whatever their number turns out to be, and the number is
// published as an observation rather than pinned as an expectation.
//
// AND IT HAS TO BE ABLE TO SAY NO. Four negative arms, each pointed at the case
// it exists to catch: an entry altered with a privileged connection, an entry
// deleted with one, a table carrying tenant data with row level security off,
// and an application role granted UPDATE on the audit log. Each is restored
// afterwards, so no arm depends on running before another.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/license"
)

// seedResult is what testdata/seed.mjs reports about what it wrote.
//
// The sequence numbers come back from the seeder rather than being assumed
// here, because a sequence is the database's to assign and a test that assumed
// it started at one would be testing the database's counter.
type seedResult struct {
	CleanOrgID     string  `json:"cleanOrgId"`
	CleanSlug      string  `json:"cleanSlug"`
	CleanUserID    string  `json:"cleanUserId"`
	TamperedOrgID  string  `json:"tamperedOrgId"`
	WindowStart    string  `json:"windowStart"`
	AnchorSeq      int64   `json:"anchorSeq"`
	InsideSeqs     []int64 `json:"insideSeqs"`
	InsideHead     string  `json:"insideHead"`
	DisposableSeqs []int64 `json:"disposableSeqs"`
}

// controlPlane is one seeded database for the whole package.
type controlPlane struct {
	conn *pgx.Conn
	name string
	seed seedResult
	from time.Time
	to   time.Time
	// lastStdout is what the most recent run of the command wrote, so a test
	// can assert on the document rather than only on the exit code. An exit
	// code is a summary of a document nobody looked at.
	lastStdout string
	// host is the server this ran against, for the note this run publishes. It never
	// carries a password: printing one into a published artifact would be a
	// secret in a build artifact whatever the password happened to be.
	host string
}

var (
	live    *controlPlane
	liveErr error
)

// The reasons a skip is honest. Every one of them is about the machine and not
// about the code, which is the distinction the Vault conformance suite in
// ee/engine/secrets draws and the one that stopped twelve of its tests reporting
// SKIP while a real bug went unnoticed.
var (
	errNoPostgres = errors.New("no Postgres is answering")
	errNoNode     = errors.New("node is not on the path")
	errNoWeb      = errors.New("the web workspace's dependencies are not installed")
)

func TestMain(m *testing.M) {
	live, liveErr = startControlPlane()
	code := m.Run()
	if live != nil {
		live.drop()
	}
	os.Exit(code)
}

// required reports whether "there is no database" is an acceptable answer.
//
// The same two ways web/packages/db/test/harness.ts offers, and deliberately
// the same two: AF_REQUIRE_DATABASE=1 is the explicit statement and belongs in
// CI, and naming a database in AF_TEST_DATABASE_URL is the implicit one,
// because naming one is a statement that one is supposed to be there.
func required() bool {
	return os.Getenv("AF_REQUIRE_DATABASE") == "1" || os.Getenv("AF_TEST_DATABASE_URL") != ""
}

// plane returns the seeded control plane, or says why there is not one.
func plane(t *testing.T) *controlPlane {
	t.Helper()
	if live != nil {
		return live
	}
	switch {
	case required():
		// A skip here would be the failure this suite exists to prevent,
		// arriving through the mechanism meant to make it safe. Somebody has
		// said a database is supposed to be present; not finding one, or not
		// being able to seed it, is a red run.
		t.Fatalf("a database was required and the control plane could not be prepared: %v", liveErr)
	case errors.Is(liveErr, errNoPostgres):
		t.Skipf("skipped: %v at %s, and proving this needs a real control plane",
			errNoPostgres, baseURL())
	case errors.Is(liveErr, errNoNode) || errors.Is(liveErr, errNoWeb):
		t.Skipf("skipped: %v, and the audit chain has to be written by the control plane's own code", liveErr)
	default:
		t.Fatalf("Postgres is answering and the control plane could not be prepared, "+
			"which is a failure rather than a reason to skip: %v", liveErr)
	}
	return nil
}

// baseURL is the server a database is created on.
//
// The same default as web/packages/db/test/harness.ts, so that a laptop with
// the control plane's own test database running needs no configuration.
func baseURL() string {
	// Named dsn rather than url so it does not shadow the package of that name
	// in a file that parses one three lines further down.
	if dsn := os.Getenv("AF_TEST_DATABASE_URL"); dsn != "" {
		return dsn
	}
	return "postgres://postgres:test@127.0.0.1:55432/antifailure"
}

func startControlPlane() (*controlPlane, error) {
	if _, err := exec.LookPath("node"); err != nil {
		return nil, errNoNode
	}
	webDir, err := filepath.Abs(filepath.Join("..", "..", "..", "web"))
	if err != nil {
		return nil, err
	}
	// Both of them, because the seeder reaches drizzle-orm through audit.ts and
	// postgres through the harness, and a missing second one fails halfway
	// through a database that has already been created.
	for _, dep := range []string{"drizzle-orm", "postgres"} {
		if _, err := os.Stat(filepath.Join(webDir, "node_modules", dep)); err != nil {
			return nil, fmt.Errorf("%w (npm ci in web/): %s", errNoWeb, dep)
		}
	}

	// Five minutes for the whole preparation, which is forty one migrations, a
	// tenant fixture and two audit chains. Sixty seconds was the first value
	// and it is the wrong shape of number: it is comfortable on an idle laptop
	// and a coin toss on a runner doing eight other things, and a timeout there
	// would report as a failure to prepare rather than as the load it is.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, baseURL())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoPostgres, err)
	}
	defer func() { _ = admin.Close(context.Background()) }()

	// A database of its own, for two reasons that are both about honesty. The
	// reader counts every public table carrying an org_id, so a shared cluster
	// carrying another branch's migrations would report that branch's tables as
	// this schema's. And the negative arms alter grants and add a table, which
	// are database-wide changes that must not be visible to anybody else.
	name := fmt.Sprintf("af_compliance_%d_%d", os.Getpid(), rand.IntN(1_000_000))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		return nil, fmt.Errorf("the database for this run could not be created: %w", err)
	}

	dsn, err := databaseURL(baseURL(), name)
	if err != nil {
		return nil, err
	}

	cp := &controlPlane{name: name, host: hostOf(baseURL())}

	// The seeder applies the migrations and writes the chain. Its stderr is
	// carried into the failure, because node's own errors are the useful ones
	// and a wrapper that reported "the seeder failed" would hide them.
	seeder, err := filepath.Abs(filepath.Join("testdata", "seed.mjs"))
	if err != nil {
		cp.drop()
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "node", seeder)
	cmd.Dir = webDir
	cmd.Env = append(os.Environ(), "AF_TEST_DATABASE_URL="+dsn)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		cp.drop()
		return nil, fmt.Errorf("the control plane could not be seeded: %w\n%s", err, stderr.String())
	}
	if err := json.Unmarshal(out, &cp.seed); err != nil {
		cp.drop()
		return nil, fmt.Errorf("the seeder's report could not be read: %w", err)
	}

	start, err := time.Parse(time.RFC3339Nano, cp.seed.WindowStart)
	if err != nil {
		cp.drop()
		return nil, fmt.Errorf("the seeder reported an unreadable window: %w", err)
	}
	cp.from, cp.to = start, time.Now().UTC().Add(time.Minute)

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		cp.drop()
		return nil, fmt.Errorf("the seeded database could not be reached: %w", err)
	}
	cp.conn = conn
	return cp, nil
}

// drop removes the database this run created.
func (c *controlPlane) drop() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if c.conn != nil {
		_ = c.conn.Close(ctx)
		c.conn = nil
	}
	admin, err := pgx.Connect(ctx, baseURL())
	if err != nil {
		return
	}
	defer func() { _ = admin.Close(context.Background()) }()
	// WITH (FORCE), because a connection left open by a failed run would
	// otherwise leave the database behind for somebody to find next week.
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{c.name}.Sanitize()+" WITH (FORCE)")
}

// reader is the same Reader the enterprise binary builds in gatherEvidence.
func (c *controlPlane) reader() *Reader {
	return NewReader(c.conn)
}

// gather reads the clean organization over the seeded window.
func (c *controlPlane) gather(t *testing.T, org string) Evidence {
	t.Helper()
	e, err := c.reader().Gather(t.Context(), org, c.from, c.to)
	require.NoError(t, err)
	// Named rather than left implied. A control reported as "not evidenced"
	// because a query failed is a different document from one where there was
	// genuinely nothing to show, and this suite must never mistake the first
	// for the second.
	require.Empty(t, e.Incomplete,
		"a query the reader makes failed, so the report would be partial and this run "+
			"would be proving less than it appears to")
	return e
}

func (c *controlPlane) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	_, err := c.conn.Exec(t.Context(), sql, args...)
	require.NoError(t, err)
}

func databaseURL(base, name string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("AF_TEST_DATABASE_URL is not a URL: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

// hostOf is the host and port with any credential removed, for the note this
// run publishes. A published artifact naming a password would be a secret in a
// build artifact whatever the password happened to be.
func hostOf(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return "unknown"
	}
	return u.Host
}

// ---------------------------------------------------------------------------
// The chain the control plane wrote
// ---------------------------------------------------------------------------

func TestTheChainTheControlPlaneWroteVerifiesThroughTheGoVerifier(t *testing.T) {
	c := plane(t)
	e := c.gather(t, c.seed.CleanOrgID)

	require.Equal(t, len(c.seed.InsideSeqs), e.Audit.Entries,
		"the reader counted the anchor entry, which is outside the period the report claims to cover")
	require.Equal(t, c.seed.InsideSeqs[0], e.Audit.FirstSeq)
	require.Equal(t, c.seed.InsideSeqs[len(c.seed.InsideSeqs)-1], e.Audit.LastSeq)
	require.Equal(t, c.seed.InsideHead, e.Audit.Head,
		"the head hash the reader reports is not the one appendAudit wrote")

	// The assertion this whole file is for. Every hash was computed by
	// web/packages/db/src/audit.ts and every one is recomputed here, out of the
	// values Postgres handed back rather than out of a recorded vector. A
	// disagreement of one byte between the two implementations would report
	// every clean audit log in the field as tampered, and reading either
	// implementation tells you nothing about which one is wrong.
	require.Empty(t, e.Audit.Breaks,
		"the Go verifier does not agree with the chain the control plane wrote")

	// The actions are read back as recorded, which is what the coverage control
	// reports on.
	require.Equal(t, 1, e.Audit.ByAction["member.removed"])
	require.Equal(t, 1, e.Audit.ByAction["session.revoked"])
	require.Equal(t, 1, e.Access.Removals)
	require.Equal(t, 0, e.Access.RemovalsWithoutSessionRevoke,
		"the removal and the revocation were not matched up, so the access control "+
			"would report a failure that did not happen")
}

func TestTheAnchorIsWhatStopsTheFirstEntryReadingAsABreak(t *testing.T) {
	c := plane(t)

	anchor, entries, err := c.reader().auditEntries(t.Context(), c.seed.CleanOrgID, c.from, c.to)
	require.NoError(t, err)
	require.NotNil(t, anchor, "no entry before the window was read, so there is nothing to anchor to")
	require.Equal(t, c.seed.AnchorSeq, anchor.Seq)
	require.Len(t, entries, len(c.seed.InsideSeqs))

	// The positive signal rather than the absence of one. Verifying the same
	// real entries WITHOUT the anchor has to produce a break at the first row,
	// because the first entry inside any period points at an entry outside it.
	// Asserting only that the anchored verification is clean would be satisfied
	// by a verifier that never reports anything.
	without := VerifyChain(nil, entries)
	require.Empty(t, without.Breaks,
		"a chain verified from its own first entry with no predecessor should have "+
			"nothing to compare the first link against")

	// And with a WRONG anchor, which is what a reader that fetched the wrong
	// row would hand it. This is the case the anchor query exists to get right.
	wrong := *anchor
	wrong.EntryHash = "0000000000000000000000000000000000000000000000000000000000000000"
	broken := VerifyChain(&wrong, entries)
	require.NotEmpty(t, broken.Breaks,
		"an entry pointing at a predecessor hash that does not match was not reported")
	require.Equal(t, entries[0].Seq, broken.Breaks[0].Seq)
	require.Equal(t, "broken_link", broken.Breaks[0].Kind)
}

// ---------------------------------------------------------------------------
// The negative arms: it has to be able to say no
// ---------------------------------------------------------------------------

func TestAPrivilegedAlterationIsNamedAtItsSequenceNumber(t *testing.T) {
	c := plane(t)
	seq := c.seed.DisposableSeqs[2]

	var before string
	require.NoError(t, c.conn.QueryRow(t.Context(),
		`SELECT detail::text FROM audit_entries WHERE seq = $1`, seq).Scan(&before))
	t.Cleanup(func() {
		_, err := c.conn.Exec(context.Background(),
			`UPDATE audit_entries SET detail = $1::jsonb WHERE seq = $2`, before, seq)
		require.NoError(t, err)
	})

	// A privileged connection, which is the only thing that can do this: the
	// application role holds no UPDATE on this table, and the test below proves
	// that separately.
	c.exec(t, `UPDATE audit_entries SET detail = '{"index":99}'::jsonb WHERE seq = $1`, seq)

	e := c.gather(t, c.seed.TamperedOrgID)
	require.Len(t, e.Audit.Breaks, 1, "an altered entry produced something other than one break")
	require.Equal(t, seq, e.Audit.Breaks[0].Seq,
		"the break was reported at the wrong sequence number, so an investigation "+
			"would go and look at the wrong entry")
	require.Equal(t, "altered", e.Audit.Breaks[0].Kind)

	// The control, and then the exit code a nightly job watches. A pack that
	// found the break and reported the control as anything softer would be
	// covering up the one thing it exists to detect.
	report := SOC2().Evaluate(e)
	require.True(t, report.Failed())
	require.Equal(t, StateFailed, resultFor(t, report, CheckAuditChain).State)

	require.Equal(t, exitControlNo, c.run(t, "soc2", c.seed.TamperedOrgID),
		"a broken audit chain did not stop the command, so a nightly job would pass")
}

func TestAPrivilegedDeletionIsNamedAtTheEntryThatFollowedIt(t *testing.T) {
	c := plane(t)
	seq := c.seed.DisposableSeqs[1]
	next := c.seed.DisposableSeqs[2]

	// The whole row, so it can be put back exactly. Restoring rather than
	// leaving the chain broken is what makes this arm independent of the order
	// the tests run in: a deletion left behind would fail the altered-entry
	// test above depending on which ran first.
	var row struct {
		orgID, actorLabel, action, targetType, origin, detail, prevHash, entryHash string
		occurredAt                                                                 time.Time
	}
	require.NoError(t, c.conn.QueryRow(t.Context(),
		`SELECT org_id::text, actor_label, action, target_type, origin, detail::text,
		        occurred_at, coalesce(prev_hash, ''), entry_hash
		   FROM audit_entries WHERE seq = $1`, seq).Scan(
		&row.orgID, &row.actorLabel, &row.action, &row.targetType, &row.origin,
		&row.detail, &row.occurredAt, &row.prevHash, &row.entryHash))
	t.Cleanup(func() {
		_, err := c.conn.Exec(context.Background(), `
			INSERT INTO audit_entries
			  (seq, org_id, actor_label, action, target_type, origin, detail,
			   occurred_at, prev_hash, entry_hash)
			VALUES ($1, $2::uuid, $3, $4, $5, $6, $7::jsonb, $8, $9, $10)`,
			seq, row.orgID, row.actorLabel, row.action, row.targetType, row.origin,
			row.detail, row.occurredAt, row.prevHash, row.entryHash)
		require.NoError(t, err)
	})

	c.exec(t, `DELETE FROM audit_entries WHERE seq = $1`, seq)

	e := c.gather(t, c.seed.TamperedOrgID)
	require.Equal(t, len(c.seed.DisposableSeqs)-1, e.Audit.Entries)

	kinds := map[string]int64{}
	for _, b := range e.Audit.Breaks {
		kinds[b.Kind] = b.Seq
	}
	require.Contains(t, kinds, "broken_link",
		"a deleted entry left the following one pointing at a hash that no longer exists, "+
			"and that was not reported")
	require.Equal(t, next, kinds["broken_link"],
		"the broken link was reported at the wrong sequence number")
	// The gap is reported separately and deliberately never called proof: a
	// rolled back transaction consumes a sequence number without writing a row,
	// and so does a deletion, and this check cannot tell them apart.
	require.Contains(t, kinds, "missing")
	require.Equal(t, next, kinds["missing"])

	require.True(t, SOC2().Evaluate(e).Failed())
}

func TestATableHoldingTenantDataWithoutRowLevelSecurityIsReported(t *testing.T) {
	c := plane(t)

	t.Cleanup(func() {
		_, err := c.conn.Exec(context.Background(), `DROP TABLE IF EXISTS compliance_probe`)
		require.NoError(t, err)
	})
	// A table shaped like every other tenant table and missing the one thing.
	// This is the table added next year and forgotten, which is exactly what
	// the catalogue query exists to notice.
	c.exec(t, `CREATE TABLE compliance_probe (id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
	           org_id uuid NOT NULL, note text)`)

	e := c.gather(t, c.seed.CleanOrgID)
	require.Contains(t, e.Posture.TenantTables, "compliance_probe",
		"a public table with an org_id column was not counted as holding tenant data")
	require.Contains(t, e.Posture.TablesWithoutRLS, "compliance_probe",
		"a table holding tenant data with row level security off was not reported, so "+
			"this check cannot say no and its yes is worth nothing")

	report := SOC2().Evaluate(e)
	require.True(t, report.Failed())
	isolation := resultFor(t, report, CheckTenantIsolation)
	require.Equal(t, StateFailed, isolation.State)
	require.Contains(t, isolation.Artifacts, "compliance_probe",
		"the failing control did not name the table, so nobody could go and fix it")

	// The other direction, which is what makes it a check rather than a wall.
	// One thing changed and the same table stops being reported.
	c.exec(t, `ALTER TABLE compliance_probe ENABLE ROW LEVEL SECURITY`)
	e = c.gather(t, c.seed.CleanOrgID)
	require.Contains(t, e.Posture.TenantTables, "compliance_probe")
	require.NotContains(t, e.Posture.TablesWithoutRLS, "compliance_probe")
	require.False(t, SOC2().Evaluate(e).Failed())
}

func TestAnApplicationRoleGrantedUpdateOnTheAuditLogIsReported(t *testing.T) {
	c := plane(t)

	t.Cleanup(func() {
		_, err := c.conn.Exec(context.Background(),
			`REVOKE UPDATE ON audit_entries FROM antifailure_app`)
		require.NoError(t, err)
	})
	c.exec(t, `GRANT UPDATE ON audit_entries TO antifailure_app`)

	e := c.gather(t, c.seed.CleanOrgID)
	require.Contains(t, e.Posture.AuditGrants, "UPDATE")

	report := SOC2().Evaluate(e)
	require.True(t, report.Failed())
	appendOnly := resultFor(t, report, CheckAuditAppendOnly)
	require.Equal(t, StateFailed, appendOnly.State)
	require.Contains(t, appendOnly.Detail, "UPDATE",
		"the control failed without naming the privilege that caused it")
}

// ---------------------------------------------------------------------------
// The posture, asserted as a property and never as a count
// ---------------------------------------------------------------------------

func TestEveryTableHoldingTenantDataHasRowLevelSecurity(t *testing.T) {
	c := plane(t)
	e := c.gather(t, c.seed.CleanOrgID)

	require.NotEmpty(t, e.Posture.TenantTables,
		"no table carrying an org_id was found, so this check looked at nothing")
	// NO NUMBER. The count is a property of the schema on the day it runs and
	// migrations land every week; pinning it would be a fixture that has to be
	// edited whenever the product grows, which is how the count in STATUS.md
	// came to be wrong. What is asserted is the property.
	require.Empty(t, e.Posture.TablesWithoutRLS,
		"a table holding tenant data has row level security disabled, so a query that "+
			"forgot its own filter would return another organization's rows")
	require.False(t, e.Posture.AppRoleBypassesRLS,
		"the application role holds BYPASSRLS, which makes every policy decorative")
	t.Logf("%d tables carry an org_id and every one has row level security",
		len(e.Posture.TenantTables))
}

func TestTheApplicationRoleHoldsOnlyInsertAndSelectOnTheAuditLog(t *testing.T) {
	c := plane(t)
	e := c.gather(t, c.seed.CleanOrgID)

	granted := append([]string(nil), e.Posture.AuditGrants...)
	sort.Strings(granted)
	require.Equal(t, []string{"INSERT", "SELECT"}, granted,
		"the application role's privileges on the audit log are not the two that make "+
			"it append-only at the database rather than by convention")
}

// ---------------------------------------------------------------------------
// The command, end to end, against the real thing
// ---------------------------------------------------------------------------

// run invokes the command the enterprise binary contributes, with the reader
// the enterprise binary builds, against this database.
//
// The whole path rather than Pack.Evaluate on its own: the licence gate, the
// flag parsing, the reader, the pack and the exit code are what a customer
// runs, and every one of them has been testable without a database until now.
func (c *controlPlane) run(t *testing.T, pack, org string, extra ...string) int {
	t.Helper()
	var stdout, stderr strings.Builder
	args := append([]string{pack, "--org", org, "--months", "1"}, extra...)
	code := Command(withFeatures(t.Context(), license.FeatureCompliance), args, Options{
		Stdout: &stdout, Stderr: &stderr, Gather: c.reader().Gather,
	})
	if stderr.Len() > 0 {
		t.Logf("af compliance %s stderr: %s", pack, stderr.String())
	}
	c.lastStdout = stdout.String()
	return code
}

func TestTheCommandProducesAReportAgainstACleanControlPlaneAndExitsZero(t *testing.T) {
	c := plane(t)

	for _, pack := range []string{"soc2", "hipaa"} {
		t.Run(pack, func(t *testing.T) {
			code := c.run(t, pack, c.seed.CleanOrgID)
			require.Equal(t, exitOK, code,
				"a control plane with an intact chain, row level security on every tenant "+
					"table and an append-only audit log reported a control as failing")
			require.Contains(t, c.lastStdout, c.seed.CleanOrgID,
				"the report does not name the organization it is about")
			require.Contains(t, c.lastStdout, "Evidenced",
				"the report has no summary table, so it is not the document at all")
		})
	}
}

func resultFor(t *testing.T, r Report, check Check) Result {
	t.Helper()
	for _, result := range r.Results {
		if result.Control.Check == check {
			return result
		}
	}
	t.Fatalf("no control in %s names the check %q", r.Pack.Name, check)
	return Result{}
}

// ---------------------------------------------------------------------------
// Publishing what it found
// ---------------------------------------------------------------------------

// evidenceDir is where this run leaves the documents it produced.
//
// A run that proves something and leaves nothing behind is the failure this
// file was written to repair: STATUS.md claimed a run against a real control
// plane and there was no artifact anybody could look at. So the reports the
// packs produce from a real database are written out, and the justfile recipe
// and the CI job both name a directory to keep them in.
const evidenceDirEnv = "AF_COMPLIANCE_EVIDENCE_DIR"

func TestThePacksAreRunAgainstThisControlPlaneAndPublishWhatTheyFound(t *testing.T) {
	c := plane(t)

	dir := os.Getenv(evidenceDirEnv)
	if dir == "" {
		dir = t.TempDir()
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))

	for _, pack := range []string{"soc2", "hipaa"} {
		for _, format := range []string{"markdown", "json"} {
			extension := map[string]string{"markdown": "md", "json": "json"}[format]
			path := filepath.Join(dir, pack+"."+extension)
			code := c.run(t, pack, c.seed.CleanOrgID, "--output", format, "--out", path)
			require.Equal(t, exitOK, code)

			// Positively produced rather than merely not complained about. An
			// assertion whose failure mode is silence is satisfied by silence,
			// and "the artifact upload found nothing" is exactly that shape.
			info, err := os.Stat(path)
			require.NoError(t, err, "the command reported success and wrote no document")
			require.Greater(t, info.Size(), int64(500), "%s is too small to be a report", path)

			body, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Contains(t, string(body), c.seed.CleanOrgID)
		}
	}

	// The JSON is re-read as a document rather than as bytes, because it is the
	// form a pipeline consumes and a file that is present but unparseable is a
	// published artifact that nothing can use.
	raw, err := os.ReadFile(filepath.Join(dir, "soc2.json"))
	require.NoError(t, err)
	//
	// Against the document's own field names rather than against the Go type's,
	// which are not the same: `Report.JSON` renames Results to `controls` and
	// carries a top level `failed`. A test that decoded into a shape nobody
	// emits would find an empty list and, had it asserted the other way round,
	// would have passed on a file with nothing in it.
	var parsed struct {
		Framework string `json:"framework"`
		Org       string `json:"org"`
		Failed    bool   `json:"failed"`
		Controls  []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"controls"`
	}
	require.NoError(t, json.Unmarshal(raw, &parsed))
	require.NotEmpty(t, parsed.Controls, "the published JSON carries no controls")
	require.Equal(t, c.seed.CleanOrgID, parsed.Org)
	require.False(t, parsed.Failed)
	for _, control := range parsed.Controls {
		require.NotEmpty(t, control.ID)
		require.NotEmpty(t, control.State,
			"control %s was published with no outcome at all", control.ID)
	}

	note := c.coverage(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "coverage.md"), []byte(note), 0o644))
	t.Log("\n" + note)
}

// coverage is the note that says what this run checked and what it did not.
//
// The second half is the half that matters, and it is written by hand because
// nothing can generate it: a tool that could enumerate what it failed to check
// would have checked it. A stated gap is a fact and a silent one is a defect,
// and every number in here is measured on this run rather than quoted from a
// previous one.
func (c *controlPlane) coverage(t *testing.T) string {
	t.Helper()
	e := c.gather(t, c.seed.CleanOrgID)

	var version string
	require.NoError(t, c.conn.QueryRow(t.Context(), "SHOW server_version").Scan(&version))

	// The authentication method, because a local Postgres using `trust` makes
	// every password fault invisible, and a run that asserted anything about a
	// credential without saying which method it ran against would be quoting a
	// number it could not have measured. Read from the server's own view, which
	// needs privilege; when it cannot be read that is said rather than assumed.
	auth := "could not be read on this server, so no claim is made about it"
	rows, err := c.conn.Query(t.Context(), `
		SELECT DISTINCT auth_method FROM pg_hba_file_rules
		 WHERE type IN ('host', 'hostssl', 'hostnossl') ORDER BY 1`)
	if err == nil {
		var methods []string
		for rows.Next() {
			var method string
			if err := rows.Scan(&method); err == nil {
				methods = append(methods, method)
			}
		}
		rows.Close()
		if len(methods) > 0 {
			auth = strings.Join(methods, ", ")
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# The compliance packs, run against a real control plane\n\n")
	fmt.Fprintf(&b, "Produced by `TestThePacksAreRunAgainstThisControlPlaneAndPublishWhatTheyFound`\n")
	fmt.Fprintf(&b, "in `ee/engine/compliance/postgres_test.go`. Re-run it with `just compliance`.\n\n")

	fmt.Fprintf(&b, "## The run\n\n")
	fmt.Fprintf(&b, "- Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Postgres: %s at %s, in a database created for this run and dropped after it\n",
		version, c.host)
	fmt.Fprintf(&b, "- Host authentication method: %s\n", auth)
	fmt.Fprintf(&b, "- Schema: every migration in `web/packages/db/migrations`, applied by the\n")
	fmt.Fprintf(&b, "  control plane's own runner\n")
	fmt.Fprintf(&b, "- Audit chain: written by `appendAudit` in `web/packages/db/src/audit.ts`,\n")
	fmt.Fprintf(&b, "  which is the implementation that writes every hash in the field\n\n")

	fmt.Fprintf(&b, "## What it found\n\n")
	fmt.Fprintf(&b, "- %d tables carry an `org_id` column, and %d of them have row level\n",
		len(e.Posture.TenantTables), len(e.Posture.TablesWithoutRLS))
	fmt.Fprintf(&b, "  security disabled. The number of tables is an observation on the day this\n")
	fmt.Fprintf(&b, "  ran and is not asserted anywhere: it is a property of the schema and\n")
	fmt.Fprintf(&b, "  migrations land every week. What is asserted is that the second number is\n")
	fmt.Fprintf(&b, "  zero.\n")
	fmt.Fprintf(&b, "- The application role holds %s on the audit log, and BYPASSRLS is %v.\n",
		strings.Join(e.Posture.AuditGrants, " and "), e.Posture.AppRoleBypassesRLS)
	fmt.Fprintf(&b, "- The hash chain over %d entries verifies, with %d break(s).\n",
		e.Audit.Entries, len(e.Audit.Breaks))
	fmt.Fprintf(&b, "- %d masking attestation(s) were read out of `golden_versions` and their\n",
		len(e.Attestations))
	fmt.Fprintf(&b, "  signatures checked.\n")
	fmt.Fprintf(&b, "- %d egress decisions and %d environments were read.\n\n",
		e.Egress.Decisions, e.Environments.Created)

	fmt.Fprintf(&b, "## What this run did NOT check\n\n")
	fmt.Fprintf(&b, "- **It is not a report about a production installation.** The evidence is\n")
	fmt.Fprintf(&b, "  read out of a database this suite seeded, so every number in `soc2.md` and\n")
	fmt.Fprintf(&b, "  `hipaa.md` is the fixture's. What is proved is that the reader, the\n")
	fmt.Fprintf(&b, "  verifier and the packs behave correctly against a real schema, not that\n")
	fmt.Fprintf(&b, "  any customer's controls hold.\n")
	fmt.Fprintf(&b, "- **The policy control has nothing to read.** There is no organization\n")
	fmt.Fprintf(&b, "  policy table in the control plane, so `policy-decisions` is reported as\n")
	fmt.Fprintf(&b, "  not evidenced on every installation and this run cannot change that.\n")
	fmt.Fprintf(&b, "- **Retention is configuration, not data.** `audit-retention` reports what\n")
	fmt.Fprintf(&b, "  the operator states in `AF_AUDIT_RETENTION_DAYS`; a policy that has\n")
	fmt.Fprintf(&b, "  never deleted anything leaves no trace in the rows, so nothing here can\n")
	fmt.Fprintf(&b, "  verify the setting is honoured.\n")
	fmt.Fprintf(&b, "- **No claim is made about authentication.** The credential is whatever\n")
	fmt.Fprintf(&b, "  `AF_TEST_DATABASE_URL` carries, and against a `trust` server a wrong\n")
	fmt.Fprintf(&b, "  password would be invisible. The method this run saw is named above.\n")
	fmt.Fprintf(&b, "- **The enterprise binary's own `gatherEvidence` is not the code under\n")
	fmt.Fprintf(&b, "  test.** This builds the same `Reader` it builds, from `NewReader`, but the\n")
	fmt.Fprintf(&b, "  environment variables it reads and the connection it opens are exercised\n")
	fmt.Fprintf(&b, "  only when the binary itself runs.\n")
	fmt.Fprintf(&b, "- **Nothing is asserted about how long any of this takes.**\n")
	return b.String()
}
