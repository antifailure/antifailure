package dblab_test

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/db/dblab"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver
)

// The two variables that point the suite at a real Database Lab Engine.
//
// There is no default token on purpose. An engine with no verification token
// is one anybody on the network can create clones of production data on, and a
// suite that silently connected to one would be teaching people to run it that
// way.
const (
	envURL   = "AF_DBLAB_URL"
	envToken = "AF_DBLAB_TOKEN"
)

// TestConformance runs the shared suite against a real Database Lab Engine.
//
// Against the real thing rather than a fake, for the reason the Neon lane
// learned the hard way: a fake tests that the provider agrees with our idea of
// the engine, and what matters is that it agrees with the engine. The engine
// is self hosted, so unlike a cloud provider anybody can stand one up and run
// this; docs/providers/dblab.md says exactly how, and the whole run costs
// nothing but disk.
func TestConformance(t *testing.T) {
	url, token := requireEngine(t)

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := dblab.New(dblab.Options{
			Endpoint: url,
			Token:    token,
			Clock:    clock.New(),
			SeedSQL:  conformance.DefaultSeedSQL,
		})
		require.NoError(t, err)
		return p
	}, conformance.Options{
		// Generous because a behaviour here is up to three clone creations,
		// each a ZFS clone plus a container start plus a Postgres recovery,
		// and the later ones are slower because the earlier ones are still
		// running. Six minutes was not enough and the way it failed was
		// instructive: the client gave up on a clone the engine went on to
		// finish, so the behaviour failed AND leaked the clone it had asked
		// for. Tight enough that a genuinely stuck clone still fails the test
		// rather than the job.
		Timeout:  15 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	})
}

// requireEngine skips unless a real engine is configured and answering.
//
// Skipped rather than failed, because the engine is something a developer
// stands up deliberately and most runs of the test suite will not have one.
// The skip line says what is missing, so that a run which was meant to include
// this does not look like a run that passed.
func requireEngine(t *testing.T) (string, secrets.Value) {
	t.Helper()

	url := strings.TrimSpace(os.Getenv(envURL))
	token := strings.TrimSpace(os.Getenv(envToken))
	if url == "" || token == "" {
		t.Skipf("skipped: set %s and %s to run against a Database Lab Engine; "+
			"docs/providers/dblab describes how to start one", envURL, envToken)
	}

	p, err := dblab.New(dblab.Options{Endpoint: url, Token: secrets.New(token), Clock: clock.New()})
	if err != nil {
		t.Skipf("skipped: %v", err)
	}
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := p.Inventory(ctx); err != nil {
		t.Skipf("skipped: the Database Lab Engine at %s did not answer: %v", url, err)
	}
	return url, secrets.New(token)
}

// TestTheEngineHasABaseSnapshot fails rather than skips when the engine is
// reachable and holds nothing to build a golden from.
//
// It is separate from the suite because it is the one precondition the suite
// cannot report clearly: every behaviour would fail at its refresh with the
// same error, and twenty three identical failures bury the one sentence that
// says what to do about it.
func TestTheEngineHasABaseSnapshot(t *testing.T) {
	url, token := requireEngine(t)

	p, err := dblab.New(dblab.Options{Endpoint: url, Token: token, Clock: clock.New()})
	require.NoError(t, err)
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err = p.RefreshGolden(ctx, provider.GoldenSpec{Version: 11})
	require.Error(t, err, "the provider accepted a Postgres version it does not support")

	inv, err := p.Inventory(ctx)
	require.NoError(t, err)
	t.Logf("the engine holds %d resources this provider owns", len(inv))
}

// TestSweepLeftovers removes everything this provider owns on the configured
// engine.
//
// A conformance run that is killed halfway leaves clones and goldens behind,
// and on a Database Lab Engine those are not free: a clone holds a container
// and a port out of a hundred, and a golden holds the difference between its
// snapshot and the base. The suite's own cleanup handles the ordinary paths;
// this is for the ones where the process died.
//
// Gated on its own variable because it deletes. A test that removes resources
// must not be reachable by running the package.
func TestSweepLeftovers(t *testing.T) {
	if os.Getenv("AF_DBLAB_SWEEP") == "" {
		t.Skip("skipped: set AF_DBLAB_SWEEP=1 to remove everything this provider owns")
	}
	url, token := requireEngine(t)

	p, err := dblab.New(dblab.Options{Endpoint: url, Token: token, Clock: clock.New()})
	require.NoError(t, err)
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Clones first. A golden cannot be collected while something came from it,
	// and the storage of a deleted clone takes a moment to be released, which
	// is exactly what DestroyGolden waits out.
	inv, err := p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range inv {
		if !strings.HasPrefix(r.Kind, "clone/") {
			continue
		}
		t.Logf("removing %s %s", r.Kind, r.ID)
		require.NoError(t, p.Destroy(ctx, provider.Branch{ProviderRef: r.ID}))
	}

	goldens, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	for _, g := range goldens {
		t.Logf("removing golden %s (%s)", g.ID, g.ProviderRef)
		require.NoError(t, p.DestroyGolden(ctx, g.ID))
	}

	after, err := p.Inventory(ctx)
	require.NoError(t, err)
	require.Empty(t, after, "the sweep left resources behind")
}

// TestTheAttestationTravelsWithTheGolden proves the claim the provider page
// makes: that anybody holding an environment can read what was scanned and
// what was found, by querying the branch they already have.
//
// The conformance suite checks that a refresh returns an attestation, which is
// a different claim: that value is in this process's memory and says nothing
// about what a branch carries. Writing it into the golden's own data is what
// makes it travel, and the only way to know it travelled is to read it back
// through a branch's connection string.
func TestTheAttestationTravelsWithTheGolden(t *testing.T) {
	url, token := requireEngine(t)

	p, err := dblab.New(dblab.Options{
		Endpoint: url, Token: token, Clock: clock.New(),
		SeedSQL: conformance.DefaultSeedSQL,
	})
	require.NoError(t, err)
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	const attestation = `{"scanner":"attestation-travels","findings":0}`
	masked := false
	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version:   17,
		RulesHash: "travels",
		Mask:      func(context.Context, secrets.Value) error { masked = true; return nil },
		Verify: func(context.Context, secrets.Value) (string, error) {
			require.True(t, masked, "verification ran before masking, so it would attest to unmasked data")
			return attestation, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_ = p.DestroyGolden(clean, gv.ID)
	})
	require.Equal(t, attestation, gv.Attestation)

	b, err := p.Branch(ctx, gv.ID, "env_attestation0001")
	require.NoError(t, err)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_ = p.Destroy(clean, b)
	})

	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	db, err := sql.Open("pgx", conn.Reveal())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	var gotVersion, gotRules, gotAttestation string
	err = db.QueryRowContext(ctx,
		`SELECT version, rules_hash, attestation FROM `+dblab.MetaSchema+`.golden WHERE version = $1`,
		gv.ID).Scan(&gotVersion, &gotRules, &gotAttestation)
	require.NoError(t, err, "the branch carries no record of what was verified")

	require.Equal(t, gv.ID, gotVersion)
	require.Equal(t, "travels", gotRules)
	require.Equal(t, attestation, gotAttestation)
}
