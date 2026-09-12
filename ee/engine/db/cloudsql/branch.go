// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql

// Branch lifecycle: branch, reset, destroy, connection strings, inventory,
// health.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// branchPrefix distinguishes a branch's instance id from a golden's.
const branchPrefix = "b"

// credentialsFor resolves the administrator and database for one instance.
// It only reads; prepareCredentials rotates and validates the credentials.
//
// Both halves are READ from the instance rather than assumed, for the reasons
// pickUser and pickDatabase give. The password set here is what makes a branch
// unreachable with the source's credential, which a Cloud SQL clone otherwise
// carries across.
func (p *Provider) credentialsFor(ctx context.Context, name string) (usr, db, password string, err error) {
	users, err := p.api.listUsers(ctx, name)
	if err != nil {
		return "", "", "", err
	}
	usr, err = selectAdministrator(p.opts.AdminUser, users)
	if err != nil {
		return "", "", "", err
	}
	found, err := p.api.listDatabases(ctx, name)
	if err != nil {
		return "", "", "", err
	}
	db = pickDatabase(p.opts.Database, found)
	return usr, db, p.branchPassword(name), nil
}

// Branch creates a database for an environment from a golden version.
//
// Three refusals here are required by the conformance suite and each needs the
// engine's own catalog code, so that a caller matching on the code gets the
// same answer from this provider as from any other:
//
//   - an unverified golden is AF-MSK-001,
//   - a golden that is not there is AF-DB-004,
//   - passing the declared branch limit is AF-DB-006.
//
// Calling it twice with the same environment identifier returns the SAME
// branch rather than creating a second one, which is what makes `af up` safe to
// re-run. That is decided by looking the instance up, not by a local map: a
// second process running the same command has no access to this one's memory,
// and creating a duplicate paid instance is the failure that would cause.
func (p *Provider) Branch(ctx context.Context, version string, envID string) (branch provider.Branch, rerr error) {
	if p.closed.Load() {
		return provider.Branch{}, fmt.Errorf("cloudsql: provider is closed")
	}
	release, err := p.admit(ctx)
	if err != nil {
		return provider.Branch{}, err
	}
	defer release()
	name := p.instanceName(branchPrefix, envID)

	// Idempotence first, and before the limit check. An existing branch must
	// be returned even when the provider is at its limit, or re-running
	// `af up` on the last environment that fits would refuse the environment
	// that already exists.
	if existing, err := p.api.getInstance(ctx, name); err == nil {
		if !p.owns(existing) {
			return provider.Branch{}, fmt.Errorf("cloudsql: instance %q: %w", name, ErrNotOurs)
		}
		if err := p.requirePrepared(existing, version, envID); err != nil {
			return provider.Branch{}, err
		}
		return provider.Branch{
			EnvID:       envID,
			From:        version,
			ProviderRef: name,
			CreatedAt:   existing.CreateTime,
		}, nil
	} else if !notFound(err) {
		return provider.Branch{}, err
	}

	golden, err := p.api.getInstance(ctx, p.instanceName(goldenPrefix, version))
	if err != nil {
		if notFound(err) {
			return provider.Branch{}, coded(codeNoSuchGolden, fmt.Sprintf(
				"the golden version %q does not exist in project %s", version, p.opts.Project))
		}
		return provider.Branch{}, err
	}
	if !p.owns(golden) {
		return provider.Branch{}, fmt.Errorf("cloudsql: golden %q: %w", golden.Name, ErrNotOurs)
	}
	// The AUTHORITATIVE version label rather than the marker, and the
	// difference matters. goldenLabelKey holds a label safe rendering of the
	// version, which is equal to it only while every version id happens to be
	// lower case; versionLabelKey holds the version itself, base32 encoded.
	// Comparing the marker would be a check that passes by coincidence and
	// would start refusing every branch the first time a rules hash carried a
	// capital letter.
	//
	// It is also the exact test for "was this published". RefreshGolden sets
	// the ownership marker BEFORE masking and the version label only AFTER
	// verification returns, so an instance carrying the first and not the
	// second is precisely a golden whose verification did not complete.
	if recorded, ok := decodeValue(golden.Settings.UserLabels[versionLabelKey]); !ok || recorded != version {
		return provider.Branch{}, coded(codeUnverifiedGolden, fmt.Sprintf(
			"the instance holding version %q does not carry the published version label, "+
				"so its masking pass never completed and it was never verified. "+
				"Branching it would hand an environment a copy of production that no "+
				"masking pass has been proved to have touched", version))
	}

	if p.opts.MaxBranches > 0 {
		count, err := p.countBranches(ctx)
		if err != nil {
			return provider.Branch{}, err
		}
		if count >= p.opts.MaxBranches {
			return provider.Branch{}, coded(codeBranchLimit, fmt.Sprintf(
				"this provider holds %d branches and database.max_branches is %d. The "+
					"limit is enforced here rather than left to Cloud SQL's per project "+
					"instance quota, because that quota is shared with everything else "+
					"in the project and hitting it takes out more than this tool",
				count, p.opts.MaxBranches))
		}
	}

	op, err := p.api.clone(ctx, golden.Name, name)
	partial := provider.Branch{EnvID: envID, From: version, ProviderRef: name}
	if err != nil && !acceptedResponse(err) {
		if uncertainResponse(err) {
			return partial, fmt.Errorf("cloudsql: clone acceptance is unknown for %q; reconcile ownership before removal: %w", name, err)
		}
		return provider.Branch{}, fmt.Errorf("cloudsql: cloning golden %q into %q: %w", golden.Name, name, err)
	}
	created := false
	defer func() {
		if created {
			return
		}
		if cleanupErr := p.removeCreated(ctx, name); cleanupErr != nil {
			branch = partial
			rerr = errors.Join(rerr, cleanupErr)
		}
	}()
	if err != nil {
		return provider.Branch{}, err
	}
	if err := p.api.waitForOperation(ctx, op, p.opts.PollInterval); err != nil {
		return provider.Branch{}, err
	}

	if err := p.label(ctx, name, map[string]string{
		labelKey:       labelValue,
		sourceLabelKey: p.sourceIdentity(),
		envLabelKey:    envID,
		// The golden this branch came from, so DestroyGolden can refuse to
		// remove one that is still referenced. Without it a golden with a live
		// branch is removable and the branch's data disappears underneath a
		// running environment.
		fromLabelKey:     shortVersion(version),
		goldenLabelKey:   "",
		versionLabelKey:  encodeValue(version),
		preparedLabelKey: "",
	}); err != nil {
		return provider.Branch{}, err
	}

	// A clone carries the SOURCE's users and passwords, which Google documents.
	// So a branch would be reachable with production's database credential
	// until this runs, and that is exactly what this provider exists to
	// prevent. Setting it is not a convenience.
	if _, err := p.prepareCredentials(ctx, name); err != nil {
		return provider.Branch{}, err
	}

	// The golden's compute policy is not inherited in a useful state: a clone
	// of a stopped instance would be stopped, and nobody can connect to it.
	if p.opts.StopGoldens == GoldenIsStopped {
		if err := p.setActivation(ctx, name, "ALWAYS"); err != nil {
			return provider.Branch{}, err
		}
	}

	if err := p.label(ctx, name, map[string]string{preparedLabelKey: p.preparedToken(name, version, envID)}); err != nil {
		return provider.Branch{}, err
	}
	in, err := p.api.getInstance(ctx, name)
	if err != nil {
		return provider.Branch{}, err
	}
	if err := p.requirePrepared(in, version, envID); err != nil {
		return provider.Branch{}, err
	}
	created = true
	return provider.Branch{
		EnvID:       envID,
		From:        version,
		ProviderRef: name,
		CreatedAt:   in.CreateTime,
	}, nil
}

// Reset returns provider.ErrUnsupported.
//
// Cloud SQL has no rewind that returns an instance to an earlier state without
// creating a new one. Restoring a backup onto an existing instance is the
// nearest thing and it is not the same operation: it goes through the same
// provisioning as a clone and it takes the instance offline while it runs, so
// dressing it up as Reset would publish a capability whose cost is nothing like
// what the name implies. The conformance suite skips this behaviour by name
// rather than passing it silently, which is the honest outcome.
func (p *Provider) Reset(context.Context, provider.Branch) error {
	return provider.ErrUnsupported
}

// Destroy removes a branch. Removing one that is already gone succeeds.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	name := b.ProviderRef
	if name == "" {
		name = p.instanceName(branchPrefix, b.EnvID)
	}
	in, err := p.api.getInstance(ctx, name)
	if err != nil {
		if notFound(err) {
			return nil
		}
		return err
	}
	if !p.owns(in) || in.Settings.UserLabels[goldenLabelKey] != "" ||
		in.Name != p.instanceName(branchPrefix, in.Settings.UserLabels[envLabelKey]) ||
		(b.EnvID != "" && in.Settings.UserLabels[envLabelKey] != b.EnvID) {
		return fmt.Errorf("cloudsql: instance %q: %w", name, ErrNotOurs)
	}
	op, err := p.api.deleteInstance(ctx, name)
	if err != nil {
		if notFound(err) {
			return nil
		}
		return err
	}
	return p.api.waitForOperation(ctx, op, p.opts.PollInterval)
}

// ConnString returns a connection string for a branch.
//
// ConnPooled answers the same address as ConnDirect and that is deliberate
// rather than unfinished. Cloud SQL's pooling is a separate product, the Auth
// Proxy or PgBouncer running beside the application, and neither is something
// this provider creates. Returning the direct address for the pooled mode would
// be a lie in the flattering direction; returning ErrUnsupported would break
// callers that ask for pooled and can use direct. So Capabilities reports
// PooledEndpoints false, which is the field a caller is supposed to read, and
// the address it gets is the one that works.
func (p *Provider) ConnString(ctx context.Context, b provider.Branch, mode provider.ConnMode) (secret.Value, error) {
	name := b.ProviderRef
	if name == "" {
		name = p.instanceName(branchPrefix, b.EnvID)
	}
	in, err := p.api.getInstance(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	if err := p.requireBranchIdentity(in, b); err != nil {
		return secret.Value{}, err
	}
	usr, db, password, err := p.credentialsFor(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	return p.secureConnString(ctx, in, usr, password, db)
}

// adminURL is the connection string the masking and verification hooks are
// given for a golden.
func (p *Provider) adminURL(ctx context.Context, name string) (secret.Value, error) {
	return p.prepareCredentials(ctx, name)
}

// primaryAddress picks the address a client should use.
//
// PRIMARY before PRIVATE before anything else, and the order is not arbitrary:
// an instance with both answers on both, and choosing the private one for a
// caller that is not on the VPC produces a timeout rather than a refusal, which
// is the slowest possible way to learn the answer.
func primaryAddress(in *instance) string {
	if in == nil {
		return ""
	}
	for _, want := range []string{"PRIMARY", "PRIVATE"} {
		for _, a := range in.IPAddresses {
			if a.Type == want && a.IPAddress != "" {
				return a.IPAddress
			}
		}
	}
	for _, a := range in.IPAddresses {
		if a.IPAddress != "" {
			return a.IPAddress
		}
	}
	return ""
}

// Inventory lists everything this provider currently holds.
//
// Required rather than optional, because the leak detector compares it against
// the journal and a provider that cannot enumerate its own resources cannot be
// checked. It lists goldens AND branches: a golden left behind by a killed
// refresh costs money in exactly the same way, and an inventory that showed
// only branches would report clean while holding one.
func (p *Provider) Inventory(ctx context.Context) ([]provider.Resource, error) {
	instances, err := p.api.listInstances(ctx)
	if err != nil {
		return nil, err
	}
	var out []provider.Resource
	for i := range instances {
		in := &instances[i]
		if !p.owns(in) {
			continue
		}
		kind := "branch"
		if in.Settings.UserLabels[goldenLabelKey] != "" {
			kind = "golden"
		}
		out = append(out, provider.Resource{
			Kind:      kind,
			ID:        in.Name,
			EnvID:     in.Settings.UserLabels[envLabelKey],
			CreatedAt: in.CreateTime,
			Labels:    in.Settings.UserLabels,
		})
	}
	return out, nil
}

// Health reports whether a branch is reachable.
//
// A removed branch is reported as NOT reachable rather than as an error, which
// the conformance suite requires and which is the right shape anyway: "the
// thing is gone" is an answer to the health question, not a failure to ask it.
// An error is reserved for not being able to find out.
func (p *Provider) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	start := p.now()
	url, err := p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		if notFound(err) {
			return provider.Health{Reachable: false, Detail: "the instance does not exist"}, nil
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
			Detail:    err.Error(),
			Latency:   p.now().Sub(start),
		}, nil
	}
	return provider.Health{Reachable: true, Latency: p.now().Sub(start)}, nil
}

// countBranches is how many branch instances this provider currently holds.
func (p *Provider) countBranches(ctx context.Context) (int, error) {
	instances, err := p.api.listInstances(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for i := range instances {
		in := &instances[i]
		if p.owns(in) && in.Settings.UserLabels[goldenLabelKey] == "" {
			count++
		}
	}
	return count, nil
}

// Compile time proof that the provider satisfies the socket it is registered
// against. Not decoration: a method whose signature drifts from the interface
// would otherwise be caught only when somebody selects this provider.
var (
	_ provider.Database = (*Provider)(nil)
)
