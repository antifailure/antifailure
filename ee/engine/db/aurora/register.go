// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora

// The registration, which is the difference between a provider that exists and
// a provider that runs.
//
// The engine chooses a database provider in a switch whose default consults the
// extension registry, so an enterprise provider needs no line in engine/. What
// it does need is somebody to put it in the registry, and that somebody is
// ee/engine/cmd/af/main.go. A provider written, tested, and never registered is
// the shippable gap that file's own header was written about: the pieces are
// all there and the behaviour is absent.

import (
	"context"
	"fmt"

	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// Registration is the socket implementation. It holds nothing: everything the
// provider needs arrives in the DatabaseConfig the engine hands to Open.
type Registration struct{}

// Register adds the Aurora provider to a registry.
//
// Unconditional rather than gated on a licence, which is the same decision the
// policy hook registration made and for the same reason. Selecting this
// provider is a manifest saying database.provider is aurora, and a build whose
// licence lapsed between starting and being used should refuse at the point of
// use with a sentence about the licence, not disappear from the list of
// providers this build has and produce "which this build does not have".
func Register(r *extension.Registry) { r.AddDatabaseProvider(Registration{}) }

func (Registration) Name() string { return Name }

// Open builds the provider from the manifest and the engine's secret chain.
//
// Every value comes through cfg, and nothing here reads the process
// environment. That is what makes each credential this provider uses declared
// and auditable, and it is not cosmetic: the AWS credential chain inside
// cloudauth is given cfg.Lookup wrapped as a Getenv, so even AWS_ACCESS_KEY_ID
// resolves through the same chain as everything else and appears in the same
// audit trail.
func (Registration) Open(ctx context.Context, cfg extension.DatabaseConfig) (provider.Database, error) {
	db := cfg.Database

	if db.Project == "" {
		return nil, fmt.Errorf(
			"database.provider is aurora and database.project is empty; it is the Aurora " +
				"PostgreSQL DB CLUSTER identifier that goldens are cloned from, such as " +
				"acme-production. It is not an instance identifier and not an endpoint " +
				"hostname")
	}

	lookup := stringLookup(ctx, cfg.Lookup)

	region := lookup(RegionVariable)
	if region == "" {
		return nil, fmt.Errorf(
			"database.provider is aurora and %s resolved to nothing. A cluster is "+
				"regional, and asking the wrong region reports that the cluster does not "+
				"exist rather than that the region is wrong", RegionVariable)
	}

	variable := db.APIKeyEnv
	if variable == "" {
		variable = DefaultVariable
	}
	key, found, err := cfg.Lookup(ctx, variable)
	if err != nil {
		return nil, err
	}
	if !found || key.IsZero() {
		return nil, fmt.Errorf(
			"database.provider is aurora and %s resolved to nothing. It is not the source "+
				"cluster's password: this provider derives a distinct master password for "+
				"every clone from it, so that a preview environment never holds "+
				"production's database credential. Any high entropy string will do, and "+
				"changing it changes every branch's password", variable)
	}

	return New(ctx, Options{
		SourceCluster: db.Project,
		Region:        region,
		BranchKey:     key,
		Variable:      variable,
		Endpoint:      lookup(EndpointVariable),
		InstanceClass: lookup(InstanceClassVariable),
		TLSMode:       lookup(TLSModeVariable),
		MaxBranches:   db.MaxBranches,
		Getenv:        lookup,
		Now:           cfg.Now,
	})
}

// stringLookup adapts the engine's credential chain to the plain Getenv shape
// the AWS credential chain and the optional settings both want.
//
// A lookup that fails and a variable that is absent both answer the empty
// string here, and that is right for these callers rather than lax: every one
// of them treats an empty value as "not configured" and has its own refusal
// with a better sentence than a chain error would carry. The one value whose
// absence must be a hard refusal, the branch key, is read through cfg.Lookup
// directly above so that its error survives.
func stringLookup(ctx context.Context, lookup func(context.Context, string) (secret.Value, bool, error)) func(string) string {
	return func(name string) string {
		if lookup == nil {
			return ""
		}
		value, found, err := lookup(ctx, name)
		if err != nil || !found {
			return ""
		}
		return value.Reveal()
	}
}

var _ extension.DatabaseProvider = Registration{}
