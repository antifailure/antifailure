package sqlload

import (
	"errors"
	"fmt"

	"github.com/antifailure/antifailure/engine/internal/load"
)

// Pooling several rounds of one SQL workload back into one result.
//
// This is load.Merge's twin, and it exists for the same reason and under the
// same rule. The base branch comparison sends each side in several short
// rounds, interleaved with the other side's, so that the order two
// environments are measured in cannot land on one side only. Each side's
// rounds then have to become one result, because everything downstream, the
// difference, the run wide measures and the verdict, reads a result rather
// than a list of them.
//
// IT POOLS SAMPLES AND NEVER AVERAGES PERCENTILES. The mean of sixteen p95s is
// not the p95 of anything: it is a number no run produced, and handing it to a
// resolution gate would make the gate answer a question about a distribution
// that never existed. So every latency a round measured is kept, the rounds'
// samples are concatenated, and the percentiles are taken once over the pool.
// That is why Result carries an unexported raw field at all.
//
// WHICH IS ALSO WHY A RESULT THAT HAS BEEN THROUGH A DOCUMENT CANNOT BE
// MERGED. JSON carries the percentiles and not the samples they came from, so
// a decoded result has no raw and Merge refuses it with ErrNoSamples rather
// than silently averaging. A caller that wants a pooled result has to pool the
// runs it made, not the documents it stored.

// ErrNoSamples is returned by Merge for a result that carries percentiles and
// not the samples they were taken from, which is every result that has been
// through a document.
var ErrNoSamples = errors.New(
	"this SQL result carries percentiles and not the samples they were taken from, " +
		"and percentiles cannot be combined into a percentile")

// rawSamples is every latency one run measured, kept so rounds can be pooled.
//
// The weights, the baselines and the transaction order ride along because a
// pooled result has to rebuild the per transaction rows and those three are
// properties of the MIX rather than of the run. Reading them off the first
// part's rows instead would work until a transaction that ran in no round at
// all had no row to read them from, which is exactly the transaction whose
// absence a report must still show.
type rawSamples struct {
	all           []float64
	byTransaction map[string][]float64
	byStatement   map[stmtKey][]float64
	order         []string
	weights       map[string]float64
	baselines     map[string]Baseline
}

// Merge combines several rounds of one workload into one result, as though
// every transaction had run in one longer round.
//
// Counts add, the duration is the time actually spent running, the throughput
// is what was committed over that time rather than any one round's rate, and
// every percentile is taken over the pooled samples.
//
// It fails closed on a client count that differs between rounds. A throughput
// pooled over two concurrencies is a throughput at a concurrency nobody chose,
// which is the same defect the run itself refuses when it will not proceed
// with fewer clients than it was asked for.
func Merge(parts ...*Result) (*Result, error) {
	if len(parts) == 0 {
		return nil, errors.New("there are no rounds to merge")
	}
	for i, p := range parts {
		switch {
		case p == nil:
			return nil, errors.New("one of the rounds to merge is missing")
		case p.raw == nil:
			return nil, ErrNoSamples
		case p.Clients != parts[0].Clients:
			return nil, fmt.Errorf(
				"round %d ran %d clients and round 1 ran %d, so pooling them would report a "+
					"throughput at a concurrency nobody chose",
				i+1, p.Clients, parts[0].Clients)
		}
	}

	first := parts[0]
	out := &Result{
		Source: first.Source, Clients: first.Clients, Refused: first.Refused,
		Errors: map[string]int{},
	}
	if out.Refused == nil {
		out.Refused = []Refused{}
	}
	pooled := poolSamples(parts)

	txCounts := map[string]*txStat{}
	stmtCounts := map[stmtKey]*stmtStat{}
	for _, p := range parts {
		out.Transactions += p.Transactions
		out.TransactionsFailed += p.TransactionsFailed
		out.Retries += p.Retries
		out.Deadlocks += p.Deadlocks
		out.SerializationFailures += p.SerializationFailures
		out.Statements += p.Statements
		out.StatementsFailed += p.StatementsFailed
		out.Rows += p.Rows
		out.Duration += p.Duration
		out.ClientsStopped += p.ClientsStopped
		for k, v := range p.Errors {
			out.Errors[k] += v
		}
		for k, v := range p.StoppedBecause {
			if out.StoppedBecause == nil {
				out.StoppedBecause = map[string]int{}
			}
			out.StoppedBecause[k] += v
		}
		for _, tx := range p.PerTransaction {
			s := txCounts[tx.Name]
			if s == nil {
				s = &txStat{}
				txCounts[tx.Name] = s
			}
			s.executed += tx.Executed
			s.failed += tx.Failed
			s.retries += tx.Retries
		}
		for _, st := range p.PerStatement {
			key := stmtKey{st.Transaction, st.Label}
			s := stmtCounts[key]
			if s == nil {
				s = &stmtStat{}
				stmtCounts[key] = s
			}
			s.executed += st.Executed
			s.errors += st.Errors
			s.rows += st.Rows
		}
	}

	out.Overall = load.Percentiles(pooled.all)
	if out.Duration > 0 {
		out.TPS = float64(out.Transactions) / out.Duration.Seconds()
	}
	if attempts := out.Transactions + out.TransactionsFailed; attempts > 0 {
		out.ErrorRate = float64(out.TransactionsFailed) / float64(attempts)
	}
	if len(out.Errors) == 0 {
		out.Errors = map[string]int{}
	}

	for _, name := range pooled.order {
		samples := pooled.byTransaction[name]
		counts := txCounts[name]
		if counts == nil {
			// Unreachable from a real run, and kept: finish emits a row for
			// every transaction the mix declares, so a name in the order
			// always has one. It guards a result assembled by hand, and
			// without it that case is a nil dereference rather than a zero.
			counts = &txStat{}
		}
		tr := TransactionResult{
			Name: name, Executed: counts.executed, Failed: counts.failed,
			Retries: counts.retries, Latency: load.Percentiles(samples),
			Weight: pooled.weights[name], Baselines: pooled.baselines[name],
		}
		// Recomputed from the pooled samples rather than averaged from the
		// rounds' own increases, for the reason the percentiles are: the mean
		// of several means is the mean of the pool only when every round
		// committed the same number of transactions, and no round does.
		if tr.Baselines.Has && tr.Baselines.MeanMs > 0 && len(samples) > 0 {
			tr.Baselines.MeanIncrease = mean(samples)/tr.Baselines.MeanMs - 1
		}
		out.PerTransaction = append(out.PerTransaction, tr)
	}
	for key, counts := range stmtCounts {
		out.PerStatement = append(out.PerStatement, StatementResult{
			Transaction: key.transaction, Label: key.label,
			Executed: counts.executed, Errors: counts.errors, Rows: counts.rows,
			Latency: load.Percentiles(pooled.byStatement[key]),
		})
	}
	sortStatements(out.PerStatement)

	mergeObservation(out, parts)
	mergeLockWaits(out, parts)
	out.raw = pooled
	return out, nil
}

// poolSamples concatenates every round's samples, keeping the mix's own
// properties from the first round that carried them.
func poolSamples(parts []*Result) *rawSamples {
	pooled := &rawSamples{
		byTransaction: map[string][]float64{},
		byStatement:   map[stmtKey][]float64{},
		weights:       map[string]float64{},
		baselines:     map[string]Baseline{},
	}
	seen := map[string]bool{}
	for _, p := range parts {
		pooled.all = append(pooled.all, p.raw.all...)
		for name, v := range p.raw.byTransaction {
			pooled.byTransaction[name] = append(pooled.byTransaction[name], v...)
		}
		for key, v := range p.raw.byStatement {
			pooled.byStatement[key] = append(pooled.byStatement[key], v...)
		}
		for name, w := range p.raw.weights {
			pooled.weights[name] = w
		}
		for name, b := range p.raw.baselines {
			pooled.baselines[name] = b
		}
		// Every transaction the mix declares keeps a row even when no round
		// picked it, and in the order the mix declares them, so a rarest
		// transaction that never ran reports zero rather than vanishing.
		for _, name := range p.raw.order {
			if !seen[name] {
				seen[name] = true
				pooled.order = append(pooled.order, name)
			}
		}
	}
	return pooled
}

// mergeObservation pools the evidence that the clients really overlapped.
//
// The peaks are the largest any round saw rather than a sum, because each is
// "the most at ONE instant" and instants do not add. Rounds that were never
// observed are named in the note rather than silently folded in: a peak taken
// over twelve of sixteen rounds is a floor, and a reader deciding whether the
// run was concurrent at all should be told which number they are holding.
func mergeObservation(out *Result, parts []*Result) {
	unobserved := 0
	var notes []string
	for _, p := range parts {
		if p.BackendsSeen == nil {
			unobserved++
			if p.ObserverNote != "" && !contains(notes, p.ObserverNote) {
				notes = append(notes, p.ObserverNote)
			}
			continue
		}
		out.PeakActiveBackends = higher(out.PeakActiveBackends, p.PeakActiveBackends)
		out.PeakOpenTransactions = higher(out.PeakOpenTransactions, p.PeakOpenTransactions)
		out.BackendsSeen = higher(out.BackendsSeen, p.BackendsSeen)
	}
	switch {
	case unobserved == len(parts):
		out.ObserverNote = joinNotes(notes)
	case unobserved > 0:
		out.ObserverNote = fmt.Sprintf(
			"%d of %d rounds were never sampled, so these peaks are the most any of the "+
				"other %d saw and are a floor rather than the run's own maximum: %s",
			unobserved, len(parts), len(parts)-unobserved, joinNotes(notes))
	}
}

// mergeLockWaits pools the contention the rounds were seen to suffer.
//
// THE COUNTS ADD, which is what makes them different from the peaks above. A
// peak is the most at ONE INSTANT and instants do not add, so pooling them
// takes the largest. A wait is an EVENT and a waiting millisecond is a
// millisecond somebody spent queueing, so sixteen rounds that each queued
// twice queued thirty two times between them. Taking the largest here would
// report a whole comparison as no more contended than its single worst round.
//
// NIL RATHER THAN ZERO IS CARRIED THROUGH, and it has more riding on it here
// than at any other layer that asserts it. This pooled result is what
// ProjectSQLLoad hands to Compare, and measureDifferences emits no lock row
// unless BOTH sides carry a number, so a pool that dropped the pointers would
// turn the one command that compares contention between two builds into
// silence. Not a missing feature: the answer meaning "nobody watched", given
// by rounds that all watched.
//
// A round that was never sampled is named rather than folded in, for the same
// reason mergeObservation names one: a count summed over twelve of sixteen
// rounds is a floor, and the reader deciding whether this build blocked more
// than the other one should be told which number they are holding.
func mergeLockWaits(out *Result, parts []*Result) {
	unwatched, dropped := 0, 0
	var lost, bounds []string
	waits, ms := 0, 0.0
	pairs := map[string]*LockWait{}
	for _, p := range parts {
		if p.LockWaits == nil {
			unwatched++
			if p.LockWaitNote != "" && !contains(lost, p.LockWaitNote) {
				lost = append(lost, p.LockWaitNote)
			}
			continue
		}
		waits += *p.LockWaits
		if p.LockWaitMS != nil {
			ms += *p.LockWaitMS
		}
		if p.LockWaitNote != "" && !contains(bounds, p.LockWaitNote) {
			bounds = append(bounds, p.LockWaitNote)
		}
		for _, w := range p.LockWaitPairs {
			key := lockKey(w)
			held, ok := pairs[key]
			if !ok {
				copied := w
				pairs[key] = &copied
				continue
			}
			held.Waits += w.Waits
			held.WaitedMS += w.WaitedMS
		}
	}
	if unwatched == len(parts) {
		out.LockWaitNote = joinNotes(lost)
		return
	}

	out.LockWaits = &waits
	out.LockWaitMS = &ms
	// An empty list rather than a null one once any round has watched, which is
	// the runner's own rule: at that point "no pairs" is a measurement saying
	// nothing of this run was ever seen queueing.
	out.LockWaitPairs = make([]LockWait, 0, len(pairs))
	for _, p := range pairs {
		out.LockWaitPairs = append(out.LockWaitPairs, *p)
	}
	sortLockWaits(out.LockWaitPairs)
	// Sorted BEFORE the cap is applied, so what a pool of rounds gives up is
	// the pairs that waited least. A round drops whichever pair it happens to
	// meet after its sixty fourth, because it is folding samples in as they
	// arrive and cannot know which will matter; a pool has every pair in hand
	// and has no such excuse.
	if len(out.LockWaitPairs) > maxLockPairs {
		dropped = len(out.LockWaitPairs) - maxLockPairs
		out.LockWaitPairs = out.LockWaitPairs[:maxLockPairs]
	}

	var note string
	if unwatched > 0 {
		note = fmt.Sprintf(
			"%d of %d rounds never read the wait queues, so these counts are the sum over the "+
				"other %d and are a floor rather than the whole run's: %s. ",
			unwatched, len(parts), len(parts)-unwatched, joinNotes(lost))
	}
	if len(bounds) > 0 {
		note += joinNotes(bounds)
	}
	if dropped > 0 {
		note += fmt.Sprintf(" %d further distinct blocking pairs were seen across the rounds "+
			"and not kept, because a result holds at most %d.", dropped, maxLockPairs)
	}
	out.LockWaitNote = note
}

func higher(a, b *int) *int {
	if b == nil {
		return a
	}
	if a == nil || *b > *a {
		v := *b
		return &v
	}
	return a
}

func contains(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}

func joinNotes(notes []string) string {
	switch len(notes) {
	case 0:
		return "no reason was recorded"
	case 1:
		return notes[0]
	}
	out := notes[0]
	for _, n := range notes[1:] {
		out += "; " + n
	}
	return out
}
