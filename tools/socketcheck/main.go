// Command socketcheck holds engine/pkg/extension to the two promises that make
// it a socket rather than a struct.
//
// A socket is a promise to two different people and only one of them is
// visible in the compiler.
//
// THE FIRST PROMISE is to the person implementing it, who is outside this
// module. Go's internal rule means they cannot import anything under
// engine/internal, so a socket whose method signatures name a type from there
// cannot be implemented from outside at all. It compiles here, it reviews as
// correct, and it fails on the first line of the first real implementation.
// That is not hypothetical: engine/api/packages.txt records the same defect
// found in provider.Database, whose ConnString returned a type from
// engine/internal/secrets, and tools/surfacecheck exists because of it. That
// check covers the STABLE packages. extension is unstable and was not covered
// by anything, and it is the package whose entire purpose is being implemented
// from outside.
//
// THE SECOND PROMISE is to the person registering it, who expects the engine
// to ask. This is the one that was already broken when this tool was written.
// extension.AuditSink has an interface, a registry, an AddAuditSink and a
// Registry.Audit that forwards to every sink, and NOTHING IN THE ENGINE HAS
// EVER CALLED Registry.Audit. So audit_stream, a licensed feature, forwards
// nothing, and it would still forward nothing after somebody wrote the sinks,
// because there is no call site to hand them an entry. A hook nothing consults
// is the same shippable gap as a block button that hides nothing: every piece
// is there and the behaviour is absent.
//
// So the rule is: every socket is either CONSULTED by the engine, or listed
// below as one that is not, with the reason. Both directions fail. A socket
// that is not consulted and not listed fails, which is the AuditSink case. A
// socket that is listed and IS consulted fails too, so the entry cannot
// outlive the gap it describes and the list cannot become a set of exemptions
// nobody rereads.
//
// WHAT IT DOES NOT CHECK. It reads the syntax tree rather than type checking,
// so a type reaching a socket signature through an alias declared elsewhere is
// invisible to it, exactly as it is to surfacecheck. It looks for a call to
// the registry's own reader method by name, so a consultation that reached the
// registry some other way would not count. Erring strict is the right way
// round: the failure this exists to catch is a socket that looks plugged in
// and is not.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() {
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	report, err := Check(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "socketcheck:", err)
		os.Exit(1)
	}
	fmt.Print(report.String())
	if len(report.Problems) > 0 {
		os.Exit(1)
	}
}

// notConsulted names a socket the engine does not ask, and why.
//
// An entry is a declaration that the gap is known, not permission for it to
// stay. Each says what would close it, because "not implemented" without that
// is how a list like this stops being read.
var notConsulted = map[string]string{
	"AuditSink": "Registry.Audit has no caller anywhere in the engine, so a registered " +
		"sink receives nothing. Closing it means emitting the entries the audit log " +
		"already writes through the registry as well.",
	"Emulator": "an egress rule cannot name an emulator yet and the sidecar has no route " +
		"to one, so a registration describes a container nothing starts. Closing it means " +
		"the emulate mode and the sidecar route that makes an unmodified application " +
		"reach it.",
}

// inventoryMethods are the registry methods that walk every socket at once.
//
// They are excluded from counting as a consultation on purpose. Registered
// lists what is plugged in and Empty asks whether anything is, and Validate
// checks the registrations themselves; all three mention every field, so
// without this exclusion every socket would look consulted the moment it had a
// field, which is precisely the AuditSink case they would have hidden.
var inventoryMethods = map[string]bool{"Registered": true, "Empty": true, "Validate": true}

// Socket is one extension point.
type Socket struct {
	// Name is the interface a registration implements, such as PolicyHook.
	Name string
	// Add is the registry method that registers one.
	Add string
	// Field is the registry field it appends to.
	Field string
	// Readers are the registry methods that read that field, which is what a
	// consultation has to call.
	Readers []string
	// ConsultedAt is where the engine calls one of them.
	ConsultedAt string
	// Foreign lists types in this socket's signatures that an implementation
	// outside this module cannot name.
	Foreign []string
}

// Report is what the check found.
type Report struct {
	Sockets  []Socket
	Problems []string
}

func (r Report) String() string {
	var b strings.Builder
	implementable, consulted := 0, 0
	for _, s := range r.Sockets {
		if len(s.Foreign) == 0 {
			implementable++
		}
		if s.ConsultedAt != "" {
			consulted++
		}
	}
	fmt.Fprintf(&b, "%d of %d sockets are implementable from outside the engine module\n",
		implementable, len(r.Sockets))
	fmt.Fprintf(&b, "%d of %d sockets are consulted by the engine\n", consulted, len(r.Sockets))
	for _, s := range r.Sockets {
		where := s.ConsultedAt
		if where == "" {
			where = "NOT CONSULTED"
		}
		fmt.Fprintf(&b, "  %-20s %s\n", s.Name, where)
	}
	for _, p := range r.Problems {
		fmt.Fprintln(&b, "socketcheck: "+p)
	}
	return b.String()
}

// Check reads the extension package and the engine that consults it.
func Check(root string) (Report, error) { return check(root, notConsulted) }

// check takes the list of known gaps as an argument rather than reading the
// package variable, so that a test can supply its own without editing global
// state that another test running beside it is reading.
func check(root string, unconsulted map[string]string) (Report, error) {
	var report Report
	dir := filepath.Join(root, "engine", "pkg", "extension")
	// The files are listed and parsed one at a time rather than through
	// parser.ParseDir, which is deprecated, and collected into a slice rather
	// than an ast.Package, which is deprecated too. Nothing here needs either:
	// this reads one package whose name is known.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return report, fmt.Errorf("reading %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if perr != nil {
			return report, fmt.Errorf("reading %s: %w", filepath.Join(dir, name), perr)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return report, fmt.Errorf("no Go files in %s", dir)
	}

	interfaces := map[string]*ast.InterfaceType{}
	structs := map[string]*ast.StructType{}
	local := map[string]bool{}
	// importPath is the package a file's identifier prefix refers to, per file,
	// because two files may import the same name differently.
	importPath := map[*ast.File]map[string]string{}
	var addMethods, readerMethods []*ast.FuncDecl

	for _, f := range files {
		importPath[f] = map[string]string{}
		for _, imp := range f.Imports {
			path, uerr := strconv.Unquote(imp.Path.Value)
			if uerr != nil {
				continue
			}
			name := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				name = imp.Name.Name
			}
			importPath[f][name] = path
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, isType := spec.(*ast.TypeSpec)
					if !isType {
						continue
					}
					local[ts.Name.Name] = true
					switch t := ts.Type.(type) {
					case *ast.InterfaceType:
						interfaces[ts.Name.Name] = t
					case *ast.StructType:
						structs[ts.Name.Name] = t
					}
				}
			case *ast.FuncDecl:
				if !isRegistryMethod(d) {
					continue
				}
				if strings.HasPrefix(d.Name.Name, "Add") {
					addMethods = append(addMethods, d)
					continue
				}
				readerMethods = append(readerMethods, d)
			}
		}
	}

	for _, add := range addMethods {
		socket := Socket{Add: add.Name.Name}
		if add.Type.Params == nil || len(add.Type.Params.List) != 1 {
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s does not take exactly one registration, so what it registers cannot be read",
				add.Name.Name))
			continue
		}
		socket.Name = typeName(add.Type.Params.List[0].Type)
		socket.Field = appendedField(add)
		if socket.Field == "" {
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s does not append to a registry field, so nothing can find what it registered",
				add.Name.Name))
			continue
		}
		for _, reader := range readerMethods {
			if inventoryMethods[reader.Name.Name] {
				continue
			}
			if mentionsField(reader, socket.Field) {
				socket.Readers = append(socket.Readers, reader.Name.Name)
			}
		}
		sort.Strings(socket.Readers)
		if iface, isIface := interfaces[socket.Name]; isIface {
			socket.Foreign = foreignTypes(iface, structs, local, importPath, files)
		} else {
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s registers %s, which is not an interface in this package",
				add.Name.Name, socket.Name))
		}
		report.Sockets = append(report.Sockets, socket)
	}
	sort.Slice(report.Sockets, func(i, j int) bool {
		return report.Sockets[i].Name < report.Sockets[j].Name
	})

	calls, err := engineCalls(root)
	if err != nil {
		return report, err
	}
	for i := range report.Sockets {
		s := &report.Sockets[i]
		if len(s.Readers) == 0 {
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s can be registered and never read: no registry method reads r.%s",
				s.Name, s.Field))
		}
		for _, reader := range s.Readers {
			if where, called := calls[reader]; called {
				s.ConsultedAt = reader + ", at " + where
				break
			}
		}
		reason, listed := unconsulted[s.Name]
		switch {
		case s.ConsultedAt == "" && !listed:
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s is registered by %s and nothing in the engine calls %s. A socket "+
					"nothing consults looks exactly like a working extension point and is "+
					"not one. Consult it, or list it in socketcheck with the reason",
				s.Name, s.Add, strings.Join(s.Readers, " or ")))
		case s.ConsultedAt != "" && listed:
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s is listed in socketcheck as not consulted and the engine consults it "+
					"at %s. Delete the entry: %s", s.Name, s.ConsultedAt, reason))
		}
		for _, foreign := range s.Foreign {
			report.Problems = append(report.Problems, fmt.Sprintf(
				"%s names %s, which is under engine/internal and cannot be imported from "+
					"outside this module, so the socket cannot be implemented from outside",
				s.Name, foreign))
		}
	}
	return report, nil
}

// isRegistryMethod reports whether a function is a method on *Registry.
func isRegistryMethod(d *ast.FuncDecl) bool {
	if d.Recv == nil || len(d.Recv.List) != 1 {
		return false
	}
	return typeName(d.Recv.List[0].Type) == "Registry"
}

// typeName renders the name of a type expression, dropping any pointer and
// keeping the package qualifier where there is one.
func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return typeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return typeName(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return typeName(t.Elt)
	}
	return ""
}

// appendedField finds the registry field an Add method appends to.
func appendedField(d *ast.FuncDecl) string {
	field := ""
	ast.Inspect(d.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 {
			return true
		}
		sel, ok := assign.Lhs[0].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		field = sel.Sel.Name
		return false
	})
	return field
}

// mentionsField reports whether a method reads a registry field.
func mentionsField(d *ast.FuncDecl, field string) bool {
	found := false
	ast.Inspect(d.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == field {
			found = true
		}
		return !found
	})
	return found
}

// foreignTypes lists the types in a socket's signatures that an implementation
// outside this module cannot name.
//
// One level of recursion into the package's own structs, because a socket
// whose parameter is a struct declared here carries that struct's fields
// across the boundary too, and a single internal type in one of them is the
// same defect one step further in.
func foreignTypes(
	iface *ast.InterfaceType, structs map[string]*ast.StructType, local map[string]bool,
	importPath map[*ast.File]map[string]string, files []*ast.File,
) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(n ast.Node, depth int)
	walk = func(n ast.Node, depth int) {
		ast.Inspect(n, func(node ast.Node) bool {
			switch t := node.(type) {
			case *ast.SelectorExpr:
				prefix, ok := t.X.(*ast.Ident)
				if !ok {
					return true
				}
				path := lookupImport(importPath, files, prefix.Name)
				if strings.Contains(path, "/engine/internal/") && !seen[path] {
					seen[path] = true
					out = append(out, prefix.Name+"."+t.Sel.Name)
				}
				return false
			case *ast.Ident:
				if depth > 0 || !local[t.Name] {
					return true
				}
				if s, isStruct := structs[t.Name]; isStruct {
					walk(s, depth+1)
				}
				return true
			}
			return true
		})
	}
	walk(iface, 0)
	sort.Strings(out)
	return out
}

// lookupImport resolves an identifier prefix to an import path, in whichever
// file of the package declares it.
func lookupImport(importPath map[*ast.File]map[string]string, files []*ast.File, name string) string {
	for _, f := range files {
		if path, ok := importPath[f][name]; ok {
			return path
		}
	}
	return ""
}

// engineCalls finds where the engine calls each registry reader.
//
// Non-test files only. A test that calls a reader proves the registry works
// and says nothing about whether the product ever asks, which is the whole
// question here.
func engineCalls(root string) (map[string]string, error) {
	calls := map[string]string{}
	for _, tree := range []string{filepath.Join(root, "engine"), filepath.Join(root, "ee")} {
		err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			// Relative to the root rather than a substring of the absolute
			// path. A substring test made the answer depend on how the root
			// was spelled: "../.." put a slash in front of "engine" and
			// skipped the socket package, "." did not, so the same tree
			// reported six sockets consulted from the test and eight from the
			// command line. The eight were Validate calling its own readers,
			// which is the inventory this gate exists to see past.
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") ||
				strings.HasPrefix(filepath.ToSlash(rel), "engine/pkg/extension/") {
				return nil
			}
			fset := token.NewFileSet()
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return fmt.Errorf("reading %s: %w", path, perr)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if _, already := calls[sel.Sel.Name]; already {
					return true
				}
				rel, rerr := filepath.Rel(root, path)
				if rerr != nil {
					rel = path
				}
				calls[sel.Sel.Name] = fmt.Sprintf("%s:%d",
					filepath.ToSlash(rel), fset.Position(sel.Pos()).Line)
				return true
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return calls, nil
}
