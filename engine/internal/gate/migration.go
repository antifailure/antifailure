// Package gate turns what a rehearsal observed into findings ranked by the
// project's own policy.
//
// It is the deterministic evaluator. Nothing here reads a threshold from
// anywhere but the report.Policy it is handed, which is resolved from the
// manifest, so a caller cannot soften a verdict by asking differently. It
// lives in its own package because two front ends now need the same answer:
// af ci, which decides whether a merge is blocked, and the MCP server, which
// tells a model the same thing. Two implementations would drift, and the one
// that drifted would be the one a model believed.
package gate

import (
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// The manifest policy keys for the migration checks. Exported because a
// caller that renders a finding needs to recognise them, and because the
// command line aliases these rather than keeping a second spelling.
const (
	RuleMigrationFailed  = "migration_failed"
	RuleMigrationLock    = "migration_lock"
	RuleMigrationRewrite = "migration_rewrite"
	RuleMigrationLint    = "migration_lint"
	RulePlanRegression   = "plan_regression"
	RuleQueryRegression  = "query_regression"
)

// migrationFindings turns an insights run into findings and the section that
// shows the evidence behind them.
//
// Worst first within the migration checks, which is the order the checks are
// appended in: a migration that did not apply, then what it locked, then what
// it rewrote, then what the lint objected to, then the plans, then the query
// counts. Somebody who reads only the first finding has read the most
// important one.
func MigrationFindings(full insights.Full, p report.Policy) ([]report.Finding, *report.Migration) {
	var out []report.Finding
	m := &report.Migration{Notes: append([]string(nil), full.Missing...)}
	for _, off := range full.Off {
		m.Notes = append(m.Notes, "turned off in the manifest: "+off)
	}

	if r := full.Rehearsal; r != nil {
		m.Tool = string(r.Tool)
		m.Pending = len(r.Pending)
		m.TotalMS = r.TotalMS

		if r.Failed {
			out = append(out, report.Finding{
				Rule: RuleMigrationFailed, Level: p.MigrationFailed,
				Title:  "The migrations did not apply to a branch with production's shape in it.",
				Detail: r.Error,
				Fix: "A migration that fails on a branch of the golden is one that would have " +
					"failed in production. The rehearsal names the statement.",
			})
		}

		for _, l := range r.Locks {
			level := p.LockLevel(l.HeldMS)
			if level == report.LevelIgnore && !l.Blocking {
				continue
			}
			m.Locks = append(m.Locks, report.Lock{
				Table: l.Table, Mode: l.Mode, HeldMS: l.HeldMS, Blocking: l.Blocking,
			})
			if level == report.LevelIgnore {
				continue
			}
			detail := fmt.Sprintf(
				"Sampled every %s, so it was held at least that long and probably longer.",
				insights.LockSampleInterval)
			if l.Blocking {
				detail = "Another session was seen waiting on it. " + detail
			}
			out = append(out, report.Finding{
				Rule: RuleMigrationLock, Level: level, Where: l.Table,
				Title: fmt.Sprintf("A migration held %s on %s for at least %s.",
					l.Mode, l.Table, msWord(l.HeldMS)),
				Detail: detail,
				Fix: "Split the statement, or take the lock in a transaction that does nothing " +
					"else, so the window is the statement rather than the migration.",
			})
		}

		for _, st := range r.Slowest(5) {
			if st.MS < 1 && len(st.Rewrote) == 0 {
				continue
			}
			m.Slowest = append(m.Slowest, report.Statement{
				SQL: st.SQL, MS: st.MS, Rewrote: st.Rewrote,
			})
		}
		if rewrote := r.Rewrote(); len(rewrote) > 0 {
			out = append(out, report.Finding{
				Rule: RuleMigrationRewrite, Level: p.MigrationRewrite,
				Where: strings.Join(rewrote, ", "),
				Title: fmt.Sprintf("Postgres rewrote %s.", plural(len(rewrote), "table", "tables")),
				Detail: "A rewrite copies every row under a lock nothing can read through, " +
					"reported by the database rather than guessed from the statement.",
				Fix: "On a table this size, do the change in steps that do not rewrite: add, " +
					"backfill, then swap.",
			})
		}

		for _, l := range r.Lint {
			out = append(out, report.Finding{
				// The lint rule's own name, not migration_lint. The policy key
				// governs all seventeen together and the finding says which
				// one it was, because "migration_lint" in a comment tells
				// nobody what to change.
				Rule: string(l.Rule), Level: p.MigrationLint, Where: l.Table,
				Title:  sentence(l.Rule.Title()),
				Detail: lintDetail(l),
				Fix:    l.Fix,
			})
		}
	}

	for _, pf := range full.PlanFindings {
		out = append(out, report.Finding{
			Rule: RulePlanRegression, Level: p.PlanRegression, Where: pf.Table,
			Title:  planTitle(pf.Kind),
			Detail: pf.Detail + " " + pf.Statement,
			Fix:    "Check the index the planner stopped using, and the statistics on that table.",
		})
	}

	if d := full.Regression; d != nil && !d.Empty() {
		out = append(out, report.Finding{
			Rule: RuleQueryRegression, Level: p.QueryRegression,
			Title:  regressionTitle(*d),
			Detail: regressionDetail(*d),
			Fix: "Compare against the baseline saved on the base branch. A query running 412 " +
				"times means nothing without knowing it ran 4 times before.",
		})
	}
	return out, m
}

// lintDetail puts the row count in the sentence rather than in a footnote,
// because it is what turns "this rewrites the table" from a note into an
// outage.
func lintDetail(l insights.LintFinding) string {
	if l.Table == "" || l.Rows == 0 {
		return l.Detail
	}
	return fmt.Sprintf("%s The branch holds about %d rows in %s.", l.Detail, l.Rows, l.Table)
}

func planTitle(k insights.PlanChange) string {
	switch k {
	case insights.PlanNewSeqScan:
		return "A table is now read end to end that was not before."
	case insights.PlanLostIndex:
		return "An index the planner used before is no longer used."
	default:
		return "The planner's cost estimate grew."
	}
}

func regressionTitle(d insights.Diff) string {
	var parts []string
	if n := len(d.Slower); n > 0 {
		parts = append(parts, plural(n, "statement is slower", "statements are slower"))
	}
	if n := len(d.Busier); n > 0 {
		parts = append(parts, plural(n, "statement runs more often", "statements run more often"))
	}
	if n := len(d.NewQueries); n > 0 {
		parts = append(parts, plural(n, "statement is new", "statements are new"))
	}
	return "Against the baseline, " + strings.Join(parts, ", ") + "."
}

func regressionDetail(d insights.Diff) string {
	var b strings.Builder
	for _, c := range d.Slower {
		fmt.Fprintf(&b, "%.1f times slower, %.2fms then and %.2fms now: %s ", c.Factor, c.Before, c.After, c.Text)
	}
	for _, c := range d.Busier {
		fmt.Fprintf(&b, "%.0f times more often, %.0f then and %.0f now: %s ", c.Factor, c.Before, c.After, c.Text)
	}
	for _, q := range d.NewQueries {
		fmt.Fprintf(&b, "new, %d calls and %.1fms in total: %s ", q.Calls, q.TotalMs, q.Text)
	}
	return strings.TrimSpace(b.String())
}

// egressFinding is the environment reaching for something the manifest does
// not mention.
//
// The request was refused either way: the twin sits on a network with no route
// out and the proxy decided against it. What this adds is whether the attempt
// stops the merge, which until now it did not, so a run whose environment tried
// to reach an unknown host still reported pass and exited zero.
// msWord prints milliseconds the way somebody reads them.
func msWord(ms float64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%.0fms", ms)
	case ms < 60000:
		return fmt.Sprintf("%.1fs", ms/1000)
	default:
		return fmt.Sprintf("%dm%02ds", int(ms)/60000, (int(ms)%60000)/1000)
	}
}

// plural and sentence are duplicated from the command line's output helpers
// rather than shared. They are three lines of grammar each, and a package
// dependency in this direction purely to avoid retyping "%d %s" would be a
// worse trade than the retyping.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// sentence capitalises a rule title so it reads beside the findings written as
// sentences. The lint rules name themselves in lower case, because they are
// headings elsewhere.
func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:] + "."
}
