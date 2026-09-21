package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// Fault injection and the crash recovery proof, which until this file existed
// were reachable from the command line and from nowhere else.
//
// THE GAP THIS CLOSES. engine/internal/cli/chaos.go has served af chaos since
// it landed, and engine/internal/mcp referenced env.Orchestrator.RunChaos
// nowhere at all. Every other tool on this server rehearses a change against a
// system that WORKS: a migration runs, traffic is sent, a browser is driven, an
// invariant is checked, and each of them measures a healthy environment doing
// what it does. None of them can ask what the system does when it stops
// working, which is the question somebody changing a storage engine, a
// replication setting, a checkpoint interval or a fsync path is actually
// asking. tools/docs/surface-exemptions.tsv carried a row saying so, and this
// file removes it.
//
// WHAT IS DELIBERATELY NOT HERE. There is no fault kind, no target, no
// process name, no signal and no duration in this schema. A caller cannot say
// what to break. The faults are the ones the manifest's chaos block declares,
// which is a document in the candidate repository that a person wrote and
// committed, and they are injected in the order it lists them. That is the
// same rule the whole server runs on: the caller chooses what to rehearse and
// the project chooses what is safe, and it matters more here than anywhere
// else, because this is the one tool whose job is to break something.
//
// The blast radius is the environment's own containers and the injector proves
// it from the daemon at the instant of the act rather than trusting a label it
// read earlier. Nothing here widens it, and there is no argument that could.

// injectFaults runs the manifest's declared faults against the environment.
//
// A function value so the tool can be built against a fake in tests. The real
// one is the orchestrator, which is the same code path af chaos takes.
type injectFaults func(ctx context.Context) (*env.ChaosRun, error)

// newRunChaosFaultsTool builds run_chaos_faults.
func newRunChaosFaultsTool(p *Project, eng *Engine, inject injectFaults) *Tool {
	return &Tool{
		Name:  "run_chaos_faults",
		Title: "Break the environment on purpose and prove what the recovery did",
		Description: "Answer what this system does when it stops working, rather than " +
			"what it does when it works. It injects the faults the project's own " +
			"manifest declares into the running environment, one at a time, each one " +
			"undone before the next begins, and reads what the system did about each. " +
			"The faults are real: a process killed with SIGKILL, a container stopped, " +
			"frozen or detached from the network, a data directory made read only, a " +
			"filesystem filled. Nothing is simulated. " +
			"Around a fault aimed at the database the durability proof runs: " +
			"concurrent writers commit while the fault lands, and afterwards every " +
			"commit the client was TOLD was committed has to still be there, nothing " +
			"may be there that no client ever wrote, and the write ahead log has to " +
			"show it really replayed, read from the server's own log, its control " +
			"file and its WAL positions rather than from the fact that it came back " +
			"up. A database that came back is not a database that kept your commits. " +
			"Use this for a change to a storage parameter, a checkpoint or fsync " +
			"setting, a replication option or anything whose whole value is what " +
			"happens during a failure. rehearse_migration_safety, run_load_test and " +
			"run_sql_workload all measure a healthy system. " +
			"You cannot choose what is broken. The faults come from the manifest's " +
			"chaos block, which is a committed document in the repository, and this " +
			"call carries no fault kind, no target and no signal. Nothing is aimed " +
			"anywhere but at the containers this environment created. " +
			"Bring an environment up first with start_environment; without one this " +
			"reports INCONCLUSIVE rather than a clean result, and so does a project " +
			"that declares no chaos block. " +
			"A result that HELD is not the same as one that was VERIFIED, and both " +
			"are reported: a run that could not establish its claim says so instead " +
			"of passing. " +
			"This takes as long as the faults it runs, so it returns a run_id " +
			"immediately: poll it with get_rehearsal_run.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id":      projectIDSchema(),
				"idempotency_key": idempotencyKeySchema(),
				"hypothesis": {
					Type: "string", MaxLength: 2000,
					Description: "Optional. What you expect the system to do when these " +
						"faults land, in your own words. Recorded with the run so the " +
						"result can be read against the expectation. It is never executed " +
						"and never changes which faults run or what is proved about them.",
				},
			},
		},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			hypothesis, _ := args["hypothesis"].(string)

			return eng.Submit(call, "run_chaos_faults", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					return runChaosFaults(ctx, p, eng, inject, runID, hypothesis)
				})
		},
	}
}

// chaosDoc is the tool specific evidence.
//
// It carries the injector's own words about what it did, because that is what
// backs the claim that the fault landed at all. A recovery section with no
// evidence behind it is a report about a crash nobody can show happened.
type chaosDoc struct {
	// Declared says the manifest has a chaos block that is on. False means
	// nothing ran, and the notes say so.
	Declared bool            `json:"declared"`
	Held     bool            `json:"held"`
	Verified bool            `json:"verified"`
	Faults   []chaosFaultDoc `json:"faults"`
	Notes    []string        `json:"notes,omitempty"`
}

// chaosFaultDoc is one fault and what it proved.
type chaosFaultDoc struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Target     string            `json:"target"`
	Injected   bool              `json:"injected"`
	Undone     bool              `json:"undone"`
	Evidence   string            `json:"evidence,omitempty"`
	Error      string            `json:"error,omitempty"`
	DurationMs int64             `json:"duration_ms"`
	Recovery   *chaosRecoveryDoc `json:"recovery,omitempty"`
}

// chaosRecoveryDoc is the durability proof around one fault.
type chaosRecoveryDoc struct {
	Crashed        bool   `json:"crashed"`
	Signal         int    `json:"signal,omitempty"`
	Replayed       bool   `json:"replayed"`
	RedoStart      string `json:"redo_start,omitempty"`
	RedoEnd        string `json:"redo_end,omitempty"`
	StateBefore    string `json:"state_before,omitempty"`
	StateAfter     string `json:"state_after,omitempty"`
	Acknowledged   int    `json:"acknowledged_commits"`
	Present        int    `json:"present_rows"`
	Lost           int    `json:"lost_commits"`
	Phantom        int    `json:"phantom_rows"`
	InFlightLanded int    `json:"in_flight_landed"`
	HeapRows       int64  `json:"heap_rows"`
	IndexRows      int64  `json:"index_rows"`
	Amcheck        string `json:"amcheck,omitempty"`
	ChecksumsOn    bool   `json:"checksums_on"`
	DowntimeMs     int64  `json:"downtime_ms"`
	Verified       bool   `json:"verified"`
}

// runChaosFaults is the experiment behind the tool.
func runChaosFaults(
	ctx context.Context, p *Project, eng *Engine, inject injectFaults,
	runID, hypothesis string,
) (string, *ResultBody, *Fault) {
	if eng.Cancelled(ctx, runID) {
		return "", nil, faultf(FaultRunNotCancellable, "This run was cancelled before it started.")
	}

	// A project with no chaos block is answered here rather than by the
	// orchestrator, and the difference is the answer's shape. RunChaos
	// returns a nil run for a manifest that declares none, which through the
	// fault below would reach a caller as a retryable failure to reach an
	// environment. Retrying it will never work: nothing is wrong except that
	// this project has not said which failures it wants rehearsed. So it is a
	// finished run carrying INCONCLUSIVE, which is the same answer
	// check_data_invariants gives a project that declares no invariants, and
	// for the same reason: an experiment that broke nothing has not passed.
	if m := p.Manifest; m == nil || m.Chaos == nil || !m.Chaos.Enabled {
		return report.VerdictUnverified, &ResultBody{
			Summary: "This project declares no chaos block, or declares one that is " +
				"off, so nothing was broken and this says nothing about the " +
				"recovery. Add one to antifailure.yaml naming the faults a " +
				"rehearsal may inject, and this reports what the system did about " +
				"each of them.",
			Detail: &chaosDoc{
				Declared: false,
				Faults:   []chaosFaultDoc{},
				Notes: []string{
					"Nothing was injected. The manifest's chaos block is what says " +
						"which failures a rehearsal is allowed to cause, and this " +
						"manifest declares none.",
				},
			},
		}, nil
	}

	eng.Phase(ctx, runID, "injecting the declared faults and proving the recovery")

	run, err := inject(ctx)
	if err != nil {
		// Nothing was broken, so this says nothing about the recovery.
		// Reporting a pass because no finding was raised would be reporting an
		// experiment that did not happen as one that found nothing, which is
		// the failure this whole server refuses.
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The faults could not be injected, so this says nothing about what " +
				"the system does when it fails. It runs against an environment " +
				"rather than creating one, so the usual cause is that nothing is " +
				"running for this branch: bring one up with start_environment. The " +
				"other cause is a container runtime this checkout cannot reach.",
			Retryable: true,
			wrapped:   err,
		}
	}
	if run == nil {
		// The manifest said yes above and the orchestrator returned nothing,
		// which is a disagreement between two reads of the same document
		// rather than a result. Saying so beats rendering an empty report.
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The chaos block was read as declared here and as absent by the " +
				"engine, so no faults ran and there is nothing to report.",
			Retryable: false,
		}
	}

	eng.Phase(ctx, runID, "ranking what the faults showed")

	held, verified := run.Holds()
	body := &ResultBody{
		Findings: boundFindings(run.Findings),
		Metrics:  chaosMetrics(run),
		Detail:   describeChaos(run, held, verified),
	}
	body.Summary = chaosSummary(run, held, verified, hypothesis)
	return chaosVerdict(run, held, verified), body, nil
}

// chaosVerdict maps the run onto the engine's own vocabulary.
//
// The same two questions af chaos asks, in the same order, and it reaches them
// through the same env.ChaosRun.Holds the command line calls. There are two
// callers now and they must not be able to disagree: a run that fails at a
// terminal and passes through an agent is worse than one that fails in both
// places.
//
// A skipped run is UNVERIFIED and not PASS, and that is the case this function
// exists for. The chaos block was declared and on, and something stopped it
// running anyway: a runtime that is not the local one, an environment that is
// not up, a block with no faults in it. Nothing was broken, so nothing about
// the recovery was established, and a green over that is the shape this
// product exists to stop.
func chaosVerdict(run *env.ChaosRun, held, verified bool) string {
	if run.Report.Skipped != "" {
		return report.VerdictUnverified
	}
	if !held {
		return report.VerdictFail
	}
	if !verified {
		return report.VerdictUnverified
	}
	return report.VerdictPass
}

// chaosMetrics is the numbers a caller can compare between runs.
//
// Commits are summed across the faults that ran a durability proof, because a
// lost commit is a lost commit whichever fault lost it, and downtime is the
// LONGEST rather than the total, because the question it answers is how long
// the worst of these failures took to come back and a sum of two separate
// outages is a number describing nothing.
//
// None of them carries a threshold. The chaos block has none: a lost commit is
// wrong at one, and so is a phantom, which is why they are findings rather
// than measurements against a limit.
func chaosMetrics(run *env.ChaosRun) []Metric {
	var injected, proofs, acked, lost, phantom, landed int
	var worstDowntime int64
	for _, f := range run.Report.Faults {
		if f.Injected {
			injected++
		}
		rec := f.Recovery
		if rec == nil {
			continue
		}
		proofs++
		acked += rec.Acknowledged
		lost += rec.Lost
		phantom += rec.Phantom
		landed += rec.InFlightLanded
		if rec.DowntimeMs > worstDowntime {
			worstDowntime = rec.DowntimeMs
		}
	}
	metrics := []Metric{
		{Name: "faults_injected", Value: float64(injected), Unit: "faults"},
		{Name: "durability_proofs", Value: float64(proofs), Unit: "proofs"},
	}
	if proofs == 0 {
		return metrics
	}
	return append(metrics,
		Metric{Name: "commits_acknowledged", Value: float64(acked), Unit: "commits"},
		// Breached on any count at all, because the threshold is zero and it
		// is not the project's to move.
		Metric{Name: "commits_lost", Value: float64(lost), Unit: "commits", Breached: lost > 0},
		Metric{Name: "phantom_rows", Value: float64(phantom), Unit: "rows", Breached: phantom > 0},
		Metric{Name: "in_flight_commits_landed", Value: float64(landed), Unit: "commits"},
		Metric{Name: "longest_downtime", Value: float64(worstDowntime), Unit: "ms"},
	)
}

// describeChaos carries the faults and their proofs into the stored document.
func describeChaos(run *env.ChaosRun, held, verified bool) *chaosDoc {
	doc := &chaosDoc{
		Declared: true, Held: held, Verified: verified,
		Faults: make([]chaosFaultDoc, 0, len(run.Report.Faults)),
	}
	if run.Report.Skipped != "" {
		doc.Notes = append(doc.Notes, "No fault was injected: "+run.Report.Skipped)
	}
	for _, f := range run.Report.Faults {
		doc.Faults = append(doc.Faults, chaosFaultDoc{
			Name: f.Name, Kind: f.Kind, Target: f.Target,
			Injected: f.Injected, Undone: f.Undone,
			Evidence: f.Evidence, Error: f.Error, DurationMs: f.DurationMs,
			Recovery: recoveryDoc(f.Recovery),
		})
		// A fault that went in and did not come out is named in the notes as
		// well as in a finding, because it is the one outcome here that
		// changes what the NEXT thing to run against this environment will
		// meet, and a caller reading the document rather than the findings
		// would otherwise not see it.
		if f.Injected && !f.Undone {
			doc.Notes = append(doc.Notes, fmt.Sprintf(
				"The fault %q was injected and its undo did not run, so the environment "+
					"is still carrying it.", f.Name))
		}
	}
	return doc
}

// recoveryDoc copies one proof, or nothing when no proof ran.
func recoveryDoc(rec *report.ChaosRecovery) *chaosRecoveryDoc {
	if rec == nil {
		return nil
	}
	return &chaosRecoveryDoc{
		Crashed: rec.Crashed, Signal: rec.Signal,
		Replayed: rec.Replayed, RedoStart: rec.RedoStart, RedoEnd: rec.RedoEnd,
		StateBefore: rec.StateBefore, StateAfter: rec.StateAfter,
		Acknowledged: rec.Acknowledged, Present: rec.Present,
		Lost: rec.Lost, Phantom: rec.Phantom, InFlightLanded: rec.InFlightLanded,
		HeapRows: rec.HeapRows, IndexRows: rec.IndexRows, Amcheck: rec.Amcheck,
		ChecksumsOn: rec.ChecksumsOn, DowntimeMs: rec.DowntimeMs,
		Verified: rec.Verified,
	}
}

// chaosSummary writes the sentence a caller reads first.
//
// It says HELD and VERIFIED separately wherever they differ, because the whole
// feature rests on that split and a summary that collapsed it would undo in
// one line what the findings were careful about.
func chaosSummary(run *env.ChaosRun, held, verified bool, hypothesis string) string {
	var b strings.Builder
	switch {
	case run.Report.Skipped != "":
		b.WriteString("No fault was injected, so nothing about the recovery was " +
			"established: " + run.Report.Skipped + ".")
	case !held:
		b.WriteString(chaosCounts(run) + " Something the system was supposed to " +
			"survive it did not: read the findings, which name the fault and what " +
			"it cost.")
	case !verified:
		b.WriteString(chaosCounts(run) + " Nothing was found to be wrong and the " +
			"run did not establish what it set out to, so this is not a pass: the " +
			"findings say which claim could not be made.")
	default:
		b.WriteString(chaosCounts(run) + " Every acknowledged commit survived, no " +
			"row appeared that no client wrote, and the write ahead log was shown " +
			"to have replayed.")
	}
	if hypothesis != "" {
		b.WriteString(" You expected: " + clip(hypothesis, 2000))
	}
	return b.String()
}

// chaosCounts opens the summary with what actually ran.
func chaosCounts(run *env.ChaosRun) string {
	var injected, proofs int
	for _, f := range run.Report.Faults {
		if f.Injected {
			injected++
		}
		if f.Recovery != nil {
			proofs++
		}
	}
	s := fmt.Sprintf("%d of %d declared faults were injected and undone",
		injected, len(run.Report.Faults))
	if proofs > 0 {
		s += fmt.Sprintf(", %d of them with the durability proof around it", proofs)
	}
	return s + "."
}

// injectFaults runs the manifest's chaos block against the built orchestrator.
func (f *orchestratorFactory) injectFaults(ctx context.Context) (*env.ChaosRun, error) {
	o, err := f.build()
	if err != nil {
		return nil, err
	}
	// The policy comes from the project's manifest, never from the call. A
	// caller that could pick the levels could rank a lost commit as something
	// to ignore, which would turn this tool into one that always passes.
	return o.RunChaos(ctx, report.Configure(f.project.Manifest.Policy))
}
