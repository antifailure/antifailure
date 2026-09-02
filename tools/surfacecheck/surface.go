package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// modulePrefix is what makes a package one of ours. An import outside it is a
// dependency, and a dependency in a stable signature is a different problem
// from the one this tool is about: it is visible in go.mod, it is versioned,
// and an outside caller can name it. An import inside it that is not stable is
// the problem, because nothing else says so.
const modulePrefix = "github.com/antifailure/antifailure/"

// A Package is a directory that compiles to something an outside module could
// import: it holds Go files, it is not under an internal directory, and it is
// not a command.
type Package struct {
	// ImportPath is what an outside module would write.
	ImportPath string
	// Dir is relative to the repository root, for error messages that can be
	// clicked.
	Dir string
	// Files are the non-test Go files, parsed.
	Files []*ast.File
	// Fset is shared across the whole run so positions print.
	Fset *token.FileSet
}

// Module is a Go module this tool reads, with the reason it is read written
// down beside it. A module absent from this list is absent on purpose and the
// reason is in main.go.
type Module struct {
	Dir        string
	ImportPath string
}

// shippedModules are the modules whose packages an outside module could import
// and whose stability the release notes make a claim about.
//
// tools is deliberately not here. Its go.mod says it is never published and
// never imported, and it holds the gates rather than the product, so a package
// added to it is not a promise anybody could rely on. If that ever stops being
// true the module belongs in this list rather than in an exception.
var shippedModules = []Module{
	{Dir: "engine", ImportPath: "github.com/antifailure/antifailure/engine"},
	{Dir: "ee/engine", ImportPath: "github.com/antifailure/antifailure/ee/engine"},
}

// skipDirs are directories that hold no importable package by construction.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "testdata": true, "vendor": true,
}

// Load walks the shipped modules and returns every package an outside module
// could import.
//
// The internal rule is applied by path, which is the same rule the compiler
// applies: a package under a directory named internal is importable only from
// within the subtree rooted at that directory's parent. Nothing outside the
// engine module is in that subtree, so nothing outside it can import
// engine/internal. That half of the promise is the toolchain's and this tool
// does not restate it; what it does is enumerate the OTHER half, the packages
// that are importable, so that one appearing has to be classified rather than
// arriving silently.
func Load(root string) ([]*Package, error) {
	fset := token.NewFileSet()
	var out []*Package
	for _, module := range shippedModules {
		moduleRoot := filepath.Join(root, module.Dir)
		err := filepath.WalkDir(moduleRoot, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if path != moduleRoot && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			// The compiler's rule, as a rule about the path and nothing else.
			if name == "internal" && path != moduleRoot {
				return filepath.SkipDir
			}
			pkg, err := parseDir(fset, root, module, path)
			if err != nil {
				return err
			}
			if pkg != nil {
				out = append(out, pkg)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ImportPath < out[j].ImportPath })
	return out, nil
}

// parseDir returns the importable package in dir, or nil when there is none:
// no Go files, only test files, or a command.
func parseDir(fset *token.FileSet, root string, module Module, dir string) (*Package, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Build constraints are not evaluated. A file excluded on this
		// platform still declares part of the surface on another, and a
		// surface that changes with GOOS is a surface nobody can pin.
		parsed, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, name), err)
		}
		files = append(files, parsed)
	}
	if len(files) == 0 {
		return nil, nil
	}
	if files[0].Name.Name == "main" {
		return nil, nil
	}
	rel, err := filepath.Rel(filepath.Join(root, module.Dir), dir)
	if err != nil {
		return nil, err
	}
	importPath := module.ImportPath
	if rel != "." {
		importPath += "/" + filepath.ToSlash(rel)
	}
	relRoot, err := filepath.Rel(root, dir)
	if err != nil {
		return nil, err
	}
	return &Package{
		ImportPath: importPath,
		Dir:        filepath.ToSlash(relRoot),
		Files:      files,
		Fset:       fset,
	}, nil
}

// An Entry is one thing a caller can name: a type, a field of an exported
// struct, a method of an exported type, a function, a constant, a variable.
//
// One line per nameable thing rather than one per declaration, because a
// struct printed whole is a single line that changes whenever any field does
// and says nothing about which. The failure message has to name the field.
type Entry struct {
	// Key is the stable identity: what a caller writes to reach this.
	Key string
	// Sig is the part that may not change incompatibly.
	Sig string
	// Pos is where it is declared, for the message.
	Pos token.Position
	// Refs are the qualified identifiers the signature names, as
	// "importpath.Name".
	Refs []string
}

// Line is the baseline file's rendering: one entry, one line.
func (e Entry) Line(importPath string) string {
	return fmt.Sprintf("%s\t%s\t%s", importPath, e.Key, e.Sig)
}

// Exports returns every entry in a package's exported surface.
func (p *Package) Exports() []Entry {
	var out []Entry
	for _, file := range p.Files {
		imports := importsOf(file)
		for _, decl := range file.Decls {
			out = append(out, p.entriesOf(file, imports, decl)...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// importsOf maps the name a file uses for an import onto the import path.
func importsOf(file *ast.File) map[string]string {
	out := map[string]string{}
	for _, spec := range file.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		out[name] = path
	}
	return out
}

func (p *Package) entriesOf(file *ast.File, imports map[string]string, decl ast.Decl) []Entry {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return p.funcEntries(imports, d)
	case *ast.GenDecl:
		return p.genEntries(file, imports, d)
	}
	return nil
}

func (p *Package) funcEntries(imports map[string]string, d *ast.FuncDecl) []Entry {
	if !d.Name.IsExported() {
		return nil
	}
	key := d.Name.Name
	if d.Recv != nil && len(d.Recv.List) > 0 {
		recv := receiverName(d.Recv.List[0].Type)
		// A method on an unexported type is not reachable from outside even
		// though the method name is capitalised.
		if recv == "" || !ast.IsExported(recv) {
			return nil
		}
		key = recv + "." + d.Name.Name
	}
	sig := p.render(d.Type)
	return []Entry{{
		Key:  key,
		Sig:  "func" + strings.TrimPrefix(sig, "func"),
		Pos:  p.Fset.Position(d.Pos()),
		Refs: refsIn(imports, d.Type),
	}}
}

func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	}
	return ""
}

func (p *Package) genEntries(file *ast.File, imports map[string]string, d *ast.GenDecl) []Entry {
	var out []Entry
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			out = append(out, p.typeEntries(imports, s)...)
		case *ast.ValueSpec:
			out = append(out, p.valueEntries(imports, d.Tok, s)...)
		}
	}
	_ = file
	return out
}

func (p *Package) typeEntries(imports map[string]string, s *ast.TypeSpec) []Entry {
	name := s.Name.Name
	head := "type " + name
	if s.TypeParams != nil {
		head += p.render(s.TypeParams)
	}
	if s.Assign.IsValid() {
		// An alias IS its right hand side, so the whole of it is the
		// signature: an alias quietly repointed at another type is exactly the
		// change this has to see.
		head += " = " + p.render(s.Type)
		return []Entry{{Key: name, Sig: head, Pos: p.Fset.Position(s.Pos()), Refs: refsIn(imports, s.Type)}}
	}

	switch t := s.Type.(type) {
	case *ast.StructType:
		out := []Entry{{Key: name, Sig: head + " struct", Pos: p.Fset.Position(s.Pos())}}
		for _, field := range t.Fields.List {
			out = append(out, p.memberEntries(imports, name, field, "field")...)
		}
		return out
	case *ast.InterfaceType:
		out := []Entry{{Key: name, Sig: head + " interface", Pos: p.Fset.Position(s.Pos())}}
		for _, method := range t.Methods.List {
			out = append(out, p.memberEntries(imports, name, method, "method")...)
		}
		return out
	default:
		return []Entry{{
			Key:  name,
			Sig:  head + " " + p.render(s.Type),
			Pos:  p.Fset.Position(s.Pos()),
			Refs: refsIn(imports, s.Type),
		}}
	}
}

// memberEntries renders one field of a struct or one method of an interface.
//
// An embedded member has no name and is reached through the type it embeds, so
// it is keyed by the embedded type's name, which is what a caller writes.
func (p *Package) memberEntries(imports map[string]string, owner string, field *ast.Field, kind string) []Entry {
	rendered := p.render(field.Type)
	refs := refsIn(imports, field.Type)
	if len(field.Names) == 0 {
		embedded := embeddedName(field.Type)
		if embedded == "" || !ast.IsExported(embedded) {
			return nil
		}
		return []Entry{{
			Key:  owner + "." + embedded,
			Sig:  "embedded " + rendered,
			Pos:  p.Fset.Position(field.Pos()),
			Refs: refs,
		}}
	}
	var out []Entry
	for _, ident := range field.Names {
		if !ident.IsExported() {
			continue
		}
		sig := kind + " " + rendered
		if kind == "method" {
			sig = "method func" + strings.TrimPrefix(rendered, "func")
		}
		out = append(out, Entry{
			Key:  owner + "." + ident.Name,
			Sig:  sig,
			Pos:  p.Fset.Position(ident.Pos()),
			Refs: refs,
		})
	}
	return out
}

func embeddedName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return embeddedName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return embeddedName(t.X)
	case *ast.IndexListExpr:
		return embeddedName(t.X)
	}
	return ""
}

// valueEntries renders a const or a var.
//
// A const carries its value and a var does not. The value of a constant is
// something a caller can compile against and compare with, so a changed one is
// a changed surface; the value of a var is state and is none of a caller's
// business. A long value is hashed rather than inlined, because a schema
// constant several hundred lines long would otherwise be the baseline file.
func (p *Package) valueEntries(imports map[string]string, tok token.Token, s *ast.ValueSpec) []Entry {
	var out []Entry
	for i, ident := range s.Names {
		if !ident.IsExported() {
			continue
		}
		sig := tok.String()
		if s.Type != nil {
			sig += " " + p.render(s.Type)
		}
		refs := refsIn(imports, s.Type)
		if tok == token.CONST && i < len(s.Values) {
			sig += " = " + shorten(p.render(s.Values[i]))
			refs = append(refs, refsIn(imports, s.Values[i])...)
		}
		out = append(out, Entry{
			Key:  ident.Name,
			Sig:  sig,
			Pos:  p.Fset.Position(ident.Pos()),
			Refs: refs,
		})
	}
	return out
}

// shorten replaces a long constant value with a digest of it. The digest still
// changes when the value does, which is the whole job; what it stops is a
// hundred line SQL constant landing in a file meant to be read in a diff.
func shorten(value string) string {
	const limit = 72
	if len(value) <= limit {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%s (%d bytes)", hex.EncodeToString(sum[:])[:12], len(value))
}

// render prints an AST node on one line.
func (p *Package) render(node any) string {
	var b strings.Builder
	if err := printer.Fprint(&b, p.Fset, node); err != nil {
		return fmt.Sprintf("<unprintable: %v>", err)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// refsIn returns every "importpath.Name" the expression names.
//
// This is the leak detector. A qualified identifier is the only way a
// signature can name a type from another package, so resolving each one
// through the file's own import list answers the question exactly: does this
// exported thing oblige a caller to name something from a package they are not
// promised.
func refsIn(imports map[string]string, node ast.Node) []string {
	if node == nil {
		return nil
	}
	var out []string
	ast.Inspect(node, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		path, ok := imports[ident.Name]
		if !ok {
			return true
		}
		out = append(out, path+"."+sel.Sel.Name)
		return true
	})
	return out
}

// RefPackage splits "importpath.Name" back into the import path.
func RefPackage(ref string) string {
	i := strings.LastIndex(ref, ".")
	if i < 0 {
		return ref
	}
	return ref[:i]
}

// Ours reports whether an import path is a package in this repository.
func Ours(importPath string) bool {
	return strings.HasPrefix(importPath, modulePrefix)
}
