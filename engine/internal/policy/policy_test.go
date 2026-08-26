package policy

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func rule(host string, mode schema.Mode, mut ...func(*schema.EgressRule)) schema.EgressRule {
	r := schema.EgressRule{Host: host, Mode: mode}
	for _, m := range mut {
		m(&r)
	}
	return r
}

func paths(p ...string) func(*schema.EgressRule) {
	return func(r *schema.EgressRule) { r.Paths = p }
}

func methods(m ...string) func(*schema.EgressRule) {
	return func(r *schema.EgressRule) { r.Methods = m }
}

func engine(t *testing.T, def schema.Mode, rules ...schema.EgressRule) *Engine {
	t.Helper()
	e, err := New(&schema.Egress{Default: def, Rules: rules})
	require.NoError(t, err)
	return e
}

func TestNew_NilEgressBlocksEverything(t *testing.T) {
	t.Parallel()
	e, err := New(nil)
	require.NoError(t, err)
	d := e.Evaluate(Request{Host: "api.stripe.com", TLS: true, Path: "/v1/charges"})
	require.Equal(t, schema.ModeBlock, d.Mode)
	require.False(t, d.Allowed())
	require.False(t, d.Matched())
	require.Contains(t, d.Reason(), "api.stripe.com")
	require.Contains(t, d.Reason(), "the default is block")
}

func TestNew_EmptyDefaultBecomesBlock(t *testing.T) {
	t.Parallel()
	e := engine(t, "")
	require.Equal(t, schema.ModeBlock, e.Default())
}

func TestNew_RefusesARuleItCannotCompile(t *testing.T) {
	t.Parallel()
	for name, host := range map[string]string{
		"empty":            "",
		"whitespace":       "   ",
		"middle wildcard":  "api.*.example.com",
		"trailing star":    "example.*",
		"bare star suffix": "*.",
		"inner star":       "*.api.*.com",
		"bad port":         "example.com:notaport",
		"zero port":        "example.com:0",
		"huge port":        "example.com:70000",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New(&schema.Egress{Rules: []schema.EgressRule{rule(host, schema.ModeAllow)}})
			require.Error(t, err, "a rule that cannot be compiled must be refused, not skipped")
			require.Contains(t, err.Error(), "policy:")
		})
	}
}

func TestEvaluate_ExactHostBeatsWildcardRegardlessOfOrder(t *testing.T) {
	t.Parallel()
	// The wildcard is written first. Order must not decide.
	e := engine(t, schema.ModeBlock,
		rule("*.stripe.com", schema.ModeBlock),
		rule("api.stripe.com", schema.ModeSandbox),
	)
	d := e.Evaluate(Request{Host: "api.stripe.com", TLS: true, Path: "/v1/charges"})
	require.Equal(t, schema.ModeSandbox, d.Mode)
	_, chain := e.Explain(Request{Host: "api.stripe.com", TLS: true, Path: "/v1/charges"})
	require.Len(t, chain, 2, "both rules matched and both are reported")
	require.Equal(t, "api.stripe.com", chain[0].Rule.Host)
	require.Equal(t, "*.stripe.com", chain[1].Rule.Host)
}

func TestEvaluate_LongerWildcardBeatsShorter(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock,
		rule("*.example.com", schema.ModeBlock),
		rule("*.api.example.com", schema.ModeAllow),
	)
	d := e.Evaluate(Request{Host: "v1.api.example.com", TLS: true})
	require.Equal(t, schema.ModeAllow, d.Mode)
}

func TestEvaluate_WildcardDoesNotMatchTheApex(t *testing.T) {
	t.Parallel()
	// An apex and its subdomains are frequently operated differently, so
	// *.example.com must not cover example.com itself.
	e := engine(t, schema.ModeBlock, rule("*.example.com", schema.ModeAllow))
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "example.com"}).Mode)
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "a.example.com"}).Mode)
}

func TestEvaluate_WildcardDoesNotMatchASuffixInsideALabel(t *testing.T) {
	t.Parallel()
	// notexample.com ends in "example.com" as a string but is a different
	// domain. The leading dot in the compiled suffix is what stops it.
	e := engine(t, schema.ModeBlock, rule("*.example.com", schema.ModeAllow))
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "notexample.com"}).Mode)
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "evilexample.com"}).Mode)
}

func TestEvaluate_MatchAllRuleCoversEveryHost(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("*", schema.ModeCapture))
	d := e.Evaluate(Request{Host: "anything.at.all"})
	require.Equal(t, schema.ModeCapture, d.Mode)
	require.Contains(t, d.Reason(), "every host")
}

func TestEvaluate_HostIsCaseAndTrailingDotInsensitive(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("API.Stripe.com", schema.ModeSandbox))
	for _, h := range []string{"api.stripe.com", "API.STRIPE.COM", "Api.Stripe.Com", "api.stripe.com."} {
		require.Equal(t, schema.ModeSandbox, e.Evaluate(Request{Host: h}).Mode, "host %q", h)
	}
}

func TestEvaluate_PathPrefixRespectsSegmentBoundaries(t *testing.T) {
	t.Parallel()
	// The classic mistake: /admin matching /administrator.
	e := engine(t, schema.ModeBlock,
		rule("example.com", schema.ModeAllow, paths("/admin")),
	)
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com", Path: "/admin"}).Mode)
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com", Path: "/admin/users"}).Mode)
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "example.com", Path: "/administrator"}).Mode)
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "example.com", Path: "/adminx"}).Mode)
}

func TestEvaluate_RootPathPrefixMatchesEverything(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("example.com", schema.ModeAllow, paths("/")))
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com", Path: "/anything/deep"}).Mode)
}

func TestEvaluate_PathWithoutLeadingSlashIsNormalized(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("example.com", schema.ModeAllow, paths("v1/charges")))
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com", Path: "/v1/charges"}).Mode)
}

func TestEvaluate_LongerPathBeatsShorterOnTheSameHost(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock,
		rule("example.com", schema.ModeAllow, paths("/v1")),
		rule("example.com", schema.ModeBlock, paths("/v1/charges")),
	)
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "example.com", Path: "/v1/charges/ch_1"}).Mode)
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com", Path: "/v1/customers"}).Mode)
}

func TestEvaluate_MethodRestrictsAndOutranks(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock,
		rule("example.com", schema.ModeAllow),
		rule("example.com", schema.ModeBlock, methods("delete")),
	)
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com", Method: "GET"}).Mode)
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "example.com", Method: "DELETE"}).Mode)
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "example.com", Method: "delete"}).Mode,
		"the request method is uppercased before matching")
}

func TestEvaluate_EmptyMethodDefaultsToGet(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("example.com", schema.ModeAllow, methods("GET")))
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com"}).Mode)
}

func TestEvaluate_PortRestricts(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("example.com:8443", schema.ModeAllow))
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com", Port: 8443}).Mode)
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "example.com", Port: 443}).Mode)
}

func TestEvaluate_DefaultPortComesFromTheScheme(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock,
		rule("example.com:443", schema.ModeAllow),
		rule("other.com:80", schema.ModeCapture),
	)
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com", TLS: true}).Mode)
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "example.com", TLS: false}).Mode)
	require.Equal(t, schema.ModeCapture, e.Evaluate(Request{Host: "other.com", TLS: false}).Mode)
}

func TestEvaluate_AddressRuleMatchesOnlyAnAddress(t *testing.T) {
	t.Parallel()
	// A name that happens to resolve to the address is a different request.
	// Treating them alike is how a rule covers traffic nobody intended.
	e := engine(t, schema.ModeBlock, rule("169.254.169.254", schema.ModeBlock),
		rule("*", schema.ModeAllow))
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "169.254.169.254", Path: "/latest/meta-data"}).Mode)
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "metadata.internal"}).Mode)
}

func TestEvaluate_IPv6AddressRuleMatches(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("fd00::1", schema.ModeAllow))
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "FD00::1"}).Mode,
		"an address compares by value, not by spelling")
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "fd00::2"}).Mode)
}

func TestEvaluate_TieBreaksOnManifestOrder(t *testing.T) {
	t.Parallel()
	// Two identical rules. The earlier one wins, so the outcome does not
	// depend on the sort being stable.
	e := engine(t, schema.ModeBlock,
		rule("example.com", schema.ModeCapture),
		rule("example.com", schema.ModeAllow),
	)
	d, chain := e.Explain(Request{Host: "example.com"})
	require.Equal(t, schema.ModeCapture, d.Mode)
	require.Equal(t, 0, chain[0].Index)
}

func TestEvaluate_CarriesTheRulesAttachments(t *testing.T) {
	t.Parallel()
	r := rule("api.stripe.com", schema.ModeSandbox)
	r.Credential = "STRIPE_SECRET_KEY"
	r.RateLimit = "10/s"
	r.Fixtures = "fixtures/stripe"
	r.Note = "Stripe has a real sandbox."
	e := engine(t, schema.ModeBlock, r)

	d := e.Evaluate(Request{Host: "api.stripe.com", TLS: true})
	require.Equal(t, "STRIPE_SECRET_KEY", d.Credential)
	require.Equal(t, "10/s", d.RateLimit)
	require.Equal(t, "fixtures/stripe", d.Fixtures)
	require.Contains(t, d.Reason(), "Stripe has a real sandbox.")
}

func TestEvaluate_SynthCarriesTheUnverifiedWarning(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("api.example.com", schema.ModeSynth))
	d := e.Evaluate(Request{Host: "api.example.com"})
	require.Contains(t, d.Reason(), "unverified")
}

func TestDecision_AllowedOnlyForRealNetworkModes(t *testing.T) {
	t.Parallel()
	allowed := map[schema.Mode]bool{
		schema.ModeAllow:   true,
		schema.ModeSandbox: true,
		schema.ModeBlock:   false,
		schema.ModeCapture: false,
		schema.ModeMock:    false,
		schema.ModeSynth:   false,
	}
	for _, m := range schema.AllModes() {
		want, ok := allowed[m]
		require.True(t, ok, "mode %q is not classified; a new mode must decide whether it reaches the network", m)
		require.Equal(t, want, Decision{Mode: m}.Allowed(), "mode %q", m)
	}
}

func TestEvaluate_NonMatchingDefaultIsReported(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeCapture, rule("example.com", schema.ModeAllow))
	d, chain := e.Explain(Request{Host: "other.com"})
	require.Equal(t, schema.ModeCapture, d.Mode)
	require.Empty(t, chain)
	require.Contains(t, d.Reason(), "the default of capture")
}

func TestEvaluate_EmptyPathBecomesRoot(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("example.com", schema.ModeAllow, paths("/")))
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com"}).Mode)
}

func TestRequest_StringRendersWhatADecisionLogShows(t *testing.T) {
	t.Parallel()
	require.Equal(t, "GET https://api.stripe.com/v1/charges",
		Request{Host: "api.stripe.com", TLS: true, Path: "/v1/charges"}.String())
	require.Equal(t, "POST http://localhost/hook",
		Request{Host: "localhost", Method: "POST", Path: "/hook", Port: 80}.String())
	require.Equal(t, "GET http://localhost:8080/",
		Request{Host: "localhost", Path: "/", Port: 8080}.String())
	require.Equal(t, "GET https://example.com:8443/",
		Request{Host: "example.com", TLS: true, Path: "/", Port: 8443}.String())
}

func TestEngine_ExposesItsRulesInEvaluationOrder(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock,
		rule("*.example.com", schema.ModeBlock),
		rule("api.example.com", schema.ModeAllow),
	)
	got := e.Rules()
	require.Equal(t, "api.example.com", got[0].Host, "the most specific rule is first, which is the order that decides")
	require.Equal(t, []string{"*.example.com", "api.example.com"}, e.Hosts())
}

func TestEngine_HostsDeduplicates(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock,
		rule("example.com", schema.ModeAllow, paths("/a")),
		rule("example.com", schema.ModeBlock, paths("/b")),
	)
	require.Equal(t, []string{"example.com"}, e.Hosts())
}

func TestEngine_IPv6IsOffUnlessDeclared(t *testing.T) {
	t.Parallel()
	// An IPv6 path that bypasses the proxy is the most common way an egress
	// control is silently defeated, so it is off unless asked for.
	e, err := New(&schema.Egress{})
	require.NoError(t, err)
	require.False(t, e.AllowsIPv6())

	e, err = New(&schema.Egress{AllowIPv6: true})
	require.NoError(t, err)
	require.True(t, e.AllowsIPv6())
}

func TestEvaluate_MethodAndPathTogetherMustBothMatch(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock,
		rule("example.com", schema.ModeAllow, paths("/v1"), methods("POST")),
	)
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com", Method: "POST", Path: "/v1/x"}).Mode)
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "example.com", Method: "POST", Path: "/v2/x"}).Mode)
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: "example.com", Method: "GET", Path: "/v1/x"}).Mode)
}

func TestEvaluate_ChainExplainsEveryMatch(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock,
		rule("*", schema.ModeBlock),
		rule("*.example.com", schema.ModeCapture),
		rule("api.example.com", schema.ModeAllow),
	)
	d, chain := e.Explain(Request{Host: "api.example.com", Path: "/x"})
	require.Len(t, chain, 3)
	require.Equal(t, schema.ModeAllow, d.Mode)
	// Descending specificity, which is what makes the printed chain readable.
	for i := 1; i < len(chain); i++ {
		require.Less(t, chain[i].Specificity, chain[i-1].Specificity)
	}
	for _, m := range chain {
		require.NotEmpty(t, m.Why)
	}
}

func TestSpecificity_ExactHostOutranksAnyWildcardCombination(t *testing.T) {
	t.Parallel()
	// The property the ranking rests on: no pile of weaker signals on a
	// wildcard can outrank an exact host. If this breaks, the one sentence
	// explanation of the ranking stops being true.
	exact, err := compile(rule("a.example.com", schema.ModeAllow), 0)
	require.NoError(t, err)
	wild, err := compile(rule("*.example.com", schema.ModeBlock,
		paths("/a/very/long/path/that/goes/on/and/on/and/on"), methods("GET")), 1)
	require.NoError(t, err)
	require.Greater(t, exact.specificity, wild.specificity)
}

func BenchmarkEvaluate(b *testing.B) {
	rules := []schema.EgressRule{
		rule("*", schema.ModeBlock),
		rule("*.stripe.com", schema.ModeBlock),
		rule("api.stripe.com", schema.ModeSandbox, paths("/v1")),
		rule("api.resend.com", schema.ModeCapture),
		rule("sentry.io", schema.ModeBlock),
		rule("*.ingest.sentry.io", schema.ModeBlock),
		rule("app.posthog.com", schema.ModeBlock),
		rule("us.i.posthog.com", schema.ModeBlock),
		rule("eu.i.posthog.com", schema.ModeBlock),
	}
	e, err := New(&schema.Egress{Default: schema.ModeBlock, Rules: rules})
	if err != nil {
		b.Fatal(err)
	}
	req := Request{Host: "api.stripe.com", Port: 443, TLS: true, Method: "POST", Path: "/v1/charges"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if d := e.Evaluate(req); d.Mode != schema.ModeSandbox {
			b.Fatal(d.Mode)
		}
	}
}

func TestExplainAgreesWithEvaluate(t *testing.T) {
	t.Parallel()
	// Two code paths deciding the same thing is how a policy engine ends up
	// telling you one story and enforcing another. They must never disagree.
	e := engine(t, schema.ModeBlock,
		rule("*", schema.ModeSynth),
		rule("*.example.com", schema.ModeCapture, paths("/a")),
		rule("api.example.com", schema.ModeAllow, methods("POST")),
		rule("10.0.0.1", schema.ModeBlock),
		rule("example.com:8443", schema.ModeMock),
	)
	reqs := []Request{
		{Host: "api.example.com", Method: "POST", Path: "/a/b", TLS: true},
		{Host: "api.example.com", Method: "GET", Path: "/a/b", TLS: true},
		{Host: "other.example.com", Path: "/a"},
		{Host: "other.example.com", Path: "/b"},
		{Host: "10.0.0.1", Path: "/"},
		{Host: "example.com", Port: 8443},
		{Host: "example.com"},
		{Host: "nowhere.test"},
	}
	for _, r := range reqs {
		fast := e.Evaluate(r)
		slow, chain := e.Explain(r)
		require.Equal(t, fast, slow, "request %s", r)
		if fast.Matched() {
			require.NotEmpty(t, chain, "request %s matched but explained nothing", r)
			require.Equal(t, fast.RuleHost, chain[0].Rule.Host,
				"the winner of the scan is the head of the chain, for %s", r)
		} else {
			require.Empty(t, chain, "request %s matched nothing but explained something", r)
		}
	}
}

func TestDecision_CarriesTheWebhookPath(t *testing.T) {
	t.Parallel()
	r := rule("api.stripe.com", schema.ModeSandbox)
	r.WebhookPath = "/api/webhooks/stripe"
	e := engine(t, schema.ModeBlock, r)
	require.Equal(t, "/api/webhooks/stripe", e.Evaluate(Request{Host: "api.stripe.com"}).WebhookPath)
}

func TestEvaluate_RuleWithATrailingDotStillMatches(t *testing.T) {
	t.Parallel()
	e := engine(t, schema.ModeBlock, rule("example.com.", schema.ModeAllow))
	require.Equal(t, schema.ModeAllow, e.Evaluate(Request{Host: "example.com"}).Mode)
}

func TestEvaluate_DoesNotAllocate(t *testing.T) {
	// Not parallel: AllocsPerRun measures the whole process.
	//
	// The proxy calls Evaluate on every outbound request the application
	// makes. An allocation here is an allocation on every HTTP call in the
	// environment, which is why the reason is a method and the chain is
	// Explain's job.
	e := engine(t, schema.ModeBlock,
		rule("*", schema.ModeBlock),
		rule("*.stripe.com", schema.ModeBlock),
		rule("api.stripe.com", schema.ModeSandbox, paths("/v1")),
	)
	req := Request{Host: "api.stripe.com", Port: 443, TLS: true, Method: "POST", Path: "/v1/charges"}
	got := testing.AllocsPerRun(200, func() { _ = e.Evaluate(req) })
	require.Zero(t, got, "Evaluate allocated %v times per call", got)
}

func TestCompile_SortsPathsLongestFirst(t *testing.T) {
	t.Parallel()
	// The longest matching path decides, and it is also the one reported. A
	// rule listing both /v1 and /v1/charges must report the longer one.
	c, err := compile(rule("example.com", schema.ModeAllow, paths("/v1", "/v1/charges", "/a")), 0)
	require.NoError(t, err)
	require.Equal(t, []string{"/v1/charges", "/v1", "/a"}, c.paths)

	w, ok := c.matches("example.com", 443, "GET", "/v1/charges/ch_1")
	require.True(t, ok)
	require.Contains(t, w.String(), "/v1/charges")
}

func TestMatches_AZeroRuleMatchesNothing(t *testing.T) {
	t.Parallel()
	// New refuses a rule it cannot compile, so a zero compiled rule never
	// reaches the engine. If one ever did, it must match nothing rather than
	// fall through to matching everything.
	var c compiled
	_, ok := c.matches("example.com", 443, "GET", "/")
	require.False(t, ok)
}

func TestWhy_RendersEveryHostForm(t *testing.T) {
	t.Parallel()
	require.Equal(t, "it matches every host", why{host: hostAny}.String())
	require.Equal(t, "the host matches exactly", why{host: hostExactly}.String())
	require.Equal(t, "the address matches", why{host: hostAddress}.String())
	require.Equal(t, "the host ends in .example.com",
		why{host: hostSuffixed, suffix: ".example.com"}.String())
	require.Equal(t, "the host matches exactly and the method is listed and the path is under /v1",
		why{host: hostExactly, method: true, path: "/v1"}.String())
}

func TestEvaluate_ALeadingDotHostDoesNotSatisfyAWildcard(t *testing.T) {
	t.Parallel()
	// ".example.com" ends in the compiled suffix and is exactly as long as it,
	// so the suffix test alone would pass it. The wildcard has to cover at
	// least one real label, and a host that is only the dotted domain has
	// none.
	e := engine(t, schema.ModeBlock, rule("*.example.com", schema.ModeAllow))
	require.Equal(t, schema.ModeBlock, e.Evaluate(Request{Host: ".example.com"}).Mode)
}

func TestSpecificity_ExactHostsTieAndKeepManifestOrder(t *testing.T) {
	t.Parallel()
	// Two exact hosts never both match one request, so ranking them against
	// each other decides nothing. They must tie, so that a printed policy
	// reads in the order it was written rather than by name length.
	e := engine(t, schema.ModeBlock,
		rule("api.stripe.com", schema.ModeSandbox),
		rule("checkout.stripe.com", schema.ModeSandbox),
		rule("a.io", schema.ModeBlock),
	)
	var hosts []string
	for _, r := range e.Rules() {
		hosts = append(hosts, r.Host)
	}
	require.Equal(t, []string{"api.stripe.com", "checkout.stripe.com", "a.io"}, hosts)
}
