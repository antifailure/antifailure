package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// chaosArgs validates one argument object against run_chaos_faults's own
// schema.
//
// Through the published schema rather than around it, because the schema is
// both the validator and the document a caller reads, and a bound that is
// published and not enforced is worse than no bound at all.
func chaosArgs(t *testing.T, body string) *Fault {
	t.Helper()
	tool := newRunChaosFaultsTool(chaosProject(), nil, nil)
	_, fault := validateArguments(tool.Input, json.RawMessage(body))
	return fault
}

// chaosProject is a project that declares a chaos block that is on.
//
// Declared and enabled, because the tool answers a project without one with a
// finished INCONCLUSIVE run, so a fixture that left it out would exercise that
// branch in every test that meant to exercise another.
func chaosProject() *Project {
	return &Project{
		ID: "test-project",
		Manifest: &schema.Manifest{
			Name: "test-project",
			Chaos: &schema.Chaos{
				Enabled: true,
				Faults: []schema.Fault{{
					Name: "kill the postmaster", Kind: schema.FaultProcessKill,
				}},
			},
		},
		Gate: report.Configure(nil),
	}
}

// chaosRun is a run that injected a fault and proved the recovery, so that a
// test of anything else is not silently testing the skipped branch.
func chaosRun() *env.ChaosRun {
	return &env.ChaosRun{
		Report: report.Chaos{Faults: []report.ChaosFault{{
			Name: "kill the postmaster", Kind: "process_kill", Target: "database",
			Evidence:   "sent SIGKILL to pid 1 matching \"postgres\"",
			Injected:   true,
			Undone:     true,
			DurationMs: 8400,
			Recovery: &report.ChaosRecovery{
				Crashed: true, Signal: 9, Replayed: true,
				RedoStart: "0/1A2B3C0", RedoEnd: "0/1A9F400",
				StateBefore: "in production", StateAfter: "in production",
				Acknowledged: 4211, Present: 4213, Lost: 0, Phantom: 0, InFlightLanded: 2,
				HeapRows: 4213, IndexRows: 4213, Amcheck: "no corruption found",
				ChecksumsOn: true, DowntimeMs: 3100, Verified: true,
			},
		}}},
	}
}

// errNoEnvironmentRunning stands in for the engine error a tool gets when
// there is no environment. Its text must never reach a caller.
var errNoEnvironmentRunning = errors.New("AF-ENV-004: nothing is running for this branch")

func TestChaosSchema_TheCallCannotChooseWhatToBreak(t *testing.T) {
	t.Parallel()
	// The single most important assertion in this file. Every other tool on
	// this server is bounded by what it cannot point at; this one is bounded
	// by what it cannot ASK FOR. A fault kind, a target, a process name, a
	// signal or a container id in this schema would turn a tool that runs a
	// committed document into one that runs whatever a caller typed, and the
	// blast radius argument in the manifest's own prose would be a comment
	// rather than a property of the interface.
	tool := newRunChaosFaultsTool(chaosProject(), nil, nil)
	for _, forbidden := range []string{
		"fault", "faults", "kind", "fault_kind", "target", "service", "process",
		"signal", "container", "container_id", "duration", "duration_seconds",
		"headroom", "data_dir", "branch",
	} {
		require.NotContainsf(t, tool.Input.Properties, forbidden,
			"%q is a way for a caller to choose what gets broken", forbidden)
	}
	require.ElementsMatch(t,
		[]string{"project_id", "idempotency_key", "hypothesis"},
		propertyNames(tool.Input),
		"a property was added to the one tool whose job is to break something")

	// And the refusal is enforced rather than merely undeclared, or the list
	// above would be a comment about a schema nobody validates against.
	fault := chaosArgs(t, `{"project_id":"test-project","kind":"container_kill"}`)
	require.NotNil(t, fault, "an unknown field was accepted by the tool that breaks things")
}

func TestChaosSchema_BoundsTheHypothesisItRecords(t *testing.T) {
	t.Parallel()
	require.Nil(t, chaosArgs(t, `{"project_id":"test-project"}`))
	require.Nil(t, chaosArgs(t, `{"project_id":"test-project","hypothesis":"it will lose nothing"}`))
	require.NotNil(t, chaosArgs(t, `{}`), "project_id is required")
	require.NotNil(t,
		chaosArgs(t, `{"project_id":"test-project","hypothesis":"`+strings.Repeat("x", 2001)+`"}`),
		"an unbounded hypothesis is a way to fill the run store")
}

func TestRunChaosFaults_AProjectWithNoChaosBlockBreaksNothingAndIsInconclusive(t *testing.T) {
	t.Parallel()
	// A project that has not said which failures it wants rehearsed has not
	// passed a chaos run, it has not had one. It must also not reach the
	// caller as the retryable fault below: retrying will never help.
	for _, tc := range []struct {
		name  string
		chaos *schema.Chaos
	}{
		{"no block at all", nil},
		{"a block that is off", &schema.Chaos{Enabled: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			inject := func(context.Context) (*env.ChaosRun, error) {
				called = true
				return chaosRun(), nil
			}
			p := chaosProject()
			p.Manifest.Chaos = tc.chaos

			h := newToolHarness(t)
			native, body, fault := runChaosFaults(
				context.Background(), p, h.engine, inject,
				h.newRun(t, "run_chaos_faults"), "")

			require.Nil(t, fault)
			require.Equal(t, report.VerdictUnverified, native)
			require.Contains(t, body.Summary, "chaos block")
			require.False(t, called,
				"a fault was injected for a project that declared none")
			doc, ok := body.Detail.(*chaosDoc)
			require.True(t, ok)
			require.False(t, doc.Declared)
		})
	}
}

func TestRunChaosFaults_AFaultThatCouldNotBeInjectedIsAFaultAndNotAVerdict(t *testing.T) {
	t.Parallel()
	// It runs against an environment rather than creating one, so the usual
	// failure is that nothing is running. That says nothing about what the
	// system does when it fails, so it must reach the caller as a failed run
	// and never as a clean one.
	inject := func(context.Context) (*env.ChaosRun, error) {
		return nil, errNoEnvironmentRunning
	}
	h := newToolHarness(t)
	native, body, fault := runChaosFaults(
		context.Background(), chaosProject(), h.engine, inject,
		h.newRun(t, "run_chaos_faults"), "")

	require.NotNil(t, fault)
	require.Equal(t, FaultSafetyUnavailable, fault.Code)
	require.True(t, fault.Retryable)
	require.Empty(t, native)
	require.Nil(t, body)
	// The engine's own error never reaches the caller, because it describes
	// the host. It travels wrapped, for the server log.
	require.NotContains(t, fault.Detail, errNoEnvironmentRunning.Error())
	require.ErrorIs(t, fault, errNoEnvironmentRunning)
}

func TestRunChaosFaults_ASkippedRunIsInconclusiveAndNeverAPass(t *testing.T) {
	t.Parallel()
	// The case this whole tool is most likely to be wrong about. The chaos
	// block was declared and on, the run finished, and no fault was injected:
	// a runtime that is not the local one, or a block with no faults in it.
	// Nothing broke, so nothing about the recovery was established, and there
	// are no findings to make it look otherwise. A pass here is a green over
	// an experiment that did not happen.
	inject := func(context.Context) (*env.ChaosRun, error) {
		return &env.ChaosRun{Report: report.Chaos{
			Skipped: "the chaos block is enabled and declares no faults",
		}}, nil
	}
	h := newToolHarness(t)
	native, body, fault := runChaosFaults(
		context.Background(), chaosProject(), h.engine, inject,
		h.newRun(t, "run_chaos_faults"), "")

	require.Nil(t, fault)
	require.Equal(t, report.VerdictUnverified, native)
	require.Empty(t, body.Findings.Items,
		"the skipped run has no findings, which is exactly why the verdict has to carry it")
	require.Contains(t, body.Summary, "declares no faults")
	doc := body.Detail.(*chaosDoc)
	require.True(t, doc.Declared)
	require.Contains(t, strings.Join(doc.Notes, " "), "No fault was injected")
}

func TestChaosVerdict_HeldAndVerifiedAreTwoDifferentAnswers(t *testing.T) {
	t.Parallel()
	// Three outcomes, and the middle one is the one a surface gets wrong: a
	// run with nothing at fail level, which did not establish its claim. It is
	// not a pass, and it is not a failure either.
	run := chaosRun()
	require.Equal(t, report.VerdictPass, chaosVerdict(run, true, true))
	require.Equal(t, report.VerdictUnverified, chaosVerdict(run, true, false))
	require.Equal(t, report.VerdictFail, chaosVerdict(run, false, true))
	require.Equal(t, report.VerdictFail, chaosVerdict(run, false, false),
		"a run that found something wrong is a failure whether or not it also could not look")
}

func TestRunChaosFaults_ReadsTheVerdictThroughTheSameClassifierAsTheCommandLine(t *testing.T) {
	t.Parallel()
	// Not a test of a copy of the rule list, but of the fact that there is no
	// copy. A rule that means "I could not look" reaches an INCONCLUSIVE here
	// because env.ChaosRun.Holds says so, and that is the same call af chaos
	// makes. A tool with its own list would pass, through an agent, a run
	// that a terminal reports as unverified.
	for _, tc := range []struct {
		name    string
		finding report.Finding
		want    string
	}{
		{"a lost commit was found", report.Finding{
			Rule: pgcrash.RuleLostCommit, Level: report.LevelFail,
		}, report.VerdictFail},
		{"the crash could not be established", report.Finding{
			Rule: pgcrash.RuleNoCrash, Level: report.LevelWarn,
		}, report.VerdictUnverified},
		{"the project ignored the unverified level", report.Finding{
			Rule: pgcrash.RuleNoCrash, Level: report.LevelIgnore,
		}, report.VerdictUnverified},
		{"a fault would not go in", report.Finding{
			Rule: env.RuleFaultRefused, Level: report.LevelWarn,
		}, report.VerdictUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := chaosRun()
			run.Findings = []report.Finding{tc.finding}
			inject := func(context.Context) (*env.ChaosRun, error) { return run, nil }

			h := newToolHarness(t)
			native, body, fault := runChaosFaults(
				context.Background(), chaosProject(), h.engine, inject,
				h.newRun(t, "run_chaos_faults"), "")

			require.Nil(t, fault)
			require.Equal(t, tc.want, native)
			require.Len(t, body.Findings.Items, 1,
				"the finding that decided the verdict has to reach the caller")
		})
	}
}

func TestChaosMetrics_DoNotInventCommitCountsForAProofThatNeverRan(t *testing.T) {
	t.Parallel()
	// A zero lost commit count from a run that never counted commits is the
	// strongest claim this result can make, and it would be made by accident.
	// So the commit metrics exist only when a durability proof produced them.
	run := chaosRun()
	run.Report.Faults[0].Recovery = nil

	names := metricNames(chaosMetrics(run))
	require.NotContains(t, names, "commits_lost")
	require.NotContains(t, names, "phantom_rows")
	require.NotContains(t, names, "commits_acknowledged")
	require.Contains(t, names, "faults_injected")

	// And the other direction, or an implementation that dropped the commit
	// metrics entirely would pass the assertions above.
	require.Contains(t, metricNames(chaosMetrics(chaosRun())), "commits_lost")
}

func TestChaosMetrics_ReportTheWorstDowntimeAndNotTheirSum(t *testing.T) {
	t.Parallel()
	// Two separate outages summed is a number describing an outage that never
	// happened. The question the metric answers is how long the worst of these
	// failures took to come back.
	run := chaosRun()
	second := run.Report.Faults[0]
	rec := *second.Recovery
	rec.DowntimeMs = 900
	rec.Lost = 3
	second.Recovery = &rec
	run.Report.Faults = append(run.Report.Faults, second)

	metrics := map[string]Metric{}
	for _, m := range chaosMetrics(run) {
		metrics[m.Name] = m
	}
	require.Equal(t, float64(3100), metrics["longest_downtime"].Value,
		"the downtimes were summed rather than maximised")
	require.Equal(t, float64(3), metrics["commits_lost"].Value,
		"lost commits are a total, because a lost commit is lost whichever fault lost it")
	require.True(t, metrics["commits_lost"].Breached,
		"a lost commit is wrong at one, so any count at all is a breach")
	require.False(t, metrics["phantom_rows"].Breached)
}

func TestDescribeChaos_NamesAFaultThatWasLeftInPlace(t *testing.T) {
	t.Parallel()
	// The one outcome here that changes what the NEXT thing to run against
	// this environment will meet. A caller reading the document rather than
	// the findings has to see it.
	run := chaosRun()
	run.Report.Faults[0].Undone = false

	doc := describeChaos(run, true, true)
	require.Contains(t, strings.Join(doc.Notes, " "), "still carrying it")
	require.Contains(t, strings.Join(doc.Notes, " "), "kill the postmaster")
	require.False(t, doc.Faults[0].Undone)

	// The undone run says nothing of the kind, or the note would be printed
	// for every fault and would mean nothing.
	require.Empty(t, describeChaos(chaosRun(), true, true).Notes)
}

func TestDescribeChaos_CarriesTheEvidenceThatBacksTheCrash(t *testing.T) {
	t.Parallel()
	// A recovery section with no evidence behind it is a report about a crash
	// nobody can show happened, which is the shape of claim this whole feature
	// exists to refuse in somebody else's system.
	doc := describeChaos(chaosRun(), true, true)
	require.Len(t, doc.Faults, 1)
	require.Contains(t, doc.Faults[0].Evidence, "SIGKILL")
	rec := doc.Faults[0].Recovery
	require.NotNil(t, rec)
	require.True(t, rec.Crashed)
	require.Equal(t, 9, rec.Signal)
	require.True(t, rec.Replayed)
	require.Equal(t, "0/1A2B3C0", rec.RedoStart)
	require.Equal(t, "0/1A9F400", rec.RedoEnd)
	require.Equal(t, 4211, rec.Acknowledged)
	require.Equal(t, 2, rec.InFlightLanded)
	require.True(t, rec.ChecksumsOn)

	// A fault with no proof carries no recovery, rather than an empty one that
	// reads as a proof that found nothing.
	none := chaosRun()
	none.Report.Faults[0].Recovery = nil
	require.Nil(t, describeChaos(none, true, true).Faults[0].Recovery)
}

func TestChaosSummary_DoesNotCollapseHeldIntoVerified(t *testing.T) {
	t.Parallel()
	// A summary that said "nothing went wrong" for a run that could not look
	// would undo in one line what the findings are careful about.
	run := chaosRun()
	require.Contains(t, chaosSummary(run, true, false, ""), "did not establish")
	require.NotContains(t, chaosSummary(run, true, false, ""), "Every acknowledged commit survived")
	require.Contains(t, chaosSummary(run, true, true, ""), "Every acknowledged commit survived")
	require.Contains(t, chaosSummary(run, false, true, ""), "did not")
	require.Contains(t, chaosSummary(run, true, true, ""),
		"1 of 1 declared faults were injected and undone")
}

func TestRunChaosFaults_RecordsTheHypothesisWithoutActingOnIt(t *testing.T) {
	t.Parallel()
	var got int
	inject := func(context.Context) (*env.ChaosRun, error) {
		got++
		return chaosRun(), nil
	}
	h := newToolHarness(t)
	native, body, fault := runChaosFaults(
		context.Background(), chaosProject(), h.engine, inject,
		h.newRun(t, "run_chaos_faults"), "the replica will take over")

	require.Nil(t, fault)
	require.Equal(t, report.VerdictPass, native)
	require.Contains(t, body.Summary, "the replica will take over")
	require.Equal(t, 1, got, "the hypothesis changed how many times the faults ran")
}

// propertyNames is the published property names of a schema.
func propertyNames(s *Schema) []string {
	out := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		out = append(out, name)
	}
	return out
}
