package insights

// stopwatch times one statement of a rehearsal, to the microsecond on every
// platform.
//
// time.Since is that everywhere but Windows. There Go's clock advances in steps
// of the system timer, measured on a windows-latest runner at 0.3 to 0.5 ms:
// 199999 of 200000 back to back readings were identical, and a loop that took
// 1.6 ms read as 0 at a tenth of the size. A statement faster than one step,
// which is most DDL on an empty table, was reported as taking no time at all,
// indistinguishable from a statement that was listed and never timed.
// TestRehearse_TimesEveryStatementSeparately caught it on Windows, red on one
// run and green on the next, as whether a nullable ADD COLUMN straddled a tick.
// On Windows the stopwatch reads the performance counter instead, which
// QueryPerformanceFrequency reports at 10 MHz on current machines.
type stopwatch struct {
	start int64
}

// startStopwatch starts one measurement.
func startStopwatch() stopwatch {
	return stopwatch{start: ticks()}
}

// elapsedMS is the time since the stopwatch started, in milliseconds to the
// microsecond, the unit every duration in a rehearsal report is written in.
func (s stopwatch) elapsedMS() float64 {
	return ticksToMS(ticks()-s.start, tickHz())
}

// ticksToMS converts a count of ticks at hz to milliseconds, to the
// microsecond.
//
// Whole seconds and the remainder are converted separately, because the
// direct product overflows: at nanosecond ticks, elapsed times a million
// passes the largest int64 after two and a half hours, and a rehearsal that
// long would have reported a negative duration. Split, the remainder is under
// one second's worth of ticks, so its product stays far inside the range at
// any frequency a clock reports.
func ticksToMS(elapsed, hz int64) float64 {
	seconds, rest := elapsed/hz, elapsed%hz
	micros := seconds*1_000_000 + rest*1_000_000/hz
	return float64(micros) / 1000
}
