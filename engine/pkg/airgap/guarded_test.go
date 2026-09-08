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

	var findings []finding
	walk(t, dir, dir, &findings)

	got := make([]string, 0, len(findings))
	for _, f := range findings {
		got = append(got, f.what)
	}
	sort.Strings(got)
	require.Equal(t, []string{
		"http.Client", "http.DefaultClient", "http.Get", "net.Dial", "net.LookupHost",
	}, got, "the walk missed a construction that opens a connection outside the guard")
}

// TestAGuardedClientIsNotAFinding proves the walk does not simply flag
// everything, which would make the test above pass for the wrong reason.
func TestAGuardedClientIsNotAFinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.go"), []byte(`package p

import (
	"time"

	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

var a = airgap.Client(airgap.SiteTelemetry, time.Second)
var b = airgap.Transport(airgap.SiteTelemetry)
`), 0o600))

	var findings []finding
	walk(t, dir, dir, &findings)
	require.Empty(t, findings)
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

// banned maps a package-qualified name to what it is, for the message.
//
// http.Transport is deliberately NOT here. A transport with no DialContext is
// unguarded, but a transport is also the thing airgap.Transport returns and the
// thing every caller then adjusts, and flagging the type would flag the fix.
// What is flagged is the client, and a client is what actually dials.
var banned = map[string]map[string]bool{
	"http": {
		"Client": true, "DefaultClient": true, "DefaultTransport": true,
		"Get": true, "Post": true, "PostForm": true, "Head": true,
	},
	"net": {
		"Dial": true, "DialTimeout": true, "DialIP": true, "DialTCP": true,
		"DialUDP": true, "Dialer": true,
		"LookupHost": true, "LookupIP": true, "LookupAddr": true,
	},
	"tls": {"Dial": true, "DialWithDialer": true},
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
	note := func(pos token.Pos, local, sel string) {
		pkg, ok := names[local]
		if !ok || !banned[pkg][sel] {
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
			// pointer, which is how every one of these is written.
			if sel, ok := v.Type.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					note(sel.Pos(), id.Name, sel.Sel.Name)
				}
			}
		case *ast.SelectorExpr:
			// http.DefaultClient, http.Get(...), net.Dial(...). A call is a
			// SelectorExpr too, so one case covers both and the value form is
			// what matters: assigning http.DefaultClient to a field is exactly
			// as unguarded as calling it.
			if id, ok := v.X.(*ast.Ident); ok {
				note(v.Pos(), id.Name, v.Sel.Name)
			}
		}
		return true
	})
	return found
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
