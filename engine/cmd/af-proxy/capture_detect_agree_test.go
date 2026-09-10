package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The detector and the sidecar are two lists about the same providers and
// nothing held them together.
//
// WHY THIS EXISTS. `engine/internal/detect` decides that a host gets egress
// mode `capture`. `engine/cmd/af-proxy` decides what a captured request is
// answered with. When the detector names a provider the sidecar has no handler
// for, `captureHandlerFor` returns `genericCapture`, which answers 200 with an
// empty object. That fallback is deliberate and its own comment argues for it,
// but the argument is explicitly about a host A PERSON WROTE DOWN in a rule of
// their own: "it is the right guess where a person wrote the host down". A host
// the DETECTOR put in the manifest is not that case. Nobody chose it, nobody
// can be expected to know the shape is a guess, and an application that reads a
// provider specific field out of the response sees an empty object where it
// expected an id or a status.
//
// The two lists agree today, seven providers for seven handlers. That is the
// reason to write this now rather than after they disagree: there is nothing to
// fix, only something to hold.
//
// WHAT THIS DELIBERATELY DOES NOT CHECK, said rather than implied.
//
// It is a claim about PROVIDERS, not about every host and path. Twilio is why:
// its matcher requires a path containing "Messages", so `verify.twilio.com` on
// any other path correctly reaches no handler. A per host claim would be false
// on a tree nobody has broken, and a gate that is red when nothing is wrong is
// deleted within a week.
//
// It does not reimplement host matching. The four places host matching already
// lives in this repository are the reason a fifth would be the worst available
// fix, so this asks the real `captureHandlerFor` and reads only the DATA out of
// the detector's table.
//
// It says nothing about whether a handler's response shape is CORRECT. That is
// what the per provider tests beside it are for.
func TestEveryDetectedCaptureProviderHasASidecarHandler(t *testing.T) {
	providers := captureProvidersFromDetectCatalog(t)

	// A provider with no listed host would pass every assertion below without
	// exercising one, which is the shape of a check that cannot say no.
	if len(providers) < 5 {
		t.Fatalf("read only %d capture providers from the detector's catalog; the parse has probably stopped matching", len(providers))
	}

	// Representative paths, because one matcher is path sensitive. A provider
	// counts as covered when ANY of its hosts reaches a handler on ANY of
	// these, which is the per provider claim this test makes.
	paths := []string{"/", "/v1/Messages.json", "/v3/mail/send", "/services/T0/B0/XXXX"}

	for _, p := range providers {
		if len(p.hosts) == 0 {
			t.Errorf("%s is declared capture with no hosts, so nothing can answer for it", p.name)
			continue
		}
		covered := false
		var tried []string
		for _, host := range p.hosts {
			h := concreteHost(host)
			tried = append(tried, h)
			for _, path := range paths {
				if _, ok := captureHandlerFor(h, path); ok {
					covered = true
					break
				}
			}
			if covered {
				break
			}
		}
		if !covered {
			t.Errorf("the detector gives %s egress mode capture and this build has no handler for any of %s.\n"+
				"    A request to it is answered by genericCapture, which returns 200 and an empty object.\n"+
				"    That fallback is written for a host a PERSON named in a rule of their own, not for one\n"+
				"    the detector put in the manifest, and an application reading a provider specific field\n"+
				"    out of the response gets nothing. Add a handler in capture.go, or give the entry in\n"+
				"    engine/internal/detect/thirdparty.go a mode this build can actually honour.",
				p.name, strings.Join(tried, ", "))
		}
	}
}

type capturedProvider struct {
	name  string
	hosts []string
}

// captureProvidersFromDetectCatalog reads Name, Hosts and Mode out of the
// `thirdParties` table.
//
// It parses the source rather than importing it because the catalog is
// unexported, and exporting a package's internals so that a test in another
// package can read them makes the production API worse to make a test easier.
// The file's own comment calls the table "a data table rather than code", and
// reading data out of it is what this does.
func captureProvidersFromDetectCatalog(t *testing.T) []capturedProvider {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "detect", "thirdparty.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("could not parse %s: %v", path, err)
	}

	var lit *ast.CompositeLit
	ast.Inspect(file, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "thirdParties" || len(vs.Values) != 1 {
			return true
		}
		lit, _ = vs.Values[0].(*ast.CompositeLit)
		return false
	})
	// Not finding the table is a different answer from finding it empty, and
	// only one of the two means the tree is fine.
	if lit == nil {
		t.Fatalf("found no thirdParties composite literal in %s, so this check is looking in the wrong place", path)
	}

	var out []capturedProvider
	for _, el := range lit.Elts {
		entry, ok := el.(*ast.CompositeLit)
		if !ok {
			continue
		}
		var p capturedProvider
		mode := ""
		for _, f := range entry.Elts {
			kv, ok := f.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Name":
				p.name = stringLiteral(kv.Value)
			case "Mode":
				mode = stringLiteral(kv.Value)
			case "Hosts":
				hosts, ok := kv.Value.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, h := range hosts.Elts {
					if s := stringLiteral(h); s != "" {
						p.hosts = append(p.hosts, s)
					}
				}
			}
		}
		if mode == "capture" {
			out = append(out, p)
		}
	}
	return out
}

func stringLiteral(e ast.Expr) string {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return ""
	}
	return s
}

// concreteHost turns a catalog wildcard into one real hostname.
//
// The catalog writes email.*.amazonaws.com because the region varies. A
// matcher is given a hostname, never a pattern, so the test has to supply one.
// The label chosen is a real region, so a matcher that checks the shape of the
// middle label rather than merely containing a suffix still sees what it
// expects.
func concreteHost(pattern string) string {
	return strings.ReplaceAll(pattern, "*", "us-east-1")
}
