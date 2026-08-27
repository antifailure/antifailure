package secrets_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// A registered source, as an enterprise adapter would write one: standard
// library types only, and no knowledge of the engine's Value at all.
type fakeRegistered struct {
	name    string
	values  map[string]string
	ok      bool
	why     string
	err     error
	lookups *int
}

func (f *fakeRegistered) Name() string { return f.name }

func (f *fakeRegistered) Available(context.Context) (bool, string) { return f.ok, f.why }

func (f *fakeRegistered) Lookup(_ context.Context, name string) (string, bool, error) {
	if f.lookups != nil {
		*f.lookups++
	}
	if f.err != nil {
		return "", false, f.err
	}
	v, ok := f.values[name]
	return v, ok, nil
}

func TestNothingRegisteredIsTheCommunityChain(t *testing.T) {
	// The property the whole extension package is built around. With nothing
	// registered the chain has to be exactly what it was before this hook
	// existed, or the community edition has been changed by the presence of a
	// socket nothing is plugged into.
	require.Nil(t, secrets.Registered(nil), "a nil registry must add nothing")
	require.Nil(t, secrets.Registered(extension.NewRegistry()), "an empty registry must add nothing")
}

func TestARegisteredSourceIsAskedLastAndTaggedByName(t *testing.T) {
	reg := extension.NewRegistry()
	reg.AddSecretSource(&fakeRegistered{
		name: "HashiCorp Vault at vault.internal", ok: true,
		values: map[string]string{"SHARED": "from-vault", "ONLY_REMOTE": "remote"},
	})

	local := &secrets.EnvSource{
		Label: "this shell's environment",
		Getenv: func(name string) (string, bool) {
			v, ok := map[string]string{"SHARED": "from-shell"}[name]
			return v, ok
		},
	}
	chain := secrets.NewChain(append([]secrets.Source{local}, secrets.Registered(reg)...)...)

	// Local wins. That is the ordering rule, and it is what makes "try it with
	// a different key" a one line thing rather than a change every colleague
	// resolves differently.
	value, res, found, err := chain.Lookup(t.Context(), "SHARED")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "from-shell", value.Reveal())
	require.Equal(t, "this shell's environment", res.Source)

	// And the registered source is reached for what nothing local has.
	value, res, found, err = chain.Lookup(t.Context(), "ONLY_REMOTE")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "remote", value.Reveal())
	require.Equal(t, "HashiCorp Vault at vault.internal", res.Source,
		"the audit record has to name the source that answered")
	require.NotEmpty(t, res.Fingerprint)
}

func TestTwoRegisteredSourcesAreAskedInRegistrationOrder(t *testing.T) {
	first := 0
	second := 0
	reg := extension.NewRegistry()
	reg.AddSecretSource(&fakeRegistered{
		name: "first", ok: true, lookups: &first,
		values: map[string]string{"BOTH": "from-first"},
	})
	reg.AddSecretSource(&fakeRegistered{
		name: "second", ok: true, lookups: &second,
		values: map[string]string{"BOTH": "from-second"},
	})

	chain := secrets.NewChain(secrets.Registered(reg)...)
	value, res, found, err := chain.Lookup(t.Context(), "BOTH")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "from-first", value.Reveal())
	require.Equal(t, "first", res.Source)
	require.Equal(t, 1, first)
	require.Equal(t, 0, second, "the second source must not be asked once the first answered")
}

func TestARegisteredSourceThatCannotAnswerSaysWhy(t *testing.T) {
	reg := extension.NewRegistry()
	reg.AddSecretSource(&fakeRegistered{
		name: "AWS Secrets Manager in eu-west-1",
		ok:   false, why: "the credentials expired at 09:14 UTC",
	})
	chain := secrets.NewChain(secrets.Registered(reg)...)

	// This is the sentence AF-SEC-001 prints. A source that returned false with
	// nothing to say would appear as "not present", which is a different claim
	// and the wrong one.
	require.Equal(t,
		[]string{"AWS Secrets Manager in eu-west-1 (the credentials expired at 09:14 UTC)"},
		chain.Considered(t.Context()))
	require.Equal(t,
		[]string{"AWS Secrets Manager in eu-west-1 (the credentials expired at 09:14 UTC)"},
		chain.Unavailable(t.Context()))
	require.Empty(t, chain.Sources(t.Context()))

	// And it is not asked, so an unusable source cannot answer by accident.
	_, _, found, err := chain.Lookup(t.Context(), "ANYTHING")
	require.NoError(t, err)
	require.False(t, found)
}

func TestARegisteredSourceThatFailsStopsTheChain(t *testing.T) {
	// Rather than falling through. A source that is reachable and erroring must
	// not hand the application the value a lower priority source has, because
	// that is the same variable resolving to a different secret than it did
	// yesterday, with nothing said about it.
	boom := errors.New("vault sealed")
	reg := extension.NewRegistry()
	reg.AddSecretSource(&fakeRegistered{name: "Vault", ok: true, err: boom})

	lower := &secrets.EnvSource{
		Label:  "a lower source",
		Getenv: func(string) (string, bool) { return "stale", true },
	}
	chain := secrets.NewChain(append(secrets.Registered(reg), lower)...)

	_, _, found, err := chain.Lookup(t.Context(), "DATABASE_URL")
	require.Error(t, err)
	require.False(t, found)
	require.ErrorIs(t, err, boom)
	require.Contains(t, err.Error(), "Vault", "the failing source has to be named")
}

func TestAValueFromARegisteredSourceIsRedactedAtTheBoundary(t *testing.T) {
	// The reason the interface hands over a plain string and this package wraps
	// it. An adapter author never touches Value, so an adapter author cannot
	// forget to redact: there is one conversion point and it is this one.
	reg := extension.NewRegistry()
	reg.AddSecretSource(&fakeRegistered{
		name: "Vault", ok: true,
		values: map[string]string{"STRIPE_SECRET_KEY": "sk_test_not_a_real_key_9f2a"},
	})
	chain := secrets.NewChain(secrets.Registered(reg)...)

	value, res, found, err := chain.Lookup(t.Context(), "STRIPE_SECRET_KEY")
	require.NoError(t, err)
	require.True(t, found)

	for _, rendered := range []string{
		fmt.Sprint(value), fmt.Sprintf("%v", value), fmt.Sprintf("%s", value),
		fmt.Sprintf("%q", value), fmt.Sprintf("%#v", value), fmt.Sprintf("%x", value),
	} {
		require.NotContains(t, rendered, "sk_test_not_a_real_key_9f2a")
		require.Contains(t, rendered, secrets.Redacted)
	}
	// And the audit record carries the name and a fingerprint, never the value.
	require.NotContains(t, fmt.Sprint(res), "sk_test_not_a_real_key_9f2a")
	require.Equal(t, "Vault", value.Source())
}
