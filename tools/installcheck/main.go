// Command installcheck reports a node_modules that is not what the lockfile says.
//
// The failure it exists for cost most of an evening. An agent verified a
// week of `www` work with `www/node_modules` holding Next 15.5.23 against a
// lockfile pinning 16.3.3. Every build, every SEO assertion and a whole prose
// sweep were checked against a different Next major from the one CI uses. It
// re-ran everything after `npm ci` and the results happened to hold, but they
// held by luck: nothing anywhere had said the tree was wrong, and every gate
// reported success in good faith the whole time.
//
// It also explains an inconsistency several people chased separately and
// nobody could pin down. `next build` rewriting `www/tsconfig.json` is Next 16
// behaviour and does not happen on a stale 15 install, so the same command
// dirtied one person's worktree and not another's. It was never flakiness. It
// was who had last run `npm ci`. The same trap produced a bogus "Invalid config
// passed to starlight integration" in `docs` (Starlight 0.36.3 installed
// against a lockfile pinning 0.41.10) and an `ERR_MODULE_NOT_FOUND: drizzle-orm`
// that looked exactly like somebody else's branch being broken.
//
// A gate that checks the wrong tree and reports success is the defect this
// repository keeps finding, and this is that defect in the one place no gate
// was looking: not the code under test, but the toolchain testing it.
//
// It compares rather than installs, deliberately. `npm ci` in every recipe that
// builds would be correct and would make every local run pay for a full
// reinstall of a tree that is almost always already right; this answers the
// same question in milliseconds, needs no network, and can therefore run at the
// front of `just gate` and inside recipes that install nothing at all.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// npm writes node_modules/.package-lock.json on every install: its own record
// of the tree it actually materialised, keyed exactly like package-lock.json.
// That is the comparison, rather than reading versions out of each package's
// own package.json, because it is the file npm itself consults to decide
// whether a tree is up to date, and it covers every transitive dependency
// rather than the handful named in a manifest.
const hiddenLockfile = "node_modules/.package-lock.json"

// lockfile is the part of either file this needs. The rest of the format is
// deliberately not modelled: a field this does not read is a field that cannot
// break it when npm adds one.
type lockfile struct {
	Packages map[string]struct {
		Version  string `json:"version"`
		Optional bool   `json:"optional"`
		Resolved string `json:"resolved"`
		Link     bool   `json:"link"`
	} `json:"packages"`
}

// state is what one workspace is.
type state int

const (
	current  state = iota // installed and matching
	drifted               // installed and not matching
	absent                // never installed here
	partial               // installed, and something the lockfile requires is not there
	dangling              // installed, and the workspace it links into is not
)

// finding is one workspace's answer, with the evidence for it.
type finding struct {
	dir     string
	state   state
	details []string // "next 15.5.23 installed, 16.3.3 in the lockfile"
	// links are the workspaces this one resolves dependencies out of, which is
	// what makes the order it is installed in matter. See dangling.
	links []string
}

// fixCommand is what to actually run.
//
// `npm ci` everywhere, and the workspaces this one links into first, which is
// the part that is easy to get wrong. `npm ci` inside ee/web SUCCEEDS with web
// uninstalled and leaves a tree that does not work: the file: links to
// @antifailure/db and @antifailure/api resolve to source directories whose own
// dependencies are not there, and `npm run typecheck` then reports five
// implicit-any errors inside web/packages/db/src/schema.ts. Somebody reading
// that is looking at a type error in a file nobody touched.
//
// An earlier version of this printed `npm install` for ee/web, on the reasoning
// that ci.yml uses `install` there. Running it showed that `ci` works and
// `install` rewrites ee/web/package-lock.json, so the verb was never the point
// and the order always was.
func (f finding) fixCommand() string {
	cmd := ""
	for _, dep := range f.links {
		cmd += fmt.Sprintf("npm --prefix %s ci --no-audit --no-fund && ", dep)
	}
	return cmd + fmt.Sprintf("npm --prefix %s ci --no-audit --no-fund", f.dir)
}

func main() {
	// Two callers ask two different questions and they need two answers.
	//
	// A recipe about to use a tree asks "is this ready?", and a workspace with
	// no node_modules is not ready: it installs. `just gate` asks "did anything
	// already installed drift?", and a workspace nobody has installed cannot
	// have answered about the wrong versions, so it is not that gate's
	// business. Every recipe installs what it uses, so an absent tree is
	// somebody's job rather than a failure, and a gate that went red on a fresh
	// worktree for a workspace it was about to install anyway is exactly the
	// false alarm that gets a check deleted.
	driftOnly := flag.Bool("drift-only", false,
		"a workspace with no node_modules is reported and does not fail")
	flag.Parse()

	root := "."
	args := flag.Args()
	var only string
	if len(args) > 0 {
		root = args[0]
	}
	// A second argument narrows the check to one workspace, which is how a
	// recipe that installs only `www` asks about only `www`.
	if len(args) > 1 {
		only = filepath.ToSlash(args[1])
	}

	dirs, err := workspaces(root)
	if err != nil {
		fail("%v", err)
	}
	if len(dirs) == 0 {
		fail("found no package-lock.json under %s. Either the JavaScript "+
			"workspaces are gone, or this has stopped recognising them.", root)
	}

	var findings []finding
	for _, dir := range dirs {
		if only != "" && dir != only {
			continue
		}
		f, err := inspect(root, dir, dirs)
		if err != nil {
			fail("reading %s: %v", dir, err)
		}
		findings = append(findings, f)
	}
	if only != "" && len(findings) == 0 {
		fail("%s is not a directory with a package-lock.json, so there is no "+
			"installed tree to compare", only)
	}

	if code := report(os.Stderr, os.Stdout, findings, *driftOnly); code != 0 {
		os.Exit(code)
	}
}

// report writes the answer and returns the exit code. Both streams are
// parameters so a test can read what a person would read: a message that says
// the wrong thing is the same defect as a check that answers the wrong
// question, and this file exists because of one of those.
func report(errOut, out io.Writer, findings []finding, driftOnly bool) int {
	var bad []finding
	for _, f := range findings {
		if f.state != current {
			bad = append(bad, f)
		}
	}
	if len(bad) == 0 {
		_, _ = fmt.Fprintf(out, "installcheck: %d installed %s the lockfile\n",
			len(findings), plural(len(findings), "tree matches", "trees match"))
		return 0
	}

	// The two are separated because they are different problems with different
	// consequences. A tree that was never installed fails loudly the moment
	// anything runs: `ERR_MODULE_NOT_FOUND`, or a recipe that installs it for
	// you. A tree that drifted runs perfectly and answers about the wrong
	// versions, which is the one that costs an evening.
	// Three sections because they are three different sentences, and a header
	// that is true of one is false of the others. A drifted tree does not match
	// its lockfile. A tree installed before the workspace it links into matches
	// its lockfile exactly and still does not work. A tree that was never
	// installed has answered nothing at all.
	var never, stale, ordered []finding
	for _, f := range bad {
		switch f.state {
		case absent:
			never = append(never, f)
		case dangling:
			ordered = append(ordered, f)
		default:
			stale = append(stale, f)
		}
	}

	if len(stale) > 0 {
		_, _ = fmt.Fprintf(errOut, "installcheck: %d installed %s not what the lockfile says:\n",
			len(stale), plural(len(stale), "tree is", "trees are"))
		for _, f := range stale {
			_, _ = fmt.Fprintf(errOut, "  %s\n", f.dir)
			for _, d := range f.details {
				_, _ = fmt.Fprintf(errOut, "      %s\n", d)
			}
		}
		_, _ = fmt.Fprintf(errOut, "\nEverything checked against that tree answered about the wrong "+
			"versions, and said so in good faith.\n")
	}
	if len(ordered) > 0 {
		_, _ = fmt.Fprintf(errOut, "\ninstallcheck: %d %s installed before the workspace it links into:\n",
			len(ordered), plural(len(ordered), "tree was", "trees were"))
		for _, f := range ordered {
			_, _ = fmt.Fprintf(errOut, "  %s\n", f.dir)
			for _, d := range f.details {
				_, _ = fmt.Fprintf(errOut, "      %s\n", d)
			}
		}
		_, _ = fmt.Fprintf(errOut, "\n`npm ci` succeeds in that order and leaves a tree that does not work: "+
			"the\nfile: links resolve to source directories whose own dependencies are absent, "+
			"so\nthe errors land in a file nobody touched.\n")
	}
	if len(never) > 0 {
		_, _ = fmt.Fprintf(errOut, "\ninstallcheck: %d %s no node_modules at all:\n",
			len(never), plural(len(never), "workspace has", "workspaces have"))
		for _, f := range never {
			_, _ = fmt.Fprintf(errOut, "  %s\n", f.dir)
		}
		_, _ = fmt.Fprintf(errOut, "\nThat is a fresh checkout rather than drift. `just deps` installs "+
			"every workspace in the tree.\n")
	}

	_, _ = fmt.Fprintf(errOut, "\nFix:\n")
	for _, f := range bad {
		_, _ = fmt.Fprintf(errOut, "  %s\n", f.fixCommand())
	}
	if driftOnly && len(stale) == 0 && len(ordered) == 0 {
		return 0
	}
	return 1
}

// inspect compares one workspace's lockfile against what is installed.
func inspect(root, dir string, all []string) (finding, error) {
	f := finding{dir: dir}
	abs := filepath.Join(root, filepath.FromSlash(dir))

	want, err := readLock(filepath.Join(abs, "package-lock.json"))
	if err != nil {
		return f, err
	}
	f.links = linkedWorkspaces(dir, want, all)
	got, err := readLock(filepath.Join(abs, filepath.FromSlash(hiddenLockfile)))
	if os.IsNotExist(err) {
		f.state = absent
		return f, nil
	}
	if err != nil {
		return f, err
	}

	// Every package npm actually put on disk has to be the version the lockfile
	// pins. Comparing the other direction as an equality would be wrong: a
	// correct install is routinely missing entries, because package-lock.json
	// carries the optional platform binaries for every operating system and npm
	// materialises only this one's. www pins 113 packages and installs 55, and
	// all 58 it leaves out are optional.
	var details []string
	for name, installed := range got.Packages {
		if name == "" {
			continue
		}
		pinned, ok := want.Packages[name]
		if !ok {
			details = append(details, fmt.Sprintf("%s %s is installed and the lockfile does not have it",
				short(name), installed.Version))
			continue
		}
		if pinned.Version != installed.Version {
			details = append(details, fmt.Sprintf("%s %s installed, %s in the lockfile",
				short(name), installed.Version, pinned.Version))
		}
	}
	// And anything the lockfile requires had better be there. This is the
	// `ERR_MODULE_NOT_FOUND: drizzle-orm` case, which reads exactly like
	// somebody else's branch being broken and is not.
	var missing []string
	for name, pinned := range want.Packages {
		if name == "" || pinned.Optional || pinned.Resolved == "" {
			continue
		}
		if _, ok := got.Packages[name]; !ok {
			missing = append(missing, short(name))
		}
	}
	sort.Strings(missing)
	sort.Strings(details)

	// A tree that is installed and points into one that is not. `npm ci` here
	// succeeds and the result does not work, which is the third shape this
	// evening's stale installs took: an ERR_MODULE_NOT_FOUND, or type errors
	// inside a file in the OTHER workspace that nobody had touched.
	for _, dep := range f.links {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(dep), "node_modules")); err != nil {
			details = append(details, fmt.Sprintf(
				"it resolves dependencies out of %s with file: links, and %s has no node_modules",
				dep, dep))
			f.state = dangling
		}
	}

	if f.state == dangling {
		f.details = clip(details, 8)
		return f, nil
	}
	if len(missing) > 0 {
		f.state = partial
		details = append(details, fmt.Sprintf("%d %s the lockfile requires %s not installed: %s",
			len(missing), plural(len(missing), "package", "packages"),
			plural(len(missing), "is", "are"), strings.Join(clip(missing, 5), ", ")))
	} else if len(details) > 0 {
		f.state = drifted
	}
	f.details = clip(details, 8)
	return f, nil
}

// short trims the node_modules/ path npm keys packages by, so a message reads
// "next 15.5.23" rather than "node_modules/next 15.5.23".
func short(key string) string {
	if i := strings.LastIndex(key, "node_modules/"); i >= 0 {
		return key[i+len("node_modules/"):]
	}
	return key
}

func clip(list []string, n int) []string {
	if len(list) <= n {
		return list
	}
	out := append([]string{}, list[:n]...)
	return append(out, fmt.Sprintf("and %d more", len(list)-n))
}

func readLock(path string) (lockfile, error) {
	var l lockfile
	body, err := os.ReadFile(path)
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(body, &l); err != nil {
		return l, fmt.Errorf("%s is not a lockfile this can read: %w", path, err)
	}
	return l, nil
}

// linkedWorkspaces is the workspaces this one resolves dependencies out of.
//
// Read from the lockfile rather than from a list, so a new file: dependency
// between two workspaces is covered without anybody remembering this exists.
// ee/web's four packages reach into web/ today and nothing else does.
func linkedWorkspaces(dir string, lock lockfile, all []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, pinned := range lock.Packages {
		if !pinned.Link || !strings.HasPrefix(pinned.Resolved, "..") {
			continue
		}
		target := path.Clean(path.Join(dir, pinned.Resolved))
		// The workspace that owns the target is the longest lockfile directory
		// the target sits inside: ../../web/packages/db belongs to web.
		owner := ""
		for _, w := range all {
			if w == dir {
				continue
			}
			if target == w || strings.HasPrefix(target, w+"/") {
				if len(w) > len(owner) {
					owner = w
				}
			}
		}
		if owner != "" && !seen[owner] {
			seen[owner] = true
			out = append(out, owner)
		}
	}
	sort.Strings(out)
	return out
}

// workspaces returns every directory holding a package-lock.json.
//
// Found rather than listed, so a workspace added later is covered without
// anybody remembering this exists. There are eight today and the two places
// that named them by hand each named a different two.
//
// A lockfile directory is the right unit rather than a package.json directory:
// web/apps/api, web/packages/db, web/packages/policy and the four under ee/web
// are npm workspaces with no lockfile of their own, resolved and installed from
// the root that lists them, so checking web checks all of them.
func workspaces(root string) ([]string, error) {
	skip := map[string]bool{
		".git": true, "node_modules": true, "vendor": true,
		"testdata": true, "dist": true, "bin": true,
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skip[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "package-lock.json" {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "installcheck: "+format+"\n", args...)
	os.Exit(1)
}
