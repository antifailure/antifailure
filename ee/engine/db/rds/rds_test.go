// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// What the shared conformance suite does not check, and this provider's whole
// mechanism lives in it.
//
// The suite asks whether a branch holds the golden's rows. It does not ask
// whether the golden was built by snapshotting the source and restoring it,
// whether the intermediate snapshot was removed afterwards, whether the master
// password the restore inherited from production was rotated before anything
// connected, or whether the delete asked RDS to skip the final snapshot. Every
// one of those is invisible in a result and visible in a request, so these
// tests read the requests.

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/cloudauth"
	"github.com/antifailure/antifailure/ee/engine/db/rds"
	"github.com/antifailure/antifailure/ee/engine/db/rds/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// spec is a refresh whose callbacks record what happened, in the shape the
// engine hands a provider.
func spec(masked, verified *int, attestation string) provider.GoldenSpec {
	return provider.GoldenSpec{
		SourceURL:  secret.New("postgres://conformance@source/db"),
		Version:    17,
		RulesHash:  "abcd1234",
		Provenance: "gp1-rds-test",
		Mask: func(context.Context, secret.Value) error {
			*masked++
			return nil
		},
		Verify: func(context.Context, secret.Value) (string, error) {
			*verified++
			return attestation, nil
		},
	}
}

func refresh(t *testing.T, p *rds.Provider) provider.GoldenVersion {
	t.Helper()
	var masked, verified int
	gv, err := p.RefreshGolden(context.Background(), spec(&masked, &verified, `{"findings":0}`))
	require.NoError(t, err)
	require.Equal(t, 1, masked)
	require.Equal(t, 1, verified)
	require.True(t, gv.Verified)
	return gv
}

// The mechanism, in the order RDS makes it happen.
//
// Five tests rather than one, and that is the testing standard rather than a
// preference. `require` stops at the first failure, so five assertions in one
// function means the last four can be unreachable and still look alive: a
// mutation that breaks the fifth is caught by the first, and nothing ever
// proves the fifth was checking anything. One assertion per test is what makes
// each cell of the mutation table point at a different line.
//
// The shared work is one refresh, recorded once.
type refreshTrace struct {
	order   []string
	actions []string
	golden  provider.GoldenVersion
	items   []provider.Resource
	p       *rds.Provider
}

func traceRefresh(t *testing.T) refreshTrace {
	t.Helper()
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	server.Reset()

	var masked, verified int
	order := []string{}
	s := spec(&masked, &verified, `{"findings":0}`)
	inner := s.Mask
	s.Mask = func(ctx context.Context, v secret.Value) error {
		order = append(order, "mask")
		return inner(ctx, v)
	}
	innerVerify := s.Verify
	s.Verify = func(ctx context.Context, v secret.Value) (string, error) {
		order = append(order, "verify")
		return innerVerify(ctx, v)
	}
	gv, err := p.RefreshGolden(context.Background(), s)
	require.NoError(t, err)

	items, err := p.Inventory(context.Background())
	require.NoError(t, err)
	return refreshTrace{order: order, actions: server.Actions(), golden: gv, items: items, p: p}
}

// The rules are applied before anything attests to the result. Verification
// running first would attest to the unmasked data, which is worse than not
// verifying at all.
func TestRefreshMasksBeforeItVerifies(t *testing.T) {
	require.Equal(t, []string{"mask", "verify"}, traceRefresh(t).order)
}

// The source is snapshotted before the candidate is restored, because the
// candidate IS that snapshot restored. A provider that restored first would be
// restoring something else.
func TestRefreshSnapshotsTheSourceBeforeItRestoresTheCandidate(t *testing.T) {
	actions := traceRefresh(t).actions
	first := indexOf(actions, "CreateDBSnapshot")
	restore := indexOf(actions, "RestoreDBInstanceFromDBSnapshot")
	require.GreaterOrEqual(t, first, 0, "the source was never snapshotted: %v", actions)
	require.Greater(t, restore, first,
		"the candidate was restored before the source was snapshotted: %v", actions)
}

// The master password is rotated before the candidate is masked, and therefore
// before anything connects to it. Until that call the candidate carries
// PRODUCTION's credential, because that is what a restore inherits.
func TestRefreshRotatesTheCandidateBeforeAnythingConnects(t *testing.T) {
	actions := traceRefresh(t).actions
	restore := indexOf(actions, "RestoreDBInstanceFromDBSnapshot")
	modify := indexOf(actions, "ModifyDBInstance")
	require.GreaterOrEqual(t, modify, 0, "the password was never rotated: %v", actions)
	require.Greater(t, modify, restore,
		"the password was rotated before the instance existed: %v", actions)
}

// The golden snapshot is taken LAST, after the rotation and therefore after the
// masking. A snapshot taken any earlier is a golden of data nothing masked, and
// it would still read as verified.
func TestRefreshTakesTheGoldenSnapshotLast(t *testing.T) {
	actions := traceRefresh(t).actions
	modify := indexOf(actions, "ModifyDBInstance")
	first := indexOf(actions, "CreateDBSnapshot")
	last := lastIndexOf(actions, "CreateDBSnapshot")
	require.NotEqual(t, first, last,
		"only one CreateDBSnapshot ran, so either the source or the candidate was not "+
			"snapshotted and the mechanism is not what this provider claims: %v", actions)
	require.Greater(t, last, modify,
		"the golden snapshot was taken before the password was rotated, which means it was "+
			"taken before anything could have masked the candidate: %v", actions)
}

// A published golden is a snapshot and nothing else. The candidate instance and
// the intermediate snapshot both cost money and neither is referenced again, so
// a refresh that left either behind is a refresh that bills for scaffolding.
func TestAPublishedGoldenIsASnapshotAndNothingElse(t *testing.T) {
	trace := traceRefresh(t)
	require.Equal(t, []string{"snapshot/golden"}, kindsOf(trace.items),
		"a refresh left %v behind", trace.items)
	require.Equal(t, trace.golden.ProviderRef, trace.items[0].ID)
}

// A refresh whose verification fails publishes nothing at all, and leaves
// nothing behind either. The candidate is a full restore of production that
// failed its own scan.
func TestRefreshThatFailsVerificationPublishesAndLeavesNothing(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)

	s := provider.GoldenSpec{
		Version:   17,
		RulesHash: "deadbeef",
		Mask:      func(context.Context, secret.Value) error { return nil },
		Verify: func(context.Context, secret.Value) (string, error) {
			return "", errors.New("AF-MSK-002: an unmasked email survived")
		},
	}
	gv, err := p.RefreshGolden(context.Background(), s)
	require.Error(t, err)
	require.Empty(t, gv.ID)

	items, err := p.Inventory(context.Background())
	require.NoError(t, err)
	require.Empty(t, items,
		"a failed verification left %v behind, and a candidate is a copy of production", items)

	goldens, err := p.ListGoldens(context.Background())
	require.NoError(t, err)
	require.Empty(t, goldens)
}

// The credential a restore inherits is production's, and handing it to a
// preview environment is the failure the branch key exists to prevent. This is
// the assertion that the rotation actually took: the source's own password does
// not open the branch, and the derived one does.
func TestBranchRotatesAwayFromTheInheritedCredential(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	gv := refresh(t, p)

	b, err := p.Branch(context.Background(), gv.ID, "env_rotation")
	require.NoError(t, err)
	conn, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)

	// The derived credential opens it.
	db, err := sql.Open("pgx", conn.Reveal())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var n int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM conformance_users").Scan(&n))
	require.Equal(t, 3, n)

	// And the one the source uses does not appear in it.
	require.NotContains(t, conn.Reveal(), "postgres:test@",
		"the branch's connection string still carries the administering credential, so the "+
			"restore's inherited password was never rotated")

	// The rotation is applied immediately rather than in the maintenance
	// window, which is the difference between a password that works now and
	// one that works next Sunday.
	require.Equal(t, "true", server.LastRequest("ModifyDBInstance").Get("ApplyImmediately"))
}

// Two branches of one golden get two passwords, so a preview's credential
// opens that preview and nothing else.
func TestTwoBranchesGetTwoCredentials(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	gv := refresh(t, p)

	first, err := p.Branch(context.Background(), gv.ID, "env_one")
	require.NoError(t, err)
	second, err := p.Branch(context.Background(), gv.ID, "env_two")
	require.NoError(t, err)

	a, err := p.ConnString(context.Background(), first, provider.ConnDirect)
	require.NoError(t, err)
	b, err := p.ConnString(context.Background(), second, provider.ConnDirect)
	require.NoError(t, err)
	require.NotEqual(t, a.Reveal(), b.Reveal())
}

// Parameters that are invisible in every response and decide real behaviour. A
// branch reachable from the internet, or one taking its own nightly backups
// that outlive the environment, is exactly the leak this product exists to
// prevent, and neither shows up in a result. One assertion per test, because
// three in one function means two of them can be unreachable.
func restoreRequest(t *testing.T) url.Values {
	t.Helper()
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	gv := refresh(t, p)
	_, err := p.Branch(context.Background(), gv.ID, "env_private")
	require.NoError(t, err)
	return server.LastRequest("RestoreDBInstanceFromDBSnapshot")
}

func TestARestoredBranchIsNotReachableFromTheInternet(t *testing.T) {
	require.Equal(t, "false", restoreRequest(t).Get("PubliclyAccessible"))
}

func TestARestoredBranchTakesNoBackupsOfItsOwn(t *testing.T) {
	require.Equal(t, "0", restoreRequest(t).Get("BackupRetentionPeriod"),
		"a preview database taking nightly backups leaves snapshots that outlive the "+
			"environment, which is the leak this product exists to prevent")
}

func TestAnUnsetInstanceClassIsOmittedRatherThanSentEmpty(t *testing.T) {
	// The KEY rather than its value, and that difference is the whole test. A
	// form carrying DBInstanceClass= parses back to an empty string, which is
	// indistinguishable from the parameter being absent if you read the value,
	// so a test that read the value would pass against a provider sending the
	// broken form. RDS defaults an ABSENT class to the snapshot's own and
	// refuses a parameter with no value, so the two are not the same request.
	_, present := restoreRequest(t)["DBInstanceClass"]
	require.False(t, present,
		"the class was sent with no value; a provider that always sent the key would work "+
			"on every account that set the variable and fail on every account that did not")
}

// Teardown must not leave a final snapshot behind, and must not leave the
// automated backups either. Both would be copies of masked data that outlive
// the environment and bill for it.
func deleteRequest(t *testing.T) url.Values {
	t.Helper()
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	gv := refresh(t, p)
	b, err := p.Branch(context.Background(), gv.ID, "env_teardown")
	require.NoError(t, err)
	require.NoError(t, p.Destroy(context.Background(), b))
	return server.LastRequest("DeleteDBInstance")
}

func TestDestroySkipsTheFinalSnapshot(t *testing.T) {
	require.Equal(t, "true", deleteRequest(t).Get("SkipFinalSnapshot"))
}

func TestDestroyRemovesTheAutomatedBackupsToo(t *testing.T) {
	require.Equal(t, "true", deleteRequest(t).Get("DeleteAutomatedBackups"))
}

// An instance that is still busy refuses a delete, and giving up on the first
// refusal leaves the expensive half of an environment behind.
func TestDestroyRetriesWhileTheInstanceIsBusy(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	gv := refresh(t, p)
	b, err := p.Branch(context.Background(), gv.ID, "env_busy")
	require.NoError(t, err)

	server.RefuseNextDelete(b.ProviderRef)
	server.Reset()
	require.NoError(t, p.Destroy(context.Background(), b))
	require.GreaterOrEqual(t, server.Calls()["DeleteDBInstance"], 2,
		"the first delete was refused and this provider did not try again")

	items, err := p.Inventory(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"snapshot/golden"}, kindsOf(items))
}

// Something that merely matches our prefix is not ours. A customer whose own
// instance is called af-b-something must not lose it to our teardown.
func TestDestroyRefusesAnInstanceThatCarriesNoMarker(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	require.NoError(t, server.SeedSource("af-b-somebody-elses", ""))
	p := newProvider(t, server)

	err := p.Destroy(context.Background(), provider.Branch{ProviderRef: "af-b-somebody-elses"})
	require.ErrorIs(t, err, rds.ErrNotOurs)
}

// The inventory is what the leak detector compares the journal against, so it
// has to see past the first page. An account with more than a hundred
// resources would otherwise report everything after them as already gone.
func TestInventoryFollowsTheMarker(t *testing.T) {
	server := newFakeWith(t, fakerds.Options{
		AdminURL:    requirePostgres(t),
		Prefix:      "af_rds_" + randomSuffix(t) + "_",
		Region:      testRegion,
		Credentials: testCredentials,
		// One record a page, so three resources need three round trips and the
		// marker loop is entered rather than assumed.
		PageSize: 1,
	}, conformance.DefaultSeedSQL)
	p := newProvider(t, server)
	gv := refresh(t, p)
	for _, env := range []string{"env_page_a", "env_page_b"} {
		_, err := p.Branch(context.Background(), gv.ID, env)
		require.NoError(t, err)
	}

	items, err := p.Inventory(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"instance/branch", "instance/branch", "snapshot/golden"},
		kindsOf(items), "the inventory stopped at a page boundary: %v", items)
}

// Aurora restores from a snapshot exactly as happily, and doing so would turn
// a clone that is flat in the database's size into a copy that is not. Nobody
// measures a thing that still appears to work, so the refusal is at
// construction and it names the provider to use instead.
func TestNewRefusesAnAuroraSourceAndNamesTheOtherProvider(t *testing.T) {
	server := newFake(t, "", "")
	require.NoError(t, server.SeedSourceWithEngine("acme-aurora", "aurora-postgresql", "16.4", ""))

	opts := options(t, server)
	opts.SourceInstance = "acme-aurora"
	_, err := scopedNew(context.Background(), opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "aurora")
	require.Contains(t, err.Error(), "copies every byte")
}

func TestNewRefusesANonPostgresSource(t *testing.T) {
	server := newFake(t, "", "")
	require.NoError(t, server.SeedSourceWithEngine("acme-mysql", "mysql", "8.0", ""))

	opts := options(t, server)
	opts.SourceInstance = "acme-mysql"
	_, err := scopedNew(context.Background(), opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mysql")
}

func TestNewRefusesASourceThatDoesNotExist(t *testing.T) {
	server := newFake(t, "", "")
	opts := options(t, server)
	opts.SourceInstance = "no-such-instance"
	_, err := scopedNew(context.Background(), opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "DB INSTANCE")
}

func TestNewRefusesAMissingBranchKey(t *testing.T) {
	server := newFake(t, "", "")
	opts := options(t, server)
	opts.BranchKey = secret.Value{}
	_, err := scopedNew(context.Background(), opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), rds.DefaultVariable)
}

// The fake refuses a signature that does not match, which is what makes every
// other test in this file evidence about a signed request rather than about an
// HTTP call. A check that cannot say no is worse than no check, so this points
// it at the wrong secret and requires a refusal.
func TestTheFakeRefusesAWrongSignature(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	opts := options(t, server)
	opts.Credentials = &cloudauth.AWSCredentials{
		AccessKeyID:     testCredentials.AccessKeyID,
		SecretAccessKey: "a-different-secret-entirely",
		Source:          "the test",
	}
	_, err := scopedNew(context.Background(), opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "SignatureDoesNotMatch")
}

// Caps.Subsetting is false, so the engine should never set Load. A provider
// that accepted it and restored the whole source anyway would have made a
// manifest key that reads as configuration and behaves as decoration.
func TestASubsetIsRefusedRatherThanQuietlyIgnored(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)

	loaded := 0
	s := provider.GoldenSpec{
		Version:   17,
		RulesHash: "abcd1234",
		Load: func(context.Context, secret.Value, secret.Value) error {
			loaded++
			return nil
		},
		Mask:   func(context.Context, secret.Value) error { return nil },
		Verify: func(context.Context, secret.Value) (string, error) { return `{}`, nil },
	}
	_, err := p.RefreshGolden(context.Background(), s)
	require.Error(t, err)
	require.Contains(t, err.Error(), "subset")
	require.Zero(t, loaded)
	require.False(t, p.Capabilities().Subsetting)
}

// Reset is unsupported by declaration and by behaviour, and both have to say
// the same thing: a capability declared false whose method quietly worked would
// make the suite skip a behaviour that could have run.
func TestResetIsUnsupportedInBothTheDeclarationAndTheMethod(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	require.False(t, p.Capabilities().Reset)
	require.ErrorIs(t, p.Reset(context.Background(), provider.Branch{}), provider.ErrUnsupported)
}

func TestPooledConnectionsAreUnsupportedInBothTheDeclarationAndTheMethod(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	require.False(t, p.Capabilities().PooledEndpoints)
	_, err := p.ConnString(context.Background(), provider.Branch{ProviderRef: "af-b-x"},
		provider.ConnPooled)
	require.ErrorIs(t, err, provider.ErrUnsupported)
}

// The capability this whole wave is sold on, declared in the direction that is
// true of a snapshot restore. The conformance suite is what falsifies it; this
// is the assertion that the declaration has not been flipped by an edit.
func TestCopyOnWriteIsDeclaredFalse(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	caps := p.Capabilities()
	require.False(t, caps.CopyOnWrite,
		"a snapshot restore hydrates a new volume with every byte, so branch time grows "+
			"with the data and this field is what the comparison table publishes")
	require.Greater(t, caps.ExpectedBranchLatency, time.Duration(0),
		"a declared latency of zero turns Branch_IsWithinTheDeclaredLatency into an "+
			"assertion nothing can satisfy")
	require.True(t, caps.Branching)
}

// A golden nothing scanned cannot be branched, and this is the product's
// central promise. A refresh with no scanner is refused before it creates
// anything, so the only way a golden without an attestation can exist is for
// its tags to change after publication, and that is the case made here: the
// published attestation is emptied, and the branch side refusal has to hold
// on its own rather than rely on the refresh having refused.
func TestBranchingAGoldenWithNoAttestationIsRefusedWithTheMaskingCode(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	gv := refresh(t, p)

	// The attestation is chunked as antifailure:attestation.1, .2 and so on,
	// and a small one fits in the first chunk, so emptying that one empties
	// the whole attestation.
	server.SetTags(gv.ProviderRef, map[string]string{"antifailure:attestation.1": ""})

	_, err := p.Branch(context.Background(), gv.ID, "env_unverified")
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-MSK-001")
}

func TestBranchingAMissingGoldenNamesWhatTheAccountDoesHold(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	gv := refresh(t, p)

	_, err := p.Branch(context.Background(), "gv_19700101000000_deadbeef", "env_missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-DB-004")
	require.Contains(t, err.Error(), gv.ID,
		"the refusal has to say what the account does hold, or the next step is a guess")
}

// An interrupted refresh must still remove its candidate. A cancelled context
// is what a control C looks like, and a cleanup that honoured the cancellation
// would leave a full restore of production behind on every interrupted run.
func TestAnInterruptedRefreshStillRemovesItsCandidate(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)

	ctx, cancel := context.WithCancel(context.Background())
	masked := false
	_, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version:   17,
		RulesHash: "abcd1234",
		Mask: func(context.Context, secret.Value) error {
			masked = true
			cancel()
			return context.Canceled
		},
		// A scanner, because a refresh without one is refused before it
		// creates anything, and this test would then pass on an empty account
		// having interrupted nothing.
		Verify: func(context.Context, secret.Value) (string, error) { return `{"findings":0}`, nil },
	})
	require.Error(t, err)
	require.True(t, masked, "the refresh never reached the masking step, so nothing was interrupted")

	items, err := p.Inventory(context.Background())
	require.NoError(t, err)
	require.Empty(t, items,
		"an interrupted refresh left %v behind; the cleanup must run on a context that "+
			"outlives the cancellation, or a control C leaks a copy of production", items)
}

// And when the process died before its cleanup could run at all, the next
// refresh sweeps what it left. Without it a killed run leaves a candidate
// instance and an intermediate snapshot billing in the account for ever.
//
// The orphan is manufactured rather than waited for: the control plane is told
// to accept deletes and do nothing, which is what a process dying mid cleanup
// leaves, and the orphan's created tag is then moved back past the six hour
// cutoff so that the sweep can reach it without the test waiting six hours.
func TestTheNextRefreshSweepsAnOrphanedCandidateAndTransitSnapshot(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)

	server.SetFault(fakerds.FaultDeleteDoesNotDelete)
	_, err := p.RefreshGolden(context.Background(), provider.GoldenSpec{
		Version:   17,
		RulesHash: "abcd1234",
		Mask:      func(context.Context, secret.Value) error { return nil },
		Verify: func(context.Context, secret.Value) (string, error) {
			return "", errors.New("AF-MSK-002: an unmasked email survived")
		},
	})
	require.Error(t, err)
	server.SetFault("")

	instances, snapshots := server.Identifiers()
	orphans := append(append([]string{}, instances...), snapshots...)
	var candidate, transit string
	for _, id := range orphans {
		tags, ok := server.TagsOf(id)
		require.True(t, ok)
		switch tags["antifailure:kind"] {
		case "candidate":
			candidate = id
		case "transit":
			transit = id
		}
		if tags["antifailure:kind"] == "" {
			continue
		}
		// Six hours and one minute ago, which is past the cutoff by the
		// smallest margin that proves the comparison rather than the constant.
		tags["antifailure:created"] = time.Now().UTC().Add(-6*time.Hour - time.Minute).
			Format(time.RFC3339Nano)
		require.True(t, server.Retag(id, tags))
	}
	require.NotEmpty(t, candidate, "the failed refresh left no candidate, so there is nothing to sweep")
	require.NotEmpty(t, transit, "the failed refresh left no intermediate snapshot")

	refresh(t, p)

	items, err := p.Inventory(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"snapshot/golden"}, kindsOf(items),
		"the sweep left %v behind, and every one of those is a copy of production that "+
			"bills until somebody finds it in a console", items)
}

// Health is asked by teardown, so a branch that has just been removed has to
// answer rather than error.
func TestHealthReportsARemovedBranchAsUnreachable(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	gv := refresh(t, p)
	b, err := p.Branch(context.Background(), gv.ID, "env_health")
	require.NoError(t, err)

	live, err := p.Health(context.Background(), b)
	require.NoError(t, err)
	require.True(t, live.Reachable)

	require.NoError(t, p.Destroy(context.Background(), b))
	gone, err := p.Health(context.Background(), b)
	require.NoError(t, err, "teardown asks for health, and an error makes a successful "+
		"teardown look like a failure")
	require.False(t, gone.Reachable)
}

// A golden a branch came from cannot be destroyed. Removing it would break
// nothing at all, which is exactly why the refusal has to be here: an
// environment would go on running and the version it says it came from would
// have stopped existing.
func TestDestroyingAReferencedGoldenIsRefused(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	gv := refresh(t, p)
	b, err := p.Branch(context.Background(), gv.ID, "env_referenced")
	require.NoError(t, err)

	err = p.DestroyGolden(context.Background(), gv.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-DB-005")
	require.Contains(t, err.Error(), "env_referenced")

	require.NoError(t, p.Destroy(context.Background(), b))
	require.NoError(t, p.DestroyGolden(context.Background(), gv.ID))
	require.NoError(t, p.DestroyGolden(context.Background(), gv.ID),
		"destroying a golden that is already gone must succeed, because the engine retries")
}

// A destroyed golden is gone, and this test exists because the SHARED suite
// cannot check it here.
//
// conformance.RunDatabase's leak detector records the golden VERSION
// identifier, gv_<stamp>_<hash>, and looks for it as a SUBSTRING of what
// Inventory reports. A Postgres database can be called af_g_gv_<stamp>_<hash>
// and internal/db/pgurl's is, so its goldens are covered by that match. An RDS
// snapshot identifier may not contain an underscore at all, so this provider
// names its goldens from a digest and no substring of the version appears in
// them. The consequence is precise and worth writing down rather than
// implying: a DestroyGolden here that quietly did nothing would leave the
// shared leak check green. So the check is made locally, against the
// inventory, which is the same question asked where it can be answered.
func TestDestroyGoldenRemovesTheSnapshot(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	gv := refresh(t, p)

	require.NoError(t, p.DestroyGolden(context.Background(), gv.ID))
	items, err := p.Inventory(context.Background())
	require.NoError(t, err)
	require.Empty(t, items,
		"the golden was destroyed and %v is still there. The shared suite's leak detector "+
			"cannot see this, because an RDS snapshot identifier cannot carry the version "+
			"string it matches on", items)
}

// Every request carries the API version the query protocol needs. The fake
// refuses another, which is what makes this a check rather than a comment.
func TestEveryRequestCarriesTheQueryAPIVersion(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	refresh(t, p)
	for action := range server.Calls() {
		require.Equal(t, "2014-10-31", server.LastRequest(action).Get("Version"), action)
	}
}

func indexOf(actions []string, want string) int {
	for i, a := range actions {
		if a == want {
			return i
		}
	}
	return -1
}

func lastIndexOf(actions []string, want string) int {
	for i := len(actions) - 1; i >= 0; i-- {
		if actions[i] == want {
			return i
		}
	}
	return -1
}

func kindsOf(items []provider.Resource) []string {
	out := make([]string, 0, len(items))
	for _, r := range items {
		out = append(out, r.Kind)
	}
	return out
}
