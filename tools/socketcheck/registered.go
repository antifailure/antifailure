package main

// The third question, and the one the first two could not ask.
//
// Consulted means the engine READS a registry. It says nothing about whether
// anything was ever PUT in it, and on 2026-09-11 that difference was about
// eleven hundred lines wide. engine/internal/env/emulator.go called
// EmulatorNamed, so the emulator socket counted as consulted and this gate
// reported every socket plugged in. emulator.RegisterBuiltin had zero callers,
// so the registry that call reads was empty in every binary anybody could run,
// and a manifest naming any of the AWS, Azure or GCP emulators this repository
// declares was refused with "this build has no emulators registered at all".
// A green gate, an accurate gate, and a dead capability, at the same time, and
// two documentation pages telling a buyer the emulators answered.
//
// So each socket is now asked a second thing: does a binary a customer runs
// register anything into it. Not a test, which proves the registry works and
// says nothing about the product. Not a tool, which nobody installs. Every main
// package in the repository is named below as shipped or as not shipped, with
// the reason, and one nobody named fails the gate, because whether a binary
// ships decides whether its registrations count and that is not something to
// infer from a directory name.
//
// Some sockets are empty ON PURPOSE, and telling those from the emulator is the
// whole difficulty. The golden store and the datastore provider have built in
// switches that run before the registry is asked, so an empty socket there is
// the shipped behaviour rather than a missing registration. Those are listed in
// notRegistered with the reason, and the list works like notConsulted in both
// directions: an empty socket nobody listed fails, and a listed socket that a
// shipped binary has since started registering fails too, so an entry cannot
// outlive the gap it describes. A blank reason fails, and so does an entry
// naming a socket that does not exist, because both are exemptions nobody can
// reread.
//
// HOW REACHED IS DECIDED, because "somewhere in the tree calls AddEmulator"
// would have passed on the exact case this was written for:
// RegisterBuiltin's own body calls AddEmulator. This is a reachability walk
// over the syntax tree, the way engine/pkg/airgap/guarded_test.go is a walk
// rather than a grep, and for the same reason: engine/internal/docs/
// pages.gen.go carries a documentation sample that calls AddDatabaseProvider
// inside a Go string literal, which a grep counts and a walk does not.
//
//  1. The packages a binary can contain are its main package and everything it
//     imports from this repository's own modules, found by reading the go.mod
//     files rather than assuming the module paths, and following the enterprise
//     module's replace of the engine exactly as the compiler does.
//  2. A file counts only if some platform in release.yml's build matrix
//     compiles it: its _GOOS and _GOARCH name suffixes and its go:build line
//     are evaluated for each, with cgo off because tools/release/build.sh
//     builds with CGO_ENABLED=0. A registration only a Windows build would
//     make is not a registration anybody receives.
//  3. A function counts only if it is reachable from main, from an init, or
//     from a package level initializer, following every function a reachable
//     body names. A registration inside a function nothing calls is exactly
//     what RegisterBuiltin was, one package over.
//
// WHAT IT DOES NOT CHECK, said here and printed in the report. It does not type
// check. A method call is followed by NAME to every method of that name in the
// binary's packages, so a dead registration sitting in a method that shares a
// name with a live one would count as reached; that errs toward passing and is
// the one generous choice, taken because following interface calls exactly
// needs the type checker. A registration made from a module outside this
// repository is not seen. A method only the standard library calls through an
// interface, such as ServeHTTP, is not followed, so a registration placed
// there would be reported as unreached; that errs toward failing, which is the
// right way round. Anything it cannot follow at all, a first party import with
// no package behind it, a dot import, a go.mod with no module line, a release
// workflow with no build matrix, stops the check with an error rather than
// quietly checking less.

import (
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// notRegistered names a socket that no binary a customer runs puts anything
// into, and why that is deliberate.
//
// Emulator is NOT here, and that absence is the point of this list. Its
// registration exists, emulator.RegisterBuiltin, and nothing shipped calls it,
// so the gate fails on it until something does. An entry for it would have
// turned the finding this direction was written for into a line nobody reads.
var notRegistered = map[string]string{
	"DatastoreProvider": "ClickHouse, the one datastore engine this product provides, is " +
		"built into newDatastoreProvider in engine/internal/env/datastores.go and reached " +
		"whenever nothing is registered under the datastore's provider name, which in a " +
		"shipped binary is always. Every other engine is refused by name with AF-MAN-002. The " +
		"socket is for an engine somebody's own build adds, and provider.BuiltInDatastoreEngines " +
		"leaves registrations out on purpose. The entry goes when a shipped binary registers one.",
	"GoldenStore": "The four places this product publishes a golden, local, azure_blob, s3 " +
		"and gcs, are the switch in engine/internal/golden/store.go, which runs before the " +
		"registry is asked. An empty socket is therefore the shipped behaviour rather than a " +
		"missing registration: it is there for a kind somebody's own build adds. The entry " +
		"goes when a shipped binary registers a store.",
	"LifecycleHook": "Nothing in this repository implements LifecycleHook, in either edition, " +
		"so there is nothing a shipped binary could register. The engine calls Observe so " +
		"that a metering or inventory hook in somebody's own build hears about every " +
		"environment, which is what the interface's own comment says it is for. The entry " +
		"goes when an implementation ships and a binary registers it.",
}

// shipped names every main package a customer runs, and how it reaches them.
//
// This is a list rather than a discovery on purpose. What ships is decided by
// a release script, an image build and a licence page, none of which a syntax
// tree can read, and a guess from directory names would have to be right about
// all three. So the gate refuses a main package that is on neither list and
// an entry naming a main package that no longer exists.
var shipped = map[string]string{
	"engine/cmd/af": "the community binary. tools/release/build.sh builds ./cmd/af into " +
		"every release archive, and it is the only Go program that script ships.",
	"ee/engine/cmd/af": "the enterprise binary. The enterprise documentation tells a licensed " +
		"customer to run the binary built from ee/, and its main.go is where every enterprise " +
		"feature is registered. The release workflow does not build it, which is why this " +
		"entry, rather than the workflow, is what says it ships.",
	"engine/cmd/af-proxy": "the sidecar. tools/proxysrc carries its sources into the proxy " +
		"image, which is built on the customer's machine and runs inside every environment.",
}

// notShipped names every other main package, or a whole module of them, and
// why no customer runs it.
var notShipped = map[string]string{
	"tools": "the tools module generates and gates the product. go.work says the tools do " +
		"not ship, and nothing from the module is copied into a release archive.",
	"engine/cmd/capacityplan": "a calculator an operator runs with go run to size a cluster. " +
		"tools/release/build.sh builds ./cmd/af and nothing else from this module.",
	"engine/cmd/loadcp": "points load at the hosted control plane, run with go run by whoever " +
		"operates it, and built into no release.",
	"examples/go-api": "the example application the walkthroughs rehearse. It is a " +
		"customer's application in miniature rather than the product, and it imports " +
		"nothing from the engine.",
}

// lists is everything the gate is told rather than finds, passed as one value
// so that a test can supply its own without editing package state another test
// running beside it is reading.
type lists struct {
	notConsulted  map[string]string
	notRegistered map[string]string
	shipped       map[string]string
	notShipped    map[string]string
}

// platform is one GOOS and GOARCH pair a release builds.
type platform struct{ goos, goarch string }

func (p platform) String() string { return p.goos + "/" + p.goarch }

// matrixEntry matches one line of release.yml's build matrix, which is written
// as a flow mapping: { os: darwin, arch: arm64 }.
var matrixEntry = regexp.MustCompile(`\{\s*os:\s*([a-z0-9]+)\s*,\s*arch:\s*([a-z0-9]+)\s*\}`)

// releasePlatforms reads the platforms a release builds.
//
// From the workflow that builds them, rather than a copy here that would drift
// the first time somebody added a platform. No matrix is an error rather than
// an empty list, because an empty list means no file builds anywhere, every
// socket reads as unregistered, and the finding is about the gate rather than
// the product.
func releasePlatforms(root string) ([]platform, error) {
	path := filepath.Join(root, ".github", "workflows", "release.yml")
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s for the platforms a release builds: %w", path, err)
	}
	var out []platform
	for _, m := range matrixEntry.FindAllStringSubmatch(string(body), -1) {
		out = append(out, platform{goos: m[1], goarch: m[2]})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s has no build matrix entry of the form { os: linux, arch: amd64 }, "+
			"so which files a release compiles cannot be decided and nothing was checked", path)
	}
	return out, nil
}

// knownOS and knownArch are the name suffixes the go command treats as build
// constraints. A file named keyring_linux.go builds only on Linux and a file
// named linux.go builds everywhere, because the go command only reads a
// suffix that follows an underscore.
var (
	knownOS = set("aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios",
		"js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos")
	unixOS = set("aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios",
		"linux", "netbsd", "openbsd", "solaris")
	knownArch = set("386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be", "loong64",
		"mips", "mipsle", "mips64", "mips64le", "mips64p32", "mips64p32le", "ppc", "ppc64",
		"ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64", "wasm")
)

func set(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// builds reports whether a file is compiled for one platform.
func builds(name string, f *ast.File, p platform) (bool, error) {
	parts := strings.Split(strings.TrimSuffix(filepath.Base(name), ".go"), "_")
	if n := len(parts); n >= 2 {
		last := parts[n-1]
		switch {
		case knownArch[last]:
			if last != p.goarch {
				return false, nil
			}
			if n >= 3 && knownOS[parts[n-2]] && parts[n-2] != p.goos {
				return false, nil
			}
		case knownOS[last]:
			if last != p.goos {
				return false, nil
			}
		}
	}
	for _, group := range f.Comments {
		if group.End() >= f.Package {
			break
		}
		for _, c := range group.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return false, fmt.Errorf("the build constraint %q: %w", c.Text, err)
			}
			// cgo is false because the release builds with CGO_ENABLED=0, and
			// any other tag, ignore included, is set by no build a customer
			// receives. Release tags are taken as satisfied: the toolchain
			// go.mod pins is newer than every go1.N a file here names.
			if !expr.Eval(func(tag string) bool {
				switch {
				case tag == p.goos, tag == p.goarch:
					return true
				case tag == "unix":
					return unixOS[p.goos]
				case strings.HasPrefix(tag, "go1."):
					return true
				}
				return false
			}) {
				return false, nil
			}
		}
	}
	return true, nil
}

// buildsAnywhere reports whether any platform a release builds compiles a file.
func buildsAnywhere(name string, f *ast.File, platforms []platform) (bool, error) {
	for _, p := range platforms {
		ok, err := builds(name, f, p)
		if err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// skipDir reports whether the go command would ignore a directory, plus the
// JavaScript dependency trees, which hold no Go and a great many files.
func skipDir(name string) bool {
	return name == "node_modules" || name == "testdata" || name == "vendor" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

var moduleLine = regexp.MustCompile(`(?m)^module\s+(\S+)`)

// modules maps every module path in the repository to its directory, relative
// to the root and slash separated.
func modules(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && skipDir(d.Name()) {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != "go.mod" {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		m := moduleLine.FindSubmatch(body)
		if m == nil {
			return fmt.Errorf("%s has no module line, so no import into it can be followed", path)
		}
		rel, rerr := filepath.Rel(root, filepath.Dir(path))
		if rerr != nil {
			return rerr
		}
		out[string(m[1])] = filepath.ToSlash(rel)
		return nil
	})
	return out, err
}

// mainPackages lists every directory holding a main package that some
// platform a release builds would compile.
func mainPackages(root string, platforms []platform) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && skipDir(d.Name()) {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		entries, rerr := os.ReadDir(path)
		if rerr != nil {
			return rerr
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			// The package clause and the comments above it, and nothing else.
			// A clause is what decides this, not a line matching "package
			// main": pages.gen.go carries documentation samples that begin
			// with exactly that line, inside a string.
			file := filepath.Join(path, name)
			f, perr := parser.ParseFile(token.NewFileSet(), file, nil,
				parser.PackageClauseOnly|parser.ParseComments)
			if perr != nil {
				return fmt.Errorf("reading %s: %w", file, perr)
			}
			ok, berr := buildsAnywhere(name, f, platforms)
			if berr != nil {
				return fmt.Errorf("reading %s: %w", file, berr)
			}
			if ok && f.Name.Name == "main" {
				rel, rerr := filepath.Rel(root, path)
				if rerr != nil {
					return rerr
				}
				out = append(out, filepath.ToSlash(rel))
				break
			}
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// fileInfo is one parsed file and what its import names refer to.
type fileInfo struct {
	ast *ast.File
	// rel is the file's path relative to the root, for reporting.
	rel string
	// imports maps each name the file refers to a package by to that
	// package's directory. An import from outside this repository maps to the
	// empty string: its name is known, so a selector on it is not mistaken for
	// a method call, and there is nothing to follow.
	imports map[string]string
	// deps are the directories of the packages this file imports from this
	// repository, blank imports included, because a blank import still runs
	// the package's init.
	deps []string
}

// funcRef is one function or method declaration and where it lives.
type funcRef struct {
	pkg  *pkgInfo
	file *fileInfo
	decl *ast.FuncDecl
}

// pkgInfo is one package's files that some release platform compiles.
type pkgInfo struct {
	rel   string
	name  string
	files []*fileInfo
	// funcs are the package level functions by name. A slice, because the
	// same name can be declared once per platform file.
	funcs    map[string][]*funcRef
	inits    []*funcRef
	methods  []*funcRef
	varInits []varInit
	resolved bool
}

// varInit is one package level initializer, which runs whether or not
// anything reads the variable.
type varInit struct {
	file *fileInfo
	expr ast.Expr
}

// loader parses packages once and shares them between binaries.
type loader struct {
	root      string
	fset      *token.FileSet
	modules   map[string]string
	platforms []platform
	pkgs      map[string]*pkgInfo
}

// dirFor resolves an import path to a directory in this repository, by the
// longest module path it falls under.
func (l *loader) dirFor(importPath string) (string, bool) {
	best, dir := "", ""
	for mod, modDir := range l.modules {
		if (importPath == mod || strings.HasPrefix(importPath, mod+"/")) && len(mod) > len(best) {
			best, dir = mod, modDir
		}
	}
	if best == "" {
		return "", false
	}
	rest := strings.TrimPrefix(importPath, best)
	return strings.TrimPrefix(dir+rest, "./"), true
}

// load parses one package directory.
func (l *loader) load(rel string) (*pkgInfo, error) {
	if p, ok := l.pkgs[rel]; ok {
		return p, nil
	}
	dir := filepath.Join(l.root, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	p := &pkgInfo{rel: rel, funcs: map[string][]*funcRef{}}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, perr := parser.ParseFile(l.fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if perr != nil {
			return nil, fmt.Errorf("reading %s: %w", path, perr)
		}
		ok, berr := buildsAnywhere(name, f, l.platforms)
		if berr != nil {
			return nil, fmt.Errorf("reading %s: %w", path, berr)
		}
		if !ok {
			continue
		}
		fi := &fileInfo{ast: f, rel: rel + "/" + name, imports: map[string]string{}}
		p.name = f.Name.Name
		p.files = append(p.files, fi)
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				ref := &funcRef{pkg: p, file: fi, decl: d}
				switch {
				case d.Recv != nil:
					p.methods = append(p.methods, ref)
				case d.Name.Name == "init":
					p.inits = append(p.inits, ref)
				default:
					p.funcs[d.Name.Name] = append(p.funcs[d.Name.Name], ref)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if vs, isValue := spec.(*ast.ValueSpec); isValue {
						for _, v := range vs.Values {
							p.varInits = append(p.varInits, varInit{file: fi, expr: v})
						}
					}
				}
			}
		}
	}
	if len(p.files) == 0 {
		return nil, fmt.Errorf("%s holds no Go file any platform in release.yml compiles", rel)
	}
	l.pkgs[rel] = p
	return p, nil
}

// majorVersion matches a path element that is a module major version, and
// dottedVersion the gopkg.in spelling of one, neither of which is the name a
// package is referred to by.
var (
	majorVersion  = regexp.MustCompile(`^v[0-9]+$`)
	dottedVersion = regexp.MustCompile(`\.v[0-9]+$`)
)

// thirdPartyName guesses the name a package from outside this repository is
// referred to by when it is imported without one. Only a guess, and only used
// so that a selector on it is not followed as a method call; a wrong guess
// errs toward following more.
func thirdPartyName(path string) string {
	elems := strings.Split(path, "/")
	name := elems[len(elems)-1]
	if len(elems) > 1 && majorVersion.MatchString(name) {
		name = elems[len(elems)-2]
	}
	name = dottedVersion.ReplaceAllString(name, "")
	name = strings.TrimPrefix(name, "go-")
	return strings.ReplaceAll(name, "-", "")
}

// resolve fills in what each file's import names refer to.
func (l *loader) resolve(p *pkgInfo) error {
	if p.resolved {
		return nil
	}
	p.resolved = true
	for _, fi := range p.files {
		for _, imp := range fi.ast.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return fmt.Errorf("reading an import in %s: %w", fi.rel, err)
			}
			local := ""
			if imp.Name != nil {
				local = imp.Name.Name
			}
			if local == "." {
				return fmt.Errorf("%s dot imports %s, so a bare name in it could be a function "+
					"from either package and what it calls cannot be followed", fi.rel, path)
			}
			dir, firstParty := l.dirFor(path)
			if !firstParty {
				if local != "_" {
					if local == "" {
						local = thirdPartyName(path)
					}
					fi.imports[local] = ""
				}
				continue
			}
			target, err := l.load(dir)
			if err != nil {
				return fmt.Errorf("%s imports %s and it cannot be followed, so whatever it "+
					"registers cannot be seen: %w", fi.rel, path, err)
			}
			fi.deps = append(fi.deps, dir)
			if local == "_" {
				continue
			}
			if local == "" {
				local = target.name
			}
			fi.imports[local] = dir
		}
	}
	return nil
}

// closure lists a main package and every package of this repository it
// imports, directly or not.
func (l *loader) closure(mainRel string) ([]*pkgInfo, error) {
	var out []*pkgInfo
	seen := map[string]bool{}
	var visit func(rel string) error
	visit = func(rel string) error {
		if seen[rel] {
			return nil
		}
		seen[rel] = true
		p, err := l.load(rel)
		if err != nil {
			return err
		}
		if err := l.resolve(p); err != nil {
			return err
		}
		out = append(out, p)
		for _, fi := range p.files {
			for _, dep := range fi.deps {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return out, visit(mainRel)
}

// registrations finds every registration a binary can reach, keyed by socket,
// each a sorted list of file:line.
func (l *loader) registrations(pkgs []*pkgInfo, mainRel string, addNames map[string]string) (
	map[string][]string, error,
) {
	methods := map[string][]*funcRef{}
	for _, p := range pkgs {
		for _, m := range p.methods {
			methods[m.decl.Name.Name] = append(methods[m.decl.Name.Name], m)
		}
	}
	reached := map[*ast.FuncDecl]bool{}
	var queue []*funcRef
	mark := func(refs []*funcRef) {
		for _, f := range refs {
			if !reached[f.decl] {
				reached[f.decl] = true
				queue = append(queue, f)
			}
		}
	}
	found := map[string]map[string]bool{}
	walk := func(node ast.Node, p *pkgInfo, fi *fileInfo) {
		var visit func(ast.Node) bool
		visit = func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if sel, isSel := x.Fun.(*ast.SelectorExpr); isSel {
					if socket, isAdd := addNames[sel.Sel.Name]; isAdd {
						if found[socket] == nil {
							found[socket] = map[string]bool{}
						}
						found[socket][fmt.Sprintf("%s:%d", fi.rel, l.fset.Position(sel.Sel.Pos()).Line)] = true
					}
				}
			case *ast.SelectorExpr:
				// A package qualified name reaches exactly that package's
				// function. Anything else is a method, followed by name.
				if id, isIdent := x.X.(*ast.Ident); isIdent {
					if dir, isImport := fi.imports[id.Name]; isImport {
						if dir != "" {
							mark(l.pkgs[dir].funcs[x.Sel.Name])
						}
						return false
					}
				}
				mark(methods[x.Sel.Name])
				ast.Inspect(x.X, visit)
				return false
			case *ast.Ident:
				mark(p.funcs[x.Name])
			}
			return true
		}
		ast.Inspect(node, visit)
	}

	mainPkg := l.pkgs[mainRel]
	if len(mainPkg.funcs["main"]) == 0 {
		return nil, fmt.Errorf("%s is listed as shipped and has no func main to start from", mainRel)
	}
	mark(mainPkg.funcs["main"])
	for _, p := range pkgs {
		mark(p.inits)
		for _, v := range p.varInits {
			walk(v.expr, p, v.file)
		}
	}
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		if f.decl.Body != nil {
			walk(f.decl.Body, f.pkg, f.file)
		}
	}
	out := map[string][]string{}
	for socket, sites := range found {
		for at := range sites {
			out[socket] = append(out[socket], at)
		}
		sort.Strings(out[socket])
	}
	return out, nil
}

// classify splits the repository's main packages into the ones a customer
// runs and the ones nobody does, reporting any the lists do not account for.
func classify(mains []string, l lists, report *Report) []string {
	isMain := map[string]bool{}
	for _, m := range mains {
		isMain[m] = true
	}
	covers := func(key, dir string) bool { return dir == key || strings.HasPrefix(dir, key+"/") }

	var binaries []string
	coveredBy := map[string]bool{}
	for _, dir := range mains {
		_, isShipped := l.shipped[dir]
		notKey := ""
		for key := range l.notShipped {
			if covers(key, dir) && len(key) > len(notKey) {
				notKey = key
			}
		}
		if notKey != "" {
			coveredBy[notKey] = true
		}
		switch {
		case isShipped && notKey != "":
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s is listed in socketcheck as shipped and, under %s, as not shipped. It is one "+
					"or the other, and which decides whether its registrations count", dir, notKey))
		case isShipped:
			binaries = append(binaries, dir)
		case notKey != "":
			if notKey == dir {
				report.NotRead = append(report.NotRead, dir+": "+l.notShipped[notKey])
			}
		default:
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s is a main package socketcheck does not classify. Whether a customer runs it "+
					"decides whether what it registers counts, so name it in shipped or notShipped "+
					"with the reason", dir))
		}
	}
	for _, key := range sortedKeys(l.notShipped) {
		if !isMain[key] && coveredBy[key] {
			report.NotRead = append(report.NotRead, key+", every main package in it: "+l.notShipped[key])
		}
	}
	for _, name := range []string{"shipped", "notShipped"} {
		entries := l.shipped
		if name == "notShipped" {
			entries = l.notShipped
		}
		for _, key := range sortedKeys(entries) {
			if strings.TrimSpace(entries[key]) == "" {
				report.Problems = append(report.Problems, fmt.Sprintf(
					"%s is listed in socketcheck's %s with no reason. Say how it reaches a customer, "+
						"or why it never does", key, name))
			}
			if (name == "shipped" && !isMain[key]) || (name == "notShipped" && !coveredBy[key]) {
				report.Problems = append(report.Problems, fmt.Sprintf(
					"%s is listed in socketcheck's %s and there is no main package there, so the "+
						"entry describes nothing. Delete it, or correct the path", key, name))
			}
		}
	}
	sort.Strings(binaries)
	if len(binaries) == 0 {
		report.Problems = append(report.Problems,
			"no main package in this repository is listed as shipped, so no registration can "+
				"count and the question of whether a socket is filled was not asked")
	}
	return binaries
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkRegistered asks, of every socket, whether a binary a customer runs puts
// anything into it.
func checkRegistered(root string, report *Report, calls map[string]string, l lists) error {
	platforms, err := releasePlatforms(root)
	if err != nil {
		return err
	}
	for _, p := range platforms {
		report.Platforms = append(report.Platforms, p.String())
	}
	mods, err := modules(root)
	if err != nil {
		return err
	}
	mains, err := mainPackages(root, platforms)
	if err != nil {
		return err
	}
	binaries := classify(mains, l, report)
	report.Binaries = binaries

	addNames := map[string]string{}
	sockets := map[string]*Socket{}
	for i := range report.Sockets {
		s := &report.Sockets[i]
		addNames[s.Add] = s.Name
		sockets[s.Name] = s
	}

	ld := &loader{root: root, fset: token.NewFileSet(), modules: mods, platforms: platforms,
		pkgs: map[string]*pkgInfo{}}
	extensionDir, _ := ld.dirFor("github.com/antifailure/antifailure/engine/pkg/extension")
	collisions := map[string]bool{}
	for _, bin := range binaries {
		pkgs, cerr := ld.closure(bin)
		if cerr != nil {
			return fmt.Errorf("following %s: %w", bin, cerr)
		}
		// Without the type checker a call is attributed to a socket by its
		// method's name. That is only sound while no other type in the binary
		// declares a method of the same name, so a second one is refused
		// rather than guessed at.
		for _, p := range pkgs {
			for _, m := range p.methods {
				if _, isAdd := addNames[m.decl.Name.Name]; !isAdd {
					continue
				}
				recv := typeName(m.decl.Recv.List[0].Type)
				if p.rel == extensionDir && recv == "Registry" {
					continue
				}
				at := fmt.Sprintf("%s:%d", m.file.rel, ld.fset.Position(m.decl.Pos()).Line)
				if !collisions[at] {
					collisions[at] = true
					report.Problems = append(report.Problems, fmt.Sprintf(
						"%s declares %s.%s, the name of the registry's own registration method, so a "+
							"call to either reads the same without the type checker and this gate "+
							"cannot tell a registration from a call to the other. Rename it",
						at, recv, m.decl.Name.Name))
				}
			}
		}
		found, rerr := ld.registrations(pkgs, bin, addNames)
		if rerr != nil {
			return rerr
		}
		for _, name := range sortedKeys(addNames) {
			socket := addNames[name]
			sites := found[socket]
			if len(sites) == 0 {
				continue
			}
			s := sockets[socket]
			s.RegisteredBy = append(s.RegisteredBy, bin)
			if s.RegisteredAt == "" || sites[0] < s.RegisteredAt {
				s.RegisteredAt = sites[0]
			}
		}
	}

	for i := range report.Sockets {
		s := &report.Sockets[i]
		reason, listed := l.notRegistered[s.Name]
		if listed {
			s.NotRegisteredBecause = reason
		}
		switch {
		case len(s.RegisteredBy) == 0 && !listed:
			existing := "Nothing outside a test calls " + s.Add + " at all."
			if where, called := calls[s.Add]; called {
				existing = "The call that exists is at " + where + ", and no shipped binary reaches it."
			}
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s can be registered with %s and no binary a customer runs does: nothing "+
					"reachable from %s calls it, so whenever the engine asks, the registry answers "+
					"with nothing. %s A socket nothing fills looks exactly like a working capability "+
					"and is not one. Register it from a shipped binary, or list it in socketcheck's "+
					"notRegistered with the reason",
				s.Name, s.Add, strings.Join(binaries, ", "), existing))
		case len(s.RegisteredBy) > 0 && listed:
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s is listed in socketcheck as registered by no shipped binary and %s registers "+
					"it at %s. Delete the entry: %s",
				s.Name, strings.Join(s.RegisteredBy, " and "), s.RegisteredAt, reason))
		}
	}
	report.Problems = append(report.Problems, exemptionProblems("notRegistered", l.notRegistered, sockets)...)
	return nil
}

// exemptionProblems refuses an exemption nobody could reread: one with no
// reason, and one naming a socket that does not exist, which can never be
// found stale because there is nothing for it to go stale against.
func exemptionProblems(list string, entries map[string]string, sockets map[string]*Socket) []string {
	var out []string
	for _, name := range sortedKeys(entries) {
		if strings.TrimSpace(entries[name]) == "" {
			out = append(out, fmt.Sprintf(
				"%s is listed in socketcheck's %s with no reason. An exemption nobody can reread is "+
					"how a gap stops being noticed: say why, and what would end it", name, list))
		}
		if _, exists := sockets[name]; !exists {
			out = append(out, fmt.Sprintf(
				"%s is listed in socketcheck's %s and is not a socket in engine/pkg/extension, so "+
					"the entry describes nothing and can never be found stale. Delete it, or "+
					"correct the name", name, list))
		}
	}
	return out
}
