package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

// chainManifest has three rules that overlap on one request, which is the only
// shape that exercises the ordering these commands exist to explain.
const chainManifest = `version: 1
name: chaindemo
services:
  - name: web
    kind: web
    command: node server.js
    port: 3000
database:
  provider: docker
  version: 17
egress:
  default: block
  rules:
    - host: '*'
      mode: block
      note: Anything not named below is refused, with a decision you can read.
    - host: '*.example.com'
      mode: capture
      paths: ['/v1']
      note: The v1 surface is captured so agents can read what was sent.
    - host: api.example.com
      mode: allow
      methods: [GET]
      rate_limit: 10/s
      note: Reads are cheap and the responses are what the workflows assert on.
`

func withManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(body), 0o644))
	return dir
}

func TestNetPolicy_ListsRulesInTheOrderThatDecides(t *testing.T) {
	t.Parallel()
	dir := withManifest(t, chainManifest)

	r := runCLI(t, dir, nil, "net", "policy")
	require.Zero(t, r.code, r.stderr)
	// The manifest lists the wildcard first. The output must not.
	require.Less(t,
		indexOf(t, r.stdout, "api.example.com"),
		indexOf(t, r.stdout, "*.example.com"),
		"the most specific rule is printed first, because that is the order it is evaluated in")
	require.Less(t,
		indexOf(t, r.stdout, "*.example.com"),
		indexOf(t, r.stdout, "\n  block    *\n"),
		"the catch all is printed last")
	// Asserted on fragments because the paragraph is wrapped to the terminal.
	require.Contains(t, r.stdout, "Anything not listed")
	require.Contains(t, r.stdout, "below is blocked.")
	require.Contains(t, r.stdout, "IPv6 is off")
	// Every note reaches the reader; a note nobody sees is a note nobody wrote.
	require.Contains(t, r.stdout, "Reads are cheap")
	require.Contains(t, r.stdout, "GET requests only")
	require.Contains(t, r.stdout, "paths under /v1")
	require.Contains(t, r.stdout, "captured so agents can read")
}

func TestNetPolicy_JSONCarriesEveryField(t *testing.T) {
	t.Parallel()
	dir := withManifest(t, chainManifest)

	r := runCLI(t, dir, nil, "net", "policy", "-o", "json")
	require.Zero(t, r.code, r.stderr)

	var doc cli.PolicyJSON
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &doc))
	require.Equal(t, "block", doc.Default)
	require.False(t, doc.AllowIPv6)
	require.Len(t, doc.Rules, 3)
	require.Equal(t, "api.example.com", doc.Rules[0].Host)
	require.Equal(t, "allow", doc.Rules[0].Mode)
	require.Equal(t, "10/s", doc.Rules[0].RateLimit)
	require.Equal(t, []string{"GET"}, doc.Rules[0].Methods)
	require.Equal(t, []string{"/v1"}, doc.Rules[1].Paths)
}

func TestNetExplain_NamesTheDecidingRuleAndTheOnesThatLost(t *testing.T) {
	t.Parallel()
	dir := withManifest(t, chainManifest)

	r := runCLI(t, dir, nil, "net", "explain", "GET", "https://api.example.com/v1/things")
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stdout, "ALLOW")
	require.Contains(t, r.stdout, "The rule for api.example.com decided allow")
	require.Contains(t, r.stdout, "the host matches exactly and the method is listed")
	require.Contains(t, r.stdout, "3 rules match this request")
	require.Contains(t, r.stdout, "10/s")
	// The losers are shown, which is what makes a surprising answer diagnosable.
	require.Contains(t, r.stdout, "*.example.com")
	require.Contains(t, r.stdout, "the host ends in .example.com and the path is under /v1")
}

func TestNetExplain_MethodChangesTheAnswer(t *testing.T) {
	t.Parallel()
	dir := withManifest(t, chainManifest)

	get := runCLI(t, dir, nil, "net", "explain", "GET", "https://api.example.com/v1/x", "-o", "json")
	post := runCLI(t, dir, nil, "net", "explain", "POST", "https://api.example.com/v1/x", "-o", "json")

	var g, p cli.ExplainJSON
	require.NoError(t, json.Unmarshal([]byte(get.stdout), &g))
	require.NoError(t, json.Unmarshal([]byte(post.stdout), &p))
	require.Equal(t, "allow", g.Mode)
	require.True(t, g.Allowed)
	require.Equal(t, "capture", p.Mode)
	require.False(t, p.Allowed, "capture answers inside the environment; nothing leaves")
	require.Len(t, g.Matched, 3)
	require.Len(t, p.Matched, 2, "the method rule does not match a POST and is not listed")
	require.True(t, g.Matched[0].Winner)
	require.False(t, g.Matched[1].Winner)
}

func TestNetExplain_UnknownHostTakesTheDefault(t *testing.T) {
	t.Parallel()
	dir := withManifest(t, chainManifest)

	r := runCLI(t, dir, nil, "net", "explain", "GET", "https://elsewhere.test/", "-o", "json")
	require.Zero(t, r.code, r.stderr)
	var doc cli.ExplainJSON
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &doc))
	require.Equal(t, "block", doc.Mode)
	// The catch all rule matches, so it is the decider and the default never
	// comes into it.
	require.Equal(t, "*", doc.Rule)
	require.Len(t, doc.Matched, 1)
	require.Equal(t, "*", doc.Matched[0].Host)
}

func TestNetExplain_AcceptsABareHost(t *testing.T) {
	t.Parallel()
	dir := withManifest(t, chainManifest)
	// What people actually type. Refusing it on a technicality would be
	// correct and useless.
	r := runCLI(t, dir, nil, "net", "explain", "get", "api.example.com", "-o", "json")
	require.Zero(t, r.code, r.stderr)
	var doc cli.ExplainJSON
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &doc))
	require.Equal(t, "GET https://api.example.com/", doc.Request)
	require.Equal(t, "allow", doc.Mode)
}

func TestNetExplain_RefusesSomethingItCannotParse(t *testing.T) {
	t.Parallel()
	dir := withManifest(t, chainManifest)

	for name, args := range map[string][]string{
		"unparseable url":     {"GET", "ht!tp://[[["},
		"wrong scheme":        {"GET", "ftp://example.com/f"},
		"no host":             {"GET", "https:///just/a/path"},
		"method with a slash": {"GET/POST", "https://example.com/"},
		"bad port":            {"GET", "https://example.com:99999/"},
	} {
		t.Run(name, func(t *testing.T) {
			r := runCLI(t, dir, nil, append([]string{"net", "explain"}, args...)...)
			require.Equal(t, 2, r.code, "a usage mistake exits 2\n%s", r.stderr)
			require.Contains(t, r.stderr, "AF-NET-002")
			require.Contains(t, r.stderr, "af net explain GET https://api.stripe.com/v1/charges")
		})
	}
}

func TestNet_ReportsAMissingManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"net", "policy"},
		{"net", "explain", "GET", "https://example.com/"},
	} {
		r := runCLI(t, dir, nil, args...)
		require.Equal(t, 3, r.code, r.stderr)
		require.Contains(t, r.stderr, "AF-MAN-001")
	}
}

func TestNetPolicy_HandlesAManifestWithNoEgressSection(t *testing.T) {
	t.Parallel()
	dir := withManifest(t, `version: 1
name: bare
services:
  - name: web
    kind: web
    command: node server.js
    port: 3000
database:
  provider: docker
  version: 17
`)
	r := runCLI(t, dir, nil, "net", "policy")
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stdout, "No rules.")
	require.Contains(t, r.stdout, "blocked")

	// And explaining against it still works rather than crashing on the nil.
	e := runCLI(t, dir, nil, "net", "explain", "GET", "https://example.com/", "-o", "json")
	require.Zero(t, e.code, e.stderr)
	var doc cli.ExplainJSON
	require.NoError(t, json.Unmarshal([]byte(e.stdout), &doc))
	require.Equal(t, "block", doc.Mode)
	require.Empty(t, doc.Matched)
}

func indexOf(t *testing.T, haystack, needle string) int {
	t.Helper()
	i := strings.Index(haystack, needle)
	require.GreaterOrEqual(t, i, 0, "output does not contain %q:\n%s", needle, haystack)
	return i
}
