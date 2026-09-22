package pgcrash

import (
	"context"
	"time"
)

// JudgeForTest drives the judgement from values, the way the rest of this
// package's tests drive its parsers, so a branch that only a broken cluster
// reaches can be run without breaking one.
func JudgeForTest(r *Result, beforeErr, afterErr error) { r.judge(Options{}, beforeErr, afterErr, 0) }

// CheckRelationsForTest reads a database's writers' table back the way the
// proof does after recovery, without a crash in front of it, so a page damaged
// on purpose can be put where the read back has to meet it.
func CheckRelationsForTest(ctx context.Context, url string) Relations {
	return checkRelations(ctx, Options{URL: url})
}

// JudgeCrashForTest is JudgeForTest for a fault that was expected to crash
// the database, which is the only judgement the replay checks run under.
func JudgeCrashForTest(r *Result) { r.judge(Options{ExpectCrash: true}, nil, nil, 0) }

// ReadControlForTest reads the control file the way the proof does, for a
// test that crashes a database by hand rather than through Verify.
func ReadControlForTest(ctx context.Context, r Runner, dataDir string) (Control, error) {
	return readControl(ctx, Options{Runner: r, DataDir: dataDir})
}

// ProbeForTest runs the availability probe against a fake attempt for a
// span, the way Verify runs it beside a fault, and says what it saw.
func ProbeForTest(attempt func(context.Context) error, interval, timeout, run time.Duration) Availability {
	p := startProbe(context.Background(), attempt, interval, timeout)
	time.Sleep(run)
	return p.stop()
}

// ProbeSample is one attempt, for driving availabilityOf from values.
type ProbeSample struct {
	At, Done time.Time
	OK       bool
}

// AvailabilityOfForTest reads an outage out of attempts given as values.
func AvailabilityOfForTest(samples []ProbeSample, interval time.Duration) Availability {
	in := make([]probeSample, 0, len(samples))
	for _, s := range samples {
		in = append(in, probeSample{at: s.At, done: s.Done, ok: s.OK})
	}
	return availabilityOf(in, interval)
}
