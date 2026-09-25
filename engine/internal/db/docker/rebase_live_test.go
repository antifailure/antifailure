package docker_test

// ONE GOLDEN, TWO DATABASE BUILDS, AGAINST A REAL DAEMON.
//
// The question these exist for is the one a Postgres storage engine developer
// asked: the same workload and the same rows on a baseline and a candidate build
// of HIS engine. Everything else in this repository compares two builds of an
// application over one database, and this is the other axis. There is one data
// directory because two would be two sets of rows, so one build wrote it and the
// other opens it, and the two outcomes that matter are proved here rather than
// reasoned about: it opens and serves the same rows, or it refuses and the
// refusal is reported as its own finding with the server's own words in it.
//
// THE SECOND TEST IS AT THE PROVIDER RATHER THAN AT THE COMMAND, deliberately,
// and it is worth saying why. `af load compare` refuses two images of different
// majors before it builds anything, which is correct and which is also the
// easiest way to produce a data directory one build cannot open. So the finding
// is provoked here, one layer below that guard, with two majors of the published
// image. That is a real instance of the class: a catalog version this server does
// not know is exactly what a fork with its own catalog version produces, and the
// classifier's other cases, a block size and a WAL format, are pinned against
// real postmaster output in rebase_internal_test.go.
//
// SKIPPED rather than failed when an image cannot be had, and the skip says
// which. A machine with no daemon, no network or no room has disproved nothing.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The two builds the pairing tests use.
//
// Both are Postgres 17 and the data directory one writes is one the other can
// open, which is what makes them a usable stand in for two builds of one engine:
// the alpine image is musl and the pgvector image is glibc with an out of tree
// extension compiled in, so nothing about them is the same but the on disk
// format, which is the property the comparison rests on.
const (
	writerImage = "postgres:17-alpine"
	readerImage = pgvectorImage
	// olderImage writes nothing here. It is the build that CANNOT open a 17 data
	// directory, and its refusal is the finding.
	olderImage = "postgres:16-alpine"
)

// The plain case, and the one the whole feature exists to make possible: a golden
// written by one build, branched by another, serving the same rows.
//
// The row is read back rather than the branch merely being started, for the reason
// the conformance suite gives about itself: a container that starts and accepts
// connections over an EMPTY data directory would pass every weaker assertion, and
// that is precisely the failure a copy into a declared volume produces.
func TestABranchCanOpenAnotherBuildsDataDirectory(t *testing.T) {
	requireImage(t, writerImage)
	requireImage(t, readerImage)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// The golden is written by one build. Nothing here names a branch image, so
	// this provider is the one every existing caller gets.
	writer := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 49100, Image: writerImage,
		SeedSQL: `CREATE TABLE ledger (id int primary key, memo text);
			INSERT INTO ledger VALUES (1, 'written by the first build'), (2, 'and this one too');`,
	})
	gv := writer.refresh(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "rebase1", Provenance: "rebase-pairing",
	})

	// Its own branch first, so that the pairing is compared against something
	// rather than asserted alone. This is the side of a comparison that runs the
	// build the golden was made on.
	native := writer.branch(ctx, gv.ID, "env_rebase_native01")
	require.Equal(t, "written by the first build",
		scan[string](t, writer.open(ctx, native), `SELECT memo FROM ledger WHERE id = 1`))

	// And now the other build, over the same golden. A second provider because
	// the two sides of a comparison are two providers: the base side is
	// constructed with the image it is to open the golden with.
	reader := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 49200, Image: writerImage, BranchImage: readerImage,
	})
	other := reader.branch(ctx, gv.ID, "env_rebase_other001")
	conn := reader.open(ctx, other)

	// The same rows, which is the claim the whole comparison rests on.
	require.Equal(t, int64(2), scan[int64](t, conn, `SELECT count(*) FROM ledger`),
		"the other build opened an empty data directory, so the copy did not land")
	require.Equal(t, "written by the first build",
		scan[string](t, conn, `SELECT memo FROM ledger WHERE id = 1`))

	// And it really is the other build serving them. Read from the server rather
	// than from the provider, because a provider reporting which image it used is
	// a provider reporting on its own intentions: this asserts that the running
	// postmaster is the one compiled with the vector extension available, which
	// the alpine image the golden was written by does not carry.
	require.True(t, scan[bool](t, conn,
		`SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector')`),
		"the branch is running the build that wrote the golden, not the one asked for")
	require.False(t, scan[bool](t, writer.open(ctx, native),
		`SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector')`),
		"the control arm is wrong: the writing build must NOT carry the extension, "+
			"or the assertion above proves nothing")
}

// THE MOST VALUABLE VERDICT IN THE FEATURE. A build that cannot open the other
// build's data directory is a finding with the server's own words in it, and it
// is not a crash, not an inconclusive run, and not a timeout.
func TestABuildThatCannotOpenTheOtherBuildsDataDirectoryIsAFinding(t *testing.T) {
	requireImage(t, writerImage)
	requireImage(t, olderImage)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	writer := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 49300, Image: writerImage,
		SeedSQL: `CREATE TABLE ledger (id int primary key); INSERT INTO ledger VALUES (1);`,
	})
	gv := writer.refresh(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "rebase2", Provenance: "rebase-refusal",
	})

	refusing := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 49400, Image: writerImage, BranchImage: olderImage,
	})
	started := time.Now()
	b, err := refusing.p.Branch(ctx, gv.ID, "env_rebase_refuse01")
	// The container is returned with the error so that the caller can tear down
	// what the refusal left behind, and this test does the tearing down.
	t.Cleanup(func() {
		clean, cancelClean := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancelClean()
		_ = refusing.p.Destroy(clean, b)
	})

	require.Error(t, err, "a build that cannot open the data directory must not report a branch")
	// Recorded rather than only asserted on, so a run of this test leaves the
	// server's own account behind. The assertions below say the message contains
	// the right things; this is what a person actually reads.
	t.Logf("the finding, as somebody running the comparison sees it:\n%v", err)
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFDB044, coded.Code(),
		"this has to be its own finding rather than a readiness failure: %v", err)

	// The server's own account, which is the part a person acts on. Without it
	// the finding names a problem and withholds the one fact that explains it.
	require.Contains(t, err.Error(), "FATAL")
	require.Contains(t, err.Error(), "incompatible with server")
	require.Contains(t, err.Error(), olderImage,
		"the message names the image somebody typed, not the derived copy")
	require.Contains(t, err.Error(), gv.ID)

	// NOT A TIMEOUT. The readiness wait is five minutes and this must not have
	// spent it: a finding that arrives as a generic timeout is a finding nobody
	// reads as one. Generous, because the copy itself and the image pull are in
	// this measurement too.
	require.Less(t, time.Since(started), 4*time.Minute,
		"the refusal waited out the readiness timeout instead of noticing the server had gone")
}

// The copies a comparison leaves behind go when the golden does. PruneChildren
// cannot reach them, because a copy is built FROM the other build's image and the
// golden is not its parent, so without an explicit sweep a destroyed golden
// leaves a full sized managed image per build that ever opened it.
func TestDestroyingAGoldenRemovesTheCopiesOfItOntoOtherBuilds(t *testing.T) {
	requireImage(t, writerImage)
	requireImage(t, readerImage)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	writer := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 49500, Image: writerImage,
		SeedSQL: `CREATE TABLE ledger (id int primary key); INSERT INTO ledger VALUES (1);`,
	})
	gv := writer.refresh(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "rebase3", Provenance: "rebase-cleanup",
	})

	reader := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 49600, Image: writerImage, BranchImage: readerImage,
	})
	b := reader.branch(ctx, gv.ID, "env_rebase_clean001")

	// The inventory sees the copy, which is what a leak detector reads. A
	// resource this provider created and the inventory cannot name is a resource
	// that leaks silently.
	copies := 0
	inventory, err := reader.p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range inventory {
		if r.Kind == "image/rebased" && r.Labels["version"] == gv.ID {
			copies++
		}
	}
	require.Equal(t, 1, copies, "the copy of the golden onto the other build is not in the inventory")

	// The branch has to go first, because a golden a branch depends on is
	// refused, which is the guard that keeps an environment's data from
	// disappearing under it.
	require.NoError(t, reader.p.Destroy(ctx, b))
	require.NoError(t, writer.p.DestroyGolden(ctx, gv.ID))

	after, err := reader.p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range after {
		require.NotEqual(t, gv.ID, r.Labels["version"],
			"%s survived the golden it was made from", r.ID)
	}
}
