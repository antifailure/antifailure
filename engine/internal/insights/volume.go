package insights

import (
	"fmt"
	"time"

	"github.com/antifailure/antifailure/engine/internal/volume"
)

// The rehearsal's arithmetic against production's size, and the one rule it
// obeys.
//
// EVERY NUMBER HERE IS LABELLED AS AN EXTRAPOLATION. A migration rehearsed
// against a branch holding a thousand rows held its lock for half a second,
// and that is a measurement. What it would hold on production's four billion
// is arithmetic, and the difference between those two is the whole reason this
// product exists: a staging database says four seconds and production says
// ninety four. Printing the projection without the word for it would make this
// engine the thing it was built to replace.
//
// So the projection is never presented as a timing, never replaces the
// measured figure, and always names both the row counts it was computed from
// and the assumption it rests on.

// Extrapolation is what one measured lock would cost at production's size.
type Extrapolation struct {
	// Table is the relation, as the lock sampler named it.
	Table string `json:"table"`
	// Mode is the strongest lock mode that was held.
	Mode string `json:"mode"`
	// HeldMS is the measurement, and BranchRows the row count it was measured
	// over.
	HeldMS     float64 `json:"held_ms"`
	BranchRows int64   `json:"branch_rows"`
	// ProductionRows is what the profile says production holds in the same
	// table.
	ProductionRows int64 `json:"production_rows"`
	// AtThisRateMS is the projection, and Factor how many times bigger
	// production is. Both are arithmetic on the two numbers above.
	AtThisRateMS float64 `json:"at_this_rate_ms"`
	Factor       float64 `json:"factor"`
	// Sentence is the whole thing in words, and it always carries the word
	// extrapolation. Callers render this rather than composing their own,
	// so there is one place the label can be lost from and it is guarded by
	// a test.
	Sentence string `json:"sentence"`
}

// Extrapolate states what the rehearsal's lock timings would be at
// production's row counts.
//
// A nil profile is not a quiet no-op. The timings measured against a branch
// are a lower bound on what production would see, and a report that does not
// say so is the exact defect this lane was written for: a lock held for 500ms
// over two hundred rows is a real measurement and a worthless prediction, and
// nothing said which of the two it was.
func Extrapolate(r *Rehearsal, profile *volume.Profile, reason string) {
	if r == nil {
		return
	}
	if profile == nil {
		if reason == "" {
			reason = "no volume profile says what production holds"
		}
		r.Missing = append(r.Missing,
			"every lock timing here was measured against this branch's own row counts, and "+
				reason+", so each one is a lower bound and not a prediction")
		return
	}
	r.ProfileCollectedAt = profile.CollectedAt
	for _, l := range r.Locks {
		if l.HeldMS <= 0 {
			continue
		}
		table, why := profile.FindRelname(l.Table)
		if why != "" {
			r.Missing = append(r.Missing,
				fmt.Sprintf("the %s held on %s for %s could not be put against production's "+
					"row count, because %s, so it is a lower bound and not a prediction",
					l.Mode, l.Table, duration(l.HeldMS), why))
			continue
		}
		branch := r.BranchRows[l.Table]
		e := Extrapolation{
			Table: l.Table, Mode: l.Mode, HeldMS: l.HeldMS,
			BranchRows: branch, ProductionRows: table.Rows,
		}
		switch {
		case branch <= 0:
			// Nothing to extrapolate from. A rate needs a denominator too, and
			// dividing by an empty table would produce an infinity somebody
			// would screenshot.
			e.Sentence = fmt.Sprintf(
				"This rehearsal held %s on %s for %s, and this branch holds no rows in it, so "+
					"there is no rate to extrapolate from. Production holds %s.",
				l.Mode, l.Table, duration(l.HeldMS), volume.Rows(table.Rows))
		case table.Rows <= branch:
			e.Factor = 1
			e.AtThisRateMS = l.HeldMS
			e.Sentence = fmt.Sprintf(
				"This rehearsal held %s on %s for %s over %s. Production holds %s, which is no "+
					"more than this branch, so this timing is a measurement rather than a lower bound.",
				l.Mode, l.Table, duration(l.HeldMS), volume.Rows(branch), volume.Rows(table.Rows))
		default:
			e.Factor = float64(table.Rows) / float64(branch)
			e.AtThisRateMS = l.HeldMS * e.Factor
			e.Sentence = fmt.Sprintf(
				"This rehearsal held %s on %s for %s over %s. Production holds %s in that table, "+
					"%s times as many. At this rate the lock would be held for roughly %s. That is "+
					"an extrapolation from one measurement rather than a second measurement, and it "+
					"assumes the cost grows with the row count, which a table rewrite does and an "+
					"index build on an already sorted column does not.",
				l.Mode, l.Table, duration(l.HeldMS), volume.Rows(branch),
				volume.Rows(table.Rows), times(e.Factor), longDuration(e.AtThisRateMS))
		}
		r.Extrapolations = append(r.Extrapolations, e)
	}
}

// times renders how many times bigger production is.
//
// Whole numbers with separators past ten, because three million is a figure
// somebody reads and 3021582.4 is a figure somebody squints at.
func times(factor float64) string {
	if factor >= 10 {
		return volume.Count(int64(factor))
	}
	return fmt.Sprintf("%.1f", factor)
}

// longDuration renders a projection that may run to days.
//
// duration above stops being readable past an hour, because it was written for
// a statement timing and a statement that takes a day is not one anybody has
// timed. A projection is exactly the number that runs to days, and printing it
// as 5760000m00s is a figure nobody can act on.
func longDuration(ms float64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	case d >= 2*time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	case d >= 2*time.Minute:
		return fmt.Sprintf("%.1f minutes", d.Minutes())
	}
	return duration(ms)
}
