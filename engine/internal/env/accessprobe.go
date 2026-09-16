package env

import (
	"context"
	"os"
	"path/filepath"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The access-probe pass is the producer half of the authenticated authorization
// differential. The engine reads the manifest's access fixtures, resolves each
// object's owning identity, and sends the runner a list of objects to reach as
// every persona. The runner reaches each one, decides whether the object's
// planted canary came back, and emits one structured observation per reach. The
// observations ride the same JSON boundary an exploration does, so the authz
// family reads them through the same collector, and a persona that reached
// another owner's object and got the canary back becomes a proven finding.
//
// It goes through the same runner subprocess and the same document boundary an
// exploration does, for the same reason Explore reuses Test's: the sign in, the
// browser and the evidence capture are identical, and a second entry point is a
// second place for the boundary to drift.

// AccessProbeOptions configure an access-probe pass.
type AccessProbeOptions struct {
	// RunnerPath overrides where the runner is found, for a test that stands up
	// a fake one. Empty uses the resolved default.
	RunnerPath string
}

// HasAccessProbes reports whether a manifest declares any access-probe objects,
// so a caller can skip the pass, and the second environment it would drive,
// when there is nothing to reach. This is what keeps the manifest off by
// default: no access block, or an empty one, means no probing and no cost.
func HasAccessProbes(m *schema.Manifest) bool {
	return m != nil && m.Security != nil && m.Security.Access != nil &&
		len(m.Security.Access.Objects) > 0
}

// AccessProbe drives the runner over the manifest's access fixtures and returns
// the observations it made, as an exploration report the collector folds into
// the authz family's candidate.
//
// It returns an error only for a fact about the tooling: no environment is up,
// or the runner would not run. A run that reached the twin and found no
// boundary crossed is a successful report with benign observations, never an
// error, because the absence of a violation is the answer this pass most often
// gives and it must not read as a blocked probe.
func (o *Orchestrator) AccessProbe(ctx context.Context, opts AccessProbeOptions) (*explore.Report, error) {
	objects := o.accessProbeDocs()
	if len(objects) == 0 {
		// Nothing declared, so nothing to reach. Reported as no report rather
		// than an empty one: the caller only runs this when HasAccessProbes is
		// true, and an empty result here would be a run that reached nothing
		// dressed as a run that found nothing.
		return nil, nil
	}

	status, err := o.Status(ctx)
	if err != nil {
		return nil, err
	}
	if status.URL == "" {
		return nil, aferrors.Coded(aferrors.AFAGT020,
			"detail", "nothing is running for this branch; bring it up with 'af up' first")
	}

	// The personas have to exist before the browser opens, exactly as an
	// exploration's do: an access probe handed an account nobody created reports
	// a sign in the application refused, which reads as a finding about the
	// application and is a fact about the environment.
	provisioned, err := o.ProvisionPersonas(ctx)
	if err != nil {
		return nil, err
	}

	job := jobDocument{
		BaseURL:      status.URL,
		Artifacts:    filepath.Join(o.opts.Root, StateDir, "artifacts", o.envID),
		AccessProbes: objects,
		Personas:     o.personaDocs(provisioned),
		WorkDir:      o.opts.Root,
		Headless:     true,
	}
	if self, err := os.Executable(); err == nil {
		job.AF = self
	}

	out, err := o.invokeRunner(ctx, opts.RunnerPath, job)
	if err != nil {
		return nil, err
	}
	return decodeExplorationReport(out)
}

// accessProbeDocs turns the manifest's access fixtures into what the runner
// reads, resolving each object's owning identity. An owner named as a persona
// is resolved to that persona's user, role and tenant, so the ownership is one
// source of truth; a tenant on the fixture still wins, so a cross-tenant reach
// can be expressed against personas that carry no tenant. An explicit owner is
// passed through as given.
//
// The canary value rides along so the runner can decide content presence inside
// the run against the object's own planted token. The value never leaves the
// engine in a finding; the runner emits only whether it was present.
func (o *Orchestrator) accessProbeDocs() []accessProbeDoc {
	if !HasAccessProbes(o.opts.Manifest) {
		return nil
	}
	byName := map[string]schema.Persona{}
	for _, p := range o.opts.Manifest.Personas {
		byName[p.Name] = p
	}
	var out []accessProbeDoc
	for _, obj := range o.opts.Manifest.Security.Access.Objects {
		owner := resolveOwner(obj.Owner, byName)
		out = append(out, accessProbeDoc{
			Route:       obj.Route,
			ID:          obj.ID,
			ObjectClass: obj.ObjectClass,
			Canary:      obj.Canary,
			Owner:       owner,
		})
	}
	return out
}

// resolveOwner turns a declared owner into a concrete identity. A persona owner
// takes its user and role from the persona; its tenant is the fixture's when the
// fixture gives one, otherwise the persona's, so a tenant can be pinned on the
// fixture for an application whose personas carry none. An explicit owner is
// used verbatim.
func resolveOwner(owner schema.AccessOwner, byName map[string]schema.Persona) accessOwnerDoc {
	if owner.Persona != "" {
		p := byName[owner.Persona]
		tenant := owner.Tenant
		if tenant == "" {
			tenant = p.Tenant
		}
		return accessOwnerDoc{Tenant: tenant, User: p.Name, Role: p.Role}
	}
	return accessOwnerDoc{Tenant: owner.Tenant, User: owner.User, Role: owner.Role}
}
