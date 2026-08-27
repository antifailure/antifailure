//go:build linux

package secrets_test

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Against a real Secret Service, not a fake.
//
// A fake would prove this code agrees with our idea of secret-tool, and what
// matters is that it agrees with secret-tool talking to a real keyring daemon
// over a real session bus. Every question this file asks was a guess in the
// implementation and each guess is a real failure if it is wrong: whether a
// miss is silent on standard error, whether a lookup adds a trailing newline
// the way the macOS security command does, whether a second store replaces or
// adds a duplicate, and whether clearing something absent is an error.
//
// See engine/internal/secrets/testdata/keyring-linux for the container that
// runs it: dbus-run-session plus a gnome-keyring unlocked with a known
// password, which is a real daemon and not a stand-in for one.
func requireKeyring(t *testing.T) secrets.Keyring {
	t.Helper()
	if _, err := exec.LookPath("secret-tool"); err != nil {
		t.Skip("skipped: secret-tool is not on the path")
	}
	ring := secrets.NewSystemKeyring()
	if ring == nil {
		t.Skip("skipped: no keyring on this machine")
	}
	// A machine can have secret-tool and no running daemon, which is the
	// ordinary state of a CI runner. That is a skip and not a failure: the
	// chain's whole design is that an unavailable source is named and stepped
	// over.
	if _, err := ring.Get("antifailure-test", "probe"); err != nil && err != secrets.ErrNotFound {
		t.Skipf("skipped: the Secret Service is not answering: %v", err)
	}
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

	// A second write has to replace rather than add a second item with the same
	// attributes, or 'af secret set' run twice leaves two entries and a lookup
	// returns whichever the daemon reaches first.
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
	// carries the characters most likely to be mangled by a shell.
	const value = `postgres://u:p@ss w0rd!"'$&@host:5432/db?opt=a b`
	require.NoError(t, ring.Set(service, name, value))
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, value, got, "the value did not survive the round trip")
}

// The test that decides whether the implementation may trim.
//
// The macOS security command always appends a newline to what it prints, so
// that implementation strips one. If secret-tool did the same and this one did
// not trim, every value read on Linux would carry a stray newline; if it does
// not and this one trimmed anyway, a private key stored verbatim out of a file
// would come back one byte short. Both are silent corruptions that only appear
// when the value is used, so the behaviour is pinned here rather than reasoned
// about.
func TestATrailingNewlineIsNotEatenOrInvented(t *testing.T) {
	ring := requireKeyring(t)
	const service = "antifailure-test"
	t.Cleanup(func() {
		_ = ring.Delete(service, "with-newline")
		_ = ring.Delete(service, "without-newline")
	})

	require.NoError(t, ring.Set(service, "with-newline", "value\n"))
	got, err := ring.Get(service, "with-newline")
	require.NoError(t, err)
	require.Equal(t, "value\n", got, "a trailing newline in the stored value was eaten")

	require.NoError(t, ring.Set(service, "without-newline", "value"))
	got, err = ring.Get(service, "without-newline")
	require.NoError(t, err)
	require.Equal(t, "value", got, "a trailing newline was invented by the reader")
}

// A multi-line value, because a PEM private key is one and it is the value most
// likely to be stored in a keyring rather than a .env file.
func TestAMultiLineValueSurvives(t *testing.T) {
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
}

// The distinction the whole chain rests on, proved rather than assumed.
//
// secret-tool exits 1 for a name that is not stored and exits 1 for a session
// bus it cannot reach, so the exit code cannot tell them apart and the
// implementation reads standard error instead. Getting that backwards is a
// silent, specific failure: a machine whose keyring is merely unreachable would
// report every variable as simply not set, the chain would fall through to
// nothing, and AF-SEC-001 would list the keyring as a source that was asked and
// had no answer. The user would then go looking in the four places the value
// is not.
//
// Pointed at a socket that does not exist rather than at no bus at all, because
// unsetting the address makes secret-tool autolaunch one and succeed.
func TestAnUnreachableBusIsUnavailableRatherThanAMiss(t *testing.T) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		t.Skip("skipped: secret-tool is not on the path")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/antifailure-no-such-bus")

	ring := secrets.NewSystemKeyring()
	require.NotNil(t, ring, "secret-tool is present, so there is a ring to try")

	_, err := ring.Get("antifailure-test", "anything")
	require.Error(t, err)
	require.NotErrorIs(t, err, secrets.ErrNotFound,
		"an unreachable bus was reported as a missing entry, which sends the user to the wrong place")
	require.ErrorIs(t, err, secrets.ErrKeyringUnavailable)
	require.NotEmpty(t, err.Error(), "the reason reaches AF-SEC-001 and must not be empty")

	// And the chain reports it, with the reason, rather than skipping it
	// silently. This is the sentence somebody actually reads.
	source := secrets.NewKeyringSource(ring, "antifailure-test")
	ok, why := source.Available(t.Context())
	require.False(t, ok)
	require.NotEmpty(t, why)
	t.Logf("the chain reports: the system keyring (%s)", why)
}
