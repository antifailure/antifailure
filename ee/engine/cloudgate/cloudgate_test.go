// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudgate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/cloudgate"
	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// ---------------------------------------------------------------------------
// The fake, which exists to be reached or not reached.
//
// It records every method that ran, so a test can assert the difference
// between "the gate refused" and "the gate let it through and the provider
// failed", which are the same error at a terminal and completely different
// facts.

type recorder struct{ calls []string }

func (r *recorder) did(name string) { r.calls = append(r.calls, name) }

func (r *recorder) ran(name string) bool {
	for _, c := range r.calls {
		if c == name {
			return true
		}
	}
	return false
}

type fakeDatabase struct{ rec *recorder }

func (f *fakeDatabase) Name() string                { return "aurora" }
func (f *fakeDatabase) Capabilities() provider.Caps { return provider.Caps{Branching: true} }

func (f *fakeDatabase) RefreshGolden(
	context.Context, provider.GoldenSpec,
) (provider.GoldenVersion, error) {
	f.rec.did("RefreshGolden")
	return provider.GoldenVersion{ID: "gv_1", Verified: true}, nil
}

func (f *fakeDatabase) ListGoldens(context.Context) ([]provider.GoldenVersion, error) {
	f.rec.did("ListGoldens")
	return []provider.GoldenVersion{{ID: "gv_1", Verified: true}}, nil
}

func (f *fakeDatabase) DestroyGolden(context.Context, string) error {
	f.rec.did("DestroyGolden")
	return nil
}

func (f *fakeDatabase) Branch(_ context.Context, version, envID string) (provider.Branch, error) {
	f.rec.did("Branch")
	return provider.Branch{EnvID: envID, From: version}, nil
}

func (f *fakeDatabase) Reset(context.Context, provider.Branch) error {
	f.rec.did("Reset")
	return nil
}

func (f *fakeDatabase) Destroy(context.Context, provider.Branch) error {
	f.rec.did("Destroy")
	return nil
}

func (f *fakeDatabase) ConnString(
	context.Context, provider.Branch, provider.ConnMode,
) (secret.Value, error) {
	f.rec.did("ConnString")
	return secret.New("postgres://example"), nil
}

func (f *fakeDatabase) Inventory(context.Context) ([]provider.Resource, error) {
	f.rec.did("Inventory")
	return []provider.Resource{{ID: "cluster-1"}}, nil
}

func (f *fakeDatabase) Health(context.Context, provider.Branch) (provider.Health, error) {
	f.rec.did("Health")
	return provider.Health{}, nil
}

func (f *fakeDatabase) Close() error { f.rec.did("Close"); return nil }

type fakeDatabaseProvider struct{ rec *recorder }

func (f *fakeDatabaseProvider) Name() string { return "aurora" }

func (f *fakeDatabaseProvider) Open(
	context.Context, extension.DatabaseConfig,
) (provider.Database, error) {
	f.rec.did("Open")
	return &fakeDatabase{rec: f.rec}, nil
}

type fakeRuntime struct{ rec *recorder }

func (f *fakeRuntime) Name() string                       { return "ecs" }
func (f *fakeRuntime) Capabilities() provider.RuntimeCaps { return provider.RuntimeCaps{Logs: true} }

func (f *fakeRuntime) Up(context.Context, provider.EnvSpec) (provider.Env, error) {
	f.rec.did("Up")
	return provider.Env{EnvID: "env-1"}, nil
}

func (f *fakeRuntime) Down(context.Context, string) (provider.Teardown, error) {
	f.rec.did("Down")
	return provider.Teardown{}, nil
}

func (f *fakeRuntime) Status(context.Context, string) (provider.Env, error) {
	f.rec.did("Status")
	return provider.Env{EnvID: "env-1"}, nil
}

func (f *fakeRuntime) Inventory(context.Context) ([]provider.Resource, error) {
	f.rec.did("Inventory")
	return []provider.Resource{{ID: "service-1"}}, nil
}

func (f *fakeRuntime) Close() error { f.rec.did("Close"); return nil }

type fakeRuntimeProvider struct{ rec *recorder }

func (f *fakeRuntimeProvider) Name() string { return "ecs" }

func (f *fakeRuntimeProvider) Open(
	context.Context, extension.RuntimeConfig,
) (provider.Runtime, error) {
	f.rec.did("Open")
	return &fakeRuntime{rec: f.rec}, nil
}

// ---------------------------------------------------------------------------

func licensed(features ...license.Feature) context.Context {
	v := license.NewVerifier(nil)
	status := v.Evaluate(license.Claims{
		ID: "l", Org: "acme", Features: features,
		ExpiresAt: time.Now().AddDate(1, 0, 0),
	}, license.Evaluation{Org: "acme", Now: time.Now()})
	return feature.With(context.Background(), status)
}

func expired() context.Context {
	v := license.NewVerifier(nil)
	status := v.Evaluate(license.Claims{
		ID: "l", Org: "acme",
		Features:  []license.Feature{license.FeatureCloudDatabase, license.FeatureCloudRuntime},
		ExpiresAt: time.Now().AddDate(-2, 0, 0),
	}, license.Evaluation{Org: "acme", Now: time.Now()})
	return feature.With(context.Background(), status)
}

func openedDatabase(t *testing.T, ctx context.Context, rec *recorder) provider.Database {
	t.Helper()
	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(&fakeDatabaseProvider{rec: rec})
	require.Equal(t, 1, cloudgate.Wrap(reg))

	p, ok := reg.DatabaseProviderNamed("aurora")
	require.True(t, ok, "wrapping removed the provider instead of replacing it")
	db, err := p.Open(ctx, extension.DatabaseConfig{})
	require.NoError(t, err)
	require.NotNil(t, db)
	return db
}

func openedRuntime(t *testing.T, ctx context.Context, rec *recorder) provider.Runtime {
	t.Helper()
	reg := extension.NewRegistry()
	reg.AddRuntimeProvider(&fakeRuntimeProvider{rec: rec})
	require.Equal(t, 1, cloudgate.Wrap(reg))

	p, ok := reg.RuntimeProviderNamed("ecs")
	require.True(t, ok)
	rt, err := p.Open(ctx, extension.RuntimeConfig{})
	require.NoError(t, err)
	require.NotNil(t, rt)
	return rt
}

func TestWrapReplacesTheRegistrationRatherThanAddingToIt(t *testing.T) {
	t.Parallel()
	// Two providers under one name are refused by Validate, and if they were
	// not, the engine would use whichever was registered first, which is the
	// ungated one. So the wrapper has to take the registration's place.
	rec := &recorder{}
	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(&fakeDatabaseProvider{rec: rec})
	reg.AddRuntimeProvider(&fakeRuntimeProvider{rec: rec})

	require.Equal(t, 2, cloudgate.Wrap(reg))
	require.Equal(t, []string{"aurora"}, reg.DatabaseProviderNames())
	require.Equal(t, []string{"ecs"}, reg.RuntimeProviderNames())
	require.NoError(t, reg.Validate(nil))

	// And the name a manifest uses is unchanged, because a manifest naming
	// aurora has to keep finding aurora.
	p, ok := reg.DatabaseProviderNamed("aurora")
	require.True(t, ok)
	require.Equal(t, "aurora", p.Name())
}

func TestAnUnlicensedInstallationCannotCreateAndCanAlwaysRemove(t *testing.T) {
	t.Parallel()
	// The whole design of this package in one test. Nothing is created, and
	// every teardown, enumeration and report still works, because a lapsed
	// licence that stopped somebody removing an Aurora cluster would leave
	// them paying for it.
	rec := &recorder{}
	ctx := context.Background() // no licence at all
	db := openedDatabase(t, ctx, rec)

	_, err := db.RefreshGolden(ctx, provider.GoldenSpec{})
	require.Error(t, err)
	require.False(t, rec.ran("RefreshGolden"), "the refusal let the call through anyway")

	_, err = db.Branch(ctx, "gv_1", "env-1")
	require.Error(t, err)
	require.False(t, rec.ran("Branch"))

	// A refusal names the provider, the feature and which licence state this
	// was. "Not licensed" with no state sends an administrator whose renewal
	// is two days late to buy something they already bought.
	var refusal *cloudgate.Refusal
	require.True(t, errors.As(err, &refusal))
	require.Equal(t, "aurora", refusal.Provider)
	require.Equal(t, license.FeatureCloudDatabase, refusal.Feature)
	require.Contains(t, err.Error(), "AF-EE-012")
	require.Contains(t, err.Error(), "no licence is installed")

	// Everything that removes, enumerates or reports is never refused.
	require.NoError(t, db.Destroy(ctx, provider.Branch{EnvID: "env-1"}))
	require.True(t, rec.ran("Destroy"), "teardown was refused, which orphans a cloud resource")

	require.NoError(t, db.DestroyGolden(ctx, "gv_1"))
	require.True(t, rec.ran("DestroyGolden"))

	inventory, err := db.Inventory(ctx)
	require.NoError(t, err)
	require.Len(t, inventory, 1, "the leak detector cannot compare what it cannot enumerate")

	_, err = db.ListGoldens(ctx)
	require.NoError(t, err)
	require.NoError(t, db.Reset(ctx, provider.Branch{EnvID: "env-1"}))
	_, err = db.ConnString(ctx, provider.Branch{EnvID: "env-1"}, provider.ConnDirect)
	require.NoError(t, err)
	_, err = db.Health(ctx, provider.Branch{EnvID: "env-1"})
	require.NoError(t, err)
	require.NoError(t, db.Close())
}

func TestALicensedInstallationReachesTheProvider(t *testing.T) {
	t.Parallel()
	// The other half, and the one a gate that refuses everything would fail.
	// A check that cannot say yes is switched off in a day.
	rec := &recorder{}
	ctx := licensed(license.FeatureCloudDatabase)
	db := openedDatabase(t, ctx, rec)

	version, err := db.RefreshGolden(ctx, provider.GoldenSpec{})
	require.NoError(t, err)
	require.Equal(t, "gv_1", version.ID)
	require.True(t, rec.ran("RefreshGolden"))

	branch, err := db.Branch(ctx, "gv_1", "env-1")
	require.NoError(t, err)
	require.Equal(t, "env-1", branch.EnvID)
	require.True(t, rec.ran("Branch"))
}

func TestOneFeatureDoesNotGrantTheOther(t *testing.T) {
	t.Parallel()
	// cloud_database and cloud_runtime are sold separately, so a licence for
	// one must not open the other. Two features that always travel together
	// are one feature with two names.
	rec := &recorder{}
	ctx := licensed(license.FeatureCloudDatabase)

	rt := openedRuntime(t, ctx, rec)
	_, err := rt.Up(ctx, provider.EnvSpec{EnvID: "env-1"})
	require.Error(t, err)
	require.False(t, rec.ran("Up"))
	require.Contains(t, err.Error(), "cloud_runtime")

	// And the reverse.
	rec2 := &recorder{}
	db := openedDatabase(t, licensed(license.FeatureCloudRuntime), rec2)
	_, err = db.Branch(licensed(license.FeatureCloudRuntime), "gv_1", "env-1")
	require.Error(t, err)
	require.False(t, rec2.ran("Branch"))
	require.Contains(t, err.Error(), "cloud_database")
}

func TestARuntimeIsGatedOnUpAndNeverOnDown(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	ctx := context.Background()
	rt := openedRuntime(t, ctx, rec)

	_, err := rt.Up(ctx, provider.EnvSpec{EnvID: "env-1"})
	require.Error(t, err)
	require.False(t, rec.ran("Up"))
	require.Contains(t, err.Error(), "AF-EE-012")

	_, err = rt.Down(ctx, "env-1")
	require.NoError(t, err)
	require.True(t, rec.ran("Down"), "teardown was refused, which orphans a cloud resource")

	_, err = rt.Status(ctx, "env-1")
	require.NoError(t, err)
	_, err = rt.Inventory(ctx)
	require.NoError(t, err)
	require.NoError(t, rt.Close())
	require.Equal(t, provider.RuntimeCaps{Logs: true}, rt.Capabilities())
	require.Equal(t, "ecs", rt.Name())
}

func TestTheLicenceIsAskedPerCallAndNotAtRegistration(t *testing.T) {
	t.Parallel()
	// The property the whole package's structure exists for. One wrapped
	// provider, opened once, answers differently for two contexts, so a
	// licence that lapses while the process is running stops enforcement
	// without a restart and a licence that renews starts it again.
	rec := &recorder{}
	reg := extension.NewRegistry()
	reg.AddDatabaseProvider(&fakeDatabaseProvider{rec: rec})
	require.Equal(t, 1, cloudgate.Wrap(reg))
	p, ok := reg.DatabaseProviderNamed("aurora")
	require.True(t, ok)

	// Opened under a valid licence, which is the case gating at registration
	// would have decided the answer for.
	db, err := p.Open(licensed(license.FeatureCloudDatabase), extension.DatabaseConfig{})
	require.NoError(t, err)

	_, err = db.Branch(licensed(license.FeatureCloudDatabase), "gv_1", "env-1")
	require.NoError(t, err, "a valid licence on the call was refused")

	_, err = db.Branch(expired(), "gv_1", "env-2")
	require.Error(t, err, "the same provider kept the licence it was opened with")
	require.Contains(t, err.Error(), "expired on")

	_, err = db.Branch(licensed(license.FeatureCloudDatabase), "gv_1", "env-3")
	require.NoError(t, err, "the refusal was sticky, so a renewal would need a restart")
}

func TestWrapAddsNothingForANameThatIsNotRegistered(t *testing.T) {
	t.Parallel()
	// An empty registry is the community case and the ordinary enterprise one
	// until a cloud provider lane registers something. Wrapping it must be a
	// no-op rather than the creation of a provider a manifest could name.
	reg := extension.NewRegistry()
	require.Equal(t, 0, cloudgate.Wrap(reg))
	require.Empty(t, reg.DatabaseProviderNames())
	require.Empty(t, reg.RuntimeProviderNames())
	require.True(t, reg.Empty())

	require.False(t, reg.ReplaceDatabaseProvider(&fakeDatabaseProvider{rec: &recorder{}}),
		"replacing a name nobody registered added it, so a typo in a decorator "+
			"becomes a provider nobody wrote")
	require.Empty(t, reg.DatabaseProviderNames())

	require.False(t, reg.ReplaceRuntimeProvider(&fakeRuntimeProvider{rec: &recorder{}}))
	require.Empty(t, reg.RuntimeProviderNames())

	require.Equal(t, 0, cloudgate.Wrap(nil))
}

func TestBothFeaturesHaveADeclaredEnforcementSite(t *testing.T) {
	t.Parallel()
	// A feature a licence can sell and nothing checks is a feature that is
	// silently free. air_gapped was exactly that for long enough to become a
	// lane, and these two must not join it.
	require.NotEmpty(t, feature.Sites(license.FeatureCloudDatabase))
	require.NotEmpty(t, feature.Sites(license.FeatureCloudRuntime))
	require.Contains(t, feature.Declared(), license.FeatureCloudDatabase)
	require.Contains(t, feature.Declared(), license.FeatureCloudRuntime)
}
