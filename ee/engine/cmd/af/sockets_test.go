// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package main_test

// The proof that the engine's extension sockets can be implemented from
// outside the engine module, made by a module that is outside it.
//
// It is here rather than in the engine's own test suite because the engine's
// suite cannot make it. Inside engine/... every internal package is
// importable, so a socket whose signature names a type from engine/internal
// compiles there and is unimplementable everywhere else, which is exactly the
// defect engine/api/packages.txt records against provider.Database: its
// ConnString returned a type from engine/internal/secrets, it compiled, it
// reviewed as correct, and no provider outside this repository could have
// implemented it. tools/socketcheck looks for that in the syntax tree. This
// file is the compiler saying the same thing, from the only place that can
// say it: a separate module, which is what every provider anybody writes will
// also be.
//
// The assertions below are almost beside the point. The compile is the test.

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// outsideDatabase registers a database provider from this module.
type outsideDatabase struct{ seen extension.DatabaseConfig }

func (*outsideDatabase) Name() string { return "outside" }

func (p *outsideDatabase) Open(
	ctx context.Context, cfg extension.DatabaseConfig,
) (provider.Database, error) {
	p.seen = cfg
	// Every field a provider is given can be named and used here, which is the
	// half of the socket a signature check cannot prove: a credential is read
	// through the engine's chain rather than out of the environment, and the
	// manifest block arrives whole.
	if cfg.Lookup != nil && cfg.Database.APIKeyEnv != "" {
		if _, _, err := cfg.Lookup(ctx, cfg.Database.APIKeyEnv); err != nil {
			return nil, err
		}
	}
	_ = cfg.Now()
	return nil, errors.New("outside: this fake builds no database")
}

// outsideDatastore registers a datastore provider from this module.
type outsideDatastore struct{}

func (outsideDatastore) Name() string   { return "outside-events" }
func (outsideDatastore) Engine() string { return "clickhouse" }
func (outsideDatastore) Open(
	context.Context, extension.DatastoreConfig,
) (extension.Datastore, error) {
	return nil, extension.ErrNoGolden
}

// outsideRuntime registers a runtime from this module.
type outsideRuntime struct{ seen extension.RuntimeConfig }

func (*outsideRuntime) Name() string { return "outside-runtime" }

func (p *outsideRuntime) Open(
	ctx context.Context, cfg extension.RuntimeConfig,
) (provider.Runtime, error) {
	p.seen = cfg
	// A runtime that places containers off this machine cannot build the
	// egress sidecar and must not run without one, so the engine hands it the
	// resolver rather than an image. Naming it here is the check that it can
	// be called from outside the module at all.
	if cfg.ResolveProxyImage == nil {
		return nil, errors.New("outside: no sidecar image is obtainable")
	}
	_ = cfg.TTL
	return nil, errors.New("outside: this fake places nothing")
}

// outsideStore registers a golden store from this module.
type outsideStore struct{}

func (outsideStore) Name() string { return "outside-bucket" }

func (outsideStore) Open(cfg extension.ObjectStoreConfig) (extension.ObjectStore, error) {
	return &outsideObjects{url: cfg.URL, objects: map[string][]byte{}}, nil
}

type outsideObjects struct {
	url     string
	objects map[string][]byte
}

func (o *outsideObjects) Name() string { return "outside-bucket at " + o.url }

func (o *outsideObjects) Put(_ context.Context, name string, _ int64, body io.Reader) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	o.objects[name] = b
	return nil
}

func (o *outsideObjects) Get(_ context.Context, name string) (io.ReadCloser, error) {
	if _, ok := o.objects[name]; !ok {
		// The sentinel this module can name. A store that could not name one
		// would have to invent an error, and every absent golden would read
		// as a broken store.
		return nil, extension.ErrObjectNotFound
	}
	return io.NopCloser(newReader(o.objects[name])), nil
}

func (o *outsideObjects) List(context.Context, string) ([]extension.StoredObject, error) {
	out := make([]extension.StoredObject, 0, len(o.objects))
	for name, b := range o.objects {
		out = append(out, extension.StoredObject{
			Name: name, Size: int64(len(b)), Modified: time.Unix(0, 0).UTC(),
		})
	}
	return out, nil
}

func (o *outsideObjects) Delete(_ context.Context, name string) error {
	delete(o.objects, name)
	return nil
}

func newReader(b []byte) io.Reader { return &sliceReader{b: b} }

type sliceReader struct {
	b []byte
	n int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.n >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.n:])
	r.n += n
	return n, nil
}

// outsideEmulator registers an emulator from this module.
type outsideEmulator struct{}

func (outsideEmulator) Name() string    { return "outside-s3" }
func (outsideEmulator) Hosts() []string { return []string{"s3.amazonaws.com"} }
func (outsideEmulator) Container() extension.EmulatorContainer {
	return extension.EmulatorContainer{
		Image: "localstack/localstack@sha256:" +
			"0000000000000000000000000000000000000000000000000000000000000000",
		Port: 4566,
	}
}

func TestEverySocketCanBeImplementedFromOutsideTheEngineModule(t *testing.T) {
	t.Parallel()

	db := &outsideDatabase{}
	rt := &outsideRuntime{}

	r := extension.NewRegistry()
	r.AddDatabaseProvider(db)
	r.AddDatastoreProvider(outsideDatastore{})
	r.AddRuntimeProvider(rt)
	r.AddGoldenStore(outsideStore{})
	r.AddEmulator(outsideEmulator{})

	require.Equal(t, []string{
		"database-provider:outside",
		"datastore-provider:outside-events",
		"emulator:outside-s3",
		"golden-store:outside-bucket",
		"runtime:outside-runtime",
	}, r.Registered())
	require.NoError(t, r.Validate(map[string][]string{
		extension.SocketDatabaseProvider: {"docker", "neon", "supabase", "dblab"},
		extension.SocketRuntimeProvider:  {"local", "kubernetes"},
		extension.SocketGoldenStore:      {"local", "azure_blob", "s3", "gcs"},
	}))

	found, ok := r.DatabaseProviderNamed("outside")
	require.True(t, ok)
	require.Equal(t, "outside", found.Name())

	// The store round trips through the interface the engine holds, including
	// the sentinel, from this module.
	store, err := outsideStore{}.Open(extension.ObjectStoreConfig{URL: "outside://bucket"})
	require.NoError(t, err)
	require.NoError(t, store.Put(context.Background(), "gv_1.sql", 3, newReader([]byte("abc"))))
	body, err := store.Get(context.Background(), "gv_1.sql")
	require.NoError(t, err)
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "abc", string(got))

	_, err = store.Get(context.Background(), "absent")
	require.ErrorIs(t, err, extension.ErrObjectNotFound)

	// And a value the engine would hand back is nameable here too, which is
	// the property that was broken in provider.Database once already.
	var value secret.Value = secret.New("postgres://example")
	require.Equal(t, secret.Redacted, value.String())
}

func TestARegistrationThisBuildCannotHonorIsRefused(t *testing.T) {
	t.Parallel()
	// The refusal a build gets for its own mistake, checked from the module
	// that would make it. Registering under a name the engine already has
	// would never be selected, because the engine asks the registry only after
	// its own switch.
	r := extension.NewRegistry()
	r.AddGoldenStore(shadowingStore{})

	err := r.Validate(map[string][]string{extension.SocketGoldenStore: {"local", "s3"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot replace")
}

type shadowingStore struct{}

func (shadowingStore) Name() string { return "s3" }
func (shadowingStore) Open(extension.ObjectStoreConfig) (extension.ObjectStore, error) {
	return nil, errors.New("never reached")
}
