package pgcrash

import (
	"sort"
	"sync"
)

// Ledger is the client's own record of what the database told it.
//
// This is the whole difference between "the database came back up" and "the
// database did not lose my data". A recovery check that reads only the
// database after the crash can say the rows it finds are self consistent; it
// cannot say whether a row that should be there is missing, because it has no
// independent record of what should be there. The ledger is that record, and
// it is written on the client side of the wire, from what the client actually
// observed:
//
//   - Attempted, before the statement is sent. The commit may or may not land.
//   - Acknowledged, only after the call returned with no error. The database
//     told this client the transaction was committed, so it is now durable or
//     the database is broken.
//   - Unresolved, when the call failed or the connection died under it. The
//     commit may have landed or may not have, and either outcome is correct.
//
// The three states are kept apart because two of the assertions that matter
// are about the boundary between them. An acknowledged commit that is absent
// after recovery is a LOST DURABLE COMMIT, which is a durability failure. A
// row that is present and was never even attempted is a PHANTOM, which means
// the ledger and the database disagree about what happened. An unresolved
// commit that landed is neither: it is the ordinary, correct outcome of a
// crash in the gap between the commit and the acknowledgement, and calling it
// a phantom would make the check cry wolf on every run.
type Ledger struct {
	mu         sync.Mutex
	acked      map[int64]struct{}
	unresolved map[int64]struct{}
	attempts   int
	// flushLSN is the highest write ahead log position any writer saw flushed
	// after one of its own commits was acknowledged. It is a lower bound on
	// how far recovery has to have replayed, taken from the database at a
	// moment the client can name.
	flushLSN uint64
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{acked: map[int64]struct{}{}, unresolved: map[int64]struct{}{}}
}

// Attempt records that a commit is about to be tried.
//
// It goes straight into unresolved, so that a client killed between this call
// and its outcome leaves the id recorded as "might have landed" rather than
// leaving it recorded nowhere. An id recorded nowhere would come back after
// recovery as a phantom, and the check would report a database defect that was
// really a bookkeeping hole in the instrument.
func (l *Ledger) Attempt(id int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts++
	l.unresolved[id] = struct{}{}
}

// Acknowledged records that the database returned success for a commit.
func (l *Ledger) Acknowledged(id int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.unresolved, id)
	l.acked[id] = struct{}{}
}

// ObservedFlushLSN records a write ahead log flush position the database
// reported after an acknowledged commit, keeping the highest.
func (l *Ledger) ObservedFlushLSN(lsn uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lsn > l.flushLSN {
		l.flushLSN = lsn
	}
}

// Counts is what the ledger holds.
func (l *Ledger) Counts() (acknowledged, unresolved, attempts int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.acked), len(l.unresolved), l.attempts
}

// FlushLSN is the highest flush position observed after an acknowledged
// commit, or zero if none was sampled.
func (l *Ledger) FlushLSN() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.flushLSN
}

// Reconciliation is the ledger compared against what the database holds after
// recovery.
type Reconciliation struct {
	// Acknowledged is how many commits the client was told were committed.
	Acknowledged int `json:"acknowledged"`
	// Unresolved is how many were in flight when the fault landed.
	Unresolved int `json:"unresolved"`
	// Present is how many rows the database holds after recovery.
	Present int `json:"present"`
	// LostCount is how many acknowledged commits are not there any more. It
	// must be zero, and anything else is a durability failure.
	LostCount int `json:"lostCount"`
	// LostSample is up to sampleLimit of them. The count is separate from the
	// sample so that nobody reads "3 lost" off a sample that is three long.
	LostSample []int64 `json:"lostSample,omitempty"`
	// PhantomCount is how many rows the client never attempted. It must be
	// zero.
	PhantomCount int `json:"phantomCount"`
	// PhantomSample is up to sampleLimit of them.
	PhantomSample []int64 `json:"phantomSample,omitempty"`
	// UnresolvedLanded are in flight commits that did land. Correct, and
	// reported because a reader comparing the counts will otherwise think
	// something is missing.
	UnresolvedLanded int `json:"unresolvedLanded"`
	// Consistent reports whether the three sets and the row count add up. It
	// is a check on this function rather than on the database: if they do not
	// add up, the reconciliation has not measured what it claims to, and the
	// run reports that rather than reporting a clean recovery.
	Consistent bool `json:"consistent"`
}

// Held reports whether recovery lost nothing and invented nothing.
//
// An inconsistent reconciliation is not held. "I could not work out what
// happened" is not a pass, and this is the one line that keeps it from
// becoming one.
func (r Reconciliation) Held() bool {
	return r.LostCount == 0 && r.PhantomCount == 0 && r.Consistent
}

// sampleLimit is how many ids a reconciliation carries into a report.
//
// A lost commit is reported by count and by example. A thousand ids in a pull
// request comment would push everything else out of it, and the hundredth id
// tells a reader nothing the tenth did not.
const sampleLimit = 10

// Reconcile compares the ledger with the set of ids the database holds.
//
// Pure, and separate from everything that talks to Postgres, because this is
// the function whose answer the whole feature rests on: it has to be provable
// against a set somebody wrote down rather than only against a database
// somebody crashed.
func (l *Ledger) Reconcile(present map[int64]struct{}) Reconciliation {
	l.mu.Lock()
	defer l.mu.Unlock()

	r := Reconciliation{
		Acknowledged: len(l.acked),
		Unresolved:   len(l.unresolved),
		Present:      len(present),
	}
	var lost, phantom []int64
	for id := range l.acked {
		if _, ok := present[id]; !ok {
			lost = append(lost, id)
		}
	}
	for id := range present {
		_, wasAcked := l.acked[id]
		_, wasTried := l.unresolved[id]
		switch {
		case wasAcked:
		case wasTried:
			r.UnresolvedLanded++
		default:
			phantom = append(phantom, id)
		}
	}
	sort.Slice(lost, func(a, b int) bool { return lost[a] < lost[b] })
	sort.Slice(phantom, func(a, b int) bool { return phantom[a] < phantom[b] })

	// Every row present is exactly one of: an acknowledged commit that
	// survived, an in flight commit that landed, or a phantom. If that sum is
	// not the row count, one of the three sets was built wrongly and no
	// verdict drawn from them means anything.
	survived := r.Acknowledged - len(lost)
	r.Consistent = survived+r.UnresolvedLanded+len(phantom) == r.Present

	r.LostCount, r.LostSample = len(lost), truncate(lost)
	r.PhantomCount, r.PhantomSample = len(phantom), truncate(phantom)
	return r
}

// truncate cuts a list of ids down to the sample a report carries.
func truncate(ids []int64) []int64 {
	if len(ids) <= sampleLimit {
		return ids
	}
	return ids[:sampleLimit]
}
