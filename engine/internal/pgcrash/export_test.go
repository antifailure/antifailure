package pgcrash

import "context"

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
