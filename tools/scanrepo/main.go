// Command scanrepo refuses a repository that carries a live credential.
//
// It uses the engine's own detector rather than a pattern list of its own, so
// the check that runs in CI and the refusal that runs in a proxy cannot
// disagree about what a credential looks like. That is also why the detector's
// own tests assemble fake keys at runtime instead of writing them out: a test
// fixture that looks like a key is a repository that fails this check.
//
// WHAT CHANGED AND WHY. This tool used to answer two questions with one
// sentence. "No live credentials in the tree" was printed both when it had
// read every file and found nothing, and when it had read NO FILES AT ALL.
// The walk swallowed its callback error, the stat error and the read error,
// each with a bare `return nil`, and nothing counted what was examined, so a
// root that does not exist produced the same confident line as a clean
// repository. Its own test suite pinned that: a case named
// TestAMissingRootIsNotASilentPass asserted the silent pass, which put a green
// tick under a name promising the hole was closed.
//
// So there are three answers here now rather than two, and the third is the
// one that was missing. It found something, it read the tree and found
// nothing, or it could not look. A scanner that skipped a file cannot say the
// tree is clean, and a scanner that read nothing at all is pointed at the
// wrong place.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/antifailure/antifailure/engine/pkg/livekey"
)

// skipDirs are not source and would make the scan slow and noisy.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "bin": true,
	".next": true, "vendor": true, "test-results": true, "playwright-report": true,
}

// maxFile bounds what is read. A credential does not live in the middle of a
// hundred megabyte fixture, and reading one would make the check slow enough
// that somebody removes it.
const maxFile = 4 << 20

// Finding is one credential, where it was found. The value is deliberately not
// carried: a report that quoted a credential would put it in the CI log of the
// job that found it.
type Finding struct {
	Path     string
	Provider string
	Prefix   string
}

// Result is what the scan found AND what it managed to look at. The second
// half is the point: len(Findings) == 0 means nothing was found, which is only
// the same as "nothing is there" when Files is large and Unreadable is empty.
type Result struct {
	// Findings is every live credential in the tree.
	Findings []Finding
	// Files is how many regular files were read to completion.
	Files int
	// Unreadable is every path the scan could not look at, with the reason.
	// A directory here takes its whole subtree with it.
	Unreadable []string
	// TooLarge is how many regular files were past maxFile and skipped by
	// policy rather than by failure. Reported, not refused.
	TooLarge int
}

// outcome is what this tool decides. Two exit codes, from four states: a
// credential was found, no file was read at all, a path could not be read, or
// the tree was read and is clean. The first three are refusals, and the middle
// two used to print the fourth one's sentence.
type outcome int

const (
	allowed outcome = iota
	refused
)

func (o outcome) String() string {
	if o == refused {
		return "refused"
	}
	return "allowed"
}

// verdict is the decision, split out from the printing so that a test can
// reach it. A check whose verdict lives inside main is a check whose verdict
// nothing has ever asserted.
func verdict(res Result) outcome {
	if len(res.Findings) > 0 || res.Files == 0 || len(res.Unreadable) > 0 {
		return refused
	}
	return allowed
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	res, err := scan(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scanrepo:", err)
		os.Exit(1)
	}
	for _, f := range res.Findings {
		fmt.Printf("%s: %s (%s)\n", f.Path, f.Provider, f.Prefix)
	}
	if len(res.Findings) > 0 {
		fmt.Fprintf(os.Stderr,
			"\n%d live credentials are committed to this repository. Rotate each one, then "+
				"remove it from the history.\n", len(res.Findings))
	}

	// The two ways of having looked at nothing, reported separately, because
	// they send somebody to different places.
	if res.Files == 0 {
		fmt.Fprintf(os.Stderr,
			"scanrepo: read 0 files under %s, so this check is looking in the wrong place "+
				"rather than at a clean tree. A scan that examined nothing cannot say a "+
				"repository carries no credential.\n", root)
	}
	if len(res.Unreadable) > 0 {
		fmt.Fprintf(os.Stderr,
			"scanrepo: read %d files and could not look at %d more:\n", res.Files, len(res.Unreadable))
		for _, u := range res.Unreadable {
			fmt.Fprintf(os.Stderr, "  %s\n", u)
		}
		fmt.Fprintf(os.Stderr,
			"\nA credential in one of those would not have been found. Make them readable "+
				"and run this again, or add the directory to skipDirs with a reason.\n")
	}

	if verdict(res) == refused {
		os.Exit(1)
	}

	if res.TooLarge > 0 {
		fmt.Printf("scanrepo: %d files read, %d past the %d MB bound and not read, "+
			"no live credentials in the tree\n", res.Files, res.TooLarge, maxFile>>20)
		return
	}
	fmt.Printf("scanrepo: %d files read, no live credentials in the tree\n", res.Files)
}

// scan walks a tree and reports every live credential in it, along with what it
// was able to read.
//
// Split out from main so that it can be tested. A gate nobody has proved can
// fail is a gate that passes everything the day it breaks, and this one is the
// difference between a rotated key and a published one.
//
// The walk continues past a path it cannot read rather than stopping, so that
// one unreadable directory produces a complete list of what was missed instead
// of the first entry in it. Every one of those is recorded, and main refuses on
// the list. Skipping quietly is what this tool used to do.
func scan(root string) (Result, error) {
	var res Result
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			res.Unreadable = append(res.Unreadable, fmt.Sprintf("%s (%v)", path, err))
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			res.Unreadable = append(res.Unreadable, fmt.Sprintf("%s (%v)", path, statErr))
			return nil
		}
		// Not a failure and not a hole worth refusing over: a socket, a
		// device, a symlink. There is nothing in one to read.
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > maxFile {
			res.TooLarge++
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			res.Unreadable = append(res.Unreadable, fmt.Sprintf("%s (%v)", path, readErr))
			return nil
		}
		res.Files++
		for _, f := range livekey.Scan(string(body), path) {
			res.Findings = append(res.Findings, Finding{Path: path, Provider: f.Provider, Prefix: f.Prefix})
		}
		return nil
	})
	sort.Strings(res.Unreadable)
	return res, err
}
