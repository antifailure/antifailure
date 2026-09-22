package pgcrash_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/pgcrash"
)

// attempts builds probe samples every 100ms from a base time, with the
// outcome and duration each one had.
func attempts(base time.Time, n int, outcome func(i int) (bool, time.Duration)) []pgcrash.ProbeSample {
	out := make([]pgcrash.ProbeSample, 0, n)
	for i := 0; i < n; i++ {
		at := base.Add(time.Duration(i) * 100 * time.Millisecond)
		ok, took := outcome(i)
		out = append(out, pgcrash.ProbeSample{At: at, Done: at.Add(took), OK: ok})
	}
	return out
}

// A crash: the database refuses every attempt, quickly, from 1.0s to 1.9s
// and answers from 2.0s. The outage runs from the first refusal to the first
// answer, and nothing the proof did in between is part of it.
func TestAvailability_ACrashIsTheSpanTheProbeWasRefused(t *testing.T) {
	base := time.Unix(1000, 0)
	a := pgcrash.AvailabilityOfForTest(attempts(base, 40, func(i int) (bool, time.Duration) {
		return i < 10 || i >= 20, 5 * time.Millisecond
	}), 100*time.Millisecond)
	require.True(t, a.Unreachable)
	require.True(t, a.Recovered)
	require.Equal(t, 1005*time.Millisecond, a.For, "from the refusal at 1.0s to the answer that finished at 2.005s")
	require.Equal(t, 100*time.Millisecond, a.Interval)
}

// A freeze: attempts started from 1.0s to 2.0s time out after a second, and
// those started from 2.1s hang until the thaw at 3.0s and are answered then.
// The outage ends at the thaw, which is when an answer FINISHED. Reading when
// that attempt started would end it 0.9s before the database answered.
func TestAvailability_AFreezeEndsWhenTheAnswerArrivesNotWhenItWasAsked(t *testing.T) {
	base := time.Unix(1000, 0)
	thaw := base.Add(3 * time.Second)
	a := pgcrash.AvailabilityOfForTest(attempts(base, 40, func(i int) (bool, time.Duration) {
		at := base.Add(time.Duration(i) * 100 * time.Millisecond)
		switch {
		case i < 10:
			return true, 5 * time.Millisecond
		case i <= 20:
			return false, time.Second
		case at.Before(thaw):
			return true, thaw.Sub(at)
		}
		return true, 5 * time.Millisecond
	}), 100*time.Millisecond)
	require.True(t, a.Unreachable)
	require.Equal(t, 2*time.Second, a.For, "the freeze ran from 1.0s to the thaw at 3.0s")
}

// Every attempt answered: there is no outage, and that is said, not a zero.
func TestAvailability_ADatabaseThatAlwaysAnsweredWasNeverUnreachable(t *testing.T) {
	a := pgcrash.AvailabilityOfForTest(attempts(time.Unix(1000, 0), 30, func(int) (bool, time.Duration) {
		return true, 5 * time.Millisecond
	}), 100*time.Millisecond)
	require.False(t, a.Unreachable)
	require.Zero(t, a.For)
}

// A database still refusing when the probe stopped has an outage with no
// end, and the number is a floor.
func TestAvailability_AnOutageWithNoEndIsAFloor(t *testing.T) {
	a := pgcrash.AvailabilityOfForTest(attempts(time.Unix(1000, 0), 30, func(i int) (bool, time.Duration) {
		return i < 10, 5 * time.Millisecond
	}), 100*time.Millisecond)
	require.True(t, a.Unreachable)
	require.False(t, a.Recovered, "an outage the probe never saw end was called recovered")
	require.Equal(t, 1900*time.Millisecond, a.For)
}

// The probe itself, against an attempt that fails for a known half second.
// It runs on its own clock, one attempt every 20ms, so it measures the window
// to within that and does not wait on anything else.
func TestProbe_MeasuresAKnownWindowToItsResolution(t *testing.T) {
	start := time.Now()
	down, up := start.Add(300*time.Millisecond), start.Add(800*time.Millisecond)
	a := pgcrash.ProbeForTest(func(context.Context) error {
		if now := time.Now(); !now.Before(down) && now.Before(up) {
			return errors.New("refused")
		}
		return nil
	}, 20*time.Millisecond, 200*time.Millisecond, 1200*time.Millisecond)
	require.True(t, a.Unreachable)
	require.True(t, a.Recovered)
	require.InDelta(t, float64(500*time.Millisecond), float64(a.For), float64(40*time.Millisecond),
		"a window of 500ms was measured as %s", a.For)
}
