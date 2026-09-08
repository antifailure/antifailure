package conformance

// Copy on write is the distinguishing commercial claim of every cloud database
// in this category, and until this file existed nothing could refuse it.
//
// Caps.CopyOnWrite said "a branch shares storage with its golden, and
// therefore branch time is independent of database size", five providers
// declared a value for it, and the database suite never read the field. The
// datastore suite beside it read the field twice and both reads were about
// self consistency: copy on write without branching, copy on write without a
// golden. Neither asks whether the claim is TRUE. So a provider could declare
// CopyOnWrite alongside Branching and Golden, copy every byte on every branch,
// and pass both suites, which is the defect this repository keeps finding in
// its own instruments: a declaration nothing can refuse.
//
// WHAT IS MEASURED, AND WHY IT IS TIME RATHER THAN BYTES.
//
// The claim's customer visible meaning is a sentence about seconds: the same
// command takes the same time on a hundred rows and on a terabyte. That is the
// sentence the plan sells the wave on, so it is the sentence the suite checks.
// Asking a provider how many bytes a branch occupies would be checking a
// second declaration with the first, and it would need an interface method
// several real services cannot answer honestly: Aurora reports clone storage
// with lag, Neon reports logical size rather than shared extents. Wall clock
// needs no new method and no cloud account, and a provider cannot answer it
// with an opinion.
//
// THE SHAPE OF THE MEASUREMENT.
//
// Two goldens are built, one small and one large, and the large one is large
// because the suite put ballast in it through the Mask callback the provider
// must call anyway. Each is branched several times, alternating between them,
// and the MINIMUM per size is taken. Minimum rather than mean because load
// only ever makes an operation slower: on a machine running fifteen other
// lanes the mean measures the machine and the minimum measures the provider.
// Alternating rather than running all of one size then all of the other
// because load drifts, and a drift that lands on one arm reads exactly like
// the effect being measured.
//
// THE ASSERTION IS TWO SIDED ON PURPOSE, AND THE TWO SIDES ARE COMPLEMENTARY.
//
//	CopyOnWrite true   requires  grown <= allowance
//	CopyOnWrite false  requires  grown >  allowance
//
// where grown is the extra branch time the extra bytes cost. Exactly one of
// those holds for any measurement whatsoever, so the suite refuses one of the
// two possible declarations every single time it runs. There is no reading of
// the stopwatch under which both declarations pass, which is the property a
// check needs before anybody should believe a green one.
//
// The false side matters as much as the true side. A provider that understates
// its own capability is lying about what the buyer is paying for in the other
// direction, it makes the wave's comparison table wrong, and a check that only
// fires one way is half an instrument.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The default sizes, and how they were chosen.
//
// Discriminating power here is physical rather than statistical: a provider
// that copies must move the bytes, so the signal is the copy time and there is
// no sample count that manufactures it from a delta too small to see. The
// large size is therefore set from the FASTEST copy anybody could plausibly
// be doing, not the slowest, because the failure that matters is calling a
// fast copy "shared storage".
//
// A local NVMe file copy runs at roughly two gigabytes a second at the very
// best. One gibibyte of ballast therefore costs an honest copier at least half
// a second even on the best hardware this suite will ever meet, and on the
// machine this was written on, at load twenty six with a hundred foreign
// containers, Postgres CREATE DATABASE moved it far slower than that. Against
// an allowance whose absolute floor is a quarter of a second, that is the
// margin: the fastest plausible honest copy is still twice the allowance, and
// the realistic one is an order of magnitude past it.
//
// The small size is not zero. A golden with no ballast at all measures the
// provider's fixed cost plus whatever a bare Postgres cluster costs to
// duplicate, and the difference between the two arms would then include the
// base cluster as well as the ballast. Eight mebibytes is small enough to
// vanish against a gibibyte and large enough that the small arm is a real
// branch of a real golden rather than a special case.
//
// Both are overridable, because a provider that bills per gibibyte or per
// second has a legitimate reason to choose differently and the alternative is
// a lane quietly dropping the behaviour. What is NOT overridable is the ratio
// floor below: sizes a provider could set equal would turn the instrument off
// while leaving it in the output as though it had run.
const (
	// DefaultCopyOnWriteSmallBytes is the ballast in the small golden.
	DefaultCopyOnWriteSmallBytes int64 = 8 << 20
	// DefaultCopyOnWriteLargeBytes is the ballast in the large golden.
	DefaultCopyOnWriteLargeBytes int64 = 1 << 30
	// DefaultCopyOnWriteSamples is how many branches are timed per size.
	DefaultCopyOnWriteSamples = 3
	// DefaultCopyOnWriteTimeout bounds this behaviour alone.
	//
	// Half an hour, which is long by the standards of every other behaviour in
	// the suite and is what the work actually is on the slowest provider that
	// runs it. The docker provider's golden is a committed IMAGE, so a
	// gibibyte of ballast is a gibibyte written into a container and then
	// tarred into a layer, twice, before any branch is timed at all. A
	// behaviour bounded at the suite's usual few minutes would report that
	// provider as hung rather than as slow, and the difference matters: one is
	// a defect and the other is the cost of the measurement.
	DefaultCopyOnWriteTimeout = 30 * time.Minute

	// MinCopyOnWriteRatio is the least the two sizes may differ by.
	//
	// It exists because the one way to make this behaviour vacuous without
	// deleting it is to configure the two goldens to be the same size, after
	// which the measurement is noise and the verdict is a coin toss that reads
	// like a check.
	MinCopyOnWriteRatio = 16
	// MinCopyOnWriteSamples is the least number of timings per size. Two,
	// because a minimum of one is a single reading and a single reading on a
	// loaded machine is not a measurement.
	MinCopyOnWriteSamples = 2

	// MinCopyOnWriteLargeBytes is the least ballast the large golden may
	// carry, and it is a derived number rather than a taste.
	//
	// The ratio floor above stops a run making the two goldens the same size,
	// but it does not stop a run making them BOTH small, and small is the
	// direction that grants a free pass to the declaration this behaviour
	// exists to refuse. A provider claiming copy on write passes trivially
	// when the extra data costs less to copy than the allowance, so the large
	// size has to be big enough that copying it costs more than the
	// allowance's absolute floor even on the fastest storage anybody will run
	// this on. A quarter of a second at two gigabytes a second is five hundred
	// and twelve megabytes, so that is the floor, and the default is twice it.
	//
	// Shrinking the sizes toward this floor is not a way to pass: it makes the
	// FALSE side stricter, because an honest copier then has less to copy and
	// has to clear the same allowance. The floor is the only direction that
	// needed defending.
	MinCopyOnWriteLargeBytes int64 = 512 << 20
)

// The allowance, which is the single boundary both sides of the assertion are
// measured against.
//
// It answers one question: how much extra branch time is attributable to noise
// rather than to the extra gibibyte. Two terms, and the larger wins.
//
// The absolute floor covers a provider whose branching is quick, where the
// proportional term would be a few milliseconds and any hiccup would clear it.
// A quarter of a second is longer than any scheduling hiccup that survives
// taking the minimum of several alternating samples, and it is a small
// fraction of what moving a gibibyte costs on any storage that exists.
//
// The proportional term covers a provider whose branching is slow, where the
// jitter itself is measured in seconds: a container start under load varies by
// much more than a quarter of a second, and holding it to an absolute floor
// would fail honest providers on a busy machine. Half is deliberately generous.
// The cost of that generosity is bounded and is stated rather than hidden: a
// provider whose branch takes eight seconds could copy up to four seconds
// worth of data and still be called copy on write by this behaviour. The
// declared latency assertion below is what stops that from being unbounded,
// and a provider wanting a tighter answer sets a larger large size.
const (
	copyOnWriteNoiseFloor      = 250 * time.Millisecond
	copyOnWriteJitterFraction  = 0.5
	copyOnWriteBallastRowBytes = 1024
)

// ballastTable is the table the suite fills to make a golden large.
//
// Nothing else in the suite reads it. It exists to occupy storage, so its
// shape is chosen for exactly one property: the bytes must survive to disk
// uncompressed. Postgres compresses a value only once the whole tuple crosses
// the TOAST threshold of about two kilobytes, so rows a little under a
// kilobyte stay inline and stay literal, and the payload is hex from md5 of a
// random value rather than a repeated character, because a run of one
// character would compress to nothing the moment a future Postgres lowered
// that threshold and the behaviour would then be measuring an empty table.
const ballastTable = "conformance_ballast"

// copyOnWriteMatchesTheDeclaration is the behaviour.
func (h *harness) copyOnWriteMatchesTheDeclaration(ctx context.Context) {
	caps := h.p.Capabilities()
	small, large, samples := h.copyOnWriteSettings()

	smallV, smallBytes := h.refreshWithBallast(ctx, small)
	largeV, largeBytes := h.refreshWithBallast(ctx, large)

	if largeBytes <= smallBytes {
		h.t.Fatalf("the ballast did not grow the golden: the small one carries %s and the "+
			"large one %s. Nothing about copy on write can be read from two goldens of the "+
			"same size, and a verdict from them would be a coin toss printed as a check.",
			bytesText(smallBytes), bytesText(largeBytes))
	}

	smallTimes := make([]time.Duration, 0, samples)
	largeTimes := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		// Alternating, because load drifts and a drift that lands on one arm
		// reads exactly like the effect being measured.
		smallTimes = append(smallTimes, h.timeOneBranch(ctx, smallV.ID, fmt.Sprintf("env_cow_small_%d", i)))
		largeTimes = append(largeTimes, h.timeOneBranch(ctx, largeV.ID, fmt.Sprintf("env_cow_large_%d", i)))
	}

	ts, tl := minDuration(smallTimes), minDuration(largeTimes)
	grown := tl - ts
	allowance := copyOnWriteAllowance(ts)
	deltaGiB := float64(largeBytes-smallBytes) / float64(1<<30)
	marginal := grown.Seconds() / deltaGiB

	// The measurement is logged whatever the verdict, because the number is
	// the deliverable as much as the pass is, and because a failure nobody can
	// see the readings behind is a failure nobody can act on.
	h.t.Logf("\ncopy on write, measured\n"+
		"  provider                 %s, declares CopyOnWrite=%v\n"+
		"  small golden ballast     %s, branch times %s, min %s\n"+
		"  large golden ballast     %s, branch times %s, min %s\n"+
		"  extra data               %s\n"+
		"  extra branch time        %s, allowance %s\n"+
		"  marginal cost            %.2f seconds per GiB\n",
		h.p.Name(), caps.CopyOnWrite,
		bytesText(smallBytes), durationsText(smallTimes), ts.Round(time.Millisecond),
		bytesText(largeBytes), durationsText(largeTimes), tl.Round(time.Millisecond),
		bytesText(largeBytes-smallBytes),
		grown.Round(time.Millisecond), allowance.Round(time.Millisecond),
		marginal)

	if caps.CopyOnWrite {
		if grown > allowance {
			h.t.Fatalf("this provider declares CopyOnWrite, and branching the golden with %s "+
				"more data in it took %s longer, which is past the %s this machine's noise "+
				"can account for. Copy on write means a branch shares storage with its golden, "+
				"so branch time does not grow with the data; %.2f seconds per GiB is a copy "+
				"being made and charged to whoever waits for the environment. Either the "+
				"provider copies and the declaration is wrong, or it shares storage and "+
				"something else in Branch scales with size.",
				bytesText(largeBytes-smallBytes), grown.Round(time.Millisecond),
				allowance.Round(time.Millisecond), marginal)
		}
		// The declared latency, checked at the LARGE size, which is where the
		// claim is actually worth something. A provider whose branch time does
		// not grow with the data has no reason to exceed at a gibibyte what it
		// declared at a handful of rows, and without this the flat side of the
		// assertion could be satisfied by a provider that is uniformly, and
		// increasingly, slow.
		if caps.ExpectedBranchLatency > 0 && tl > caps.ExpectedBranchLatency {
			h.t.Fatalf("this provider declares CopyOnWrite and an expected branch latency of %s, "+
				"and its fastest branch of the %s golden took %s. The point of the capability "+
				"is that the larger database costs no more than the smaller one, so a declared "+
				"latency that holds on the conformance dataset and not on a gibibyte is a "+
				"number that stops being true exactly where a customer starts caring about it.",
				caps.ExpectedBranchLatency, bytesText(largeBytes), tl.Round(time.Millisecond))
		}
		return
	}

	if grown <= allowance {
		h.t.Fatalf("this provider declares CopyOnWrite FALSE, and branching the golden with %s "+
			"more data in it took only %s longer, inside the %s this machine's noise can "+
			"account for. Branch time that does not grow with the data is what copy on write "+
			"IS, so either the capability is understated or the branch is not carrying the "+
			"golden's data. Understating it is not the safe direction: the wave publishes one "+
			"table of branch time per provider, a buyer chooses from that table, and a "+
			"provider that hides a flat branch time is as wrong in that table as one that "+
			"invents it.",
			bytesText(largeBytes-smallBytes), grown.Round(time.Millisecond),
			allowance.Round(time.Millisecond))
	}
}

// branchIsWithinTheDeclaredLatency is the assertion Caps.ExpectedBranchLatency
// has always documented and nothing has ever made.
//
// The field's own comment says "The suite asserts against it, so a provider
// that gets slower fails rather than quietly degrading", and Wave 2 of the
// plan repeats that promise to five providers that have not been written yet.
// What the suite actually did was require the number to be greater than zero.
// So a provider could declare eight seconds, take three minutes, and stay
// green, and the sentence in the interface was describing protection that was
// not there. internal/db/pgurl carried a local copy of this assertion for its
// own provider with a comment saying generalising it belonged to the lane that
// owns the suite. This is that lane.
//
// The minimum of a few timings, not one, and not the mean. One reading on a
// machine at load twenty six is a reading of the machine.
func (h *harness) branchIsWithinTheDeclaredLatency(ctx context.Context) {
	caps := h.p.Capabilities()
	gv := h.refresh(ctx)

	const attempts = 3
	times := make([]time.Duration, 0, attempts)
	for i := 0; i < attempts; i++ {
		times = append(times, h.timeOneBranch(ctx, gv.ID, fmt.Sprintf("env_latency_%d", i)))
	}
	best := minDuration(times)
	h.t.Logf("branch of the conformance dataset: %s, best of %s, declared %s",
		best.Round(time.Millisecond), durationsText(times), caps.ExpectedBranchLatency)

	if best > caps.ExpectedBranchLatency {
		h.t.Fatalf("this provider declares an expected branch latency of %s and its FASTEST of "+
			"%d branches took %s. The declaration is what the engine plans an environment "+
			"around and what the wave's comparison table publishes, so a provider that has "+
			"got slower has to fail here rather than degrade quietly. Either the service "+
			"regressed or the number was never true.",
			caps.ExpectedBranchLatency, attempts, best.Round(time.Millisecond))
	}
}

// copyOnWriteSettings resolves the sizes and sample count, refusing a
// configuration that would make the behaviour unable to answer.
func (h *harness) copyOnWriteSettings() (small, large int64, samples int) {
	h.t.Helper()
	small, large = h.opts.CopyOnWriteSmallBytes, h.opts.CopyOnWriteLargeBytes
	if small <= 0 {
		small = DefaultCopyOnWriteSmallBytes
	}
	if large <= 0 {
		large = DefaultCopyOnWriteLargeBytes
	}
	samples = h.opts.CopyOnWriteSamples
	if samples <= 0 {
		samples = DefaultCopyOnWriteSamples
	}
	// Refused rather than corrected. A run configured so that the two goldens
	// cannot be told apart would still print a verdict, and a verdict from a
	// measurement that could not have gone the other way is the whole defect
	// this behaviour exists to remove.
	if large < MinCopyOnWriteLargeBytes {
		h.t.Fatalf("the large copy on write golden is configured at %s and the floor is %s. "+
			"Below it, copying the extra data costs less than the allowance the measurement "+
			"gives to noise, so a provider declaring copy on write would pass without the "+
			"claim ever having been at risk.",
			bytesText(large), bytesText(MinCopyOnWriteLargeBytes))
	}
	if large < small*MinCopyOnWriteRatio {
		h.t.Fatalf("the copy on write sizes are %s and %s, a ratio of %.1f, and this behaviour "+
			"needs at least %d. Below that the extra data costs less to copy than the machine's "+
			"own noise, so the measurement cannot refuse either declaration and the pass it "+
			"prints means nothing.",
			bytesText(small), bytesText(large), float64(large)/float64(small), MinCopyOnWriteRatio)
	}
	if samples < MinCopyOnWriteSamples {
		h.t.Fatalf("copy on write is configured for %d timing per size and needs at least %d; "+
			"one reading on a loaded machine is a reading of the machine", samples, MinCopyOnWriteSamples)
	}
	return small, large, samples
}

// refreshWithBallast publishes a golden carrying roughly the requested number
// of bytes, and returns what it actually carries.
//
// The ballast is written from inside the Mask callback, which is the one hook
// every conformant provider must call with a writable connection to the
// candidate, and it is therefore the one place the suite can change the size
// of a golden without adding a method to the interface that five real services
// would have to implement for a test's benefit.
//
// The size that comes back is MEASURED with pg_total_relation_size rather than
// assumed from the row count, because the number the behaviour divides by has
// to be the number of bytes that exist, not the number that were asked for.
func (h *harness) refreshWithBallast(ctx context.Context, want int64) (provider.GoldenVersion, int64) {
	h.t.Helper()

	var got int64
	spec := h.spec()
	inner := spec.Mask
	spec.Mask = func(mctx context.Context, candidate secrets.Value) error {
		if inner != nil {
			if err := inner(mctx, candidate); err != nil {
				return err
			}
		}
		n, err := fillBallast(mctx, candidate, want)
		if err != nil {
			return err
		}
		got = n
		return nil
	}

	gv, err := h.p.RefreshGolden(ctx, spec)
	h.trackGolden(gv.ID)
	if err != nil {
		h.t.Fatalf("RefreshGolden with %s of ballast: %v", bytesText(want), err)
	}
	if !gv.Verified || gv.ID == "" {
		h.t.Fatalf("RefreshGolden with %s of ballast published %q, verified=%v",
			bytesText(want), gv.ID, gv.Verified)
	}
	if got == 0 {
		h.t.Fatalf("the golden was published and the suite could not put any ballast in it. " +
			"Copy on write cannot be measured against two goldens of the same size, and " +
			"reporting that as a pass is what this behaviour exists to stop.")
	}
	return gv, got
}

// fillBallast writes rows into the candidate until the table holds about want
// bytes, and returns what it holds.
func fillBallast(ctx context.Context, candidate secrets.Value, want int64) (int64, error) {
	db, err := sql.Open("pgx", candidate.Reveal())
	if err != nil {
		return 0, fmt.Errorf("open the golden candidate to size it: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		// Loud, and named as the thing that could not be done. A provider that
		// hands Mask a connection string nothing can connect to has broken the
		// contract every other behaviour depends on, and a suite that treated
		// it as a reason to skip would be reporting a coverage it does not
		// have.
		return 0, fmt.Errorf("the connection string this provider passed to Mask does not "+
			"connect, so the golden cannot be sized and copy on write cannot be measured: %w", err)
	}

	if _, err := db.ExecContext(ctx,
		"CREATE TABLE IF NOT EXISTS "+ballastTable+" (pad text NOT NULL)"); err != nil {
		return 0, fmt.Errorf("create the ballast table: %w", err)
	}
	rows := want / copyOnWriteBallastRowBytes
	if rows < 1 {
		rows = 1
	}
	// Thirty md5 hex digests is nine hundred and sixty characters, which keeps
	// the tuple under the TOAST threshold so the bytes stay inline and stay
	// literal. One md5 per row rather than per digest, because the point is
	// occupying storage and a million calls to random() is the slow part.
	if _, err := db.ExecContext(ctx,
		"INSERT INTO "+ballastTable+" (pad) SELECT repeat(md5(random()::text), 30) "+
			"FROM generate_series(1, $1::bigint)", rows); err != nil {
		return 0, fmt.Errorf("write %d ballast rows: %w", rows, err)
	}

	var size int64
	if err := db.QueryRowContext(ctx,
		"SELECT pg_total_relation_size($1)", ballastTable).Scan(&size); err != nil {
		return 0, fmt.Errorf("measure the ballast: %w", err)
	}
	return size, nil
}

// timeOneBranch creates a branch, times it, and destroys it before the next
// sample starts.
//
// Destroyed inline rather than at cleanup for two reasons. A provider with a
// declared branch limit would refuse the later samples if they accumulated,
// and a provider whose branches share a machine gets slower as they pile up,
// which would show up as growth attributable to the ballast when it is
// attributable to the previous sample.
func (h *harness) timeOneBranch(ctx context.Context, version, env string) time.Duration {
	h.t.Helper()
	start := time.Now()
	b, err := h.p.Branch(ctx, version, env)
	took := time.Since(start)
	if err != nil {
		h.t.Fatalf("Branch(%s, %s): %v", version, env, err)
	}
	h.created.add(b.ProviderRef)
	h.created.add(b.EnvID)
	// Registered as well as destroyed below, so that a Fatalf between here and
	// the destroy still gets swept.
	h.t.Cleanup(func() { _ = h.p.Destroy(context.Background(), b) })
	if err := h.p.Destroy(ctx, b); err != nil {
		h.t.Fatalf("Destroy the timing branch %s: %v", b.ProviderRef, err)
	}
	return took
}

func copyOnWriteAllowance(small time.Duration) time.Duration {
	proportional := time.Duration(float64(small) * copyOnWriteJitterFraction)
	if proportional > copyOnWriteNoiseFloor {
		return proportional
	}
	return copyOnWriteNoiseFloor
}

func minDuration(ds []time.Duration) time.Duration {
	best := ds[0]
	for _, d := range ds[1:] {
		if d < best {
			best = d
		}
	}
	return best
}

func durationsText(ds []time.Duration) string {
	parts := make([]string, 0, len(ds))
	for _, d := range ds {
		parts = append(parts, d.Round(time.Millisecond).String())
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func bytesText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(1<<20))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
