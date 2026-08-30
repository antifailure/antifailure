package insights

import (
	"fmt"
	"strings"
)

// ruleTitle is the one line summary of each rule, used as a heading in the
// report. The rationale and the fix live on the finding, because they depend
// on the table it found.
var ruleTitle = map[Rule]string{
	RuleNotNullNoDefault:   "NOT NULL column added with no default",
	RuleAlterColumnType:    "column type change that rewrites the table",
	RuleIndexNotConcurrent: "index built without CONCURRENTLY",
	RuleForeignKeyNotValid: "foreign key added without NOT VALID",
	RuleRenameColumnInUse:  "column renamed while something still reads it",
	RuleDropColumnInView:   "column dropped while a view still selects it",

	RuleNoLockTimeout:          "no lock_timeout, so a lock wait becomes an outage",
	RuleSetNotNull:             "NOT NULL set on a column that already exists",
	RuleCheckNotValid:          "CHECK constraint added without NOT VALID",
	RuleUniqueConstraint:       "unique constraint that builds its index in place",
	RuleBackfillWithDDL:        "rows changed in the same transaction as the schema",
	RuleDropIndexNotConcurrent: "index dropped without CONCURRENTLY",
	RuleReindexNotConcurrent:   "index rebuilt without CONCURRENTLY",
	RuleVacuumFull:             "VACUUM FULL, which rewrites the table offline",
	RuleCluster:                "CLUSTER, which rewrites the table offline",
	RuleDropTable:              "table dropped",
	RuleTruncate:               "table truncated",
}

// Title is the rule's one line summary.
func (r Rule) Title() string {
	if t, ok := ruleTitle[r]; ok {
		return t
	}
	return string(r)
}

// Explain renders every check for a person.
//
// Worst first, and the worst thing is always a migration that did not apply,
// because that is a deploy that will fail. Then what a migration will do to a
// table this size, then the plans, then the query counts. Somebody who reads
// only the first section should have read the most important one.
func (f Full) Explain() string {
	var b strings.Builder

	if r := f.Rehearsal; r != nil {
		b.WriteString(r.Explain())
	}

	// Second, because a previous release that cannot talk to the new schema is
	// an outage during the deploy window rather than a slow deploy, and the
	// only thing above it is a migration that did not apply at all.
	b.WriteString(f.Rolling.Explain())

	if len(f.PlanFindings) > 0 {
		b.WriteString("Query plans that changed:\n")
		for _, p := range f.PlanFindings {
			fmt.Fprintf(&b, "  %s\n    %s\n    %s\n", planTitle(p.Kind), p.Detail, p.Statement)
		}
		b.WriteString("\n")
	}

	if d := f.Regression; d != nil && !d.Empty() {
		b.WriteString("What got worse against the baseline:\n")
		for _, c := range d.Busier {
			fmt.Fprintf(&b, "  %.0f times more often, %.0f then and %.0f now\n    %s\n",
				c.Factor, c.Before, c.After, c.Text)
		}
		for _, c := range d.Slower {
			fmt.Fprintf(&b, "  %.1f times slower, %.2fms then and %.2fms now\n    %s\n",
				c.Factor, c.Before, c.After, c.Text)
		}
		for _, q := range d.NewQueries {
			fmt.Fprintf(&b, "  new: %d calls, %.1fms total\n    %s\n", q.Calls, q.TotalMs, q.Text)
		}
		b.WriteString("\n")
	}

	b.WriteString(f.Stats.Explain())

	for _, o := range f.Off {
		fmt.Fprintf(&b, "Turned off in the manifest: %s\n", o)
	}
	for _, m := range f.Missing {
		fmt.Fprintf(&b, "Not measured: %s\n", m)
	}
	return b.String()
}

// Explain renders the rehearsal on its own, which is what the pull request
// comment shows when nothing else found anything.
func (r Rehearsal) Explain() string {
	var sb strings.Builder
	b := &sb
	if r.Failed {
		fmt.Fprintf(b, "The migrations did not apply:\n  %s\n\n", r.Error)
	}

	if len(r.Pending) == 0 && !r.Failed {
		if r.Tool != "" {
			fmt.Fprintf(b, "Migrations: %s, nothing pending on this branch.\n\n", r.Tool)
		}
	} else {
		fmt.Fprintf(b, "Migrations rehearsed: %d pending, %s in total.\n",
			len(r.Pending), duration(r.TotalMS))
		for _, s := range r.Slowest(5) {
			if s.MS < 1 && len(s.Rewrote) == 0 {
				continue
			}
			line := fmt.Sprintf("  %8s  %s", duration(s.MS), s.SQL)
			if len(s.Rewrote) > 0 {
				line += "\n            rewrote " + strings.Join(s.Rewrote, ", ") +
					", which copies every row under a lock nothing can read through"
			}
			fmt.Fprintln(b, line)
		}
		b.WriteString("\n")
	}

	if len(r.Lint) > 0 {
		b.WriteString("What these migrations do to a table this size:\n")
		for _, l := range r.Lint {
			fmt.Fprintf(b, "  %s\n", l.Rule.Title())
			if l.Table != "" {
				fmt.Fprintf(b, "    on %s, about %d rows\n", l.Table, l.Rows)
			}
			fmt.Fprintf(b, "    %s\n    Instead: %s\n    %s\n", l.Detail, l.Fix, l.Statement)
		}
		b.WriteString("\n")
	}

	if held := blockingLocks(r.Locks); len(held) > 0 {
		b.WriteString("Locks held while the migrations ran:\n")
		for _, l := range held {
			line := fmt.Sprintf("  %-28s %s for at least %s", l.Table, l.Mode, duration(l.HeldMS))
			if l.Blocking {
				line += ", with another session waiting on it"
			}
			fmt.Fprintln(b, line)
		}
		b.WriteString("\nSampled every " + LockSampleInterval.String() +
			", so each figure is a lower bound rather than a measurement.\n\n")
	}
	return sb.String()
}

// blockingLocks drops the locks that cost nothing. Every statement takes a
// lock; the ones worth a line are the ones that stop other work.
func blockingLocks(all []LockHold) []LockHold {
	var out []LockHold
	for _, l := range all {
		if lockStrength[l.Mode] >= lockStrength["ShareLock"] || l.Blocking {
			out = append(out, l)
		}
	}
	return out
}

func planTitle(k PlanChange) string {
	switch k {
	case PlanNewSeqScan:
		return "a table is now read end to end"
	case PlanLostIndex:
		return "an index is no longer used"
	default:
		return "the planner's estimate grew"
	}
}

// duration prints milliseconds the way somebody reads them: a migration that
// took 94000ms is a migration that took a minute and a half, and the second
// form is the one that makes somebody stop.
func duration(ms float64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%.0fms", ms)
	case ms < 60000:
		return fmt.Sprintf("%.1fs", ms/1000)
	default:
		return fmt.Sprintf("%dm%02ds", int(ms)/60000, (int(ms)%60000)/1000)
	}
}
