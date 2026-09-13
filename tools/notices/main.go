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

	"github.com/antifailure/antifailure/engine/pkg/emulator"
)

type module struct {
	Path     string
	Version  string
	Indirect bool
	Main     bool
	// Dir is where the module cache holds the module, which is where its
	// licence and NOTICE files are read from. go list reports it with the rest.
	Dir string

	// Licence and Notices are filled in by attribute, never by go list.
	Licence string    `json:"-"`
	Notices []shipped `json:"-"`
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
	mods, err = attribute(mods, fileRules)
	if err != nil {
		fail("%v", err)
	}
	imgs, err := images(*root, "npm")
	if err != nil {
		fail("%v", err)
	}
	notices := render(targets, mods) + renderImages(imgs)
	if *out == "" {
		fmt.Print(notices)
		return
	}
	if err := replace(outPath(*root, *out), notices); err != nil {
		fail("%v", err)
	}
}

// outPath is where -out writes. A relative path is the repository's, which is
// how the gendrift ledger and the release workflow name it. An absolute path is
// exactly where it says: filepath.Join would have cleaned "." and
// "/tmp/notices.md" into "tmp/notices.md", so an absolute -out was written
// under whatever directory the generator ran in, or refused because that
// directory did not exist, after every package had already been attributed.
func outPath(root, out string) string {
	if filepath.IsAbs(out) {
		return out
	}
	return filepath.Join(root, out)
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
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
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
	b.WriteString(wrap("Antifailure is MIT licensed, except for the `ee/` directory, which is "+
		"licensed under the Antifailure Enterprise License. This file lists the third party "+
		"software each artifact carries, in a section per artifact: the af binary, the "+
		"community control plane image, and what the enterprise control plane image adds. "+
		"It is generated from what each one actually contains rather than maintained by "+
		"hand, and it travels inside each of them: in the af release archives, and in both "+
		"images at /usr/share/doc/antifailure/THIRD_PARTY_NOTICES.md.", 74))
	b.WriteString("\n")
	b.WriteString("Run `just generate` to regenerate it. `just _generated` and CI both\n")
	b.WriteString("regenerate it and fail on a difference, so a stale copy cannot be\n")
	b.WriteString("committed. It used to go stale anyway, because for a long time the\n")
	b.WriteString("generator ran only while building a release and nothing compared its\n")
	b.WriteString("output against this file.\n\n")

	b.WriteString("## The af binary\n\n")
	b.WriteString("The list is the union over every platform a release publishes, because\n")
	b.WriteString("one release ships all of them and a module can be linked on one platform\n")
	b.WriteString("and not another. A list taken from a single platform attributes too few\n")
	b.WriteString("people on every other one.\n\n")
	b.WriteString(wrap("Platforms: "+strings.Join(names, ", ")+".", 74))
	b.WriteString("\n")

	fmt.Fprintf(&b, "### Go modules (%d)\n\n", len(mods))
	for _, m := range mods {
		if m.Licence == "" {
			fmt.Fprintf(&b, "- `%s` %s\n", m.Path, m.Version)
			continue
		}
		fmt.Fprintf(&b, "- `%s` %s, %s\n", m.Path, m.Version, m.Licence)
	}
	renderNotices(&b, mods)
	b.WriteString("\n### Container images\n\n")
	b.WriteString("An environment starts an emulator when a manifest asks for one, and an\n")
	b.WriteString("emulator is somebody else's software running beside the application.\n")
	b.WriteString("It is not linked into the binary, so the module list above cannot see\n")
	b.WriteString("it, and an image whose licence is recorded by hand goes stale the\n")
	b.WriteString("first time a digest is bumped. These come from the declarations the\n")
	b.WriteString("engine starts the containers from.\n\n")
	for _, e := range emulator.Builtin() {
		fmt.Fprintf(&b, "- %s, %s\n", e.Project, e.Licence.Name)
		// A copyright holder is prose somebody else wrote and its length is
		// theirs, not ours, so it is wrapped rather than truncated or left to
		// run past the width every other line here is held to.
		for _, l := range wrapAt(e.Licence.Holder, 70) {
			fmt.Fprintf(&b, "  %s\n", l)
		}
		fmt.Fprintf(&b, "  - Answers for %s as `%s`\n", e.Vendor, e.Name())
		// The repository and the digest on separate lines, because a
		// sha256 digest is 71 characters of unbreakable token and the
		// generated prose is held to 74. Splitting at the @ is the only
		// wrap point a digest reference has.
		repo, digest, _ := strings.Cut(e.Image, "@")
		fmt.Fprintf(&b, "  - `%s` pinned at\n", repo)
		fmt.Fprintf(&b, "  %s\n", digest)
		fmt.Fprintf(&b, "  - %s\n", e.Licence.URL)
	}

	b.WriteString("\n### Node packages\n\n")
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

// wrapAt breaks a line on spaces at a width. A word longer than the width
// stays on its own line rather than being cut, because a token nobody can
// break is a value and shortening it would make it wrong.
func wrapAt(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	lines := []string{words[0]}
	for _, w := range words[1:] {
		last := len(lines) - 1
		if len(lines[last])+1+len(w) <= width {
			lines[last] += " " + w
			continue
		}
		lines = append(lines, w)
	}
	return lines
}

// renderNotices reproduces the NOTICE files and notices documents the modules
// ship, each inside a fence longer than any it contains.
//
// Verbatim, because the obligation is to reproduce the file and a paraphrase
// does not. Fenced, because the text is somebody else's: its line lengths,
// punctuation and names are not this repository's prose, and every prose gate
// that reads this file already leaves a fenced block alone.
func renderNotices(b *strings.Builder, mods []module) {
	count := 0
	for _, m := range mods {
		count += len(m.Notices)
	}
	if count == 0 {
		return
	}
	b.WriteString("\n#### Notices the modules ship\n\n")
	b.WriteString(wrap("Reproduced as each module ships them, because the Apache License 2.0 "+
		"asks in section 4(d) that a NOTICE file distributed with a work be carried with "+
		"any redistribution of it.", 74))
	for _, m := range mods {
		for _, n := range m.Notices {
			fence := fenceFor(n.text)
			fmt.Fprintf(b, "\n##### `%s` %s\n\n%stext\n%s\n%s\n", m.Path, n.file, fence,
				strings.TrimRight(n.text, "\n"), fence)
		}
	}
}

// imageNotices is what the two control plane images carry beyond the af binary.
type imageNotices struct {
	community  []npmPackage // the community image's own install
	console    []npmPackage // what the console's static export bundles
	enterprise []npmPackage // what the enterprise image's installs add to the community set
}

// images generates the control plane image sections from each image's own
// installs and from a build of the console.
//
// Every temporary directory is removed when this returns, success or not. That
// is why this is a function returning an error rather than work done in main,
// whose failure path exits the process and would skip the removals.
func images(root, npm string) (imageNotices, error) {
	var in imageNotices
	install := func(file, stageName string) ([]npmPackage, error) {
		st, err := parseStage(filepath.Join(root, filepath.FromSlash(file)), stageName)
		if err != nil {
			return nil, err
		}
		tmp, err := os.MkdirTemp("", "af-notices-")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		dir, err := replay(root, st, tmp, npm, gitTracked)
		if err != nil {
			return nil, err
		}
		return installed(dir)
	}

	community, err := install("deploy/docker/control-plane.Dockerfile", "deps")
	if err != nil {
		return in, err
	}
	// The enterprise image's first install is read from its own Dockerfile rather
	// than assumed to match the community one. Today they match; the day they do
	// not, the additions below say so instead of hiding it.
	enterpriseWeb, err := install("deploy/docker/control-plane-enterprise.Dockerfile", "deps")
	if err != nil {
		return in, err
	}
	enterpriseEE, err := install("deploy/docker/control-plane-enterprise.Dockerfile", "eedeps")
	if err != nil {
		return in, err
	}

	tmp, err := os.MkdirTemp("", "af-notices-console-")
	if err != nil {
		return in, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	console, err := consoleBundle(root, npm, tmp)
	if err != nil {
		return in, err
	}

	in.community = community
	in.console = console
	in.enterprise = additions(append(append([]npmPackage{}, enterpriseWeb...), enterpriseEE...), community)
	return in, nil
}

// additions returns the packages in pkgs that are not in base, by name and
// version, once each and sorted.
func additions(pkgs, base []npmPackage) []npmPackage {
	have := map[string]bool{}
	for _, p := range base {
		have[p.Name+"@"+p.Version] = true
	}
	var out []npmPackage
	for _, p := range pkgs {
		key := p.Name + "@" + p.Version
		if have[key] {
			continue
		}
		have[key] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// renderImages writes the control plane image sections, each marked with the
// artifact it describes and saying how its licences were read.
func renderImages(in imageNotices) string {
	var b strings.Builder
	b.WriteString("\n## The community control plane image\n\n")
	b.WriteString(wrap("What deploy/docker/control-plane.Dockerfile puts into the image, read "+
		"from what the image actually carries rather than from a lockfile. The npm packages "+
		"come from replaying the image's own npm ci with the same flags. The console is "+
		"listed as its static export bundles it, measured from a build with source maps, "+
		"with every static asset traced to the file it is identical to. A licence is read "+
		"from the text a package ships, and a package that ships no licence text is "+
		"attributed from its declaration and says so.", 74))
	fmt.Fprintf(&b, "\n### npm packages (%d)\n\n", len(in.community))
	writePackages(&b, in.community)
	fmt.Fprintf(&b, "\n### The console export (%d)\n\n", len(in.console))
	writePackages(&b, in.console)
	writePackageNotices(&b, append(append([]npmPackage{}, in.community...), in.console...))

	b.WriteString("\n## The enterprise control plane image, in addition\n\n")
	b.WriteString(wrap("The enterprise image carries everything in the community image section "+
		"above, and its second install, the eedeps stage of "+
		"deploy/docker/control-plane-enterprise.Dockerfile, adds the packages below. It "+
		"carries no Go binary of its own: tools/release/build.sh builds only engine/cmd/af, "+
		"the release workflow builds nothing else, and the enterprise image's dockerignore "+
		"excludes ee/engine, so there are no enterprise Go modules to attribute.", 74))
	fmt.Fprintf(&b, "\n### npm packages (%d)\n\n", len(in.enterprise))
	writePackages(&b, in.enterprise)
	writePackageNotices(&b, in.enterprise)
	return b.String()
}

func writePackages(b *strings.Builder, pkgs []npmPackage) {
	if len(pkgs) == 0 {
		b.WriteString("None.\n")
		return
	}
	for _, p := range pkgs {
		fmt.Fprintf(b, "- `%s` %s, %s", p.Name, p.Version, p.Licence)
		if p.Declared {
			b.WriteString(" (declared, no licence file shipped)")
		}
		b.WriteString("\n")
	}
}

func writePackageNotices(b *strings.Builder, pkgs []npmPackage) {
	count := 0
	for _, p := range pkgs {
		count += len(p.Notices)
	}
	if count == 0 {
		return
	}
	b.WriteString("\n### Notices the packages ship\n")
	for _, p := range pkgs {
		for _, n := range p.Notices {
			fence := fenceFor(n.text)
			fmt.Fprintf(b, "\n#### `%s` %s\n\n%stext\n%s\n%s\n", p.Name, n.file, fence,
				strings.TrimRight(n.text, "\n"), fence)
		}
	}
}
