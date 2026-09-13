package xata

import (
	"context"
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

// TestConformance runs the shared suite against a fake Xata control plane over
// a real local Postgres.
//
// Every behaviour runs, on every run, with nothing to set. What it proves is the
// provider's logic, its request shapes against the API document and its error
// mapping, over databases a real Postgres actually holds. What it does not
// prove is that Xata accepts those requests, or any claim about Xata's storage:
// the options below assert no real service, so the copy on write behaviour
// answers unproven rather than timing the copy this fake makes.
// TestTheFakeControlPlaneReallyCopies is why that is the right answer.
func TestConformance(t *testing.T) {
	admin := requirePostgres(t)
	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		return providerOver(t, newFakeXata(t, admin))
	}, conformanceOptions())
}

func conformanceOptions() conformance.Options {
	return conformance.Options{
		Timeout:  4 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	}
}

// TestConformanceAgainstXata runs the same suite against the REAL Xata, and it
// is the run that can settle the copy on write declaration.
//
// It asserts the real service, because it drives one: the control plane is
// Xata's and so is the storage under the branches. It skips BY NAME without
// credentials, and the skip says what was not run rather than passing quietly.
//
//	AF_XATA_API_KEY      an API key with branch:read, branch:write and credentials:read
//	AF_XATA_ORG          the organization identifier
//	AF_XATA_PROJECT      the project identifier
//
// What it costs the account: one branch per golden and one per environment,
// each a copy on write branch that shares storage with its parent, all removed
// by the suite's own cleanup and checked by its leak assertion at the end.
func TestConformanceAgainstXata(t *testing.T) {
	key := os.Getenv("AF_XATA_API_KEY")
	org := os.Getenv("AF_XATA_ORG")
	project := os.Getenv("AF_XATA_PROJECT")
	if key == "" || org == "" || project == "" {
		t.Skip("skipped: AF_XATA_API_KEY, AF_XATA_ORG and AF_XATA_PROJECT are not all " +
			"set, so the suite did not run against Xata and the copy on write declaration " +
			"stays unproven in engine/conformance/ledger.go")
	}

	limit := 4
	if raw := os.Getenv("AF_XATA_MAX_BRANCHES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err, "AF_XATA_MAX_BRANCHES is not a number")
		limit = parsed
	}

	opts := conformanceOptions()
	opts.Timeout = 6 * time.Minute
	opts.RealService = "Xata, at " + DefaultBaseURL
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
	}, opts)
}

// TestTheFakeControlPlaneReallyCopies is the evidence that asserting a real
// service in TestConformance would be false.
//
// It reads the fake's own byte counter across one branch. The counter is not a
// declaration: the fake adds what pg_database_size reported for the template
// immediately before the CREATE DATABASE that copied it, so a non zero reading
// is bytes Postgres actually moved and bytes a copy on write snapshot would not
// have moved. A branch rather than a refresh, because the branch is the
// operation the copy on write claim is about.
func TestTheFakeControlPlaneReallyCopies(t *testing.T) {
	admin := requirePostgres(t)
	f := newFakeXata(t, admin)
	p := providerOver(t, f)
	ctx := context.Background()

	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{Version: localMajor(t, admin), RulesHash: "copies01"})
	require.NoError(t, err)

	f.resetCounter()
	_, err = p.Branch(ctx, gv.ID, "env_really_copies")
	require.NoError(t, err)

	require.Positive(t, f.copied(),
		"this fake control plane branched without moving a byte, so the reason "+
			"TestConformance leaves conformance.Options.RealService empty no longer holds. "+
			"Either the fake stopped copying, in which case the copy on write behaviour "+
			"could honestly be measured here, or the branch stopped carrying the golden's "+
			"data, which is a much worse defect")
}

// TestTheConformanceRunDoesNotAssertARealService is the other half.
//
// The suite's default is unproven and forgetting the field produces it, so this
// asserts the field STAYS empty rather than that somebody remembered to leave
// it so. Pointing TestConformance at a real endpoint has to change this test,
// and changing it is where the sentence above gets read again.
func TestTheConformanceRunDoesNotAssertARealService(t *testing.T) {
	require.Empty(t, conformanceOptions().RealService,
		"the fake backed suite claims to drive a real service. The control plane it "+
			"drives is fake_test.go and the Postgres under it is local, so every service "+
			"owned behaviour would be decided by a measurement of that Postgres and "+
			"published as a measurement of Xata")
}

// TestSweepLeftovers removes anything a killed real run left behind.
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
