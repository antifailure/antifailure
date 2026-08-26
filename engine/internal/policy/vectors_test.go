package policy

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The control plane renders a policy page and answers "what would happen to
// this request", and it does it in TypeScript because that is what the web
// application is written in. Two implementations of the same decision will
// drift, and the one that drifts is the one nobody is running against real
// traffic: the page will confidently show allow for a request the sidecar
// blocks, and the person reading the page will believe the page.
//
// The fix is not to hope they agree. It is to make the Go implementation emit
// the answers and make the TypeScript one prove it reproduces them, so that a
// change to either side that alters a decision fails a test in both languages.
// This file is the emitter. web/packages/policy/test/vectors.test.ts is the
// consumer, and it reads this exact file.

var updateVectors = flag.Bool("update-vectors", false,
	"rewrite schemas/policy-vectors.json from the current implementation")

// vectorFile is the shared corpus.
type vectorFile struct {
	// Note is addressed to whoever opens the file wondering what it is for.
	Note     string         `json:"note"`
	Policies []vectorPolicy `json:"policies"`
}

type vectorPolicy struct {
	Name     string          `json:"name"`
	Egress   schema.Egress   `json:"egress"`
	Requests []vectorRequest `json:"requests"`
}

type vectorRequest struct {
	Request  vectorReq      `json:"request"`
	Decision vectorDecision `json:"decision"`
	Chain    []vectorMatch  `json:"chain"`
	// Inspects is InspectsHost for the request's host and port. The control
	// plane does not terminate TLS, but it explains which hosts the engine
	// will, and that explanation has to be right.
	Inspects bool `json:"inspects_host"`
}

type vectorReq struct {
	Host   string `json:"host"`
	Port   int    `json:"port,omitempty"`
	Method string `json:"method,omitempty"`
	Path   string `json:"path,omitempty"`
	TLS    bool   `json:"tls,omitempty"`
	String string `json:"string"`
}

type vectorDecision struct {
	Mode        schema.Mode `json:"mode"`
	RuleHost    string      `json:"rule_host,omitempty"`
	RateLimit   string      `json:"rate_limit,omitempty"`
	Credential  string      `json:"credential,omitempty"`
	Fixtures    string      `json:"fixtures,omitempty"`
	WebhookPath string      `json:"webhook_path,omitempty"`
	Matched     bool        `json:"matched"`
	Allowed     bool        `json:"allowed"`
	Reason      string      `json:"reason"`
}

type vectorMatch struct {
	Host        string `json:"host"`
	Index       int    `json:"index"`
	Specificity int    `json:"specificity"`
	Why         string `json:"why"`
}

// corpus is chosen so that every branch a reimplementation could get wrong is
// represented at least once, not so that it covers many hosts. Each policy
// names the confusion it exists to catch.
func corpus() []vectorPolicy {
	return []vectorPolicy{
		{
			Name:   "empty policy: everything falls to the default",
			Egress: schema.Egress{},
			Requests: []vectorRequest{
				{Request: vectorReq{Host: "api.stripe.com", TLS: true, Path: "/v1/charges"}},
				{Request: vectorReq{Host: "localhost", Port: 3000}},
			},
		},
		{
			Name: "specificity, not order: an exact host beats a wildcard written after it",
			Egress: schema.Egress{
				Default: schema.ModeBlock,
				Rules: []schema.EgressRule{
					{Host: "*.example.com", Mode: schema.ModeBlock,
						Note: "Everything under the domain is refused."},
					{Host: "api.example.com", Mode: schema.ModeAllow},
					{Host: "*", Mode: schema.ModeBlock},
				},
			},
			Requests: []vectorRequest{
				{Request: vectorReq{Host: "api.example.com", TLS: true, Path: "/v1/things"}},
				{Request: vectorReq{Host: "cdn.example.com", TLS: true}},
				// The wildcard must cover at least one label, so the apex
				// falls through to the match-all rule.
				{Request: vectorReq{Host: "example.com", TLS: true}},
				// A trailing dot is the same name.
				{Request: vectorReq{Host: "API.Example.com.", TLS: true}},
				{Request: vectorReq{Host: "elsewhere.test", TLS: true}},
			},
		},
		{
			Name: "a longer wildcard suffix beats a shorter one",
			Egress: schema.Egress{
				Default: schema.ModeBlock,
				Rules: []schema.EgressRule{
					{Host: "*.example.com", Mode: schema.ModeBlock},
					{Host: "*.api.example.com", Mode: schema.ModeAllow},
				},
			},
			Requests: []vectorRequest{
				{Request: vectorReq{Host: "eu.api.example.com", TLS: true}},
				{Request: vectorReq{Host: "www.example.com", TLS: true}},
			},
		},
		{
			Name: "path boundaries: /admin does not cover /administrator",
			Egress: schema.Egress{
				Default: schema.ModeBlock,
				Rules: []schema.EgressRule{
					{Host: "app.test", Mode: schema.ModeBlock},
					{Host: "app.test", Mode: schema.ModeAllow, Paths: []string{"/admin"}},
					{Host: "app.test", Mode: schema.ModeCapture, Paths: []string{"/admin/mail"}},
				},
			},
			Requests: []vectorRequest{
				{Request: vectorReq{Host: "app.test", Path: "/admin"}},
				{Request: vectorReq{Host: "app.test", Path: "/admin/"}},
				{Request: vectorReq{Host: "app.test", Path: "/admin/users"}},
				{Request: vectorReq{Host: "app.test", Path: "/admin/mail/send"}},
				{Request: vectorReq{Host: "app.test", Path: "/administrator"}},
				{Request: vectorReq{Host: "app.test", Path: "/"}},
				// No path at all normalizes to "/".
				{Request: vectorReq{Host: "app.test"}},
			},
		},
		{
			Name: "methods and ports narrow a rule",
			Egress: schema.Egress{
				Default: schema.ModeBlock,
				Rules: []schema.EgressRule{
					{Host: "api.test", Mode: schema.ModeBlock},
					{Host: "api.test", Mode: schema.ModeAllow, Methods: []string{"get", "HEAD"}},
					{Host: "api.test:8443", Mode: schema.ModeSandbox, Credential: "API_KEY"},
				},
			},
			Requests: []vectorRequest{
				{Request: vectorReq{Host: "api.test", Method: "GET"}},
				{Request: vectorReq{Host: "api.test", Method: "get"}},
				{Request: vectorReq{Host: "api.test", Method: "POST"}},
				{Request: vectorReq{Host: "api.test", Port: 8443, Method: "POST", TLS: true}},
				// An empty method is a GET.
				{Request: vectorReq{Host: "api.test"}},
			},
		},
		{
			Name: "an address rule matches an address and never a name",
			Egress: schema.Egress{
				Default: schema.ModeBlock,
				Rules: []schema.EgressRule{
					{Host: "10.0.0.5", Mode: schema.ModeAllow},
					{Host: "203.0.113.7:9000", Mode: schema.ModeAllow},
				},
			},
			Requests: []vectorRequest{
				{Request: vectorReq{Host: "10.0.0.5"}},
				{Request: vectorReq{Host: "10.0.0.6"}},
				{Request: vectorReq{Host: "203.0.113.7", Port: 9000}},
				{Request: vectorReq{Host: "203.0.113.7", Port: 80}},
			},
		},
		{
			Name: "every mode, so that a reimplementation carries every field",
			Egress: schema.Egress{
				Default: schema.ModeBlock,
				Rules: []schema.EgressRule{
					{Host: "block.test", Mode: schema.ModeBlock,
						Note: "Errors from a preview environment would drown the production feed."},
					{Host: "allow.test", Mode: schema.ModeAllow, RateLimit: "10/s"},
					{Host: "capture.test", Mode: schema.ModeCapture},
					{Host: "mock.test", Mode: schema.ModeMock, Fixtures: "fixtures/mock",
						WebhookPath: "/hooks/mock"},
					{Host: "sandbox.test", Mode: schema.ModeSandbox, Credential: "STRIPE_SECRET_KEY",
						WebhookPath: "/api/webhooks/stripe"},
					{Host: "synth.test", Mode: schema.ModeSynth},
				},
			},
			Requests: []vectorRequest{
				{Request: vectorReq{Host: "block.test", TLS: true}},
				{Request: vectorReq{Host: "allow.test", TLS: true}},
				{Request: vectorReq{Host: "capture.test", TLS: true}},
				{Request: vectorReq{Host: "mock.test", TLS: true}},
				{Request: vectorReq{Host: "sandbox.test", TLS: true}},
				{Request: vectorReq{Host: "synth.test", TLS: true}},
			},
		},
		{
			Name: "a non-blocking default still reports which rule decided",
			Egress: schema.Egress{
				Default: schema.ModeAllow,
				Rules: []schema.EgressRule{
					{Host: "*.ingest.sentry.io", Mode: schema.ModeBlock},
				},
			},
			Requests: []vectorRequest{
				{Request: vectorReq{Host: "o1.ingest.sentry.io", TLS: true}},
				{Request: vectorReq{Host: "anything.else", TLS: true}},
			},
		},
	}
}

func buildVectors(t *testing.T) vectorFile {
	t.Helper()
	policies := corpus()
	for pi := range policies {
		p := &policies[pi]
		eng, err := New(&p.Egress)
		if err != nil {
			t.Fatalf("policy %q: %v", p.Name, err)
		}
		for ri := range p.Requests {
			vr := &p.Requests[ri]
			req := Request{
				Host: vr.Request.Host, Port: vr.Request.Port,
				Method: vr.Request.Method, Path: vr.Request.Path, TLS: vr.Request.TLS,
			}
			vr.Request.String = req.String()
			d, chain := eng.Explain(req)
			vr.Decision = vectorDecision{
				Mode: d.Mode, RuleHost: d.RuleHost, RateLimit: d.RateLimit,
				Credential: d.Credential, Fixtures: d.Fixtures, WebhookPath: d.WebhookPath,
				Matched: d.Matched(), Allowed: d.Allowed(), Reason: d.Reason(),
			}
			vr.Chain = make([]vectorMatch, 0, len(chain))
			for _, m := range chain {
				vr.Chain = append(vr.Chain, vectorMatch{
					Host: m.Rule.Host, Index: m.Index, Specificity: m.Specificity, Why: m.Why,
				})
			}
			vr.Inspects = eng.InspectsHost(req.Host, req.Port)
		}
	}
	return vectorFile{
		Note: "Generated by engine/internal/policy/vectors_test.go. " +
			"The engine emits these decisions and every other implementation " +
			"of the policy must reproduce them exactly. Regenerate with " +
			"'go test ./internal/policy -update-vectors'.",
		Policies: policies,
	}
}

func vectorPath(t *testing.T) string {
	t.Helper()
	// The file is shared with the web workspace, so it lives at the repository
	// root rather than inside either one's tree.
	return filepath.Join("..", "..", "..", "schemas", "policy-vectors.json")
}

func TestPolicyVectorsMatchTheCheckedInFile(t *testing.T) {
	want := buildVectors(t)
	encoded, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')

	path := vectorPath(t)
	if *updateVectors {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", path)
		return
	}

	have, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v\n\nRegenerate with: go test ./internal/policy -update-vectors", err)
	}
	if !bytes.Equal(bytes.TrimSpace(have), bytes.TrimSpace(encoded)) {
		t.Errorf("schemas/policy-vectors.json is out of date with the policy engine.\n" +
			"The control plane proves it matches these vectors, so a stale file means\n" +
			"the two implementations are no longer being compared.\n\n" +
			"Regenerate with: go test ./internal/policy -update-vectors")
	}
}

// The corpus is only worth what it covers. If a policy stops exercising a
// mode, the control plane can stop implementing it and nothing fails.
func TestVectorCorpusCoversEveryMode(t *testing.T) {
	vectors := buildVectors(t)
	seen := map[schema.Mode]bool{}
	for _, p := range vectors.Policies {
		for _, r := range p.Requests {
			seen[r.Decision.Mode] = true
		}
	}
	for _, m := range schema.AllModes() {
		if !seen[m] {
			t.Errorf("no vector produces mode %q, so no other implementation is forced to handle it", m)
		}
	}
}
