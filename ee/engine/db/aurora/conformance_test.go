// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora_test

// The shared database conformance suite, run against the Aurora provider.
//
// Every line of the provider runs. What is replaced is the thing on the other
// end of the socket: the RDS control plane is fakerds and the Postgres behind
// it is real, so the five behaviours that are claims about bytes are checked
// against bytes rather than against a fake's opinion of them.
//
// WHAT THIS IS EVIDENCE OF, stated here rather than left to be inferred.
//
// It is evidence that the provider's logic is right: that a clone is
// requested copy on write and never any other way, that a golden is masked
// before it is verified and published only if verification passed, that a
// branch holds the golden's rows and is isolated from the golden and from
// other branches, that branching an unverified or missing golden is refused
// with the code the engine knows, that the declared limit is enforced rather
// than hung on, that destroy removes and destroying twice succeeds, that
// health reports a removed branch as gone rather than erroring, and that the
// provider leaks nothing across a whole run.
//
// It is NOT evidence that AWS accepts these requests. Nothing here has an
// account and nothing here should: section 10 of the plan says no test may
// need one. What stands between this suite and a real Aurora is that the
// request shapes are what the RDS query API documents and the signature is
// recomputed and compared by the fake, and neither of those is the same as
// AWS having answered. The provider's own documentation page carries that
// sentence too, because a reader who finds it only in a test file has already
// been misled.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/aurora"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestConformance(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := aurora.New(context.Background(), options(t, server))
		require.NoError(t, err)
		return p
	}, conformance.Options{
		// Generous but bounded. Every behaviour here is a clone, which against
		// this fake is a server side file copy on a Postgres other suites are
		// using at the same time, and a hung call must fail the behaviour
		// rather than the job.
		Timeout:  4 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	})
}

// TestSweepLeftovers removes what a killed run left on the shared server.
//
// Separate from the suite, the way pgurl's is: a failing behaviour
// legitimately leaves databases behind for inspection, and a sweep that ran
// automatically would destroy the evidence. It is by name rather than by the
// provider's inventory because a killed run's fake took its cluster map with
// it, so the only record left is the prefix on the Postgres.
func TestSweepLeftovers(t *testing.T) {
	if os.Getenv("AF_AURORA_SWEEP") == "" {
		t.Skip("skipped: set AF_AURORA_SWEEP=1 to remove databases and roles left by a killed run")
	}
	url := requirePostgres(t)
	db, err := openAdmin(url)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'af\_aur\_%'`)
	require.NoError(t, err)
	var databases []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		databases = append(databases, name)
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	for _, name := range databases {
		t.Logf("dropping %s", name)
		_, err := db.ExecContext(ctx, `DROP DATABASE IF EXISTS `+quote(name)+` WITH (FORCE)`)
		require.NoError(t, err)
	}

	roles, err := db.QueryContext(ctx,
		`SELECT rolname FROM pg_roles WHERE rolname LIKE 'af\_aur\_%'`)
	require.NoError(t, err)
	var names []string
	for roles.Next() {
		var name string
		require.NoError(t, roles.Scan(&name))
		names = append(names, name)
	}
	require.NoError(t, roles.Err())
	require.NoError(t, roles.Close())
	for _, name := range names {
		t.Logf("dropping role %s", name)
		_, err := db.ExecContext(ctx, `DROP ROLE IF EXISTS `+quote(name))
		require.NoError(t, err)
	}
}
