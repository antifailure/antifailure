// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql

// Golden lifecycle: refresh, list, destroy.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// goldenPrefix distinguishes a golden's instance id from a branch's.
const goldenPrefix = "g"

// RefreshGolden builds a new masked, verified golden version.
//
// The ORDER is the product's central guarantee and it is the reason this
// method is not a thin wrapper: clone, then Load if the caller supplied one,
// then Mask, then Verify, and publish ONLY if Verify returned. A version whose
// Verified field is false is never returned, and the failure path destroys the
// instance it created rather than leaving an unmasked copy of production
// sitting in the project under a name that looks published.
//
// That cleanup is the part worth reading. An error between the clone and the
// verification leaves a running Cloud SQL instance holding UNMASKED production
// data. Returning the error without removing it would be a data exposure that
// the caller has no handle to clean up, because the instance id was never
// returned to anybody.
func (p *Provider) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	if p.closed {
		return provider.GoldenVersion{}, fmt.Errorf("cloudsql: provider is closed")
	}
	created := p.now().UTC()
	version := provider.NewGoldenVersionID(created, spec.RulesHash)
	name := p.instanceName(goldenPrefix, version)

	source := normaliseInstanceID(p.opts.SourceInstance)
	op, err := p.api.clone(ctx, source, name)
	if err != nil {
		return provider.GoldenVersion{}, fmt.Errorf(
			"cloudsql: cloning source instance %q: %w", source, err)
	}

	// From here on the instance EXISTS and holds production's rows. Every
	// return before publication has to remove it.
	published := false
	defer func() {
		if published {
			return
		}
		// A fresh context: the failure that brought us here is very often
		// ctx being cancelled, and cleanup that inherits a cancelled context
		// does nothing at all while looking like it ran.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if op, err := p.api.deleteInstance(cleanup, name); err == nil {
			_ = p.api.waitForOperation(cleanup, op, p.opts.PollInterval)
		}
	}()
	if err := p.api.waitForOperation(ctx, op, p.opts.PollInterval); err != nil {
		return provider.GoldenVersion{}, err
	}

	if err := p.label(ctx, name, map[string]string{
		labelKey:       labelValue,
		goldenLabelKey: shortVersion(version),
	}); err != nil {
		return provider.GoldenVersion{}, err
	}

	url, err := p.adminURL(ctx, name)
	if err != nil {
		return provider.GoldenVersion{}, err
	}

	if spec.Load != nil {
		if err := spec.Load(ctx, spec.SourceURL, url); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("cloudsql: loading golden: %w", err)
		}
	}
	if spec.Mask == nil {
		return provider.GoldenVersion{}, fmt.Errorf(
			"cloudsql: GoldenSpec.Mask is nil, and publishing an unmasked golden is the " +
				"one thing this provider must never do")
	}
	if err := spec.Mask(ctx, url); err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("cloudsql: masking golden: %w", err)
	}
	if spec.Verify == nil {
		return provider.GoldenVersion{}, fmt.Errorf(
			"cloudsql: GoldenSpec.Verify is nil, so nothing would check the masking")
	}
	attestation, err := spec.Verify(ctx, url)
	if err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("cloudsql: verifying golden: %w", err)
	}

	// The metadata goes on AFTER verification, so a golden that never verified
	// cannot be found carrying a provenance that suggests it did.
	//
	// This is also where a provenance or attestation too large for Cloud SQL's
	// labels is refused, and refusing here rather than at the start is
	// deliberate: the size that matters is the attestation's, and the
	// attestation does not exist until Verify has returned.
	metadata, err := encodeMetadata(goldenMetadata{
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		Attestation: attestation,
	})
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	metadata[versionLabelKey] = encodeValue(version)
	if err := p.label(ctx, name, metadata); err != nil {
		return provider.GoldenVersion{}, err
	}

	// The golden is masked and verified. Now, and only now, the compute policy
	// applies. Stopping BEFORE verification would have verified a thing nobody
	// could connect to.
	if p.opts.StopGoldens == GoldenIsStopped {
		if err := p.setActivation(ctx, name, "NEVER"); err != nil {
			return provider.GoldenVersion{}, err
		}
	}

	size, _ := p.diskBytes(ctx, name)
	published = true
	return provider.GoldenVersion{
		ID:          version,
		CreatedAt:   created,
		SizeBytes:   size,
		RulesHash:   spec.RulesHash,
		Provenance:  spec.Provenance,
		Verified:    true,
		Attestation: attestation,
		ProviderRef: name,
	}, nil
}

// ListGoldens returns known versions, newest first.
//
// Built from the project's own instances rather than from a local record, so
// that a golden created by another machine, or one this process has forgotten,
// is still listed. A provider whose list came from memory would report a clean
// inventory while holding instances somebody pays for.
func (p *Provider) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	instances, err := p.api.listInstances(ctx)
	if err != nil {
		return nil, err
	}
	var out []provider.GoldenVersion
	for i := range instances {
		in := &instances[i]
		if !isOurs(in) {
			continue
		}
		if in.Settings.UserLabels[goldenLabelKey] == "" {
			continue
		}
		version, ok := decodeValue(in.Settings.UserLabels[versionLabelKey])
		if !ok || version == "" {
			// A golden whose version label is missing or undecodable is one
			// this provider cannot name, and listing it under a wrong or empty
			// id would make the engine refuse to branch it with no reason a
			// reader could act on. Skipped, and Inventory still reports the
			// instance, so it is never invisible to the leak detector.
			continue
		}
		meta := decodeMetadata(in.Settings.UserLabels)
		out = append(out, provider.GoldenVersion{
			ID:          version,
			CreatedAt:   in.CreateTime,
			SizeBytes:   diskBytesOf(in),
			RulesHash:   meta.RulesHash,
			Provenance:  meta.Provenance,
			Verified:    true,
			Attestation: meta.Attestation,
			ProviderRef: in.Name,
		})
	}
	sortVersionsNewestFirst(out)
	return out, nil
}

// DestroyGolden removes a version. Removing one that does not exist succeeds.
//
// It refuses an instance that does not carry the ownership label, with
// ErrNotOurs, before issuing the delete. That check is the whole reason this is
// not one API call: a project where somebody has named their own instance the
// way this provider names its own must not lose it here.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	name, err := p.goldenInstance(ctx, version)
	if err != nil {
		if notFound(err) {
			return nil
		}
		return err
	}
	// A golden with a live branch must not be removed. On Cloud SQL a branch
	// is a full clone rather than a view onto the golden's storage, so the
	// branch would survive; but the engine's contract is that a referenced
	// golden is refused, and a provider that quietly allowed it would let one
	// environment's teardown remove the version another environment is
	// recorded against.
	referenced, err := p.branchesFrom(ctx, version)
	if err != nil {
		return err
	}
	if len(referenced) > 0 {
		return coded(codeGoldenReferenced, fmt.Sprintf(
			"the golden version %q still has %d branch(es) recorded against it: %s",
			version, len(referenced), strings.Join(referenced, ", ")))
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

// branchesFrom lists the branches recorded as cloned from one golden.
func (p *Provider) branchesFrom(ctx context.Context, version string) ([]string, error) {
	instances, err := p.api.listInstances(ctx)
	if err != nil {
		return nil, err
	}
	marker := shortVersion(version)
	var out []string
	for i := range instances {
		in := &instances[i]
		if !isOurs(in) || in.Settings.UserLabels[goldenLabelKey] != "" {
			continue
		}
		if in.Settings.UserLabels[fromLabelKey] == marker {
			out = append(out, in.Name)
		}
	}
	return out, nil
}

// goldenInstance resolves a version to the instance holding it, checking
// ownership.
func (p *Provider) goldenInstance(ctx context.Context, version string) (string, error) {
	name := p.instanceName(goldenPrefix, version)
	in, err := p.api.getInstance(ctx, name)
	if err != nil {
		return "", err
	}
	if !isOurs(in) {
		return "", fmt.Errorf("cloudsql: instance %q: %w", name, ErrNotOurs)
	}
	return name, nil
}

// label sets user labels on an instance, merging rather than replacing.
//
// Merging matters: a patch that sent only our own labels would drop whatever
// the customer's organisation policy put there, and a label somebody's billing
// export depends on is not ours to remove.
func (p *Provider) label(ctx context.Context, name string, add map[string]string) error {
	in, err := p.api.getInstance(ctx, name)
	if err != nil {
		return err
	}
	merged := map[string]string{}
	for k, v := range in.Settings.UserLabels {
		merged[k] = v
	}
	for k, v := range add {
		merged[k] = v
	}
	op, err := p.api.patchInstance(ctx, name, map[string]any{
		"settings": map[string]any{"userLabels": merged},
	})
	if err != nil {
		return fmt.Errorf("cloudsql: labelling %q: %w", name, err)
	}
	return p.api.waitForOperation(ctx, op, p.opts.PollInterval)
}

// setActivation starts or stops an instance's compute.
func (p *Provider) setActivation(ctx context.Context, name, policy string) error {
	op, err := p.api.patchInstance(ctx, name, map[string]any{
		"settings": map[string]any{"activationPolicy": policy},
	})
	if err != nil {
		return fmt.Errorf("cloudsql: setting activation policy of %q to %s: %w", name, policy, err)
	}
	return p.api.waitForOperation(ctx, op, p.opts.PollInterval)
}

// diskBytes reports an instance's provisioned disk in bytes.
func (p *Provider) diskBytes(ctx context.Context, name string) (int64, error) {
	in, err := p.api.getInstance(ctx, name)
	if err != nil {
		return 0, err
	}
	return diskBytesOf(in), nil
}

// diskBytesOf converts the API's string gibibyte count.
//
// SizeBytes is PROVISIONED disk rather than the size of the data, and that is
// the honest reading of what Cloud SQL exposes: the Admin API reports
// dataDiskSizeGb, which is what the instance was given, and the bytes actually
// occupied are a Postgres question this provider would have to open a
// connection to answer. Reporting the provisioned figure as though it were the
// data size would overstate every golden, so the field's meaning is written
// here rather than left for somebody to infer from a number that looks too
// round.
func diskBytesOf(in *instance) int64 {
	if in == nil {
		return 0
	}
	gb, err := strconv.ParseInt(in.Settings.DataDiskSizeGb, 10, 64)
	if err != nil {
		return 0
	}
	return gb * 1024 * 1024 * 1024
}
