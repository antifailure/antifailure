package pgcrash

// JudgeForTest drives the judgement from values, the way the rest of this
// package's tests drive its parsers, so a branch that only a broken cluster
// reaches can be run without breaking one.
func JudgeForTest(r *Result, beforeErr, afterErr error) { r.judge(Options{}, beforeErr, afterErr, 0) }
