package env

import (
	"bytes"
	"context"
	"os"

	"github.com/antifailure/antifailure/engine/internal/change"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
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
func (o *Orchestrator) Change(ctx context.Context, opts ChangeOptions) (*change.Profile, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	var (
		files     []change.File
		truncated bool
		err       error
		base      = opts.Base
		head      = opts.Head
	)
	if head == "" {
		head = "HEAD"
	}

	switch {
	case opts.DiffPath != "":
		body, rErr := os.ReadFile(opts.DiffPath)
		if rErr != nil {
			return nil, aferrors.Coded(aferrors.AFDET011,
				"path", opts.DiffPath, "detail", rErr.Error())
		}
		files, truncated, err = change.ParseUnifiedDiff(bytes.NewReader(body))
		if err != nil {
			return nil, aferrors.Coded(aferrors.AFDET011,
				"path", opts.DiffPath, "detail", err.Error())
		}
		base, head = opts.Base, opts.Head
	default:
		if base == "" {
			base, err = change.ResolveBase(ctx, o.opts.Root, getenv)
			if err != nil {
				return nil, err
			}
		}
		o.progress("reading the diff between " + base + " and " + head)
		files, truncated, err = change.FromGit(ctx, change.GitOptions{
			Dir: o.opts.Root, Base: base, Head: head,
		})
		if err != nil {
			return nil, err
		}
	}

	return change.Analyze(change.Options{
		Manifest:  o.opts.Manifest,
		Base:      base,
		Head:      head,
		Files:     files,
		Truncated: truncated,
	}), nil
}
