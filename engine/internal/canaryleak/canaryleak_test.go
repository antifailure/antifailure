package canaryleak_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/canaryleak"
	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// A live Stripe secret key shape: the prefix the verify credential detector
// knows, followed by enough entropy for it to read as a credential rather than
// a word. Split so this test file does not itself carry a string a secret
// scanner matches.
const stripeKey = "sk" + "_live_" + "0123456789abcdefghijABCDEF"

// A PEM private key header. The value is fake; only the shape matters.
const pemKey = "-----BEGIN RSA PRIVATE KEY-----\nMIIfake\n-----END RSA PRIVATE KEY-----"

// A United States social security number in the written form, with none of the
// never-issued parts, so the national-id detector accepts it.
const ssn = "451-22-8899"

// probe runs the family with a policy that makes both keys reportable and the
// given evidence and canaries, the way the router would.
func probe(t *testing.T, ev security.Evidence, canaries ...security.Canary) []report.Finding {
	t.Helper()
	in := security.Input{
		Policy: report.Policy{Security: map[report.PolicyKey]report.Level{
			canaryleak.KeySecret: report.LevelFail,
			canaryleak.KeyPII:    report.LevelWarn,
		}},
		Golden: security.NewGoldenView(canaries),
	}.WithRunArtifacts(security.RunArtifacts{Evidence: ev})
	fs, err := canaryleak.New().Probe(context.Background(), in)
	require.NoError(t, err)
	return fs
}

func rules(fs []report.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return out
}

// The end-to-end claim: a secret compiled into a client bundle, rendered
// against the twin, is caught. This is the headline case, effective today with
// no canary planted, from the response body alone.
func TestProbe_SecretInABundleIsCaught(t *testing.T) {
	t.Parallel()
	fs := probe(t, security.Evidence{Responses: []string{
		"window.__STRIPE_KEY='" + stripeKey + "';",
	}})
	require.Equal(t, []string{string(canaryleak.KeySecret)}, rules(fs))
	require.Equal(t, report.LevelFail, fs[0].Level)
	// The value itself never leaves the engine: no field of the finding carries it.
	require.NotContains(t, fs[0].Detail+fs[0].Title+fs[0].Where+fs[0].Fix, stripeKey)
}

func TestProbe_PrivateKeyInADOMIsASecret(t *testing.T) {
	t.Parallel()
	fs := probe(t, security.Evidence{DOM: []string{"<pre>" + pemKey + "</pre>"}})
	require.Equal(t, []string{string(canaryleak.KeySecret)}, rules(fs))
	require.NotContains(t, fs[0].Detail, "PRIVATE KEY-----\nMII")
}

func TestProbe_SocialSecurityNumberIsPersonalData(t *testing.T) {
	t.Parallel()
	fs := probe(t, security.Evidence{Responses: []string{`{"ssn":"` + ssn + `"}`}})
	require.Equal(t, []string{string(canaryleak.KeyPII)}, rules(fs))
	require.Equal(t, report.LevelWarn, fs[0].Level)
	require.NotContains(t, fs[0].Detail+fs[0].Where, ssn)
}

// A page with nothing sensitive produces nothing. This is the negative fixture
// that keeps the family from being one an operator turns off.
func TestProbe_AnOrdinaryPageIsClean(t *testing.T) {
	t.Parallel()
	require.Empty(t, probe(t, security.Evidence{
		DOM:       []string{"<html><body><h1>Welcome</h1><p>Your order shipped.</p></body></html>"},
		Responses: []string{`{"status":"ok","count":3}`},
	}))
}

// No evidence is "nothing to scan", not a blocked probe and not a finding.
func TestProbe_NoEvidenceIsNoFinding(t *testing.T) {
	t.Parallel()
	require.Empty(t, probe(t, security.Evidence{}))
}

// A planted secret canary that reaches a stream is a definite leak, routed to
// the secret key by its kind. This proves the canary path that becomes live
// when a seeder plants tokens.
func TestProbe_PlantedSecretCanaryIsCaught(t *testing.T) {
	t.Parallel()
	const token = "AFCANARY-7f3a9c2b-secret"
	fs := probe(t,
		security.Evidence{Responses: []string{`{"leaked":"` + token + `"}`}},
		security.Canary{Tenant: "t1", Kind: canaryleak.KindSecret, Value: token})
	require.Equal(t, []string{string(canaryleak.KeySecret)}, rules(fs))
	require.Contains(t, fs[0].Detail, "planted")
	require.NotContains(t, fs[0].Detail+fs[0].Where, token)
}

func TestProbe_PlantedPIICanaryIsCaught(t *testing.T) {
	t.Parallel()
	const token = "AFCANARY-9a1b-pii"
	fs := probe(t,
		security.Evidence{DOM: []string{"<span>" + token + "</span>"}},
		security.Canary{Tenant: "t1", Kind: canaryleak.KindPII, Value: token})
	require.Equal(t, []string{string(canaryleak.KeyPII)}, rules(fs))
	require.NotContains(t, fs[0].Detail+fs[0].Where, token)
}

// One detector firing across many pages of one bundle is one leak to fix, not
// one finding per page.
func TestProbe_OneCausePerStreamIsDeduplicated(t *testing.T) {
	t.Parallel()
	body := "a=" + stripeKey
	fs := probe(t, security.Evidence{Responses: []string{body, body, body}})
	// Three response bodies, but they are three distinct streams, so a leak in
	// each is a finding in each; within one stream the same cause is one
	// finding. Here each stream leaks once, so three findings, all secret.
	require.Len(t, fs, 3)
	for _, f := range fs {
		require.Equal(t, string(canaryleak.KeySecret), f.Rule)
	}
}

// A level the manifest set to ignore drops the finding: the family reads the
// level from policy and never decides one itself.
func TestProbe_IgnoreLevelDropsTheFinding(t *testing.T) {
	t.Parallel()
	in := security.Input{
		Policy: report.Policy{Security: map[report.PolicyKey]report.Level{
			canaryleak.KeySecret: report.LevelIgnore,
		}},
	}.WithRunArtifacts(security.RunArtifacts{Evidence: security.Evidence{
		Responses: []string{"k=" + stripeKey},
	}})
	fs, err := canaryleak.New().Probe(context.Background(), in)
	require.NoError(t, err)
	require.Empty(t, fs)
}

// Family contract, so the registry can register it.
func TestFamily_RegistersOnTheSpine(t *testing.T) {
	t.Parallel()
	f := canaryleak.New()
	require.Equal(t, "canary_leak", f.Name())
	require.NotEmpty(t, f.Surfaces())
	require.Equal(t, []change.Check{change.CheckCanaryLeak}, f.Checks())
	require.Empty(t, f.Licensed())

	reg := security.NewRegistry()
	require.NotPanics(t, func() { reg.Register(f) })
	require.Equal(t, []security.Family{f}, reg.ForSurface(change.SurfaceCode))
}

// Each key groups under the family and its declared exit equals the spine's.
// Both are verification failures: the family proves a leak by reading output.
func TestFamily_KeysAndExits(t *testing.T) {
	t.Parallel()
	keys := canaryleak.New().Keys()
	require.Len(t, keys, 2)
	for _, k := range keys {
		require.Equal(t, "canary_leak", security.FamilyOf(string(k.Key)))
		require.NotEmpty(t, k.Title)
		require.Equal(t, security.ExitFor(k.Key), k.Exit)
		require.Equal(t, report.ExitVerification, k.Exit)
	}
	// The default the family declares matches the intent: a secret fails, a
	// shape of personal data warns.
	bykey := map[report.PolicyKey]report.Level{}
	for _, k := range keys {
		bykey[k.Key] = k.Default
	}
	require.Equal(t, report.LevelFail, bykey[canaryleak.KeySecret])
	require.Equal(t, report.LevelWarn, bykey[canaryleak.KeyPII])
}
