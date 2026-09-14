// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql

// The registration, which is the difference between a provider that exists and
// a provider that runs.
//
// The engine chooses a database provider in a switch whose default consults the
// extension registry, so an enterprise provider needs no line in engine/. What
// it does need is somebody to put it in the registry, and that somebody is
// ee/engine/cmd/af/main.go. A provider written, tested, and never registered is
// the shippable gap that file's own header was written about.
//
// The registration must also sit ABOVE the cloudgate.Wrap call in that file.
// Wrap replaces what is registered at the moment it runs, so a provider added
// below it is served with no licence check at all, and that mistake compiles,
// vets clean and keeps every symbol count at one.

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

// Register adds the Cloud SQL provider to a registry.
//
// Unconditional rather than gated on a licence, which is the decision aurora's
// registration made and for the same reason. Selecting this provider is a
// manifest saying database.provider is cloudsql, and a build whose licence
// lapsed between starting and being used should refuse at the point of use with
// a sentence about the licence, not disappear from the list of providers this
// build has and produce "which this build does not have".
func Register(r *extension.Registry) { r.AddDatabaseProvider(Registration{}) }

func (Registration) Name() string { return Name }

// Open builds the provider from the manifest and the engine's secret chain.
//
// Every value comes through cfg and nothing here reads the process
// environment, which is what makes each credential this provider uses declared
// and auditable.
func (Registration) Open(ctx context.Context, cfg extension.DatabaseConfig) (provider.Database, error) {
	db := cfg.Database

	if db.Project == "" {
		return nil, fmt.Errorf(
			"database.provider is cloudsql and database.project is empty; it is the Cloud " +
				"SQL INSTANCE that goldens are cloned from, such as acme-production. The " +
				"connection name project:region:instance is accepted too and the instance " +
				"is taken from it. It is not a database name and not an IP address")
	}

	lookup := stringLookup(ctx, cfg.Lookup)

	project := lookup(ProjectVariable)
	if project == "" {
		return nil, fmt.Errorf(
			"database.provider is cloudsql and %s resolved to nothing. Every Admin API "+
				"call names the project, and a Cloud SQL instance identifier is only "+
				"unique within one", ProjectVariable)
	}

	region := lookup(RegionVariable)
	if region == "" {
		return nil, fmt.Errorf(
			"database.provider is cloudsql and %s resolved to nothing. An instance is "+
				"regional, and asking the wrong region reports that the instance does not "+
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
			"database.provider is cloudsql and %s resolved to nothing. It is not the "+
				"source instance's password: this provider derives a distinct password "+
				"for every clone from it, so that a preview environment never holds "+
				"production's database credential. A clone carries the source's users "+
				"and passwords, so without this every branch would be reachable with "+
				"production's credential. Any high entropy string will do, and changing "+
				"it changes every branch's password", variable)
	}

	policy := GoldenStaysRunning
	switch raw := lookup(GoldenStopVariable); raw {
	case "", "0", "false":
		// The default, and the one that is known to work.
	case "1", "true":
		policy = GoldenIsStopped
	default:
		return nil, fmt.Errorf(
			"database.provider is cloudsql and %s is %q. It takes 1 or 0. Setting it "+
				"stops a published golden's compute, which is cheaper and which this "+
				"lane could NOT establish is clonable: Google's clone documentation does "+
				"not say whether a stopped instance can be cloned, in either direction",
			GoldenStopVariable, raw)
	}

	return New(ctx, Options{
		Project:        project,
		Region:         region,
		SourceInstance: normaliseInstanceID(db.Project),
		BranchKey:      key,
		Variable:       variable,
		Endpoint:       lookup(EndpointVariable),
		Tier:           lookup(TierVariable),
		TLSMode:        lookup(TLSModeVariable),
		StopGoldens:    policy,
		MaxBranches:    db.MaxBranches,
		Getenv:         lookup,
		Now:            cfg.Now,
	})
}

// stringLookup adapts the engine's credential chain to the plain Getenv shape
// the optional settings want.
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
