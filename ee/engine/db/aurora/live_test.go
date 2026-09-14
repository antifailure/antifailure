// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora_test

// The provider against real AWS, through the same calls a customer's af up makes.
//
// Everything else in this package drives fakerds, which this repository wrote,
// and a fake agreeing with the assumptions that produced it is not evidence
// that AWS agrees. This test is the other half. It needs a disposable source
// cluster, a runner inside that cluster's VPC because a clone's writer is never
// publicly accessible, and AWS credentials the provider discovers for itself.
// So it skips unless AF_AURORA_LIVE is 1, it refuses any source whose name is
// not a proof cluster's, and it never runs in CI. The runner that provisions
// the source, runs this and deletes everything is not in the repository; the
// provider page says what it did and what AWS answered.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/aurora"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// liveSourcePrefix is the only kind of source cluster this test will clone.
// Pointing it at anything else would clone somebody's real database.
const liveSourcePrefix = "af-aurora-proof-"

// liveRows is how many synthetic rows the source holds. Enough that a branch
// reading back the same count is reading the golden's data rather than an
// empty database, and nowhere near enough to say anything about size.
const liveRows = 100000

func TestLiveAuroraCloneMaskBranchAndDelete(t *testing.T) {
	if os.Getenv("AF_AURORA_LIVE") != "1" {
		t.Skip("skipped: needs a disposable Aurora PostgreSQL source and a runner in its VPC; set AF_AURORA_LIVE=1")
	}
	sourceCluster := os.Getenv("AF_AURORA_LIVE_SOURCE")
	require.True(t, strings.HasPrefix(sourceCluster, liveSourcePrefix),
		"the live proof clones only a cluster named %s*, and %q is not one", liveSourcePrefix, sourceCluster)
	host := os.Getenv("AF_AURORA_LIVE_HOST")
	require.NotEmpty(t, host, "AF_AURORA_LIVE_HOST is the source writer endpoint, used only to seed it")
	require.Empty(t, os.Getenv("AWS_ACCESS_KEY_ID"),
		"the live proof discovers credentials the way a runner on EC2 does, through the instance role")

	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Minute)
	defer cancel()

	sourceURL := url.URL{Scheme: "postgres", Host: host + ":5432", Path: "/postgres",
		User: url.UserPassword(os.Getenv("AF_AURORA_LIVE_USER"), os.Getenv("AF_AURORA_LIVE_PASSWORD")), RawQuery: "sslmode=require"}
	source, err := sql.Open("pgx", sourceURL.String())
	require.NoError(t, err)
	defer func() { _ = source.Close() }()

	// A login the source has and a preview must not. Its password is random per
	// run, so nothing in this file is a credential.
	var inherited [12]byte
	_, err = rand.Read(inherited[:])
	require.NoError(t, err)
	inheritedPassword := hex.EncodeToString(inherited[:])
	for _, statement := range []string{
		`DROP TABLE IF EXISTS af_live_proof, af_live_rows`,
		`CREATE TABLE af_live_proof (id integer PRIMARY KEY, value text NOT NULL)`,
		`INSERT INTO af_live_proof VALUES (1, 'synthetic-original')`,
		`CREATE TABLE af_live_rows AS SELECT g AS id, md5(g::text) AS body FROM generate_series(1, 100000) AS g`,
		`DO $proof$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'af_proof_reader') THEN CREATE ROLE af_proof_reader; END IF; END $proof$`,
		`ALTER ROLE af_proof_reader LOGIN PASSWORD '` + inheritedPassword + `'`,
		`GRANT SELECT ON af_live_proof TO af_proof_reader`,
	} {
		_, err := source.ExecContext(ctx, statement)
		require.NoError(t, err, "seeding the source")
	}
	// The clone is taken at the latest restorable time, which trails the last
	// write by a few minutes. Seeding and cloning at once would clone the
	// cluster from before the seed, so wait until AWS reports a restorable time
	// after it.
	seeded := time.Now().UTC()
	t.Logf("source seeded at %s with %d rows and one inherited login", seeded.Format(time.RFC3339), liveRows)

	p, err := aurora.New(ctx, aurora.Options{
		SourceCluster: sourceCluster,
		Region:        os.Getenv("AWS_REGION"),
		BranchKey:     secret.New(os.Getenv(aurora.DefaultVariable)),
		Getenv:        os.Getenv,
		// The class the registration reads from the same variable. A proof
		// account may allow only Serverless v2, and a clone takes whatever
		// class its writer is created with.
		InstanceClass: os.Getenv(aurora.InstanceClassVariable),
		PollInterval:  5 * time.Second,
	})
	require.NoError(t, err, "the provider describes the source before it returns")
	defer func() { _ = p.Close() }()
	require.True(t, p.Capabilities().CopyOnWrite)

	waitForRestorableTime(ctx, t, seeded)

	query := func(ctx context.Context, connection secret.Value, statement string) (string, error) {
		db, err := sql.Open("pgx", connection.Reveal())
		if err != nil {
			return "", err
		}
		defer func() { _ = db.Close() }()
		var value string
		return value, db.QueryRowContext(ctx, statement).Scan(&value)
	}

	var golden provider.GoldenVersion
	var branch provider.Branch
	t.Cleanup(func() {
		// A failure part way leaves clusters that bill by the hour. The runner
		// deletes by name as well, and this is the first line of that.
		cleanup, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
		defer cancel()
		if branch.ProviderRef != "" {
			if err := p.Destroy(cleanup, branch); err != nil {
				t.Errorf("cleanup: destroying the branch: %v", err)
			}
		}
		if golden.ID != "" {
			if err := p.DestroyGolden(cleanup, golden.ID); err != nil {
				t.Errorf("cleanup: destroying the golden: %v", err)
			}
		}
	})

	goldenStarted := time.Now()
	golden, err = p.RefreshGolden(ctx, provider.GoldenSpec{
		RulesHash:  "live-aurora-proof",
		Provenance: "disposable synthetic rows",
		Mask: func(ctx context.Context, connection secret.Value) error {
			_, err := query(ctx, connection, `UPDATE af_live_proof SET value = 'masked' WHERE id = 1 RETURNING value`)
			return err
		},
		Verify: func(ctx context.Context, connection secret.Value) (string, error) {
			value, err := query(ctx, connection, `SELECT value FROM af_live_proof WHERE id = 1`)
			if err != nil {
				return "", err
			}
			if value != "masked" {
				return "", errors.New("the golden's row was not masked")
			}
			count, err := query(ctx, connection, `SELECT count(*)::text FROM af_live_rows`)
			if err != nil {
				return "", err
			}
			if count != "100000" {
				return "", errors.New("the golden does not hold the source's rows, it holds " + count)
			}
			return "live-aurora-verified rows=" + count, nil
		},
	})
	require.NoError(t, err, "refreshing a golden from the source")
	t.Logf("AF_MEASURED golden_seconds=%.1f (clone of the source, writer, password rotation, mask, verify, publish)", time.Since(goldenStarted).Seconds())
	require.True(t, golden.Verified)

	listed, err := p.ListGoldens(ctx)
	require.NoError(t, err)
	if assert.Len(t, listed, 1) {
		assert.Equal(t, golden.ID, listed[0].ID)
		assert.True(t, listed[0].Verified, "a golden read back from its tags must still carry its attestation")
		assert.Equal(t, golden.Attestation, listed[0].Attestation)
	}

	const envID = "live-aurora-proof"
	branchStarted := time.Now()
	branch, err = p.Branch(ctx, golden.ID, envID)
	require.NoError(t, err, "branching the golden")
	t.Logf("AF_MEASURED branch_seconds=%.1f (clone of the golden, writer, password rotation)", time.Since(branchStarted).Seconds())

	againStarted := time.Now()
	again, err := p.Branch(ctx, golden.ID, envID)
	if assert.NoError(t, err) {
		assert.Equal(t, branch.ProviderRef, again.ProviderRef, "a retried Branch must return the same cluster")
	}
	t.Logf("AF_MEASURED branch_retry_seconds=%.1f (the same environment again)", time.Since(againStarted).Seconds())

	connection, err := p.ConnString(ctx, branch, provider.ConnDirect)
	require.NoError(t, err)
	parsed, err := url.Parse(connection.Reveal())
	require.NoError(t, err)
	t.Logf("branch connection sslmode=%s host_is_branch=%v", parsed.Query().Get("sslmode"),
		strings.HasPrefix(parsed.Hostname(), branch.ProviderRef+"."))

	value, err := query(ctx, connection, `SELECT value FROM af_live_proof WHERE id = 1`)
	require.NoError(t, err, "opening the branch with the connection string the provider handed out")
	assert.Equal(t, "masked", value, "the branch must hold the golden's masked row")
	count, err := query(ctx, connection, `SELECT count(*)::text FROM af_live_rows`)
	assert.NoError(t, err)
	assert.Equal(t, "100000", count, "the branch must hold every row the source had")

	written, err := query(ctx, connection, `UPDATE af_live_proof SET value = 'written-on-branch' WHERE id = 1 RETURNING value`)
	assert.NoError(t, err)
	assert.Equal(t, "written-on-branch", written)
	var original string
	assert.NoError(t, source.QueryRowContext(ctx, `SELECT value FROM af_live_proof WHERE id = 1`).Scan(&original))
	assert.Equal(t, "synthetic-original", original, "a write to the branch reached the source")
	goldenConnection, err := p.ConnString(ctx, provider.Branch{ProviderRef: golden.ProviderRef}, provider.ConnDirect)
	if assert.NoError(t, err, "a connection to the golden, to read it back") {
		goldenValue, err := query(ctx, goldenConnection, `SELECT value FROM af_live_proof WHERE id = 1`)
		assert.NoError(t, err)
		assert.Equal(t, "masked", goldenValue, "a write to the branch reached the golden")
	}

	// A clone carries the source's logins. The one the source has must not open
	// the branch, and must still open the source, so the check is refusing the
	// login and not merely failing to connect.
	readerURL := *parsed
	readerURL.User = url.UserPassword("af_proof_reader", inheritedPassword)
	assert.Error(t, ping(ctx, readerURL.String()), "a login copied from the source opened the branch")
	sourceReader := readerURL
	sourceReader.Host = sourceURL.Host
	sourceReader.RawQuery = "sslmode=require"
	assert.NoError(t, ping(ctx, sourceReader.String()), "the source's own login must keep working on the source")

	health, err := p.Health(ctx, branch)
	if assert.NoError(t, err) {
		assert.True(t, health.Reachable, "health: %s", health.Detail)
	}

	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	kinds := map[string]string{}
	for _, r := range inventory {
		kinds[r.ID] = r.Kind
	}
	assert.Equal(t, map[string]string{golden.ProviderRef: "cluster/golden", branch.ProviderRef: "cluster/branch"}, kinds)

	assert.Error(t, p.DestroyGolden(ctx, golden.ID), "a golden with a live branch must not be collected")

	destroyStarted := time.Now()
	require.NoError(t, p.Destroy(ctx, branch))
	t.Logf("AF_MEASURED branch_destroy_seconds=%.1f", time.Since(destroyStarted).Seconds())
	assert.NoError(t, p.Destroy(ctx, branch), "destroying a destroyed branch succeeds")
	gone, err := p.Health(ctx, branch)
	if assert.NoError(t, err) {
		assert.False(t, gone.Reachable)
	}
	branch = provider.Branch{}

	destroyStarted = time.Now()
	require.NoError(t, p.DestroyGolden(ctx, golden.ID))
	t.Logf("AF_MEASURED golden_destroy_seconds=%.1f", time.Since(destroyStarted).Seconds())
	golden = provider.GoldenVersion{}

	inventory, err = p.Inventory(ctx)
	require.NoError(t, err)
	assert.Empty(t, inventory, "the provider must hold nothing after teardown")
	listed, err = p.ListGoldens(ctx)
	require.NoError(t, err)
	assert.Empty(t, listed)
}

// waitForRestorableTime blocks until the runner says the source's latest
// restorable time has passed the seed. The provider clones at that time and
// has no way to ask for a later one, so this is the fixture's problem.
func waitForRestorableTime(ctx context.Context, t *testing.T, after time.Time) {
	t.Helper()
	marker := os.Getenv("AF_AURORA_LIVE_RESTORABLE_FILE")
	require.NotEmpty(t, marker, "the runner writes the source's latest restorable time to this file")
	for {
		raw, err := os.ReadFile(marker)
		if err == nil {
			if at, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw))); err == nil && at.After(after) {
				t.Logf("latest restorable time %s is after the seed", at.Format(time.RFC3339))
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("the source never reported a restorable time after the seed: %v", ctx.Err())
		case <-time.After(15 * time.Second):
		}
	}
}

func ping(ctx context.Context, connection string) error {
	db, err := sql.Open("pgx", connection)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}
