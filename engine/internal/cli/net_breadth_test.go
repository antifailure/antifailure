package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
)

// broadManifest is the rule the four stacks report measured as a clean ALLOW:
// a leading wildcard over the domain a CRM catch hook lives on.
const broadManifest = `version: 1
name: crmdemo
services:
  - name: web
    kind: web
    command: node server.js
    port: 3000
egress:
  default: block
  rules:
    - host: '*.zapier.com'
      mode: allow
`

const catchHook = "https://hooks.zapier.com/hooks/catch/1234/abcd"

func TestNetExplain_ARuleThatNamesNoHostSaysHowFarItReaches(t *testing.T) {
	t.Parallel()
	r := runCLI(t, withManifest(t, broadManifest), nil, "net", "explain", "POST", catchHook)
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stdout, "ALLOW")
	// Fragments, because the sentence is wrapped to the terminal.
	require.Contains(t, r.stdout, "names no host")
	require.Contains(t, r.stdout, "zapier.com, however many labels deep")
}

func TestNetExplain_TheJSONCarriesTheCaution(t *testing.T) {
	t.Parallel()
	r := runCLI(t, withManifest(t, broadManifest), nil, "--output", "json", "net", "explain", "POST", catchHook)
	require.Zero(t, r.code, r.stderr)
	var doc cli.ExplainJSON
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &doc))
	require.True(t, doc.Allowed)
	require.Contains(t, doc.Caution, "every name under zapier.com")
}

func TestNetExplain_ARuleThatNamesTheHostCarriesNoCaution(t *testing.T) {
	t.Parallel()
	named := `version: 1
name: crmdemo
services:
  - name: web
    kind: web
    command: node server.js
    port: 3000
egress:
  default: block
  rules:
    - host: hooks.zapier.com
      mode: allow
`
	r := runCLI(t, withManifest(t, named), nil, "net", "explain", "POST", catchHook)
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stdout, "ALLOW", "the fixture must decide allow or the absence says nothing")
	require.NotContains(t, r.stdout, "names no host")
}

func TestNetPolicy_ARuleThatNamesNoHostSaysHowFarItReaches(t *testing.T) {
	t.Parallel()
	r := runCLI(t, withManifest(t, broadManifest), nil, "net", "policy")
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stdout, "names no host")
}

func TestNetPolicy_TheJSONCarriesTheCaution(t *testing.T) {
	t.Parallel()
	r := runCLI(t, withManifest(t, broadManifest), nil, "--output", "json", "net", "policy")
	require.Zero(t, r.code, r.stderr)
	var doc cli.PolicyJSON
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &doc))
	require.Len(t, doc.Rules, 1)
	require.Contains(t, doc.Rules[0].Caution, "every name under zapier.com")
}

// The hint used to offer af net explain GET https://*.zapier.com/, which asks
// about a host no request can carry and was answered ALLOW all the same.
func TestNetPolicy_NeverSuggestsExplainingAPattern(t *testing.T) {
	t.Parallel()
	r := runCLI(t, withManifest(t, broadManifest), nil, "net", "policy")
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stdout, "Ask about one request", "the hint must still be printed")
	require.NotContains(t, r.stdout, "https://*.")
}

func TestNetExplain_RefusesAPatternAsTheHost(t *testing.T) {
	t.Parallel()
	r := runCLI(t, withManifest(t, broadManifest), nil, "net", "explain", "GET", "https://*.zapier.com/")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "is a pattern")
}

// af init printed "Nothing reaches the internet by accident" under a table
// holding its own catalogue's *.supabase.co in allow, which reaches every
// Supabase customer's project.
func TestInit_SaysHowFarTheCataloguesWildcardAllowReaches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"package.json": `{"name":"notes","scripts":{"start":"next start"},
			"dependencies":{"next":"15.0.0","@supabase/supabase-js":"2.45.0"}}`,
		"Dockerfile": "FROM node:20\nCMD [\"node\", \"server.js\"]\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}
	r := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stdout, "*.supabase.co", "the fixture must draw the Supabase rule or the case says nothing")
	require.Contains(t, r.stdout, "names no host")
	require.NotContains(t, r.stdout, "Nothing reaches the internet by accident")
}

func TestInit_KeepsTheReassuranceWhereItIsTrue(t *testing.T) {
	t.Parallel()
	r := runCLI(t, cloudsFixture(t), nil, "init", "--non-interactive")
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stdout, "Nothing reaches the internet by accident")
}
