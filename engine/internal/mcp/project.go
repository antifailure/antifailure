package mcp

import (
	"path/filepath"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Project is the one repository this server serves.
//
// One, fixed at startup, chosen by whoever launched the process. That is the
// whole tenancy model and it is deliberate: the engine has no notion of a
// project as a thing to be selected among. What the control plane calls a
// tenant it calls an organization and a repository, and what the engine calls
// a project is a Neon database project, which is a different thing entirely.
// Inventing a third meaning here would be a parallel tenancy model with no
// backing.
//
// So project_id in a tool call is an assertion, never a selector. A caller
// that names this project is answered; a caller that names another is refused.
// It can narrow or refuse and it can never widen, which is the strongest form
// of "it never grants access" available.
type Project struct {
	// Root is the absolute, symlink resolved checkout root.
	Root string
	// ID is the stable name a caller may assert. It comes from the manifest,
	// which is checked into the repository, so it is the same for everyone
	// working on it rather than an artifact of one person's directory layout.
	ID string
	// ManifestPath is where the manifest was found.
	ManifestPath string
	// Manifest is the loaded, validated manifest.
	Manifest *schema.Manifest
	// Gate is the project's own thresholds, resolved from the manifest's
	// policy block.
	//
	// This is the trusted policy the deterministic evaluator uses, and it is
	// read here so that no tool ever has to be handed one. A threshold cannot
	// arrive from a call because there is no argument that carries one and no
	// field on this struct that a call can reach.
	Gate report.Policy
}

// BindProject finds the manifest above workDir and resolves the project.
//
// It fails rather than degrading. A server that started without a manifest
// would accept calls and refuse every one of them at the point where the
// experiment was supposed to begin, which wastes a caller's time to report
// something that was knowable at startup.
func BindProject(workDir string) (*Project, error) {
	path, err := manifest.Find(workDir)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Load(path)
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(path)
	// Resolved once, here, so that every later containment check compares
	// resolved paths against a resolved root. Comparing a resolved target
	// against an unresolved root is how a checkout reached through a symlink
	// makes every legitimate path look like an escape.
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}

	id := strings.TrimSpace(m.Name)
	if id == "" {
		// A manifest with no name still has to be identifiable, and the
		// directory is the only other thing both ends can see.
		id = filepath.Base(root)
	}
	return &Project{
		Root: root, ID: id, ManifestPath: path,
		Manifest: m, Gate: report.Configure(m.Policy),
	}, nil
}

// checkAssertion refuses a call that names a different project.
//
// An absent project_id is accepted, because there is exactly one project and a
// caller that did not name it cannot have meant a different one. A present one
// must match, so that a client configured against two checkouts cannot run an
// experiment on the wrong repository and read the verdict as though it were
// the right one.
func (p *Project) checkAssertion(args map[string]any) *Fault {
	raw, present := args["project_id"]
	if !present {
		return nil
	}
	asserted, ok := raw.(string)
	if !ok {
		return fieldFault(FaultInvalidArgument, "project_id", "This field must be a string.")
	}
	if asserted != p.ID {
		return fieldFault(FaultProjectMismatch, "project_id",
			"This server serves the project %q. It cannot run an experiment against "+
				"another project; start a server in that repository instead.", p.ID)
	}
	return nil
}

// projectIDSchema is the shared declaration, so that every tool describes the
// field identically and none of them can accidentally describe it as a way to
// choose something.
func projectIDSchema() *Schema {
	return &Schema{
		Type: "string", MaxLength: 200,
		Description: "Optional. The project this server serves, as a check that you are " +
			"talking to the repository you meant. It selects nothing and grants nothing: " +
			"a value naming another project is refused rather than followed.",
	}
}
