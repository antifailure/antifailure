package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Two MCP servers ship in this repository and a client can be connected to
// both at once. This one is local and runs beside the checkout; the other is
// hosted and lives in web/apps/api/src/mcp.ts. They are written in different
// languages by different lanes, so nothing but this test stands between them
// and two tools that answer to one name and mean different things.
//
// A name may appear on both servers only when it is listed here, and the
// listing is the place to say why. Adding a name to this list is a decision to
// document the pair in docs/src/content/docs/reference/mcp.md, not a way to
// quiet the test.
var namesDeliberatelySharedWithTheHostedServer = map[string]string{
	"start_environment": "Both servers answer the same request, to be given an " +
		"environment, and the caller wants the one its server can provide. The " +
		"hosted tool dispatches a repository workflow and returns before anything " +
		"is running; the local tool builds the environment for the checked out " +
		"branch on this machine. The reference documents both meanings under this " +
		"one name.",
}

// TestLocalToolNamesDoNotCollideWithTheHostedServer reads the names the local
// server actually registers and the names the hosted server actually
// registers, and refuses an accidental overlap.
//
// It reads the registrations rather than a written list, because a written
// list is what drifts. Every step that could come up empty fails instead of
// passing quietly: a scan that finds nothing would otherwise report no
// collisions, which is the shape of check this repository keeps catching in
// its own instruments.
func TestLocalToolNamesDoNotCollideWithTheHostedServer(t *testing.T) {
	local := localToolNames(t)
	hosted := hostedToolNames(t)

	var collided []string
	for name := range local {
		if _, isHosted := hosted[name]; !isHosted {
			continue
		}
		if _, allowed := namesDeliberatelySharedWithTheHostedServer[name]; allowed {
			continue
		}
		collided = append(collided, name)
	}
	sort.Strings(collided)

	require.Emptyf(t, collided,
		"these tool names are registered by both this server and the hosted server in "+
			"web/apps/api/src/mcp.ts, and a client connected to both would see each name "+
			"twice meaning two different things: %v\n\n"+
			"Rename the local tool with a distinguishing word, which is the convention "+
			"already in this package: get_rehearsal_run is the local counterpart of "+
			"hosted get_run, inspect_egress_firewall of hosted inspect_recorded_egress, "+
			"and run_browser_workflows of hosted run_workflows. If the two really are "+
			"the same request answered by whichever server the caller reached, add the "+
			"name to namesDeliberatelySharedWithTheHostedServer with the reason and "+
			"document both meanings in docs/src/content/docs/reference/mcp.md.",
		collided)

	// A name may be retired from either server. Left in place, its entry would
	// silently permit a future collision that nobody decided on.
	for name := range namesDeliberatelySharedWithTheHostedServer {
		_, isLocal := local[name]
		_, isHosted := hosted[name]
		require.Truef(t, isLocal && isHosted,
			"%q is listed as deliberately shared with the hosted server, but it is "+
				"registered locally=%v hosted=%v. Remove the entry now that the pair is "+
				"gone, or it will permit a collision nobody chose.",
			name, isLocal, isHosted)
	}
}

// localToolNames returns the Name of every tool Serve registers, found by
// reading the registrations and then reading each constructor's Tool literal.
// The constructor's own identifier is not the tool's name: newGetRunTool
// returns the tool named get_rehearsal_run.
func localToolNames(t *testing.T) map[string]string {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err, "parsing this package's own source")

	pkg, ok := pkgs["mcp"]
	require.True(t, ok, "package mcp was not found in its own directory")

	// Every constructor in the package, by identifier, mapped to the Name in
	// the Tool literal it returns.
	nameByConstructor := map[string]string{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv != nil || fn.Body == nil {
				continue
			}
			if name, found := toolNameIn(fn.Body); found {
				nameByConstructor[fn.Name.Name] = name
			}
		}
	}
	require.NotEmpty(t, nameByConstructor,
		"no tool constructor in this package was recognised, so this test could not "+
			"have found a collision. The shape it reads is a function returning a "+
			"&Tool{Name: \"...\"} literal.")

	serve := findFunc(pkg, "Serve")
	require.NotNil(t, serve, "Serve was not found, so the registrations could not be read")

	registered := map[string]string{}
	var unresolved []string
	ast.Inspect(serve.Body, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall {
			return true
		}
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel || sel.Sel.Name != "Register" || len(call.Args) != 1 {
			return true
		}
		ctor, isCtorCall := call.Args[0].(*ast.CallExpr)
		if !isCtorCall {
			// A Register of something built inline. Nothing here does that, and
			// if something starts to, this test must be taught to read it rather
			// than skip it.
			unresolved = append(unresolved, "a Register argument that is not a constructor call")
			return true
		}
		ident, isIdent := ctor.Fun.(*ast.Ident)
		if !isIdent {
			unresolved = append(unresolved, "a Register argument whose function is not a plain identifier")
			return true
		}
		name, known := nameByConstructor[ident.Name]
		if !known {
			unresolved = append(unresolved, ident.Name+" (no Tool literal with a Name was found in it)")
			return true
		}
		registered[name] = ident.Name
		return true
	})

	require.Emptyf(t, unresolved,
		"Serve registers tools this test could not resolve to a name, so it cannot "+
			"claim it checked them: %v", unresolved)
	require.NotEmpty(t, registered, "Serve was read but no registration was found in it")

	return registered
}

// toolNameIn finds the Name field of the first &Tool{...} literal in a body.
func toolNameIn(body *ast.BlockStmt) (string, bool) {
	var name string
	var found bool
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		lit, isLit := n.(*ast.CompositeLit)
		if !isLit {
			return true
		}
		ident, isIdent := lit.Type.(*ast.Ident)
		if !isIdent || ident.Name != "Tool" {
			return true
		}
		for _, elt := range lit.Elts {
			kv, isKV := elt.(*ast.KeyValueExpr)
			if !isKV {
				continue
			}
			key, isKey := kv.Key.(*ast.Ident)
			if !isKey || key.Name != "Name" {
				continue
			}
			basic, isBasic := kv.Value.(*ast.BasicLit)
			if !isBasic || basic.Kind != token.STRING {
				continue
			}
			unquoted, err := strconv.Unquote(basic.Value)
			if err != nil {
				continue
			}
			name, found = unquoted, true
			return false
		}
		return true
	})
	return name, found
}

func findFunc(pkg *ast.Package, name string) *ast.FuncDecl {
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if isFunc && fn.Recv == nil && fn.Name.Name == name && fn.Body != nil {
				return fn
			}
		}
	}
	return nil
}

var hostedRegistration = regexp.MustCompile(`registerTool\(\s*'([a-z_]+)'`)

// hostedToolNames reads the names the hosted server registers. It fails rather
// than skips when the file cannot be read, because a missing file would
// otherwise mean an empty hosted set and therefore no collision, which is a
// pass this test has not earned.
func hostedToolNames(t *testing.T) map[string]struct{} {
	t.Helper()

	path := filepath.Join("..", "..", "..", "web", "apps", "api", "src", "mcp.ts")
	source, err := os.ReadFile(path)
	require.NoErrorf(t, err,
		"the hosted MCP server at %s could not be read, so no comparison was made. "+
			"If it moved, point this test at the new path. Do not let it pass by "+
			"finding nothing.", path)

	matches := hostedRegistration.FindAllSubmatch(source, -1)
	names := map[string]struct{}{}
	for _, m := range matches {
		names[string(m[1])] = struct{}{}
	}

	// The file is TypeScript and is read with a pattern, so a change to how it
	// registers tools would quietly empty this set.
	require.NotEmptyf(t, names,
		"%s was read but no registerTool call was recognised in it, so this test "+
			"compared against nothing. The hosted server has changed how it registers "+
			"tools and this pattern must be updated.", path)

	total := strings.Count(string(source), "registerTool(")
	require.Equalf(t, total, len(matches),
		"%s contains %d registerTool calls but only %d were parsed, so %d hosted "+
			"names were not compared. Update the pattern rather than checking a subset.",
		path, total, len(matches), total-len(matches))

	return names
}
