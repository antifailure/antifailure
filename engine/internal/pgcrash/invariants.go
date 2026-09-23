package pgcrash

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/antifailure/antifailure/engine/internal/invariant"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The manifest's own rules about the user's own data, asked of the recovered
// database.
//
// WHY THIS IS A SEPARATE ARM. Everything else this package asserts is about a
// schema of its own, and workload.go says why: asserting that a table somebody
// else is writing did not change, while they are writing it, is a claim about
// a moving target. That reason expires the moment the writers stop and the
// database answers a query again, and that moment is exactly when the user's
// own invariants are the right question. The ledger proves the engine's own
// commits survived. Only the manifest's invariants can say whether the user's
// data still means what the user says it means, and until this existed nothing
// asked: `af ci` runs the invariants early and the chaos run last, so the
// rules a project writes about its data were never once evaluated against a
// database that had just been crashed.
//
// WHY BOTH SIDES. An invariant that was already violated before anything was
// broken is not something the fault did, and reporting it as a crash failure
// would blame the fault for a defect the run inherited. So each invariant is
// asked twice and the answers are kept apart:
//
//   - held before and violated after: the fault broke it. That is a failure,
//     and it is the only one of the three that is.
//   - violated before: nothing this run measured about it is attributable to
//     the fault, whatever it says afterwards. Reported, attributed to nothing.
//   - not askable on one side or the other: unverified, never a pass. A
//     database that never came back is the loudest case, and it is the one
//     where a silent pass would do the most harm.
//
// A manifest that declares no invariants runs none of this, opens no
// connection, and adds nothing to any output.

// InvariantSide is one evaluation of one invariant.
//
// Held and Error are separate for the reason engine/internal/invariant keeps
// them separate: a statement that could not be asked has not been shown to be
// violated, and reading "not held" as "violated" would report a check that
// never ran as a broken rule.
type InvariantSide struct {
	// Held is true when the statement returned no rows. It is false only when
	// rows came back; a side with no verdict leaves it false and sets Error.
	Held bool `json:"held"`
	// Error is why this side has no verdict, empty when it has one.
	Error string `json:"error,omitempty"`
	// Columns and Rows are the violating rows, bounded the way
	// engine/internal/invariant bounds them, and More says there were others.
	Columns []string   `json:"columns,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
	More    bool       `json:"more,omitempty"`
}

// Evaluated reports whether this side produced a verdict either way.
func (s InvariantSide) Evaluated() bool { return s.Error == "" }

// Violated reports whether this side was shown to be broken.
func (s InvariantSide) Violated() bool { return s.Error == "" && !s.Held }

// InvariantCheck is one invariant asked on both sides of the fault.
type InvariantCheck struct {
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Before      InvariantSide `json:"before"`
	After       InvariantSide `json:"after"`
}

// runInvariants asks every declared invariant of the database and returns one
// side per invariant, in the order the manifest declares them.
//
// The connection is this package's own and is closed here. It is opened only
// when there is something to ask, so a project that declares no invariants
// pays nothing and, more to the point, cannot be handed a connection error
// about a question it never asked.
//
// A connection that cannot be made is not an empty answer. Every invariant
// gets a side that says why it was not asked, because a missing entry reads as
// nothing to report and this is the case where there is the most to report.
func runInvariants(ctx context.Context, opts Options) []InvariantSide {
	if len(opts.Invariants) == 0 {
		return nil
	}
	conn, err := pgx.Connect(ctx, opts.URL)
	if err != nil {
		return unevaluatedSides(opts.Invariants,
			fmt.Errorf("connecting to the database to ask it: %w", err))
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	return sidesOf(invariant.Run(ctx, conn, opts.Invariants, invariant.Options{}))
}

// sidesOf carries what the statements said into this package's shape.
//
// engine/internal/invariant is reused rather than reimplemented: it already
// runs each statement in a read only RepeatableRead transaction with a
// statement timeout, bounds the rows at the server so a check matching a
// million rows does not pull a million rows back, and keeps "could not ask"
// apart from "asked and violated". A second implementation of that would be a
// second set of answers to drift from this one.
func sidesOf(s invariant.Summary) []InvariantSide {
	out := make([]InvariantSide, 0, len(s.Results))
	for _, r := range s.Results {
		side := InvariantSide{Held: r.Held, Columns: r.Columns, Rows: r.Rows, More: r.More}
		if r.Err != nil {
			side.Error = r.Err.Error()
		}
		out = append(out, side)
	}
	return out
}

// unevaluatedSides is one side per invariant, each saying why it was not asked.
func unevaluatedSides(invs []schema.Invariant, err error) []InvariantSide {
	out := make([]InvariantSide, 0, len(invs))
	for range invs {
		out = append(out, InvariantSide{Error: err.Error()})
	}
	return out
}

// pairInvariants puts the two sides of each invariant together.
//
// By position, which is exact because both sides are produced from the same
// declared list in the manifest's own order and engine/internal/invariant
// returns one result per invariant whatever happens to it, including a run
// that was cancelled partway. A side that is short is filled with "not asked"
// rather than dropped, because an invariant missing from the arm reads as an
// invariant with nothing to report.
func pairInvariants(invs []schema.Invariant, before, after []InvariantSide) []InvariantCheck {
	if len(invs) == 0 {
		return nil
	}
	out := make([]InvariantCheck, 0, len(invs))
	for i, inv := range invs {
		out = append(out, InvariantCheck{
			Name:        inv.Name,
			Description: inv.Description,
			Before:      sideAt(before, i),
			After:       sideAt(after, i),
		})
	}
	return out
}

// sideAt reads one side, or says it was never asked.
func sideAt(sides []InvariantSide, i int) InvariantSide {
	if i >= 0 && i < len(sides) {
		return sides[i]
	}
	return InvariantSide{Error: "the engine did not ask this invariant"}
}

// judgeInvariants turns the pairs into problems and unverified entries.
//
// The order of the branches is the order of what is knowable. A side with no
// verdict is checked first, because an invariant that could not be asked says
// nothing about the fault however the other side came out. Then the before
// side, because a rule that was already broken cannot be broken by the fault.
// Only what held before and is violated after is attributable, and only that
// is a failure.
func (r *Result) judgeInvariants() {
	for _, c := range r.Invariants {
		switch {
		case !c.Before.Evaluated() || !c.After.Evaluated():
			which, why := "after the recovery", c.After.Error
			if !c.Before.Evaluated() {
				which, why = "before the fault", c.Before.Error
			}
			r.Unverified = append(r.Unverified, Problem{
				Rule:  RuleInvariantUnevaluated,
				Title: "An invariant could not be asked of this database",
				Detail: fmt.Sprintf("invariant %s was not asked %s: %s. Whether the fault broke a rule this project "+
					"states about its own data is not established.", c.Name, which, oneLine(why)),
				Fix: "Check that the database answers a query after this fault and that the invariant's statement runs " +
					"against it by hand. An invariant that could not be asked is not an invariant that held.",
			})
		case !c.Before.Held:
			r.Unverified = append(r.Unverified, Problem{
				Rule:  RuleInvariantAlreadyViolated,
				Title: "An invariant was already violated before the fault",
				Detail: fmt.Sprintf("invariant %s did not hold BEFORE the fault, so nothing this run measured about it "+
					"is attributable to the fault. After the recovery %s.", c.Name, invariantAfterSays(c.After)),
				Fix: "Fix the data or the invariant and run the fault again. While it is violated before the fault goes " +
					"in, this run cannot say whether the fault breaks it.",
			})
		case !c.After.Held:
			r.Problems = append(r.Problems, Problem{
				Rule:  RuleInvariantBroken,
				Count: invariantRowCount(c.After),
				Title: "An invariant held before the fault and does not hold after the recovery",
				Detail: fmt.Sprintf("invariant %s returned no rows before the fault and %s after the recovery, "+
					"so the fault broke a rule this project states about its own data.%s",
					c.Name, invariantRowsSay(c.After), invariantBecause(c.Description)),
				Fix: "Keep this database. A rule that held before the crash and does not hold after it is a durability " +
					"defect in what the application wrote, not a problem with the check.",
			})
		}
	}
}

// invariantAfterSays is what the after side came out as, in a clause.
func invariantAfterSays(s InvariantSide) string {
	if s.Held {
		return "it holds, so the rows that violated it before the fault are no longer there"
	}
	return "it still does not hold and " + invariantRowsSay(s)
}

// invariantRowsSay is how many rows violate an invariant, said honestly.
//
// "more than" rather than a total, because engine/internal/invariant stops the
// statement once it has enough rows to show and deliberately does not count
// the rest. Printing the kept count as a total would understate a check that
// matched a million rows by a million.
func invariantRowsSay(s InvariantSide) string {
	switch {
	case s.More:
		return fmt.Sprintf("more than %d rows violate it", len(s.Rows))
	case len(s.Rows) == 1:
		return "1 row violates it"
	default:
		return fmt.Sprintf("%d rows violate it", len(s.Rows))
	}
}

// invariantRowCount is how many violating rows a finding covers, and zero when
// the true number is larger than what was kept. Zero rather than the kept
// count, because Count is read as a total and "5" beside a check that matched
// a million would be wrong in the direction that reassures.
func invariantRowCount(s InvariantSide) int {
	if s.More {
		return 0
	}
	return len(s.Rows)
}

// invariantBecause appends the manifest's own description of the rule, which
// is the sentence that says what the violation MEANS to this project. The
// engine cannot write it and the project already has.
func invariantBecause(description string) string {
	d := oneLine(description)
	if d == "" {
		return ""
	}
	return " The manifest says of it: " + d
}

// oneLine flattens text that came from a database or an error into something
// that fits in a finding, which is read in a terminal line and in a pull
// request comment.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
