package supabase_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/db/supabase"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// TestConformance runs the shared suite against the real Supabase Management
// API.
//
// Against the real service and not a fake, for the same reason the Neon and
// Docker providers are: a fake would prove this provider agrees with our idea
// of Supabase, and what matters is that it agrees with Supabase. Everything
// that decided the shape of this provider was learned this way and none of it
// was in the specification: that a persistent branch answers DELETE with 422,
// that a copy between two branches dies on a schema the platform owns in both,
// that the pooled connection string comes back with the password missing.
//
// It needs a project to work in, and it will create and delete branches there.
// Set AF_SUPABASE_TOKEN and AF_SUPABASE_PROJECT_REF, and point them at a
// project that holds nothing else. Branches are billed by the hour.
func TestConformance(t *testing.T) {
	token, ref := supabaseCredentials(t)

	// Declared rather than discovered, and small. Supabase does not report a
	// branch ceiling anywhere this provider can rely on, and declaring one is
	// also what makes the limit behaviour run at all rather than skip. Every
	// branch past this number is one more running project, so the number is
	// kept low on purpose.
	limit := 3
	if raw := os.Getenv("AF_SUPABASE_MAX_BRANCHES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err, "AF_SUPABASE_MAX_BRANCHES is not a number")
		limit = parsed
	}

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := supabase.New(supabase.Options{
			Token:       token,
			ProjectRef:  ref,
			Clock:       clock.New(),
			SeedSQL:     conformance.DefaultSeedSQL,
			Version:     17,
			MaxBranches: limit,
			// A branch reports healthy in about six seconds, so polling faster
			// than the default keeps a suite of twenty behaviours to minutes.
			PollInterval: 2 * time.Second,
			PollTimeout:  6 * time.Minute,
		})
		require.NoError(t, err)
		return p
	}, conformance.Options{
		// Every behaviour crosses the public internet, provisions at least one
		// project, and copies a database into it. Generous, and still a bound:
		// a hung call fails the behaviour rather than the job.
		Timeout:  10 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	})
}

// TestSweepLeftovers removes anything a failed run left behind.
//
// Run with -run TestSweepLeftovers when a run was killed part way. It is
// separate from the suite on purpose: a failing behaviour legitimately leaves
// things behind for inspection, and a sweep that ran automatically would
// destroy the evidence. On Supabase it matters more than on a local provider,
// because what is left behind is a running project that bills by the hour.
//
// It is safe to point at any project. The only branches it can touch are the
// ones whose names carry this provider's prefixes and that are not the default
// branch, which is the same rule Inventory uses. A Supabase project's branch
// listing includes a row for production itself, and that row is why the rule
// exists rather than being an afterthought.
func TestSweepLeftovers(t *testing.T) {
	if os.Getenv("AF_SUPABASE_SWEEP") == "" {
		t.Skip("skipped: set AF_SUPABASE_SWEEP=1 to remove branches left by a killed run")
	}
	token, ref := supabaseCredentials(t)
	c := &supabase.Client{Key: token, ProjectRef: ref, PollInterval: time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	branches, err := c.ListBranches(ctx)
	require.NoError(t, err)

	removed := 0
	for _, b := range branches {
		if !b.IsOurs() {
			t.Logf("leaving %s alone (%s)", b.Name, b.ProjectRef)
			continue
		}
		t.Logf("removing %s (%s)", b.Name, b.ID)
		require.NoError(t, c.DeleteBranch(ctx, b.ID))
		removed++
	}
	t.Logf("removed %d branches", removed)

	// Proved rather than assumed. A sweep that reported success while the
	// branches were still there would be worse than no sweep, because somebody
	// would stop checking the bill.
	after, err := c.ListBranches(ctx)
	require.NoError(t, err)
	for _, b := range after {
		require.False(t, b.IsOurs(), "%s survived the sweep", b.Name)
	}
}

func supabaseCredentials(t *testing.T) (secrets.Value, string) {
	t.Helper()
	token := strings.TrimSpace(os.Getenv("AF_SUPABASE_TOKEN"))
	ref := strings.TrimSpace(os.Getenv("AF_SUPABASE_PROJECT_REF"))
	if token == "" || ref == "" {
		t.Skip("skipped: AF_SUPABASE_TOKEN and AF_SUPABASE_PROJECT_REF are not set")
	}
	return secrets.NewFrom(token, "AF_SUPABASE_TOKEN"), ref
}
