package docker_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A branch this provider makes can have its statements timed.
//
// pg_stat_statements has to be preloaded when the server starts. Created
// without the preload it exists and records nothing, and the insights on every
// Docker branch reported "query statistics need the pg_stat_statements
// extension, which is not available here" while the CI container that the
// same repository's own tests run against had preloaded it from the start.
// This reads what the server says, on a branch of a committed golden, because
// a committed image is where a lost command would go unnoticed.
func TestABranchPreloadsStatementStatistics(t *testing.T) {
	requireDocker(t)
	p, err := dockerdb.New(dockerdb.Options{
		Version: 17, Clock: clock.New(), PortFrom: 47400,
		SeedSQL: "CREATE TABLE things (id int); INSERT INTO things VALUES (1), (2), (3);",
	})
	require.NoError(t, err)
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "stats1234", Provenance: "gp1-statement-statistics",
		Mask:   func(context.Context, secrets.Value) error { return nil },
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"ok":true}`, nil },
	})
	require.NoError(t, err)
	defer func() {
		c, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel2()
		_ = p.DestroyGolden(c, gv.ID)
	}()

	b, err := p.Branch(ctx, gv.ID, "env_statistics0001")
	require.NoError(t, err)
	defer func() {
		c, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel2()
		_ = p.Destroy(c, b)
	}()

	url, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	conn, err := pgx.Connect(ctx, url.Reveal())
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()

	var preload string
	require.NoError(t, conn.QueryRow(ctx, "SHOW shared_preload_libraries").Scan(&preload))
	require.Contains(t, preload, "pg_stat_statements",
		"the branch's server must have been started with the module preloaded")

	// And it records: a statement run before the view existed is in the view
	// once it does, which is what lets the insights create it after the fact.
	var n int
	require.NoError(t, conn.QueryRow(ctx, "SELECT count(*) FROM things WHERE id > 1").Scan(&n))
	_, err = conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_stat_statements")
	require.NoError(t, err)
	rows, err := conn.Query(ctx, "SELECT query FROM pg_stat_statements")
	require.NoError(t, err)
	defer rows.Close()
	seen := false
	for rows.Next() {
		var q string
		require.NoError(t, rows.Scan(&q))
		if strings.Contains(q, "FROM things") {
			seen = true
		}
	}
	require.NoError(t, rows.Err())
	require.True(t, seen, "the module must record statements run before the view was created")
}
