package env

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
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
	first, firstGoldens, stopFirst := requireOrchestrator(
		t, storeProject, "publisher", sourceURL, storeDir)
	defer stopFirst()

	refreshed, err := first.RefreshGolden(ctx)
	// Registered before the error check: a refresh that fails partway can
	// still have committed the image, and the teardown has to know about it.
	firstGoldens.add(refreshed.Version)
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
	second, secondGoldens, stopSecond := requireOrchestrator(
		t, storeProject, "puller", "", storeDir)
	defer stopSecond()

	// The two machines share this laptop's Docker daemon, so they share its
	// image store, and pretending otherwise would be a fiction the assertion
	// then has to work around. What is real and is asserted below is that the
	// second machine has NO production credential: PROD_URL is unset in its
	// environment, so it could not refresh even if it wanted to, and the only
	// way it can end up with data is through the store.
	pulled, err := second.PullGolden(ctx, "")
	secondGoldens.add(pulled.Version)
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

// A shared store does not make one project's golden everybody's.
//
// `af golden pull` with no version named takes the newest complete object in
// the store, and a store is shared by a fleet on purpose. Without a check on
// whose golden it is, the newest object in a bucket several projects publish
// to would be restored into whichever project asked, and every later `af up`
// there would branch it as its own: the same defect as the local one, one
// layer further out and with the data crossing a machine boundary on the way.
//
// The refusal is by name so somebody can act on it, and naming a version
// explicitly does not get around it, because the check is on the attestation
// rather than on which version was chosen.
func TestPull_RefusesAGoldenPublishedByAnotherProject(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	sourceURL, stopSource := requireSourceDatabase(t, ctx)
	defer stopSource()
	storeDir := t.TempDir()

	publisher, publisherGoldens, stopPublisher := requireOrchestrator(
		t, storeProject, "publisher", sourceURL, storeDir)
	defer stopPublisher()

	refreshed, err := publisher.RefreshGolden(ctx)
	publisherGoldens.add(refreshed.Version)
	require.NoError(t, err)
	require.NotEmpty(t, refreshed.Published, "the golden reached the store")

	// A different project, everything else identical: the same store, the same
	// provider, the same Postgres version, the same declared source variable
	// and the same subset. Only the manifest name differs, which is the thing
	// that says whose work this is.
	stranger, strangerGoldens, stopStranger := requireOrchestrator(
		t, "some-other-app", "puller", "", storeDir)
	defer stopStranger()

	pulled, err := stranger.PullGolden(ctx, "")
	if pulled != nil {
		strangerGoldens.add(pulled.Version)
	}
	require.Error(t, err, "another project's published golden was restored into this one")
	require.Contains(t, err.Error(), "AF-DB-015")

	// And naming it explicitly is refused for the same reason, so the refusal
	// is not merely a property of how the newest version is chosen.
	pulled, err = stranger.PullGolden(ctx, refreshed.Version)
	if pulled != nil {
		strangerGoldens.add(pulled.Version)
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-DB-015")

	// The counter check, so this test cannot pass by the pull being broken for
	// everybody: the project that published it can still pull it.
	owner, ownerGoldens, stopOwner := requireOrchestrator(
		t, storeProject, "puller", "", storeDir)
	defer stopOwner()
	ok, err := owner.PullGolden(ctx, "")
	if ok != nil {
		ownerGoldens.add(ok.Version)
	}
	require.NoError(t, err, "the project that published this golden must still be able to pull it")
	require.True(t, ok.Verified)
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

	o, _, stop := requireOrchestrator(t, storeProject, "puller", "", storeDir)
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

	// Unique per run, for the same reason the standing server path names its
	// database af_store_source_<nanos>. A fixed name is reused rather than
	// created when a container of that name is still there, and the schema is
	// then applied to a branch that already has it: "relation regions already
	// exists", on every subsequent run, until somebody notices a stray
	// container. That is not hypothetical. This test takes minutes, so a run
	// killed by a test timeout or a control C skips its cleanup and leaves
	// af-db-storesource behind, and the next run of the suite fails for a
	// reason that has nothing to do with the change being tested.
	branch, err := p.Branch(ctx, gv.ID, fmt.Sprintf("storesource%d", time.Now().UnixNano()%1e9))
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

// storeProject is the manifest name both machines in the store test declare.
//
// One name, because they are one project: a runner pulling what a laptop
// published is the same repository checked out somewhere else, and a golden is
// selected by the project it was made for. The fixture used to give them two
// names, app-publisher and app-puller, while its own comment said "same
// manifest", and nothing noticed because selection did not look at the name.
const storeProject = "app-goldenstore"

// requireOrchestrator builds one machine's engine: its own state directory,
// its own Docker provider namespace, and a manifest pointing at the store.
//
// project and branch are separate parameters because they are separate
// properties and the difference is the point of the fixture. Two machines of
// ONE project differ by branch, which a golden's identity ignores, so what one
// publishes the other may branch. A different project differs by name, which
// the identity includes, so it may not.
//
// sourceURL is the CREDENTIAL and not the declaration. Both machines declare
// source_url_env: PROD_URL, because both are the same manifest; only the
// publisher has a value for it. That asymmetry is the whole reason a golden's
// identity records the variable's NAME rather than the resolved host: a runner
// that could resolve production would not need the store.
func requireOrchestrator(
	t *testing.T, project, branch, sourceURL, storeDir string,
) (*Orchestrator, *ownGoldens, func()) {
	t.Helper()
	one := 1
	env := map[string]string{"AF_GOLDEN_STORE": storeDir}
	db := &schema.Database{
		Provider:     schema.DBDocker,
		Version:      testPostgresMajor,
		SourceURLEnv: "PROD_URL",
		Subset: &schema.Subset{
			Enabled:          true,
			SeedTable:        "regions",
			SeedWhere:        "code = 'eu'",
			MaxRows:          1000,
			FollowDependents: &one,
		},
		Golden: &schema.Golden{
			Retain:     3,
			Storage:    schema.StorageLocal,
			StorageURL: "$AF_GOLDEN_STORE",
		},
	}
	if sourceURL != "" {
		env["PROD_URL"] = sourceURL
	}

	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: project, Database: db},
		Branch:   branch,
		Clock:    clock.New(),
		Getenv:   func(k string) string { return env[k] },
		Secrets: secrets.NewChain(secrets.NewCISource(func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		})),
	})
	require.NoError(t, err)

	mine := &ownGoldens{}
	return o, mine, func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		// Whatever THIS orchestrator made, it takes away, and nothing else.
		//
		// This used to list every golden on the daemon and destroy all of
		// them. The daemon is shared: `go test ./...` runs packages in
		// parallel, so internal/db/docker's conformance suite is refreshing
		// and branching its own goldens on the same image store at the same
		// time. This teardown deleted them, and a conformance behaviour that
		// had refreshed a golden but not yet branched it failed with AF-DB-004
		// naming a version it had just created. That is the intermittent
		// failure of TestConformance/Branch_ReadsAKnownRow, and it was
		// misdiagnosed twice as a name collision: the one golden that ever
		// survived the sweep was this test's own, protected only because a
		// branch already pinned it and DestroyGolden refuses those.
		//
		// A leak here is now a leak the detector reports, which is the right
		// place for it. Deleting somebody else's golden to keep our own house
		// tidy trades a visible leak for an invisible flake.
		for _, id := range mine.ids {
			_ = o.DestroyGolden(c, id)
		}
	}
}

// ownGoldens records the golden versions one orchestrator created, so its
// teardown can remove exactly those on a daemon it shares with other packages.
type ownGoldens struct{ ids []string }

func (g *ownGoldens) add(id string) {
	if id != "" {
		g.ids = append(g.ids, id)
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

// TestOrchestratorTeardown_LeavesGoldensItDidNotCreate is the regression test
// for the flake that outlasted two wrong diagnoses.
//
// This teardown used to list every golden on the daemon and destroy all of
// them. `go test ./...` runs packages in parallel and they share one Docker
// daemon, so this deleted goldens belonging to internal/db/docker's
// conformance suite while that suite was mid-behaviour, and
// TestConformance/Branch_ReadsAKnownRow failed with AF-DB-004 naming a version
// it had itself created seconds earlier.
//
// The check is both halves. A golden this orchestrator never made survives its
// teardown, and one it did make does not, so the test cannot pass by the
// teardown having quietly stopped removing anything at all.
func TestOrchestratorTeardown_LeavesGoldensItDidNotCreate(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cli, err := dockerutil.Client()
	if err != nil {
		t.Skipf("no Docker daemon is reachable: %v", err)
	}
	defer func() { _ = cli.Close() }()

	// Two goldens that stand in for another package's work and for this
	// orchestrator's own. They are empty images rather than real Postgres
	// commits because what is under test is which tags the teardown chooses,
	// not what is inside them, and an empty import takes milliseconds.
	stamp := time.Now().UnixNano()
	foreign := fmt.Sprintf("gv_%d_foreign0", stamp)
	ours := fmt.Sprintf("gv_%d_ours0000", stamp)
	for _, id := range []string{foreign, ours} {
		ref := dockerdb.ImageRepo + ":" + id
		body, importErr := cli.ImageImport(ctx,
			image.ImportSource{Source: bytes.NewReader(make([]byte, 1024)), SourceName: "-"},
			ref, image.ImportOptions{})
		if importErr != nil {
			t.Skipf("this daemon will not import a scratch image: %v", importErr)
		}
		dockerutil.Discard(body)
		t.Cleanup(func() {
			c, done := context.WithTimeout(context.Background(), time.Minute)
			defer done()
			_, _ = cli.ImageRemove(c, ref, image.RemoveOptions{Force: true})
		})
	}

	o, mine, stop := requireOrchestrator(t, storeProject, "teardown", "", t.TempDir())
	// Only one of the two is claimed, which is the whole point.
	mine.add(ours)
	stop()

	after, err := o.Goldens(ctx)
	require.NoError(t, err)
	ids := make(map[string]bool, len(after))
	for _, g := range after {
		ids[g.ID] = true
	}
	require.True(t, ids[foreign],
		"the teardown destroyed a golden this orchestrator never created; on a shared "+
			"daemon that is another package's environment disappearing under it")
	require.False(t, ids[ours],
		"the teardown left behind a golden it did create, so this test would pass "+
			"even if teardown removed nothing at all")
}
