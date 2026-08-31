package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
)

// A declared value that nothing can produce is this repository's signature
// defect, and it is worse than a miscounted sentence because a count is at
// least visible to a reader.
//
// Eleven instances were found by people reading, in one evening, none by a
// gate: env.sleeping and env.waking declared and emitted by nothing,
// environment.queued accepted by ingest and produced nowhere, artifact.stored
// the same, four agent.* events mapped into the control plane's public
// vocabulary with no emitter, runtime.idle_sleep read by nothing that acts on
// it, VolumeCreate with no caller so "volumes" named a kind the engine never
// makes, and planTitle's default branch rendering a fourth kind as the third.
//
// WHAT THIS CHECKS AND WHY IT IS THE NARROW HALF. It asserts that every
// constant in a block is returned by the one function that is supposed to
// produce them, and that the function returns nothing else. That is decidable
// from the syntax alone: the const block and the function body are both right
// there, so there is no call graph to approximate and no judgement to make.
//
// WHAT IT DOES NOT CHECK, and where the general form already lives. Whether a
// value is produced ANYWHERE is gated already, by
// `go test ./internal/events -run Emit`, which holds every catalog type to
// being emitted or exempt with a written reason. Do not rebuild that here.
//
// Worth reading before extending this file, because two of us reached the same
// wrong stopping point independently: a reference is NOT a production, so a
// reference count calls events.EnvSleeping live on the strength of the control
// plane type map naming it, which is backwards on the very example that
// motivates the check. The decidable narrowing is position rather than count:
// a type is emitted if it appears as an ARGUMENT to a call to a known emit
// function, and a map key cannot occupy that position. That test does it that
// way. This file does the smaller case of one const block against one named
// function, which needs no call graph at all.
type reachRef struct {
	// name is for the report.
	name string
	// file holds both the const block and the function.
	file string
	// prefix selects the constants.
	prefix string
	// fn is the function that must be able to return each of them.
	fn string
	// want is the block size this check was written against. A block that
	// changes size is not a failure, but it means somebody added a value and
	// this check should be re-read rather than trusted, so it is asserted.
	want int
}

var reachable = []reachRef{
	{
		name:   "run verdicts",
		file:   "engine/internal/report/report.go",
		prefix: "Verdict",
		fn:     "Verdict",
		want:   6,
	},
}

// checkReachable proves every constant in a block is producible by the
// function that produces them, and that the function produces nothing else.
func checkReachable(root string, r reachRef) ([]finding, error) {
	path := root + "/" + r.file
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, body, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.file, err)
	}

	declared, block := prefixedConsts(f, r.prefix)
	if len(declared) == 0 {
		return nil, fmt.Errorf("%s: found no constants prefixed %s, so this check "+
			"is reading the wrong block and would have passed over anything",
			r.file, r.prefix)
	}
	// The precondition, asserted rather than assumed. A block that has grown
	// has not necessarily broken anything, but the reasoning behind this check
	// was done against a particular block and somebody should look again.
	if len(declared) != r.want {
		return nil, fmt.Errorf("%s: the %s block holds %d constants and this check "+
			"was written against %d. Re-read it and update want",
			r.file, r.prefix, len(declared), r.want)
	}
	if block != len(declared) {
		return nil, fmt.Errorf("%s: the block holding the %s constants also holds %d "+
			"other entries, so the block is no longer exactly this set",
			r.file, r.prefix, block-len(declared))
	}

	returned, literals := returnsOf(f, r.fn)
	if len(returned) == 0 && len(literals) == 0 {
		return nil, fmt.Errorf("%s: found no returns in %s, so this check is reading "+
			"the wrong function", r.file, r.fn)
	}

	var out []finding
	for _, name := range declared {
		if !returned[name] {
			out = append(out, finding{
				file: r.file, line: 1,
				why: fmt.Sprintf(
					"declares %s and %s can never return it. A value nothing produces is "+
						"dead, and it is worse than a wrong number because it reaches a "+
						"vocabulary somebody else consumes.", name, r.fn),
				text: "return it from " + r.fn + ", or delete it",
			})
		}
	}
	for _, lit := range literals {
		out = append(out, finding{
			file: r.file, line: 1,
			why: fmt.Sprintf(
				"%s returns the bare string %q rather than one of the %s constants, so "+
					"the set no longer describes what the function can produce.",
				r.fn, lit, r.prefix),
			text: "return the constant",
		})
	}
	return out, nil
}

// prefixedConsts returns the names of constants carrying a prefix, and the
// total size of the block they live in, so the caller can tell a block that is
// exactly this set from one that merely contains it.
func prefixedConsts(f *ast.File, prefix string) ([]string, int) {
	var best []string
	blockSize := 0
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		var out []string
		n := 0
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, id := range vs.Names {
				n++
				if strings.HasPrefix(id.Name, prefix) {
					out = append(out, id.Name)
				}
			}
		}
		if len(out) > len(best) {
			best, blockSize = out, n
		}
	}
	sort.Strings(best)
	return best, blockSize
}

// returnsOf collects the identifiers and the bare string literals a function
// returns.
//
// It finds the function by name whether or not it has a receiver, because the
// one this exists for is a method.
func returnsOf(f *ast.File, name string) (map[string]bool, []string) {
	ids := map[string]bool{}
	var lits []string
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != name || fd.Body == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			ret, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, res := range ret.Results {
				switch v := res.(type) {
				case *ast.Ident:
					ids[v.Name] = true
				case *ast.BasicLit:
					if v.Kind == token.STRING {
						lits = append(lits, strings.Trim(v.Value, `"`))
					}
				}
			}
			return true
		})
	}
	return ids, lits
}
