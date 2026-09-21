package env

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/fault"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
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
func (o *Orchestrator) runOneFault(
	ctx context.Context, inj *fault.Injector, url string,
	declared schema.Fault, cr *schema.CrashRecovery,
) (report.ChaosFault, *pgcrash.Result) {
	started := time.Now()
	f := faultFrom(declared)
	entry := report.ChaosFault{
		Name: declared.Name, Kind: string(declared.Kind), Target: f.Target.String(),
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

	in, err := inj.Inject(ctx, f)
	if err != nil {
		entry.Error = err.Error()
		return entry, nil
	}
	entry.Injected, entry.Evidence = true, in.Evidence
	hold, _ := time.ParseDuration(declared.Hold)
	sleepFor(ctx, hold)
	if err := in.Undo(context.WithoutCancel(ctx)); err != nil {
		entry.Error = err.Error()
		return entry, nil
	}
	entry.Undone = true
	return entry, nil
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
		WarmCommits: cr.CommitsBeforeFault,
		// The declared wait is a floor on the warm up as well as a wait, so a
		// manifest that asks for a long soak gets one and one that asks for
		// none still waits for the commits the proof needs.
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
	if injection != nil {
		if undoErr := injection.Undo(context.WithoutCancel(ctx)); undoErr != nil && entry.Error == "" {
			entry.Error = undoErr.Error()
		} else {
			entry.Undone = true
		}
	}
	entry.DurationMs = time.Since(started).Milliseconds()
	if err != nil {
		if entry.Error == "" {
			entry.Error = err.Error()
		}
		entry.Evidence = res.Evidence
		return entry, nil
	}
	entry.Injected, entry.Evidence = true, res.Evidence
	entry.Recovery = recoveryOf(res)
	return entry, &res
}

// recoveryOf carries a crash proof into the shape the report reads.
//
// Exported nowhere and deliberately a translation rather than an embedding,
// because the report crosses a JSON boundary into a pull request comment and
// nothing that crosses it should carry a raw control file or a log.
func recoveryOf(res pgcrash.Result) *report.ChaosRecovery {
	rec := res.Reconciliation
	return &report.ChaosRecovery{
		Crashed:        res.Crashed(),
		Signal:         res.CrashSignal(),
		Replayed:       res.Recovery.Replayed(),
		RedoStart:      res.Recovery.RedoStart,
		RedoEnd:        res.Recovery.RedoEnd,
		StateBefore:    res.Before.State,
		StateAfter:     res.After.State,
		Acknowledged:   rec.Acknowledged,
		Present:        rec.Present,
		Lost:           rec.LostCount,
		Phantom:        rec.PhantomCount,
		InFlightLanded: rec.UnresolvedLanded,
		HeapRows:       res.Relations.HeapRows,
		IndexRows:      res.Relations.IndexRows,
		Amcheck:        res.Relations.Amcheck,
		ChecksumsOn:    res.After.ChecksumsEnabled(),
		DowntimeMs:     res.Downtime.Milliseconds(),
		Verified:       res.Verified(),
	}
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
func ChaosFindings(f report.ChaosFault, proof *pgcrash.Result, gate report.Policy) []report.Finding {
	where := "fault " + f.Name
	var out []report.Finding
	if f.Error != "" {
		return []report.Finding{{
			Rule:   RuleFaultRefused,
			Level:  gate.ChaosUnverified,
			Title:  "A fault could not be injected",
			Detail: fmt.Sprintf("%s was not applied: %s Nothing measured after it means anything.", where, f.Error),
			Fix:    "Read what the container said, and correct the fault or the environment it is aimed at.",
			Where:  where,
		}}
	}
	if f.Injected && !f.Undone {
		out = append(out, report.Finding{
			Rule:   RuleFaultNotUndone,
			Level:  gate.ChaosUnverified,
			Title:  "A fault was left in place",
			Detail: fmt.Sprintf("%s was injected and its undo did not run, so this environment is still broken.", where),
			Fix:    "Tear the environment down and build it again. Anything that ran after this fault was measured against a broken environment.",
			Where:  where,
		})
	}
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

// The two rules this package owns, as opposed to the ones pgcrash owns. They
// are about the fault rather than about the recovery: a fault that would not
// go in, and a fault that would not come out.
const (
	RuleFaultRefused   = "chaos.fault.refused"
	RuleFaultNotUndone = "chaos.fault.not_undone"
)

// Holds reads a run's findings for the two answers a caller needs.
//
// Two answers rather than one, and the second is not the negation of the
// first. Held says nothing was found to be wrong. Verified says the run
// established what it set out to. A run that is held and not verified has not
// passed, it has not looked, and collapsing the two is the exact defect this
// whole feature exists to catch in somebody else's system.
//
// It lives beside the run rather than on a surface because there is more than
// one surface now. A run that fails at a terminal and passes through an agent
// would be worse than one that fails in both places, and two copies of this
// loop is all it would take.
func (r *ChaosRun) Holds() (held, verified bool) {
	held, verified = true, true
	for _, f := range r.Findings {
		if f.Level == report.LevelFail {
			held = false
		}
		// Read from the rule and not from the level, deliberately. A project
		// that set chaos_unverified to ignore has chosen not to be stopped by
		// an unverified run; it has not thereby made the run verified, and
		// every surface says so either way.
		if UnverifiedRule(f.Rule) {
			verified = false
		}
	}
	return held, verified
}

// UnverifiedRule reports whether a rule means "I could not look" rather than
// "I looked and it is wrong".
//
// A list rather than a level, because the level is a policy choice: a project
// that raised chaos_unverified to fail has not thereby turned an unverified
// run into a verified one.
//
// The names come from the packages that own them rather than from string
// literals. A literal here and a constant there drift silently, and the drift
// would read as a run that could not look reporting itself as one that did.
func UnverifiedRule(rule string) bool {
	switch rule {
	case pgcrash.RuleNoCrash, pgcrash.RuleNoReplay, pgcrash.RuleControlUnreadable,
		pgcrash.RuleAmcheckUnavailable, pgcrash.RuleChecksumsOff,
		pgcrash.RuleInconsistentLedger, RuleFaultRefused, RuleFaultNotUndone:
		return true
	}
	return false
}

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
