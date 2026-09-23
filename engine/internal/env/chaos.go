package env

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/fault"
	"github.com/antifailure/antifailure/engine/internal/gate"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// RunChaos injects the manifest's declared faults into this environment and
// proves what the recovery did.
//
// It runs against an environment that is already up, and against that
// environment's own containers. There is no mode that points it at anything
// else: the injector is constructed with this orchestrator's environment id
// and refuses every container whose labels do not carry it, so the guarantee
// the manifest's prose makes is the guarantee the code enforces rather than a
// convention the caller is trusted to keep.
//
// One fault at a time, each undone before the next begins. Two faults at once
// would make a finding say something about a combination nobody declared, and
// the undo of the first would race the injection of the second.
func (o *Orchestrator) RunChaos(ctx context.Context, gate report.Policy) (*ChaosRun, error) {
	m := o.opts.Manifest
	if m == nil || m.Chaos == nil || !m.Chaos.Enabled {
		return nil, nil
	}
	run := &ChaosRun{}
	if len(m.Chaos.Faults) == 0 {
		run.Report.Skipped = "the chaos block is enabled and declares no faults"
		return run, nil
	}
	if provider := runtimeProvider(m); provider != schema.RuntimeLocal {
		return nil, aferrors.Coded(aferrors.AFCHS007, "provider", string(provider))
	}

	cli, err := dockerutil.Client()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()

	inj, err := fault.New(cli, o.envID)
	if err != nil {
		return nil, err
	}

	s, err := o.openReading(ctx)
	if err != nil {
		return nil, err
	}
	defer s.close()
	url, err := o.branchURL(ctx, s)
	if err != nil {
		return nil, err
	}

	for _, declared := range m.Chaos.Faults {
		entry, proof := o.runOneFault(ctx, inj, url.Reveal(), declared, m.Chaos.CrashRecovery)
		run.Report.Faults = append(run.Report.Faults, entry)
		run.Findings = append(run.Findings, ChaosFindings(entry, proof, gate)...)
	}
	return run, nil
}

// ChaosRun is what the fault injection produced: the section a reader sees and
// the findings the gate reads.
//
// The two are built together and returned together, rather than the findings
// being derived from the section afterwards, because the section crosses a
// JSON boundary into a pull request comment and deliberately does not carry
// the full crash proof. Deriving findings from what survived that boundary
// would silently drop every detail the boundary drops.
type ChaosRun struct {
	Report   report.Chaos
	Findings []report.Finding
}

// runtimeProvider is which runtime this manifest asks for.
func runtimeProvider(m *schema.Manifest) schema.RuntimeProvider {
	if m.Runtime == nil || m.Runtime.Provider == "" {
		return schema.RuntimeLocal
	}
	return m.Runtime.Provider
}

// runOneFault injects one fault and reads what happened.
//
// The undo is registered the moment the injection returns and is deferred, so
// a panic, a cancelled context or a failed verification all still leave the
// environment the way they found it. A fault held open past the end of its own
// step is a fault the next step is measuring without knowing it.
//
// The results are named because the deferred line below writes the duration
// into the entry the caller receives. With unnamed results it wrote into a
// local the return statement had already copied, so every fault that was not
// a durability proof reported a duration of zero: a network partition held
// for its full five seconds read as one that lasted no time at all, to the
// agent that asked for it.
func (o *Orchestrator) runOneFault(
	ctx context.Context, inj *fault.Injector, url string,
	declared schema.Fault, cr *schema.CrashRecovery,
) (entry report.ChaosFault, proof *pgcrash.Result) {
	started := time.Now()
	f := faultFrom(declared)
	hold, _ := time.ParseDuration(declared.Hold)
	entry = report.ChaosFault{
		Name: declared.Name, Kind: string(declared.Kind), Target: f.Target.String(),
		HoldDeclaredMs: hold.Milliseconds(),
	}
	defer func() { entry.DurationMs = time.Since(started).Milliseconds() }()

	o.progress(fmt.Sprintf("chaos: %s, %s on %s", declared.Name, declared.Kind, f.Target))

	// The crash proof drives its own injection, because what it measures has
	// to bracket the fault: the control file before, the log window from the
	// instant of the kill, the writers stopped after it. A fault that is not
	// aimed at the database, or whose recovery proof is switched off, is
	// injected here instead and held for its own duration.
	if wantsCrashProof(declared, cr) {
		return o.crashProof(ctx, inj, url, declared, cr, entry, started)
	}

	// The declared wait before the fault is honoured here too. It used to be
	// read only by the durability proof, so a fault aimed at a service went in
	// the instant the step began whatever the manifest said.
	after, _ := time.ParseDuration(declared.After)
	sleepFor(ctx, after)

	in, err := inj.Inject(ctx, f)
	if err != nil {
		entry.Error, entry.Refused = err.Error(), refusedAsUnsafe(err)
		return entry, nil
	}
	applied := time.Now()
	entry.Injected, entry.Evidence = true, in.Evidence
	sleepFor(ctx, hold)
	// Measured from the moment the injection returned to the moment its undo
	// begins, which is the only span in which the fault is known to be in
	// place: the injector's own timestamp is taken before it acts. Read off
	// the clock rather than copied from the manifest, so a run cancelled
	// halfway through its hold says how long the fault really lasted.
	entry.InPlaceMs = time.Since(applied).Milliseconds()
	if err := in.Undo(context.WithoutCancel(ctx)); err != nil {
		entry.Error = err.Error()
		return entry, nil
	}
	entry.Undone = true
	return entry, nil
}

// sentence ends an error's text with a full stop unless it already ends a
// sentence, so the next sentence of a finding does not run straight on from
// it. The catalog's messages carry no closing punctuation, and joining one
// onto "It was turned down" produced "every other container on this machine
// with it It was turned down", on the frame the demo film is built around.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}

// refusedAsUnsafe reports whether err is the injector turning a fault down
// because its effect would reach past this environment, which it decides
// before it writes anything.
//
// Read from the catalog code rather than from the message, so rewording the
// sentence cannot move a fault from one side of the line to the other, and
// the code has one meaning for this to rely on: AF-CHS-005 is raised only
// where the check runs before the fault acts. The one place that raised it
// after acting, a read only fault that changed nothing, raises AF-CHS-004,
// which is the code that says exactly that.
func refusedAsUnsafe(err error) bool {
	return stderrors.Is(err, aferrors.Coded(aferrors.AFCHS005))
}

// wantsCrashProof reports whether the durability proof runs around this fault.
//
// Only a fault aimed at the database, and only when the block that runs it is
// on. A proof run around a fault aimed at a service would be asserting that
// killing the application did not lose a commit the application never made.
func wantsCrashProof(f schema.Fault, cr *schema.CrashRecovery) bool {
	if f.Target == schema.FaultTargetService {
		return false
	}
	return cr != nil && cr.Enabled != nil && *cr.Enabled
}

// crashProof runs the durability proof around one fault.
func (o *Orchestrator) crashProof(
	ctx context.Context, inj *fault.Injector, url string,
	declared schema.Fault, cr *schema.CrashRecovery,
	entry report.ChaosFault, started time.Time,
) (report.ChaosFault, *pgcrash.Result) {
	f := faultFrom(declared)
	sh, err := inj.Shell(ctx, f.Target)
	if err != nil {
		entry.Error = err.Error()
		return entry, nil
	}
	dataDir := dataDirOf(ctx, sh)

	var injection *fault.Injection
	after, _ := time.ParseDuration(declared.After)
	hold, _ := time.ParseDuration(declared.Hold)
	timeout, _ := time.ParseDuration(cr.RecoveryTimeout)

	res, err := pgcrash.Verify(ctx, pgcrash.Options{
		URL:     url,
		Runner:  shellRunner{sh},
		DataDir: dataDir,
		Workload: pgcrash.WorkloadOptions{
			Writers:           cr.Writers,
			SynchronousCommit: cr.SynchronousCommit,
		},
		// The manifest's own invariants, asked either side of the fault. The
		// proof's own assertions are all about a schema of the engine's, for
		// the reason pgcrash's workload gives, and that reason stops applying
		// once the writers have stopped and the database is answering again.
		// This is the arm that asks whether the user's data still means what
		// the user says it means. A manifest with none leaves it empty and
		// nothing about it runs.
		Invariants: o.opts.Manifest.Invariants,
		WarmCommits: cr.CommitsBeforeFault,
		// The declared wait is a floor on the warm up, so a manifest that asks
		// for a long soak gets one and one that asks for none still waits for
		// the commits the proof needs. This comment used to say so while only
		// the timeout below read it, and the timeout is a ceiling: a freeze
		// declared after 5s went in 0.6s into its run, measured.
		WarmFloor:    after,
		WarmTimeout:  timeout + after,
		Settle:       hold,
		ReadyTimeout: timeout,
		ExpectCrash:  fault.Kind(declared.Kind).ExpectsCrash(),
		FaultName:    declared.Name,
		Inject: func(ctx context.Context) (pgcrash.Injected, error) {
			in, err := inj.Inject(ctx, f)
			if err != nil {
				return pgcrash.Injected{}, err
			}
			injection = in
			return pgcrash.Injected{Evidence: in.Evidence, KilledSignal: in.KilledSignal}, nil
		},
		Recover: func(ctx context.Context) error {
			return injection.Undo(ctx)
		},
	})
	// The undo runs whatever happened, including when Verify returned an
	// error before it reached its own Recover. Undo is idempotent, so the
	// ordinary path calls it twice and the second call does nothing.
	//
	// A non nil injection means the fault WENT IN, whatever Verify said
	// afterwards, so Injected is set here rather than only on the path where
	// Verify succeeded. Without it an undo that failed after a Verify error
	// left Injected false and Undone false, which no reader could tell from a
	// fault that never went in, and the finding that says this environment is
	// still broken never fired.
	if injection != nil {
		entry.Injected = true
		if undoErr := injection.Undo(context.WithoutCancel(ctx)); undoErr != nil {
			if entry.Error == "" {
				entry.Error = undoErr.Error()
			}
		} else {
			entry.Undone = true
		}
	}
	// Measured by the proof between its injection and its recovery, and read
	// on the error path too: a proof that failed after the fault went in
	// still left it in place for as long as it did.
	entry.InPlaceMs = res.FaultInPlace.Milliseconds()
	entry.DurationMs = time.Since(started).Milliseconds()
	entry.Invariants = chaosInvariantsOf(res.Invariants, o.opts.Redactor)
	if err != nil {
		if entry.Error == "" {
			entry.Error = err.Error()
			entry.Refused = refusedAsUnsafe(err)
		}
		entry.Evidence = res.Evidence
		// The proof is returned on this path too, but ONLY when the invariant
		// arm has something in it. What the user's own rules said about the
		// database does not depend on the rest of the proof finishing: the
		// invariants were asked before the fault, and whether they could be
		// asked afterwards is itself the answer. A database that never came
		// back fails here, and a run that returned nothing would have lost the
		// one finding that says so.
		//
		// Conditional rather than unconditional, so a manifest that declares
		// no invariants gets exactly what it got before. res carries no
		// judgement on this path, since judge never ran, except the log read
		// failure that Verify records as it goes, and surfacing that for a
		// project with no invariants would be a finding this change has no
		// business adding.
		if len(res.Invariants) > 0 {
			return entry, &res
		}
		return entry, nil
	}
	entry.Injected, entry.Evidence = true, res.Evidence
	entry.Recovery = recoveryOf(res)
	return entry, &res
}

// chaosInvariantsOf carries the invariant arm into the shape the report and
// the terminal read.
//
// A translation rather than an embedding, for the reason recoveryOf gives: the
// report crosses a JSON boundary into a pull request comment. The errors are
// put through the redactor here, which is where engine/internal/env does it
// for the invariants af test runs, so a connection failure that quoted a URL
// cannot carry a password across that boundary.
func chaosInvariantsOf(checks []pgcrash.InvariantCheck, red *redact.Redactor) []report.ChaosInvariant {
	if len(checks) == 0 {
		return nil
	}
	out := make([]report.ChaosInvariant, 0, len(checks))
	for _, c := range checks {
		entry := report.ChaosInvariant{
			Name:        c.Name,
			Description: c.Description,
			BeforeHeld:  c.Before.Held,
			AfterHeld:   c.After.Held,
			Columns:     c.After.Columns,
			Rows:        c.After.Rows,
			More:        c.After.More,
		}
		if c.Before.Error != "" {
			entry.BeforeError = red.String(c.Before.Error)
		}
		if c.After.Error != "" {
			entry.AfterError = red.String(c.After.Error)
		}
		out = append(out, entry)
	}
	return out
}

// recoveryOf carries a crash proof into the shape the report reads.
//
// Exported nowhere and deliberately a translation rather than an embedding,
// because the report crosses a JSON boundary into a pull request comment and
// nothing that crosses it should carry a raw control file or a log.
func recoveryOf(res pgcrash.Result) *report.ChaosRecovery {
	rec := res.Reconciliation
	return &report.ChaosRecovery{
		Crashed:         res.Crashed(),
		Signal:          res.CrashSignal(),
		Replayed:        res.Recovery.Replayed(),
		RedoStart:       res.Recovery.RedoStart,
		RedoEnd:         replayedTo(res),
		StateBefore:     res.Before.State,
		StateAfter:      res.After.State,
		Acknowledged:    rec.Acknowledged,
		Present:         rec.Present,
		Lost:            rec.LostCount,
		Phantom:         rec.PhantomCount,
		InFlightLanded:  rec.UnresolvedLanded,
		HeapRows:        res.Relations.HeapRows,
		IndexRows:       res.Relations.IndexRows,
		Amcheck:         res.Relations.Amcheck,
		ChecksumsOn:     res.After.ChecksumsEnabled(),
		DowntimeMs:      res.Downtime.Milliseconds(),
		Unreachable:     res.Availability.Unreachable,
		Recovered:       res.Availability.Recovered,
		ProbeIntervalMs: res.Availability.Interval.Milliseconds(),
		Verified:        res.Verified(),
	}
}

// replayedTo is how far replay reached, for the report's "from X to Y".
//
// The end of replay when the proof established it, and otherwise the log's
// "redo done at". That line is the START of the last record replayed, so it
// is short of the true end by one record, and it is used only when nothing
// better was read, rather than printing no end at all.
func replayedTo(res pgcrash.Result) string {
	if res.ReplayEnd != "" {
		return res.ReplayEnd
	}
	return res.Recovery.RedoEnd
}

// ChaosFindings turns one fault's outcome into the findings the gate reads.
//
// Exported and taking only values, the way InvariantResults is, so that a
// suite driving a real crash against a real database exercises the production
// translation rather than writing its own beside the assertion. A translation
// written beside the assertion agrees with itself whatever the product does.
//
// The two levels are separate on purpose. A lost commit is a failure at
// ChaosFailure, which defaults to fail. A run that could not establish its
// claim is at ChaosUnverified, which defaults to warn: it is a real fact
// somebody has to see and it is not evidence that the change broke anything.
//
// THREE FACTS ARRIVE WITH AN ERROR, and they used to share one finding. A
// fault that went in and did not come out is still applied, so everything
// measured after it is suspect. A fault the injector refused as unsafe never
// touched anything, so nothing else in the run is affected. A fault that tried
// to go in and failed is the case the refused rule was written for. Reading
// Error first gave all three the refused rule's sentence, which told a reader
// that a durability proof run AFTER a refused disk fill meant nothing, on the
// screen that showed it, and reported a fault still applied to this
// environment as one that "was not applied". So the order below is the order
// of how much each one damages: left in place, then refused as unsafe, then
// could not be injected.
//
// The fault's own findings and the proof's are appended rather than the first
// returning instead of the second. They used to be one switch whose refusal
// arms returned, which was right while a refused fault always meant an absent
// proof. It stopped being right when the proof grew an arm that survives its
// own failure: a database that does not come back makes Verify return an
// error AND is the single most important moment to say that the project's own
// invariants were never asked. A return there would have dropped it.
func ChaosFindings(f report.ChaosFault, proof *pgcrash.Result, gate report.Policy) []report.Finding {
	where := "fault " + f.Name
	out := faultFindings(f, gate, where)
	if proof == nil {
		return out
	}
	for _, p := range proof.Problems {
		out = append(out, report.Finding{
			Rule: p.Rule, Level: gate.ChaosFailure, Title: p.Title,
			Detail: p.Detail, Fix: p.Fix, Count: p.Count, Where: where,
		})
	}
	for _, p := range proof.Unverified {
		out = append(out, report.Finding{
			Rule: p.Rule, Level: gate.ChaosUnverified, Title: p.Title,
			Detail: p.Detail, Fix: p.Fix, Count: p.Count, Where: where,
		})
	}
	return out
}

// faultFindings is what went wrong with the fault itself, as opposed to with
// the recovery it was measured across.
func faultFindings(f report.ChaosFault, gate report.Policy, where string) []report.Finding {
	var out []report.Finding
	switch {
	case f.Injected && !f.Undone:
		detail := fmt.Sprintf("%s was injected and its undo did not run, so this environment is still broken.", where)
		if f.Error != "" {
			detail = fmt.Sprintf("%s was injected and its undo failed, so this environment is still broken: %s", where, sentence(f.Error))
		}
		out = append(out, report.Finding{
			Rule:   RuleFaultNotUndone,
			Level:  gate.ChaosUnverified,
			Title:  "A fault was left in place",
			Detail: detail,
			Fix:    "Tear the environment down and build it again. Anything that ran after this fault was measured against a broken environment.",
			Where:  where,
		})
	case f.Refused:
		// Unverified rather than informational, deliberately. The manifest
		// declared this fault, so it declared a claim, and the claim was not
		// established: reporting that below warn would be reporting an
		// unestablished claim as nothing to see. It fires on every run only
		// while the manifest declares a fault this environment cannot hold,
		// which is a manifest a person can fix, so the warn is a to do and not
		// noise. What it must NOT do is reach past its own fault.
		return []report.Finding{{
			Rule:  RuleFaultUnsafe,
			Level: gate.ChaosUnverified,
			Title: "A fault was refused before it touched anything",
			Detail: fmt.Sprintf("%s was not applied: %s It was turned down by the guard that keeps a fault "+
				"inside this environment, before it acted, so it left this environment as it was and changed "+
				"nothing the other faults in this run measured. What this fault was declared to establish "+
				"was not established.", where, sentence(f.Error)),
			Fix: "Read the refusal, which names what would have reached past this environment. Change the fault " +
				"or the environment so the effect stays inside it, or remove the fault: while it is declared, " +
				"every run reports its claim as not established.",
			Where: where,
		}}
	case f.Error != "":
		return []report.Finding{{
			Rule:   RuleFaultRefused,
			Level:  gate.ChaosUnverified,
			Title:  "A fault could not be injected",
			Detail: fmt.Sprintf("%s was not applied: %s Nothing measured after it means anything.", where, sentence(f.Error)),
			Fix:    "Read what the container said, and correct the fault or the environment it is aimed at.",
			Where:  where,
		}}
	}
	return out
}

// The three rules this package RAISES, as opposed to the ones pgcrash owns.
// They are about the fault rather than about the recovery: a fault that tried
// to go in and failed, a fault the injector refused as unsafe before it acted,
// and a fault that would not come out.
//
// Aliases rather than literals. The classification that decides what a chaos
// finding MEANS lives in engine/internal/gate, so that the command line and the
// MCP server read one answer, and a rule name spelled once there and once here
// is two strings that agree until somebody edits one.
const (
	RuleFaultRefused   = gate.RuleFaultRefused
	RuleFaultUnsafe    = gate.RuleFaultUnsafe
	RuleFaultNotUndone = gate.RuleFaultNotUndone
)

// faultFrom turns a manifest fault into the one the injector runs.
func faultFrom(f schema.Fault) fault.Fault {
	target := fault.Target{Role: fault.RoleDatabase}
	if f.Target == schema.FaultTargetService {
		target = fault.Target{Role: fault.RoleService, Service: f.Service}
	}
	return fault.Fault{
		Name:          f.Name,
		Kind:          fault.Kind(f.Kind),
		Target:        target,
		Process:       f.Process,
		Path:          pathFor(f),
		HeadroomBytes: f.HeadroomBytes,
		MaxFillBytes:  f.MaxFillBytes,
	}
}

// pathFor is the directory a storage fault acts on.
//
// Empty for every other kind, because the faults that do not touch the
// filesystem refuse a path rather than ignoring one, and handing them the data
// directory would make the refusal fire on every manifest.
func pathFor(f schema.Fault) string {
	switch f.Kind {
	case schema.FaultReadOnlyData, schema.FaultDiskFill:
		return defaultDataDir
	default:
		return ""
	}
}

// defaultDataDir is where the local database provider puts PGDATA.
//
// The container's own PGDATA is read first and this is the fallback, so a
// branch started by a future provider with a different layout still gets a
// directory rather than an empty string that would make every storage fault
// refuse.
const defaultDataDir = "/var/lib/antifailure/pgdata"

// dataDirOf asks the container where its data directory is.
func dataDirOf(ctx context.Context, sh *fault.Shell) string {
	out, err := sh.Run(ctx, []string{"/bin/sh", "-c", "printf %s \"$PGDATA\""})
	if err != nil || out.ExitCode != 0 {
		return defaultDataDir
	}
	if dir := strings.TrimSpace(out.Stdout); dir != "" {
		return dir
	}
	return defaultDataDir
}

// shellRunner adapts the ownership guarded shell to what pgcrash reads
// evidence through.
type shellRunner struct{ sh *fault.Shell }

func (r shellRunner) Run(ctx context.Context, argv []string) (string, int, error) {
	out, err := r.sh.Run(ctx, argv)
	return out.Stdout, out.ExitCode, err
}

func (r shellRunner) Logs(ctx context.Context, since time.Time) (string, error) {
	return r.sh.Logs(ctx, since)
}

// sleepFor waits, and returns early if the run is cancelled.
func sleepFor(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
