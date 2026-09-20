package docker_test

// EXTENSIONS AND CUSTOM TABLE ACCESS METHODS, AGAINST REAL POSTGRES IMAGES.
//
// The question these exist for came from somebody who writes Postgres storage
// engines: does this work with extensions and with custom storage. Before this
// wave the honest answer was no, and it was no in a way nothing in the
// repository said out loud. The docker provider hardcoded postgres:N-alpine,
// which carries the contrib modules and nothing else, so a schema using
// PostGIS, pgvector, TimescaleDB, pg_cron or an access method out of an
// extension could not be copied into a golden at all; AF-DB-007 said so and
// offered only "use a different provider".
//
// Every assertion below reads what a SERVER says rather than what the provider
// thinks. That is the same rule the statistics test is written to, and it is
// the only rule that can settle this: a provider reporting that it created an
// extension is a provider reporting on its own intentions.
//
// Two of the images are real and third party. pgvector is the out of tree
// extension people ask for most; timescaledb is the one that proves the
// preload path, because it is loaded by the postmaster and a server carrying
// its catalog entries without its library refuses to start rather than
// starting degraded. The third is built here, because the access method it
// carries has to be one whose relam is not the heap and no published image
// exists for that alone; the comment on customAMImage says what that does and
// does not settle, and where the semantics it is designed against were
// measured.
//
// They are SKIPPED rather than failed when the image cannot be had, and the
// skip says which image and why. A machine with no daemon, no network, or no
// room is not a machine that has disproved anything.

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/subset"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

const (
	// pgvectorImage carries the vector type and its index access methods, and
	// nothing about the server is unusual, so it is the plain case: an
	// extension that is installed and has to be created.
	pgvectorImage = "pgvector/pgvector:pg17"
	// timescaleImage carries an extension the postmaster loads. Creating it in
	// a server that did not preload the library fails with a message naming
	// shared_preload_libraries, which is what makes this image the instrument
	// for the preload path rather than a second copy of the pgvector case.
	timescaleImage = "timescale/timescaledb:2.17.2-pg17"
	// customAMImage carries a table access method compiled here rather than
	// pulled, and the reason is the whole difficulty of testing this.
	//
	// The access method people ask about is citus `columnar`, and it was
	// measured directly while this was written, on Postgres 17.2 in
	// citusdata/citus:13.0: a columnar table accepts a PRIMARY KEY, accepts
	// COPY ... FORMAT BINARY, reports through pg_am and pg_class.relam like
	// any other, and refuses both `SELECT ctid::text FROM t` and `UPDATE t SET
	// ... WHERE id = 2` with "UPDATE and CTID scans not supported for
	// ColumnarScan". Those four facts are what the refusal in the masking
	// dialect is designed against and they are quoted where that refusal
	// lives.
	//
	// What that image cannot be is a TEST fixture here. citus publishes amd64
	// only, so on an arm64 machine every container runs under emulation, and
	// measured on this one a branch of an emulated citus golden reached a
	// checkpoint that took 43 seconds to write 147 buffers and never became
	// usable. A test that cannot pass on the machine people develop on is a
	// test people learn to ignore.
	//
	// So the fixture is an extension built from four files below: a handler
	// function, a control file, an install script and a Makefile. It is a
	// genuine out of tree table access method, with its own shared object, its
	// own pg_am row of amtype 't' and its own relam on every table created
	// USING it, which is exactly what everything in this pipeline reads. Its
	// storage routines are the heap's, and that is stated rather than hidden:
	// it proves that an access method survives a golden, a branch, a dump and
	// restore and a subset with its identity intact, and it does not prove
	// that an engine with different storage semantics does.
	customAMImage = "antifailure/test-custom-am:17"
	// customAMName is the access method the extension registers.
	customAMName = "af_demo"
)

// The extension, as the files a PGXS build expects. Embedded rather than kept
// beside this file so that the build context is assembled from the test binary
// and no path outside it has to exist.
const (
	customAMSource = `#include "postgres.h"
#include "fmgr.h"
#include "access/heapam.h"
#include "access/tableam.h"

PG_MODULE_MAGIC;

PG_FUNCTION_INFO_V1(af_demo_am_handler);

Datum
af_demo_am_handler(PG_FUNCTION_ARGS)
{
	PG_RETURN_POINTER(GetHeapamTableAmRoutine());
}
`
	customAMControl = `comment = 'a table access method for proving custom storage survives a golden'
default_version = '1.0'
module_pathname = '$libdir/af_demo_am'
relocatable = true
`
	customAMInstall = `CREATE FUNCTION af_demo_am_handler(internal)
RETURNS table_am_handler
AS 'MODULE_PATHNAME'
LANGUAGE C STRICT;

CREATE ACCESS METHOD af_demo TYPE TABLE HANDLER af_demo_am_handler;
`
	customAMMakefile = "MODULES = af_demo_am\n" +
		"EXTENSION = af_demo_am\n" +
		"DATA = af_demo_am--1.0.sql\n" +
		"PG_CONFIG = pg_config\n" +
		"PGXS := $(shell $(PG_CONFIG) --pgxs)\n" +
		"include $(PGXS)\n"
	// Two stages, and the second one is why. Building an extension needs the
	// server headers, and on Debian those depend on LLVM, so a single stage
	// image is about four times the size of the one it started from. Every
	// golden built from it is a commit of that, and a commit of two gigabytes
	// is minutes rather than seconds. The runtime stage is the stock image
	// plus three files: the shared object and the two the extension machinery
	// reads.
	customAMDockerfile = `FROM postgres:17 AS build
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential postgresql-server-dev-17 \
 && rm -rf /var/lib/apt/lists/*
COPY af_demo_am.c af_demo_am.control af_demo_am--1.0.sql Makefile /src/
RUN cd /src && make && make install

FROM postgres:17
COPY --from=build /usr/lib/postgresql/17/lib/af_demo_am.so /usr/lib/postgresql/17/lib/
COPY --from=build /usr/share/postgresql/17/extension/af_demo_am.control /usr/share/postgresql/17/extension/
COPY --from=build /usr/share/postgresql/17/extension/af_demo_am--1.0.sql /usr/share/postgresql/17/extension/
`
)

// requireCustomAccessMethodImage builds the fixture image if the daemon does
// not already have it.
//
// Built rather than pulled, because no published image carries an access
// method whose only job is to be a different relam. The build needs apt and a
// compiler, so it is skipped rather than failed when it cannot happen: a
// machine with no network has not disproved anything.
func requireCustomAccessMethodImage(t *testing.T) {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	cli, err := dockerutil.Client()
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	defer func() { _ = cli.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	if _, err := cli.ImageInspect(ctx, customAMImage); err == nil {
		return
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range map[string]string{
		"Dockerfile":          customAMDockerfile,
		"af_demo_am.c":        customAMSource,
		"af_demo_am.control":  customAMControl,
		"af_demo_am--1.0.sql": customAMInstall,
		"Makefile":            customAMMakefile,
	} {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)),
		}); err != nil {
			t.Skipf("skipped: the build context could not be assembled: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Skipf("skipped: the build context could not be assembled: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Skipf("skipped: the build context could not be assembled: %v", err)
	}

	res, err := cli.ImageBuild(ctx, &buf, client.ImageBuildOptions{
		Tags: []string{customAMImage}, Remove: true, Dockerfile: "Dockerfile",
	})
	if err != nil {
		t.Skipf("skipped: %s could not be built: %v", customAMImage, err)
	}
	// The stream has to be drained before the build is finished, exactly as a
	// pull does, or the image is only partly there when the next call looks.
	dockerutil.Discard(res.Body)
	if _, err := cli.ImageInspect(ctx, customAMImage); err != nil {
		t.Skipf("skipped: %s is not present after building it: %v", customAMImage, err)
	}
}

// requireImage skips unless the daemon has, or can fetch, an image.
//
// Separate from requireDocker because the two say different things. A missing
// daemon means no container test in this package can run; a missing image
// means this one cannot, and saying which image is the difference between a
// skip somebody can act on and a skip nobody reads.
func requireImage(t *testing.T, ref string) {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	cli, err := dockerutil.Client()
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	defer func() { _ = cli.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return
	}
	rc, err := cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		t.Skipf("skipped: %s is not present and could not be pulled: %v", ref, err)
	}
	dockerutil.Discard(rc)
	if _, err := cli.ImageInspect(ctx, ref); err != nil {
		t.Skipf("skipped: %s is not present after pulling it: %v", ref, err)
	}
}

// extensionProvider is one provider under test, with its own port base so that
// two of these running at once cannot be handed the same port.
type extensionProvider struct {
	t *testing.T
	p *dockerdb.Provider
}

func newExtensionProvider(t *testing.T, opts dockerdb.Options) *extensionProvider {
	t.Helper()
	opts.Clock = clock.New()
	p, err := dockerdb.New(opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	return &extensionProvider{t: t, p: p}
}

// refresh builds a golden and registers its removal.
//
// The mask and verify callbacks are stubs on purpose: this file is about what
// the container carries, and the masking package's own behaviour against a
// custom access method is proved in TestMaskingRefusesToRewriteATableOnACustom
// AccessMethod, where it can be asserted rather than merely survived.
func (e *extensionProvider) refresh(ctx context.Context, spec provider.GoldenSpec) provider.GoldenVersion {
	e.t.Helper()
	if spec.Mask == nil {
		spec.Mask = func(context.Context, secrets.Value) error { return nil }
	}
	if spec.Verify == nil {
		spec.Verify = func(context.Context, secrets.Value) (string, error) { return `{"ok":true}`, nil }
	}
	gv, err := e.p.RefreshGolden(ctx, spec)
	require.NoError(e.t, err)
	e.t.Cleanup(func() {
		// A fresh context, so that a cancelled test still tears down. The
		// golden is an image and a leaked one is half a gigabyte.
		clean, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_ = e.p.DestroyGolden(clean, gv.ID)
	})
	return gv
}

// branch starts a branch and registers its removal.
//
// A failure here carries the container's own last words, because the two
// failures this has actually produced on an emulated image, an abrupt EOF and
// a server answering "the database system is in recovery mode", are
// indistinguishable from outside and the server log is the only thing that
// separates them. A refused dial with no log is a failure nobody can act on.
func (e *extensionProvider) branch(ctx context.Context, version, envID string) provider.Branch {
	e.t.Helper()
	b, err := e.p.Branch(ctx, version, envID)
	if err != nil {
		e.t.Fatalf("the branch did not come up: %v\n\ncontainer log:\n%s",
			err, branchLog(ctx, envID))
	}
	e.t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_ = e.p.Destroy(clean, b)
	})
	return b
}

// branchLog is the branch container's own output, by the deterministic name
// the provider derives from the environment identifier. Best effort: this runs
// on a path that has already failed and must not replace the caller's problem
// with its own.
func branchLog(ctx context.Context, envID string) string {
	cli, err := dockerutil.Client()
	if err != nil {
		return "unavailable: " + err.Error()
	}
	defer func() { _ = cli.Close() }()
	rc, err := cli.ContainerLogs(ctx, "af-db-"+envID, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: "40",
	})
	if err != nil {
		return "unavailable: " + err.Error()
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	return string(body)
}

// open connects to a branch through the provider's own connection string,
// because a test that builds its own has stopped testing the provider.
func (e *extensionProvider) open(ctx context.Context, b provider.Branch) *pgx.Conn {
	e.t.Helper()
	url, err := e.p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(e.t, err)
	conn, err := pgx.Connect(ctx, url.Reveal())
	require.NoError(e.t, err)
	e.t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func (e *extensionProvider) connString(ctx context.Context, b provider.Branch) secrets.Value {
	e.t.Helper()
	url, err := e.p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(e.t, err)
	return url
}

// scan reads one value out of a branch.
func scan[T any](t *testing.T, conn *pgx.Conn, sql string) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var out T
	require.NoErrorf(t, conn.QueryRow(ctx, sql).Scan(&out), "reading: %s", sql)
	return out
}

// TestADeclaredExtensionReachesTheGoldenAndEveryBranchOfIt is the plain case.
//
// The seed uses the extension's own type, which is what makes this a test of
// the extension rather than of a name in a catalog: `vector(3)` does not parse
// in a server where the extension was not created, so a provider that named
// the image and skipped the CREATE EXTENSION fails at the seed rather than
// passing with an empty pg_extension.
func TestADeclaredExtensionReachesTheGoldenAndEveryBranchOfIt(t *testing.T) {
	requireImage(t, pgvectorImage)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	e := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 47600,
		Image:      pgvectorImage,
		Extensions: []string{"vector", "pg_trgm"},
		SeedSQL: `CREATE TABLE documents (id int primary key, body text, embedding vector(3));
			INSERT INTO documents VALUES
				(1, 'the first document', '[1,0,0]'),
				(2, 'the second document', '[0,1,0]'),
				(3, 'the third document', '[0,0,1]');`,
	})

	gv := e.refresh(ctx, provider.GoldenSpec{Version: 17, RulesHash: "pgvector1", Provenance: "extensions-pgvector"})
	b := e.branch(ctx, gv.ID, "env_pgvector000001")
	conn := e.open(ctx, b)

	// Both extensions, on the BRANCH rather than on the candidate. A branch is
	// a container started from the committed image, so this is what proves the
	// extension survived the commit rather than only existing in the candidate
	// the commit was taken from.
	require.True(t, scan[bool](t, conn,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')`),
		"the out of tree extension the manifest declared is not in the branch")
	require.True(t, scan[bool](t, conn,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')`),
		"the contrib extension the manifest declared is not in the branch")

	// The type works, the rows arrived, and the extension's own operator
	// answers. An extension present in pg_extension and not usable would pass
	// the two assertions above.
	require.Equal(t, int64(3), scan[int64](t, conn, `SELECT count(*) FROM documents`),
		"the seeded rows did not reach the branch")
	require.InDelta(t, 1.0,
		scan[float64](t, conn, `SELECT (embedding <-> '[0,0,0]')::float8 FROM documents WHERE id = 1`),
		0.0001, "the vector distance operator did not answer, so the type is not really there")
}

// TestAPreloadedLibraryIsAddedToTheStatisticsModuleRatherThanReplacingIt is
// the half of extension support CREATE EXTENSION cannot do.
//
// It asserts both directions on purpose, in separate assertions, because the
// two failures are opposite and one of them is silent. A provider that ignores
// the declared library fails loudly at CREATE EXTENSION timescaledb. A
// provider that REPLACES the list with it starts perfectly, and the only
// symptom is that every environment it makes reports statement timing as
// unavailable, which is the failure the statistics module was preloaded for in
// the first place.
func TestAPreloadedLibraryIsAddedToTheStatisticsModuleRatherThanReplacingIt(t *testing.T) {
	requireImage(t, timescaleImage)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	e := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 47700,
		Image:            timescaleImage,
		PreloadLibraries: []string{"timescaledb"},
		Extensions:       []string{"timescaledb"},
		SeedSQL: `CREATE TABLE readings (taken timestamptz not null, sensor int, value double precision);
			SELECT create_hypertable('readings', by_range('taken'));
			INSERT INTO readings SELECT now() - (g || ' hours')::interval, g, g * 1.5
			FROM generate_series(1, 10) g;`,
	})

	gv := e.refresh(ctx, provider.GoldenSpec{Version: 17, RulesHash: "timescale1", Provenance: "extensions-timescale"})
	b := e.branch(ctx, gv.ID, "env_timescale00001")
	conn := e.open(ctx, b)

	preload := scan[string](t, conn, `SHOW shared_preload_libraries`)
	require.Contains(t, preload, "timescaledb",
		"the declared library did not reach the branch's postmaster")
	require.Contains(t, preload, "pg_stat_statements",
		"the declared library REPLACED the statistics module, which is how every environment "+
			"this provider makes loses statement timing while looking healthy")

	// Preloaded is not the same as working, for either of them.
	require.True(t, scan[bool](t, conn,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')`),
		"the extension was not created in the branch")
	require.Equal(t, int64(10), scan[int64](t, conn, `SELECT count(*) FROM readings`),
		"the hypertable's rows did not reach the branch")

	// Preloaded is not the same as working for the statistics module either,
	// and reading it is the only way to tell the two apart. The provider
	// preloads the library and does NOT create the extension, because the
	// insights create it when they need it, so the create comes first here.
	//
	// The point is what the SELECT after it does. A server that did not
	// preload the library accepts the CREATE EXTENSION and then answers that
	// select with SQLSTATE 55000, "pg_stat_statements must be loaded via
	// shared_preload_libraries". So this fails loudly in exactly the case the
	// assertion above is about, and it is the only assertion here that could
	// not be satisfied by a name in a catalog.
	_, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`)
	require.NoError(t, err, "the statistics extension could not be created in the branch")
	require.Positive(t, scan[int64](t, conn, `SELECT count(*) FROM pg_stat_statements`),
		"the statistics view answered nothing, so the module is not really loaded: adding the "+
			"declared library displaced it, which is the silent half of this failure")
}

// TestABranchCarriesTheLibraryItsGoldenWasBuiltWithEvenWhenTheManifestStops
// is the "a golden that has an extension and a branch that does not" case,
// which is worse than neither.
//
// The second provider is built with NO preload at all, which stands for the
// manifest having changed between the refresh and the branch: a line removed,
// a different checkout, a rollback. The golden is unchanged and still holds an
// extension whose library the postmaster loads before any database is opened,
// so a branch started without it does not start degraded, it does not start.
func TestABranchCarriesTheLibraryItsGoldenWasBuiltWithEvenWhenTheManifestStops(t *testing.T) {
	requireImage(t, timescaleImage)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	built := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 47800,
		Image:            timescaleImage,
		PreloadLibraries: []string{"timescaledb"},
		Extensions:       []string{"timescaledb"},
		SeedSQL:          `CREATE TABLE kept (id int primary key); INSERT INTO kept VALUES (1), (2);`,
	})
	gv := built.refresh(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "timescale2", Provenance: "extensions-timescale-forgotten",
	})

	// The manifest has stopped asking. Same daemon, same golden, no preload.
	forgetful := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 47850, Image: timescaleImage,
	})
	b := forgetful.branch(ctx, gv.ID, "env_timescale00002")
	conn := forgetful.open(ctx, b)

	require.Contains(t, scan[string](t, conn, `SHOW shared_preload_libraries`), "timescaledb",
		"the branch was started without the library its golden was built with, which is a "+
			"container that cannot come up out of a golden that is perfectly good")
	require.Equal(t, int64(2), scan[int64](t, conn, `SELECT count(*) FROM kept`),
		"the golden's rows did not reach a branch taken by a provider that declared nothing")
}

// columnarSeed is a schema with one table on a custom access method and one on
// the heap beside it.
//
// The heap table is not decoration. Every assertion about the columnar table
// has to be read against a table in the same database that is ordinary, or a
// golden that lost EVERYTHING would pass the ones about survival by accident.
const columnarSeed = `
CREATE TABLE ordinary (id int primary key, note text);
INSERT INTO ordinary VALUES (1, 'on the heap'), (2, 'also on the heap');
CREATE TABLE measurements (id int, sensor text, reading double precision) USING af_demo;
INSERT INTO measurements
SELECT g, 'sensor-' || g, g * 1.25 FROM generate_series(1, 7) g;`

// TestACustomTableAccessMethodSurvivesTheGoldenAndTheBranch is the storage
// engine question asked directly.
//
// The far side is queried through pg_am and pg_class.relam rather than by
// reading rows back, because rows coming back would also be the answer for a
// table that had silently been rewritten onto the heap by a copy that dropped
// the access method. A custom access method that survives as a heap table is
// not a custom access method that survived.
func TestACustomTableAccessMethodSurvivesTheGoldenAndTheBranch(t *testing.T) {
	requireCustomAccessMethodImage(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	e := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 47900,
		Image:      customAMImage,
		Extensions: []string{"af_demo_am"},
		SeedSQL:    columnarSeed,
	})

	gv := e.refresh(ctx, provider.GoldenSpec{Version: 17, RulesHash: "columnar1", Provenance: "extensions-columnar"})
	b := e.branch(ctx, gv.ID, "env_columnar000001")
	conn := e.open(ctx, b)

	// The access method exists on the branch as a table access method.
	require.True(t, scan[bool](t, conn,
		`SELECT EXISTS (SELECT 1 FROM pg_am WHERE amname = 'af_demo' AND amtype = 't')`),
		"the branch has no custom table access method, so nothing stored in one could be read")

	// The TABLE is still stored in it. This is the assertion the whole test is
	// for: relam, read on the far side of a commit and a container start.
	require.Equal(t, customAMName, scan[string](t, conn,
		`SELECT am.amname FROM pg_class c JOIN pg_am am ON am.oid = c.relam
		 WHERE c.relname = 'measurements'`),
		"the table came back on a different access method than it was created with")
	require.Equal(t, "heap", scan[string](t, conn,
		`SELECT am.amname FROM pg_class c JOIN pg_am am ON am.oid = c.relam
		 WHERE c.relname = 'ordinary'`),
		"the ordinary table beside it is the control and it is not on the heap either")

	require.Equal(t, int64(7), scan[int64](t, conn, `SELECT count(*) FROM measurements`),
		"the rows in the custom access method did not reach the branch")
	require.InDelta(t, 8.75,
		scan[float64](t, conn, `SELECT reading FROM measurements WHERE id = 7`), 0.0001,
		"a row came back with the wrong value, so the storage round trip is not faithful")
}

// TestACustomTableAccessMethodSurvivesPgDumpAndPgRestore is the other half,
// and it is a different mechanism rather than a second run of the same one.
//
// A golden built from a SEED is SQL this test wrote, replayed. A golden built
// from a SOURCE is pg_dump piped into pg_restore, which is the path every real
// project takes and the one where an access method can be dropped without
// anybody noticing: the rows arrive, the table is there, and it is on the
// heap. So the first golden is the source of the second, and the second's
// branch is where relam is read.
func TestACustomTableAccessMethodSurvivesPgDumpAndPgRestore(t *testing.T) {
	requireCustomAccessMethodImage(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	origin := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 48000,
		Image:      customAMImage,
		Extensions: []string{"af_demo_am"},
		SeedSQL:    columnarSeed,
	})
	seeded := origin.refresh(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "columnar2", Provenance: "extensions-columnar-source",
	})
	source := origin.branch(ctx, seeded.ID, "env_columnar000002")

	// A second provider, copying from the first branch as though it were
	// production. Same image, because an image that cannot create the access
	// method is the AF-DB-007 case rather than this one.
	copied := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 48100,
		Image:      customAMImage,
		Extensions: []string{"af_demo_am"},
	})
	gv := copied.refresh(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "columnar3", Provenance: "extensions-columnar-copied",
		SourceURL: origin.connString(ctx, source),
	})
	b := copied.branch(ctx, gv.ID, "env_columnar000003")
	conn := copied.open(ctx, b)

	require.Equal(t, customAMName, scan[string](t, conn,
		`SELECT am.amname FROM pg_class c JOIN pg_am am ON am.oid = c.relam
		 WHERE c.relname = 'measurements'`),
		"pg_dump and pg_restore carried the table and dropped its access method, which is the "+
			"silent version of this failure: the rows are all there and the storage is not")
	require.Equal(t, int64(7), scan[int64](t, conn, `SELECT count(*) FROM measurements`),
		"the copy did not carry the rows out of the custom access method")
	require.Equal(t, int64(2), scan[int64](t, conn, `SELECT count(*) FROM ordinary`),
		"the ordinary table beside it did not survive the copy either, so this says nothing about the access method")
}

// TestMaskingRefusesToRewriteATableOnACustomAccessMethod is the negative arm,
// and it is the one that matters most.
//
// Masking addresses a row either by its primary key or, failing that, by ctid,
// and BOTH of those are heap guarantees. Against citus columnar on 17.2 both
// are refused outright, which is the measurement the refusal is designed
// against, and a masking run that discovers that partway through a table
// leaves data neither real nor safe. So the catalog reads relam and the plan
// refuses before anything is written.
//
// The fixture here is the built access method rather than that one, and the
// refusal is keyed on "not the heap" rather than on what a particular engine
// implements, so the same rule is what is being measured. That the refusal is
// conservative for an access method which would in fact have accepted the
// rewrite is the deliberate choice, and the second half of this test is what
// keeps it survivable.
//
// The refusal is narrow, and the second half of this test is what proves that:
// a column on the same custom access method that masking would NOT rewrite
// goes through untouched, which is what lets a golden carry such a table at
// all.
func TestMaskingRefusesToRewriteATableOnACustomAccessMethod(t *testing.T) {
	requireCustomAccessMethodImage(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	e := newExtensionProvider(t, dockerdb.Options{
		Version: 17, PortFrom: 48200,
		Image:      customAMImage,
		Extensions: []string{"af_demo_am"},
		SeedSQL: `
			CREATE TABLE people (id int primary key, email text);
			INSERT INTO people VALUES (1, 'real.one@example.com'), (2, 'real.two@example.com');
			CREATE TABLE archived_people (id int, email text) USING af_demo;
			INSERT INTO archived_people VALUES (3, 'real.three@example.com');`,
	})
	gv := e.refresh(ctx, provider.GoldenSpec{Version: 17, RulesHash: "columnar4", Provenance: "extensions-columnar-mask"})
	b := e.branch(ctx, gv.ID, "env_columnar000004")
	conn := e.open(ctx, b)

	tables, err := masking.ReadCatalog(ctx, conn)
	require.NoError(t, err)

	byName := map[string]masking.Table{}
	for _, tbl := range tables {
		byName[tbl.Name] = tbl
	}

	// The catalog reads the access method off the server. Without this the
	// refusal below could not exist, because nothing would know.
	require.Equal(t, "heap", byName["people"].AccessMethod,
		"an ordinary table must report the heap, or the refusal would catch every table there is")
	require.Equal(t, customAMName, byName["archived_people"].AccessMethod,
		"the catalog did not read the custom access method, so masking cannot know to refuse")

	// The whole assignment pass, through the real rule set, so that the
	// refusal is measured where it actually happens rather than by calling the
	// dialect directly.
	// The built in rule set, which is what a project with no masking.yaml
	// gets and therefore the one an email column is classified by.
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	problems := map[string]string{}
	for _, a := range rules.Assign(tables) {
		if a.Problem != "" {
			problems[a.Table.Name+"."+a.Column.Name] = a.Problem
		}
	}

	require.Contains(t, problems, "archived_people.email",
		"masking planned a rewrite of a column in a custom access method; against this one it "+
			"would fail partway through and leave the table neither real nor safe")
	require.Contains(t, problems["archived_people.email"], customAMName,
		"the refusal does not name the access method, so nobody reading it can tell what to do")
	require.NotContains(t, problems, "people.email",
		"the refusal reached an ordinary table, which would stop every masking run there is")

	// And the narrowness: a column nothing rewrites is not refused, whatever
	// it is stored in. The id here is an integer nobody has a rule for, on the
	// same columnar table.
	require.NotContains(t, problems, "archived_people.id",
		"a column masking would not write was refused anyway, which would make a custom access "+
			"method impossible to carry at all rather than impossible to rewrite")
}

// TestAnExtensionTheImageDoesNotCarryIsRefusedByName is the other negative
// arm: asking for something that is not there.
//
// The stock image is the one every project starts on, and naming an extension
// it does not have is the most likely way to get this wrong. The refusal has
// to name the extension AND the image, because "could not create extension" on
// its own sends somebody to look at their SQL rather than at their image.
func TestAnExtensionTheImageDoesNotCarryIsRefusedByName(t *testing.T) {
	requireImage(t, "postgres:17-alpine")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	p, err := dockerdb.New(dockerdb.Options{
		Version: 17, PortFrom: 48300, Clock: clock.New(),
		// No image, so the stock one. It carries the contrib modules and
		// nothing else, and vector is not one of them.
		Extensions: []string{"vector"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	_, err = p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "absent1", Provenance: "extensions-absent",
		Mask:   func(context.Context, secrets.Value) error { return nil },
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"ok":true}`, nil },
	})
	require.Error(t, err, "a golden was published in an image missing the extension the manifest declared")
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB040)),
		"the refusal did not carry the code whose remedy is about the image")
	require.Contains(t, err.Error(), "vector", "the refusal does not name the extension")
	require.Contains(t, err.Error(), "postgres:17-alpine", "the refusal does not name the image")
}

// TestAnImageOnADifferentMajorThanTheManifestDeclaresIsRefused.
//
// Naming your own image moves the choice of Postgres version out of the
// manifest and into a tag, and the two disagreeing is neither rare nor
// visible: everything downstream works, and every branch runs a Postgres the
// application does not. Checked against the running server rather than against
// the tag, because a tag is a string somebody chose.
func TestAnImageOnADifferentMajorThanTheManifestDeclaresIsRefused(t *testing.T) {
	requireImage(t, pgvectorImage)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	p, err := dockerdb.New(dockerdb.Options{
		Version: 16, PortFrom: 48400, Clock: clock.New(), Image: pgvectorImage,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	_, err = p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 16, RulesHash: "major1", Provenance: "extensions-major",
		Mask:   func(context.Context, secrets.Value) error { return nil },
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"ok":true}`, nil },
	})
	require.Error(t, err, "a golden was published on a major the manifest did not declare")
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB039)),
		"the refusal did not carry the code whose remedy is about the version")
	require.Contains(t, err.Error(), "17", "the refusal does not say what the image actually runs")
	require.Contains(t, err.Error(), "16", "the refusal does not say what the manifest declared")
}

// TestAnImageThatDeclaresAVolumeOverTheDataDirectoryIsRefused.
//
// This is the failure the dataDir constant was written to close, re-opened by
// letting somebody name their own image. A golden is `docker commit` of a
// container and commit captures the writable layer, so anything written under
// a path the image declares as a VOLUME is not in it. The candidate starts,
// the copy succeeds, the masking succeeds, the verification succeeds, the
// commit succeeds, and every branch is an immaculate empty Postgres.
//
// The image is built here rather than pulled, because no published image
// declares a volume over a path this project invented, and an unbuildable
// case is one nobody would ever check.
func TestAnImageThatDeclaresAVolumeOverTheDataDirectoryIsRefused(t *testing.T) {
	requireImage(t, "postgres:17-alpine")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	name := fmt.Sprintf("af-volume-image-%d", time.Now().UnixNano()%1e9)
	// Created and never started. A commit reads the container's configuration
	// and its writable layer, and this one needs neither to run: the Changes
	// below are what put the VOLUME on the resulting image.
	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  "postgres:17-alpine",
			Labels: dockerutil.Managed("db-test", name, time.Now()),
			Cmd:    []string{"true"},
		},
		Name: name,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		clean, cancelClean := context.WithTimeout(context.Background(), time.Minute)
		defer cancelClean()
		_ = dockerutil.RemoveContainer(clean, cli, created.ID)
	})

	const tag = "antifailure/test-volume-over-pgdata:latest"
	_, err = cli.ContainerCommit(ctx, created.ID, client.ContainerCommitOptions{
		Reference: tag,
		Comment:   "an image that swallows the data directory",
		Changes:   []string{"VOLUME /var/lib/antifailure"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		clean, cancelClean := context.WithTimeout(context.Background(), time.Minute)
		defer cancelClean()
		_, _ = cli.ImageRemove(clean, tag, client.ImageRemoveOptions{PruneChildren: true})
	})

	p, err := dockerdb.New(dockerdb.Options{
		Version: 17, PortFrom: 48500, Clock: clock.New(), Image: tag,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	_, err = p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "volume1", Provenance: "extensions-volume",
		Mask:   func(context.Context, secrets.Value) error { return nil },
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"ok":true}`, nil },
	})
	require.Error(t, err, "a golden was published from an image whose declared volume swallows the data directory")
	require.True(t, errors.Is(err, aferrors.Coded(aferrors.AFDB038)),
		"the refusal did not carry the code whose remedy is about the volume")
	require.Contains(t, err.Error(), "/var/lib/antifailure",
		"the refusal does not name the declared volume")
	// The parent path is what was declared and the data directory is what is
	// inside it, so a check comparing the two for equality would miss this.
	require.Contains(t, err.Error(), "/var/lib/antifailure/pgdata",
		"the refusal does not name the data directory the volume swallows")
	require.True(t, strings.Contains(err.Error(), tag), "the refusal does not name the image")
}

// TestASubsetLoadsIntoACustomTableAccessMethod is the third way rows move.
//
// Golden creation replays SQL, a copy shells out to pg_dump and pg_restore,
// and a SUBSET does neither: the schema arrives through pg_dump --schema-only
// and the rows arrive through `COPY ... TO STDOUT (FORMAT BINARY)` piped into
// `COPY ... FROM STDIN (FORMAT BINARY)`. Binary COPY is a wire format an
// access method has to accept on its own terms, so a subset into a custom one
// is a different claim from a restore into it and is proved separately.
//
// The relationship is VIRTUAL rather than a foreign key, and that is not a
// convenience. What constraints an access method accepts is that engine's own
// business, so a test that depended on one would be measuring the engine
// rather than the subset. A declared relationship is the product's own answer
// for a link the schema does not enforce, and it follows identically.
func TestASubsetLoadsIntoACustomTableAccessMethod(t *testing.T) {
	requireCustomAccessMethodImage(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	opts := dockerdb.Options{
		Version: 17, PortFrom: 48600,
		Image:      customAMImage,
		Extensions: []string{"af_demo_am"},
		SeedSQL: `
			CREATE TABLE tenants (id int primary key, name text);
			INSERT INTO tenants VALUES (1, 'keep'), (2, 'drop');
			CREATE TABLE measurements (id int, tenant_id int, reading double precision) USING af_demo;
			INSERT INTO measurements
			SELECT g, CASE WHEN g <= 4 THEN 1 ELSE 2 END, g * 1.25 FROM generate_series(1, 8) g;`,
	}
	origin := newExtensionProvider(t, opts)
	seeded := origin.refresh(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "columnar5", Provenance: "extensions-columnar-subset-source",
	})
	source := origin.branch(ctx, seeded.ID, "env_columnar000005")

	// An empty candidate of the same shape, which is what the engine hands a
	// subset: the provider makes the container, pgcopy puts the schema in it,
	// and the subset puts a slice of the rows in.
	targetOpts := opts
	targetOpts.PortFrom = 48700
	targetOpts.SeedSQL = ""
	target := newExtensionProvider(t, targetOpts)
	blank := target.refresh(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "columnar6", Provenance: "extensions-columnar-subset-target",
	})
	into := target.branch(ctx, blank.ID, "env_columnar000006")

	sourceURL := origin.connString(ctx, source)
	targetURL := target.connString(ctx, into)
	require.NoError(t, pgcopy.CopySchema(ctx, sourceURL, targetURL),
		"the schema could not be copied into the candidate, so there is nothing to subset into")

	src, err := pgx.Connect(ctx, sourceURL.Reveal())
	require.NoError(t, err)
	defer func() { _ = src.Close(context.Background()) }()

	cat, err := subset.ReadCatalog(ctx, src)
	require.NoError(t, err)
	plan, err := subset.Build(cat, subset.Config{
		SeedTable:        "public.tenants",
		SeedWhere:        "id = 1",
		FollowDependents: 1,
		Virtual: []subset.ForeignKey{{
			From: "public.measurements", FromColumns: []string{"tenant_id"},
			To: "public.tenants", ToColumns: []string{"id"},
		}},
	})
	require.NoError(t, err)

	_, err = subset.Execute(ctx, subset.Options{
		SourceURL: sourceURL.Reveal(), TargetURL: targetURL.Reveal(), Plan: plan,
	})
	require.NoError(t, err, "the subset could not load a table stored in a custom access method")

	conn := target.open(ctx, into)
	require.Equal(t, customAMName, scan[string](t, conn,
		`SELECT am.amname FROM pg_class c JOIN pg_am am ON am.oid = c.relam
		 WHERE c.relname = 'measurements'`),
		"the schema copy put the table on a different access method than the source has")
	// Four of the eight, because the subset took one tenant. A count of eight
	// would mean the narrowing did not happen and a count of zero would mean
	// the load did not, and neither is this passing.
	require.Equal(t, int64(4), scan[int64](t, conn, `SELECT count(*) FROM measurements`),
		"the rows the subset selected did not arrive in the custom access method")
	require.Equal(t, int64(1), scan[int64](t, conn, `SELECT count(*) FROM tenants`),
		"the heap table beside it did not get the seed row, so this says nothing about the subset")
}
