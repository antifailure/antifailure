//go:build darwin

package secrets_test

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The longest value the keychain command takes is stored whole, and one byte
// more is refused before security ever sees it.
//
// security reads an interactive command into a 4096 byte buffer, and a longer
// line is not refused by it. The line is cut: the first 4095 bytes run as a
// command of their own and store a truncated value, the remainder runs as a
// second command and fails, and the process exits 1 having already written the
// truncation. So the limit has to be enforced here, and the number is proved
// against the real command on both sides rather than read out of Apple's
// source and trusted.
//
// The names are chosen so the command at the limit is exactly 4096 bytes with
// its newline, which puts the refused write one hex pair past the buffer rather
// than somewhere comfortably beyond it.
func TestTheLongestValueTheKeychainTakesIsStoredWholeAndOneMoreIsRefused(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	ring := secrets.NewSystemKeyring()
	const service = "antifailure-test-limit"
	const name = "limits"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	limit := secrets.KeychainValueLimit(service, name)
	// The credential af login stores is a few hundred bytes of JSON, and a model
	// key is under two hundred. A limit that crept below this would send both of
	// them to the fallback store on every Mac.
	require.GreaterOrEqual(t, limit, 1500,
		"the keychain would refuse values the product stores there every day")

	longest := strings.Repeat("k", limit)
	require.NoError(t, ring.Set(service, name, longest))
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, len(longest), len(got), "the longest accepted value was stored truncated")
	require.Equal(t, longest, got)

	err = ring.Set(service, name, longest+"k")
	require.ErrorIs(t, err, secrets.ErrKeyringUnavailable,
		"a value too long for the command has to fall through to the next store, not reach security")

	got, err = ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, longest, got, "a refused write still changed what the keychain holds")
}
