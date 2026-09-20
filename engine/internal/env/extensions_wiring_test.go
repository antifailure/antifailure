package env

// THE MANIFEST'S ANSWER TO "WHAT POSTGRES" HAS TO REACH THE CONTAINER.
//
// The docker provider now honours an image, a list of extensions to create and
// a list of libraries to preload, and every one of those is proved against a
// real server in engine/internal/db/docker/extensions_live_test.go. What that
// file cannot prove is the step before it: those tests construct
// dockerdb.Options by hand, so a manifest whose three keys never reached
// Options would leave every one of them green.
//
// That gap is the one this repository keeps finding. A field that validates,
// is documented, is reported by af explain and is assigned into nothing is the
// exact shape of health_timeout, and the counting gate that found that one
// asks whether a field is READ, not whether its value arrives. So these two
// go through newDatabaseProvider from a manifest and then ask a Postgres.
//
// Two tests rather than one because the two halves fail in opposite ways. An
// image or an extension that did not arrive is a golden built in the wrong
// Postgres, which the refusal below names. A preloaded library that did not
// arrive is a server that starts and is missing a module, which nothing would
// say out loud, so that one is read back off the running postmaster.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func requireDaemon(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped in short mode: this needs a Docker daemon")
	}
	cli, err := dockerutil.Client()
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	_ = cli.Close()
}

// TestTheManifestsImageAndItsExtensionsReachTheGolden.
//
// The extension is one no image has, so the refusal is the observation: it
// names the extension AND the image, and the image it names is the one the
// manifest asked for rather than the stock tag the version would have built.
// One error carries both halves, which is why this is one test.
func TestTheManifestsImageAndItsExtensionsReachTheGolden(t *testing.T) {
	requireDaemon(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	o := orchestrator(t, &schema.Database{
		Provider:   schema.DBDocker,
		Version:    17,
		Image:      "pgvector/pgvector:pg17",
		Extensions: []string{"af_not_a_real_extension"},
	}, nil)
	p, err := o.newDatabaseProvider(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.IsType(t, &dockerdb.Provider{}, p)

	_, err = p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "wiring1", Provenance: "env-extensions-wiring",
		Mask:   func(context.Context, secrets.Value) error { return nil },
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"ok":true}`, nil },
	})
	require.Error(t, err, "a golden was published with an extension the manifest declared and no server has")
	require.Contains(t, err.Error(), "af_not_a_real_extension",
		"database.extensions did not reach the provider, so the manifest's list is read by nothing")
	require.Contains(t, err.Error(), "pgvector/pgvector:pg17",
		"database.image did not reach the provider, so the golden was built in the stock image")
}

// TestTheManifestsPreloadedLibrariesReachTheRunningServer.
//
// auto_explain rather than timescaledb, because this test is about the wiring
// and not about the library: it ships in the stock image, so the whole run is
// one ordinary golden and one ordinary branch, and what the postmaster reports
// is the only thing being read. The library the manifest asked for has to be
// there AND the statistics module has to still be there, because adding one by
// replacing the other is the failure that leaves every environment reporting
// that statement timing is unavailable.
func TestTheManifestsPreloadedLibrariesReachTheRunningServer(t *testing.T) {
	requireDaemon(t)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	o := orchestrator(t, &schema.Database{
		Provider:         schema.DBDocker,
		Version:          17,
		PreloadLibraries: []string{"auto_explain"},
	}, nil)
	p, err := o.newDatabaseProvider(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "wiring2", Provenance: "env-preload-wiring",
		Mask:   func(context.Context, secrets.Value) error { return nil },
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"ok":true}`, nil },
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		clean, cancelClean := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancelClean()
		_ = p.DestroyGolden(clean, gv.ID)
	})

	b, err := p.Branch(ctx, gv.ID, "env_preloadwiring1")
	require.NoError(t, err)
	t.Cleanup(func() {
		clean, cancelClean := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancelClean()
		_ = p.Destroy(clean, b)
	})

	url, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	conn, err := pgx.Connect(ctx, url.Reveal())
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()

	var preload string
	require.NoError(t, conn.QueryRow(ctx, `SHOW shared_preload_libraries`).Scan(&preload))
	require.Contains(t, preload, "auto_explain",
		"database.preload_libraries did not reach the server, so the manifest's list is read by nothing")
	require.Contains(t, preload, "pg_stat_statements",
		"the declared library replaced the statistics module rather than being added to it")

	// The same list, read off the golden IMAGE rather than off the branch,
	// which is what a branch taken after the manifest changes will read
	// instead of the manifest. Asserted here because this is the cheapest
	// place it can be: the timescaledb test proves the behaviour and this
	// proves the record it depends on exists at all.
	cli, err := dockerutil.Client()
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()
	info, err := cli.ImageInspect(ctx, gv.ProviderRef)
	require.NoError(t, err)
	require.NotNil(t, info.Config)
	require.Equal(t, "auto_explain", info.Config.Labels[dockerutil.LabelPreload],
		"the golden does not record what it was built with, so a branch of it after the "+
			"manifest changes has nothing to fall back to")
}
