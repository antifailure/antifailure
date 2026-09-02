package egress_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/egress"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

func TestObserve_CountsAnUnsubstitutedSandboxCall(t *testing.T) {
	t.Parallel()
	// The case the whole package exists for. Two calls to the same provider
	// under the same sandbox rule: one had its credential replaced, one did
	// not, because the rule's credential was never configured. In every other
	// column they are identical, so without this count the second is
	// indistinguishable from a working sandbox call and the application's own
	// credential reached the provider unnoticed.
	c := egress.Observe([]local.Decision{
		{Host: "api.stripe.com", Mode: "sandbox", Rule: "api.stripe.com",
			Allowed: true, Substituted: true},
		{Host: "api.stripe.com", Mode: "sandbox", Rule: "api.stripe.com",
			Allowed: true, Substituted: false},
	})

	require.Equal(t, 2, c.Sandbox)
	require.Equal(t, 1, c.Substituted)
	require.Equal(t, 1, c.SandboxUnsubstituted)

	require.Len(t, c.Hosts, 1)
	require.Equal(t, 1, c.Hosts[0].Unsubstituted)
	require.Equal(t, 1, c.Hosts[0].Substituted)
}

func TestCredentialFinding_IsAlwaysAFailureAndNamesTheHosts(t *testing.T) {
	t.Parallel()
	c := egress.Observe([]local.Decision{
		{Host: "api.stripe.com", Mode: "sandbox", Rule: "api.stripe.com", Allowed: true},
		{Host: "api.twilio.com", Mode: "sandbox", Rule: "api.twilio.com", Allowed: true},
	})

	f := egress.CredentialFinding(c)
	require.NotNil(t, f)
	// No manifest key and no configurable level. Nobody wants a live
	// credential sent to a provider from an environment running unreviewed
	// code, so there is nothing here to turn down.
	require.Equal(t, report.LevelFail, f.Level)
	require.Equal(t, egress.RuleUnsubstituted, f.Rule)
	require.Equal(t, 2, f.Count)
	require.Contains(t, f.Where, "api.stripe.com")
	require.Contains(t, f.Where, "api.twilio.com")
}

func TestCredentialFinding_IsNilWhenEverySandboxCallWasSubstituted(t *testing.T) {
	t.Parallel()
	c := egress.Observe([]local.Decision{
		{Host: "api.stripe.com", Mode: "sandbox", Rule: "api.stripe.com",
			Allowed: true, Substituted: true},
	})
	require.Nil(t, egress.CredentialFinding(c))
}

func TestObserve_RanksTheWorstHostFirst(t *testing.T) {
	t.Parallel()
	c := egress.Observe([]local.Decision{
		// Lots of ordinary traffic to a declared host.
		{Host: "api.declared.com", Mode: "allow", Rule: "api.declared.com", Allowed: true},
		{Host: "api.declared.com", Mode: "allow", Rule: "api.declared.com", Allowed: true},
		{Host: "api.declared.com", Mode: "allow", Rule: "api.declared.com", Allowed: true},
		// One host nothing declared.
		{Host: "surprise.example", Mode: "block"},
		// One leaked credential, which outranks everything.
		{Host: "api.stripe.com", Mode: "sandbox", Rule: "api.stripe.com", Allowed: true},
	})

	require.Equal(t, "api.stripe.com", c.Hosts[0].Host,
		"a host that carried an unreplaced credential is read first")
	require.Equal(t, "surprise.example", c.Hosts[1].Host,
		"then one nothing in the manifest declared")
	require.Equal(t, "api.declared.com", c.Hosts[2].Host)
}

func TestObserve_CountsHostOnlyAndRateLimiting(t *testing.T) {
	t.Parallel()
	c := egress.Observe([]local.Decision{
		{Host: "a.example", Mode: "allow", Rule: "a.example", Allowed: true,
			HostOnly: true, WaitedMs: 500, Limit: "10 a second"},
		{Host: "a.example", Mode: "allow", Rule: "a.example", Allowed: true, WaitedMs: 250},
	})

	require.Equal(t, 1, c.HostOnly,
		"a decision made without seeing the path could only half apply a rule")
	require.Equal(t, 2, c.RateLimited)
	require.Equal(t, int64(750), c.WaitedMs,
		"a request the policy held is not a slow application")
}

func TestSummarise_NamesOnlyUndeclaredHostsAsSurprises(t *testing.T) {
	t.Parallel()
	// A blocked host that a rule DID match is a policy working as written,
	// not a surprise. Only a refusal with no matching rule means the manifest
	// never mentioned the destination.
	e := egress.Summarise([]local.Decision{
		{Host: "declared-but-blocked.example", Mode: "block", Rule: "declared-but-blocked.example"},
		{Host: "nobody-declared.example", Mode: "block"},
	})

	require.Equal(t, 2, e.Refused)
	require.Equal(t, []string{"nobody-declared.example"}, e.Surprises)
}

func TestFinding_RespectsTheProjectPolicy(t *testing.T) {
	t.Parallel()
	e := egress.Summarise([]local.Decision{{Host: "nobody.example", Mode: "block"}})

	require.Nil(t, egress.Finding(e, report.Policy{EgressSurprise: report.LevelIgnore}),
		"a project that set this to ignore gets no finding")

	warn := egress.Finding(e, report.Policy{EgressSurprise: report.LevelWarn})
	require.NotNil(t, warn)
	require.Equal(t, report.LevelWarn, warn.Level,
		"the level comes from the manifest, never from the caller")
}

func TestObserve_HandlesAnEmptyLog(t *testing.T) {
	t.Parallel()
	c := egress.Observe(nil)
	require.Equal(t, 0, c.Total)
	require.Empty(t, c.Hosts)
	require.Nil(t, egress.CredentialFinding(c))
}
