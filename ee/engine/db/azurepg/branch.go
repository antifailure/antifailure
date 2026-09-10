// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg

// Branch lifecycle: branch, reset, destroy, connection strings, inventory,
// health.

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

const branchPrefix = "b"

// Branch creates a database for an environment from a golden version.
//
// Three refusals here are required by the conformance suite and each needs the
// engine's own catalog code: an unverified golden is AF-MSK-001, a golden that
// is not there is AF-DB-004, and passing the declared branch limit is
// AF-DB-006.
//
// Calling it twice with the same environment identifier returns the SAME branch
// rather than creating a second one. That is decided by looking the server up
// rather than from a local map: a second process running the same command has
// no access to this one's memory, and on Azure a duplicate is not merely a
// wasted instance, it is a second server billed by the hour.
func (p *Provider) Branch(ctx context.Context, version string, envID string) (provider.Branch, error) {
	if p.closed {
		return provider.Branch{}, fmt.Errorf("azurepg: provider is closed")
	}
	name := p.serverName(branchPrefix, envID)

	// Idempotence first, and before the limit check. An existing branch must
	// be returned even when the provider is at its limit, or re-running
	// `af up` on the last environment that fits would refuse the environment
	// that already exists.
	if existing, err := p.api.getServer(ctx, name); err == nil {
		if !isOurs(existing) {
			return provider.Branch{}, fmt.Errorf("azurepg: server %q: %w", name, ErrNotOurs)
		}
		return provider.Branch{EnvID: envID, From: version, ProviderRef: name}, nil
	} else if !notFound(err) {
		return provider.Branch{}, err
	}

	goldenName := p.serverName(goldenPrefix, version)
	golden, err := p.api.getServer(ctx, goldenName)
	if err != nil {
		if notFound(err) {
			return provider.Branch{}, coded(codeNoSuchGolden, fmt.Sprintf(
				"the golden version %q does not exist in resource group %s",
				version, p.opts.ResourceGroup))
		}
		return provider.Branch{}, err
	}
	if !isOurs(golden) {
		return provider.Branch{}, fmt.Errorf("azurepg: golden %q: %w", goldenName, ErrNotOurs)
	}
	if golden.Tags[versionTagKey] != version {
		return provider.Branch{}, coded(codeUnverifiedGolden, fmt.Sprintf(
			"the server holding version %q does not carry the published version tag, so "+
				"its masking pass never completed and it was never verified. Branching "+
				"it would hand an environment a copy of production that no masking pass "+
				"has been proved to have touched", version))
	}
	if err := p.refuseAccessCrossing(golden); err != nil {
		return provider.Branch{}, err
	}

	if p.opts.MaxBranches > 0 {
		count, err := p.countBranches(ctx)
		if err != nil {
			return provider.Branch{}, err
		}
		if count >= p.opts.MaxBranches {
			return provider.Branch{}, coded(codeBranchLimit, fmt.Sprintf(
				"this provider holds %d branches and database.max_branches is %d. The "+
					"limit is enforced here rather than left to the subscription's own "+
					"quota, because that quota is shared with everything else in the "+
					"subscription and hitting it takes out more than this tool",
				count, p.opts.MaxBranches))
		}
	}

	op, err := p.api.restore(ctx, goldenName, name, golden.Location, golden.Properties.Network, p.now().UTC(), map[string]string{
		tagKey:    tagValue,
		envTagKey: envID,
		// The golden this branch came from, so DestroyGolden can refuse to
		// remove one that is still referenced.
		fromTagKey: version,
	})
	if err != nil {
		return provider.Branch{}, fmt.Errorf(
			"azurepg: restoring golden %q into %q: %w", goldenName, name, err)
	}

	created := false
	defer func() {
		if created {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		if op, err := p.api.deleteServer(cleanup, name); err == nil {
			_ = p.api.wait(cleanup, op, p.opts.PollInterval)
		}
	}()
	if err := p.api.wait(ctx, op, p.opts.PollInterval); err != nil {
		return provider.Branch{}, err
	}

	// The post restore work Azure does not do: reset the inherited
	// administrator password and create the firewall rule. See prepare.
	if _, err := p.prepare(ctx, name); err != nil {
		return provider.Branch{}, err
	}

	created = true
	return provider.Branch{
		EnvID:       envID,
		From:        version,
		ProviderRef: name,
		CreatedAt:   p.now().UTC(),
	}, nil
}

// Reset returns provider.ErrUnsupported.
//
// A restore on Azure creates a new server rather than returning an existing one
// to an earlier state, which Microsoft states plainly: "A restore operation
// always creates a new database server with the name that you provide. It
// doesn't overwrite the existing database server." So there is no operation
// here that matches the capability's definition, and the conformance suite
// skips the behaviour by name rather than passing it silently.
func (p *Provider) Reset(context.Context, provider.Branch) error {
	return provider.ErrUnsupported
}

// Destroy removes a branch. Removing one that is already gone succeeds.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	name := b.ProviderRef
	if name == "" {
		name = p.serverName(branchPrefix, b.EnvID)
	}
	s, err := p.api.getServer(ctx, name)
	if err != nil {
		if notFound(err) {
			return nil
		}
		return err
	}
	if !isOurs(s) {
		return fmt.Errorf("azurepg: server %q: %w", name, ErrNotOurs)
	}
	op, err := p.api.deleteServer(ctx, name)
	if err != nil {
		if notFound(err) {
			return nil
		}
		return err
	}
	return p.api.wait(ctx, op, p.opts.PollInterval)
}

// ConnString returns a connection string for a branch.
//
// ConnPooled answers the same address as ConnDirect. Azure offers PgBouncer as
// a server parameter on some tiers rather than as a separate endpoint, so there
// is no second address to hand back; Capabilities reports PooledEndpoints false,
// which is the field a caller is supposed to read, and the address it gets is
// the one that works.
func (p *Provider) ConnString(ctx context.Context, b provider.Branch, mode provider.ConnMode) (secret.Value, error) {
	name := b.ProviderRef
	if name == "" {
		name = p.serverName(branchPrefix, b.EnvID)
	}
	s, err := p.api.getServer(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	host := s.Properties.FullyQualifiedDomainName
	if host == "" {
		return secret.Value{}, fmt.Errorf(
			"azurepg: server %q reports no fully qualified domain name", name)
	}
	found, err := p.api.listDatabases(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	database, err := pickDatabase(p.opts.Database, found)
	if err != nil {
		return secret.Value{}, err
	}
	return p.connString(host, p.port(), adminLoginOf(s, p.opts.AdminUser),
		p.branchPassword(name), database), nil
}

// Inventory lists everything this provider currently holds.
//
// Goldens AND branches: a golden left behind by a killed refresh is a billed
// flexible server in exactly the way a branch is, and an inventory that showed
// only branches would report clean while holding one.
func (p *Provider) Inventory(ctx context.Context) ([]provider.Resource, error) {
	servers, err := p.api.listServers(ctx)
	if err != nil {
		return nil, err
	}
	var out []provider.Resource
	for i := range servers {
		s := &servers[i]
		if !isOurs(s) {
			continue
		}
		kind := "branch"
		if s.Tags[goldenTagKey] != "" {
			kind = "golden"
		}
		out = append(out, provider.Resource{
			Kind:   kind,
			ID:     s.Name,
			EnvID:  s.Tags[envTagKey],
			Labels: s.Tags,
		})
	}
	return out, nil
}

// Health reports whether a branch is reachable.
//
// A removed branch is reported as NOT reachable rather than as an error, which
// the conformance suite requires and which is the right shape anyway: "the
// thing is gone" is an answer to the health question, not a failure to ask it.
//
// On Azure an unreachable branch has a second common cause worth naming in the
// detail, because the two look identical from a timeout: the server may be
// there and have no firewall rule. Microsoft does not copy rules across a
// restore, so that is the ordinary failure rather than an exotic one.
func (p *Provider) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	start := p.now()
	url, err := p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		if notFound(err) {
			return provider.Health{Reachable: false, Detail: "the server does not exist"}, nil
		}
		return provider.Health{}, err
	}
	db, err := sql.Open("pgx", url.Reveal())
	if err != nil {
		return provider.Health{Reachable: false, Detail: err.Error()}, nil
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		return provider.Health{
			Reachable: false,
			Detail: err.Error() + " (on Azure this is also what a missing firewall rule " +
				"looks like: rules are not copied across a restore)",
			Latency: p.now().Sub(start),
		}, nil
	}
	return provider.Health{Reachable: true, Latency: p.now().Sub(start)}, nil
}

func (p *Provider) countBranches(ctx context.Context) (int, error) {
	servers, err := p.api.listServers(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for i := range servers {
		s := &servers[i]
		if isOurs(s) && s.Tags[goldenTagKey] == "" {
			count++
		}
	}
	return count, nil
}

// Compile time proof that the provider satisfies the socket it is registered
// against.
var _ provider.Database = (*Provider)(nil)
