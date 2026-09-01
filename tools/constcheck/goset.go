package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
)

// A closed set declared in Go and nowhere else.
//
// Four shapes are read, because this repository declares its closed sets in
// four ways and a checker that understood only constants would silently skip
// the analyzers, which are a slice built by a function and are one of the two
// sets prose had already miscounted.
type shape int

const (
	// constType is a run of typed string constants: type Rule string, then
	// const ( RuleX Rule = "x" ... ).
	constType shape = iota
	// sliceFunc is a function whose whole body is `return []T{...}`.
	// DefaultAnalyzers is this.
	sliceFunc
	// sliceVar and mapVar are package level registries.
	sliceVar
	mapVar
)

// goSet reads one closed set out of one Go file.
//
// The name is what the file calls it and the members are read from source
// every time. Nothing here writes down a count: a checker carrying its own
// copy of the list is one more place for the list to be wrong, which is the
// defect this whole tool exists to catch.
func goSet(path string, kind shape, name string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, body, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var out []string
	switch kind {
	case constType:
		out = constMembers(f, name)
	case sliceFunc:
		out = sliceFuncMembers(f, name)
	case sliceVar, mapVar:
		out = varMembers(f, name, kind)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: found no members of %s. Either it was renamed or "+
			"this tool no longer understands how it is declared, and either way this "+
			"check would have gone quiet rather than gone wrong", path, name)
	}
	return out, nil
}

// constMembers collects the string values of every constant of one named type.
//
// Go carries the type only on the first spec of a run, so the type in force is
// tracked across specs rather than read from each one. An untyped constant in
// the middle of the block would otherwise be dropped and the count would come
// out one short, which is the exact error this tool reports.
func constMembers(f *ast.File, typeName string) []string {
	var out []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		var inForce string
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				if id, ok := vs.Type.(*ast.Ident); ok {
					inForce = id.Name
				} else {
					inForce = ""
				}
			}
			if inForce != typeName || len(vs.Values) == 0 {
				continue
			}
			if s, ok := stringLit(vs.Values[0]); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// sliceFuncMembers reads the elements of the slice a function returns.
//
// The element name rather than a string is what identifies a member here:
// DefaultAnalyzers returns &WorkspaceAnalyzer{} and friends, which have no
// string value at all. The name is enough, because this rule only ever counts.
func sliceFuncMembers(f *ast.File, funcName string) []string {
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || fd.Name.Name != funcName || fd.Body == nil {
			continue
		}
		for _, stmt := range fd.Body.List {
			ret, ok := stmt.(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				continue
			}
			if cl, ok := ret.Results[0].(*ast.CompositeLit); ok {
				return elements(cl)
			}
		}
	}
	return nil
}

// varMembers reads a package level registry. A map's keys are its members and
// a slice's elements are.
func varMembers(f *ast.File, varName string, kind shape) []string {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != varName || len(vs.Values) == 0 {
				continue
			}
			cl, ok := vs.Values[0].(*ast.CompositeLit)
			if !ok {
				continue
			}
			if kind == mapVar {
				var out []string
				for _, e := range cl.Elts {
					kv, ok := e.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if s, ok := stringLit(kv.Key); ok {
						out = append(out, s)
					}
				}
				return out
			}
			return elements(cl)
		}
	}
	return nil
}

// elements names each element of a composite literal. A struct element is
// named by its first string field where it has one, so that thirdParties comes
// back as stripe, resend and the rest rather than as a row of blanks.
func elements(cl *ast.CompositeLit) []string {
	out := make([]string, 0, len(cl.Elts))
	for i, e := range cl.Elts {
		if s, ok := stringLit(e); ok {
			out = append(out, s)
			continue
		}
		if n, ok := literalName(e); ok {
			out = append(out, n)
			continue
		}
		// Counted but unnamed. Losing the name costs nothing here and losing
		// the element would cost the count, which is the thing being checked.
		out = append(out, fmt.Sprintf("#%d", i+1))
	}
	return out
}

func literalName(e ast.Expr) (string, bool) {
	switch n := e.(type) {
	case *ast.UnaryExpr:
		return literalName(n.X)
	case *ast.Ident:
		return n.Name, true
	case *ast.SelectorExpr:
		return n.Sel.Name, true
	case *ast.CompositeLit:
		if n.Type != nil {
			if id, ok := n.Type.(*ast.Ident); ok {
				return id.Name, true
			}
		}
		for _, el := range n.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if s, ok := stringLit(kv.Value); ok {
				return s, true
			}
		}
	}
	return "", false
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil || s == "" {
		return "", false
	}
	return s, true
}
