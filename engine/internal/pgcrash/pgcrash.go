// Package pgcrash crashes a running Postgres underneath a concurrent write
// workload and then proves what the recovery did, rather than observing that
// it finished.
//
// The distinction is the whole package. "The database came back up" is what
// almost every crash test asserts, and it is true of a database that lost half
// its commits, true of a database that was never crashed, and true of a
// database whose fault was refused by the daemon. None of those is the claim
// anybody wants. The claims worth making are:
//
//   - every transaction the client was told was committed is still there
//   - nothing is there that the client never even tried to write
//   - the write ahead log was actually replayed, from the checkpoint the
//     control file named, to a position past the last flush the client saw
//   - the heap and its indexes still agree after the replay
//
// The first two need something the database cannot provide, because they are
// about what the database SAID, not about what it holds. That is the Ledger,
// written on the client side of the wire. The third needs the cluster's own
// record of itself, which is pg_controldata and the postmaster's log, read
// before and after. The fourth needs amcheck.
//
// Every one of those has a way to come out negative, and the package is
// arranged so that "I could not tell" is a third answer rather than a quiet
// pass: an unparsed control file, a log with no crash in it, an amcheck
// extension that is not installed and a reconciliation whose sets do not add
// up all produce an UNVERIFIED result, never a held one.
package pgcrash

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Runner runs commands inside the database's own container and reads its log.
//
// An interface rather than the Docker client, so that this package holds no
// opinion about where the database is running and so that the container it
// can reach is the one its caller already proved the environment owns.
type Runner interface {
	// Run executes argv and returns its output and exit code. A non-zero exit
	// is not an error: half of what is run here is a question whose answer is
	// the exit code.
	Run(ctx context.Context, argv []string) (stdout string, exitCode int, err error)
	// Logs returns what the container has written since a moment.
	Logs(ctx context.Context, since time.Time) (string, error)
}

// Options is one crash verification.
type Options struct {
	// URL is the database, reachable from this process.
	URL string
	// Runner reaches the database's container, for pg_controldata and the log.
	Runner Runner
	// DataDir is PGDATA inside that container.
	DataDir string
	// Workload configures the writers.
	Workload WorkloadOptions
	// WarmCommits is how many commits must be acknowledged before the fault
	// is injected, and WarmTimeout is how long to wait for them.
	WarmCommits int
	WarmTimeout time.Duration
	// Settle is how long to leave the fault in place before recovering.
	Settle time.Duration
	// ReadyTimeout is how long the database has to come back.
	ReadyTimeout time.Duration
	// Inject applies the fault and returns what it did. It is required: a
	// verification with nothing to inject is the liveness arm, and that is
	// spelled by passing an Inject that does nothing and setting
	// ExpectCrash to false, so that the expectation is written down.
	Inject func(ctx context.Context) (Injected, error)
	// Recover undoes a fault the database cannot undo for itself. A container
	// that was killed has to be started again; a backend that was killed does
	// not. Nil means the database recovers on its own.
	Recover func(ctx context.Context) error
	// ExpectCrash says whether this fault is supposed to crash the database.
	//
	// Written down rather than inferred from what happened, because inferring
	// it is how a recovery check comes to pass on a run where nothing crashed.
	// When it is true and the log carries no crash, the result is unverified
	// and says so.
	ExpectCrash bool
	// FaultName is what the report calls the fault.
	FaultName string
}

// defaults for the parts of Options a caller is allowed to leave empty.
const (
	defaultWarmCommits  = 200
	defaultWarmTimeout  = 30 * time.Second
	defaultSettle       = 3 * time.Second
	defaultReadyTimeout = 120 * time.Second
	readyPoll           = 250 * time.Millisecond
)

func (o Options) withDefaults() Options {
	if o.WarmCommits <= 0 {
		o.WarmCommits = defaultWarmCommits
	}
	if o.WarmTimeout <= 0 {
		o.WarmTimeout = defaultWarmTimeout
	}
	if o.Settle <= 0 {
		o.Settle = defaultSettle
	}
	if o.ReadyTimeout <= 0 {
		o.ReadyTimeout = defaultReadyTimeout
	}
	return o
}

// Result is everything one crash verification established.
type Result struct {
	// Fault is what was injected, and Evidence is what the injector said it
	// did at the moment it did it.
	Fault    string `json:"fault"`
	Evidence string `json:"evidence,omitempty"`
	// KilledSignal is the signal the container's main process died on, when
	// the fault killed the container rather than a process inside it.
	KilledSignal int `json:"killedSignal,omitempty"`
	// ExpectedCrash records what was asked for, so a reader can tell a
	// database that did not crash from one that was not meant to.
	ExpectedCrash bool `json:"expectedCrash"`
	// Before and After are the control file either side of the fault.
	Before Control `json:"before"`
	After  Control `json:"after"`
	// Recovery is what the postmaster's log said.
	Recovery Recovery `json:"recovery"`
	// Reconciliation is the ledger against the rows.
	Reconciliation Reconciliation `json:"reconciliation"`
	// Relations is the heap and index check.
	Relations Relations `json:"relations"`
	// FlushLSN is the highest flush position a writer saw after one of its own
	// acknowledged commits, in the text form Postgres prints.
	FlushLSN string `json:"flushLsn,omitempty"`
	// Downtime is how long the database was unreachable.
	Downtime time.Duration `json:"downtime"`
	// WriteErrors is how many writes failed while the fault was in place, and
	// LastWriteError is the most recent one. A crash with no write errors at
	// all is a crash the workload never noticed, which is worth seeing.
	WriteErrors    int    `json:"writeErrors"`
	LastWriteError string `json:"lastWriteError,omitempty"`
	// Problems are the things that went wrong: lost commits, phantoms, a log
	// with no replay in it.
	Problems []Problem `json:"problems,omitempty"`
	// Unverified are the things that could not be established either way.
	// They are not problems and they are not nothing, and keeping them apart
	// from Problems is the difference between "I checked and it is wrong" and
	// "I could not check".
	Unverified []Problem `json:"unverified,omitempty"`
}

// Injected is what a fault did, as the injector observed it at the instant of
// the act.
//
// The signal is carried separately from the prose because it is the only
// evidence a node level crash leaves. A database whose postmaster is killed
// cannot write the line that says its postmaster was killed, so a check that
// read only the log would report that the most complete crash it can cause was
// not a crash at all.
type Injected struct {
	// Evidence is what to print: the process that was killed, the exit code,
	// the network that was detached.
	Evidence string
	// KilledSignal is the signal the container's main process died on, or zero
	// when the fault left the container running.
	KilledSignal int
}

// Problem is one thing this run found, or one thing it could not look at.
type Problem struct {
	// Rule is the stable identifier a policy and a report key off.
	Rule string `json:"rule"`
	// Title is the one line summary.
	Title string `json:"title"`
	// Detail says what was measured.
	Detail string `json:"detail"`
	// Fix is what to do about it.
	Fix string `json:"fix"`
	// Count is how many things it covers.
	Count int `json:"count,omitempty"`
}

// The rules this package can report. They are constants because a report, a
// policy and a test all have to name the same string, and three string
// literals in three files is how they come to differ.
const (
	// RuleLostCommit is an acknowledged commit that is not there after
	// recovery. It is the durability failure.
	RuleLostCommit = "chaos.durability.lost_commit"
	// RulePhantomCommit is a row present that the client never attempted.
	RulePhantomCommit = "chaos.durability.phantom_commit"
	// RuleNoCrash is a fault that was supposed to crash the database and did
	// not. Everything downstream of it measured a database that never broke.
	RuleNoCrash = "chaos.recovery.no_crash"
	// RuleNoReplay is a crash whose log carries no replay.
	RuleNoReplay = "chaos.recovery.no_replay"
	// RuleReplayShort is a replay that stopped before the last position the
	// client saw the database flush.
	RuleReplayShort = "chaos.recovery.replay_short"
	// RuleNotInProduction is a cluster that did not come back to serving.
	RuleNotInProduction = "chaos.recovery.not_in_production"
	// RuleTimelineMoved is a timeline change, which crash recovery does not
	// do, so something other than a crash happened.
	RuleTimelineMoved = "chaos.recovery.timeline_moved"
	// RuleRelationDamaged is the heap and its index disagreeing afterwards.
	RuleRelationDamaged = "chaos.integrity.relation_damaged"
	// RuleChecksumsOff is data page checksums being off, so a torn page would
	// not have been detected. Unverified, never a failure: it is a fact about
	// the cluster's configuration and not about this change.
	RuleChecksumsOff = "chaos.integrity.checksums_off"
	// RuleAmcheckUnavailable is the amcheck extension not being installable.
	RuleAmcheckUnavailable = "chaos.integrity.amcheck_unavailable"
	// RuleInconsistentLedger is the reconciliation not adding up, which means
	// the instrument rather than the database is in question.
	RuleInconsistentLedger = "chaos.durability.inconsistent_ledger"
	// RuleControlUnreadable is pg_controldata not being readable, so the
	// before and after comparison could not be made.
	RuleControlUnreadable = "chaos.recovery.control_unreadable"
)

// Rules is every rule this package can report, for a test that wants to hold
// the documented set against the implemented one.
func Rules() []string {
	return []string{
		RuleLostCommit, RulePhantomCommit, RuleNoCrash, RuleNoReplay,
		RuleReplayShort, RuleNotInProduction, RuleTimelineMoved,
		RuleRelationDamaged, RuleChecksumsOff, RuleAmcheckUnavailable,
		RuleInconsistentLedger, RuleControlUnreadable,
	}
}

// Crashed reports whether this run has evidence that the database actually
// stopped uncleanly, from either of the two places that evidence can be.
//
// Two sources and not one, because neither covers the other. A process killed
// INSIDE a running container is recorded by the postmaster, which survives to
// write the line. A container whose main process is killed leaves no such
// line, because the writer of the line is what died, and the only record is
// the container's exit status. A check with one source is blind to one of the
// two faults it advertises.
func (r Result) Crashed() bool { return r.Recovery.Crashed || r.KilledSignal > 0 }

// CrashSignal is the signal the database died on, from whichever source
// recorded it.
func (r Result) CrashSignal() int {
	if r.Recovery.Signal > 0 {
		return r.Recovery.Signal
	}
	return r.KilledSignal
}

// Held reports whether recovery was correct.
//
// Unverified entries do not make it false, and they are why Held is never read
// on its own: a caller asks Held and Verified, and a run that is held and not
// verified is reported as unverified.
func (r Result) Held() bool { return len(r.Problems) == 0 }

// Verified reports whether the run established what it set out to.
func (r Result) Verified() bool { return len(r.Unverified) == 0 }

// Verify runs one crash verification from end to end.
//
// The order is fixed and every step of it is load bearing:
//
//	prepare the table and checkpoint it, so the schema is not itself at risk
//	read the control file, so there is a before
//	start the writers and wait for real acknowledged commits, not for a clock
//	inject the fault and record the instant, which windows the log
//	let it settle, then stop the writers so nothing races the read
//	recover, if the fault needs recovering
//	wait for the database, timing how long it was gone
//	read the control file and the log, and reconcile the ledger
//	check the heap against its index
func Verify(ctx context.Context, opts Options) (Result, error) {
	opts = opts.withDefaults()
	if opts.Inject == nil {
		return Result{}, errors.New("pgcrash: no fault to inject, so there would be nothing to recover from")
	}
	if opts.Runner == nil {
		return Result{}, errors.New("pgcrash: no way to reach the database's container, so no recovery evidence could be read")
	}
	if strings.TrimSpace(opts.DataDir) == "" {
		return Result{}, errors.New("pgcrash: no data directory, so the control file could not be found")
	}
	res := Result{Fault: opts.FaultName, ExpectedCrash: opts.ExpectCrash}

	if err := Prepare(ctx, opts.URL, opts.Workload); err != nil {
		return res, err
	}
	wl := opts.Workload
	wl.URL = opts.URL

	before, beforeErr := readControl(ctx, opts)
	res.Before = before

	w, err := StartWorkload(ctx, wl)
	if err != nil {
		return res, err
	}
	// Stopped here as well as below, so that a failure between the two does
	// not leave eight writers hammering an environment that is being torn
	// down. Stop is idempotent for exactly this.
	defer w.Stop()

	warm := w.WaitForCommits(ctx, opts.WarmCommits, opts.WarmTimeout)

	faultAt := time.Now()
	injected, err := opts.Inject(ctx)
	if err != nil {
		return res, err
	}
	res.Evidence, res.KilledSignal = injected.Evidence, injected.KilledSignal

	sleep(ctx, opts.Settle)
	w.Stop()
	res.WriteErrors, res.LastWriteError = w.Errors()

	if opts.Recover != nil {
		if err := opts.Recover(ctx); err != nil {
			return res, err
		}
	}
	downtime, err := waitReady(ctx, opts, faultAt)
	res.Downtime = downtime
	if err != nil {
		return res, aferrors.Wrap(err, aferrors.AFCHS006,
			"timeout", opts.ReadyTimeout.String(), "fault", opts.FaultName,
			"detail", err.Error())
	}

	after, afterErr := readControl(ctx, opts)
	res.After = after

	if log, err := opts.Runner.Logs(ctx, faultAt); err == nil {
		res.Recovery = ParseRecovery(log)
	} else {
		res.Unverified = append(res.Unverified, Problem{
			Rule:   RuleNoReplay,
			Title:  "The database's log could not be read",
			Detail: "Nothing can be said about whether the write ahead log replayed: " + err.Error(),
			Fix:    "Check that the engine can read the database container's log, then run the fault again.",
		})
	}

	present, err := readPresent(ctx, opts)
	if err != nil {
		return res, err
	}
	res.Reconciliation = w.Ledger().Reconcile(present)
	if lsn := w.Ledger().FlushLSN(); lsn > 0 {
		res.FlushLSN = FormatLSN(lsn)
	}
	res.Relations = checkRelations(ctx, opts)

	res.judge(opts, beforeErr, afterErr, warm)
	return res, nil
}

// judge turns what was measured into problems and unverified entries.
//
// Separate from Verify and taking only values, so that every branch in it can
// be driven from a table without a database. A judgement that can only be
// exercised by crashing something is a judgement most of whose branches have
// never run.
func (r *Result) judge(opts Options, beforeErr, afterErr error, warmCommits int) {
	if beforeErr != nil || afterErr != nil {
		which := "after the fault"
		err := afterErr
		if beforeErr != nil {
			which, err = "before the fault", beforeErr
		}
		r.Unverified = append(r.Unverified, Problem{
			Rule:   RuleControlUnreadable,
			Title:  "The control file could not be read " + which,
			Detail: err.Error(),
			Fix:    "Check that pg_controldata is present in the database image and that the data directory is the one the manifest names.",
		})
	}

	if opts.ExpectCrash {
		switch {
		case !r.Crashed():
			r.Unverified = append(r.Unverified, Problem{
				Rule:  RuleNoCrash,
				Title: "The fault did not crash the database",
				Detail: "The fault was applied and the database's log carries no process dying on a signal, so everything measured after it " +
					"describes a database that never broke.",
				Fix: "Aim the fault at a process the postmaster supervises, and confirm the run is reaching the database's own container.",
			})
		case !r.Recovery.Unclean:
			r.Unverified = append(r.Unverified, Problem{
				Rule:  RuleNoReplay,
				Title: "The database came back without recording an unclean shutdown",
				Detail: "The log carries a process killed by signal " + strconv.Itoa(r.CrashSignal()) +
					" and no line saying the cluster was not shut down properly, so it is not established that recovery ran.",
				Fix: "Read the database's log around the fault. A clean shutdown does no recovery, and a recovery check that passes on one has not checked anything.",
			})
		case !r.Recovery.Replayed() && !r.Recovery.RedoNotRequired:
			r.Unverified = append(r.Unverified, Problem{
				Rule:   RuleNoReplay,
				Title:  "Recovery ran and the log does not say how far it replayed",
				Detail: "There is no redo start or redo end in the log, so the extent of the replay is unknown.",
				Fix:    "Raise the database's log level so the startup process records the redo positions, then run the fault again.",
			})
		case r.Recovery.RedoNotRequired && warmCommits > 0:
			r.Unverified = append(r.Unverified, Problem{
				Rule:  RuleNoReplay,
				Title: "The database said redo was not required",
				Detail: fmt.Sprintf("%d commits were acknowledged before the fault and the cluster replayed nothing, "+
					"so this run did not exercise replay.", warmCommits),
				Fix: "Let the workload run past a checkpoint before injecting the fault, so there is write ahead log to replay.",
			})
		}
	}

	if opts.ExpectCrash && r.Crashed() && r.Recovery.Replayed() {
		start, errStart := ParseLSN(r.Recovery.RedoStart)
		end, errEnd := ParseLSN(r.Recovery.RedoEnd)
		switch {
		case errStart != nil || errEnd != nil:
			r.Unverified = append(r.Unverified, Problem{
				Rule:   RuleNoReplay,
				Title:  "The replay positions in the log are not readable",
				Detail: fmt.Sprintf("redo started at %q and ended at %q", r.Recovery.RedoStart, r.Recovery.RedoEnd),
				Fix:    "Report this: the engine read a redo position it cannot parse, which is a defect in the engine rather than in the database.",
			})
		case end < start:
			r.Problems = append(r.Problems, Problem{
				Rule:   RuleNoReplay,
				Title:  "Recovery finished before where it started",
				Detail: fmt.Sprintf("redo started at %s and ended at %s", r.Recovery.RedoStart, r.Recovery.RedoEnd),
				Fix:    "This is a database defect. Keep the log and the control file from this run.",
			})
		default:
			if r.Before.RedoLSN != "" {
				if want, err := ParseLSN(r.Before.RedoLSN); err == nil && start < want {
					r.Problems = append(r.Problems, Problem{
						Rule:  RuleNoReplay,
						Title: "Recovery started before the checkpoint the control file named",
						Detail: fmt.Sprintf("the control file said recovery would start at %s and the log says it started at %s",
							r.Before.RedoLSN, r.Recovery.RedoStart),
						Fix: "This is a database defect. Keep the log and the control file from this run.",
					})
				}
			}
			if r.FlushLSN != "" {
				if flushed, err := ParseLSN(r.FlushLSN); err == nil && end < flushed {
					r.Problems = append(r.Problems, Problem{
						Rule:  RuleReplayShort,
						Title: "Recovery replayed less than the client saw flushed",
						Detail: fmt.Sprintf("a writer read the flush position %s from this database before the fault, and recovery stopped at %s",
							r.FlushLSN, r.Recovery.RedoEnd),
						Fix: "This is a durability defect. Keep the log, the control file and the flush position from this run.",
					})
				}
			}
		}
	}

	if r.After.State != "" && !r.After.InProduction() {
		r.Problems = append(r.Problems, Problem{
			Rule:   RuleNotInProduction,
			Title:  "The cluster is not serving after recovery",
			Detail: "pg_controldata reports the cluster state as " + r.After.State + " rather than in production.",
			Fix:    "Read the database's log for how far startup reached before it stopped.",
		})
	}
	if r.Before.TimeLine != 0 && r.After.TimeLine != 0 && r.Before.TimeLine != r.After.TimeLine {
		r.Problems = append(r.Problems, Problem{
			Rule:  RuleTimelineMoved,
			Title: "The timeline changed across a crash",
			Detail: fmt.Sprintf("the timeline was %d before the fault and is %d after it, and crash recovery does not change it",
				r.Before.TimeLine, r.After.TimeLine),
			Fix: "Something other than crash recovery happened to this cluster. Check for a restore or a promotion in the run.",
		})
	}

	rec := r.Reconciliation
	if rec.LostCount > 0 {
		r.Problems = append(r.Problems, Problem{
			Rule:  RuleLostCommit,
			Count: rec.LostCount,
			Title: fmt.Sprintf("%d transactions the client was told were committed are gone", rec.LostCount),
			Detail: fmt.Sprintf("%d of %d acknowledged commits are absent after recovery, for example %s. "+
				"A commit that returned success and is not there is a durability failure, whatever the cause.",
				rec.LostCount, rec.Acknowledged, sampleOf(rec.LostSample)),
			Fix: "Check synchronous_commit and fsync on this database. Both are on by default, and with either off a crash loses acknowledged commits by design.",
		})
	}
	if rec.PhantomCount > 0 {
		r.Problems = append(r.Problems, Problem{
			Rule:  RulePhantomCommit,
			Count: rec.PhantomCount,
			Title: fmt.Sprintf("%d rows are present that no client ever wrote", rec.PhantomCount),
			Detail: fmt.Sprintf("%d rows carry identifiers this run never attempted, for example %s.",
				rec.PhantomCount, sampleOf(rec.PhantomSample)),
			Fix: "Check that nothing else is writing to this schema. If nothing is, keep this database: it has invented data.",
		})
	}
	if !rec.Consistent {
		r.Unverified = append(r.Unverified, Problem{
			Rule:  RuleInconsistentLedger,
			Title: "The reconciliation does not add up",
			Detail: fmt.Sprintf("%d acknowledged, %d in flight and %d rows present cannot be reconciled, so nothing is concluded from them",
				rec.Acknowledged, rec.Unresolved, rec.Present),
			Fix: "Report this: the engine's own bookkeeping disagrees with itself, and a verdict drawn from it would be meaningless.",
		})
	}

	rel := r.Relations
	switch {
	case rel.Checked && !rel.Agreed:
		r.Problems = append(r.Problems, Problem{
			Rule:  RuleRelationDamaged,
			Title: "The heap and its index disagree after recovery",
			Detail: fmt.Sprintf("a sequential scan counted %d rows and an index only scan counted %d, and amcheck said %q",
				rel.HeapRows, rel.IndexRows, rel.Amcheck),
			Fix: "Keep this database. A heap and an index that disagree after replay is corruption, not configuration.",
		})
	case !rel.Checked:
		r.Unverified = append(r.Unverified, Problem{
			Rule:   RuleAmcheckUnavailable,
			Title:  "The relations could not be checked after recovery",
			Detail: rel.Why,
			Fix:    "Install the amcheck extension in the database image so a damaged index is found rather than assumed absent.",
		})
	}

	if r.After.Raw != "" && !r.After.ChecksumsEnabled() {
		r.Unverified = append(r.Unverified, Problem{
			Rule:  RuleChecksumsOff,
			Title: "Data page checksums are off on this cluster",
			Detail: "pg_controldata reports checksum version 0, so a page torn by the crash would be read back as data rather than " +
				"reported, and this run cannot say a page was not torn.",
			Fix: "Initialise the database with data checksums on, or turn them on with pg_checksums, if torn page detection matters here.",
		})
	}
}

// sampleOf renders a handful of identifiers for a finding.
func sampleOf(ids []int64) string {
	if len(ids) == 0 {
		return "none recorded"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ", ")
}

// readControl runs pg_controldata inside the database's container.
func readControl(ctx context.Context, opts Options) (Control, error) {
	out, code, err := opts.Runner.Run(ctx, []string{"pg_controldata", "-D", opts.DataDir})
	if err != nil {
		return Control{}, err
	}
	if code != 0 {
		return Control{}, fmt.Errorf("pgcrash: pg_controldata exited %d: %s", code, strings.TrimSpace(out))
	}
	return ParseControl(out)
}

// waitReady polls until the database accepts a connection, and returns how
// long it was unreachable.
//
// A connection rather than pg_isready alone, because pg_isready answers about
// the postmaster's socket and a cluster still replaying its write ahead log
// answers that socket to say it is in recovery. What the next step needs is a
// database that will answer a query.
func waitReady(ctx context.Context, opts Options, since time.Time) (time.Duration, error) {
	deadline := time.Now().Add(opts.ReadyTimeout)
	var last error
	for {
		conn, err := pgx.Connect(ctx, opts.URL)
		if err == nil {
			var one int
			err = conn.QueryRow(ctx, "SELECT 1").Scan(&one)
			_ = conn.Close(context.WithoutCancel(ctx))
			if err == nil {
				return time.Since(since), nil
			}
		}
		last = err
		if time.Now().After(deadline) {
			return time.Since(since), fmt.Errorf("the database did not answer a query within %s: %w", opts.ReadyTimeout, last)
		}
		select {
		case <-ctx.Done():
			return time.Since(since), ctx.Err()
		case <-time.After(readyPoll):
		}
	}
}

// readPresent reads back every identifier the table holds.
func readPresent(ctx context.Context, opts Options) (map[int64]struct{}, error) {
	conn, err := pgx.Connect(ctx, opts.URL)
	if err != nil {
		return nil, fmt.Errorf("pgcrash: connecting to read the rows back: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	wl := opts.Workload.withDefaults()
	rows, err := conn.Query(ctx, "SELECT id FROM "+wl.Qualified())
	if err != nil {
		return nil, fmt.Errorf("pgcrash: reading the rows back: %w", err)
	}
	defer rows.Close()
	present := map[int64]struct{}{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("pgcrash: reading a row back: %w", err)
		}
		present[id] = struct{}{}
	}
	return present, rows.Err()
}

// sleep waits, and returns early if the context ends.
func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
