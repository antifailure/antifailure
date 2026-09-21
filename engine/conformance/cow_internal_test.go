package conformance

import (
	"testing"
	"time"
)

// The resolution figure's own decision table.
//
// It is printed rather than enforced, so nothing else in the suite will notice
// if it silently starts answering the wrong question. The readings are the
// real ones from 2026-09-21, because a threshold nobody has pointed at real
// numbers is the thing this file exists to falsify.
func TestCopyOnWriteResolutionSaysWhenTheRunCouldNotSee(t *testing.T) {
	t.Parallel()
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }

	cases := []struct {
		name          string
		grown, jitter time.Duration
		want          float64
		couldSee      bool
	}{
		// The CI run that refused the docker provider. The only reading of the
		// four that resolved anything, and it is the one that failed.
		{"the quiet CI runner", ms(351), ms(231), 351.0 / 231.0, true},
		// The three taken on a loaded machine, whose numbers fell on a curve
		// smooth enough to be mistaken for a result.
		{"loaded, 2 GiB arm", ms(2168), ms(2869), 2168.0 / 2869.0, false},
		{"loaded, 512 MiB first", ms(2579), ms(6034), 2579.0 / 6034.0, false},
		{"loaded, 512 MiB second", ms(1652), ms(4136), 1652.0 / 4136.0, false},
		// Exactly at the line is not resolved: the growth equals the distance
		// the minimum could have moved, so it could be entirely that.
		{"exactly at the line", ms(500), ms(500), 1, true},
		// Two identical readings are a resolution nothing can be divided by,
		// not a perfect one.
		{"no jitter at all", ms(500), 0, 0, false},
		// A real reading from a provider that genuinely shares storage: the
		// large arm came back FASTER than the small one, which is noise big
		// enough to reorder the arms rather than a small copy. It is the
		// magnitude that decides whether the run saw anything.
		{"growth came out negative", -ms(108), ms(120), 108.0 / 120.0, false},
		// And a negative large enough to be real still resolves, because the
		// question this figure answers is whether the number beat the noise.
		{"a negative that beats the noise", -ms(3000), ms(100), 30, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := copyOnWriteResolution(c.grown, c.jitter)
			if diff := got - c.want; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("copyOnWriteResolution(%s, %s) = %v, want %v",
					c.grown, c.jitter, got, c.want)
			}
			if (got >= 1) != c.couldSee {
				t.Fatalf("resolution %v reads as couldSee=%v, want %v",
					got, got >= 1, c.couldSee)
			}
		})
	}
}

// The jitter has to take the WORSE arm, because the verdict is a difference of
// two minima and a difference is no better than its worse half. Taking the
// small arm alone is what the allowance already does, and it is the specific
// thing this figure exists to stop a reader assuming.
func TestCopyOnWriteJitterTakesTheWorseArm(t *testing.T) {
	t.Parallel()
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }

	// The CI readings. The LARGE arm is the noisier of the two, 231ms against
	// 143ms, and it is the one the allowance charges nothing for.
	small := []time.Duration{ms(818), ms(675), ms(1424)}
	large := []time.Duration{ms(1257), ms(1538), ms(1026)}

	if got, want := minimaGap(small), ms(143); got != want {
		t.Fatalf("small arm gap = %s, want %s", got, want)
	}
	if got, want := minimaGap(large), ms(231); got != want {
		t.Fatalf("large arm gap = %s, want %s", got, want)
	}
	if got, want := copyOnWriteJitter(small, large), ms(231); got != want {
		t.Fatalf("jitter = %s, want %s: the larger arm has to govern", got, want)
	}
	// Order must not matter, or the figure would depend on which arm was
	// passed first rather than on the readings.
	if got, want := copyOnWriteJitter(large, small), ms(231); got != want {
		t.Fatalf("jitter with the arms swapped = %s, want %s", got, want)
	}
	// The allowance still reads the small arm alone, which is the existing
	// behaviour and is not changed here. Stated as an assertion so that a
	// later edit cannot quietly make these two the same quantity.
	if got, want := copyOnWriteAllowance(small), ms(286); got != want {
		t.Fatalf("allowance = %s, want %s", got, want)
	}
}

// The allowance had no test at all before this file. Its floor is the half
// that decides every quiet run, so it is the half worth pinning.
func TestCopyOnWriteAllowanceFloorHolds(t *testing.T) {
	t.Parallel()
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }

	// A steady arm: twice a 10ms gap is 20ms, far under the floor, so the
	// floor governs.
	steady := []time.Duration{ms(500), ms(510), ms(505)}
	if got, want := copyOnWriteAllowance(steady), copyOnWriteNoiseFloor; got != want {
		t.Fatalf("allowance on a steady arm = %s, want the %s floor", got, want)
	}
	// A jittery arm: twice a 400ms gap is 800ms, which clears the floor and
	// governs instead.
	jittery := []time.Duration{ms(500), ms(900), ms(2000)}
	if got, want := copyOnWriteAllowance(jittery), ms(800); got != want {
		t.Fatalf("allowance on a jittery arm = %s, want %s", got, want)
	}
}
