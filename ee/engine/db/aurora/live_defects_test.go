// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora_test

// Three things real Aurora did on 2026-09-13 that fakerds did not, each of which
// stopped the provider on its first live run. The fake now does all three, and
// these tests point at them one at a time.

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/aurora/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// newFakeWithPasswordDelay is newFake with a slower rotation.
func newFakeWithPasswordDelay(t *testing.T, delay time.Duration) *fakerds.Server {
	t.Helper()
	server, err := fakerds.New(fakerds.Options{
		AdminURL:      requirePostgres(t),
		Prefix:        "af_aur_" + randomSuffix(t) + "_",
		Region:        testRegion,
		Credentials:   testCredentials,
		PasswordDelay: delay,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, problem := range server.Close() {
			t.Errorf("the fake control plane could not clean up: %v", problem)
		}
	})
	require.NoError(t, server.SeedSource(sourceCluster, conformance.DefaultSeedSQL))
	return server
}

// connects is a mask that does what the engine's masking does first: open the
// golden with the connection string the provider handed it.
func connects(ctx context.Context, connection secret.Value) error {
	db, err := sql.Open("pgx", connection.Reveal())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var one int
	return db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

func TestARotatedPasswordIsWaitedForBeforeAnythingConnects(t *testing.T) {
	// Against AWS the provider called ModifyDBCluster at 15:17:33, the cluster
	// still read available, the masking connected in the same second with the
	// new password, and Postgres refused it with 28P01. The golden was never
	// published. Two seconds is long enough that a provider which does not wait
	// cannot pass by luck.
	const delay = 2 * time.Second
	server := newFakeWithPasswordDelay(t, delay)
	p := newProvider(t, server)

	started := time.Now()
	golden, err := p.RefreshGolden(context.Background(), provider.GoldenSpec{
		RulesHash:  "rotation-is-waited-for",
		Provenance: "the fake",
		Mask:       connects,
		Verify: func(ctx context.Context, connection secret.Value) (string, error) {
			return "verified after the rotation landed", connects(ctx, connection)
		},
	})
	require.NoError(t, err, "a refresh must wait for the rotated password rather than connect before it is in force")
	require.True(t, golden.Verified)
	require.GreaterOrEqual(t, time.Since(started), delay,
		"the refresh finished before the password could have been in force, so it did not connect with it")
}

func TestDestroyingABranchWhoseWriterIsAlreadyDeletingSucceeds(t *testing.T) {
	// Against AWS the refresh's cleanup had already started deleting the
	// golden's writer, and the next delete was answered
	// "InvalidDBInstanceState: Instance ... is already being deleted". The
	// provider treats that as done only if it recognises the code, and it was
	// matching InvalidDBInstanceStateFault, which RDS never sends.
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	ctx := context.Background()

	golden, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		RulesHash: "deleting-writer", Provenance: "the fake", Mask: connects,
		Verify: func(ctx context.Context, connection secret.Value) (string, error) {
			return "verified", connects(ctx, connection)
		},
	})
	require.NoError(t, err)
	branch, err := p.Branch(ctx, golden.ID, "env-deleting-writer")
	require.NoError(t, err)

	require.True(t, server.StartDeletingInstance(branch.ProviderRef+"-w"),
		"the branch's writer was not found under the name the provider gives it, so nothing below is about a deleting writer")
	require.NoError(t, p.Destroy(ctx, branch), "a writer AWS is already deleting is the state destroy asked for")

	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range inventory {
		require.NotEqual(t, branch.ProviderRef, r.ID, "the branch's cluster outlived its destroy")
	}
	require.NoError(t, p.DestroyGolden(ctx, golden.ID))
}
