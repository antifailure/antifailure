package ssrf

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/security"
)

func policy(internal, callback report.Level) report.Policy {
	return report.Policy{Security: map[report.PolicyKey]report.Level{
		RuleInternalHost:  internal,
		RuleCallbackDrift: callback,
	}}
}

func TestIsInternalTarget(t *testing.T) {
	for _, h := range []string{
		"127.0.0.1", "169.254.169.254", "10.0.0.5", "192.168.1.1", "172.16.0.1",
		"100.64.0.1", "0.0.0.0", "::1", "fd00::1",
		"localhost", "db", "metadata.google.internal", "svc.cluster.local", "api.svc",
	} {
		require.Truef(t, isInternalTarget(h), "%s is internal", h)
	}
	for _, h := range []string{"example.com", "8.8.8.8", "api.stripe.com", "1.1.1.1"} {
		require.Falsef(t, isInternalTarget(h), "%s is external", h)
	}
}

func TestDetect_RefusedInternalReachFires(t *testing.T) {
	// The proof is the firewall's refusal of a request to the metadata endpoint.
	decisions := []local.Decision{
		{Host: "169.254.169.254", Method: "GET", Path: "/latest/meta-data", Allowed: false},
	}
	findings := Detect(decisions, nil, policy(report.LevelFail, report.LevelFail))
	require.Len(t, findings, 1)
	require.Equal(t, string(RuleInternalHost), findings[0].Rule)
	require.Equal(t, report.LevelFail, findings[0].Level)
	require.Equal(t, 1, findings[0].Count)
}

func TestDetect_AllowedInternalIsTheTwinTalkingToItself(t *testing.T) {
	// An ALLOWED internal decision is the environment reaching its own service,
	// not an SSRF. Only a refused internal reach is a finding.
	decisions := []local.Decision{
		{Host: "10.0.0.5", Method: "GET", Path: "/health", Allowed: true},
	}
	require.Empty(t, Detect(decisions, nil, policy(report.LevelFail, report.LevelFail)))
}

func TestDetect_RefusedExternalIsNotSSRF(t *testing.T) {
	// A refused EXTERNAL host is egress_surprise territory, not an internal reach.
	decisions := []local.Decision{
		{Host: "evil.example.com", Method: "GET", Path: "/", Allowed: false},
	}
	require.Empty(t, Detect(decisions, nil, policy(report.LevelFail, report.LevelFail)))
}

func TestDetect_CallbackReachingInternalIsDrift(t *testing.T) {
	decisions := []local.Decision{
		{Host: "127.0.0.1", Method: "POST", Path: "/webhooks/deliver", Allowed: false},
	}
	findings := Detect(decisions, nil, policy(report.LevelFail, report.LevelFail))
	require.Len(t, findings, 1)
	require.Equal(t, string(RuleCallbackDrift), findings[0].Rule)
}

func TestDetect_GroupsManyReachesToOneHost(t *testing.T) {
	decisions := []local.Decision{
		{Host: "169.254.169.254", Method: "GET", Path: "/a", Allowed: false},
		{Host: "169.254.169.254", Method: "GET", Path: "/b", Allowed: false},
		{Host: "169.254.169.254", Method: "GET", Path: "/c", Allowed: false},
	}
	findings := Detect(decisions, nil, policy(report.LevelFail, report.LevelFail))
	require.Len(t, findings, 1, "many reaches to one internal host are one finding")
	require.Equal(t, 3, findings[0].Count)
}

func TestDetect_IgnoreLevelDropsTheFinding(t *testing.T) {
	decisions := []local.Decision{
		{Host: "169.254.169.254", Method: "GET", Path: "/latest", Allowed: false},
	}
	require.Empty(t, Detect(decisions, nil, policy(report.LevelIgnore, report.LevelIgnore)))
}

func TestDetect_NamesTheChangedEndpointAndNeverTheRawAddress(t *testing.T) {
	decisions := []local.Decision{
		{Host: "10.0.0.5", Method: "GET", Path: "/fetch", Allowed: false},
	}
	targets := []change.Target{{Kind: change.TargetEndpoint, Ref: "app/api/import/route.ts"}}
	findings := Detect(decisions, targets, policy(report.LevelFail, report.LevelFail))
	require.Len(t, findings, 1)
	require.Equal(t, "app/api/import/route.ts", findings[0].Where)
	require.NotContains(t, findings[0].Detail, "10.0.0.5", "the raw internal address must not reach a finding")
	require.Contains(t, findings[0].Detail, "private range")
}

func TestProbe_ReadsInputAndReturnsFindings(t *testing.T) {
	in := security.Input{Policy: policy(report.LevelFail, report.LevelFail)}.
		WithRunArtifacts(security.RunArtifacts{Decisions: []local.Decision{
			{Host: "169.254.169.254", Method: "GET", Path: "/", Allowed: false},
		}})
	findings, err := New().Probe(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	require.Equal(t, string(RuleInternalHost), findings[0].Rule)
}

func TestProbe_UnreadDecisionsAreBlockedNeverAPass(t *testing.T) {
	// An Input with no decision log attached returns nil from Decisions, which a
	// reader treats as not measured and fails closed on.
	findings, err := New().Probe(context.Background(), security.Input{Policy: policy(report.LevelFail, report.LevelFail)})
	require.Error(t, err, "an unread decision log is a blocked probe, not an empty pass")
	require.Nil(t, findings)
}

func TestKeys_DeclaredExitMatchesExitFor(t *testing.T) {
	for _, k := range keys() {
		require.Equalf(t, security.ExitFor(k.Key), k.Exit,
			"the declared exit for %s must equal security.ExitFor", k.Key)
	}
	require.Equal(t, report.ExitVerification, security.ExitFor(RuleInternalHost))
	require.Equal(t, report.ExitVerification, security.ExitFor(RuleCallbackDrift))
}

func TestFamily_ShapeIsRegisterable(t *testing.T) {
	f := New()
	require.Equal(t, "ssrf", f.Name())
	require.NotEmpty(t, f.Surfaces())
	require.Empty(t, f.Licensed())
	reg := security.NewRegistry()
	require.NotPanics(t, func() { reg.Register(f) })
}
