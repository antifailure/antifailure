package secrets_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func required(name string) schema.EnvVar { return schema.EnvVar{Name: name} }

func optional(name string) schema.EnvVar {
	no := false
	return schema.EnvVar{Name: name, Required: &no}
}

func TestResolve_HandsServicesWhatTheManifestDeclaresAndNothingElse(t *testing.T) {
	t.Parallel()
	// A preview environment that inherited the shell it was started from would
	// inherit AWS credentials, a production database URL, and whatever else is
	// exported on a developer's laptop.
	chain := secrets.NewChain(envSource("shell", map[string]string{
		"DATABASE_URL":          "postgres://declared",
		"AWS_SECRET_ACCESS_KEY": "not-declared-and-must-not-travel",
		"PROD_ADMIN_TOKEN":      "definitely-not",
	}))

	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{required("DATABASE_URL")},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"DATABASE_URL"}, keys(got.Service))
	require.Equal(t, "postgres://declared", got.Service["DATABASE_URL"].Reveal())
}

func TestResolve_ASandboxCredentialNeverReachesTheService(t *testing.T) {
	t.Parallel()
	// The whole point of substituting at the boundary is that the application
	// never holds one.
	chain := secrets.NewChain(envSource("shell", map[string]string{
		"STRIPE_SECRET_KEY": "sk_test_realish_value",
	}))

	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{required("STRIPE_SECRET_KEY")},
		Sandbox:  []string{"STRIPE_SECRET_KEY"},
		EnvID:    "af-shopfront-feature-checkout",
	})
	require.NoError(t, err)

	require.Equal(t, "sk_test_realish_value", got.Sidecar["STRIPE_SECRET_KEY"].Reveal())

	service := got.Service["STRIPE_SECRET_KEY"].Reveal()
	require.NotEqual(t, "sk_test_realish_value", service, "the service was handed the real credential")
	// Unmistakable, so a value in a log is obviously a placeholder rather than
	// something somebody has to go and check.
	require.Contains(t, service, "not_a_real_credential")
	require.Contains(t, service, "stripe_secret_key")
	require.Contains(t, service, "af-shopfront")
}

func TestResolve_AServiceStillGetsSomethingRatherThanNothing(t *testing.T) {
	t.Parallel()
	// An application reading an unset variable usually crashes on startup with
	// a message about configuration, which looks like a bug in the tool.
	chain := secrets.NewChain(envSource("shell", map[string]string{"K": "v"}))
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Sandbox: []string{"K"}, EnvID: "af-1",
	})
	require.NoError(t, err)
	require.NotEmpty(t, got.Service["K"].Reveal())
}

func TestResolve_ASandboxCredentialIsFetchedEvenIfNoServiceDeclaresIt(t *testing.T) {
	t.Parallel()
	// The sidecar needs it whether or not the application does.
	chain := secrets.NewChain(envSource("shell", map[string]string{"STRIPE_SECRET_KEY": "sk_test_x"}))
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Sandbox: []string{"STRIPE_SECRET_KEY"}, EnvID: "af-1",
	})
	require.NoError(t, err)
	require.Contains(t, got.Sidecar, "STRIPE_SECRET_KEY")
}

func TestResolve_ALiveCredentialInASandboxSlotIsRefused(t *testing.T) {
	t.Parallel()
	// It would be substituted into every request to that provider, which is the
	// opposite of what sandbox mode is for, and it would charge real cards.
	live := "sk_live_" + strings.Repeat("a", 24)
	chain := secrets.NewChain(envSource("shell", map[string]string{"STRIPE_SECRET_KEY": live}))

	_, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{required("STRIPE_SECRET_KEY")},
		Sandbox:  []string{"STRIPE_SECRET_KEY"},
		EnvID:    "af-1",
	})
	require.Error(t, err)
	var liveErr *secrets.LiveCredentialError
	require.ErrorAs(t, err, &liveErr)
	require.Equal(t, "STRIPE_SECRET_KEY", liveErr.Name)
	require.Equal(t, "shell", liveErr.Source, "the message should say where the wrong value came from")
	// And it must not quote the credential while explaining.
	require.NotContains(t, err.Error(), live)
}

func TestResolve_ALiveCredentialInAnOrdinarySlotIsNotRefused(t *testing.T) {
	t.Parallel()
	// A live key in a variable no sandbox rule names is the user's own
	// business: it might be a read-only production key they deliberately want
	// the preview to use. The refusal is specifically about substitution.
	live := "sk_live_" + strings.Repeat("a", 24)
	chain := secrets.NewChain(envSource("shell", map[string]string{"SOME_KEY": live}))

	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{required("SOME_KEY")}, EnvID: "af-1",
	})
	require.NoError(t, err)
	require.Equal(t, live, got.Service["SOME_KEY"].Reveal())
}

func TestResolve_ReportsEveryMissingVariableAtOnce(t *testing.T) {
	t.Parallel()
	// Somebody with three to set wants to be told all three, not to run the
	// command three times.
	chain := secrets.NewChain(envSource("shell", map[string]string{}))
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{required("A"), required("B"), required("C")},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Len(t, got.Missing, 3)
	require.Equal(t, []string{"A", "B", "C"}, missingNames(got.Missing))
	// And it says where it looked, so the message can say where to put it.
	require.Equal(t, []string{"shell"}, got.Missing[0].Searched)
}

func TestResolve_AnOptionalVariableIsReportedSeparatelyFromAMissingOne(t *testing.T) {
	t.Parallel()
	// Mixing them would make a warning look like an error, and every user would
	// learn to ignore both.
	chain := secrets.NewChain(envSource("shell", map[string]string{}))
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{required("NEEDED"), optional("NICE_TO_HAVE")},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"NEEDED"}, missingNames(got.Missing))
	require.Equal(t, []string{"NICE_TO_HAVE"}, missingNames(got.Optional))
}

func TestResolve_ASandboxCredentialIsAlwaysRequired(t *testing.T) {
	t.Parallel()
	// The rule naming it says requests to that provider get substituted, and
	// there is nothing to substitute.
	chain := secrets.NewChain(envSource("shell", map[string]string{}))
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{optional("STRIPE_SECRET_KEY")},
		Sandbox:  []string{"STRIPE_SECRET_KEY"},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"STRIPE_SECRET_KEY"}, missingNames(got.Missing))
	require.Empty(t, got.Optional)
}

func TestResolve_ALiteralInTheManifestIsNotLookedUp(t *testing.T) {
	t.Parallel()
	// It is in the repository, so treating it as a secret would put a value
	// nobody considered private into the redactor and the audit trail.
	chain := secrets.NewChain(envSource("shell", map[string]string{"NODE_ENV": "production"}))
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{{Name: "NODE_ENV", Value: "preview"}},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Equal(t, "preview", got.Service["NODE_ENV"].Reveal(),
		"the manifest's literal should win over the shell")
	require.Equal(t, "the manifest", got.Resolutions[0].Source)
}

func TestResolve_ARenameLooksUpOneNameAndDeliversAnother(t *testing.T) {
	t.Parallel()
	chain := secrets.NewChain(envSource("shell", map[string]string{"PROD_DATABASE_URL": "postgres://x"}))
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{{Name: "DATABASE_URL", From: "PROD_DATABASE_URL"}},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Equal(t, "postgres://x", got.Service["DATABASE_URL"].Reveal())
	require.NotContains(t, got.Service, "PROD_DATABASE_URL")
	// The trail has to be followable, so the record names both.
	require.Contains(t, got.Resolutions[0].Name, "DATABASE_URL")
	require.Contains(t, got.Resolutions[0].Name, "PROD_DATABASE_URL")
}

func TestResolve_ARenameThatIsMissingNamesTheNameToSet(t *testing.T) {
	t.Parallel()
	// Telling somebody DATABASE_URL is missing when the variable to set is
	// PROD_DATABASE_URL is a message that actively misleads.
	chain := secrets.NewChain(envSource("shell", map[string]string{}))
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{{Name: "DATABASE_URL", From: "PROD_DATABASE_URL"}},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"PROD_DATABASE_URL"}, missingNames(got.Missing))
}

func TestResolve_WhereTwoServicesDisagreeTheStricterWins(t *testing.T) {
	t.Parallel()
	chain := secrets.NewChain(envSource("shell", map[string]string{}))

	// One service says optional, the other required.
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{optional("SHARED"), required("SHARED")},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"SHARED"}, missingNames(got.Missing),
		"a variable one service requires is required")

	// One says sandbox, the other does not. A variable that is a credential in
	// one service's view is a credential.
	chain = secrets.NewChain(envSource("shell", map[string]string{"K": "v"}))
	got, err = secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{{Name: "K"}, {Name: "K", Sandbox: true}},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Contains(t, got.Sidecar, "K")
	require.Contains(t, got.Service["K"].Reveal(), "not_a_real_credential")
}

func TestResolve_ProducesOneRecordPerVariableSorted(t *testing.T) {
	t.Parallel()
	// Two services declaring the same variable produce one lookup and one audit
	// record, in a stable order, so two runs can be compared.
	chain := secrets.NewChain(envSource("shell", map[string]string{
		"B": "1", "A": "2", "C": "3",
	}))
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{required("C"), required("A"), required("B"), required("A")},
		EnvID:    "af-1",
	})
	require.NoError(t, err)
	require.Len(t, got.Resolutions, 3)
	require.Equal(t, []string{"A", "B", "C"}, resolutionNames(got.Resolutions))
}

func TestAuditFields_CarryNamesAndSourcesAndNeverValues(t *testing.T) {
	t.Parallel()
	// This goes into the event log, af explain, and a support bundle, so it has
	// to be safe to show to somebody who should not see the secrets.
	chain := secrets.NewChain(envSource("shell", map[string]string{
		"STRIPE_SECRET_KEY": "sk_test_distinctive",
	}))
	got, err := secrets.Resolve(t.Context(), chain, secrets.Request{
		Declared: []schema.EnvVar{required("STRIPE_SECRET_KEY")}, EnvID: "af-1",
	})
	require.NoError(t, err)

	fields := secrets.AuditFields(got.Resolutions)
	require.Len(t, fields, 1)
	require.Equal(t, "STRIPE_SECRET_KEY", fields[0]["name"])
	require.Equal(t, "shell", fields[0]["source"])
	require.NotEmpty(t, fields[0]["fingerprint"])

	for _, field := range fields {
		for key, value := range field {
			require.NotContainsf(t, value, "sk_test_distinctive",
				"the %s field carries the value itself", key)
		}
	}
}

func TestFingerprint_IsStableAndDoesNotRevealTheValue(t *testing.T) {
	t.Parallel()
	// Two environments can be compared without either value being shown.
	a := secrets.New("the-same-value")
	b := secrets.New("the-same-value")
	c := secrets.New("a-different-value")

	require.Equal(t, a.Fingerprint(), b.Fingerprint())
	require.NotEqual(t, a.Fingerprint(), c.Fingerprint())
	require.NotContains(t, a.Fingerprint(), "the-same-value")
	require.Less(t, len(a.Fingerprint()), 20, "a fingerprint long enough to brute force is not one")
}

// ---------------------------------------------------------------------------

func keys(m map[string]secrets.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return sortedCopy(out)
}

func missingNames(ms []secrets.Missing) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

func resolutionNames(rs []secrets.Resolution) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
