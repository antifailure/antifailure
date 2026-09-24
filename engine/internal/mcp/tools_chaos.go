package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/gate"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Fault injection, which until this file existed was reachable from `af chaos`
// and from nowhere else.
//
// It got here the way the SQL workload did and was caught by the instrument
// that catches it: tools/paritycheck reported RunChaos as declared at
// engine/internal/env/chaos.go and reachable from the command line and from
// nothing else. Two pull requests were each green and the pair was red, because
// the one that brought the capability was judged against a tree that did not
// yet have the gate.
//
// WHAT IS PRESERVED HERE RATHER THAN FLATTENED, because a boolean would be
// easier and would throw away the answer:
//
//	held and verified are TWO fields and the second is not the negation of the
//	first. Held says nothing was found to be wrong. Verified says the run
//	established what it set out to. A run that is held and not verified has not
//	passed, it has not looked, and that distinction is the whole feature.
//
//	A fault that was applied and changed nothing is REFUSED rather than reported
//	as survived, because every assertion after it would be measuring a system
//	that never broke.
//
//	A fault that was injected and not undone is reported on its own, because the
//	environment the next thing meets is still broken.
//
// The decision itself is not made here. gate.ChaosHolds is the one the command
// line reads, lifted into engine/internal/gate so that a model and a person get
// the same answer rather than two implementations that agree until one is
// edited.

// chaosOutcome is what the injection produced.
type chaosOutcome struct {
	Run *env.ChaosRun
}

// runChaos injects the manifest's declared faults into this environment.
//
// A function value so the tool can be built against a fake in tests. The real
// one is the orchestrator, which is the same code path af chaos takes.
type runChaos func(ctx context.Context) (chaosOutcome, error)

// The two lists a result carries are bounded, because they grow with the
// manifest rather than with the run.
const (
	maxFaultsReported   = 20
	maxChaosNoteLength  = 400
	maxEvidenceReported = 400
	// A manifest may declare a hundred invariants and twenty faults may be
	// reported, so the arm is bounded PER FAULT for the same reason the faults
	// are: two thousand entries is not a result an agent can read. Named apart
	// from tools_explore.go's maxInvariantsReported, which bounds the same
	// manifest list on a different tool and at a different number, because one
	// name for two bounds is how the two come to be read as one.
	maxChaosInvariantsReported = 20
)

// newInjectFaultsTool builds inject_declared_faults.
func newInjectFaultsTool(p *Project, eng *Engine, run runChaos) *Tool {
	return &Tool{
		Name:  "inject_declared_faults",
		Title: "Break this environment on purpose and prove the recovery",
		Description: "Answer whether this change survives the failures it will actually meet. " +
			"It injects the faults the manifest's chaos block declares into the environment " +
			"running for this branch, ONE AT A TIME, each undone before the next begins, and " +
			"reads what the system did about each one. " +
			"The faults are real and nothing is simulated: a process is killed with SIGKILL, a " +
			"container is stopped, frozen or detached from the network, a data directory is made " +
			"read only. Nothing is aimed anywhere but at the containers this environment created, " +
			"proved from the labels the runtime stamped at create time and proved again from the " +
			"daemon at the instant of the act, and the egress sidecar is refused whatever a fault " +
			"asks for, because a fault that could stop the thing deciding where the environment " +
			"may connect would be a way out rather than an outage. " +
			"Around a fault aimed at the database the durability proof runs: writers commit while " +
			"the fault lands, and afterwards every commit the client was told was committed must " +
			"still be there and nothing may be there that no client ever wrote. " +
			"The result carries HELD and VERIFIED separately. Held says nothing was found to be " +
			"wrong. Verified says the run established what it set out to. A run that is held and " +
			"not verified has NOT passed, it has not looked, and it is reported INCONCLUSIVE. " +
			"A fault that was applied and changed nothing is refused rather than reported as " +
			"survived. " +
			"There is no argument that can choose which faults run, weaken one, or turn the " +
			"durability proof off: the manifest decides all of it. " +
			"Bring an environment up first with start_environment. " +
			"This takes as long as its faults and their recovery windows, so it returns a run_id " +
			"immediately: poll it with get_rehearsal_run.",
		Input: &Schema{
			Type:     "object",
			Required: []string{"project_id"},
			Properties: map[string]*Schema{
				"project_id":      projectIDSchema(),
				"idempotency_key": idempotencyKeySchema(),
				"hypothesis": {
					Type: "string", MaxLength: 2000,
					Description: "Optional. What you expect breaking this to show, in your own " +
						"words. Recorded with the run so the outcome can be read against the " +
						"expectation. It is never executed and never changes which faults are " +
						"injected or what is measured.",
				},
			},
		},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}
			hypothesis, _ := args["hypothesis"].(string)

			return eng.Submit(call, "inject_declared_faults", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					return injectFaults(ctx, p, eng, run, runID, hypothesis)
				})
		},
	}
}

// injectFaults runs the declared faults and reports what they proved.
func injectFaults(
	ctx context.Context, p *Project, eng *Engine, run runChaos,
	runID, hypothesis string,
) (string, *ResultBody, *Fault) {
	if eng.Cancelled(ctx, runID) {
		return "", nil, faultf(FaultRunNotCancellable, "This run was cancelled before it started.")
	}

	// The two states that are facts about the PROJECT rather than about the
	// host are answered here, as finished runs carrying INCONCLUSIVE. Reaching
	// the orchestrator for either would turn a configuration answer into the
	// retryable fault below, and retrying will never make a manifest declare a
	// fault or make a remote runtime local.
	if why := chaosCannotRun(p.Manifest); why != "" {
		return report.VerdictUnverified, &ResultBody{
			Summary: why,
			Detail:  &chaosDoc{Faults: []chaosFaultDoc{}, Notes: []string{why}},
		}, nil
	}

	eng.Phase(ctx, runID, "breaking the environment on purpose")

	out, err := run(ctx)
	if err != nil {
		// Nothing was broken, so nothing was proved about the recovery.
		// Reporting a pass because no finding was raised would be reporting an
		// experiment that did not happen as one that found nothing.
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The faults could not be injected, so this says nothing about how the " +
				"change survives them. They are injected into an environment rather than " +
				"creating one, so the usual cause is that nothing is running for this " +
				"branch: bring one up with start_environment. The other cause is a " +
				"container runtime this machine cannot reach.",
			Retryable: true,
			wrapped:   err,
		}
	}
	if out.Run == nil {
		// The orchestrator answers nil for a manifest with no chaos block. The
		// check above should have caught it, so this is the belt rather than
		// the braces, and it must not read as a pass.
		return report.VerdictUnverified, &ResultBody{
			Summary: "No fault was injected, so nothing was proved about the recovery.",
			Detail:  &chaosDoc{Faults: []chaosFaultDoc{}},
		}, nil
	}

	eng.Phase(ctx, runID, "reading what the recovery did")

	held, verified := gate.ChaosHolds(out.Run.Findings)
	native := chaosVerdict(out.Run, held, verified)
	body := &ResultBody{
		Findings: boundFindings(out.Run.Findings),
		Metrics:  chaosMetrics(out.Run),
		Evidence: chaosEvidence(out.Run),
		Detail:   describeChaos(out.Run, held, verified),
	}
	body.Summary = chaosSummary(out.Run, held, verified, native, hypothesis)
	return native, body, nil
}

// chaosCannotRun names the reason this project cannot be broken on purpose, or
// returns empty.
//
// Both reasons are the manifest's rather than the machine's, which is why they
// are answered as a finished INCONCLUSIVE run and not as a retryable failure.
func chaosCannotRun(m *schema.Manifest) string {
	if m == nil || m.Chaos == nil || !m.Chaos.Enabled {
		return "This project declares no chaos block, or declares one that is off, so no " +
			"fault was injected and this says nothing about how the change survives one. " +
			"Add a chaos block to antifailure.yaml naming the failures this system is " +
			"expected to survive."
	}
	if len(m.Chaos.Faults) == 0 {
		return "This project's chaos block is on and declares no faults, so there was " +
			"nothing to inject. A block with no faults is not a system that survives " +
			"everything; it is a check that examined nothing."
	}
	if m.Runtime != nil && m.Runtime.Provider != "" && m.Runtime.Provider != schema.RuntimeLocal {
		return fmt.Sprintf(
			"Fault injection runs only against the local container runtime and this project "+
				"declares %s, so nothing was broken and nothing was proved. The faults act on "+
				"containers this environment created, proved from the labels the runtime "+
				"stamped on them, and no such proof exists for another runtime.",
			neutralize(string(m.Runtime.Provider), 64))
	}
	return ""
}

// chaosVerdict maps a run onto the engine's own vocabulary.
//
// The order is the order of usefulness and it matches what af chaos exits on.
// Something found to be wrong outranks something the run could not establish,
// because the first is a fact about the change and the second is a fact about
// the rehearsal. A run that is held and not verified is INCONCLUSIVE and never
// a pass: it has not looked.
func chaosVerdict(run *env.ChaosRun, held, verified bool) string {
	if len(run.Report.Faults) == 0 {
		// Nothing ran. Every finding list is empty and held is true, which is
		// the green over nothing this product exists to stop.
		//
		// This covers a skipped run too, and deliberately does not test
		// Report.Skipped separately. engine/internal/env/chaos.go sets that
		// field on exactly one path, the one where the block declares no
		// faults, and returns immediately, so a run can never carry both a
		// skip reason and a fault. A second condition for it would be a branch
		// no input can reach and no mutation can kill, which is worse than no
		// branch: it reads as a case somebody handled.
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

func chaosMetrics(run *env.ChaosRun) []Metric {
	zero := 0.0
	injected, undone, refused, leftInPlace := 0, 0, 0, 0
	var acknowledged, lost, phantom, inFlight, proofs int
	var longestOutage int64
	for _, f := range run.Report.Faults {
		// Injected first. A fault whose undo failed carries an Error too, and
		// counting Error first called a fault still applied to the environment
		// "refused", which is the opposite fact.
		switch {
		case f.Injected:
			injected++
		case f.Error != "":
			refused++
		}
		if f.Undone {
			undone++
		}
		if f.Injected && !f.Undone {
			leftInPlace++
		}
		if f.Recovery == nil {
			continue
		}
		proofs++
		acknowledged += f.Recovery.Acknowledged
		lost += f.Recovery.Lost
		phantom += f.Recovery.Phantom
		inFlight += f.Recovery.InFlightLanded
		if f.Recovery.DowntimeMs > longestOutage {
			longestOutage = f.Recovery.DowntimeMs
		}
	}

	metrics := []Metric{
		{Name: "faults_declared", Value: float64(len(run.Report.Faults)), Unit: "faults"},
		{Name: "faults_injected", Value: float64(injected), Unit: "faults"},
		{Name: "faults_undone", Value: float64(undone), Unit: "faults"},
		// Reported always, including as zero. A fault that did not go in is
		// a declared claim that was not established, and the injected count
		// alone cannot show it. It is NOT a statement about the other faults:
		// one refused as unsafe never touched the environment, so what the
		// others measured stands. The summary says which kind it was.
		{
			Name: "faults_refused", Value: float64(refused), Unit: "faults",
			Threshold: &zero, Breached: refused > 0,
		},
		{
			Name: "faults_left_in_place", Value: float64(leftInPlace), Unit: "faults",
			Threshold: &zero, Breached: leftInPlace > 0,
		},
	}
	if proofs == 0 {
		return metrics
	}
	return append(metrics,
		Metric{Name: "durability_proofs_run", Value: float64(proofs), Unit: "proofs"},
		Metric{Name: "commits_acknowledged", Value: float64(acknowledged), Unit: "commits"},
		// The two that decide whether the database kept its word. A lost
		// commit is one the client was TOLD was committed and is gone; a
		// phantom is a row no client ever wrote.
		Metric{
			Name: "commits_lost", Value: float64(lost), Unit: "commits",
			Threshold: &zero, Breached: lost > 0,
		},
		Metric{
			Name: "rows_phantom", Value: float64(phantom), Unit: "rows",
			Threshold: &zero, Breached: phantom > 0,
		},
		Metric{Name: "commits_in_flight_that_landed", Value: float64(inFlight), Unit: "commits"},
		// The LONGEST single outage, never the sum of them.
		//
		// The faults run one at a time and each is undone before the next
		// begins, so their outages are separate events. Adding them produces a
		// number that is arithmetically true and describes an outage that never
		// happened: 4000 reads as one four second gap when it was two gaps of
		// two seconds, and those are different facts about a system. It is the
		// same defect as reporting zero commits lost for a proof that never
		// ran, which this file is careful not to do, and it was found in review
		// by lane-chaos rather than by any test here.
		//
		// A caller that wants the total can add the per fault numbers, which
		// the detail carries. A caller handed a total cannot recover the parts.
		Metric{Name: "longest_database_outage", Value: float64(longestOutage), Unit: "ms"},
	)
}

// chaosEvidence points at where the full measurement lives, and never carries
// it.
func chaosEvidence(run *env.ChaosRun) []Evidence {
	evidence := []Evidence{{
		URI: "af://chaos/faults", Kind: "command",
		Note: "Run af chaos -o json for every fault's full evidence, including the control " +
			"file positions and the log window this result summarises.",
	}}
	for _, f := range run.Report.Faults {
		if f.Injected && !f.Undone {
			evidence = append(evidence, Evidence{
				URI: "af://chaos/left-in-place", Kind: "environment_state",
				Note: "A fault was injected and its undo did not run, so this environment is " +
					"still broken. Tear it down with teardown_environment and build it again: " +
					"anything measured after that fault was measured against a broken system.",
			})
			break
		}
	}
	return evidence
}

// chaosDoc is the fault evidence a result carries.
type chaosDoc struct {
	// Held and Verified are two answers and the second is not the negation of
	// the first. A caller that reads only Held will report a run that never
	// looked as a success, which is the failure this whole feature exists to
	// catch in somebody else's system.
	Held     bool `json:"held"`
	Verified bool `json:"verified"`
	// Skipped is why no fault was injected, when none was.
	Skipped     string          `json:"skipped,omitempty"`
	FaultsTotal int             `json:"faults_total"`
	Faults      []chaosFaultDoc `json:"faults"`
	Notes       []string        `json:"notes,omitempty"`
}

// chaosFaultDoc is one fault and what it proved.
type chaosFaultDoc struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	// Injected says the fault was applied at all, Undone says its undo ran,
	// and Error says why it could not be applied. A fault that was applied and
	// not undone leaves an environment the next thing to run will meet.
	Injected bool   `json:"injected"`
	Undone   bool   `json:"undone"`
	Error    string `json:"error,omitempty"`
	// Evidence is what the injector said it did at the moment it did it, which
	// is what backs the claim that the fault landed rather than being asked
	// for.
	Evidence string `json:"evidence,omitempty"`
	// InPlaceMs is how long the fault was measured to be in place, from the
	// injection returning to its undo beginning, and HoldDeclaredMs is the
	// hold the manifest asked for. InPlace says the same in one sentence.
	//
	// They exist because duration_ms alone was read as the length of the
	// fault. It was zero for every fault outside a durability proof, whatever
	// happened, and an agent that saw a five second network partition
	// reported as lasting 0 ms rightly doubted the cut had lasted at all.
	InPlaceMs      int64  `json:"in_place_ms"`
	HoldDeclaredMs int64  `json:"hold_declared_ms"`
	InPlace        string `json:"in_place,omitempty"`
	// DurationMs is the whole step: the declared wait before the fault, the
	// fault, its undo and any verification. It is longer than InPlaceMs.
	DurationMs int64             `json:"duration_ms"`
	Recovery   *chaosRecoveryDoc `json:"recovery,omitempty"`
	// Invariants is what the project's own rules about its own data said
	// before the fault and after the recovery. Absent when the manifest
	// declares none, and absent when no durability proof ran around this
	// fault.
	Invariants []chaosInvariantDoc `json:"invariants,omitempty"`
}

// chaosInvariantDoc is one of the manifest's own invariants, asked on both
// sides of the fault.
//
// The two sentences, and deliberately not the violating rows. Those rows come
// out of the customer's database and this crosses into an agent's context: a
// finding is something to act on and a row is something somebody else wrote.
// What an agent needs is which rule changed and which way, and `af chaos -o
// json` holds the rows for a person who wants them.
type chaosInvariantDoc struct {
	Name string `json:"name"`
	// Before and After are what the statement said on each side, in the same
	// words the terminal and the pull request comment use. "not asked" is one
	// of those words and it is never "held", because a rule nobody could ask
	// is not a rule that held.
	Before string `json:"before_the_fault"`
	After  string `json:"after_the_recovery"`
	// Attributable is true only when the rule held before the fault and does
	// not hold after the recovery, which is the one answer about it this run
	// can put on the fault. A rule that was already violated is reported with
	// this false, so an agent cannot read an inherited defect as one the
	// change caused.
	Attributable bool `json:"attributable_to_the_fault"`
}

// chaosRecoveryDoc is what the durability proof established around one fault.
type chaosRecoveryDoc struct {
	// Verified reports whether this proof established what it set out to. A
	// proof that is not verified has not passed: it has not looked.
	Verified bool `json:"verified"`
	Crashed  bool `json:"crashed"`
	Signal   int  `json:"killed_by_signal,omitempty"`
	Replayed bool `json:"write_ahead_log_replayed"`
	// RedoStart and RedoEnd are how far replay reached, empty when there was
	// no replay to read.
	RedoStart string `json:"redo_start,omitempty"`
	RedoEnd   string `json:"redo_end,omitempty"`
	// StateBefore and StateAfter are the cluster state from its own control
	// file either side of the fault.
	StateBefore string `json:"cluster_state_before,omitempty"`
	StateAfter  string `json:"cluster_state_after,omitempty"`
	// Acknowledged is how many commits the client was TOLD were committed,
	// Lost is how many of those are gone, Phantom is how many rows no client
	// ever wrote, and InFlightLanded is how many commits that were in flight
	// at the crash did land. Lost and Phantom are the two that decide whether
	// the database kept its word.
	Acknowledged   int `json:"commits_acknowledged"`
	Lost           int `json:"commits_lost"`
	Phantom        int `json:"rows_phantom"`
	InFlightLanded int `json:"commits_in_flight_that_landed"`
	// HeapRows and IndexRows are two independent counts of the same rows, and
	// Amcheck is what the index verifier said. They disagree when an index is
	// damaged.
	HeapRows  int64  `json:"heap_rows"`
	IndexRows int64  `json:"index_rows"`
	Amcheck   string `json:"amcheck,omitempty"`
	// ChecksumsOn reports whether a torn page would have been detected at all.
	ChecksumsOn bool `json:"data_checksums_enabled"`
	// DowntimeMs is how long a probe beside the fault saw the database not
	// answer. It is zero both when the database never stopped answering and
	// when it did for less than the probe's resolution, so Unreachable says
	// which, and Unreachable carries the sentence the terminal prints.
	DowntimeMs        int64  `json:"database_unreachable_ms"`
	BecameUnreachable bool   `json:"database_became_unreachable"`
	Recovered         bool   `json:"database_answered_again"`
	ProbeIntervalMs   int64  `json:"probe_interval_ms"`
	Unreachable       string `json:"database_unreachable"`
}

// describeChaos renders what the faults did, bounded and neutralised.
//
// A fault's name comes from the manifest and its evidence and error come from a
// container in the candidate environment, so all of them are repository or
// runtime content read by a model. They are neutralised and clipped rather than
// checked against safeIdentifier, which would refuse an evidence line for
// containing a space.
func describeChaos(run *env.ChaosRun, held, verified bool) *chaosDoc {
	doc := &chaosDoc{
		Held: held, Verified: verified,
		Skipped:     neutralize(run.Report.Skipped, maxChaosNoteLength),
		FaultsTotal: len(run.Report.Faults),
		Faults:      []chaosFaultDoc{},
	}

	faults := run.Report.Faults
	if len(faults) > maxFaultsReported {
		faults = faults[:maxFaultsReported]
		doc.Notes = append(doc.Notes, fmt.Sprintf(
			"%d faults ran and the first %d are shown. Read the rest with af chaos -o json.",
			doc.FaultsTotal, maxFaultsReported))
	}
	for _, f := range faults {
		entry := chaosFaultDoc{
			Name: neutralize(f.Name, 128), Kind: neutralize(f.Kind, 64),
			Target: neutralize(f.Target, 128), Injected: f.Injected, Undone: f.Undone,
			Error: neutralize(f.Error, maxChaosNoteLength),
			// The evidence is what backs the claim, so it travels even when
			// the fault failed: a refusal with no reason attached is a refusal
			// nobody can act on.
			Evidence: neutralize(f.Evidence, maxEvidenceReported), DurationMs: f.DurationMs,
			InPlaceMs: f.InPlaceMs, HoldDeclaredMs: f.HoldDeclaredMs, InPlace: f.InPlaceSays(),
		}
		if f.Recovery != nil {
			entry.Recovery = describeRecovery(f.Recovery)
		}
		entry.Invariants = describeInvariants(f.Invariants)
		if len(f.Invariants) > maxChaosInvariantsReported {
			doc.Notes = append(doc.Notes, fmt.Sprintf(
				"Fault %s asked %d invariants and the first %d are shown. Read the rest with af chaos -o json.",
				neutralize(f.Name, 128), len(f.Invariants), maxChaosInvariantsReported))
		}
		doc.Faults = append(doc.Faults, entry)
	}

	if !verified {
		doc.Notes = append(doc.Notes,
			"Something in this run could not be established, so it is reported as "+
				"inconclusive rather than as a pass. Read the findings: a rule that means "+
				"the run could not look is a different fact from one that means the system "+
				"was wrong.")
	}
	for _, f := range run.Report.Faults {
		if f.Injected && !f.Undone {
			doc.Notes = append(doc.Notes,
				"A fault was injected and its undo did not run, so this environment is "+
					"still broken and anything measured after it was measured against a "+
					"broken system.")
			break
		}
	}
	return doc
}

// describeInvariants carries the invariant arm across the boundary.
//
// Every string is neutralised, because an invariant's name and the error a
// statement raised both come from outside the engine, and this is read by a
// model.
func describeInvariants(invs []report.ChaosInvariant) []chaosInvariantDoc {
	if len(invs) == 0 {
		return nil
	}
	if len(invs) > maxChaosInvariantsReported {
		invs = invs[:maxChaosInvariantsReported]
	}
	out := make([]chaosInvariantDoc, 0, len(invs))
	for _, i := range invs {
		out = append(out, chaosInvariantDoc{
			Name:         neutralize(i.Name, 128),
			Before:       neutralize(i.BeforeSays(), maxChaosNoteLength),
			After:        neutralize(i.AfterSays(), maxChaosNoteLength),
			Attributable: i.Attributable(),
		})
	}
	return out
}

func describeRecovery(rec *report.ChaosRecovery) *chaosRecoveryDoc {
	return &chaosRecoveryDoc{
		Verified: rec.Verified, Crashed: rec.Crashed, Signal: rec.Signal,
		Replayed: rec.Replayed,
		// The positions come out of the database's own log and control file,
		// so they are neutralised like everything else that crossed that
		// boundary, and they are short by construction.
		RedoStart: neutralize(rec.RedoStart, 64), RedoEnd: neutralize(rec.RedoEnd, 64),
		StateBefore: neutralize(rec.StateBefore, 64), StateAfter: neutralize(rec.StateAfter, 64),
		Acknowledged: rec.Acknowledged, Lost: rec.Lost, Phantom: rec.Phantom,
		InFlightLanded: rec.InFlightLanded,
		HeapRows:       rec.HeapRows, IndexRows: rec.IndexRows,
		Amcheck:     neutralize(rec.Amcheck, 200),
		ChecksumsOn: rec.ChecksumsOn, DowntimeMs: rec.DowntimeMs,
		BecameUnreachable: rec.Unreachable, Recovered: rec.Recovered,
		ProbeIntervalMs: rec.ProbeIntervalMs, Unreachable: rec.UnreachableSays(),
	}
}

func chaosSummary(
	run *env.ChaosRun, held, verified bool, native, hypothesis string,
) string {
	var b strings.Builder

	if run.Report.Skipped != "" {
		fmt.Fprintf(&b, "No fault was injected: %s. ", neutralize(run.Report.Skipped, 200))
	} else {
		injected, unsafe, failed, leftInPlace := 0, 0, 0, 0
		for _, f := range run.Report.Faults {
			switch {
			case f.Injected && f.Undone:
				injected++
			case f.Injected:
				leftInPlace++
			case f.Refused:
				unsafe++
			case f.Error != "":
				failed++
			}
		}
		fmt.Fprintf(&b, "%d declared %s: %d injected and undone, %d refused. ",
			len(run.Report.Faults), plural(len(run.Report.Faults), "fault", "faults"),
			injected, unsafe+failed)
		// Two different sentences for two different facts. A refusal as
		// unsafe is decided before the fault acts, so the environment was
		// never touched and the other faults' results stand; saying otherwise
		// told a model to discard a durability proof that ran after it.
		if unsafe > 0 {
			fmt.Fprintf(&b, "%d %s refused as unsafe before touching anything, so what it was "+
				"declared to establish was not established, and it changed nothing the other faults "+
				"measured. ", unsafe, plural(unsafe, "fault was", "faults were"))
		}
		if failed > 0 {
			b.WriteString("A fault that would not go in means nothing measured after it " +
				"says anything, because the system never broke. ")
		}
		if leftInPlace > 0 {
			fmt.Fprintf(&b, "%d %s injected and not undone, so this environment is still "+
				"broken. ", leftInPlace, plural(leftInPlace, "fault was", "faults were"))
		}
		b.WriteString(describeDurability(run))
	}

	switch {
	case native == report.VerdictFail:
		b.WriteString("Something was found to be wrong. ")
	case !verified:
		b.WriteString("Nothing was found to be wrong AND the run did not establish what it " +
			"set out to, so this is inconclusive rather than a pass: held is true and " +
			"verified is false. ")
	case native == report.VerdictUnverified:
		b.WriteString("Nothing ran, so nothing was proved. ")
	default:
		b.WriteString("Every declared fault landed, every one was undone, and the recovery " +
			"proved what it set out to. ")
	}

	if hypothesis != "" {
		fmt.Fprintf(&b, "Your stated hypothesis, unevaluated: %q.", neutralize(hypothesis, 500))
	}
	return strings.TrimSpace(b.String())
}

// describeDurability is the one sentence a reader wants about the database.
func describeDurability(run *env.ChaosRun) string {
	var acknowledged, lost, phantom, proofs int
	for _, f := range run.Report.Faults {
		if f.Recovery == nil {
			continue
		}
		proofs++
		acknowledged += f.Recovery.Acknowledged
		lost += f.Recovery.Lost
		phantom += f.Recovery.Phantom
	}
	if proofs == 0 {
		return "No durability proof ran, so this says nothing about whether a commit " +
			"survived. "
	}
	return fmt.Sprintf(
		"Across %d %s, %d commits were acknowledged, %d of them are gone and %d rows are "+
			"present that no client wrote. ",
		proofs, plural(proofs, "durability proof", "durability proofs"),
		acknowledged, lost, phantom)
}

// runChaosThrough injects the declared faults through the orchestrator.
//
// The environment, the faults and the recovery thresholds are all decided here
// from the manifest and the checkout, and no argument in the tool schema
// reaches any of them. There is nothing a caller can say that makes a fault
// gentler, skips one, or turns the durability proof off.
func (f *orchestratorFactory) runChaos(ctx context.Context) (chaosOutcome, error) {
	o, err := f.build()
	if err != nil {
		return chaosOutcome{}, err
	}
	// The project's own resolved policy, which is the one af ci reads. A
	// policy assembled here would be a second answer to a question the
	// manifest already settled.
	run, err := o.RunChaos(ctx, f.project.Gate)
	if err != nil {
		return chaosOutcome{}, err
	}
	return chaosOutcome{Run: run}, nil
}
