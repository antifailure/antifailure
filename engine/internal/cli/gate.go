package cli

import (
	"fmt"
	"strconv"
	"strings"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// The release gate: everything a run measured, turned into findings a policy
// has already ranked.
//
// These are pure functions on purpose. The decision about whether a change
// ships is the part of af ci most worth testing and the part hardest to reach
// through a command that wants Docker, a Postgres and a browser, so the
// decision lives here with the I/O left in ci.go.
//
// Rule names are the stable identifiers and they carry no error code. A
// finding is evidence rather than an error, and the six migration lint rules
// already established that the thing somebody greps for six months later is
// the rule name. Every rule below is also a key in the manifest's policy
// block, so the answer to "why did this fail" is always a key a person can go
// and read.
const (
	ruleMigrationFailed  = "migration_failed"
	ruleMigrationLock    = "migration_lock"
	ruleMigrationRewrite = "migration_rewrite"
	rulePlanRegression   = "plan_regression"
	ruleQueryRegression  = "query_regression"
	ruleLoadRegression   = "load_regression"
	ruleEgressSurprise   = "egress_surprise"
	ruleMasking          = "masking"
	ruleCleanup          = "cleanup"
)

// migrationFindings turns an insights run into findings and the section that
// shows the evidence behind them.
//
// Worst first within the migration checks, which is the order the checks are
// appended in: a migration that did not apply, then what it locked, then what
// it rewrote, then what the lint objected to, then the plans, then the query
// counts. Somebody who reads only the first finding has read the most
// important one.
func migrationFindings(full insights.Full, p report.Policy) ([]report.Finding, *report.Migration) {
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
				Rule: ruleMigrationFailed, Level: p.MigrationFailed,
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
				Rule: ruleMigrationLock, Level: level, Where: l.Table,
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
				Rule: ruleMigrationRewrite, Level: p.MigrationRewrite,
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
				// governs all six together and the finding says which one it
				// was, because "migration_lint" in a comment tells nobody what
				// to change.
				Rule: string(l.Rule), Level: p.MigrationLint, Where: l.Table,
				Title:  sentence(l.Rule.Title()),
				Detail: lintDetail(l),
				Fix:    l.Fix,
			})
		}
	}

	for _, pf := range full.PlanFindings {
		out = append(out, report.Finding{
			Rule: rulePlanRegression, Level: p.PlanRegression, Where: pf.Table,
			Title:  planTitle(pf.Kind),
			Detail: pf.Detail + " " + pf.Statement,
			Fix:    "Check the index the planner stopped using, and the statistics on that table.",
		})
	}

	if d := full.Regression; d != nil && !d.Empty() {
		out = append(out, report.Finding{
			Rule: ruleQueryRegression, Level: p.QueryRegression,
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
func egressFinding(e *report.Egress, p report.Policy) *report.Finding {
	if e == nil || len(e.Surprises) == 0 || p.EgressSurprise == report.LevelIgnore {
		return nil
	}
	return &report.Finding{
		Rule: ruleEgressSurprise, Level: p.EgressSurprise,
		Count: len(e.Surprises), Where: strings.Join(e.Surprises, ", "),
		Title: fmt.Sprintf("The environment tried to reach %s nothing in the manifest mentions.",
			plural(len(e.Surprises), "host", "hosts")),
		Detail: "The request was refused, so nothing left the environment. It is usually a " +
			"dependency somebody added without noticing.",
		Fix: "Add an egress rule for it with the mode you intend, or leave it blocked and " +
			"set policy.egress_surprise to warn.",
	}
}

// maskingFinding is the environment's own branch reading back with something
// in it that still parses as real data.
func maskingFinding(v *report.Verification, p report.Policy) *report.Finding {
	if v == nil || v.Unavailable != "" || v.Clean || p.Masking == report.LevelIgnore {
		return nil
	}
	return &report.Finding{
		Rule: ruleMasking, Level: p.Masking,
		Title: fmt.Sprintf("The branch read back with %s that still parses as real data.",
			plural(len(v.Findings), "column", "columns")),
		Detail: strings.Join(v.Findings, " "),
		Fix:    "Add a masking rule for each column and refresh the golden. The value itself is never printed.",
	}
}

// cleanupFinding is teardown leaving something behind.
//
// Nil when teardown was not attempted, which is what --keep asks for. A run
// that deliberately kept its environment has not failed to clean up.
func cleanupFinding(c *report.Cleanup, p report.Policy) *report.Finding {
	if c == nil || p.Cleanup == report.LevelIgnore {
		return nil
	}
	if c.Error == "" && len(c.Pending) == 0 {
		return nil
	}
	title := fmt.Sprintf("Teardown left %s behind.", plural(len(c.Pending), "resource", "resources"))
	detail := strings.Join(c.Pending, "; ")
	if c.Error != "" {
		title = "Teardown failed."
		detail = c.Error
	}
	count := len(c.Pending)
	if c.Error != "" {
		count++
	}
	return &report.Finding{
		Rule: ruleCleanup, Level: p.Cleanup, Count: count, Title: title, Detail: detail,
		Fix: "Run 'af down' against this environment once the provider is reachable. The " +
			"journal remembers what is left.",
	}
}

// loadFinding is a load threshold from the manifest being exceeded.
//
// The thresholds were already compared and the breaches were already listed;
// what was missing is that they changed nothing. A run whose p95 doubled
// reported pass.
func loadFinding(l *report.Load, p report.Policy) *report.Finding {
	if l == nil || len(l.Regressed) == 0 || p.LoadRegression == report.LevelIgnore {
		return nil
	}
	return &report.Finding{
		Rule: ruleLoadRegression, Level: p.LoadRegression,
		Count: len(l.Regressed), Where: strings.Join(l.Regressed, ", "),
		Title: fmt.Sprintf("Load crossed %s the manifest sets.",
			plural(len(l.Regressed), "threshold", "thresholds")),
		Detail: fmt.Sprintf("%d requests at %.0f a second, p95 %.0fms, %.1f%% failed.",
			l.Sent, l.Rate, l.P95Ms, l.ErrorRate*100),
		Fix: "Raise the threshold in load.thresholds, or fix the regression.",
	}
}

// gateError is the error a failing finding exits with.
//
// Every failing class maps to a code from the catalog, because the exit code
// is the only part of this a script keeps and "1" tells a pipeline nothing. The
// codes are reused rather than invented where one already says the right
// thing: unmasked data really is AF-MSK-002 and a teardown that left resources
// behind really is AF-RUN-030, whichever command noticed.
func gateError(f report.Finding) error {
	switch f.Rule {
	case ruleEgressSurprise:
		return aferrors.Coded(aferrors.AFNET013, "hosts", f.Where)
	case ruleMasking:
		return aferrors.Coded(aferrors.AFMSK002,
			"detector", "the verification scan", "table", "the branch", "column", "see above")
	case ruleCleanup:
		return aferrors.Coded(aferrors.AFRUN030, "count", strconv.Itoa(f.Count))
	case ruleLoadRegression:
		return aferrors.Coded(aferrors.AFLOD011, "count", strconv.Itoa(f.Count))
	default:
		// Every migration finding, including the six lint rules, which have
		// rule names of their own.
		return aferrors.Coded(aferrors.AFDB031, "rule", f.Rule, "detail", f.Title)
	}
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

// plural renders a count with its noun, matching the report package's.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
