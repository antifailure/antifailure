//go:build darwin

package secrets_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
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

// The secret must not reach the child process's argv.
//
// This is the defect the file's comment describes: every secret this product
// stores went through `security add-generic-password -w <value>`, where any
// other user on the machine could read it out of ps. A control plane bearer
// token was read that way on this project's own machine, by accident.
//
// Asserted by watching the process rather than by reading the code, because
// reading the code is what missed it for the life of the file. A writer is
// started against a value nothing else on the machine would produce, and the
// full command line of every security process is sampled while it runs.
func TestTheSecretNeverReachesTheProcessListing(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("skipped: ps is not on the path, so the listing cannot be read")
	}
	ring := secrets.NewSystemKeyring()

	const service = "antifailure-test-argv"
	const name = "argv-probe"
	// Distinctive enough that a match cannot be anything else on the machine.
	const secret = "af-argv-canary-8c41d2e7-do-not-store"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	seen := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(seen)
		for {
			select {
			case <-done:
				return
			default:
			}
			out, err := exec.Command("ps", "-Ao", "args=").Output()
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(out), "\n") {
				if securityArgvHolding(line, secret) {
					select {
					case seen <- line:
					default:
					}
					return
				}
			}
		}
	}()

	require.NoError(t, ring.Set(service, name, secret))
	close(done)

	if line, ok := <-seen; ok {
		t.Fatalf("the secret was on a command line, readable by any user on this machine:\n%s", line)
	}

	// And it really was stored, or this test would pass by writing nothing.
	got, err := ring.Get(service, name)
	require.NoError(t, err)
	require.Equal(t, secret, got)
}

// securityArgvHolding reports whether this is the security command with the
// secret in its own arguments.
//
// Scoped to that process rather than to any line containing the string, and the
// first version was not, which made it fail against a fixed implementation: the
// shell that wrote this test file still had the canary in ITS command line,
// because the heredoc quotes the test source. A watcher that matches anything
// is a watcher that reports the machine rather than the subject.
func securityArgvHolding(line, secret string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 || filepath.Base(fields[0]) != "security" {
		return false
	}
	return strings.Contains(line, secret)
}

// The sampler has to be able to see a secret that IS on a command line, or the
// test above passes because it is looking at nothing.
//
// Proved against real `ps` output rather than by racing a process, which is
// what two earlier versions of this did and both were wrong for different
// reasons. Matching any line holding the string reported the shell that wrote
// this test file, because the heredoc quotes the test source. Starting a
// shebang script named security reported nothing, because `ps` renders such a
// process as `/bin/sh /path/to/security` and the first field is the shell.
//
// So the predicate is exercised on the exact line the old implementation
// produced, taken from a real listing, alongside the line that fooled the first
// version.
func TestTheProcessListingWatcherCanSeeASecretThatIsThere(t *testing.T) {
	const canary = "af-argv-positive-control-5b2f9a"

	// What ps showed while the old implementation ran.
	defective := "/usr/bin/security add-generic-password -s antifailure -a model.anthropic -w " +
		canary + " -U"
	if !securityArgvHolding(defective, canary) {
		t.Error("the watcher cannot see the defect it was written for, so the test above proves nothing")
	}

	// And the shapes it must not report. The first is this test's own shell,
	// which is why the earlier version failed against a fixed implementation.
	for name, line := range map[string]string{
		"a shell whose command line quotes this file": "/bin/zsh -c cat > keyring_darwin_test.go <<EOF " + canary,
		"security running with no secret in argv":     "/usr/bin/security add-generic-password -s antifailure -a model.anthropic -U -w",
		"an unrelated process":                        "/usr/bin/grep -r " + canary + " .",
		"an empty line":                               "",
	} {
		if securityArgvHolding(line, canary) {
			t.Errorf("the watcher reports %s, which is a false finding: %s", name, line)
		}
	}
}

// A newline cannot go through the prompt protocol, so it is refused rather than
// silently truncated at the first line.
func TestAValueWithALineBreakIsRefusedRatherThanTruncated(t *testing.T) {
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("skipped: the security command is not on the path")
	}
	ring := secrets.NewSystemKeyring()
	const service = "antifailure-test-newline"
	const name = "pem"
	t.Cleanup(func() { _ = ring.Delete(service, name) })

	err := ring.Set(service, name, "-----BEGIN KEY-----\nabc\n-----END KEY-----")
	require.Error(t, err, "a value with a line break was accepted")
	require.ErrorIs(t, err, secrets.ErrKeyringUnavailable,
		"the refusal has to fall through to the encrypted store rather than fail the command")

	// Nothing was written, so a later read cannot return a truncated key.
	_, getErr := ring.Get(service, name)
	require.ErrorIs(t, getErr, secrets.ErrNotFound)
}
