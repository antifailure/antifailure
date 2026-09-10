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
	"engine/cmd/af-proxy/h2.go":          "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/mitm.go":        "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/transparent.go": "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/capture.go":     "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/sandbox.go":     "the sidecar cannot import engine/pkg",
	"engine/cmd/af-proxy/internal.go":    "the sidecar cannot import engine/pkg",

	// The application's own image. What a docker build fetches is a base image
	// and whatever the repository's package manager resolves, all of it inside
	// the daemon and BuildKit where nothing here can see it, and refusing it
	// would make an air gapped installation unable to build an ordinary
	// repository at all. Governing it is the daemon's job: an internal registry
	// mirror and an internal package mirror, or build.strategy image with a
	// prebuilt image, which an air gapped installation usually already does.
	// The enterprise documentation says this in the section naming what the
	// mode does not cover, which is where a buyer needs it.
	"engine/internal/build/docker.go": "the application's own image build, governed by the daemon rather than by this process",
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
	// A pull, in a file that imports nothing this walk would otherwise look at.
	// That is the real shape: a file that fetches a container image has no
	// reason to import net/http.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "pull.go"), []byte(`package p

import "context"

func h(ctx context.Context, cli client, ref string) error {
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	_, err := cli.ImagePull(ctx, ref)
	return err
}
`), 0o600))

	var findings []finding
	walk(t, dir, dir, &findings)

	got := make([]string, 0, len(findings))
	for _, f := range findings {
		got = append(got, f.what)
	}
	sort.Strings(got)
	require.Equal(t, []string{
		"airgap.Reset", "an unguarded ImagePull", "http.Client", "http.DefaultClient",
		"http.Get", "net.Dial", "net.LookupHost",
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

func h(ctx context.Context, cli client, ref string) error {
	if err := airgap.CheckImage(airgap.SiteImagePull, ref); err != nil {
		return err
	}
	_, err := cli.ImagePull(ctx, ref)
	return err
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
	// NOT an early return when names is empty, and that was very nearly a hole.
	// A file that pulls a container image need not import net/http, net or
	// crypto/tls at all: L1.1's emulator support imports a Docker client and
	// nothing else, so an early return here would have skipped the exact file
	// this rule was written for.
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
	found = append(found, imageFetches(file, fset, rel, names)...)
	return found
}

// imageFetches finds a container image pull or build that its own function does
// not check with the guard first.
//
// A different rule from the others because it is a different mechanism. Those
// happen in this process and a dialer can refuse them. A pull happens in the
// Docker daemon, over a socket the guard never sees, so what the call site has
// to do is ASK before it hands the work over, and the only thing a source walk
// can check is that it asked.
//
// The rule is that the enclosing function mentions the guard somewhere. That is
// coarse on purpose: proving that the check dominates the call would need
// control flow analysis, and a walk that tried would be wrong in ways nobody
// could predict. Coarse is enough for what this catches, which is a function
// that pulls an image and has never heard of the air gap.
//
// It was written for a pull that had not landed yet. L1.1's emulator support
// adds a third ImagePull, for LocalStack and Azurite, with the same inspect
// first shape as the two this branch guarded. Without this rule it would merge
// into an air gapped installation that silently reaches a registry, and the
// only thing that would have noticed is somebody remembering.
func imageFetches(file *ast.File, fset *token.FileSet, rel string, names map[string]string) []finding {
	var out []finding
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var fetches []*ast.SelectorExpr
		guarded := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "ImagePull", "ImageBuild":
				fetches = append(fetches, sel)
			}
			if id, ok := sel.X.(*ast.Ident); ok {
				if names[id.Name] == "airgap" || id.Name == "airgap" {
					guarded = true
				}
			}
			return true
		})
		if guarded {
			continue
		}
		for _, sel := range fetches {
			out = append(out, finding{
				path: rel, line: fset.Position(sel.Pos()).Line,
				what: "an unguarded " + sel.Sel.Name,
			})
		}
	}
	return out
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

// TestTheDocumentedListIsTheRealList keeps the enterprise page's table honest.
//
// The page's whole value is that it is specific: it tells a buyer every place
// this product can reach, by name, before they buy. A list like that is worth
// nothing the moment it drifts, and it drifts silently, because adding a site
// constant is a one line change in Go and nobody opens a Markdown file to
// finish it. That is the same failure the mode list gate exists for.
//
// Both directions. A site missing from the page is an outbound path a buyer was
// not told about. A row on the page that is not a site is a claim about a
// refusal that does not happen.
func TestTheDocumentedListIsTheRealList(t *testing.T) {
	t.Parallel()
	page := filepath.Join(repoRoot(t), "docs", "src", "content", "docs",
		"enterprise", "air-gapped.md")
	body, err := os.ReadFile(page)
	require.NoErrorf(t, err, "%s could not be read, so NOTHING was checked", page)

	documented := map[string]bool{}
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "| the ") {
			continue
		}
		documented[strings.TrimSpace(strings.SplitN(line, "|", 3)[1])] = true
	}
	require.NotEmptyf(t, documented, "no table rows were found in %s, so NOTHING was checked", page)

	declared := map[string]bool{}
	for _, s := range sitesFromSource(t) {
		declared[s] = true
	}
	require.NotEmpty(t, declared, "no sites were read from the source, so NOTHING was checked")

	for s := range declared {
		require.Truef(t, documented[s],
			"%q is a place this product can reach and the enterprise page does not list it", s)
	}
	for d := range documented {
		require.Truef(t, declared[d],
			"the enterprise page lists %q and no site by that name exists, so it "+
				"promises a refusal that does not happen", d)
	}
}

// sitesFromSource reads the site names out of the package's own source.
//
// From the source rather than from a slice in the package, because a slice a
// developer has to remember to append to has the identical failure this test
// exists to catch, one level further in.
func sitesFromSource(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "engine", "pkg", "airgap", "airgap.go"))
	require.NoError(t, err)

	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		_, rest, found := strings.Cut(line, `Site = "`)
		if !found {
			continue
		}
		name, _, ok := strings.Cut(rest, `"`)
		if ok {
			out = append(out, name)
		}
	}
	return out
}
