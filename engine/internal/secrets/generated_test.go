package secrets_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/livekey"
)

func TestGeneratedSource_RunsTheRecipeOnlyWhenSomebodyAsks(t *testing.T) {
	t.Parallel()
	// The laziness is the reason this type exists rather than a map. A 2048 bit
	// key costs enough to notice and most environments never declare the
	// variable, so an environment that does not ask must not pay.
	runs := 0
	src := secrets.NewGeneratedSource("a label", map[string]func() (string, error){
		"A_NAME": func() (string, error) { runs++; return "a value", nil },
	})
	require.Zero(t, runs, "the recipe ran before anything looked the name up")

	_, found, err := src.Lookup(context.Background(), "SOMETHING_ELSE")
	require.NoError(t, err)
	require.False(t, found)
	require.Zero(t, runs, "a lookup of a different name ran the recipe")

	v, found, err := src.Lookup(context.Background(), "A_NAME")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "a value", v.Reveal())
	require.Equal(t, 1, runs)
}

func TestGeneratedSource_AnswersTheSameThingTwice(t *testing.T) {
	t.Parallel()
	// A source consulted twice has to answer the same thing twice. A key that
	// changed between af up resolving it and af explain reporting it would be
	// two identities where the user was told there was one.
	runs := 0
	src := secrets.NewGeneratedSource("a label", map[string]func() (string, error){
		"A_NAME": func() (string, error) { runs++; return string(rune('a' + runs)), nil },
	})
	first, _, err := src.Lookup(context.Background(), "A_NAME")
	require.NoError(t, err)
	second, _, err := src.Lookup(context.Background(), "A_NAME")
	require.NoError(t, err)
	require.Equal(t, first.Reveal(), second.Reveal())
	require.Equal(t, 1, runs, "the recipe ran twice")
}

func TestGeneratedSource_ReportsAFailedRecipeRatherThanAnEmptyValue(t *testing.T) {
	t.Parallel()
	// An empty value handed to a service is a container that starts and fails
	// ten seconds later in a log nobody is watching.
	src := secrets.NewGeneratedSource("a label", map[string]func() (string, error){
		"A_NAME": func() (string, error) { return "", errors.New("no entropy") },
	})
	_, found, err := src.Lookup(context.Background(), "A_NAME")
	require.Error(t, err)
	require.False(t, found)
	require.Contains(t, err.Error(), "A_NAME")
}

func TestGeneratedSource_IsInvisibleWhenItHoldsNothing(t *testing.T) {
	t.Parallel()
	// A source with nothing to generate must not appear in the "Looked in"
	// list of a missing variable. A list naming a source that could never have
	// answered sends somebody to configure a place that does not exist.
	empty := secrets.NewGeneratedSource("a label", nil)
	ok, _ := empty.Available(context.Background())
	require.False(t, ok)

	full := secrets.NewGeneratedSource("a label", map[string]func() (string, error){
		"A_NAME": func() (string, error) { return "a value", nil },
	})
	ok, _ = full.Available(context.Background())
	require.True(t, ok)
}

func TestGenerateGitHubAppPrivateKey_IsAKeyTheApplicationsCanRead(t *testing.T) {
	t.Parallel()
	// The half configured case is refused by every application that reads
	// these three variables together, so a placeholder string would either be
	// rejected at startup or fail later with a decoder error naming the wrong
	// problem. It has to be a real key.
	value, err := secrets.GenerateGitHubAppPrivateKey()
	require.NoError(t, err)

	block, rest := pem.Decode([]byte(value))
	require.NotNil(t, block, "the value is not PEM")
	require.Equal(t, "PRIVATE KEY", block.Type, "GitHub issues PKCS#8 and Node reads it without argument")
	require.Empty(t, rest)

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	require.NoError(t, err)
	require.NotNil(t, key)
}

func TestGenerateGitHubAppPrivateKey_IsDifferentEveryTime(t *testing.T) {
	t.Parallel()
	// Nothing outside the environment reproduces it, so there is nothing to be
	// gained by making it predictable and something to lose.
	first, err := secrets.GenerateGitHubAppPrivateKey()
	require.NoError(t, err)
	second, err := secrets.GenerateGitHubAppPrivateKey()
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}

func TestGenerateGitHubAppPrivateKey_IsWhatTheCredentialGateRefuses(t *testing.T) {
	t.Parallel()
	// The point of generating it is that it never has to be written down, and
	// the check that would object if it were is the one this asserts against.
	// If this ever stops finding it, the key stopped being real.
	value, err := secrets.GenerateGitHubAppPrivateKey()
	require.NoError(t, err)
	found := livekey.Scan(value, "the value")
	require.Len(t, found, 1)
	require.Equal(t, "Private key", found[0].Provider)
}
