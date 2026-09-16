package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
)

// A lifetime of zero is the immortal-environment hole. ParseDuration accepts
// "0h", so the duration check the runtime block already had passes it, and the
// runtimes then stamp no expiry label, and the reaper never sees the
// environment. It lives until somebody removes it by hand and reads the bill.
// There is no "never expires" on purpose: the manifest is the only source of an
// environment's lifetime, so a manifest that cannot say zero is a machine on
// which nothing can be born immortal.
//
// A NEGATIVE lifetime is caught one level up, by the duration-form pattern the
// schema publishes (^[0-9]+(ms|s|m|h|d)$ has no sign), so it never reaches this
// rule. The rule's own job is the value that PARSES and is still not positive,
// which is zero with a unit: "0h", "0s", "0m". Those are the cases here.

func TestParse_RejectsAZeroHourLifetime(t *testing.T) {
	t.Parallel()
	body := minimal + "\nruntime:\n  ttl: 0h\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))),
		`The lifetime "0h" is not positive.`)
}

func TestParse_RejectsAZeroSecondLifetime(t *testing.T) {
	t.Parallel()
	// A separate spelling of the same zero. It passes the duration-form pattern
	// (0 with a unit), so it reaches this rule and nothing before it, which is
	// what makes it a clean witness that the rule, and not the pattern, is what
	// refuses it.
	body := minimal + "\nruntime:\n  ttl: 0s\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))),
		`The lifetime "0s" is not positive.`)
}

func TestParse_AcceptsAPositiveLifetime(t *testing.T) {
	t.Parallel()
	// The liveness arm. A positive lifetime is exactly what the rule must let
	// through, or a rule that refused everything would prove nothing.
	body := minimal + "\nruntime:\n  ttl: 24h\n"
	_, err := parse(t, body)
	require.NoError(t, err)
}

// An environment created with no explicit lifetime inherits the default rather
// than living forever. This is the other half of closing the hole: "no ttl"
// must not mean "no expiry", it must mean the default, so the environment is
// reapable. Proven at the loaded manifest, because normalize runs before
// validate and the default is what the runtime reads.
func TestParse_NoLifetimeInheritsTheReapableDefault(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal)
	require.Equal(t, manifest.DefaultTTL, m.Runtime.TTL,
		"a manifest that states no ttl must carry the default, so the environment it creates is reapable")

	// And the default is a positive duration, so the runtime stamps an expiry
	// and the reaper can collect it. A default that did not parse to a positive
	// value would be the hole reopened one level down.
	d, err := manifest.ParseDuration(m.Runtime.TTL)
	require.NoError(t, err)
	require.Positive(t, d)
}
