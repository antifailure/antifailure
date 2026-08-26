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
	"strings"

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

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	found := 0
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
			// The finding names the kind and the file, and never the value.
			// A report that quoted the credential would put it in the CI log
			// of the job that found it.
			fmt.Printf("%s: %s (%s)\n", path, f.Provider, f.Prefix)
			found++
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "scanrepo:", err)
		os.Exit(1)
	}
	if found > 0 {
		fmt.Fprintf(os.Stderr,
			"\n%d live credentials are committed to this repository. Rotate each one, then "+
				"remove it from the history.\n", found)
		os.Exit(1)
	}
	fmt.Println("scanrepo: no live credentials in the tree")
	_ = strings.TrimSpace
}
