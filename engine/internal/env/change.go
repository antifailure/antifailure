package env

import (
	"context"

	"github.com/antifailure/antifailure/engine/internal/change"
)

// ChangeOptions are the parts of a change analysis the caller decides.
type ChangeOptions struct {
	// Base is the ref this change is measured against. Empty asks the
	// checkout, which is what a pull request job wants.
	Base string
	// Head is the ref being measured, defaulting to HEAD.
	Head string
	// DiffPath reads the diff from a file instead of from git, for a system
	// that already has one and a checkout that may not have the base ref.
	DiffPath string
	// Getenv is how the base ref is discovered from the job's environment.
	Getenv func(string) string
}

// Change classifies the diff between two refs against this project's manifest.
//
// It opens no session and touches no database, which is the point: this is the
// one thing the product can say about a pull request before anything is built,
// and making it depend on an environment being up would put it after the
// expense it exists to justify.
//
// The reading and the classifying both live in internal/change, so that the
// same code answers here and in the command's own path for a repository that
// has no manifest to build an orchestrator from. Two implementations of this
// would eventually disagree about what a diff touched, and the one nobody ran
// in anger would be the one that was wrong.
func (o *Orchestrator) Change(ctx context.Context, opts ChangeOptions) (*change.Profile, error) {
	return change.ForRepo(ctx, o.opts.Manifest, change.Source{
		Root:     o.opts.Root,
		Base:     opts.Base,
		Head:     opts.Head,
		DiffPath: opts.DiffPath,
		Getenv:   opts.Getenv,
		Progress: o.progress,
	})
}

// DependencyFiles reads the same diff Change classifies and returns the
// dependency manifests and lockfiles it touched, each with the lines the change
// adds. It exists because the profile Change returns carries the classification
// but not the added lines, and the supply_chain family reasons about what a
// dependency change ADDS: an install hook, a binary download, a source moved
// off the registry. It reads the diff, not the environment, so like Change it
// opens no session and touches no database.
//
// A file the diff deleted carries no added lines and is returned all the same,
// so a caller can tell a removed manifest from an absent one. A non-dependency
// file is dropped here rather than handed on, so the family reads only what it
// is meant to.
func (o *Orchestrator) DependencyFiles(ctx context.Context, opts ChangeOptions) ([]change.File, error) {
	files, _, _, _, err := change.Read(ctx, change.Source{
		Root:     o.opts.Root,
		Base:     opts.Base,
		Head:     opts.Head,
		DiffPath: opts.DiffPath,
		Getenv:   opts.Getenv,
		Progress: o.progress,
	})
	if err != nil {
		return nil, err
	}
	var deps []change.File
	for _, f := range files {
		if change.SurfaceOf(f.Path, o.opts.Manifest) == change.SurfaceDependency {
			deps = append(deps, f)
		}
	}
	return deps, nil
}

// CodeFiles reads the same diff Change classifies and returns the code files it
// touched, each with the lines the change adds. It is the counterpart of
// DependencyFiles for the static code reviewer, which reasons about what a diff
// ADDS to application code the way the supply_chain family reasons about what a
// dependency change adds, and the profile Change returns carries the
// classification but not the added lines. Like Change and DependencyFiles it
// reads the diff and not the environment, so it opens no session and touches no
// database.
//
// A file is a code file when its BASE surface is code, application authorization
// code, or a schema migration, which are the surfaces a line-by-line correctness
// review reads. Service attribution is deliberately not consulted here:
// SurfaceService is an additive fact layered onto a file that is already code by
// its path, never a base surface SurfaceOf returns, so filtering on the base
// surface already includes a service's own code and excludes the config and
// docs a review has nothing to say about.
func (o *Orchestrator) CodeFiles(ctx context.Context, opts ChangeOptions) ([]change.File, error) {
	files, _, _, _, err := change.Read(ctx, change.Source{
		Root:     o.opts.Root,
		Base:     opts.Base,
		Head:     opts.Head,
		DiffPath: opts.DiffPath,
		Getenv:   opts.Getenv,
		Progress: o.progress,
	})
	if err != nil {
		return nil, err
	}
	var code []change.File
	for _, f := range files {
		switch change.SurfaceOf(f.Path, o.opts.Manifest) {
		case change.SurfaceCode, change.SurfaceAuth, change.SurfaceSchema:
			code = append(code, f)
		}
	}
	return code, nil
}

// FileContent returns the head side contents of a changed path, so the static
// code reviewer can show the model the whole file as context around the lines a
// diff added rather than only the added lines. It reads the checkout with git
// and touches no environment, the same as Change and CodeFiles.
//
// A path absent at head, which a deletion or a ref with no such file produces,
// returns ok=false rather than an error: the reviewer falls back to the added
// lines alone, so a file it cannot fetch context for is reviewed narrowly, never
// dropped.
func (o *Orchestrator) FileContent(ctx context.Context, head, path string) (string, bool, error) {
	return change.FileAtRef(ctx, o.opts.Root, head, path)
}
