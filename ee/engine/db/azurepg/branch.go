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
	if p.closed.Load() {
		return provider.Branch{}, fmt.Errorf("azurepg: provider is closed")
	}
	release, err := p.admit(ctx)
	if err != nil {
		return provider.Branch{}, err
	}
	defer release()
	name := p.serverName(branchPrefix, envID)

	// Idempotence first, and before the limit check. An existing branch must
	// be returned even when the provider is at its limit, or re-running
	// `af up` on the last environment that fits would refuse the environment
	// that already exists.
	if existing, err := p.api.getServer(ctx, name); err == nil {
		if !p.isOurs(existing) {
			return provider.Branch{}, fmt.Errorf("azurepg: server %q: %w", name, ErrNotOurs)
		}
		if existing.Tags[envTagKey] != envID || existing.Tags[fromTagKey] != version || existing.Tags[goldenTagKey] != "" {
			return provider.Branch{}, fmt.Errorf("azurepg: existing server does not match the requested environment and golden")
		}
		if err := p.finishBranch(ctx, existing); err != nil {
			return provider.Branch{EnvID: envID, From: version, ProviderRef: name}, err
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
	if !p.isOurs(golden) {
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

	at, err := p.waitForRestorePoint(ctx, golden)
	if err != nil {
		return provider.Branch{}, err
	}
	op, err := p.api.restore(ctx, goldenName, name, golden.Location, golden.SKU, golden.Properties.Network, at, map[string]string{
		tagKey:       tagValue,
		envTagKey:    envID,
		sourceTagKey: normaliseServerName(p.opts.SourceServer),
		// The golden this branch came from, so DestroyGolden can refuse to
		// remove one that is still referenced.
		fromTagKey: version,
	})
	if err != nil && op == nil {
		return provider.Branch{EnvID: envID, From: version, ProviderRef: name}, fmt.Errorf(
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
	if err != nil {
		return provider.Branch{}, fmt.Errorf("azurepg: accepted restore response could not be read: %w", err)
	}
	if err := p.api.wait(ctx, op, p.opts.PollInterval); err != nil {
		return provider.Branch{}, err
	}

	// The post restore work Azure does not do: reset the inherited
	// administrator password and create the firewall rule. See prepare.
	restored, err := p.api.getServer(ctx, name)
	if err != nil {
		return provider.Branch{}, err
	}
	if err := p.finishBranch(ctx, restored); err != nil {
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

// waitForRestorePoint returns the point in time a restore of srv may ask for.
//
// Microsoft documents that a restore time "earlier than the earliest restore
// point available on the source server" is answered with InternalServerError,
// and that a new server's first snapshot backup "is scheduled immediately after
// a server is created" rather than existing at creation. A golden is a server
// this provider created by a restore minutes before a branch is taken from it,
// so asking for now before its first backup exists fails with an error that
// names nothing. The one live run of the branch path failed exactly that way.
//
// So the server's backup.earliestRestoreDate is read, and the restore waits
// until it is reported and not later than now, polling at PollInterval and
// bounded by RestoreReadyTimeout. The time returned is now, which is the latest
// restore point and the default every Azure client uses.
func (p *Provider) waitForRestorePoint(ctx context.Context, srv *server) (time.Time, error) {
	started := p.now()
	deadline := started.Add(p.opts.RestoreReadyTimeout)
	// Bounded twice. The clock is injectable, and a clock that does not move
	// never reaches a deadline measured with it, so the number of polls is
	// bounded too and the wait ends whatever the clock does.
	maxPolls := int(p.opts.RestoreReadyTimeout/p.opts.PollInterval) + 1
	polls := 0
	waiting := false
	var lastReported time.Time
	for {
		raw := srv.Properties.Backup.EarliestRestoreDate
		if raw != "" {
			earliest, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return time.Time{}, fmt.Errorf(
					"azurepg: server %q reports backup.earliestRestoreDate %q, which is not a time: %w",
					srv.Name, raw, err)
			}
			if now := p.now().UTC(); !now.Before(earliest) {
				if waiting {
					p.report(fmt.Sprintf("Azure's first backup of %s is ready after %s; restoring from it",
						srv.Name, p.now().Sub(started).Round(time.Second)))
				}
				return now, nil
			}
		}
		state := "not yet reported"
		if raw != "" {
			state = "reported as " + raw
		}
		if !p.now().Before(deadline) || polls >= maxPolls {
			return time.Time{}, fmt.Errorf(
				"azurepg: server %q has no backup to restore from after waiting %s: its "+
					"backup.earliestRestoreDate is %s, and Azure answers a restore time before "+
					"that with InternalServerError. A server's first backup is taken after it is "+
					"created, so a golden published moments ago can need a few minutes",
				srv.Name, p.opts.RestoreReadyTimeout, state)
		}
		switch {
		case !waiting:
			waiting = true
			lastReported = p.now()
			p.report(fmt.Sprintf("waiting for Azure's first backup of %s, earliest restore point %s; "+
				"a restore before it is refused, so this waits for it, up to %s",
				srv.Name, state, p.opts.RestoreReadyTimeout))
		case p.now().Sub(lastReported) >= restoreWaitHeartbeat:
			lastReported = p.now()
			p.report(fmt.Sprintf("still waiting for Azure's first backup of %s after %s, earliest restore point %s",
				srv.Name, p.now().Sub(started).Round(time.Second), state))
		}
		select {
		case <-ctx.Done():
			return time.Time{}, ctx.Err()
		case <-time.After(p.opts.PollInterval):
		}
		polls++
		current, err := p.api.getServer(ctx, srv.Name)
		if err != nil {
			return time.Time{}, err
		}
		srv = current
	}
}

// A retry after process loss resumes preparation before handing out a branch.
func (p *Provider) finishBranch(ctx context.Context, srv *server) error {
	for srv.Properties.State != "Ready" {
		if srv.Properties.State == "Failed" || srv.Properties.State == "Dropping" {
			return fmt.Errorf("azurepg: restored server is %s", srv.Properties.State)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.opts.PollInterval):
		}
		current, err := p.api.getServer(ctx, srv.Name)
		if err != nil {
			return err
		}
		srv = current
	}
	if srv.Tags[preparedTagKey] == p.preparationReceipt(srv) {
		return nil
	}
	if _, err := p.prepare(ctx, srv.Name); err != nil {
		return err
	}
	tags := make(map[string]string, len(srv.Tags)+1)
	for key, value := range srv.Tags {
		tags[key] = value
	}
	tags[preparedTagKey] = p.preparationReceipt(srv)
	op, err := p.api.patchServer(ctx, srv.Name, map[string]any{"tags": tags})
	if err != nil {
		return err
	}
	return p.api.wait(ctx, op, p.opts.PollInterval)
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
	if !p.isOurs(s) {
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
	if p.closed.Load() {
		return secret.Value{}, fmt.Errorf("azurepg: provider is closed")
	}
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
		if !p.isOurs(s) {
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
		if p.isOurs(s) && s.Tags[goldenTagKey] == "" {
			count++
		}
	}
	return count, nil
}

// Compile time proof that the provider satisfies the socket it is registered
// against.
var _ provider.Database = (*Provider)(nil)
