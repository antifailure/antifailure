// Package runnerpath answers one question in one place: where the agent runner
// might be.
//
// It exists because that question was answered twice, and the two answers
// drifted. `af runner install` learned to walk up to the top of the checkout,
// because anyone keeping a project in a subdirectory was told to install a
// runner the checkout already had. `af ci` kept its own copy of the search,
// which looked in the manifest's directory and exactly one level above it, and
// nobody moved the fix across. So `af runner install` worked from
// examples/django-api and `af ci` from the same directory reported AF-AGT-004
// naming four paths, none of which was the runner sitting at the top of the
// checkout it was running inside.
//
// Two copies of a search is the defect. One list, with the one difference
// between the callers named out loud, is the fix.
package runnerpath

import (
	"os"
	"path/filepath"
)

// Home is where `af runner install` puts its copy, and where a run looks for
// an installed one.
func Home() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".antifailure", "runner"), nil
}

// ToRun lists the runner directories a run may use, nearest first.
//
// dir is the directory holding the manifest, which is not reliably the top of
// the checkout: a customer whose manifest sits three directories down is the
// ordinary case, not the exotic one.
func ToRun(dir string) []string {
	candidates := inCheckout(dir)
	if home, err := Home(); err == nil {
		candidates = append(candidates, home)
	}
	return append(candidates, besideTheBinary()...)
}

// ToInstallFrom lists the runner directories `af runner install` may copy
// from, nearest first.
//
// The same list as ToRun with Home left out, and that omission is the only
// difference between the two. Home is this command's target. Offering it as a
// source would let an install copy a directory onto itself, and a stale runner
// left there by an older engine would become the source of the fresh one,
// which is how a release ships against a runner it was never tested with.
func ToInstallFrom(dir string) []string {
	return append(inCheckout(dir), besideTheBinary()...)
}

// inCheckout lists the runner directories at and above dir, stopping at the
// checkout dir sits in.
//
// This ascends because the working directory is not reliably the root of the
// checkout, and looking exactly one level up quietly assumed it was.
//
// The ascent stops at the directory holding .git rather than walking to the
// filesystem root. A walk to the root would eventually find an unrelated
// ~/runner belonging to something else and use that, which is a worse failure
// than the one this fixes: it succeeds, and the wrong runner is only visible
// later as a test that will not run.
//
// Outside any checkout there is no root to stop at, so only the two nearest
// directories are offered. That is exactly the pair this searched before, so
// the case that already worked still works, and the error does not list every
// directory up to / and bury the two that matter.
func inCheckout(dir string) []string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	var found []string
	for d := abs; ; {
		found = append(found, filepath.Join(d, "runner"))
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return found
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if len(found) > 2 {
		found = found[:2]
	}
	return found
}

// besideTheBinary is where an installed release keeps the runner it shipped
// with, rather than fetching one.
//
// Both layouts, because both exist: next to the binary for an archive somebody
// unpacked, and under ../share/antifailure for a package that follows the
// filesystem hierarchy. The run path used to know only the first, so on a
// packaged install `af runner install` could find the shipped runner and
// `af ci` could not.
func besideTheBinary() []string {
	self, err := os.Executable()
	if err != nil {
		return nil
	}
	dir := filepath.Dir(self)
	return []string{
		filepath.Join(dir, "runner"),
		filepath.Join(dir, "..", "share", "antifailure", "runner"),
	}
}
