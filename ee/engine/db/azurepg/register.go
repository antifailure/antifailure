// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg

// The registration, which is the difference between a provider that exists and
// a provider that runs.
//
// The registration must sit ABOVE the cloudgate.Wrap call in
// ee/engine/cmd/af/main.go. Wrap replaces what is registered at the moment it
// runs, so a provider added below it is served with no licence check at all,
// and that mistake compiles, vets clean and keeps every symbol count at one.

import (
	"context"
	"fmt"

	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// Registration is the socket implementation.
type Registration struct{}

// Register adds the Azure PostgreSQL provider to a registry.
//
// Unconditional rather than gated on a licence, which is the decision aurora's
// and cloudsql's registrations made and for the same reason: a build whose
// licence lapsed between starting and being used should refuse at the point of
// use with a sentence about the licence, not disappear from the list of
// providers this build has.
func Register(r *extension.Registry) { r.AddDatabaseProvider(Registration{}) }

func (Registration) Name() string { return Name }

// Open builds the provider from the manifest and the engine's secret chain.
func (Registration) Open(ctx context.Context, cfg extension.DatabaseConfig) (provider.Database, error) {
	db := cfg.Database

	if db.Project == "" {
		return nil, fmt.Errorf(
			"database.provider is azurepg and database.project is empty; it is the Azure " +
				"Database for PostgreSQL FLEXIBLE SERVER that goldens are restored from, " +
				"such as acme-production. The fully qualified domain name is accepted too " +
				"and the server name is taken from it. It is not a database name and not " +
				"a resource id")
	}

	lookup := stringLookup(ctx, cfg.Lookup)

	subscription := lookup(SubscriptionVariable)
	if subscription == "" {
		return nil, fmt.Errorf(
			"database.provider is azurepg and %s resolved to nothing. Every Resource "+
				"Manager request names the subscription, and a flexible server name is "+
				"only unique within a region's DNS zone rather than globally addressable "+
				"without one", SubscriptionVariable)
	}

	group := lookup(ResourceGroupVariable)
	if group == "" {
		return nil, fmt.Errorf(
			"database.provider is azurepg and %s resolved to nothing. A flexible server "+
				"is addressed by subscription, resource group and name together, and "+
				"asking the wrong group reports that the server does not exist rather "+
				"than that the group is wrong", ResourceGroupVariable)
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
			"database.provider is azurepg and %s resolved to nothing. It is not the "+
				"source server's administrator password: this provider derives a distinct "+
				"password for every restore from it, so that a preview environment never "+
				"holds production's database credential. A restored server keeps the "+
				"SOURCE's administrator login, so without this every branch would be "+
				"reachable with production's credential. Any high entropy string will do, "+
				"and changing it changes every branch's password", variable)
	}

	// Read here rather than at first use so that the refusal arrives while
	// somebody is still looking at the manifest, instead of after a server has
	// been provisioned and has to be deleted again.
	allow := lookup(FirewallVariable)
	if allow != "" {
		if _, _, err := cidrRange(allow); err != nil {
			return nil, err
		}
	}

	return New(Options{
		Subscription:  subscription,
		ResourceGroup: group,
		Location:      lookup(LocationVariable),
		SourceServer:  normaliseServerName(db.Project),
		BranchKey:     key,
		Variable:      variable,
		Endpoint:      lookup(EndpointVariable),
		AllowCIDR:     allow,
		Database:      lookup(DatabaseVariable),
		TLSMode:       lookup(TLSModeVariable),
		MaxBranches:   db.MaxBranches,
		Getenv:        lookup,
		Now:           cfg.Now,
	})
}

// stringLookup adapts the engine's credential chain to the plain Getenv shape
// the optional settings want.
//
// A lookup that fails and a variable that is absent both answer the empty
// string here. The one value whose absence must be a hard refusal, the branch
// key, is read through cfg.Lookup directly above so that its error survives,
// and the firewall CIDR gets its own refusal from cidrRange.
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
