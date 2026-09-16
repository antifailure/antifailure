package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestPolicy_DefaultsAreFilledIn(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal)
	require.NotNil(t, m.Policy)
	require.NotNil(t, m.Policy.MigrationLock)
	require.EqualValues(t, manifest.DefaultLockWarnMS, m.Policy.MigrationLock.WarnMS)
	require.EqualValues(t, manifest.DefaultLockFailMS, m.Policy.MigrationLock.FailMS)
	require.Equal(t, schema.PolicyFail, m.Policy.EgressSurprise)
	require.Equal(t, schema.PolicyFail, m.Policy.Cleanup)
	require.Equal(t, schema.PolicyFail, m.Policy.Masking)
	require.Equal(t, schema.PolicyFail, m.Policy.MigrationFailed)
	require.Equal(t, schema.PolicyWarn, m.Policy.MigrationLint)
	require.Equal(t, schema.PolicyWarn, m.Policy.MigrationRewrite)
	require.Equal(t, schema.PolicyWarn, m.Policy.PlanRegression)
	require.Equal(t, schema.PolicyWarn, m.Policy.QueryRegression)
	require.Equal(t, schema.PolicyWarn, m.Policy.LoadRegression)
	require.Equal(t, schema.PolicyWarn, m.Policy.Review,
		"the static code reviewer is advisory by default, so its normalized level is warn")
}

func TestPolicy_AnExplicitLevelSurvivesNormalisation(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+`
policy:
  migration_lint: ignore
  egress_surprise: warn
  migration_lock:
    warn_ms: 100
    fail_ms: 900
`)
	require.Equal(t, schema.PolicyIgnore, m.Policy.MigrationLint)
	require.Equal(t, schema.PolicyWarn, m.Policy.EgressSurprise)
	require.EqualValues(t, 100, m.Policy.MigrationLock.WarnMS)
	require.EqualValues(t, 900, m.Policy.MigrationLock.FailMS)
}

func TestPolicy_AnUnknownLevelIsRefusedRatherThanCoerced(t *testing.T) {
	t.Parallel()
	// Coercing would mean a manifest that says "block" quietly warns, and the
	// first anybody heard of it would be a merge that should not have
	// happened.
	_, err := parse(t, minimal+`
policy:
  egress_surprise: block
`)
	require.Contains(t, messages(problems(t, err)), "not a policy level")
}

func TestPolicy_ASecurityKeyTakesALevelAndRefusesAnUnknownOne(t *testing.T) {
	t.Parallel()
	// A security override is the same three levels as every other key, and an
	// unrecognised one is refused rather than coerced. The KEY is not
	// constrained: a family declares the legal keys, and until it lands a
	// manifest may name one, so security.authz.idor parses whether or not a
	// family reads it yet.
	m, err := parse(t, minimal+`
policy:
  security:
    security.authz.idor: fail
    security.headers.missing_hsts: warn
`)
	require.NoError(t, err)
	require.Equal(t, schema.PolicyFail, m.Policy.Security["security.authz.idor"])

	_, err = parse(t, minimal+`
policy:
  security:
    security.authz.idor: block
`)
	require.Contains(t, messages(problems(t, err)), "not a policy level")
}

func TestPolicy_AFailingThresholdBelowTheWarningOneIsRefused(t *testing.T) {
	t.Parallel()
	// A lock long enough to fail the check is always long enough to be worth
	// reporting, so the pair has only one sensible order.
	_, err := parse(t, minimal+`
policy:
  migration_lock:
    warn_ms: 4000
    fail_ms: 1000
`)
	require.Contains(t, messages(problems(t, err)), "below the warning threshold")
}
