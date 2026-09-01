package cli

// Internal, because the release gate is unexported: the functions that turn a
// measurement into a finding are the part of af ci most worth testing and the
// part hardest to reach through a command that wants Docker, a Postgres and a
// browser. Testing them here is what makes "a lock over the threshold fails
// the check" a property with a test rather than a sentence in a comment.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/report"
)

func defaultGate() report.Policy { return report.Configure(nil) }

func lockRun(heldMS float64, blocking bool) insights.Full {
	return insights.Full{Rehearsal: &insights.Rehearsal{
		Tool: "sql", Applier: "sql", TotalMS: heldMS,
		Pending: []insights.Migration{{Name: "0003_add_index.sql", Version: "0003"}},
		Locks: []insights.LockHold{{
			Table: "orders", Mode: "AccessExclusiveLock", HeldMS: heldMS, Blocking: blocking,
		}},
	}}
}

func TestGate_ALockOverTheFailingThresholdReachesTheReport(t *testing.T) {
	t.Parallel()
	// The product's headline promise: catch exclusive locks before they take
	// checkout down. Before af ci called insights, no lock finding reached a
	// pull request at all.
	findings, migration := migrationFindings(lockRun(4200, true), defaultGate())
	require.Len(t, findings, 1)
	require.Equal(t, ruleMigrationLock, findings[0].Rule)
	require.Equal(t, report.LevelFail, findings[0].Level)
	require.Contains(t, findings[0].Title, "AccessExclusiveLock on orders")
	require.Contains(t, findings[0].Title, "4.2s")
	require.Contains(t, findings[0].Detail, "Another session was seen waiting on it")

	r := report.Run{Findings: findings, Migration: migration,
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}}}
	require.Equal(t, report.VerdictFail, r.Verdict())
	out := r.Markdown()
	require.Contains(t, out, "AccessExclusiveLock on orders")
	require.Contains(t, out, "`migration_lock`")
	// The evidence as well as the answer.
	require.Contains(t, out, "| `orders` | AccessExclusiveLock |")
}

func TestGate_ALockBetweenTheThresholdsWarnsAndDoesNotFail(t *testing.T) {
	t.Parallel()
	findings, _ := migrationFindings(lockRun(900, false), defaultGate())
	require.Len(t, findings, 1)
	require.Equal(t, report.LevelWarn, findings[0].Level)

	r := report.Run{Findings: findings,
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}}}
	require.Equal(t, report.VerdictWarn, r.Verdict())
	require.NoError(t, ciExit(r), "a warning must not fail the build")
}

func TestGate_ALockUnderBothThresholdsIsNotAFinding(t *testing.T) {
	t.Parallel()
	// A gate that reports every lock is a gate people turn off. Every
	// statement takes a lock.
	findings, migration := migrationFindings(lockRun(120, false), defaultGate())
	require.Empty(t, findings)
	require.Empty(t, migration.Locks)
}

func TestGate_ALockNothingWaitedForIsStillShownWhenSomethingWaited(t *testing.T) {
	t.Parallel()
	// Blocking is the other half of the question. A short lock another session
	// waited on cost somebody something, so it reaches the evidence table even
	// though it is under the warning threshold.
	_, migration := migrationFindings(lockRun(120, true), defaultGate())
	require.Len(t, migration.Locks, 1)
	require.True(t, migration.Locks[0].Blocking)
}

func TestGate_TheThresholdsComeFromTheManifest(t *testing.T) {
	t.Parallel()
	strict := defaultGate()
	strict.LockWarnMS, strict.LockFailMS = 50, 100
	findings, _ := migrationFindings(lockRun(120, false), strict)
	require.Len(t, findings, 1)
	require.Equal(t, report.LevelFail, findings[0].Level)

	relaxed := defaultGate()
	relaxed.LockWarnMS, relaxed.LockFailMS = 30000, 60000
	findings, _ = migrationFindings(lockRun(4200, false), relaxed)
	require.Empty(t, findings)
}

func TestGate_AFailedRehearsalIsTheWorstMigrationFinding(t *testing.T) {
	t.Parallel()
	full := insights.Full{Rehearsal: &insights.Rehearsal{
		Failed: true, Error: `relation "orders" does not exist`,
		Pending: []insights.Migration{{Name: "0004.sql", Version: "0004"}},
	}}
	findings, _ := migrationFindings(full, defaultGate())
	require.Len(t, findings, 1)
	require.Equal(t, ruleMigrationFailed, findings[0].Rule)
	require.Equal(t, report.LevelFail, findings[0].Level)
	require.Contains(t, findings[0].Detail, `relation "orders" does not exist`)
}

func TestGate_ALintFindingCarriesItsOwnRuleName(t *testing.T) {
	t.Parallel()
	// The stable identifier for a lint finding is the rule name, not the
	// policy key that governs all six. "migration_lint" in a comment tells
	// nobody what to change.
	full := insights.Full{Rehearsal: &insights.Rehearsal{
		Pending: []insights.Migration{{Name: "0005.sql", Version: "0005"}},
		Lint: []insights.LintFinding{{
			Rule: insights.RuleIndexNotConcurrent, Migration: "0005.sql",
			Statement: "CREATE INDEX ON orders (customer_id)", Table: "orders", Rows: 2100000,
			Detail: "This locks writes on orders for the length of the build.",
			Fix:    "CREATE INDEX CONCURRENTLY, outside a transaction.",
		}},
	}}
	findings, _ := migrationFindings(full, defaultGate())
	require.Len(t, findings, 1)
	require.Equal(t, "index_not_concurrent", findings[0].Rule)
	require.Equal(t, report.LevelWarn, findings[0].Level)
	require.Contains(t, findings[0].Detail, "about 2100000 rows in orders")
}

func TestGate_ARewriteIsReportedByTheDatabaseAndNotGuessed(t *testing.T) {
	t.Parallel()
	full := insights.Full{Rehearsal: &insights.Rehearsal{
		Pending: []insights.Migration{{Name: "0006.sql", Version: "0006"}},
		Statements: []insights.StatementTiming{{
			SQL: "ALTER TABLE orders ALTER COLUMN total TYPE numeric", MS: 94000,
			Rewrote: []string{"orders"},
		}},
	}}
	findings, migration := migrationFindings(full, defaultGate())
	require.Len(t, findings, 1)
	require.Equal(t, ruleMigrationRewrite, findings[0].Rule)
	require.Equal(t, "orders", findings[0].Where)
	require.Len(t, migration.Slowest, 1)
	require.Equal(t, []string{"orders"}, migration.Slowest[0].Rewrote)
}

func TestGate_TurnedOffChecksAreSaidRatherThanPassedOver(t *testing.T) {
	t.Parallel()
	// A report that silently omits a check reads exactly like a check that
	// found nothing.
	full := insights.Full{
		Off:     []string{"migration rehearsal, because insights.migration_rehearsal is false"},
		Missing: []string{"the migrations were not rehearsed: no migration tool was recognised"},
	}
	_, migration := migrationFindings(full, defaultGate())
	require.Len(t, migration.Notes, 2)
	r := report.Run{Migration: migration}
	require.Contains(t, r.Markdown(), "no migration tool was recognised")
}

func TestGate_InsightsTurnedOffIsSaidBeforeAnythingIsOpened(t *testing.T) {
	t.Parallel()
	// A manifest that turns the checks off must not pay for a session and a
	// connection to be told so, and the report must say the check did not run
	// rather than showing nothing, which reads like a check that found
	// nothing.
	var run report.Run
	findings := readDatabase(context.Background(), nil, nil, defaultGate(),
		insights.Config{Enabled: false}, &run, "", "")
	require.Nil(t, findings)
	require.Len(t, run.Notes, 1)
	require.Contains(t, run.Notes[0], "insights.enabled is false")
	require.Contains(t, run.Markdown(), "insights.enabled is false")
}

func TestGate_AnUnknownDestinationDoesNotReportPass(t *testing.T) {
	t.Parallel()
	// It used to. Run.Verdict ignored Run.Egress entirely, so a run whose
	// environment reached for a host nothing in the manifest mentions still
	// reported pass and exited zero.
	egress := &report.Egress{Allowed: 12, Refused: 1, Surprises: []string{"api.segment.io"}}
	f := egressFinding(egress, defaultGate())
	require.NotNil(t, f)
	require.Equal(t, report.LevelFail, f.Level)
	require.Equal(t, 1, f.Count)

	r := report.Run{Egress: egress, Findings: []report.Finding{*f},
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}}}
	require.Equal(t, report.VerdictFail, r.Verdict())
	require.Contains(t, r.Markdown(), "api.segment.io")

	err := ciExit(r)
	require.Error(t, err)
	require.Equal(t, aferrors.ExitPolicyDenied, exitCodeOfSilent(t, err))
}

func TestGate_AnUnknownDestinationCanBeSetToWarn(t *testing.T) {
	t.Parallel()
	gate := defaultGate()
	gate.EgressSurprise = report.LevelWarn
	f := egressFinding(&report.Egress{Surprises: []string{"api.segment.io"}}, gate)
	require.NotNil(t, f)
	require.Equal(t, report.LevelWarn, f.Level)

	gate.EgressSurprise = report.LevelIgnore
	require.Nil(t, egressFinding(&report.Egress{Surprises: []string{"api.segment.io"}}, gate))
}

func TestGate_RefusedHostsTheManifestMentionsAreNotASurprise(t *testing.T) {
	t.Parallel()
	// A host with a rule that says block was blocked on purpose.
	require.Nil(t, egressFinding(&report.Egress{Allowed: 3, Refused: 4}, defaultGate()))
}

func TestGate_AFailedTeardownDoesNotReportPass(t *testing.T) {
	t.Parallel()
	// Teardown used to run in a deferred function after the report was
	// written, and the error was discarded, so a run that could not remove its
	// database reported pass while the branch stayed up.
	c := &report.Cleanup{Removed: 4, Pending: []string{"database shop-x: provider unreachable"}}
	f := cleanupFinding(c, defaultGate())
	require.NotNil(t, f)
	require.Equal(t, report.LevelFail, f.Level)
	require.Equal(t, 1, f.Count)

	r := report.Run{Cleanup: c, Findings: []report.Finding{*f},
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}}}
	require.Equal(t, report.VerdictFail, r.Verdict())
	require.Equal(t, aferrors.ExitInterruptedDirty, exitCodeOfSilent(t, ciExit(r)))
	require.Contains(t, r.Markdown(), "provider unreachable")
}

func TestGate_ACleanTeardownIsNotAFinding(t *testing.T) {
	t.Parallel()
	require.Nil(t, cleanupFinding(&report.Cleanup{Removed: 7}, defaultGate()))
	// Nil means teardown was not attempted, which is what --keep asks for. A
	// run that deliberately kept its environment has not failed to clean up.
	require.Nil(t, cleanupFinding(nil, defaultGate()))
}

func TestGate_UnmaskedDataDoesNotReportPass(t *testing.T) {
	t.Parallel()
	v := &report.Verification{Columns: 84, RowsSampled: 168000,
		Findings: []string{"public.users.email holds email (12 of the sampled rows, for example a***@e***)"}}
	f := maskingFinding(v, defaultGate())
	require.NotNil(t, f)
	require.Equal(t, report.LevelFail, f.Level)

	r := report.Run{Verification: v, Findings: []report.Finding{*f},
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}}}
	require.Equal(t, report.VerdictFail, r.Verdict())
	require.Equal(t, aferrors.ExitVerification, exitCodeOfSilent(t, ciExit(r)))
}

func TestGate_AVerificationThatCouldNotRunIsNotAFinding(t *testing.T) {
	t.Parallel()
	// A scan that could not run is a fact about us. Counting it against the
	// change is exactly what blocked exists to prevent.
	v := &report.Verification{Unavailable: "the branch refused a connection"}
	require.Nil(t, maskingFinding(v, defaultGate()))
	r := report.Run{Verification: v,
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}}}
	require.Equal(t, report.VerdictPass, r.Verdict())
	require.Contains(t, r.Markdown(), "Masking was not checked on this branch")
}

func TestGate_ACleanVerificationSaysWhatItCovered(t *testing.T) {
	t.Parallel()
	r := report.Run{
		Verification: &report.Verification{Clean: true, Columns: 84, RowsSampled: 168000},
		Workflows:    []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}},
	}
	require.Contains(t, r.Markdown(), "84 columns read back, 168000 rows sampled")
}

func TestGate_ALoadBreachIsAFindingRatherThanAListing(t *testing.T) {
	t.Parallel()
	// The thresholds were compared and the breaches were listed, and they
	// changed nothing. A run whose p95 doubled reported pass.
	l := &report.Load{Sent: 4200, Rate: 70, P95Ms: 480, Regressed: []string{"GET /orders p95"}}
	f := loadFinding(l, defaultGate())
	require.NotNil(t, f)
	require.Equal(t, report.LevelWarn, f.Level, "the site says warn, and so does the default")
	require.Equal(t, 1, f.Count)
}

func TestCIExit_OnlyFailIsNonZero(t *testing.T) {
	t.Parallel()
	for _, verdict := range []string{
		report.VerdictPass, report.VerdictWarn, report.VerdictFlaky,
		report.VerdictBlocked, report.VerdictUnverified,
	} {
		r := report.Run{Workflows: []report.Workflow{{Name: "checkout", Verdict: verdict}}}
		if verdict == report.VerdictWarn {
			// No workflow produces warn, so it comes from a finding.
			r = report.Run{
				Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}},
				Findings: []report.Finding{{
					Rule: ruleMigrationLock, Level: report.LevelWarn, Title: "a warning",
				}},
			}
		}
		require.Equal(t, verdict, r.Verdict())
		require.NoError(t, ciExit(r), "%s must exit zero", verdict)
	}
}

func TestCIExit_BlockedStillExitsZeroWithAWarningPresent(t *testing.T) {
	t.Parallel()
	// The one rule that must not regress. A blocked workflow is a gap in our
	// tooling and must never fail somebody's build, whatever else the run
	// found.
	r := report.Run{
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictBlocked}},
		Findings: []report.Finding{{
			Rule: ruleMigrationLock, Level: report.LevelWarn, Title: "a warning",
		}},
	}
	require.NoError(t, ciExit(r))
}

func TestCIExit_AWorkflowFailureOutranksAFindingForTheCode(t *testing.T) {
	t.Parallel()
	// A pipeline reads the exit code. The one it gets should be about the
	// change's own behaviour before it is about the database.
	r := report.Run{
		Workflows: []report.Workflow{{Name: "checkout", Verdict: report.VerdictFail}},
		Findings: []report.Finding{{
			Rule: ruleEgressSurprise, Level: report.LevelFail, Title: "a host", Count: 1,
		}},
	}
	require.Equal(t, aferrors.ExitTestFailure, exitCodeOfSilent(t, ciExit(r)))
}

func TestCIExit_AViolatedInvariantOutranksEverything(t *testing.T) {
	t.Parallel()
	r := report.Run{
		Workflows:  []report.Workflow{{Name: "checkout", Verdict: report.VerdictPass}},
		Invariants: []report.Invariant{{Name: "no_orphan_orders", Held: false}},
		Findings: []report.Finding{{
			Rule: ruleEgressSurprise, Level: report.LevelFail, Title: "a host", Count: 1,
		}},
	}
	require.Equal(t, aferrors.ExitTestFailure, exitCodeOfSilent(t, ciExit(r)))
}

func TestCIExit_AMigrationFindingNamesItsRule(t *testing.T) {
	t.Parallel()
	err := gateError(report.Finding{
		Rule: "index_not_concurrent", Level: report.LevelFail, Title: "index built without CONCURRENTLY",
	})
	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Equal(t, aferrors.AFDB031, coded.Entry.Code)
	require.Contains(t, coded.Error(), "index_not_concurrent")
}

// exitCodeOfSilent reads the code off a silent failure, which is what af ci
// returns once its report has already said what is wrong.
func exitCodeOfSilent(t *testing.T, err error) aferrors.ExitCode {
	t.Helper()
	require.Error(t, err)
	var quiet *silentError
	require.True(t, aferrors.As(err, &quiet), "expected a silent failure, got %v", err)
	return quiet.ExitCode()
}

// A run in which every workflow was blocked is not a passing run.
//
// The defect this covers shipped in both of this repository's own answers. The
// control plane's dogfood job reported "6 workflows could not be carried
// through" and exited zero on every run, and the example corpus reported
// "Nothing ran" and went green, so a whole nightly could be green having
// verified nothing about any application.
func TestWorkflowsUnverified_EveryWorkflowBlockedIsAFinding(t *testing.T) {
	run := report.Run{Workflows: []report.Workflow{
		{Name: "sign-in", Verdict: report.VerdictBlocked},
		{Name: "place-an-order", Verdict: report.VerdictUnverified},
	}}
	f := workflowsUnverifiedFinding(run, defaultGate())
	require.NotNil(t, f, "a run that verified nothing must produce a finding")
	require.Equal(t, report.LevelFail, f.Level, "the default has to be the one that does not lie")
	require.Contains(t, f.Title, "No workflow reached a verdict")
}

// A manifest that declares no workflows is the same fact wearing a hat.
func TestWorkflowsUnverified_NoWorkflowsAtAllIsAlsoAFinding(t *testing.T) {
	f := workflowsUnverifiedFinding(report.Run{}, defaultGate())
	require.NotNil(t, f)
	require.Contains(t, f.Title, "No workflows ran")
}

// One verdict about the application is enough to clear it.
//
// The per workflow rule is untouched by this: five blocked workflows beside one
// that failed is a run that tested the application, and the failure is what it
// reports. Charging the five to the application is the mistake this must not
// start making while fixing the other one.
func TestWorkflowsUnverified_OneRealVerdictIsEnough(t *testing.T) {
	for _, verdict := range []string{report.VerdictPass, report.VerdictFail, report.VerdictFlaky} {
		run := report.Run{Workflows: []report.Workflow{
			{Name: "blocked-one", Verdict: report.VerdictBlocked},
			{Name: "real-one", Verdict: verdict},
		}}
		require.Nil(t, workflowsUnverifiedFinding(run, defaultGate()),
			"a %s verdict is a verdict about the application", verdict)
	}
}

// A project with no workflows yet can say so, and have that recorded.
func TestWorkflowsUnverified_IgnoreTurnsItOff(t *testing.T) {
	gate := defaultGate()
	gate.WorkflowsUnverified = report.LevelIgnore
	require.Nil(t, workflowsUnverifiedFinding(report.Run{}, gate))
}

// An unreadable verdict is blocked, so a runner ahead of this engine cannot
// make a run look verified by naming an outcome this build does not know.
func TestWorkflowsUnverified_AnUnknownVerdictDoesNotCount(t *testing.T) {
	run := report.Run{Workflows: []report.Workflow{
		{Name: "from-the-future", Verdict: "materialised"},
	}}
	require.NotNil(t, workflowsUnverifiedFinding(run, defaultGate()))
}
