//go:build darwin

package secrets_test

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// An entry name holding a line break is refused before security runs.
//
// The write is one line on security's stdin, so a line break in a name ends the
// command early: the first piece runs as a write under a name nobody asked for,
// and the rest runs as a second command. Refused with ErrKeyringUnavailable, so
// the caller falls through to its next store rather than failing, and nothing
// is written under either half of the name.
func TestAnEntryNameWithALineBreakIsRefusedBeforeSecurityRuns(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	ring := secrets.NewSystemKeyring()
	const service = "antifailure-test-names"
	t.Cleanup(func() {
		for _, name := range []string{"first\nsecond", "first", "second"} {
			_ = ring.Delete(service, name)
		}
	})

	err := ring.Set(service, "first\nsecond", "value")
	require.ErrorIs(t, err, secrets.ErrKeyringUnavailable,
		"a name that would split the command line reached security")

	_, getErr := ring.Get(service, "first")
	require.ErrorIs(t, getErr, secrets.ErrNotFound, "the first half of the name was written")
}
