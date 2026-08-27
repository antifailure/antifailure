package env

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The point of this file is that RunInsights has a caller and that the caller
// works. Everything the insights package does is proved in its own tests
// against a real Postgres; what is proved here is the part only the
// orchestrator does: find the golden, branch it, find the repository's
// migrations, rehearse them on that branch and not on the environment's, and
// take the branch away again.
//
// The last of those is why this test exists at all rather than being folded
// into the package's own suite. A rehearsal branch that outlives the check is a
// copy of production's data nobody is watching, and nothing inside the
// insights package can prove it was removed.

const insightsFixture = `
CREATE TABLE orders (
  id          bigserial PRIMARY KEY,
  user_id     bigint NOT NULL,
  total_cents int NOT NULL
);
INSERT INTO orders (user_id, total_cents)
  SELECT g, g FROM generate_series(1, 5000) g;
ANALYZE;
`

func TestRunInsightsRehearsesOnItsOwnBranchAndTakesItAway(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	p, err := dockerdb.New(dockerdb.Options{Version: 17, Clock: clock.New(), PortFrom: 46900})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	golden, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "insights-env-test",
		Mask: func(ctx context.Context, url secrets.Value) error {
			conn, cErr := pgx.Connect(ctx, url.Reveal())
			if cErr != nil {
				return cErr
			}
			defer func() { _ = conn.Close(context.Background()) }()
			_, eErr := conn.Exec(ctx, insightsFixture)
			return eErr
		},
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"rows":0}`, nil },
	})
	if err != nil {
		t.Skipf("skipped: no golden could be made: %v", err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 3*time.Minute)
		defer done()
		_ = p.DestroyGolden(c, golden.ID)
	})

	// A repository with a migration in it, which is what Discover reads.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "migrations"), 0o755))
	// int to bigint: it succeeds, it rewrites every row, and whether it
	// rewrites depends on the type it is coming FROM, which is in the
	// catalogue and not in the statement. So one migration exercises the
	// applier, the event trigger and the catalogue aware lint at once.
	require.NoError(t, os.WriteFile(filepath.Join(root, "migrations", "001_widen.sql"),
		[]byte("ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint;\n"), 0o644))

	// Below the fixture's row count, so the lint has something real to say
	// about a table this size.
	rows := 1000
	o, err := New(Options{
		Root:   root,
		Branch: "main",
		Clock:  clock.New(),
		Manifest: &schema.Manifest{
			Name:     "insightsenv",
			Database: &schema.Database{Provider: schema.DBDocker, Version: 17},
			Insights: &schema.Insights{LargeTableRows: rows},
		},
		Secrets: secrets.NewChain(),
	})
	require.NoError(t, err)

	// The environment's own database, as af up would have left it.
	envBranch, err := p.Branch(ctx, golden.ID, o.EnvID())
	require.NoError(t, err)
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 3*time.Minute)
		defer done()
		_ = p.Destroy(c, envBranch)
	})

	full, err := o.RunInsights(ctx, InsightsOptions{Limit: 10})
	require.NoError(t, err)

	require.NotNil(t, full.Rehearsal, "the migrations were never rehearsed")
	require.False(t, full.Rehearsal.Failed, full.Rehearsal.Error)
	require.Equal(t, insights.ToolSQLDir, full.Rehearsal.Tool)
	require.Len(t, full.Rehearsal.Pending, 1)
	require.NotEmpty(t, full.Rehearsal.Statements, "no statement was timed")

	// The lint ran against the branch's real catalogue rather than against the
	// SQL alone. Nothing in the statement says the column is an int today, and
	// that is the fact that decides whether Postgres rewrites the table.
	require.Len(t, full.Rehearsal.Lint, 1)
	require.Equal(t, insights.RuleAlterColumnType, full.Rehearsal.Lint[0].Rule)
	require.Equal(t, "orders", full.Rehearsal.Lint[0].Table)
	require.Greater(t, full.Rehearsal.Lint[0].Rows, int64(rows),
		"the row count came from the branch, and it is what makes a rewrite an outage")

	// And Postgres itself said it rewrote the table, through the event trigger
	// the orchestrator installed on the branch it made.
	require.Contains(t, full.Rehearsal.Rewrote(), "orders")

	// The environment's own database was NOT migrated. Rehearsing on it would
	// change the thing somebody is using, which is not a rehearsal.
	envURL, err := p.ConnString(ctx, envBranch, provider.ConnDirect)
	require.NoError(t, err)
	conn, err := pgx.Connect(ctx, envURL.Reveal())
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()

	var dataType string
	require.NoError(t, conn.QueryRow(ctx,
		"SELECT data_type FROM information_schema.columns "+
			"WHERE table_name = 'orders' AND column_name = 'total_cents'").Scan(&dataType))
	require.Equal(t, "integer", dataType,
		"the migration was applied to the environment's own database, not to a rehearsal branch")

	// And the rehearsal branch is gone. A copy of production's data that
	// outlives the check is a copy nobody is watching.
	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range inventory {
		require.NotContains(t, r.ID, o.EnvID()+"-rehearsal",
			"the rehearsal branch outlived the rehearsal")
	}
}
