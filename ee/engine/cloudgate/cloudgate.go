// Package cloudgate is the licence check on the managed cloud providers.
//
// Not MIT. This directory is covered by the Antifailure Enterprise License; see
// ee/LICENSE.md.
//
// Two licensed features live here, and the editions rule is what decides that
// they are licensed at all:
//
//	cloud_database   a managed cloud Postgres: Aurora, Cloud SQL, RDS
//	cloud_runtime    a managed cloud runtime: ECS, Cloud Run, Container Apps
//
// The rule is that a provider is licensed when it needs an ORGANIZATION to
// exist. One developer with their own account and their own card gets MIT, and
// every provider in that class is already built into the engine: docker, neon,
// supabase, dblab and pgurl for databases, local and kubernetes for runtimes.
// A managed cloud provider needs an IAM role somebody with authority has to
// grant, a billing account somebody signed for, and usually a network somebody
// else runs.
//
// WHAT THIS WRAPS, AND WHY THAT IS THE WHOLE DEFINITION. Every MIT provider is
// BUILT IN and is reached by the engine's own switch, which never consults the
// registry. So a database or runtime provider that arrives through
// extension.Registry is, by the editions rule, one that needed an organization.
// That makes "registered" the definition of "cloud" rather than a list of
// vendor names kept in step by hand, and it means a cloud provider lane can
// register Aurora or ECS without editing this package and without being able to
// forget to.
//
// GATED PER CALL, NEVER AT REGISTRATION. A licence can expire while the process
// is running. Gating at registration would mean a control plane that started
// before a renewal never enforces again until somebody restarts it, and a
// control plane that started under a valid licence keeps a feature nobody is
// paying for. So the wrapper is installed unconditionally and every gated
// method asks the licence carried on the CONTEXT of that call. This is the rule
// ee/engine/cmd/af/main.go already states for the policy hook, applied here.
//
// WHAT IS GATED AND WHAT IS NOT, which is the design decision in this package.
// The gate refuses anything that CREATES a cloud resource, and never refuses
// anything that removes, enumerates or reports one:
//
//	refused    RefreshGolden, Branch, Up
//	always     DestroyGolden, Destroy, Down, Inventory, Status, ListGoldens,
//	           Health, ConnString, Reset, Capabilities, Name, Close
//
// Refusing a teardown would turn a lapsed licence into an orphaned Aurora
// cluster the customer keeps paying for, which is a worse outcome for them than
// the feature they lost and a worse outcome for us than not gating at all. The
// leak detector needs Inventory for the same reason. Reset is not a creation:
// it returns a branch that already exists to a state it already had.
package cloudgate

import (
	"context"
	"fmt"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

func init() {
	// Recorded so a feature that is sold and checked nowhere shows up as such.
	// See ee/engine/feature.
	feature.Declare(license.FeatureCloudDatabase, "cloudgate/cloudgate.go:gatedDatabase.Branch")
	feature.Declare(license.FeatureCloudRuntime, "cloudgate/cloudgate.go:gatedRuntime.Up")
}

// Refusal is a licensed provider used without the licence for it.
//
// A distinct type rather than a formatted string, so that a caller can tell a
// licensing refusal from a provider that is down. They have completely
// different next steps and the same shape at a terminal.
type Refusal struct {
	// Feature is the entitlement that was missing.
	Feature license.Feature
	// Provider is the provider that was refused, by the name a manifest uses.
	Provider string
	// Reason says which of the licence states this was, in words an
	// administrator can act on.
	Reason string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("AF-EE-012: the provider %s needs the %s feature: %s",
		r.Provider, r.Feature, r.Reason)
}

// Wrap puts the licence gate in front of every registered database and runtime
// provider in a registry.
//
// It returns the number of providers wrapped, because a caller that wraps
// nothing and a caller that never ran look identical otherwise, and this is the
// enterprise binary's only evidence that the gate is installed.
//
// Called unconditionally and BEFORE anything can open a provider. Every
// registration that arrives later is unwrapped, which is why the enterprise
// binary calls this after its own registrations and why a provider registered
// by something else afterwards is a gap worth knowing about rather than one to
// paper over here: this package cannot see a registration it was not present
// for, and pretending otherwise would be a gate that silently covers less than
// it claims.
func Wrap(reg *extension.Registry) int {
	if reg == nil {
		return 0
	}
	wrapped := 0
	for _, name := range reg.DatabaseProviderNames() {
		p, ok := reg.DatabaseProviderNamed(name)
		if !ok {
			continue
		}
		reg.ReplaceDatabaseProvider(&gatedDatabaseProvider{inner: p})
		wrapped++
	}
	for _, name := range reg.RuntimeProviderNames() {
		p, ok := reg.RuntimeProviderNamed(name)
		if !ok {
			continue
		}
		reg.ReplaceRuntimeProvider(&gatedRuntimeProvider{inner: p})
		wrapped++
	}
	return wrapped
}

// refuse builds the error for a call the licence does not permit.
//
// The QUESTION is asked at each call site rather than here, with the feature
// constant written out, so that the file names the feature it gates. A helper
// taking the feature as a parameter reads fine and leaves the source with no
// occurrence of feature.Enabled(ctx, license.FeatureCloudDatabase) anywhere in
// it, which is a site a reader cannot find by grep and a check cannot match
// against what was declared.
func refuse(ctx context.Context, f license.Feature, providerName string) error {
	return &Refusal{Feature: f, Provider: providerName, Reason: reason(ctx, f)}
}

// reason renders the licence state as a sentence somebody can act on.
//
// The order of the answers is the order somebody needs them. A licence that has
// lapsed is reported as a licence and not as a missing entitlement, because an
// administrator whose renewal is late wants to be told that rather than being
// told to buy something they already bought.
func reason(ctx context.Context, f license.Feature) string {
	status := feature.StatusFrom(ctx)
	switch status.State {
	case license.StateNone:
		return "no licence is installed"
	case license.StateExpired:
		return "the licence expired on " +
			status.Claims.ExpiresAt.UTC().Format("2 January 2006") +
			" and its grace period has ended"
	case license.StateRevoked:
		return "the licence has been revoked"
	case license.StateWrongOrg:
		return "the installed licence was issued to " + status.Claims.Org
	case license.StateClockRollback:
		return "this machine's clock reads earlier than the licence was last checked at"
	default:
		return "this licence does not include " + string(f)
	}
}

// ---------------------------------------------------------------------------
// Databases.

type gatedDatabaseProvider struct{ inner extension.DatabaseProvider }

func (g *gatedDatabaseProvider) Name() string { return g.inner.Name() }

// Open is NOT gated, and that is deliberate rather than an omission.
//
// Teardown, inventory and status all need an opened provider, and refusing to
// open one under a lapsed licence would mean a customer whose renewal is late
// cannot remove the cluster they are being billed for. The gate is on the
// methods that create.
func (g *gatedDatabaseProvider) Open(
	ctx context.Context, cfg extension.DatabaseConfig,
) (provider.Database, error) {
	inner, err := g.inner.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if inner == nil {
		// The engine reports a nil provider with a nil error rather than
		// dereferencing it, and a wrapper that hid one inside a non-nil
		// interface would take that check away.
		return nil, nil
	}
	return &gatedDatabase{inner: inner, name: g.inner.Name()}, nil
}

type gatedDatabase struct {
	inner provider.Database
	name  string
}

func (g *gatedDatabase) Name() string                { return g.inner.Name() }
func (g *gatedDatabase) Capabilities() provider.Caps { return g.inner.Capabilities() }

func (g *gatedDatabase) RefreshGolden(
	ctx context.Context, spec provider.GoldenSpec,
) (provider.GoldenVersion, error) {
	if !feature.Enabled(ctx, license.FeatureCloudDatabase) {
		return provider.GoldenVersion{}, refuse(ctx, license.FeatureCloudDatabase, g.name)
	}
	return g.inner.RefreshGolden(ctx, spec)
}

func (g *gatedDatabase) Branch(
	ctx context.Context, version string, envID string,
) (provider.Branch, error) {
	if !feature.Enabled(ctx, license.FeatureCloudDatabase) {
		return provider.Branch{}, refuse(ctx, license.FeatureCloudDatabase, g.name)
	}
	return g.inner.Branch(ctx, version, envID)
}

// Everything below removes, enumerates or reports, and is never refused.

func (g *gatedDatabase) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	return g.inner.ListGoldens(ctx)
}

func (g *gatedDatabase) DestroyGolden(ctx context.Context, version string) error {
	return g.inner.DestroyGolden(ctx, version)
}

func (g *gatedDatabase) Reset(ctx context.Context, b provider.Branch) error {
	return g.inner.Reset(ctx, b)
}

func (g *gatedDatabase) Destroy(ctx context.Context, b provider.Branch) error {
	return g.inner.Destroy(ctx, b)
}

func (g *gatedDatabase) ConnString(
	ctx context.Context, b provider.Branch, mode provider.ConnMode,
) (secret.Value, error) {
	return g.inner.ConnString(ctx, b, mode)
}

func (g *gatedDatabase) Inventory(ctx context.Context) ([]provider.Resource, error) {
	return g.inner.Inventory(ctx)
}

func (g *gatedDatabase) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	return g.inner.Health(ctx, b)
}

func (g *gatedDatabase) Close() error { return g.inner.Close() }

// ---------------------------------------------------------------------------
// Runtimes.

type gatedRuntimeProvider struct{ inner extension.RuntimeProvider }

func (g *gatedRuntimeProvider) Name() string { return g.inner.Name() }

func (g *gatedRuntimeProvider) Open(
	ctx context.Context, cfg extension.RuntimeConfig,
) (provider.Runtime, error) {
	inner, err := g.inner.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if inner == nil {
		return nil, nil
	}
	return &gatedRuntime{inner: inner, name: g.inner.Name()}, nil
}

type gatedRuntime struct {
	inner provider.Runtime
	name  string
}

func (g *gatedRuntime) Name() string                       { return g.inner.Name() }
func (g *gatedRuntime) Capabilities() provider.RuntimeCaps { return g.inner.Capabilities() }

func (g *gatedRuntime) Up(ctx context.Context, spec provider.EnvSpec) (provider.Env, error) {
	if !feature.Enabled(ctx, license.FeatureCloudRuntime) {
		return provider.Env{}, refuse(ctx, license.FeatureCloudRuntime, g.name)
	}
	return g.inner.Up(ctx, spec)
}

func (g *gatedRuntime) Down(ctx context.Context, envID string) (provider.Teardown, error) {
	return g.inner.Down(ctx, envID)
}

func (g *gatedRuntime) Status(ctx context.Context, envID string) (provider.Env, error) {
	return g.inner.Status(ctx, envID)
}

func (g *gatedRuntime) Inventory(ctx context.Context) ([]provider.Resource, error) {
	return g.inner.Inventory(ctx)
}

func (g *gatedRuntime) Close() error { return g.inner.Close() }
