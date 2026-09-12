// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg

// Golden lifecycle: refresh, list, destroy.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

const goldenPrefix = "g"

// RefreshGolden builds a new masked, verified golden version.
//
// The ORDER is the product's central guarantee: restore, then Load if the
// caller supplied one, then Mask, then Verify, and publish ONLY if Verify
// returned. A version whose Verified field is false is never returned, and the
// failure path deletes the server it created rather than leaving an unmasked
// copy of production in the resource group under a name that looks published.
//
// The cleanup is sharper on Azure than elsewhere and the comment says so at the
// point it matters: an error between the restore and the verification leaves a
// running flexible server holding UNMASKED production data, billed by the hour,
// under a name returned to nobody.
func (p *Provider) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	if p.closed.Load() {
		return provider.GoldenVersion{}, fmt.Errorf("azurepg: provider is closed")
	}
	source := normaliseServerName(p.opts.SourceServer)
	src, err := p.api.getServer(ctx, source)
	if err != nil {
		return provider.GoldenVersion{}, fmt.Errorf(
			"azurepg: reading source server %q: %w", source, err)
	}
	if err := p.refuseAccessCrossing(src); err != nil {
		return provider.GoldenVersion{}, err
	}

	created := p.now().UTC()
	version := provider.NewGoldenVersionID(created, spec.RulesHash)
	name := p.serverName(goldenPrefix, version)

	op, err := p.api.restore(ctx, source, name, src.Location, src.SKU, src.Properties.Network, p.now().UTC(), map[string]string{
		tagKey:       tagValue,
		goldenTagKey: version,
		sourceTagKey: source,
	})
	if err != nil && op == nil {
		return provider.GoldenVersion{}, fmt.Errorf(
			"azurepg: restoring source server %q into %q: %w", source, name, err)
	}

	published := false
	defer func() {
		if published {
			return
		}
		// A fresh context: the failure that brought us here is very often ctx
		// being cancelled, and cleanup that inherits a cancelled context does
		// nothing at all while looking like it ran.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		defer cancel()
		if op, err := p.api.deleteServer(cleanup, name); err == nil {
			_ = p.api.wait(cleanup, op, p.opts.PollInterval)
		}
	}()
	if err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("azurepg: accepted restore response could not be read: %w", err)
	}
	if err := p.api.wait(ctx, op, p.opts.PollInterval); err != nil {
		return provider.GoldenVersion{}, err
	}

	url, err := p.prepare(ctx, name)
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	if spec.Load != nil {
		if err := spec.Load(ctx, spec.SourceURL, url); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("azurepg: loading golden: %w", err)
		}
	}
	if spec.Mask == nil {
		return provider.GoldenVersion{}, fmt.Errorf(
			"azurepg: GoldenSpec.Mask is nil, and publishing an unmasked golden is the " +
				"one thing this provider must never do")
	}
	if err := spec.Mask(ctx, url); err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("azurepg: masking golden: %w", err)
	}
	if spec.Verify == nil {
		return provider.GoldenVersion{}, fmt.Errorf(
			"azurepg: GoldenSpec.Verify is nil, so nothing would check the masking")
	}
	attestation, err := spec.Verify(ctx, url)
	if err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("azurepg: verifying golden: %w", err)
	}
	if err := p.disableInheritedLogins(ctx, url); err != nil {
		return provider.GoldenVersion{}, err
	}

	// The metadata goes on AFTER verification, so a golden that never verified
	// cannot be found carrying a provenance that suggests it did. It is also
	// where an attestation too large for Azure's tags is refused, and refusing
	// here rather than at the start is deliberate: the attestation does not
	// exist until Verify has returned.
	metadata, err := encodeMetadata(goldenMetadata{
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		Attestation: attestation,
	})
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	metadata[versionTagKey] = version
	metadata[tagKey] = tagValue
	metadata[sourceTagKey] = source
	metadata[goldenTagKey] = version
	metadata[createdTagKey] = created.Format(time.RFC3339Nano)
	op, err = p.api.patchServer(ctx, name, map[string]any{"tags": metadata})
	if err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("azurepg: tagging golden %q: %w", name, err)
	}
	if err := p.api.wait(ctx, op, p.opts.PollInterval); err != nil {
		return provider.GoldenVersion{}, err
	}

	golden, err := p.api.getServer(ctx, name)
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	published = true
	return provider.GoldenVersion{
		ID:          version,
		CreatedAt:   created,
		SizeBytes:   golden.Properties.Storage.StorageSizeGB * 1024 * 1024 * 1024,
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		Verified:    true,
		Attestation: attestation,
		ProviderRef: name,
	}, nil
}

// refuseAccessCrossing enforces the boundary Azure will not let a restore
// cross.
//
// Microsoft states a public access server restores only to public access and a
// virtual network server only to a virtual network. Public sources require a
// firewall range; private sources require the DNS zone that restores retain.
// Both are checked before a billed server is provisioned.
func (p *Provider) refuseAccessCrossing(src *server) error {
	if p.opts.Location != "" && canonicalLocation(p.opts.Location) != canonicalLocation(src.Location) {
		return fmt.Errorf("azurepg: the configured location %q differs from the source location %q", p.opts.Location, src.Location)
	}
	if src.access() == AccessPrivate {
		if src.Properties.Network.PrivateDNSZoneResourceID == "" {
			return fmt.Errorf("azurepg: source %q is on a virtual network but names no private DNS zone; a restore cannot preserve its private connectivity", src.Name)
		}
		return nil
	}
	_, _, err := cidrRange(p.opts.AllowCIDR)
	return err
}

// Resource Manager can return a display name such as Central US for centralus.
func canonicalLocation(location string) string {
	return strings.ToLower(strings.Join(strings.Fields(location), ""))
}

// prepare does the POST RESTORE work Azure does not do for you.
//
// Two things, and neither is optional:
//
//   - The administrator password is reset. A restored server keeps the SOURCE's
//     administrator login, so without this every branch would be reachable with
//     production's credential.
//   - The firewall rule is created. Microsoft lists this as a post restore task
//     because rules are NOT copied, so a branch without it is a server that
//     provisioned successfully and answers nobody.
func (p *Provider) prepare(ctx context.Context, name string) (secret.Value, error) {
	srv, err := p.api.getServer(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	login := adminLoginOf(srv, p.opts.AdminUser)
	password := p.branchPassword(name)
	op, err := p.api.patchServer(ctx, name, map[string]any{
		"properties": map[string]any{"administratorLoginPassword": password},
	})
	if err != nil {
		return secret.Value{}, fmt.Errorf("azurepg: resetting the password on %q: %w", name, err)
	}
	if err := p.api.wait(ctx, op, p.opts.PollInterval); err != nil {
		return secret.Value{}, err
	}

	if srv.access() == AccessPublic {
		start, end, err := cidrRange(p.opts.AllowCIDR)
		if err != nil {
			return secret.Value{}, err
		}
		op, err = p.api.putFirewallRule(ctx, name, "antifailure", start, end)
		if err != nil {
			return secret.Value{}, fmt.Errorf("azurepg: creating the restored server's firewall rule: %w", err)
		}
		if err := p.api.wait(ctx, op, p.opts.PollInterval); err != nil {
			return secret.Value{}, err
		}
	}

	srv, err = p.api.getServer(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	host := srv.Properties.FullyQualifiedDomainName
	if host == "" {
		return secret.Value{}, fmt.Errorf(
			"azurepg: server %q reports no fully qualified domain name, so there is no "+
				"address to connect to", name)
	}
	found, err := p.api.listDatabases(ctx, name)
	if err != nil {
		return secret.Value{}, err
	}
	database, err := pickDatabase(p.opts.Database, found)
	if err != nil {
		return secret.Value{}, err
	}
	connection := p.connString(host, p.port(), login, password, database)
	if err := p.disableInheritedLogins(ctx, connection); err != nil {
		return secret.Value{}, err
	}
	return connection, nil
}

// ListGoldens returns known versions, newest first.
//
// Built from the resource group's own servers rather than from a local record,
// so that a golden created by another machine, or one this process has
// forgotten, is still listed. A provider whose list came from memory would
// report a clean inventory while holding servers somebody pays for.
func (p *Provider) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	servers, err := p.api.listServers(ctx)
	if err != nil {
		return nil, err
	}
	var out []provider.GoldenVersion
	for i := range servers {
		s := &servers[i]
		if !p.isOurs(s) {
			continue
		}
		if s.Tags[goldenTagKey] == "" {
			continue
		}
		version := s.Tags[versionTagKey]
		if version == "" {
			// A server carrying the ownership marker and no version tag is a
			// golden whose verification did not complete. Skipped here, and
			// Inventory still reports it, so it is never invisible to the leak
			// detector.
			continue
		}
		meta := decodeMetadata(s.Tags)
		created, _ := time.Parse(time.RFC3339Nano, s.Tags[createdTagKey])
		out = append(out, provider.GoldenVersion{
			ID:          version,
			CreatedAt:   created,
			SizeBytes:   s.Properties.Storage.StorageSizeGB * 1024 * 1024 * 1024,
			RulesHash:   meta.RulesHash,
			Provenance:  meta.Provenance,
			Verified:    true,
			Attestation: meta.Attestation,
			ProviderRef: s.Name,
		})
	}
	sortVersionsNewestFirst(out)
	return out, nil
}

// DestroyGolden removes a version. Removing one that does not exist succeeds.
//
// The ownership check runs first and it matters more here than anywhere else in
// this repository: Microsoft states that deleting a flexible server deletes
// every backup belonging to it. There is no recovery from a wrong delete.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	name := p.serverName(goldenPrefix, version)
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
	// A golden with a live branch must not be removed. The engine's contract
	// is that a referenced golden is refused, and on Azure the consequence of
	// allowing it is worse than elsewhere: deleting a flexible server deletes
	// every backup belonging to it, so the version another environment is
	// recorded against becomes unrestorable rather than merely absent.
	referenced, err := p.branchesFrom(ctx, version)
	if err != nil {
		return err
	}
	if len(referenced) > 0 {
		return coded(codeGoldenReferenced, fmt.Sprintf(
			"the golden version %q still has %d branch(es) recorded against it: %s",
			version, len(referenced), strings.Join(referenced, ", ")))
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

// branchesFrom lists the branches recorded as restored from one golden.
func (p *Provider) branchesFrom(ctx context.Context, version string) ([]string, error) {
	servers, err := p.api.listServers(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for i := range servers {
		s := &servers[i]
		if !p.isOurs(s) || s.Tags[goldenTagKey] != "" {
			continue
		}
		if s.Tags[fromTagKey] == version {
			out = append(out, s.Name)
		}
	}
	return out, nil
}
