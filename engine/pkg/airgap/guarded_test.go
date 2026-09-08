package airgap_test

// The instrument, and it is the point of the package next door.
//
// A guard that every outbound client in this product goes through is a claim
// about the WHOLE product, not about the twenty three call sites somebody
// happened to convert. Nothing in Go enforces it: net/http will build a client
// with the default transport for anybody who asks, and the twenty fourth one is
// a single line in a package nobody was thinking about, in a review that was
// about something else. It compiles, it works, it is not guarded, and no test
// fails.
//
// So this walks the source and looks for the constructions that can open a
// connection outside the guard. It is an AST walk rather than a grep on
// purpose, and that is not fastidiousness: engine/internal/proxyimage/
// sources.gen.go carries the whole of the sidecar's source as Go STRING
// LITERALS, so a grep for &http.Client{ finds two clients in it that are not
// clients at all, and engine/internal/docs/pages.gen.go carries the
// documentation the same way. An instrument that reports those is an instrument
// whose output has to be filtered by hand every time, which is how a real
// finding gets filtered out with them.
//
// Every exemption carries a reason in this file. A new one is meant to be
// awkward to add, because "there was a reason" written down beats "there was a
// reason" remembered.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// exempt maps a path to why an unguarded outbound client is correct there.
//
// Keyed on the path relative to the repository root.
var exempt = map[string]string{
	"engine/pkg/airgap/airgap.go": "this is the guard, and it is the one place that " +
		"builds a real dialer",

	// tools/proxysrc/main.go carries a fixed list of source files into the
	// sidecar's build context, and engine/pkg is not on it. The sidecar is a
	// separate binary in a separate container with a separate module graph, so
	// there is no import path by which it could reach the guard even if the
	// list changed. What governs it instead is the egress policy it is handed
	// in proxy.json, and the air gapped hook refuses an environment whose
	// policy would have it reach anything, which is the same control one step
	// earlier.
	"engine/cmd/af-proxy/main.go":        "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/synth.go":       "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/destination.go": "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/dns.go":         "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/mitm.go":        "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/transparent.go": "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/capture.go":     "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/sandbox.go":     "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/internal.go":    "the sidecar cannot import engine/pkg",
}

// finding is one unguarded construction.
type finding struct {
	path string
	line int
	what string
}

func TestEveryOutboundClientInTheProductGoesThroughTheGuard(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	// engine and ee, and deliberately not tools. Nothing under tools/ ships:
	// they are the generators and gates CI runs, they are a separate module
	// that the engine's dependency graph does not contain, and several of them
	// exist precisely to talk to GitHub. Walking them would produce findings
	// that can only ever be exemptions, and a list of permanent exemptions is
	// how a real one gets waved through.
	var findings []finding
	for _, tree := range []string{"engine", "ee"} {
		walk(t, filepath.Join(root, tree), root, &findings)
	}

	if len(findings) > 0 {
		lines := make([]string, 0, len(findings))
		for _, f := range findings {
			lines = append(lines, f.path+":"+itoa(f.line)+" builds "+f.what)
		}
		sort.Strings(lines)
		t.Fatalf("%d outbound clients bypass engine/pkg/airgap, so an air gapped "+
			"installation would reach the network through them:\n  %s\n\n"+
			"Build it with airgap.Client, airgap.Transport, airgap.Dial or "+
			"airgap.LookupHost, or add the file to exempt in this test with the "+
			"reason it cannot be guarded.",
			len(findings), strings.Join(lines, "\n  "))
	}
}

// TestTheInstrumentCanSayNo points the walk at a file that does bypass the
// guard and requires it to be found.
//
// Without this the test above passes on a tree where the walk is broken, where
// the pattern list is empty, or where every path was skipped, and all three of
// those read exactly like a clean repository. A check that cannot return no is
// worse than no check, because it is trusted.
func TestTheInstrumentCanSayNo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bad := filepath.Join(dir, "bypass.go")
	require.NoError(t, os.WriteFile(bad, []byte(`package p

import (
	"net"
	"net/http"
	"time"
)

var a = &http.Client{Timeout: time.Second}
var b = http.DefaultClient

func c() { _, _ = net.Dial("tcp", "example.com:443") }
func d() { _, _ = http.Get("https://example.com") }
func e() { _, _ = net.LookupHost("example.com") }
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "undo.go"), []byte(`package p

import "github.com/antifailure/antifailure/engine/pkg/airgap"

func f() { airgap.Reset() }
`), 0o600))

	var findings []finding
	walk(t, dir, dir, &findings)

	got := make([]string, 0, len(findings))
	for _, f := range findings {
		got = append(got, f.what)
	}
	sort.Strings(got)
	require.Equal(t, []string{
		"airgap.Reset", "http.Client", "http.DefaultClient", "http.Get",
		"net.Dial", "net.LookupHost",
	}, got, "the walk missed a construction that opens a connection outside the guard")
}

// TestAGuardedClientIsNotAFinding proves the walk does not simply flag
// everything, which would make the test above pass for the wrong reason.
func TestAGuardedClientIsNotAFinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The file imports net/http and uses it, because a fixture that imported
	// nothing interesting would make the walk return before it looked at
	// anything and the test would pass without exercising the decision.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.go"), []byte(`package p

import (
	"context"
	"net/http"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

var a = airgap.Client(airgap.SiteTelemetry, time.Second)
var b = airgap.Transport(airgap.SiteTelemetry)
var d = &http.Client{Timeout: time.Second, Transport: airgap.Transport(airgap.SiteLoadTest)}

func g() *http.Client {
	tr := airgap.Transport(airgap.SiteLoadTest)
	tr.DisableCompression = true
	return &http.Client{Transport: tr}
}

func c(ctx context.Context) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
}
`), 0o600))

	var findings []finding
	walk(t, dir, dir, &findings)
	require.Empty(t, findings,
		"a client literal whose Transport is the guard's is guarded, and so is one "+
			"built from a local the caller customised, which is how both load runners "+
			"and the conformance suite write theirs")
}

func walk(t *testing.T, dir, root string, out *[]finding) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "testdata", "node_modules", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if _, ok := exempt[filepath.ToSlash(rel)]; ok {
			return nil
		}
		*out = append(*out, inspect(t, path, filepath.ToSlash(rel))...)
		return nil
	})
	require.NoError(t, err)
}

// bannedTypes are types whose CONSTRUCTION opens a connection outside the
// guard. Flagged only as a composite literal, never as a type reference.
//
// The distinction is the whole reason there are two maps. `&http.Client{}` is a
// client nothing guards; `Client *http.Client` on a struct and
// `hc *http.Client` on a parameter are the ordinary way every one of these
// packages passes a client around, including the guarded ones, and a rule that
// could not tell them apart would report fifteen findings on a clean tree and
// be switched off within a day.
var bannedTypes = map[string]map[string]bool{
	"http": {"Client": true, "Transport": true},
	"net":  {"Dialer": true},
}

// bannedValues are values and calls that reach the network outside the guard.
//
// airgap.Reset is not one of those. It is the one function in the guard that
// can undo the seal, it is exported only because the tests that need it are in
// three packages across two modules, and a call to it from production code is
// the same class of defect. It is reported in the same pass because a separate
// pass is a second thing to remember.
var bannedValues = map[string]map[string]bool{
	"http": {
		"DefaultClient": true, "DefaultTransport": true,
		"Get": true, "Post": true, "PostForm": true, "Head": true,
	},
	"net": {
		"Dial": true, "DialTimeout": true, "DialIP": true, "DialTCP": true,
		"DialUDP":    true,
		"LookupHost": true, "LookupIP": true, "LookupAddr": true,
	},
	"tls":    {"Dial": true, "DialWithDialer": true},
	"airgap": {"Reset": true},
}

func inspect(t *testing.T, path, rel string) []finding {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	require.NoErrorf(t, err, "%s could not be parsed, so it was NOT checked", path)

	// The import name each banned package was given in this file, so that an
	// alias is followed and a local variable called http is not mistaken for
	// the standard library.
	names := map[string]string{}
	for _, imp := range file.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		var pkg string
		switch p {
		case "net/http":
			pkg = "http"
		case "net":
			pkg = "net"
		case "crypto/tls":
			pkg = "tls"
		case "github.com/antifailure/antifailure/engine/pkg/airgap":
			pkg = "airgap"
		default:
			continue
		}
		local := pkg
		if imp.Name != nil {
			local = imp.Name.Name
		}
		names[local] = pkg
	}
	if len(names) == 0 {
		return nil
	}

	var found []finding
	note := func(pos token.Pos, local, sel string, set map[string]map[string]bool) {
		pkg, ok := names[local]
		if !ok || !set[pkg][sel] {
			return
		}
		found = append(found, finding{
			path: rel, line: fset.Position(pos).Line, what: pkg + "." + sel,
		})
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CompositeLit:
			// &http.Client{...} and net.Dialer{...}. The type may be behind a
			// pointer, which is how every one of these is written, and the
			// pointer is a UnaryExpr wrapping this node rather than part of it.
			if sel, ok := v.Type.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					// An http.Client written as a literal is fine when its
					// Transport is the guard's. Four callers need one: two load
					// runners and the release check set CheckRedirect, and the
					// conformance suite turns keep alive off, and none of those
					// is expressible as a bare airgap.Client. What is NOT fine
					// is a literal with no Transport at all, because nil means
					// http.DefaultTransport and that dials anywhere.
					if !guardedTransport(v, names) {
						note(sel.Pos(), id.Name, sel.Sel.Name, bannedTypes)
					}
				}
			}
		case *ast.SelectorExpr:
			// http.DefaultClient, http.Get(...), net.Dial(...), airgap.Reset().
			// A call is a SelectorExpr too, so one case covers both, and the
			// value form is what matters: assigning http.DefaultClient to a
			// field is exactly as unguarded as calling it.
			//
			// The two sets are disjoint, which is what stops a composite
			// literal being counted twice. ast.Inspect descends into a
			// CompositeLit's Type, so this case sees the same http.Client the
			// case above just reported, and the first version of this walk
			// returned every constructed client twice.
			if id, ok := v.X.(*ast.Ident); ok {
				note(v.Pos(), id.Name, v.Sel.Name, bannedValues)
			}
		}
		return true
	})
	return found
}

// guardedTransport reports whether a composite literal sets Transport from the
// guard.
//
// Deliberately narrow: the value has to be a call on the airgap package, or an
// identifier assigned from one earlier in the same function, which is how the
// two load runners write it. Anything cleverer would be a checker guessing, and
// a checker that guesses in the permissive direction is the one that lets the
// real case through.
func guardedTransport(lit *ast.CompositeLit, names map[string]string) bool {
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Transport" {
			continue
		}
		return fromAirgap(kv.Value, names)
	}
	return false
}

// fromAirgap reports whether an expression is a call on the guard package, or a
// plain identifier, which is the local variable the load runners build first.
func fromAirgap(e ast.Expr, names map[string]string) bool {
	switch v := e.(type) {
	case *ast.CallExpr:
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && names[id.Name] == "airgap"
	case *ast.Ident:
		// A local, which this walk does not follow. It is accepted because the
		// alternative is refusing the shape the two load runners use, and the
		// assignment that built it is three lines above in the same function
		// where a reader sees it. The walk's job is finding the client nobody
		// thought about, not proving dataflow.
		return true
	}
	return false
}

// repoRoot walks up from this package to the directory holding go.work.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("the repository root was not found, so NOTHING was checked")
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
