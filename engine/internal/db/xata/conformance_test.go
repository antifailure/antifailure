package xata

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// TestConformance runs the shared suite against the REAL Xata.
//
// Against the real service, like the Neon and Supabase suites beside it, and
// the reasoning in engine/internal/db/neon/conformance_test.go is right: a fake
// proves the provider agrees with our idea of the service, and what matters is
// that it agrees with the service. For this provider that reasoning is sharper
// than usual, because the one capability worth choosing Xata for is copy on
// write, and copy on write is a claim about seconds that only a real branch of
// a real database can settle.
//
// It skips BY NAME without credentials, and the skip says what was not run
// rather than passing quietly. Nobody on this lane had a Xata account: setting
// the three variables below is what turns this from a skip into a verdict, and
// until somebody does, the copy on write declaration in Capabilities is a
// truthful reading of Xata's published documentation that no measurement in
// this repository has confirmed.
//
//	AF_XATA_API_KEY      an API key with branch:read and branch:write
//	AF_XATA_ORG          the organization identifier
//	AF_XATA_PROJECT      the project identifier
//
// What it costs the account: one branch per golden and one per environment,
// each a copy on write branch that shares storage with its parent, all removed
// by the suite's own cleanup and checked by its leak assertion at the end.
func TestConformance(t *testing.T) {
	key := os.Getenv("AF_XATA_API_KEY")
	org := os.Getenv("AF_XATA_ORG")
	project := os.Getenv("AF_XATA_PROJECT")
	if key == "" || org == "" || project == "" {
		t.Skip("skipped: AF_XATA_API_KEY, AF_XATA_ORG and AF_XATA_PROJECT are not all " +
			"set, so the suite ran against nothing. This is the only place the copy on " +
			"write declaration in Capabilities can be checked, and it was not checked.")
	}

	limit := 4
	if raw := os.Getenv("AF_XATA_MAX_BRANCHES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err, "AF_XATA_MAX_BRANCHES is not a number")
		limit = parsed
	}

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := New(Options{
			APIKey:       secrets.NewFrom(key, "AF_XATA_API_KEY"),
			OrgID:        org,
			ProjectID:    project,
			ParentBranch: os.Getenv("AF_XATA_PARENT_BRANCH"),
			SeedSQL:      conformance.DefaultSeedSQL,
			MaxBranches:  limit,
			Clock:        clock.New(),
		})
		require.NoError(t, err)
		return p
	}, conformance.Options{
		// Generous, because every behaviour crosses the public internet to a
		// branch that may be waking from scale to zero. Bounded, because a
		// hung call must fail the behaviour rather than the job.
		Timeout:  6 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	})
}

// TestSweepLeftovers removes anything a killed run left behind.
//
// Separate from the suite on purpose, the way the Neon and pgurl ones are: a
// failing behaviour legitimately leaves things behind for inspection, and a
// sweep that ran automatically would destroy the evidence.
func TestSweepLeftovers(t *testing.T) {
	if os.Getenv("AF_XATA_SWEEP") == "" {
		t.Skip("skipped: set AF_XATA_SWEEP=1 to remove branches left by a killed run")
	}
	key := os.Getenv("AF_XATA_API_KEY")
	org := os.Getenv("AF_XATA_ORG")
	project := os.Getenv("AF_XATA_PROJECT")
	require.NotEmpty(t, key, "AF_XATA_API_KEY is required to sweep")
	require.NotEmpty(t, org, "AF_XATA_ORG is required to sweep")
	require.NotEmpty(t, project, "AF_XATA_PROJECT is required to sweep")

	p, err := New(Options{
		APIKey: secrets.NewFrom(key, "AF_XATA_API_KEY"),
		OrgID:  org, ProjectID: project, Clock: clock.New(),
	})
	require.NoError(t, err)
	defer func() { _ = p.Close() }()

	ctx := t.Context()
	items, err := p.Inventory(ctx)
	require.NoError(t, err)
	// Branches before goldens, because a golden with a live branch is refused
	// and a sweep that hit them in listing order would fail on the first one.
	for _, pass := range []string{"branch", "candidate", "golden"} {
		for _, r := range items {
			if r.Kind != pass {
				continue
			}
			t.Logf("removing %s (%s)", r.ID, r.Kind)
			if r.Kind == "golden" {
				require.NoError(t, p.DestroyGolden(ctx, r.Labels["version"]))
				continue
			}
			require.NoError(t, p.Destroy(ctx, provider.Branch{ProviderRef: r.ID}))
		}
	}
}
