package env

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dblabdb "github.com/antifailure/antifailure/engine/internal/db/dblab"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	neondb "github.com/antifailure/antifailure/engine/internal/db/neon"
	supabasedb "github.com/antifailure/antifailure/engine/internal/db/supabase"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The manifest names a provider and the engine has to build that one.
//
// Without these, the constants in the schema are a list of intentions: a
// manifest could say neon, pass validation, and silently get Docker. Nobody
// would see it until they wondered why their preview had no production data in
// it. An in-package test because the selection is deliberately not exported;
// what is exported is the manifest field that drives it.

func orchestrator(t *testing.T, db *schema.Database, env map[string]string) *Orchestrator {
	t.Helper()
	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app", Database: db},
		Branch:   "main",
		Clock:    clock.New(),
		Secrets: secrets.NewChain(secrets.NewCISource(func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		})),
	})
	require.NoError(t, err)
	return o
}

func TestAManifestAskingForNeonGetsNeon(t *testing.T) {
	p, err := orchestrator(t, &schema.Database{
		Provider: schema.DBNeon, Project: "proj-1", APIKeyEnv: "MY_NEON_KEY",
	}, map[string]string{"MY_NEON_KEY": "napi_whatever"}).newDatabaseProvider(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.Equal(t, "neon", p.Name())
	require.IsType(t, &neondb.Provider{}, p)
}

func TestTheKeyVariableDefaultsToTheOneNeonDocuments(t *testing.T) {
	p, err := orchestrator(t, &schema.Database{
		Provider: schema.DBNeon, Project: "proj-1",
	}, map[string]string{"NEON_API_KEY": "napi_whatever"}).newDatabaseProvider(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.Equal(t, "neon", p.Name())
}

func TestNeonWithoutAProjectIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	_, err := orchestrator(t, &schema.Database{
		Provider: schema.DBNeon,
	}, map[string]string{"NEON_API_KEY": "napi_whatever"}).newDatabaseProvider(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN002))
	require.Contains(t, err.Error(), "database.project")
}

func TestNeonWithoutAKeySaysWhichVariableIsMissing(t *testing.T) {
	// The name is in the message because "a secret is missing" is not
	// actionable and "MY_NEON_KEY is missing" is.
	_, err := orchestrator(t, &schema.Database{
		Provider: schema.DBNeon, Project: "proj-1", APIKeyEnv: "MY_NEON_KEY",
	}, nil).newDatabaseProvider(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFSEC001))
	require.Contains(t, err.Error(), "MY_NEON_KEY")
}

func TestAProviderThisBuildDoesNotHaveIsRefusedRatherThanSubstituted(t *testing.T) {
	// The failure this prevents is the quiet one. Falling back to Docker gives
	// somebody an empty preview and no reason for it.
	// Both Supabase and DBLab are built after this merge, so each side's
	// listing of the other as absent is now wrong. What is left is a provider
	// that genuinely does not exist.
	for _, kind := range []schema.DBProvider{"invented"} {
		_, err := orchestrator(t, &schema.Database{Provider: kind}, nil).
			newDatabaseProvider(context.Background())
		require.Error(t, err, "%s was accepted", kind)
		require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN002))
		require.Contains(t, err.Error(), string(kind))
	}
}

func TestAManifestAskingForSupabaseGetsSupabase(t *testing.T) {
	p, err := orchestrator(t, &schema.Database{
		Provider: schema.DBSupabase, Project: "abcdefghijklmnopqrst", APIKeyEnv: "MY_SUPABASE_TOKEN",
	}, map[string]string{"MY_SUPABASE_TOKEN": "sbp_whatever"}).newDatabaseProvider(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.Equal(t, "supabase", p.Name())
	require.IsType(t, &supabasedb.Provider{}, p)
}

func TestTheSupabaseTokenVariableDefaultsToTheOneSupabaseDocuments(t *testing.T) {
	p, err := orchestrator(t, &schema.Database{
		Provider: schema.DBSupabase, Project: "abcdefghijklmnopqrst",
	}, map[string]string{"SUPABASE_ACCESS_TOKEN": "sbp_whatever"}).
		newDatabaseProvider(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.Equal(t, "supabase", p.Name())
}

func TestSupabaseWithoutAProjectIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	// A Supabase branch is a running project billed by the hour, so a manifest
	// that cannot say which project to create one in has to fail before the
	// first API call rather than after it.
	_, err := orchestrator(t, &schema.Database{
		Provider: schema.DBSupabase,
	}, map[string]string{"SUPABASE_ACCESS_TOKEN": "sbp_whatever"}).
		newDatabaseProvider(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN002))
	require.Contains(t, err.Error(), "database.project")
}

func TestSupabaseWithoutATokenSaysWhichVariableIsMissing(t *testing.T) {
	_, err := orchestrator(t, &schema.Database{
		Provider: schema.DBSupabase, Project: "abcdefghijklmnopqrst", APIKeyEnv: "MY_SUPABASE_TOKEN",
	}, nil).newDatabaseProvider(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFSEC001))
	require.Contains(t, err.Error(), "MY_SUPABASE_TOKEN")
}

func TestAManifestAskingForDBLabGetsDBLab(t *testing.T) {
	p, err := orchestrator(t, &schema.Database{
		Provider: schema.DBDBLab, Project: "http://dblab.internal:2345", APIKeyEnv: "MY_DBLAB_TOKEN",
	}, map[string]string{"MY_DBLAB_TOKEN": "whatever"}).newDatabaseProvider(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.Equal(t, "dblab", p.Name())
	require.IsType(t, &dblabdb.Provider{}, p)
}

func TestTheDBLabTokenVariableDefaultsToTheOneTheEngineDocuments(t *testing.T) {
	p, err := orchestrator(t, &schema.Database{
		Provider: schema.DBDBLab, Project: "http://dblab.internal:2345",
	}, map[string]string{"DBLAB_VERIFICATION_TOKEN": "whatever"}).newDatabaseProvider(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	require.Equal(t, "dblab", p.Name())
}

func TestDBLabWithoutAnEndpointIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	// A Database Lab Engine is self hosted, so unlike a hosted provider there
	// is no account to enumerate and nothing to fall back to. A default would
	// be a guess at somebody else's infrastructure.
	_, err := orchestrator(t, &schema.Database{
		Provider: schema.DBDBLab,
	}, map[string]string{"DBLAB_VERIFICATION_TOKEN": "whatever"}).newDatabaseProvider(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN002))
	require.Contains(t, err.Error(), "database.project")
}

func TestDBLabWithoutATokenIsRefusedRatherThanRunningWithoutOne(t *testing.T) {
	// The engine itself will run with no verification token, and an engine
	// with no verification token is one that anybody who can reach the port
	// can create clones of production data on. Defaulting to empty here would
	// make that the quiet path.
	_, err := orchestrator(t, &schema.Database{
		Provider: schema.DBDBLab, Project: "http://dblab.internal:2345", APIKeyEnv: "MY_DBLAB_TOKEN",
	}, nil).newDatabaseProvider(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFSEC001))
	require.Contains(t, err.Error(), "MY_DBLAB_TOKEN")
}

func TestAnEmptyProviderIsDockerAndTheVersionFollowsTheManifest(t *testing.T) {
	for _, db := range []*schema.Database{nil, {}, {Provider: schema.DBDocker, Version: 16}} {
		p, err := orchestrator(t, db, nil).newDatabaseProvider(context.Background())
		require.NoError(t, err)
		require.Equal(t, "docker", p.Name())
		require.IsType(t, &dockerdb.Provider{}, p)
		_ = p.Close()
	}
}

func TestOnlyALocalProviderIsAttachable(t *testing.T) {
	// The runtime attaches a database container to the environment's network.
	// A cloud provider has nothing to attach: its connection string already
	// works from inside a container. This is the distinction the runtime makes
	// at the type level, checked here so that adding a provider that gets it
	// wrong fails a test rather than an environment.
	type attachable interface {
		AttachToNetwork(ctx context.Context, ref, networkID, alias string) (int, error)
	}

	local, err := orchestrator(t, &schema.Database{Provider: schema.DBDocker}, nil).
		newDatabaseProvider(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = local.Close() })
	_, ok := local.(attachable)
	require.True(t, ok, "the Docker provider must be attachable; its branches are containers")

	for _, db := range []*schema.Database{
		{Provider: schema.DBNeon, Project: "p", APIKeyEnv: "K"},
		{Provider: schema.DBSupabase, Project: "p", APIKeyEnv: "K"},
	} {
		cloud, err := orchestrator(t, db, map[string]string{"K": "token_x"}).
			newDatabaseProvider(context.Background())
		require.NoError(t, err)
		t.Cleanup(func() { _ = cloud.Close() })
		_, ok = cloud.(attachable)
		require.False(t, ok,
			"%s is a cloud provider and must not be attachable; there is nothing to attach",
			db.Provider)
	}
}

func TestARuntimeThisBuildDoesNotHaveIsRefusedRatherThanSubstituted(t *testing.T) {
	// This used to name kubernetes, because this build did not have it. It
	// does now, so the test names something no build will ever have: the
	// property being checked is that an unrecognised runtime is REFUSED, not
	// that any particular one is missing. A repository configured for a
	// cluster that quietly got containers on whichever laptop ran af is a
	// difference nobody notices until they go looking for their environment
	// in the cluster.
	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app", Runtime: &schema.Runtime{Provider: "nomad"}},
		Branch:   "main",
		Clock:    clock.New(),
	})
	require.NoError(t, err)

	_, err = o.newRuntime(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN002))
	require.Contains(t, err.Error(), "nomad")
	require.Contains(t, err.Error(), "local", "the message does not say what to do instead")
	require.Contains(t, err.Error(), "kubernetes", "the message does not list the runtimes there are")
}

func TestTheKubernetesRuntimeIsBuiltRatherThanRefused(t *testing.T) {
	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app", Runtime: &schema.Runtime{Provider: schema.RuntimeKubernetes}},
		Branch:   "main",
		Clock:    clock.New(),
	})
	require.NoError(t, err)

	// Deliberately not asserting success. Whether a cluster is reachable from
	// the machine running this test is not something the test can arrange,
	// and a test that needed one would skip on most machines and prove
	// nothing on the rest. What IS asserted is the thing this build changed:
	// kubernetes is no longer refused as a runtime this build does not have.
	// Anything else it fails with is a fact about the machine.
	rt, err := o.newRuntime(context.Background())
	if err != nil {
		require.NotErrorIs(t, err, aferrors.Coded(aferrors.AFMAN002),
			"kubernetes was refused as a runtime this build does not have, and it has it")
		return
	}
	t.Cleanup(func() { _ = rt.Close() })
	require.Equal(t, "kubernetes", rt.Name())

	// Building the runtime must not have built the sidecar image. That is a
	// container build of a minute or more on a cold cache, and af status, af
	// logs and af down all come through here.
	require.False(t, rt.Capabilities().AttachesLocalDatabase,
		"a cluster cannot reach a database container on this machine")
}

func TestAnUnsetRuntimeIsLocal(t *testing.T) {
	for _, rt := range []*schema.Runtime{nil, {}, {Provider: schema.RuntimeLocal}} {
		o, err := New(Options{
			Root:     t.TempDir(),
			Manifest: &schema.Manifest{Name: "app", Runtime: rt},
			Branch:   "main",
			Clock:    clock.New(),
		})
		require.NoError(t, err)
		r, err := o.newRuntime(context.Background())
		if err != nil {
			// No Docker daemon on this machine is a different failure from the
			// manifest being refused, and only the second is under test here.
			require.NotErrorIs(t, err, aferrors.Coded(aferrors.AFMAN002))
			continue
		}
		require.NotNil(t, r)
		_ = r.Close()
	}
}

// ---------------------------------------------------------------------------
// Registered providers.
//
// Everything above this line is about the providers this build carries. These
// are about the ones it does not: a build outside this repository registers a
// provider through engine/pkg/extension, and the selection above has to reach
// it. Before this the switches ended at a default that refused, so the only
// way to add a provider was to edit this file.

func TestARegisteredDatabaseProviderIsBuiltWhenTheManifestNamesIt(t *testing.T) {
	db := newFakeDB("acmedb")
	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(&fakeDBProvider{name: "acmedb", db: db})

	o := orchestrator(t, &schema.Database{Provider: "acmedb"}, nil)
	o.opts.Extensions = reg

	p, err := o.newDatabaseProvider(context.Background())
	require.NoError(t, err)
	require.Equal(t, "acmedb", p.Name())
	require.Equal(t, 1, db.opened)
}

func TestARegisteredRuntimeIsBuiltWhenTheManifestNamesIt(t *testing.T) {
	rt := &fakeRT{name: "acmert"}
	reg := extension.NewRegistry()
	reg.AddRuntimeProvider(&fakeRTProvider{name: "acmert", rt: rt})

	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app", Runtime: &schema.Runtime{Provider: "acmert"}},
		Branch:   "main",
		Clock:    clock.New(),
	})
	require.NoError(t, err)
	o.opts.Extensions = reg

	built, err := o.newRuntime(context.Background())
	require.NoError(t, err)
	require.Equal(t, "acmert", built.Name())
}

func TestARefusalNamesTheRegisteredProvidersAsWellAsTheBuiltInOnes(t *testing.T) {
	// A build that registered a provider and then misspelled it in the
	// manifest used to be told the name was wrong by a message that did not
	// mention the provider the build has, which sends somebody looking for a
	// registration that is already there.
	// The name in the manifest is deliberately not a substring of the
	// registered one and does not contain it. A near miss like "acmedbb"
	// against "acmedb" passes this assertion from the quoted name alone, which
	// is how this test first passed against a message that listed nothing.
	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(&fakeDBProvider{name: "aurora", db: newFakeDB("aurora")})
	reg.AddRuntimeProvider(&fakeRTProvider{name: "nomad", rt: &fakeRT{name: "nomad"}})

	o := orchestrator(t, &schema.Database{Provider: "arora"}, nil)
	o.opts.Extensions = reg
	_, err := o.newDatabaseProvider(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN002))
	require.Contains(t, err.Error(), "aurora",
		"the refusal does not name the registered provider this build has")
	require.Contains(t, err.Error(), "docker")

	r, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app", Runtime: &schema.Runtime{Provider: "nmad"}},
		Branch:   "main",
		Clock:    clock.New(),
	})
	require.NoError(t, err)
	r.opts.Extensions = reg
	_, err = r.newRuntime(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "nomad",
		"the refusal does not name the registered runtime this build has")
	require.Contains(t, err.Error(), "kubernetes")
}

func TestARegistrationUnderABuiltInNameStopsTheCommandRatherThanBeingIgnored(t *testing.T) {
	// The switches consult the registry only in their default, so a provider
	// registered as "docker" would never be selected. Silently ignoring it is
	// how somebody ships a build they believe replaces the Docker provider.
	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(&fakeDBProvider{name: "docker", db: newFakeDB("docker")})

	o := orchestrator(t, &schema.Database{Provider: schema.DBDocker}, nil)
	o.opts.Extensions = reg

	_, err := o.open(context.Background(), "af up")
	require.Error(t, err)
	require.Contains(t, err.Error(), "docker")
	require.Contains(t, err.Error(), "cannot replace")
}

func TestAProviderThatReturnsNothingAndNoErrorIsReportedRatherThanDereferenced(t *testing.T) {
	// The same defect the comment above newDatabaseProvider's switch describes,
	// from the other side: a registration returning a nil interface would pass
	// the caller's nil guard and crash in Close, and af down would segfault
	// rather than say what was wrong.
	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(&nilProvider{name: "hollow"})

	o := orchestrator(t, &schema.Database{Provider: "hollow"}, nil)
	o.opts.Extensions = reg
	_, err := o.newDatabaseProvider(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "hollow")
}

type nilProvider struct{ name string }

func (p *nilProvider) Name() string { return p.name }
func (p *nilProvider) Open(
	context.Context, extension.DatabaseConfig,
) (provider.Database, error) {
	return nil, nil
}
