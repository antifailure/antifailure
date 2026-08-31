package change

import (
	"bytes"
	"context"
	"os"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Source says where a diff comes from and what to measure it against.
//
// It lives here rather than in the orchestrator because reading a diff needs
// no environment: no image is built, no database is started, nothing is
// migrated. That is the whole argument for this command running first, and it
// is also why a repository with no antifailure.yaml can still be asked what a
// change touches. The answer in that case is every check reported unavailable,
// which is a true and useful thing to be told.
type Source struct {
	// Root is the repository to read the diff out of.
	Root string
	// Base is the ref the change is measured against. Empty asks the checkout,
	// which is what a pull request job wants.
	Base string
	// Head is the ref being measured, defaulting to HEAD.
	Head string
	// DiffPath reads a unified diff from a file instead of from git, for a
	// system that already has one and a checkout that may not have the base
	// ref.
	DiffPath string
	// Getenv is how the base ref is discovered from the job's environment.
	// Nil falls back to the process environment.
	Getenv func(string) string
	// Progress is told what is being read, and may be nil.
	Progress func(string)
}

// Read returns the diff the source names, along with the refs it ended up
// using, so the report can say what was actually compared rather than what was
// asked for.
func Read(ctx context.Context, s Source) (files []File, base, head string, truncated bool, err error) {
	getenv := s.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	base, head = s.Base, s.Head
	if head == "" {
		head = "HEAD"
	}

	if s.DiffPath != "" {
		body, rErr := os.ReadFile(s.DiffPath)
		if rErr != nil {
			return nil, base, head, false, aferrors.Coded(aferrors.AFDET011,
				"path", s.DiffPath, "detail", rErr.Error())
		}
		files, truncated, err = ParseUnifiedDiff(bytes.NewReader(body))
		if err != nil {
			return nil, base, head, truncated, aferrors.Coded(aferrors.AFDET011,
				"path", s.DiffPath, "detail", err.Error())
		}
		// A diff read from a file carries no refs of its own, so the report
		// says only what the caller told it, and says nothing when the caller
		// said nothing. Inventing "HEAD" here would name a comparison that was
		// never made.
		return files, s.Base, s.Head, truncated, nil
	}

	if base == "" {
		base, err = ResolveBase(ctx, s.Root, getenv)
		if err != nil {
			return nil, base, head, false, err
		}
	}
	if s.Progress != nil {
		s.Progress("reading the diff between " + base + " and " + head)
	}
	files, truncated, err = FromGit(ctx, GitOptions{Dir: s.Root, Base: base, Head: head})
	if err != nil {
		return nil, base, head, truncated, err
	}
	return files, base, head, truncated, nil
}

// ForRepo reads a diff and classifies it against a manifest.
//
// The manifest may be nil. That is not a degenerate case to be defended
// against but the first run in a repository nobody has configured yet: the
// facts about what the diff touches are all still true, and every check is
// reported unavailable with the reason being that there is no manifest. A
// command that refused to answer without one would be withholding the only
// part of this product that costs nothing to produce.
func ForRepo(ctx context.Context, m *schema.Manifest, s Source) (*Profile, error) {
	files, base, head, truncated, err := Read(ctx, s)
	if err != nil {
		return nil, err
	}
	return Analyze(Options{
		Manifest: m, Base: base, Head: head, Files: files, Truncated: truncated,
	}), nil
}
