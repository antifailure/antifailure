// A provider written outside the engine module, which is the thing the
// stability promise says is possible.
//
// This file has no assertions in it worth the name. Its whole value is that it
// COMPILES, from a module that is not the engine, and it lives here because the
// tools module is the only Go in this repository that is outside the engine and
// already depends on it. Everything the gate next door checks structurally is
// checked here by the compiler instead, on the real interfaces, in CI.
//
// Before this landed, the file below could not be written at all:
//
//	vet: outside_test.go:13:89: undefined: secrets
//
// because ConnString returned engine/internal/secrets.Value and there is no
// spelling of that type an outside package is allowed to use. Adding the import
// that would define it fails differently and for the same reason:
//
//	use of internal package github.com/antifailure/antifailure/engine/internal/secrets not allowed
//
// So provider.Database, the interface the release notes call an integration
// surface, could not be implemented from outside the repository, on a line the
// author had no way to write differently. The type moved to engine/pkg/secret
// and this compiles. If it ever stops compiling the promise has broken again,
// and a build failure here is a better way to learn that than a stranger's bug
// report.
package main

import (
	"context"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// outsideDatabase implements every method of provider.Database.
type outsideDatabase struct{}

func (outsideDatabase) Name() string { return "outside" }

func (outsideDatabase) Capabilities() provider.Caps { return provider.Caps{} }

func (outsideDatabase) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	// The two fields that carry a credential, named by an outside package.
	if err := spec.Mask(ctx, spec.SourceURL); err != nil {
		return provider.GoldenVersion{}, err
	}
	if _, err := spec.Verify(ctx, spec.SourceURL); err != nil {
		return provider.GoldenVersion{}, err
	}
	return provider.GoldenVersion{ID: provider.NewGoldenVersionID(time.Now(), "00000000")}, nil
}

func (outsideDatabase) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	return nil, nil
}

func (outsideDatabase) DestroyGolden(ctx context.Context, version string) error { return nil }

func (outsideDatabase) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	return provider.Branch{EnvID: envID}, nil
}

func (outsideDatabase) Reset(ctx context.Context, b provider.Branch) error { return nil }

func (outsideDatabase) Destroy(ctx context.Context, b provider.Branch) error { return nil }

// The method that could not be written before. The return type is named, which
// is what an implementation has no way to avoid doing.
func (outsideDatabase) ConnString(ctx context.Context, b provider.Branch, mode provider.ConnMode) (secret.Value, error) {
	return secret.New("postgres://outside/db"), nil
}

func (outsideDatabase) Inventory(ctx context.Context) ([]provider.Resource, error) { return nil, nil }

func (outsideDatabase) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	return provider.Health{}, nil
}

func (outsideDatabase) Close() error { return nil }

// outsideRuntime implements every method of provider.Runtime.
type outsideRuntime struct{}

func (outsideRuntime) Name() string { return "outside" }

func (outsideRuntime) Capabilities() provider.RuntimeCaps { return provider.RuntimeCaps{} }

func (outsideRuntime) Up(ctx context.Context, spec provider.EnvSpec) (provider.Env, error) {
	// EnvSpec carries several credentials, and building one is what a caller
	// of a runtime does rather than an implementer, so both directions are
	// exercised.
	_ = provider.EnvSpec{
		DatabaseURL:          secret.New("postgres://outside/app"),
		MigrationDatabaseURL: secret.New("postgres://outside/admin"),
		CAKeyPEM:             secret.New("-----BEGIN PRIVATE KEY-----"),
		SandboxCredentials:   map[string]secret.Value{"token": secret.New("t")},
		Services: []provider.ServiceSpec{{
			Name: "web",
			Env:  map[string]secret.Value{"API_KEY": secret.New("k")},
		}},
	}
	return provider.Env{EnvID: spec.EnvID}, nil
}

func (outsideRuntime) Down(ctx context.Context, envID string) (provider.Teardown, error) {
	return provider.Teardown{}, nil
}

func (outsideRuntime) Status(ctx context.Context, envID string) (provider.Env, error) {
	return provider.Env{EnvID: envID}, nil
}

func (outsideRuntime) Inventory(ctx context.Context) ([]provider.Resource, error) { return nil, nil }

func (outsideRuntime) Close() error { return nil }

// The assertions the compiler makes. If either interface grows a method or
// changes one, this stops building, which is what an implementer outside this
// repository experiences on the release that does it.
var (
	_ provider.Database = outsideDatabase{}
	_ provider.Runtime  = outsideRuntime{}
	_ provider.Factory  = func(ctx context.Context, cfg provider.Config) (provider.Database, error) {
		// Config.Secret hands back a credential, so a provider that reads one
		// has to name the type too.
		if resolve := cfg.Secret; resolve != nil {
			var value secret.Value
			value, _ = resolve("dsn")
			_ = value
		}
		return outsideDatabase{}, nil
	}
	// The conformance suite is stable alongside the interfaces, and it is the
	// thing a provider author actually runs. Naming its entry points from
	// outside is the rest of the promise.
	_ conformance.Factory        = func(t *testing.T) provider.Database { return outsideDatabase{} }
	_ conformance.RuntimeFactory = func(t *testing.T) provider.Runtime { return outsideRuntime{} }
	// engine/pkg/schema is stable because the provider interfaces carry it.
	_ = schema.Manifest{}
)

// TestAnOutsideProviderIsRunnable does the little that is left once the
// compiler has done the work above: the suite can be described from out here,
// and the credential type behaves the way its own package promises.
func TestAnOutsideProviderIsRunnable(t *testing.T) {
	if len(conformance.Behaviors()) == 0 || len(conformance.RuntimeBehaviors()) == 0 {
		t.Fatal("the conformance suite describes no behaviors, so running it would prove nothing")
	}
	value, err := outsideDatabase{}.ConnString(t.Context(), provider.Branch{}, provider.ConnPooled)
	if err != nil {
		t.Fatal(err)
	}
	if value.Reveal() != "postgres://outside/db" {
		t.Fatalf("the credential did not survive the boundary: %q", value.Reveal())
	}
	if value.String() != secret.Redacted {
		t.Fatalf("a credential printed itself as %q from outside the engine module", value.String())
	}
}
