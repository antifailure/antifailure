package fidelity_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The report said a wildcard capture was a faithful substitution, and for half
// the hosts such a rule matches the sidecar refuses it.
//
// The failure this exists to stop is the report lying about the one thing it
// exists to be honest about. A prospect's delivery path posts to third party
// CRMs through a Zapier catch hook. Written as *.zapier.com under capture,
// af fidelity reported `substituted, recorded into the inbox and answered with
// the provider's documented success shape`, and at run time the sidecar
// answered 403 and recorded nothing. Somebody reading that report before a demo
// would believe their deliveries were being captured.
//
// The correction is not the opposite claim. The sidecar captures a request
// under such a rule when this build has a handler for the host that actually
// arrives and refuses it otherwise, so *.resend.com IS captured and
// *.zapier.com is not, and neither answer can be read off the rule. Unmeasured
// is the package's own word for that, and the mock branch of the same function
// already uses it for the same rule shape through PackReason.
func TestThirdParty_AWildcardCaptureIsUnmeasuredRatherThanASubstitution(t *testing.T) {
	t.Parallel()

	obs := full()
	obs.Manifest.Egress.Rules = append(obs.Manifest.Egress.Rules,
		schema.EgressRule{Host: "*.zapier.com", Mode: schema.ModeCapture})
	obs.Hosts = append(obs.Hosts,
		fidelity.Host{Name: "*.zapier.com", Mode: schema.ModeCapture})

	c := componentState(t, fidelity.Build(obs), schema.FidelityThirdParty, "*.zapier.com")

	require.Equal(t, fidelity.Unmeasured, c.State,
		"a rule that does not name a host says nothing about what the sidecar will do")
	require.NotContains(t, c.Detail, "documented success shape",
		"the report must not claim a provider's own shape for a rule that may never reach capture")
	require.Contains(t, c.Detail, "naming a host",
		"an unmeasured component is only useful when it says why")
}

// The other half, and it is what makes the rule above a boundary rather than a
// blanket refusal to answer. A host somebody wrote down IS captured and
// answered, and the report should keep saying so.
func TestThirdParty_ANamedCaptureHostIsStillASubstitution(t *testing.T) {
	t.Parallel()

	c := componentState(t, fidelity.Build(full()), schema.FidelityThirdParty, "api.resend.com")

	require.Equal(t, fidelity.Substituted, c.State)
	require.Contains(t, c.Detail, "recorded into the inbox")
}

// An unmeasured component leaves the score and is named in Excluded, and this
// asserts the second half of that rather than trusting it.
//
// The two halves are not the same claim. Dropping out of the denominator is
// what stops a guess being counted; appearing in Excluded with a reason is what
// stops it disappearing silently, and a percentage that quietly absorbs an
// unknown is the thing this package exists to refuse.
func TestThirdParty_AWildcardCaptureLeavesTheScoreAndIsNamed(t *testing.T) {
	t.Parallel()

	obs := full()
	obs.Manifest.Egress.Rules = append(obs.Manifest.Egress.Rules,
		schema.EgressRule{Host: "*.zapier.com", Mode: schema.ModeCapture})
	obs.Hosts = append(obs.Hosts,
		fidelity.Host{Name: "*.zapier.com", Mode: schema.ModeCapture})

	before := fidelity.Build(full()).Score()
	after := fidelity.Build(obs).Score()

	require.Equal(t, before.Counted, after.Counted,
		"an unmeasured component is in neither half of the fraction")

	var named bool
	for _, e := range after.Excluded {
		if e.Component == "*.zapier.com" {
			named = true
			require.Contains(t, e.Because, "naming a host",
				"the exclusion carries the reason or it is a silent absence")
		}
	}
	require.True(t, named, "the excluded component has to appear in Excluded by name")
}

// An interior star is not the same rule shape and must not be swept up.
//
// email.*.amazonaws.com pins the service label and the label count, so it can
// only ever reach SES and no other service under that domain. The policy counts
// that as naming the host, `Decision.NamesHost` is true for it, and the sidecar
// captures it. Reporting it as undetermined would trade one wrong answer for
// another, quieter one, and this is what keeps the predicate a leading star
// rather than any star at all.
func TestThirdParty_AnInteriorStarNamesTheServiceAndIsStillASubstitution(t *testing.T) {
	t.Parallel()

	obs := full()
	obs.Manifest.Egress.Rules = append(obs.Manifest.Egress.Rules,
		schema.EgressRule{Host: "email.*.amazonaws.com", Mode: schema.ModeCapture})
	obs.Hosts = append(obs.Hosts,
		fidelity.Host{Name: "email.*.amazonaws.com", Mode: schema.ModeCapture})

	c := componentState(t, fidelity.Build(obs), schema.FidelityThirdParty, "email.*.amazonaws.com")

	require.Equal(t, fidelity.Substituted, c.State,
		"a pattern whose stars are all interior can reach only one service, "+
			"which is what the policy means by naming a host")
}
