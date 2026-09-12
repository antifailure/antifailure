package xata

import (
	"context"
	"database/sql"
	"errors"
	"os"
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

// requirePostgres returns the local Postgres the fake control plane keeps its
// branches on, or skips.
//
// A skip is right on a laptop with no Postgres and wrong in CI, where a job
// that skipped every one of these would go green having proved nothing. That is
// what AF_REQUIRE_DATABASE turns into a failure.
func requirePostgres(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("AF_XATA_TEST_DATABASE_URL")
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
			t.Fatalf("AF_REQUIRE_DATABASE is set and the local Postgres did not answer: %v", err)
		}
		t.Skipf("skipped: no Postgres answered: %v", err)
	}
	return raw
}

// providerOver builds a provider pointed at a fake control plane.
//
// BaseURL is the ONLY override. Everything else is what a real run uses, which
// is what makes the request shapes this exercises the real ones.
func providerOver(t *testing.T, f *fakeXata) *Provider {
	t.Helper()
	p, err := New(Options{
		APIKey:       secrets.New("xau_test_key"),
		OrgID:        f.org,
		ProjectID:    f.project,
		BaseURL:      f.server.URL,
		Clock:        clock.New(),
		SeedSQL:      conformance.DefaultSeedSQL,
		PollInterval: 10 * time.Millisecond,
		PollTimeout:  10 * time.Second,
	})
	require.NoError(t, err)
	return p
}

// ---------------------------------------------------------------------------
// New refuses what it can decide without a round trip
// ---------------------------------------------------------------------------

func TestNewRefusesAKeylessOrHalfAddressedProject(t *testing.T) {
	_, err := New(Options{OrgID: "o", ProjectID: "p"})
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFSEC001)),
		"a provider with no API key is refused at construction, naming the variable, "+
			"rather than at the first call. Got: %v", err)

	_, err = New(Options{APIKey: secrets.New("k"), ProjectID: "p"})
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFMAN002)),
		"Xata addresses a project by organization AND project, and neither can be "+
			"discovered from the other, so a provider holding one of them is refused "+
			"rather than left to send every request to a path nobody wrote. Got: %v", err)

	_, err = New(Options{APIKey: secrets.New("k"), OrgID: "o"})
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFMAN002)),
		"and the same when the project is the missing half. Got: %v", err)
}

// ---------------------------------------------------------------------------
// The request shapes, which is the half a fake can prove
// ---------------------------------------------------------------------------

func TestTheRequestShapesAreTheOnesXataDocuments(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)
	ctx := context.Background()

	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "shapes01",
		Verify: func(context.Context, secrets.Value) (string, error) { return "attested", nil },
	})
	require.NoError(t, err)

	b, err := p.Branch(ctx, gv.ID, "env_shapes")
	require.NoError(t, err)

	paths := strings.Join(f.pathsSeen(), "\n")
	base := "/organizations/" + f.org + "/projects/" + f.project + "/branches"

	require.Contains(t, paths, "POST "+base,
		"a branch is created by posting to the branches collection")
	require.Contains(t, paths, "GET "+base,
		"and the project's branches are read from the same collection")
	require.Contains(t, paths, "GET "+base+"/"+b.ProviderRef+"/credentials",
		"and a connection string comes from the branch's credentials endpoint, which is "+
			"the one path in this provider that returns a secret")
	require.Contains(t, paths, "PATCH "+base+"/",
		"and publishing a golden is a rename, which is a PATCH")

	// The bearer token, which is the one thing a fake could accept without
	// checking and thereby certify a provider that never authenticated.
	require.Equal(t, "Bearer xau_test_key", f.token,
		"every call carries the API key as a bearer token")

	// The create body, field by field, because this is the request the vendor
	// either accepts or rejects and the only thing standing behind it is that
	// somebody read the reference.
	var create map[string]any
	for _, body := range f.bodies {
		if _, ok := body["mode"]; ok {
			create = body
			break
		}
	}
	require.NotNil(t, create, "no create body was recorded")
	require.Equal(t, "inherit", create["mode"],
		"a golden and a branch are both copy on write branches of something, which is "+
			"mode inherit; custom would mean an empty branch and a cluster configuration "+
			"this provider has no verified shape for")
	require.NotEmpty(t, create["parentID"], "mode inherit requires the parent")
	require.LessOrEqual(t, len(create["description"].(string)), 50,
		"the description is Xata's only free text field and it is capped at fifty "+
			"characters, which is why the version identifier fits and nothing else does")
}

// ---------------------------------------------------------------------------
// The error mapping, which a happy path cannot show
// ---------------------------------------------------------------------------

func TestALimitRefusalBecomesTheCodedCeiling(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)
	ctx := context.Background()

	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{Version: 17, RulesHash: "limit001"})
	require.NoError(t, err)

	f.failOnce("POST", "/branches", 412, "project branch limit reached")
	_, err = p.Branch(ctx, gv.ID, "env_limit")
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB006)),
		"Xata's own ceiling is the real one, and it has to arrive as the same coded "+
			"error the configured limit produces so an operator gets one answer rather "+
			"than a raw HTTP 412. Got: %v", err)
}

func TestDestroyingABranchThatIsAlreadyGoneSucceeds(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)
	ctx := context.Background()

	f.failOnce("DELETE", "/branches", 404, "no such branch")
	require.NoError(t, p.Destroy(ctx, provider.Branch{ProviderRef: "br_gone"}),
		"every destroy in this interface must be idempotent, because the engine retries "+
			"after timeouts and a teardown that failed on an absent resource would report "+
			"a successful teardown as a failure")
}

func TestAGoldenWithLiveBranchesIsRefusedByCodeRatherThanByTheVendor(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)
	ctx := context.Background()

	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{Version: 17, RulesHash: "refs0001"})
	require.NoError(t, err)
	_, err = p.Branch(ctx, gv.ID, "env_refs")
	require.NoError(t, err)

	err = p.DestroyGolden(ctx, gv.ID)
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB005)),
		"a golden something came from is refused here, naming the count, rather than "+
			"passed to the vendor to refuse with a message about children. Got: %v", err)
}

func TestBranchingAnUnpublishedCandidateSaysUnverifiedRatherThanMissing(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)
	ctx := context.Background()

	// A refresh whose verification fails leaves no candidate at all, which is
	// the point of the deferred delete, so the candidate is made directly.
	// What is under test is the reading, not how one gets there.
	root, err := p.parentBranch(ctx)
	require.NoError(t, err)
	version := provider.NewGoldenVersionID(time.Now(), "cand0001")
	_, err = p.client.CreateBranch(ctx, CreateBranchRequest{
		Name: PrefixCandidate + branchSafe(version), ParentID: root.ID, Description: version,
	})
	require.NoError(t, err)

	_, err = p.Branch(ctx, version, "env_unverified")
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFMSK001)),
		"a version that exists as a candidate was never verified, and saying unverified "+
			"rather than missing tells the operator which of the two problems they have. "+
			"Got: %v", err)
}

// ---------------------------------------------------------------------------
// The behaviour, over the real Postgres behind the fake
// ---------------------------------------------------------------------------

func TestARefreshPublishesOnlyAfterVerificationAndABranchReadsTheData(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)
	ctx := context.Background()

	masked := false
	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "publish1", Provenance: "proj/af",
		Mask: func(ctx context.Context, conn secrets.Value) error {
			masked = true
			db, err := sql.Open("pgx", conn.Reveal())
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			_, err = db.ExecContext(ctx, `CREATE TABLE af_probe (v text)`)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, `INSERT INTO af_probe VALUES ('from the golden')`)
			return err
		},
		Verify: func(context.Context, secrets.Value) (string, error) { return "attestation-here", nil },
	})
	require.NoError(t, err)
	require.True(t, masked, "the provider must call the engine's masking rather than its own")
	require.True(t, gv.Verified)
	require.Equal(t, "attestation-here", gv.Attestation)

	// Published means renamed, and nothing else. A listing is what a caller
	// sees, so that is what is checked.
	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	require.Len(t, goldens, 1)
	require.Equal(t, gv.ID, goldens[0].ID)
	require.Equal(t, "publish1", goldens[0].RulesHash,
		"the rules hash comes from the golden's own metadata table, because Xata's "+
			"branch object has nowhere to keep it and a listing that invented one would "+
			"be a claim about a database made without reading it")
	require.Equal(t, "proj/af", goldens[0].Provenance)

	b, err := p.Branch(ctx, gv.ID, "env_reads")
	require.NoError(t, err)
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)

	db, err := sql.Open("pgx", conn.Reveal())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var got string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT v FROM af_probe`).Scan(&got))
	require.Equal(t, "from the golden", got,
		"a branch that cannot read a row the golden holds is an empty database wearing "+
			"the word branch")
}

func TestAFailedVerificationLeavesNothingBranchable(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)
	ctx := context.Background()

	_, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "failed01",
		Verify: func(context.Context, secrets.Value) (string, error) {
			return "", errors.New("the scanner found an unmasked column")
		},
	})
	require.Error(t, err)

	// The worst possible outcome this function has is a branchable copy of
	// unmasked production left behind, so what is checked is the inventory
	// rather than the error.
	items, err := p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range items {
		require.NotEqual(t, "candidate", r.Kind,
			"a refresh whose verification failed left %s behind, and anything that can "+
				"be branched from is a copy of production nothing scanned", r.ID)
		require.NotEqual(t, "golden", r.Kind,
			"a refresh whose verification failed published %s", r.ID)
	}
}

func TestBranchIsIdempotentByEnvironment(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)
	ctx := context.Background()

	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{Version: 17, RulesHash: "idem0001"})
	require.NoError(t, err)

	first, err := p.Branch(ctx, gv.ID, "env_idem")
	require.NoError(t, err)
	second, err := p.Branch(ctx, gv.ID, "env_idem")
	require.NoError(t, err)
	require.Equal(t, first.ProviderRef, second.ProviderRef,
		"the engine retries after a timeout, and a retry that creates a second branch "+
			"is how an orphan is made")
}

func TestResetIsRefusedRatherThanFaked(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)

	require.False(t, p.Capabilities().Reset,
		"Xata publishes no endpoint that returns a branch to another branch's state")
	require.ErrorIs(t, p.Reset(context.Background(), provider.Branch{ProviderRef: "br_x"}),
		provider.ErrUnsupported,
		"a reset implemented as a delete and a recreate would hand back a different "+
			"branch on a different connection string while the caller still holds the old "+
			"one, so the capability is declared false and the method says so")
}

func TestCopyOnWriteIsDeclaredAndTheSuiteCannotYetCheckItHere(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)

	require.True(t, p.Capabilities().CopyOnWrite,
		"Xata's own reference says a child branch copies the parent's schema and data "+
			"using a copy on write storage snapshot and completes in seconds even for "+
			"terabyte scale databases. Declaring false to satisfy a harness that cannot "+
			"exhibit copy on write would be publishing a claim this repository knows is "+
			"wrong.")
	require.Positive(t, p.Capabilities().ExpectedBranchLatency,
		"and a declared latency, which is what stops a provider that has got slower "+
			"degrading quietly")
	require.False(t, p.Capabilities().Subsetting,
		"a candidate here holds the whole database the moment it exists, so subsetting "+
			"could only mean deleting down, which copies everything first")
	require.False(t, p.Capabilities().PooledEndpoints,
		"Xata returns one connection string and documents no pooled variant, so "+
			"declaring pooled endpoints would make a capability out of a synonym")
}

// TestConformanceAgainstTheFake runs the whole suite against the fake, and it
// is expected to FAIL on one behaviour.
//
// Behind an environment variable, because a package test that is known to go
// red cannot be the thing CI runs. Not deleted, because the refusal is the
// evidence: copy on write is the distinguishing commercial claim of this whole
// wave, this provider declares it truthfully, and the harness prescribed for
// the wave cannot exhibit it. Somebody should be able to SEE that rather than
// read an assertion about it in a comment.
//
//	AF_XATA_FAKE_SUITE=1 go test ./internal/db/xata -run TestConformanceAgainstTheFake -v
//
// What it prints: twenty five behaviours pass or skip by capability, and
// CopyOnWrite_BranchTimeMatchesTheDeclaration fails, saying that branching the
// larger golden took longer than the noise allowance. That is correct. The fake
// branches with CREATE DATABASE ... TEMPLATE, which copies the files, so the
// measurement is of a copy and the provider's declaration is about Xata.
//
// The lane building a third verdict for engine/conformance/cow.go is what
// closes this: a behaviour that can answer UNPROVEN, reachable only from a
// harness declaring it cannot exhibit copy on write, never from a provider
// asking to be excused.
func TestConformanceAgainstTheFake(t *testing.T) {
	if os.Getenv("AF_XATA_FAKE_SUITE") == "" {
		t.Skip("skipped: set AF_XATA_FAKE_SUITE=1 to run the whole suite against the " +
			"fake control plane. One behaviour is expected to fail, and the doc comment " +
			"on this test says which and why.")
	}
	admin := requirePostgres(t)
	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		f := newFakeXata(t, admin)
		return providerOver(t, f)
	}, conformance.Options{
		Timeout:  4 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	})
}
