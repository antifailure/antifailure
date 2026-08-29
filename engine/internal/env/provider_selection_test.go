package env

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dblabdb "github.com/antifailure/antifailure/engine/internal/db/dblab"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	neondb "github.com/antifailure/antifailure/engine/internal/db/neon"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
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
	for _, kind := range []schema.DBProvider{schema.DBSupabase, "invented"} {
		_, err := orchestrator(t, &schema.Database{Provider: kind}, nil).
			newDatabaseProvider(context.Background())
		require.Error(t, err, "%s was accepted", kind)
		require.ErrorIs(t, err, aferrors.Coded(aferrors.AFMAN002))
		require.Contains(t, err.Error(), string(kind))
	}
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

	cloud, err := orchestrator(t, &schema.Database{
		Provider: schema.DBNeon, Project: "p", APIKeyEnv: "K",
	}, map[string]string{"K": "napi_x"}).newDatabaseProvider(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = cloud.Close() })
	_, ok = cloud.(attachable)
	require.False(t, ok, "a cloud provider must not be attachable; there is nothing to attach")
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

	_, err = o.newRuntime()
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
	rt, err := o.newRuntime()
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
		r, err := o.newRuntime()
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
