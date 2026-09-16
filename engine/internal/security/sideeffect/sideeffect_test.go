package sideeffect

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// policy builds a resolved policy with the two keys set to the given levels,
// standing in for the router's default overlay plus the manifest overrides.
func policy(externalCall, destructive report.Level) report.Policy {
	return report.Policy{Security: map[report.PolicyKey]report.Level{
		RuleExternalCall: externalCall,
		RuleDestructive:  destructive,
	}}
}

func TestClassify_CountsEachEffectByMeaningNotHost(t *testing.T) {
	decisions := []local.Decision{
		{Host: "api.stripe.com", Method: "POST", Path: "/v1/payment_intents"},
		{Host: "api.stripe.com", Method: "POST", Path: "/v1/charges"},
		{Host: "api.stripe.com", Method: "POST", Path: "/v1/refunds"},
		{Host: "ec2.us-east-1.amazonaws.com", Method: "DELETE", Path: "/instances/i-1"},
		{Host: "compute.googleapis.com", Method: "POST", Path: "/projects/p/instances"},
		{Host: "sqs.us-east-1.amazonaws.com", Method: "POST", Path: "/queue"},
		// An unknown host is not classified and must not inflate any count.
		{Host: "example.com", Method: "POST", Path: "/anything"},
		// A GET to a cloud host is a read, not a create or delete.
		{Host: "compute.googleapis.com", Method: "GET", Path: "/projects/p/instances"},
	}
	messages := []local.Message{
		{Kind: "email"}, {Kind: "email"}, {Kind: "sms"}, {Kind: "webhook"},
	}
	got := Classify(decisions, messages)
	require.Equal(t, 2, got[ClassPayment], "payment_intents and charges are two payments")
	require.Equal(t, 1, got[ClassRefund])
	require.Equal(t, 1, got[ClassCloudDelete])
	require.Equal(t, 1, got[ClassCloudCreate])
	require.Equal(t, 1, got[ClassQueuePublish])
	require.Equal(t, 2, got[ClassEmail])
	require.Equal(t, 1, got[ClassSMS])
	require.Equal(t, 1, got[ClassWebhook])
}

func TestDetect_IncreaseFiresOnTheDelta(t *testing.T) {
	// The headline: the checkout made one PaymentIntent before and three after.
	head := Counts{ClassPayment: 3}
	base := Counts{ClassPayment: 1}
	findings := Detect(head, base, true, policy(report.LevelFail, report.LevelFail))
	require.Len(t, findings, 1)
	f := findings[0]
	require.Equal(t, string(RuleExternalCall), f.Rule)
	require.Equal(t, report.LevelFail, f.Level)
	require.Equal(t, 2, f.Count, "the delta is three minus one")
	require.Equal(t, string(ClassPayment), f.Where)
	require.Contains(t, f.Detail, "base branch made 1")
	require.Contains(t, f.Detail, "made 3")
}

func TestDetect_NoIncreaseWhenHeadDoesNotExceedBase(t *testing.T) {
	head := Counts{ClassPayment: 1}
	base := Counts{ClassPayment: 1}
	require.Empty(t, Detect(head, base, true, policy(report.LevelFail, report.LevelFail)),
		"an equal count is not an increase")
}

func TestDetect_MissingBaselineDoesNotReadAsZero(t *testing.T) {
	// The fail closed rule: with no base twin, the increase comparison is not
	// made at all. Reading the missing base as zero would report every payment
	// as new, which is the defect this family exists to avoid.
	head := Counts{ClassPayment: 3}
	findings := Detect(head, nil, false, policy(report.LevelFail, report.LevelFail))
	require.Empty(t, findings, "a missing baseline is unmeasured, never a baseline of zero")
}

func TestDetect_DestructiveFiresRegardlessOfBaseline(t *testing.T) {
	// A destructive op is refused even when the base branch did it too, and it
	// is reported under its own rule, never as a mere increase.
	head := Counts{ClassCloudDelete: 1}
	base := Counts{ClassCloudDelete: 1}
	findings := Detect(head, base, true, policy(report.LevelFail, report.LevelFail))
	require.Len(t, findings, 1)
	require.Equal(t, string(RuleDestructive), findings[0].Rule)
	require.Equal(t, report.LevelFail, findings[0].Level)
	require.Equal(t, string(ClassCloudDelete), findings[0].Where)
}

func TestDetect_DestructiveFiresEvenWithoutBaseline(t *testing.T) {
	head := Counts{ClassCloudDelete: 2}
	findings := Detect(head, nil, false, policy(report.LevelFail, report.LevelFail))
	require.Len(t, findings, 1)
	require.Equal(t, string(RuleDestructive), findings[0].Rule)
	require.Equal(t, 2, findings[0].Count)
}

func TestDetect_NoDestructiveWhenNoneOccurred(t *testing.T) {
	// A zero destructive count emits nothing: the count guard must not report a
	// destructive operation that did not happen.
	require.Empty(t, Detect(Counts{ClassPayment: 2}, nil, false, policy(report.LevelFail, report.LevelFail)),
		"no cloud delete occurred, so no destructive finding")
}

func TestDetect_IgnoreLevelDropsTheFinding(t *testing.T) {
	head := Counts{ClassPayment: 3, ClassCloudDelete: 1}
	base := Counts{ClassPayment: 1}
	require.Empty(t, Detect(head, base, true, policy(report.LevelIgnore, report.LevelIgnore)),
		"a key resolved to ignore emits nothing")
}

func TestProbe_ReadsInputAndReturnsFindings(t *testing.T) {
	in := security.Input{Policy: policy(report.LevelFail, report.LevelFail)}.
		WithRunArtifacts(security.RunArtifacts{
			Decisions: []local.Decision{
				{Host: "api.stripe.com", Method: "POST", Path: "/v1/payment_intents"},
				{Host: "api.stripe.com", Method: "POST", Path: "/v1/payment_intents"},
				{Host: "api.stripe.com", Method: "POST", Path: "/v1/payment_intents"},
			},
			Baseline: &security.Baseline{Decisions: []local.Decision{
				{Host: "api.stripe.com", Method: "POST", Path: "/v1/payment_intents"},
			}},
		})
	findings, err := New().Probe(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	require.Equal(t, string(RuleExternalCall), findings[0].Rule)
	require.Equal(t, 2, findings[0].Count)
}

func TestProbe_UnreadEffectsAreBlockedNeverAPass(t *testing.T) {
	// An Input with neither a decision nor a message log attached is a run whose
	// effects were not captured: a blocked probe, never an empty pass.
	findings, err := New().Probe(context.Background(), security.Input{Policy: policy(report.LevelFail, report.LevelFail)})
	require.Error(t, err, "an unread effect log is a blocked probe, not an empty pass")
	require.Nil(t, findings)
}

func TestProbe_MissingBaselineStillFiresDestructiveButNotIncrease(t *testing.T) {
	// Candidate decisions are attached (so the probe is not blocked) but no base
	// twin was built (Baseline nil, ok=false).
	in := security.Input{Policy: policy(report.LevelFail, report.LevelFail)}.
		WithRunArtifacts(security.RunArtifacts{Decisions: []local.Decision{
			{Host: "ec2.us-east-1.amazonaws.com", Method: "DELETE", Path: "/instances/i-1"},
			{Host: "api.stripe.com", Method: "POST", Path: "/v1/payment_intents"},
		}})
	findings, err := New().Probe(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, findings, 1, "no baseline means no increase finding, but destructive still fires")
	require.Equal(t, string(RuleDestructive), findings[0].Rule)
}

func TestFindingsCarryNoValue(t *testing.T) {
	// A finding names the class and the counts, never a host, a body, or a row.
	head := Counts{ClassPayment: 3, ClassCloudDelete: 1}
	base := Counts{ClassPayment: 1}
	for _, f := range Detect(head, base, true, policy(report.LevelFail, report.LevelFail)) {
		require.NotContains(t, f.Detail, "stripe", "a finding must not name the destination host")
		require.NotContains(t, f.Detail, "amazonaws")
		require.NotContains(t, f.Where, ".com")
	}
}

func TestKeys_DeclaredExitMatchesExitFor(t *testing.T) {
	for _, k := range keys() {
		require.Equalf(t, security.ExitFor(k.Key), k.Exit,
			"the declared exit for %s must equal security.ExitFor", k.Key)
	}
	// And the shape is the one the spine committed: external_call is a policy
	// denial, destructive is a verification failure.
	require.Equal(t, report.ExitPolicyDenial, security.ExitFor(RuleExternalCall))
	require.Equal(t, report.ExitVerification, security.ExitFor(RuleDestructive))
}

func TestFamily_ShapeIsRegisterable(t *testing.T) {
	f := New()
	require.Equal(t, "side_effect", f.Name())
	require.NotEmpty(t, f.Surfaces(), "a family with no surface would never run")
	require.Equal(t, []report.PolicyKey{RuleExternalCall, RuleDestructive},
		[]report.PolicyKey{f.Keys()[0].Key, f.Keys()[1].Key})
	require.Empty(t, f.Licensed(), "the family runs in every edition")
	// The registry accepts it without panicking, which is the surface check.
	reg := security.NewRegistry()
	require.NotPanics(t, func() { reg.Register(f) })
	require.Contains(t, strings.Join(checkNames(f.Checks()), ","), "side_effect")
}

func checkNames(cs []change.Check) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, string(c))
	}
	return out
}
