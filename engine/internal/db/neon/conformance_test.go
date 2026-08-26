package neon_test

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
	"github.com/antifailure/antifailure/engine/internal/db/neon"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// TestConformance runs the shared suite against the real Neon API.
//
// Against the real service and not a fake, for the same reason the Docker
// provider runs against a real daemon: a fake would prove this provider agrees
// with our idea of Neon, and what matters is that it agrees with Neon. Every
// behaviour worth having, from waiting on an asynchronous operation to what a
// compute does when it has been asleep, only exists on the real thing.
//
// It needs a project to work in, and it will create and delete branches there.
// Set AF_NEON_API_KEY and AF_NEON_PROJECT_ID, and point them at a project that
// holds nothing else.
func TestConformance(t *testing.T) {
	key, project := neonCredentials(t)

	// Free tier projects have a low branch ceiling and the suite makes a
	// branch per behaviour, so the limit is declared rather than discovered:
	// declaring it is also what makes the limit behaviour run at all.
	limit := 5
	if raw := os.Getenv("AF_NEON_MAX_BRANCHES"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		require.NoError(t, err, "AF_NEON_MAX_BRANCHES is not a number")
		limit = parsed
	}

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := neon.New(neon.Options{
			APIKey:      key,
			ProjectID:   project,
			Clock:       clock.New(),
			SeedSQL:     conformance.DefaultSeedSQL,
			MaxBranches: limit,
			// Neon's operations settle in seconds, so polling faster than the
			// default keeps a suite of twenty behaviours to a few minutes.
			PollInterval: 750 * time.Millisecond,
			PollTimeout:  4 * time.Minute,
		})
		require.NoError(t, err)
		return p
	}, conformance.Options{
		// Every step crosses the public internet to a compute that may be
		// starting cold, so this is generous. It is still a bound: a hung call
		// fails the behaviour rather than the job.
		Timeout:  8 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	})
}

// TestSweepLeftovers removes anything a failed run left behind.
//
// Run with -run TestSweepLeftovers when a run was killed part way. It is
// separate from the suite on purpose: a failing behaviour legitimately leaves
// things behind for inspection, and a sweep that ran automatically would
// destroy the evidence.
func TestSweepLeftovers(t *testing.T) {
	if os.Getenv("AF_NEON_SWEEP") == "" {
		t.Skip("skipped: set AF_NEON_SWEEP=1 to remove branches left by a killed run")
	}
	key, project := neonCredentials(t)
	c := &neon.Client{Key: key, ProjectID: project, PollInterval: time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	branches, err := c.ListBranches(ctx)
	require.NoError(t, err)

	// Environment branches first, then candidates, then goldens. A golden with
	// a child cannot be deleted, and doing it in the wrong order turns a sweep
	// into a list of errors.
	for _, prefix := range []string{neon.PrefixEnv, neon.PrefixCandidate, neon.PrefixGolden} {
		for _, b := range branches {
			if !strings.HasPrefix(b.Name, prefix) {
				continue
			}
			t.Logf("removing %s (%s)", b.Name, b.ID)
			require.NoError(t, c.DeleteBranch(ctx, b.ID))
		}
	}
}

func neonCredentials(t *testing.T) (secrets.Value, string) {
	t.Helper()
	key := os.Getenv("AF_NEON_API_KEY")
	project := os.Getenv("AF_NEON_PROJECT_ID")
	if key == "" || project == "" {
		t.Skip("skipped: AF_NEON_API_KEY and AF_NEON_PROJECT_ID are not set")
	}
	return secrets.NewFrom(key, "AF_NEON_API_KEY"), project
}
