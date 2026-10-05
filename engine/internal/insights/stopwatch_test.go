package insights

import (
	"testing"
	"time"
)

// A statement faster than one tick of a coarse clock read as taking no time,
// on Windows, where Go's clock advances in steps of 0.3 to 0.5 ms. This is
// work measured at about 0.16 ms on a windows-latest runner, which read as 0
// there through time.Since every time. The stopwatch must see it on every
// platform, because a zero is how a statement that was never timed looks.
func TestTheStopwatchSeesWorkShorterThanTheSystemTimer(t *testing.T) {
	for i := 0; i < 20; i++ {
		clock := startStopwatch()
		x := 0
		for j := 0; j < 200_000; j++ {
			x += j
		}
		if ms := clock.elapsedMS(); ms <= 0 {
			t.Fatalf("attempt %d: %d sums timed at %v ms, so the stopwatch cannot tell a short statement from an untimed one", i, x, ms)
		}
	}
}

// The unit is milliseconds: ten of them read as ten, not as ten thousand or
// a hundredth, whichever clock is underneath.
func TestTheStopwatchReadsMilliseconds(t *testing.T) {
	clock := startStopwatch()
	sleepFor(t, 20)
	ms := clock.elapsedMS()
	if ms < 15 || ms > 2000 {
		t.Fatalf("a 20 ms sleep timed at %v ms", ms)
	}
}

func sleepFor(t *testing.T, ms int) {
	t.Helper()
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

// A long rehearsal must not overflow into a negative duration. The direct
// product of nanosecond ticks and a million passes the largest int64 at about
// two and a half hours; a day and the counter's 10 MHz are both checked, and
// the remainder of a second keeps its microseconds.
func TestTheStopwatchConvertsLongDurationsWithoutOverflow(t *testing.T) {
	day := int64(24 * time.Hour)
	if got := ticksToMS(day, int64(time.Second)); got != 86_400_000 {
		t.Fatalf("a day of nanosecond ticks read as %v ms", got)
	}
	if got := ticksToMS(864_000_000_000, 10_000_000); got != 86_400_000 {
		t.Fatalf("a day of 10 MHz ticks read as %v ms", got)
	}
	if got := ticksToMS(int64(3*time.Hour+1500*time.Microsecond), int64(time.Second)); got != 10_800_001.5 {
		t.Fatalf("three hours and 1.5 ms read as %v ms", got)
	}
}
