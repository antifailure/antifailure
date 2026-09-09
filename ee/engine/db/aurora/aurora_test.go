// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora_test

// What this provider claims, checked one claim at a time.
//
// The conformance suite next door checks the twenty four behaviours every
// database provider owes. These check the things that are true of THIS one and
// are the reason it exists: that a clone is asked for copy on write and never
// any other way, that branching does no work proportional to the database,
// that a clone's master password is not the one it inherited, and that a
// golden stops costing compute once it is published.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
	"github.com/antifailure/antifailure/ee/engine/db/aurora"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

const seedSQL = `
CREATE TABLE people (id bigserial PRIMARY KEY, email text NOT NULL);
INSERT INTO people (email) VALUES ('one@example.test'), ('two@example.test');
`

// spec is a refresh whose callbacks record what they were given and connect to
// nothing.
//
// Connecting to nothing is deliberate in most of these: the masking and the
// verification are the engine's, they are proved elsewhere, and a test here
// that opened a connection would stop being able to say whether the PROVIDER
// opened one.
func spec(rules string) (provider.GoldenSpec, *specRecord) {
	record := &specRecord{}
	return provider.GoldenSpec{
		SourceURL:  secret.New("postgres://this-provider-never-reads-this/db"),
		Version:    16,
		RulesHash:  rules,
		Provenance: "gp1-aurora-" + rules,
		Mask: func(_ context.Context, url secret.Value) error {
			record.masked++
			record.maskURL = url
			return nil
		},
		Verify: func(_ context.Context, url secret.Value) (string, error) {
			record.verified++
			record.verifyURL = url
			if record.fail {
				return "", fmt.Errorf("verification found data matching email in people.email")
			}
			if record.masked == 0 {
				return "", fmt.Errorf("verify ran before mask")
			}
			return `{"scanner":"aurora-test","findings":0}`, nil
		},
	}, record
}

type specRecord struct {
	masked    int
	verified  int
	fail      bool
	maskURL   secret.Value
	verifyURL secret.Value
}

func TestTheCloneIsAlwaysAskedForCopyOnWrite(t *testing.T) {
	// The distinguishing claim, and the only place it is visible. A response
	// to a full-copy restore and a response to a clone are the same shape, so
	// no assertion on the result can tell them apart. The request can.
	server := newFake(t, seedSQL, "")
	p := newProvider(t, server)
	ctx := context.Background()

	golden, record := spec("aaaa1111")
	version, err := p.RefreshGolden(ctx, golden)
	require.NoError(t, err)
	require.Equal(t, 1, record.masked)
	require.Equal(t, "copy-on-write",
		server.LastRequest("RestoreDBClusterToPointInTime").Get("RestoreType"),
		"the golden was not cloned; a full copy restore is a different provider with a "+
			"different number")

	_, err = p.Branch(ctx, version.ID, "env_one")
	require.NoError(t, err)
	require.Equal(t, "copy-on-write",
		server.LastRequest("RestoreDBClusterToPointInTime").Get("RestoreType"),
		"the branch was not cloned, so branch time is no longer flat in the size")
}

func TestBranchNeverOpensAConnectionToTheDatabase(t *testing.T) {
	// The measurable half of the flat branch claim, made falsifiable without
	// an AWS account: if branching touched a row, or opened a connection at
	// all, it could not be independent of how many rows there are.
	//
	// The proof is that the database is UNREACHABLE while the branch is made.
	// sslmode is require and the test Postgres speaks no TLS, so any
	// connection attempt fails. The branch still succeeds, and the assertion
	// at the end shows the endpoint really was unusable, which is what stops
	// this passing vacuously.
	server := newFake(t, seedSQL, "")
	opts := options(t, server)
	opts.TLSMode = "require"
	p, err := aurora.New(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	ctx := context.Background()
	golden, _ := spec("bbbb2222")
	version, err := p.RefreshGolden(ctx, golden)
	require.NoError(t, err)

	branch, err := p.Branch(ctx, version.ID, "env_unreachable")
	require.NoError(t, err,
		"branching opened a connection to the database, which it must not: a branch that "+
			"reads or writes a row is a branch whose cost depends on how many rows there are")

	connection, err := p.ConnString(ctx, branch, provider.ConnDirect)
	require.NoError(t, err)
	require.Error(t, dial(connection),
		"the database was reachable after all, so this test proved nothing about whether "+
			"Branch connected")
}

func TestBranchDoesTheSameWorkAtOneGigabyteAndAtOneTerabyte(t *testing.T) {
	// The number this lane owes, in the only form it can honestly take without
	// an AWS account. See benchmark_test.go for the published report and for
	// what is NOT measured here.
	server := newFake(t, seedSQL, "")
	ctx := context.Background()

	atSize := func(gb int64, env string) []string {
		server.SetSourceStorage(sourceCluster, gb)
		p := newProvider(t, server)
		golden, _ := spec(fmt.Sprintf("%08x", gb))
		version, err := p.RefreshGolden(ctx, golden)
		require.NoError(t, err)
		require.Equal(t, gb*(1<<30), version.SizeBytes,
			"the provider did not see the volume size it was told about, so this "+
				"comparison is between two identical inputs")
		server.Reset()
		_, err = p.Branch(ctx, version.ID, env)
		require.NoError(t, err)
		return server.Actions()
	}

	small := atSize(1, "env_small")
	large := atSize(1024, "env_large")

	require.Equal(t, small, large,
		"branching a one terabyte volume did different work than branching a one "+
			"gigabyte volume, so the flat branch claim is false")
	require.NotEmpty(t, small, "no control plane calls were recorded, so nothing was compared")
}

func TestEveryCloneGetsAMasterPasswordThatIsNotTheSourcesAndNotAnothersBranchs(t *testing.T) {
	// A clone inherits the source cluster's master credential. Left alone,
	// every preview environment would hold production's database password, and
	// the person who set this up would have no way to know.
	server := newFake(t, seedSQL, "")
	p := newProvider(t, server)
	ctx := context.Background()

	golden, _ := spec("cccc3333")
	version, err := p.RefreshGolden(ctx, golden)
	require.NoError(t, err)

	first, err := p.Branch(ctx, version.ID, "env_first")
	require.NoError(t, err)
	second, err := p.Branch(ctx, version.ID, "env_second")
	require.NoError(t, err)

	one := revealed(t, p, first)
	two := revealed(t, p, second)
	require.NotEqual(t, passwordIn(one), passwordIn(two),
		"two branches share a master password, so one environment's credential opens "+
			"another's database")
	require.NotContains(t, one, "test@",
		"the branch carries the source server's own password, which is the credential "+
			"this provider exists to stop handing out")
}

func TestAConnectionStringIsRebuiltByAProcessThatDidNotCreateTheBranch(t *testing.T) {
	// Nothing stores a password: not a tag, not the state directory, not the
	// journal. So the derivation has to be the same in a second process, which
	// is what af status, af logs and every teardown are.
	server := newFake(t, seedSQL, "")
	ctx := context.Background()

	first := newProvider(t, server)
	golden, _ := spec("dddd4444")
	version, err := first.RefreshGolden(ctx, golden)
	require.NoError(t, err)
	branch, err := first.Branch(ctx, version.ID, "env_second_process")
	require.NoError(t, err)
	before := revealed(t, first, branch)

	second := newProvider(t, server)
	after := revealed(t, second, provider.Branch{
		EnvID: branch.EnvID, From: branch.From, ProviderRef: branch.ProviderRef,
	})
	require.Equal(t, before, after,
		"a second provider built a different connection string for the same branch, so "+
			"every command that is not the one that created it would be locked out")
}

func TestTheDatabaseTheProviderHandsOutHoldsTheSourcesRows(t *testing.T) {
	// The clone is a clone. Everything else here is about control plane calls,
	// and a provider that made every call correctly and handed back an empty
	// database would pass all of them.
	server := newFake(t, seedSQL, "")
	p := newProvider(t, server)
	ctx := context.Background()

	golden, _ := spec("eeee5555")
	version, err := p.RefreshGolden(ctx, golden)
	require.NoError(t, err)
	branch, err := p.Branch(ctx, version.ID, "env_rows")
	require.NoError(t, err)

	connection, err := p.ConnString(ctx, branch, provider.ConnDirect)
	require.NoError(t, err)
	db, err := sql.Open("pgx", connection.Reveal())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var rows int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM people").Scan(&rows))
	require.Equal(t, 2, rows, "the branch does not hold the source's rows")
}

func TestAPublishedGoldenKeepsItsWriterInstance(t *testing.T) {
	// The obvious saving this provider does NOT take, asserted so that nobody
	// takes it later without an account to check it against.
	//
	// Deleting the writer would make a published golden cost storage and no
	// compute, and it ought to work, because a cluster's volume survives
	// without one and cloning is a cluster level operation. The only
	// instrument here that could say whether it does is the fake in this
	// repository, and a fake agreeing with the assumption that produced it is
	// not evidence. An untested cost saving that silently breaks branching is
	// worse than the standing cost.
	server := newFake(t, seedSQL, "")
	p := newProvider(t, server)
	ctx := context.Background()

	golden, _ := spec("ffff6666")
	version, err := p.RefreshGolden(ctx, golden)
	require.NoError(t, err)

	require.NotContains(t, server.Actions(), "DeleteDBInstance",
		"the golden's writer instance was deleted. That may well be safe and nobody "+
			"here can show it is: cloning a cluster with nothing attached is unproven "+
			"without an AWS account, and the fake would agree with either answer")

	_, err = p.Branch(ctx, version.ID, "env_after_publish")
	require.NoError(t, err)
}

func TestAClusterThatIsNotOursIsRefusedWithASentinel(t *testing.T) {
	// The refusal a caller has to be able to recognise without matching on
	// English, because it is the one that stops a misconfigured project
	// operating on somebody else's infrastructure.
	server := newFake(t, seedSQL, "")
	require.NoError(t, server.SeedSourceWithEngine(
		"af-b-ffffffffffff", "aurora-postgresql", "16.4", ""))
	p := newProvider(t, server)

	err := p.Destroy(context.Background(), provider.Branch{ProviderRef: "af-b-ffffffffffff"})
	require.ErrorIs(t, err, aurora.ErrNotOurs)
}

func TestAnAbandonedCandidateIsSweptByTheNextRefresh(t *testing.T) {
	// A candidate is a full clone of production with an instance attached. One
	// left by a process that died between the clone and the publish is
	// unreachable by ListGoldens, unbranchable by anything, and billing.
	server := newFake(t, seedSQL, "")
	ctx := context.Background()

	// A provider whose clock is a day ahead, so the candidate the first
	// refresh abandons is already older than the sweep's window when the
	// second refresh looks at it. The window is six hours and waiting for it
	// is not a test.
	opts := options(t, server)
	p, err := aurora.New(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	abandoned, record := spec("aaaa0001")
	record.fail = true
	_, err = p.RefreshGolden(ctx, abandoned)
	require.Error(t, err)

	// The failed refresh removes its own candidate, so the sweep has nothing
	// to find here and the assertion is that it runs at all and removes
	// nothing it should not. A candidate an abrupt death left behind cannot be
	// produced without killing a process mid call, so what is proved is the
	// sweep's selectivity rather than its trigger, and that is said plainly
	// rather than claimed as more.
	later := opts
	later.Now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	q, err := aurora.New(ctx, later)
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })

	golden, _ := spec("aaaa0002")
	version, err := q.RefreshGolden(ctx, golden)
	require.NoError(t, err)

	goldens, err := q.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, goldens, 1, "the sweep removed a golden it should not have")
	require.Equal(t, version.ID, goldens[0].ID)
}

func TestAFailedVerificationPublishesNothingAndLeavesNoCluster(t *testing.T) {
	// The product's central promise, and its expensive corollary: a clone that
	// failed its own scan is a cluster full of production data sitting in the
	// account. It has to go, and nothing may be able to branch it in the
	// meantime.
	server := newFake(t, seedSQL, "")
	p := newProvider(t, server)
	ctx := context.Background()

	golden, record := spec("9999aaaa")
	record.fail = true
	version, err := p.RefreshGolden(ctx, golden)
	require.Error(t, err)
	require.Empty(t, version.ID, "a version that exists after a failed verification is one "+
		"something else can branch")

	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Empty(t, goldens)

	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	require.Empty(t, inventory,
		"the clone that failed verification is still in the account, holding the "+
			"production data it was refused for")
}

func TestRefusesASourceClusterThatIsNotAuroraPostgres(t *testing.T) {
	// The refusal that must not become a substitution. RDS for PostgreSQL
	// cannot be cloned, a snapshot restore would work and would copy every
	// byte, and a flat cost quietly becoming linear is worse than an error
	// because nobody measures a thing that still appears to work.
	server := newFake(t, seedSQL, "")
	require.NoError(t, server.SeedSourceWithEngine("acme-rds", "postgres", "16.4", ""))

	opts := options(t, server)
	opts.SourceCluster = "acme-rds"
	_, err := aurora.New(context.Background(), opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "copy on write")
	require.Contains(t, err.Error(), "aurora-postgresql")
}

func TestRefusesASourceClusterThatDoesNotExist(t *testing.T) {
	server := newFake(t, seedSQL, "")
	opts := options(t, server)
	opts.SourceCluster = "not-a-cluster"
	_, err := aurora.New(context.Background(), opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "DB CLUSTER identifier")
}

func TestAMissignedRequestIsRefusedByTheFake(t *testing.T) {
	// The instrument has to be able to say no, or every signature assertion in
	// this package is decoration. This points the provider at the fake with
	// the wrong secret key and requires a refusal, which is what AWS answers
	// for the same request.
	server := newFake(t, seedSQL, "")
	opts := options(t, server)
	opts.Credentials = &cloudauth.AWSCredentials{
		AccessKeyID:     testCredentials.AccessKeyID,
		SecretAccessKey: "not-the-secret-these-were-signed-with",
	}
	_, err := aurora.New(context.Background(), opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "SignatureDoesNotMatch")
}

func TestASignatureForTheWrongRegionIsRefused(t *testing.T) {
	// The mistake this catches is a real one and it is silent: a provider that
	// signs for us-east-1 while talking to eu-west-1 produces a 403 that reads
	// exactly like a wrong secret key.
	server := newFake(t, seedSQL, "")
	opts := options(t, server)
	opts.Region = "us-east-1"
	_, err := aurora.New(context.Background(), opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "SignatureDoesNotMatch")
}

func TestDestroyLeavesAClusterThisProviderDidNotCreate(t *testing.T) {
	// Nothing is removed on the strength of its name. A customer whose own
	// cluster is called af-b-something must not lose it to our teardown, and
	// the only thing separating the two is a tag we wrote.
	server := newFake(t, seedSQL, "")
	require.NoError(t, server.SeedSourceWithEngine(
		"af-b-000000000000", "aurora-postgresql", "16.4", ""))
	p := newProvider(t, server)

	err := p.Destroy(context.Background(), provider.Branch{ProviderRef: "af-b-000000000000"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not created by antifailure")

	_, stillThere := server.DatabaseOf("af-b-000000000000")
	require.True(t, stillThere, "a cluster this provider did not create was destroyed")
}

func TestBranchIsIdempotentByEnvironment(t *testing.T) {
	// The engine retries after a timeout. A retry that made a second cluster
	// would be an orphan nothing names and everything pays for.
	server := newFake(t, seedSQL, "")
	p := newProvider(t, server)
	ctx := context.Background()

	golden, _ := spec("7777bbbb")
	version, err := p.RefreshGolden(ctx, golden)
	require.NoError(t, err)

	first, err := p.Branch(ctx, version.ID, "env_retried")
	require.NoError(t, err)
	server.Reset()
	second, err := p.Branch(ctx, version.ID, "env_retried")
	require.NoError(t, err)

	require.Equal(t, first.ProviderRef, second.ProviderRef)
	require.NotContains(t, server.Actions(), "RestoreDBClusterToPointInTime",
		"the second branch cloned again, so a retried af up pays for two clusters")
}

func TestPooledConnectionStringsAreRefusedRatherThanFaked(t *testing.T) {
	// RDS Proxy is a separate resource with its own IAM and its own subnet
	// group, and this provider does not create one. Handing back the direct
	// string would be a pool that is not one.
	server := newFake(t, seedSQL, "")
	p := newProvider(t, server)
	ctx := context.Background()

	golden, _ := spec("6666cccc")
	version, err := p.RefreshGolden(ctx, golden)
	require.NoError(t, err)
	branch, err := p.Branch(ctx, version.ID, "env_pooled")
	require.NoError(t, err)

	_, err = p.ConnString(ctx, branch, provider.ConnPooled)
	require.ErrorIs(t, err, provider.ErrUnsupported)
	require.False(t, p.Capabilities().PooledEndpoints)
}

func TestResetIsRefusedRatherThanRecreated(t *testing.T) {
	// Aurora's only rewind is Backtrack and that is MySQL. Destroying the
	// clone and cloning again would work and is exactly what the capability
	// says this is not, and answering yes here would take the choice away from
	// the engine.
	server := newFake(t, seedSQL, "")
	p := newProvider(t, server)
	require.False(t, p.Capabilities().Reset)
	require.ErrorIs(t, p.Reset(context.Background(), provider.Branch{ProviderRef: "af-b-1"}),
		provider.ErrUnsupported)
}

func TestCapabilitiesDeclareCopyOnWriteAndNotSubsetting(t *testing.T) {
	server := newFake(t, seedSQL, "")
	caps := newProvider(t, server).Capabilities()
	require.True(t, caps.CopyOnWrite)
	// A clone has the whole database the moment it exists, so there is no
	// empty candidate to load a slice into. Declaring subsetting would mean a
	// manifest key that reads as configuration and behaves as decoration.
	require.False(t, caps.Subsetting)
	require.GreaterOrEqual(t, caps.ExpectedBranchLatency, time.Second,
		"a provider must declare its expected branch latency")
}

// dial opens the connection string and closes it, returning what happened.
func dial(connection secret.Value) error {
	db, err := sql.Open("pgx", connection.Reveal())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

func revealed(t *testing.T, p *aurora.Provider, b provider.Branch) string {
	t.Helper()
	connection, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	return connection.Reveal()
}

// passwordIn returns the password out of a Postgres URL, for comparing two of
// them without printing either.
func passwordIn(url string) string {
	_, after, found := strings.Cut(url, "://")
	if !found {
		return ""
	}
	credentials, _, _ := strings.Cut(after, "@")
	_, password, _ := strings.Cut(credentials, ":")
	return password
}
