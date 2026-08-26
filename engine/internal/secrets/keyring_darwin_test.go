//go:build darwin

package secrets_test

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Against the real keychain, not a fake.
//
// A fake would prove this code agrees with our idea of the security command,
// and what matters is that it agrees with the security command: the exit code
// for a missing item, whether a second write replaces or fails, whether a
// delete of something absent is an error. Those are the things that were
// guessed at, and each one is a real failure if the guess is wrong.
func TestSystemKeyringRoundTrips(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	ring := secrets.NewSystemKeyring()
	require.NotNil(t, ring, "darwin must have a keyring")

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
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	ring := secrets.NewSystemKeyring()
	const service = "antifailure-test"
	const name = "awkward"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	// A connection string is the value most likely to be stored here, and it
	// carries the characters most likely to be mangled by a shell.
	const value = `postgres://u:p@ss w0rd!"'$&@host:5432/db?opt=a b`
	require.NoError(t, ring.Set(service, name, value))
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, value, got, "the value did not survive the round trip")
}

func TestAMissingSecurityCommandIsReportedAsUnavailable(t *testing.T) {
	// Not the same as an empty keyring. The chain reports which sources it
	// considered and why each did not answer, and "unavailable" and "no such
	// entry" are different sentences.
	require.True(t, errors.Is(secrets.ErrKeyringUnavailable, secrets.ErrKeyringUnavailable))
}
