// Command notices generates the third party attribution file.
//
// Generated from what is actually linked rather than maintained by hand,
// because a list maintained by hand attributes the wrong people the moment
// somebody adds a dependency and forgets. MIT does not require a NOTICE file;
// the Apache licensed dependencies still require attribution, and getting that
// wrong is the kind of quiet unfairness nobody notices until it matters.
//
// The list is the union over every platform the release publishes, and that is
// not a refinement. `go list -deps` answers for one GOOS and GOARCH, so run on
// one platform this tool omits what only another links: modernc.org/libc
// imports github.com/ncruces/go-strftime on darwin and not on linux, so the
// release job, which runs on ubuntu, published a notice that attributed 88
// modules while the two darwin archives in the same release linked 89. That is
// an under attribution in the one file a legal reader opens, and the shape of
// it is worse than the count: nothing was wrong with any step, each one just
// answered about the platform it happened to be standing on.
//
// The union also makes the output host independent, which is what lets a gate
// compare it against the committed file at all. Before this, regenerating on a
// laptop and regenerating in CI produced two different files and whichever one
// a check ran on, the other was a failure waiting to be blamed on drift.
//
// The platforms are read out of the release workflow's build matrix rather than
// written here, for the reason ldcheck reads the same file: a second copy of a
// list is a list that goes stale silently. Add an architecture to the release
// and this file covers it on the next run.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type module struct {
	Path     string
	Version  string
	Indirect bool
	Main     bool
}

// target is one GOOS and GOARCH the release publishes an archive for.
type target struct{ os, arch string }

func (t target) String() string { return t.os + "/" + t.arch }

func main() {
	root := flag.String("root", ".", "repository root")
	workflow := flag.String("workflow", ".github/workflows/release.yml",
		"the workflow whose build matrix says which platforms ship")
	moduleDir := flag.String("module-dir", "engine", "the shipping module's directory")
	pkg := flag.String("package", "./cmd/af", "the package the release links")
	out := flag.String("out", "", "write here instead of stdout, replacing the file only once it is complete")
	flag.Parse()

	// A positional root as well, so this is invoked the same way as ldcheck and
	// errcheck beside it. Accepting an argument and ignoring it is how a check
	// ends up pointed at the wrong tree and passing.
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}

	targets, err := released(filepath.Join(*root, *workflow))
	if err != nil {
		fail("%v", err)
	}
	mods, err := linked(filepath.Join(*root, *moduleDir), *pkg, targets)
	if err != nil {
		fail("%v", err)
	}
	notices := render(targets, mods)
	if *out == "" {
		fmt.Print(notices)
		return
	}
	if err := replace(filepath.Join(*root, *out), notices); err != nil {
		fail("%v", err)
	}
}

// replace writes the file only once the whole of it exists.
//
// `go run ./tools/notices > THIRD_PARTY_NOTICES.md` truncates the file before
// the tool runs, so a generator that fails halfway leaves an empty legal notice
// in the tree it was meant to check. That is survivable in CI, which throws its
// checkout away, and it is not survivable in a working tree: the gate that
// caught the problem would have destroyed the file it was gating.
func replace(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	// CreateTemp makes the file 0600 and this one is committed, so it has to
	// end up with the mode the rest of the tree has rather than the mode a
	// temporary file has.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "notices: "+format+"\n", args...)
	os.Exit(1)
}

// os and arch out of one matrix entry, in either order, so that reordering the
// keys does not silently empty the list.
var (
	matrixOS   = regexp.MustCompile(`\bos:\s*([\w.-]+)`)
	matrixArch = regexp.MustCompile(`\barch:\s*([\w.-]+)`)
)

// released reads the platforms the release builds out of the release workflow.
//
// Deliberately not a YAML parser, the same choice gatecheck and ldcheck make
// about the same directory: it looks for the pair on a line and takes it. What
// matters is the other half, that finding nothing is a failure and never an
// empty list. A silent empty list here would produce a notices file with no
// modules in it, and every step downstream would stay green over a file that
// attributes nobody.
func released(path string) ([]target, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the release workflow: %w", err)
	}

	seen := map[target]bool{}
	var targets []target
	for _, line := range strings.Split(string(source), "\n") {
		goos := matrixOS.FindStringSubmatch(line)
		arch := matrixArch.FindStringSubmatch(line)
		if goos == nil || arch == nil {
			continue
		}
		t := target{os: goos[1], arch: arch[1]}
		if seen[t] {
			continue
		}
		seen[t] = true
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no build matrix entry in %s. The matrix is where the "+
			"released platforms are recorded, and a notices file generated from an "+
			"empty list of them attributes nobody", path)
	}

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].os != targets[j].os {
			return targets[i].os < targets[j].os
		}
		return targets[i].arch < targets[j].arch
	})
	return targets, nil
}

// linked returns the modules the engine actually builds against, on every
// platform it is built for.
//
// The build list rather than the requirement list, because a module that is
// required and not linked is not something the binary distributes, and
// attributing it would be as wrong as omitting one that is.
func linked(dir, pkg string, targets []target) ([]module, error) {
	seen := map[string]module{}
	for _, t := range targets {
		mods, err := listFor(dir, pkg, t)
		if err != nil {
			return nil, err
		}
		for _, m := range mods {
			// Selected versions come from minimal version selection, which does
			// not depend on the platform, so two targets disagreeing means an
			// assumption this file rests on has stopped holding. Say so rather
			// than keeping whichever arrived first.
			if was, ok := seen[m.Path]; ok && was.Version != m.Version {
				return nil, fmt.Errorf("%s resolves to %s on one released platform and "+
					"%s on another, so there is no single version to attribute",
					m.Path, was.Version, m.Version)
			}
			seen[m.Path] = m
		}
	}

	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })
	return mods, nil
}

// listFor is the build list for one platform.
func listFor(dir, pkg string, t target) ([]module, error) {
	cmd := exec.Command("go", "list", "-deps", "-json", pkg)
	cmd.Dir = dir
	// CGO_ENABLED=0 because that is how tools/release/build.sh builds what
	// ships. It makes no difference to this list today, and asking the question
	// under the settings the release uses is what keeps that true by accident
	// rather than by luck.
	cmd.Env = append(os.Environ(), "GOOS="+t.os, "GOARCH="+t.arch, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// go's own message names the package that would not load, and wrapping
		// the ExitError alone throws it away.
		return nil, fmt.Errorf("listing dependencies for %s: %w: %s",
			t, err, strings.TrimSpace(stderr.String()))
	}

	var mods []module
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p struct {
			Standard bool
			Module   *module
		}
		if err := dec.Decode(&p); err != nil {
			break
		}
		// The standard library needs no attribution, and the main module is
		// this project.
		if p.Standard || p.Module == nil || p.Module.Main {
			continue
		}
		mods = append(mods, *p.Module)
	}
	return mods, nil
}

func render(targets []target, mods []module) string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.String()
	}

	var b strings.Builder
	b.WriteString("# Third party notices\n\n")
	b.WriteString("Antifailure is MIT licensed, except for the `ee/` directory, which is\n")
	b.WriteString("licensed under the Antifailure Enterprise License. This file lists the\n")
	b.WriteString("dependencies the binary links, and is generated from them rather than\n")
	b.WriteString("maintained by hand, so it cannot go stale.\n\n")
	b.WriteString("The list is the union over every platform a release publishes, because\n")
	b.WriteString("one release ships all of them and a module can be linked on one platform\n")
	b.WriteString("and not another. A list taken from a single platform attributes too few\n")
	b.WriteString("people on every other one.\n\n")
	b.WriteString(wrap("Platforms: "+strings.Join(names, ", ")+".", 74))
	b.WriteString("\n")
	b.WriteString("Run `go run ./tools/notices > THIRD_PARTY_NOTICES.md` to regenerate it.\n")
	b.WriteString("CI checks that what is committed is what the generator produces.\n\n")

	fmt.Fprintf(&b, "## Go modules (%d)\n\n", len(mods))
	for _, m := range mods {
		fmt.Fprintf(&b, "- `%s` %s\n", m.Path, m.Version)
	}
	b.WriteString("\n## Node packages\n\n")
	b.WriteString("The agent runner depends on Playwright, which is Apache 2.0 licensed,\n")
	b.WriteString("and on its own transitive dependencies. Run `npm ls --all` inside\n")
	b.WriteString("`runner/` for the full tree of whatever version is installed.\n")
	return b.String()
}

// wrap keeps the generated prose inside the width the rest of the repository's
// markdown uses. The platform list grows when the release matrix does, and a
// line that grows without bound is how a generated file starts failing the
// readability gate for a reason nobody can see in the diff.
func wrap(text string, width int) string {
	var b strings.Builder
	column := 0
	for i, word := range strings.Fields(text) {
		switch {
		case i == 0:
			column = len(word)
		case column+1+len(word) > width:
			b.WriteString("\n")
			column = len(word)
		default:
			b.WriteString(" ")
			column += 1 + len(word)
		}
		b.WriteString(word)
	}
	b.WriteString("\n")
	return b.String()
}
