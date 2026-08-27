//go:build windows

package secrets_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Against the real Credential Manager, not a fake.
//
// Everything this file asks was a guess in the implementation, and each guess
// is a real failure if it is wrong: which error advapi32 reports for a target
// that is not stored, whether CredWrite replaces an existing credential or
// refuses, whether deleting something absent errors, and whether the blob comes
// back as the bytes that went in. None of those can be learned from a stand-in
// for advapi32, because a stand-in would agree with whatever this code assumed.
//
// This runs on a windows-latest runner in CI. There is no Windows machine in
// the development loop for this project, so CI is the only honest place to
// prove it, and a claim that it works without a run there would be a claim
// about code nobody has executed.
func requireKeyring(t *testing.T) secrets.Keyring {
	t.Helper()
	ring := secrets.NewSystemKeyring()
	require.NotNil(t, ring, "windows must have a keyring")
	return ring
}

func TestSystemKeyringRoundTrips(t *testing.T) {
	ring := requireKeyring(t)

	const service = "antifailure-test"
	const name = "round-trip"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	// Missing is a miss, not a failure. The chain relies on telling those
	// apart: a miss falls through to the next source and a failure does not.
	_, err := ring.Get(service, "definitely-not-set")
	require.ErrorIs(t, err, secrets.ErrNotFound)

	require.NoError(t, ring.Set(service, name, "first"))
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, "first", got)

	// A second write has to replace rather than fail, or 'af secret set' can
	// only ever be run once per name.
	require.NoError(t, ring.Set(service, name, "second"))
	got, err = ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, "second", got)

	require.NoError(t, ring.Delete(service, name))
	_, err = ring.Get(service, name)
	require.ErrorIs(t, err, secrets.ErrNotFound)

	// Deleting something already gone succeeds, for the same reason every
	// teardown in this product does: the caller wanted it gone.
	require.NoError(t, ring.Delete(service, name))
}

func TestAValueWithAwkwardCharactersSurvives(t *testing.T) {
	ring := requireKeyring(t)
	const service = "antifailure-test"
	const name = "awkward"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	// A connection string is the value most likely to be stored here, and it
	// carries the characters most likely to be mangled on the way through.
	const value = `postgres://u:p@ss w0rd!"'$&@host:5432/db?opt=a b`
	require.NoError(t, ring.Set(service, name, value))
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, value, got, "the value did not survive the round trip")
}

// The blob is UTF-16, and UTF-16 is where a non-ASCII value goes wrong.
//
// A character outside the basic multilingual plane is two code units rather
// than one, which is exactly the case a length calculation gets wrong, and a
// terminating NUL left on the end of a counted buffer comes back as a stray
// rune. Both would pass every ASCII test in this file.
func TestNonASCIIAndAstralCharactersSurvive(t *testing.T) {
	ring := requireKeyring(t)
	const service = "antifailure-test"
	const name = "unicode"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	const value = "päßwörd 日本語 \U0001F511 éèê"
	require.NoError(t, ring.Set(service, name, value))
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, value, got)
	require.NotContains(t, got, "\x00", "a terminator was stored inside the value")
}

// A multi-line value, because a PEM private key is one and it is the value most
// likely to be stored in a credential store rather than a .env file. It also
// pins that no newline is eaten or invented, which is the mistake the macOS
// implementation has to correct for and this one must not make.
func TestAMultiLineValueAndTrailingNewlineSurvive(t *testing.T) {
	ring := requireKeyring(t)
	const service = "antifailure-test"
	const name = "multi-line"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	// Assembled at run time rather than written out, so that no line in this
	// repository looks like the beginning of a real key to a secret scanner.
	value := "-----BEGIN " + "TEST" + " KEY-----\nline one\nline two\n-----END " + "TEST" + " KEY-----\n"
	require.NoError(t, ring.Set(service, name, value))
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, value, got)
	require.True(t, strings.HasSuffix(got, "\n"), "the trailing newline was eaten")
}

// A long value, because the blob is length delimited by a uint32 and a
// connection string with a base64 certificate in it is the realistic large one.
func TestALargeValueSurvives(t *testing.T) {
	ring := requireKeyring(t)
	const service = "antifailure-test"
	const name = "large"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	value := strings.Repeat("abcdefghij", 200)
	require.NoError(t, ring.Set(service, name, value))
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, value, got)
}

// An entry that exists and holds nothing is present, not absent.
//
// The chain treats present-and-empty as an answer, so that a variable somebody
// deliberately set to nothing does not fall through to a lower priority source
// with a stale value in it. That only holds if the store reports it that way.
func TestAnEmptyValueIsPresentRatherThanMissing(t *testing.T) {
	ring := requireKeyring(t)
	const service = "antifailure-test"
	const name = "empty"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	require.NoError(t, ring.Set(service, name, ""))
	got, err := ring.Get(service, name)
	require.NoError(t, err, "an empty stored value must not read as a failure")
	require.Equal(t, "", got)
}
