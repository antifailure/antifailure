package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// A request the policy deliberately slowed has to say so.
//
// The sidecar recorded the wait from the beginning and nothing showed it: the
// engine's Decision had no waited_ms field to decode into, and the function
// that renders a limit in words had no caller outside its own test. So a
// request held for half a second on purpose looked in af net log exactly like
// an application that is slow, which sends somebody to profile their own code.
func TestAShapedRequestSaysWhatHeldItAndForHowLong(t *testing.T) {
	got := outcomeOf(local.Decision{
		Status: 200, Bytes: 0, WaitedMs: 420,
		Limit: "10 a second, bursting to 10",
	})
	require.Equal(t, "200, held 420ms by 10 a second, bursting to 10", got)
}

func TestAnUnshapedRequestReadsExactlyAsItDidBefore(t *testing.T) {
	require.Equal(t, "200", outcomeOf(local.Decision{Status: 200}))
	require.Equal(t, "", outcomeOf(local.Decision{}))
	require.Equal(t, "boom", outcomeOf(local.Decision{Error: "boom"}))
}

// The wait is worth reporting even when the limit could not be rendered, and
// a refused request that waited first still shows both.
func TestTheWaitIsShownWithoutALimitAndWithoutAStatus(t *testing.T) {
	require.Equal(t, "held 420ms", outcomeOf(local.Decision{WaitedMs: 420}))
	require.Equal(t, "held 90ms by 1 a second, bursting to 1",
		outcomeOf(local.Decision{WaitedMs: 90, Limit: "1 a second, bursting to 1"}))
}
