package env

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	"github.com/antifailure/antifailure/engine/internal/golden"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The loop this whole feature exists for, end to end and against real things:
// one machine reads production, takes a slice of it, masks it, verifies it, and
// publishes it; a second machine that has never seen production pulls it,
// verifies it again for itself, and branches it.
//
// It is one test rather than several because the value is in the seam. Every
// piece of it has its own tests elsewhere; what nothing else checks is that a
// dump written by the publish is a dump the pull can restore, and that what
// comes out the far end is the same data.

// testPostgresMajor is the server version this suite asks for.
//
// 17, matching the server this job now starts and the one `just db` starts.
// It used to be 16, and the reason written here was that the GitHub runners
// ship a 16 client and pg_dump refuses to read a server newer than itself.
// That reason no longer holds: this branch gives the engine job a Postgres 17
// and the job now installs a 17 client to go with it.
//
// Keeping 16 here after that change is what actually broke, and it broke from
// the other side. The source became the 17 standing server while the golden
// stayed 16, so pg_dump 17 read the source correctly and the dump it produced
// carried SET transaction_timeout, which exists from 17 onward, and restoring
// that into a 16 target failed on an unrecognized parameter. A client can be
// too old for the source and too new for the target, and only the first of
// those is a refusal pgcopy makes by name today.
//
// Nothing here is version specific: the catalogue queries, COPY, generated and
// identity columns, composite keys, materialized common table expressions and
// session_replication_role behave the same on both.
const testPostgresMajor = 17

// sourceSchema is small and has a foreign key, so that the subset has
// something to close over and the restore has something to get wrong.
const sourceSchema = `
CREATE TABLE regions (
  code text PRIMARY KEY,
  name text NOT NULL
);
CREATE TABLE customers (
  id          bigserial PRIMARY KEY,
  region_code text NOT NULL REFERENCES regions(code),
  email       text NOT NULL
);
INSERT INTO regions VALUES ('eu','Europe'),('us','Americas');
INSERT INTO customers (region_code, email) VALUES
  ('eu','ada@eu.example'), ('eu','grace@eu.example'), ('us','katherine@us.example');
`

func TestPublishAndPull_ASecondMachineBranchesWhatTheFirstPublished(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// A real Postgres to be production, and a real store to publish into. The
	// store is the filesystem one, because what is being tested here is the
	// seam between the refresh and the pull, and the three backends have
	// already been proved to behave identically against real servers in
	// internal/golden.
	sourceURL, stopSource := requireSourceDatabase(t, ctx)
	defer stopSource()
	storeDir := t.TempDir()

	// Machine one. It has the production credential and a subset block.
	first, stopFirst := requireOrchestrator(t, "publisher", sourceURL, storeDir)
	defer stopFirst()

	refreshed, err := first.RefreshGolden(ctx)
	require.NoError(t, err)
	require.True(t, refreshed.Verified)
	require.NotEmpty(t, refreshed.Version)
	require.Empty(t, refreshed.PublishError, "the publish is reported when it fails")
	require.NotEmpty(t, refreshed.Published, "and named when it works")

	// The subset ran, so what was published is a slice and not the whole thing.
	require.NotNil(t, refreshed.Subset, "the manifest asked for a subset and one was taken")
	require.Empty(t, refreshed.Subset.Orphans)
	rows := map[string]int64{}
	for _, tbl := range refreshed.Subset.Tables {
		rows[tbl.Table] = tbl.Rows
	}
	require.Equal(t, int64(1), rows["public.regions"],
		"the seed is one region, so the slice is one region")
	require.Equal(t, int64(2), rows["public.customers"],
		"and its two customers, followed downward; the American one is not in the slice")

	// Machine two. Same manifest, same store, and NO production credential at
	// all, which is the point: a runner that cannot reach production is
	// exactly what this is for.
	second, stopSecond := requireOrchestrator(t, "puller", "", storeDir)
	defer stopSecond()

	// The two machines share this laptop's Docker daemon, so they share its
	// image store, and pretending otherwise would be a fiction the assertion
	// then has to work around. What is real and is asserted below is that the
	// second machine has NO production credential: its manifest has no
	// source_url_env, so it could not refresh even if it wanted to, and the
	// only way it can end up with data is through the store.
	pulled, err := second.PullGolden(ctx, "")
	require.NoError(t, err)
	require.Equal(t, refreshed.Version, pulled.From, "the newest complete version was taken")
	require.True(t, pulled.Verified,
		"and it was verified HERE, against the database that actually arrived")
	require.Positive(t, pulled.Bytes)
	require.NotEqual(t, pulled.From, pulled.Version,
		"the local copy gets its own identifier, because it was made now")

	// The data survived the round trip, which is the only claim that matters.
	branchURL, stopBranch := requireBranch(t, ctx, second, pulled.Version)
	defer stopBranch()

	conn, err := pgx.Connect(ctx, branchURL)
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()

	var customers, regions, orphans int64
	require.NoError(t, conn.QueryRow(ctx, "SELECT count(*) FROM customers").Scan(&customers))
	require.NoError(t, conn.QueryRow(ctx, "SELECT count(*) FROM regions").Scan(&regions))
	require.NoError(t, conn.QueryRow(ctx, `
		SELECT count(*) FROM customers c
		WHERE NOT EXISTS (SELECT 1 FROM regions r WHERE r.code = c.region_code)`).Scan(&orphans))

	require.Equal(t, int64(2), customers)
	require.Equal(t, int64(1), regions)
	require.Equal(t, int64(0), orphans,
		"the foreign keys still resolve on the far side of the store")

	var americans int64
	require.NoError(t, conn.QueryRow(ctx,
		"SELECT count(*) FROM customers WHERE region_code = 'us'").Scan(&americans))
	require.Equal(t, int64(0), americans, "and it is still a slice rather than everything")
}

func TestPull_RefusesAVersionWhosePublishDidNotFinish(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	// A dump with no attestation beside it is a database with nothing to check
	// it against, which is the one thing a golden is not allowed to be. It is
	// refused by name rather than restored and trusted.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	storeDir := t.TempDir()
	store, err := golden.OpenStore(golden.KindLocal, storeDir, nil)
	require.NoError(t, err)
	require.NoError(t, store.Put(ctx, golden.DumpName("gv_halfpublished"), 3,
		strings.NewReader("abc")))

	o, stop := requireOrchestrator(t, "puller", "", storeDir)
	defer stop()

	_, err = o.PullGolden(ctx, "gv_halfpublished")
	require.Error(t, err)
	require.Contains(t, err.Error(), "attestation")

	// And it is not offered by the listing either, so nobody reaches it by
	// asking for the newest.
	_, err = o.PullGolden(ctx, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no complete versions")
}

// requireSourceDatabase produces a real Postgres to play production.
//
// Two paths, and the reason for each. When AF_TEST_DATABASE_URL names a server
// that is already running, a database on it is used: this test already asks
// the Docker provider for two candidates, each of which is an initdb, and on a
// busy machine a third is the one that misses the provider's readiness window.
// What that produces is a t.Skipf, so the suite prints ok and nothing ran,
// which is worse than a failure. The cheapest container is the one not created.
//
// With no such server, one is started here, because continuous integration has
// no standing Postgres for the engine job and a test that quietly skips there
// is a test that exists to be green rather than to check anything.
func requireSourceDatabase(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	if base := os.Getenv("AF_TEST_DATABASE_URL"); base != "" {
		return sourceOnStandingServer(t, ctx, base)
	}
	return sourceInItsOwnContainer(t, ctx)
}

func sourceOnStandingServer(t *testing.T, ctx context.Context, base string) (string, func()) {
	t.Helper()
	admin, err := pgx.Connect(ctx, base)
	require.NoError(t, err, "AF_TEST_DATABASE_URL is set, so an unreachable server is a failure "+
		"rather than a reason to skip: setting it is a statement that a database is meant to be there")

	name := fmt.Sprintf("af_store_source_%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)

	url := replaceDatabase(base, name)
	require.NoError(t, pgcopy.Exec(ctx, secrets.New(url), sourceSchema))

	return url, func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = admin.Exec(c, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = admin.Close(c)
	}
}

func sourceInItsOwnContainer(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	p, err := dockerdb.New(dockerdb.Options{
		Version: testPostgresMajor, Clock: clock.New(), PortFrom: 46900,
	})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: testPostgresMajor, RulesHash: "store-test",
		Mask:   func(context.Context, secrets.Value) error { return nil },
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"rows":0}`, nil },
	})
	require.NoError(t, err, "Docker is here, so a source that will not start is a failure "+
		"rather than a reason to skip")

	branch, err := p.Branch(ctx, gv.ID, "storesource")
	require.NoError(t, err)
	url, err := p.ConnString(ctx, branch, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, pgcopy.Exec(ctx, url, sourceSchema))

	return url.Reveal(), func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_ = p.Destroy(c, branch)
		_ = p.DestroyGolden(c, gv.ID)
		_ = p.Close()
	}
}

// replaceDatabase swaps the database in a connection string.
func replaceDatabase(url, name string) string {
	at := strings.LastIndex(url, "@")
	rest := url[at+1:]
	slash := strings.Index(rest, "/")
	suffix := ""
	if q := strings.Index(rest, "?"); q >= 0 {
		suffix = rest[q:]
	}
	return url[:at+1] + rest[:slash+1] + name + suffix
}

// requireOrchestrator builds one machine's engine: its own state directory,
// its own Docker provider namespace, and a manifest pointing at the store.
func requireOrchestrator(t *testing.T, name, sourceURL, storeDir string) (*Orchestrator, func()) {
	t.Helper()
	env := map[string]string{"AF_GOLDEN_STORE": storeDir}
	db := &schema.Database{
		Provider: schema.DBDocker,
		Version:  testPostgresMajor,
		Golden: &schema.Golden{
			Retain:     3,
			Storage:    schema.StorageLocal,
			StorageURL: "$AF_GOLDEN_STORE",
		},
	}
	if sourceURL != "" {
		env["PROD_URL"] = sourceURL
		db.SourceURLEnv = "PROD_URL"
		one := 1
		db.Subset = &schema.Subset{
			Enabled:          true,
			SeedTable:        "regions",
			SeedWhere:        "code = 'eu'",
			MaxRows:          1000,
			FollowDependents: &one,
		}
	}

	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app-" + name, Database: db},
		Branch:   "main",
		Clock:    clock.New(),
		Getenv:   func(k string) string { return env[k] },
		Secrets: secrets.NewChain(secrets.NewCISource(func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		})),
	})
	require.NoError(t, err)

	return o, func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		// Whatever this machine made, it takes away. A test that leaves a
		// golden image behind makes the next run of the leak detector blame
		// somebody else.
		goldens, listErr := o.Goldens(c)
		if listErr == nil {
			for _, g := range goldens {
				_ = o.DestroyGolden(c, g.ID)
			}
		}
	}
}

// requireBranch makes a branch of a version and hands back its connection
// string, plus the cleanup that removes it.
func requireBranch(t *testing.T, ctx context.Context, o *Orchestrator, version string) (string, func()) {
	t.Helper()
	s, err := o.open(ctx, "test branch")
	require.NoError(t, err)

	branch, err := s.dbProv.Branch(ctx, version, o.envID+"-check")
	require.NoError(t, err)
	url, err := s.dbProv.ConnString(ctx, branch, provider.ConnDirect)
	require.NoError(t, err)

	return url.Reveal(), func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = s.dbProv.Destroy(c, branch)
		s.close()
	}
}

var _ = fmt.Sprintf
