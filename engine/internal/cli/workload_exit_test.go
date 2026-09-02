package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// Internal, because the mapping is unexported and it is the whole of what a
// hosted job learns from the process.
//
// The row that earned this test is blocked and unverified. af test exits 0 on
// unverified and does not count blocked against a run, and that single fact is
// how an entire nightly corpus in this repository went green having never once
// reached an agent. A workload is a job somebody gates on, so a run that
// measured nothing must not be indistinguishable from a run that found
// nothing: it gets its own code, and the two are asserted apart here rather
// than described apart in a comment.
func TestTheWorkloadExitCodeSaysWhichKindOfNotPassingItWas(t *testing.T) {
	cases := []struct {
		name   string
		result *workload.Result
		exit   int
		code   string
	}{
		{
			name:   "a pass is a clean exit",
			result: &workload.Result{State: workload.StateSucceeded, Verdict: workload.VerdictPass},
			exit:   0,
		},
		{
			// Matching af test, which counts a flaky workflow as not failed.
			// A different answer here would make one product disagree with
			// itself about the same word.
			name:   "flaky is a clean exit, as it is for af test",
			result: &workload.Result{State: workload.StateSucceeded, Verdict: workload.VerdictFlaky},
			exit:   0,
		},
		{
			name: "a failure exits as a test failure",
			result: &workload.Result{
				State: workload.StateSucceeded, Verdict: workload.VerdictFail,
				Detail: "the error rate breached its threshold",
			},
			exit: int(aferrors.ExitTestFailure),
			code: "AF-WLD-012",
		},
		{
			name:   "a run that measured nothing is a verification failure, not a pass",
			result: &workload.Result{State: workload.StateSucceeded, Verdict: workload.VerdictUnverified},
			exit:   int(aferrors.ExitVerification),
			code:   "AF-WLD-013",
		},
		{
			name:   "a run that could not start is the same",
			result: &workload.Result{State: workload.StateSucceeded, Verdict: workload.VerdictBlocked},
			exit:   int(aferrors.ExitVerification),
			code:   "AF-WLD-013",
		},
		{
			name:   "a cancelled run is an interruption",
			result: &workload.Result{State: workload.StateCancelled, Verdict: workload.VerdictBlocked},
			exit:   int(aferrors.ExitInterruptedClean),
			code:   "AF-WLD-014",
		},
		{
			name:   "a run past its deadline is the same interruption",
			result: &workload.Result{State: workload.StateTimedOut, Verdict: workload.VerdictBlocked},
			exit:   int(aferrors.ExitInterruptedClean),
			code:   "AF-WLD-014",
		},
		{
			name: "a refused knob is a usage error",
			result: &workload.Result{
				State: workload.StateFailed, Verdict: workload.VerdictBlocked,
				FailureCode: string(aferrors.AFWLD002),
				Refusals:    []workload.Refusal{{Knob: "concurrency"}},
			},
			exit: int(aferrors.ExitUsage),
			code: "AF-WLD-002",
		},
		{
			// The loudest of the lot, because an environment still standing
			// costs money for as long as nobody looks. It outranks the
			// verdict: a run that passed and left containers behind is not a
			// clean run.
			name: "resources still standing outrank a passing verdict",
			result: &workload.Result{
				State: workload.StateSucceeded, Verdict: workload.VerdictPass,
				Teardown: &workload.TeardownResult{
					Removed: 3,
					Pending: []workload.PendingResource{{Kind: "container", ID: "abc"}},
				},
			},
			exit: int(aferrors.ExitInterruptedDirty),
			code: "AF-RUN-030",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both halves, because they are two claims. The process code is
			// what a job gates on; the coded error is what the person reading
			// the terminal sees, and a render that named a different reason
			// from the one the process exited with would be worse than
			// neither.
			err := workloadExit(tc.result)
			coded := codedOutcome(tc.result)
			if tc.exit == 0 {
				require.NoError(t, err)
				require.NoError(t, coded)
				return
			}
			require.Error(t, err)
			require.Error(t, coded)
			require.Equal(t, tc.exit, exitCodeOfCommand(err))
			require.Contains(t, coded.Error(), tc.code)
			require.Equal(t, tc.exit, int(aferrors.ExitCodeOf(coded)),
				"the code the terminal shows and the code the process exits with are the same answer")
		})
	}
}

// Every exit this command can produce is one the registry already names. A new
// number invented here would mean nothing to a script reading it.
func TestTheWorkloadExitCodesAreAllInTheRegistry(t *testing.T) {
	known := map[int]bool{
		int(aferrors.ExitSuccess): true, int(aferrors.ExitUsage): true,
		int(aferrors.ExitVerification): true, int(aferrors.ExitTestFailure): true,
		int(aferrors.ExitInterruptedClean): true, int(aferrors.ExitInterruptedDirty): true,
	}
	for _, verdict := range []string{
		workload.VerdictPass, workload.VerdictFail, workload.VerdictFlaky,
		workload.VerdictBlocked, workload.VerdictUnverified,
	} {
		for _, state := range []workload.State{
			workload.StateSucceeded, workload.StateFailed,
			workload.StateCancelled, workload.StateTimedOut,
		} {
			err := workloadExit(&workload.Result{State: state, Verdict: verdict})
			require.Truef(t, known[exitCodeOfCommand(err)],
				"%s/%s exits %d, which the registry does not name",
				state, verdict, exitCodeOfCommand(err))
		}
	}
}

// exitCodeOfCommand is what Execute would return for this error.
//
// Written out rather than calling aferrors.ExitCodeOf, because silent()
// deliberately keeps only the code and drops the message, so ExitCodeOf on its
// own answers 1 for every one of these. This is the branch root.go takes.
func exitCodeOfCommand(err error) int {
	if err == nil {
		return int(aferrors.ExitSuccess)
	}
	var quiet *silentError
	if aferrors.As(err, &quiet) {
		return int(quiet.ExitCode())
	}
	return int(aferrors.ExitCodeOf(err))
}

// The exit code a hosted exploration produces, which is the half a person feels.
//
// docs/concepts/exploration promises that an exploration cannot fail your
// build. That promise used to hold for `af explore` and break for
// `af workload run --kind exploration`, because a goal that was not reached
// rolled up to `unverified` and this table maps unverified to AFWLD013, a
// non-zero exit. So the hosted path, which is the one the console drives,
// failed the job on a page that had no control to press.
//
// Both halves are asserted here because they are one decision: an exploration
// that looked exits zero, and an exploration that never ran does not.
func TestAHostedExplorationCannotFailTheBuildUnlessItNeverRan(t *testing.T) {
	looked := &workload.Result{
		Kind: workload.Exploration, State: workload.StateSucceeded,
		Verdict: workload.VerdictPass,
		Detail:  "correct-a-customer-name did not reach its goal",
	}
	_, _, clean := workloadOutcome(looked)
	require.True(t, clean,
		"an exploration that explored and found a wall failed the build, which the "+
			"documentation promises never happens")
	require.NoError(t, codedOutcome(looked))

	neverRan := &workload.Result{
		Kind: workload.Exploration, State: workload.StateSucceeded,
		Verdict: workload.VerdictBlocked,
		Detail:  "no goal was explored, so nothing was found",
	}
	_, _, clean = workloadOutcome(neverRan)
	require.False(t, clean,
		"nobody looked and the job passed, which is the green over nothing this "+
			"table exists to prevent")
}
