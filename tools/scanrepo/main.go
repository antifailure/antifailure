// Command scanrepo refuses a repository that carries a live credential.
//
// It uses the engine's own detector rather than a pattern list of its own, so
// the check that runs in CI and the refusal that runs in a proxy cannot
// disagree about what a credential looks like. That is also why the detector's
// own tests assemble fake keys at runtime instead of writing them out: a test
// fixture that looks like a key is a repository that fails this check.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

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

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	found, err := scan(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scanrepo:", err)
		os.Exit(1)
	}
	for _, f := range found {
		fmt.Printf("%s: %s (%s)\n", f.Path, f.Provider, f.Prefix)
	}
	if len(found) > 0 {
		fmt.Fprintf(os.Stderr,
			"\n%d live credentials are committed to this repository. Rotate each one, then "+
				"remove it from the history.\n", len(found))
		os.Exit(1)
	}
	fmt.Println("scanrepo: no live credentials in the tree")
}

// scan walks a tree and reports every live credential in it.
//
// Split out from main so that it can be tested. A gate nobody has proved can
// fail is a gate that passes everything the day it breaks, and this one is the
// difference between a rotated key and a published one.
func scan(root string) ([]Finding, error) {
	var found []Finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > maxFile || !info.Mode().IsRegular() {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, f := range livekey.Scan(string(body), path) {
			found = append(found, Finding{Path: path, Provider: f.Provider, Prefix: f.Prefix})
		}
		return nil
	})
	return found, err
}
