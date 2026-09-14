package local

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// The budget and the probe's message, without a daemon.
//
// These two are here rather than in the live file because neither needs a
// container and both are read on a path that only runs when something has
// already gone wrong. A wrong budget is how the bounded wait stops being
// bounded, and a mangled message is what the operator is left holding after
// three minutes of waiting.

func TestEmulatorReadyTimeout_DefaultsWhenNothingIsSet(t *testing.T) {
	t.Parallel()
	r := &Runtime{getenv: func(string) string { return "" }}
	d, err := r.emulatorReadyTimeout()
	require.NoError(t, err)
	require.Equal(t, defaultEmulatorReadyTimeout, d)
	require.Greater(t, d, time.Minute,
		"a budget under a minute is shorter than the slowest emulator measured on this "+
			"machine, so the default would refuse a healthy environment")
}

func TestEmulatorReadyTimeout_TakesTheVariable(t *testing.T) {
	t.Parallel()
	r := &Runtime{getenv: func(name string) string {
		if name == emulatorReadyTimeoutVar {
			return "45s"
		}
		return ""
	}}
	d, err := r.emulatorReadyTimeout()
	require.NoError(t, err)
	require.Equal(t, 45*time.Second, d,
		"the variable is what an operator on a slow machine has, and the tests that prove "+
			"the timeout is enforced would otherwise have to wait the default out")
}

func TestEmulatorReadyTimeout_RefusesAValueItCannotRead(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"five minutes", "0", "-1m", "300"} {
		r := &Runtime{getenv: func(name string) string {
			if name == emulatorReadyTimeoutVar {
				return raw
			}
			return ""
		}}
		_, err := r.emulatorReadyTimeout()
		require.Error(t, err,
			"%q was accepted as a duration, so the wait runs on a budget nobody asked for "+
				"and the person who set the variable concludes it does nothing", raw)
		require.Contains(t, err.Error(), raw,
			"the refusal does not quote what was set, so nobody can see the typo")
		require.Contains(t, err.Error(), emulatorReadyTimeoutVar,
			"the refusal does not name the variable that is wrong")
		require.ErrorIs(t, err, aferrors.Coded(aferrors.AFRUN040),
			"a malformed variable is a refusal before anything is waiting, not the timeout "+
				"itself, so it must not arrive as the emulator's own failure")
	}
}

func TestFirstLine_IsTheProbesMessageWithoutItsTerminalBytes(t *testing.T) {
	t.Parallel()
	// The probe runs on a terminal, so what comes back is CRLF terminated and
	// can carry more than one line when the dialer is verbose. AF-RUN-049 quotes
	// this inside its own sentence, and a raw carriage return there rewrites the
	// start of the operator's line with the end of it.
	require.Equal(t,
		"af-proxy dial: dial tcp 172.19.0.2:8080: connect: connection refused",
		firstLine("af-proxy dial: dial tcp 172.19.0.2:8080: connect: connection refused\r\n"))
	require.Equal(t, "first", firstLine("first\r\nsecond\r\n"))
	// Trailing space BEFORE the line break, which is the case the leading trim
	// cannot reach: it trims the ends of the whole output, and this space is in
	// the middle of it until the truncation puts it at the end.
	require.Equal(t, "first", firstLine("first \r\nsecond"))
	require.Equal(t, "", firstLine("\r\n \r\n"))
	require.Equal(t, "", firstLine(""))
	require.False(t, strings.ContainsAny(firstLine("a\rb\nc"), "\r\n"))
}
