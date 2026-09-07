package pgurl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The tests that need no database come first. They are the ones that hold the
// rules a name has to obey, and every one of them exists because getting the
// rule wrong writes a statement against somebody's server.

func TestBranchNameIsDeterministicAndFitsPostgres(t *testing.T) {
	long := "env_" + strings.Repeat("abcdefgh", 20)

	first, err := branchName(long)
	require.NoError(t, err)
	second, err := branchName(long)
	require.NoError(t, err)

	// Idempotency by environment is what stops a retry after a timeout
	// creating a second database, and it rests entirely on this being a
	// function of the environment identifier alone.
	require.Equal(t, first, second, "the same environment produced two different database names")
	require.LessOrEqual(t, len(first), 63,
		"a Postgres identifier is 63 bytes and this one is longer, so the server would truncate "+
			"it and two environments could collide inside the server rather than here")

	other, err := branchName(long + "x")
	require.NoError(t, err)
	require.NotEqual(t, first, other,
		"two environments whose identifiers differ only past the truncation point were given "+
			"one database, so one environment would be handed the other's data")
}

func TestBranchNameLowercasesAndReplacesWhatPostgresWouldFold(t *testing.T) {
	got, err := branchName("Env-Preview/42")
	require.NoError(t, err)
	require.Equal(t, "af_b_env_preview_42", got)
	require.NoError(t, checkName(got))
}

func TestBranchNameRefusesNoEnvironment(t *testing.T) {
	_, err := branchName("   ")
	require.Error(t, err, "a branch with no environment would be named af_b_ and shared by everything")
}

func TestCheckNameRefusesADatabaseThisProviderDidNotName(t *testing.T) {
	// Every one of these is a database name this provider must never create,
	// rename or drop. The guard is what stands between a bug in a caller and
	// somebody's production database.
	for _, name := range []string{
		"postgres", "template1", "antifailure", "",
		"af_x_something",                  // not one of the three kinds
		`af_b_x"; DROP DATABASE prod; --`, // a name carrying a statement
		"AF_B_X",                          // Postgres would fold this, the guard must not
	} {
		require.Error(t, checkName(name), "checkName accepted %q", name)
	}
	require.NoError(t, checkName("af_b_env_1"))
	require.NoError(t, checkName("af_g_gv_20260907120000000000_abcd1234"))
	require.NoError(t, checkName("af_c_1757241600000000000"))
}

func TestNormalizeDefaultsTheDatabaseAndKeepsTheRest(t *testing.T) {
	got, hostport, err := normalize(secrets.New("postgres://u:p@db.example.test:6543?sslmode=require"), "V")
	require.NoError(t, err)
	require.Equal(t, "db.example.test:6543", hostport)
	// The admin connection must land on a database this provider never drops.
	// Without the default it lands on "", the driver falls back to the role's
	// own name, and CREATE DATABASE is issued from a database that may not
	// exist.
	require.Contains(t, got.Reveal(), "/postgres")
	require.Contains(t, got.Reveal(), "sslmode=require")
}

func TestNormalizeSuppliesThePortInMessagesWhenTheURLOmitsIt(t *testing.T) {
	_, hostport, err := normalize(secrets.New("postgres://u:p@db.example.test/app"), "V")
	require.NoError(t, err)
	require.Equal(t, "db.example.test:5432", hostport)
}

func TestNormalizeRefusesWhatIsNotAPostgresURL(t *testing.T) {
	for _, raw := range []string{
		"https://db.example.test/app", // a scheme the driver would reject later, with a worse message
		"postgres:///app",             // no host at all
		"not a url at all",
	} {
		_, _, err := normalize(secrets.New(raw), "V")
		require.Error(t, err, "normalize accepted %q", raw)
		require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB024)),
			"a bad connection string must fail with AF-DB-024, and %q failed with %v", raw, err)
	}
}

func TestHostPortOfNeverCarriesTheCredential(t *testing.T) {
	// This value is PRINTED, by af status, on a rung that names where the
	// goldens live. The whole point of the helper is that the thing printed is
	// the address and not the password beside it.
	got := HostPortOf(secrets.New("postgres://someone:hunter2@db.example.test:6543/app"))
	require.Equal(t, "db.example.test:6543", got)
	require.NotContains(t, got, "hunter2")
	require.NotContains(t, got, "someone")
}

func TestQuoteLiteralSurvivesAnAttestationWithQuotesInIt(t *testing.T) {
	require.Equal(t, `'{"a":"b''c"}'`, quoteLiteral(`{"a":"b'c"}`))
	require.Equal(t, `"af_b_x"`, quoteIdent("af_b_x"))
}

func TestNewRefusesAServerThatIsNotThere(t *testing.T) {
	// Port 1 is not a Postgres. The refusal has to arrive here, at
	// construction, rather than after a refresh has already read production.
	_, err := New(context.Background(), Options{
		AdminURL: secrets.New("postgres://postgres:test@127.0.0.1:1/postgres"),
		Variable: "AF_PGURL_ADMIN_URL",
		Clock:    clock.New(),
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB034)),
		"an unreachable server must fail with AF-DB-034 and it failed with %v", err)
	require.NotContains(t, err.Error(), "test", "the refusal quoted the password")
}

func TestNewRefusesNoAdminURLAtAll(t *testing.T) {
	_, err := New(context.Background(), Options{Clock: clock.New()})
	require.Error(t, err)
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFSEC001)),
		"a missing admin URL must fail with AF-SEC-001 and it failed with %v", err)
}

// The rest need a real Postgres, because every property below is one Postgres
// decides and none of them is one a fake could establish.

func TestNewRefusesARoleThatCannotCreateDatabases(t *testing.T) {
	admin := requirePostgres(t)
	ctx := context.Background()
	requireSuperuser(t, admin)

	const role = "af_pgurl_test_nocreate"
	execAdmin(t, admin, "DROP ROLE IF EXISTS "+role)
	execAdmin(t, admin, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD 'test' NOCREATEDB", role))
	t.Cleanup(func() { execAdmin(t, admin, "DROP ROLE IF EXISTS "+role) })

	_, err := New(ctx, Options{
		AdminURL: secrets.New(withRole(admin, role, "test")),
		Variable: "AF_PGURL_ADMIN_URL",
		Clock:    clock.New(),
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB035)),
		"a role without CREATEDB must be refused with AF-DB-035, and it failed with %v", err)
}

func TestDropRefusesADatabaseThisProviderDidNotCreate(t *testing.T) {
	p, ctx := newProvider(t)

	// A database whose NAME says it is a branch and whose comment says nothing.
	// Somebody else's. It is not hypothetical: two projects on one server, or
	// a person who reused an environment identifier, produce exactly this.
	const name = "af_b_not_ours_at_all"
	execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	execAdmin(t, p.admin.Reveal(), "CREATE DATABASE "+name)
	t.Cleanup(func() { execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })

	err := p.Destroy(ctx, provider.Branch{ProviderRef: name, EnvID: "not_ours_at_all"})
	require.Error(t, err, "the provider dropped a database it never created")
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB036)),
		"a name collision must be refused with AF-DB-036, and it failed with %v", err)

	_, found, err := p.lookup(ctx, name)
	require.NoError(t, err)
	require.True(t, found, "the database was dropped despite the refusal")
}

func TestInventoryDoesNotReportADatabaseItDoesNotOwn(t *testing.T) {
	p, ctx := newProvider(t)

	// The inventory is what the leak detector compares against the journal,
	// which makes it a list of things somebody is about to delete. A database
	// that merely shares the naming scheme must never appear in it.
	const name = "af_b_someone_elses"
	execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	execAdmin(t, p.admin.Reveal(), "CREATE DATABASE "+name)
	t.Cleanup(func() { execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })
	// A comment that parses as JSON and is not ours, which is the case the
	// marker exists for. With no comment at all the listing would exclude it
	// for a second reason, the parse failing, and then this test would pass
	// with the marker check deleted. It did: removing the check left this
	// green until the comment was added.
	execAdmin(t, p.admin.Reveal(),
		`COMMENT ON DATABASE `+name+` IS '{"owner":"another tool","kind":"golden"}'`)

	items, err := p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range items {
		require.NotEqual(t, name, r.ID,
			"the inventory reports a database this provider never created, and the leak "+
				"detector would propose deleting it")
	}
}

func TestRefreshRefusesToWriteOverADatabaseThatIsNotOurs(t *testing.T) {
	p, ctx := newProvider(t)

	// The same collision from the other direction: the name a branch would
	// take is already held by somebody else's database, and creating it must
	// refuse rather than adopt it.
	const name = "af_b_collides"
	execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	execAdmin(t, p.admin.Reveal(), "CREATE DATABASE "+name)
	t.Cleanup(func() { execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })

	err := p.createDatabase(ctx, name, "")
	require.Error(t, err)
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB036)),
		"creating over a database that is not ours must be refused with AF-DB-036, and it failed with %v", err)
}

func TestBranchRefusesAGoldenNothingScanned(t *testing.T) {
	p, ctx := newProvider(t)

	// A refresh with no verifier publishes honestly, with Verified false. What
	// must not then happen is that anybody branches it: the branch would carry
	// production data that no scan ever looked at.
	spec := testSpec()
	spec.Verify = nil
	gv, err := p.RefreshGolden(ctx, spec)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })
	require.False(t, gv.Verified, "a refresh with no verifier reported the golden as verified")

	// Cleaned up before it is attempted, not after it succeeds. A provider
	// that wrongly ALLOWS this leaves a branch behind, and Branch is
	// idempotent by environment, so the next run of this test would find that
	// branch, return it, and report no error: the test would pass for exactly
	// the build it exists to fail. Mutation testing is what found that, by
	// leaving the suite unable to go green again after one red run.
	t.Cleanup(func() { _ = p.Destroy(context.Background(), provider.Branch{EnvID: "env_unverified"}) })

	_, err = p.Branch(ctx, gv.ID, "env_unverified")
	require.Error(t, err, "an unverified golden was branched; this is the product's central promise")
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFMSK001)),
		"branching an unverified golden must fail with AF-MSK-001, and it failed with %v", err)
}

func TestTheGoldenIsSealedOnceItIsPublished(t *testing.T) {
	p, ctx := newProvider(t)
	gv := refresh(t, p, ctx)

	var isTemplate, allowConn bool
	queryAdmin(t, p.admin.Reveal(),
		"SELECT datistemplate, datallowconn FROM pg_database WHERE datname = $1",
		goldenPrefix+gv.ID, &isTemplate, &allowConn)

	// Both halves matter and they answer different failures. IS_TEMPLATE is
	// what makes Postgres refuse to drop it, so a golden cannot go while an
	// environment is still using its copy. ALLOW_CONNECTIONS false is what
	// stops one forgotten psql making every subsequent branch fail, because
	// CREATE DATABASE refuses while a session is connected to the template.
	require.True(t, isTemplate, "the published golden is not marked as a template, so it can be dropped by accident")
	require.False(t, allowConn, "the published golden still accepts connections, so it can drift from what was verified")

	db, err := sql.Open("pgx", p.urlFor(goldenPrefix+gv.ID).Reveal())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var one int
	require.Error(t, db.QueryRowContext(ctx, "SELECT 1").Scan(&one),
		"the golden accepted a connection")
}

func TestAGoldenThatWasNeverFinishedIsNotListed(t *testing.T) {
	p, ctx := newProvider(t)

	// The crash window: the comment is written and the rename has not
	// happened, or the rename happened and the process died before the marker
	// said golden. Either way the listing must not offer it, because what it
	// holds is an unverified, unmasked, half loaded copy.
	name := goldenPrefix + "gv_19700101000000000000_deadbeef"
	require.NoError(t, p.createDatabase(ctx, name, ""))
	t.Cleanup(func() { _ = p.dropDatabase(context.Background(), name) })
	require.NoError(t, p.comment(ctx, name, meta{
		Antifailure: marker, Kind: "candidate", Version: "gv_19700101000000000000_deadbeef",
		CreatedAt: time.Unix(0, 0).UTC(),
	}))

	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	for _, g := range goldens {
		require.NotEqual(t, "gv_19700101000000000000_deadbeef", g.ID,
			"a half published golden appears in the listing, so af up could branch it")
	}
}

// TestBranchIsWithinTheLatencyItDeclares is the assertion the wave preamble
// expects the shared suite to make and the shared suite does NOT make.
//
// engine/conformance/db.go:531 requires only that ExpectedBranchLatency is
// greater than zero; nothing anywhere compares it against a measured branch.
// So a provider can declare eight seconds, take three minutes, and stay green.
// Generalising that is a change to the shared suite, a new fault in
// testutil/fakes and a new row in the self test's table, which belongs to the
// lane that owns the suite rather than to this one. Until then the property
// holds here, for this provider, which is the provider whose branch time grows
// with the customer's database and therefore the one most likely to drift.
func TestBranchIsWithinTheLatencyItDeclares(t *testing.T) {
	p, ctx := newProvider(t)
	gv := refresh(t, p, ctx)

	start := time.Now()
	b, err := p.Branch(ctx, gv.ID, "env_latency")
	require.NoError(t, err)
	took := time.Since(start)
	t.Cleanup(func() { _ = p.Destroy(context.Background(), b) })

	declared := p.Capabilities().ExpectedBranchLatency
	require.LessOrEqual(t, took, declared,
		"branching the conformance dataset took %s and this provider declares %s; "+
			"either the server got slower or the declaration is wrong, and a declaration "+
			"nothing checks is what lets a provider degrade quietly", took, declared)
	t.Logf("branch of the conformance dataset: %s, declared %s", took.Round(time.Millisecond), declared)
}

func TestResetRewindsTheBranchIncludingItsSequences(t *testing.T) {
	p, ctx := newProvider(t)
	gv := refresh(t, p, ctx)
	b, err := p.Branch(ctx, gv.ID, "env_reset")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Destroy(context.Background(), b) })

	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	execURL(t, conn.Reveal(), "INSERT INTO conformance_users (email) VALUES ('written@example.test')")
	require.Equal(t, 4, countUsers(t, conn.Reveal()))

	require.NoError(t, p.Reset(ctx, b))
	conn, err = p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	require.Equal(t, 3, countUsers(t, conn.Reveal()), "reset did not rewind the rows")
	// The sequence is the half a dump replay gets wrong, and a collision on
	// the next insert is an application bug nobody can reproduce.
	execURL(t, conn.Reveal(), "INSERT INTO conformance_users (email) VALUES ('after@example.test')")
	require.Equal(t, 4, countUsers(t, conn.Reveal()), "the sequence was not rewound")
}

func TestDestroyFindsTheBranchFromTheEnvironmentAlone(t *testing.T) {
	p, ctx := newProvider(t)
	gv := refresh(t, p, ctx)
	b, err := p.Branch(ctx, gv.ID, "env_by_environment")
	require.NoError(t, err)

	// A journal written by an older build, or a caller that kept only the
	// environment, carries no provider reference. Teardown that quietly did
	// nothing here would report success and leave a copy of production on
	// somebody's server for ever, which is the leak the whole journal exists
	// to prevent.
	require.NoError(t, p.Destroy(ctx, provider.Branch{EnvID: "env_by_environment"}))

	_, found, err := p.lookup(ctx, b.ProviderRef)
	require.NoError(t, err)
	require.False(t, found, "destroying by environment alone left the branch database behind")
}

func TestARefreshSurvivesAConnectionTheMaskingLeftOpen(t *testing.T) {
	p, ctx := newProvider(t)

	// Masking and verification are given a connection string and are somebody
	// else's code. One that returns without closing its pool leaves a session
	// on the candidate, and Postgres refuses to rename a database that has
	// one. The refresh would then fail at the very last step, after the whole
	// copy had been paid for.
	var held *sql.DB
	spec := testSpec()
	spec.Version = p.major
	spec.Mask = func(_ context.Context, candidate secrets.Value) error {
		db, err := sql.Open("pgx", candidate.Reveal())
		if err != nil {
			return err
		}
		var one int
		if err := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one); err != nil {
			return err
		}
		held = db // deliberately not closed
		return nil
	}
	gv, err := p.RefreshGolden(ctx, spec)
	if held != nil {
		defer func() { _ = held.Close() }()
	}
	require.NoError(t, err, "a session left open on the candidate stopped the golden being published")
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })
	require.NotEmpty(t, gv.ID)
}

func TestARefreshSweepsACandidateNothingCanBeUsing(t *testing.T) {
	p, ctx := newProvider(t)

	// A killed process leaves a candidate holding a full copy of production.
	// Nothing ever references a candidate, so removing an old one is safe, and
	// without this it stays on the server until a person notices.
	name := candidatePrefix + "1"
	require.NoError(t, p.createDatabase(ctx, name, ""))
	t.Cleanup(func() { _ = p.dropDatabase(context.Background(), name) })
	require.NoError(t, p.comment(ctx, name, meta{
		Antifailure: marker, Kind: "candidate",
		CreatedAt: time.Now().Add(-24 * time.Hour).UTC(),
	}))

	refresh(t, p, ctx)

	_, found, err := p.lookup(ctx, name)
	require.NoError(t, err)
	require.False(t, found, "an abandoned candidate survived a refresh, so a killed run leaks a copy of production")
}

// helpers

func newProvider(t *testing.T) (*Provider, context.Context) {
	t.Helper()
	admin := requirePostgres(t)
	ctx := context.Background()
	p, err := New(ctx, Options{
		AdminURL: secrets.New(admin),
		Variable: "AF_PGURL_ADMIN_URL",
		SeedSQL:  conformance.DefaultSeedSQL,
		Clock:    clock.New(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return p, ctx
}

func testSpec() provider.GoldenSpec {
	return provider.GoldenSpec{
		SourceURL: secrets.New("postgres://conformance@source/db"),
		Version:   17,
		RulesHash: fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff),
		Verify: func(context.Context, secrets.Value) (string, error) {
			return `{"scanner":"pgurl-test","findings":0}`, nil
		},
	}
}

func refresh(t *testing.T, p *Provider, ctx context.Context) provider.GoldenVersion {
	t.Helper()
	spec := testSpec()
	spec.Version = p.major
	gv, err := p.RefreshGolden(ctx, spec)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })
	return gv
}

func requirePostgres(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("AF_PGURL_ADMIN_URL")
	if raw == "" {
		raw = os.Getenv("AF_TEST_DATABASE_URL")
	}
	if raw == "" {
		raw = "postgres://postgres:test@127.0.0.1:55432/antifailure"
	}
	db, err := sql.Open("pgx", raw)
	if err == nil {
		defer func() { _ = db.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var one int
		err = db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
	}
	if err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and %s did not answer: %v",
				HostPortOf(secrets.New(raw)), err)
		}
		t.Skipf("skipped: no Postgres answered at %s: %v", HostPortOf(secrets.New(raw)), err)
	}
	return raw
}

func requireSuperuser(t *testing.T, admin string) {
	t.Helper()
	var super bool
	queryAdmin(t, admin, "SELECT rolsuper FROM pg_roles WHERE rolname = current_user", nil, &super)
	if !super {
		t.Skip("skipped: creating a role to test the CREATEDB refusal needs a superuser")
	}
}

func execAdmin(t *testing.T, admin, stmt string) {
	t.Helper()
	execURL(t, admin, stmt)
}

func execURL(t *testing.T, url, stmt string) {
	t.Helper()
	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(context.Background(), stmt)
	require.NoError(t, err, "running %q", stmt)
}

func queryAdmin(t *testing.T, admin, query string, arg any, dest ...any) {
	t.Helper()
	db, err := sql.Open("pgx", admin)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var args []any
	if arg != nil {
		args = append(args, arg)
	}
	require.NoError(t, db.QueryRowContext(context.Background(), query, args...).Scan(dest...))
}

func countUsers(t *testing.T, url string) int {
	t.Helper()
	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		"SELECT count(*) FROM conformance_users").Scan(&n))
	return n
}

// withRole rewrites a connection string to log in as another role.
func withRole(raw, role, password string) string {
	at := strings.Index(raw, "@")
	scheme := strings.Index(raw, "://")
	return raw[:scheme+3] + role + ":" + password + raw[at:]
}

func TestConnStringAnswersTheWayTheCallerAsked(t *testing.T) {
	p, ctx := newProvider(t)
	gv := refresh(t, p, ctx)
	b, err := p.Branch(ctx, gv.ID, "env_connstring")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Destroy(context.Background(), b) })

	// A pooler in front of this server is the operator's to run, and this
	// provider would be guessing at its address. Declaring the capability it
	// does not have would make the conformance suite pass a behaviour it
	// should skip.
	_, err = p.ConnString(ctx, b, provider.ConnPooled)
	require.ErrorIs(t, err, provider.ErrUnsupported)

	// A caller holding only the environment, which is what a journal written
	// by an older build carries.
	byEnv, err := p.ConnString(ctx, provider.Branch{EnvID: "env_connstring"}, provider.ConnDirect)
	require.NoError(t, err)
	direct, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	require.True(t, byEnv.Equal(direct), "the two ways of naming one branch gave two connections")

	// Which refusal a missing branch gets depends on what was asked for, and
	// getting it wrong sends somebody to the wrong command: af up for a branch
	// that was never made, af golden refresh for a golden that is gone.
	_, err = p.ConnString(ctx, provider.Branch{EnvID: "env_never_branched"}, provider.ConnDirect)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB014))
	_, err = p.ConnString(ctx,
		provider.Branch{EnvID: "env_never_branched", From: "gv_19700101000000000000_deadbeef"},
		provider.ConnDirect)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFDB004))
}

func TestResetRefusesABranchThatDoesNotSayWhereItCameFrom(t *testing.T) {
	p, ctx := newProvider(t)
	// Reset destroys and recreates from the golden. A branch with no golden
	// recorded would be destroyed and not recreated, which is a reset that
	// deletes an environment's database and reports success.
	err := p.Reset(ctx, provider.Branch{EnvID: "env_no_from", ProviderRef: "af_b_env_no_from"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not record which golden")
}

func TestARefreshGivesTheSubsetLoaderTheEmptyCandidate(t *testing.T) {
	p, ctx := newProvider(t)

	// The Subsetting capability is declared, and a declared capability is a
	// promise the conformance suite checks for everything except this: the
	// suite hands every provider a source URL that does not resolve, so the
	// Load path never runs there. Declaring it and never exercising it is how
	// a manifest asking for a subset ends up copying everything.
	source := "af_bench_subset_source"
	execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+source+" WITH (FORCE)")
	execAdmin(t, p.admin.Reveal(), "CREATE DATABASE "+source)
	t.Cleanup(func() {
		execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+source+" WITH (FORCE)")
	})

	called := 0
	spec := testSpec()
	spec.Version = p.major
	spec.SourceURL = p.urlFor(source)
	spec.Load = func(_ context.Context, from, into secrets.Value) error {
		called++
		require.Equal(t, spec.SourceURL.Reveal(), from.Reveal(),
			"the loader was given something other than the source it was asked to slice")
		// The candidate has to be EMPTY and it has to be writable: that is the
		// whole reason this provider declares the capability, and the reason
		// the engine takes the slice rather than the provider.
		require.Equal(t, 0, tablesIn(t, into.Reveal()), "the candidate handed to the loader is not empty")
		execURL(t, into.Reveal(), "CREATE TABLE subset_rows (id int primary key)")
		execURL(t, into.Reveal(), "INSERT INTO subset_rows VALUES (1), (2)")
		return nil
	}
	gv, err := p.RefreshGolden(ctx, spec)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })
	require.Equal(t, 1, called, "the provider copied the source itself instead of asking the loader")

	b, err := p.Branch(ctx, gv.ID, "env_subset")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Destroy(context.Background(), b) })
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	var n int
	queryURL(t, conn.Reveal(), "SELECT count(*) FROM subset_rows", &n)
	require.Equal(t, 2, n, "what the loader wrote is not in the branch")
}

func TestHostPortOfSaysNothingRatherThanGuessing(t *testing.T) {
	// It is used to PRINT where the goldens live. A value that is not a URL
	// has no host to print, and printing the value itself would print a
	// credential.
	require.Equal(t, "", HostPortOf(secrets.New("not a url at all")))
	require.Equal(t, "", HostPortOf(secrets.Value{}))
}

func tablesIn(t *testing.T, url string) int {
	t.Helper()
	var n int
	queryURL(t, url, "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'", &n)
	return n
}

func queryURL(t *testing.T, url, query string, dest ...any) {
	t.Helper()
	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	require.NoError(t, db.QueryRowContext(context.Background(), query).Scan(dest...))
}

func TestARefreshCopiesTheSourceItWasGiven(t *testing.T) {
	p, ctx := newProvider(t)

	// The copy itself, which is the thing this provider exists to make
	// possible and the one path the conformance suite cannot exercise: the
	// suite hands every provider a source URL that does not resolve, because
	// what it is testing is the provider and not pg_dump. So without this,
	// pgcopy.Copy has no caller in any test that runs on a pull request, and
	// the provider's whole purpose would be proved by a benchmark nobody runs.
	source := "af_e2e_copy_source"
	execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+source+" WITH (FORCE)")
	execAdmin(t, p.admin.Reveal(), "CREATE DATABASE "+source)
	t.Cleanup(func() {
		execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+source+" WITH (FORCE)")
	})
	execURL(t, p.urlFor(source).Reveal(), `
CREATE TABLE conformance_users (
    id bigserial PRIMARY KEY,
    email text NOT NULL UNIQUE
);
INSERT INTO conformance_users (email) VALUES ('one@example.test'), ('two@example.test');`)

	spec := testSpec()
	spec.Version = p.major
	spec.SourceURL = p.urlFor(source)
	gv, err := p.RefreshGolden(ctx, spec)
	if err != nil && strings.Contains(err.Error(), "pg_dump") {
		t.Skipf("skipped: no usable pg_dump on this machine: %v", err)
	}
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })

	b, err := p.Branch(ctx, gv.ID, "env_copied")
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Destroy(context.Background(), b) })
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	require.Equal(t, 2, countUsers(t, conn.Reveal()),
		"the branch does not hold the source's rows, so nothing was actually copied")

	// The source is untouched. It is somebody's production database and the
	// only thing this provider may ever do to it is read.
	require.Equal(t, 2, countUsers(t, p.urlFor(source).Reveal()))
}

func TestARefreshRefusesAVersionTheServerDoesNotRun(t *testing.T) {
	p, ctx := newProvider(t)

	// The version is the server's and there is no other version it could be.
	// A provider that accepted this would build the golden on whatever the
	// server runs and hand the application a database that agrees with
	// production until the day it does not.
	spec := testSpec()
	spec.Version = p.major + 1
	_, err := p.RefreshGolden(ctx, spec)
	require.Error(t, err)
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB003)),
		"a version the server does not run must be refused with AF-DB-003, and it failed with %v", err)
	require.Contains(t, err.Error(), strconv.Itoa(p.major), "the refusal does not say what the server runs")
}

func TestBranchRefusesToAdoptADatabaseThatIsNotOurs(t *testing.T) {
	p, ctx := newProvider(t)
	gv := refresh(t, p, ctx)

	// The collision on the branch path. The name an environment would take is
	// already held by somebody else's database, and the idempotency check is
	// what would otherwise HAND IT BACK as though this provider had made it.
	const name = "af_b_env_collision"
	execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	execAdmin(t, p.admin.Reveal(), "CREATE DATABASE "+name)
	t.Cleanup(func() { execAdmin(t, p.admin.Reveal(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })

	_, err := p.Branch(ctx, gv.ID, "env_collision")
	require.Error(t, err)
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB036)),
		"branching onto a database that is not ours must be refused with AF-DB-036, and it failed with %v", err)
}

func TestAMissingGoldenSaysWhichOnesExist(t *testing.T) {
	p, ctx := newProvider(t)
	gv := refresh(t, p, ctx)

	// What DOES exist, at the moment the missing one was asked for. For a
	// person this is the difference between "that version is gone" and being
	// told which versions they could have asked for instead.
	_, err := p.Branch(ctx, "gv_19700101000000000000_deadbeef", "env_missing_golden")
	require.Error(t, err)
	require.Contains(t, err.Error(), gv.ID, "the refusal does not name the versions that exist")
}

func TestDestroyAndConnStringRefuseABranchWithNoIdentityAtAll(t *testing.T) {
	p, ctx := newProvider(t)
	// Neither an identifier nor an environment names anything. Destroying
	// "nothing" quietly would report a teardown that removed a database it
	// never found.
	require.Error(t, p.Destroy(ctx, provider.Branch{}))
	_, err := p.ConnString(ctx, provider.Branch{}, provider.ConnDirect)
	require.Error(t, err)
}

func TestNewTakesTheWallClockWhenNobodyPassesOne(t *testing.T) {
	admin := requirePostgres(t)
	p, err := New(context.Background(), Options{AdminURL: secrets.New(admin)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.NotNil(t, p.clock, "a provider with no clock panics on its first refresh")
	require.Equal(t, DefaultVariable, p.variable, "the variable name a refusal would print is empty")
}

func TestNormalizeRefusesAValueThatIsNotEvenAURL(t *testing.T) {
	_, _, err := normalize(secrets.New("postgres://user:pass@ho st:5432/db"), "V")
	require.Error(t, err)
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB024)))
}

func TestShortErrorOnNothingIsNothing(t *testing.T) {
	// It is called on paths that may have no error, and returning "<nil>"
	// there would put that word in a message a person reads.
	require.Equal(t, "", shortError(nil))
}
