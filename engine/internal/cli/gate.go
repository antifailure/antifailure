package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/egress"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/gate"
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
// finding is evidence rather than an error, and the seventeen migration lint rules
// already established that the thing somebody greps for six months later is
// the rule name. Every rule below is also a key in the manifest's policy
// block, so the answer to "why did this fail" is always a key a person can go
// and read.
const (
	ruleMigrationFailed  = gate.RuleMigrationFailed
	ruleMigrationLock    = gate.RuleMigrationLock
	ruleMigrationRewrite = gate.RuleMigrationRewrite
	rulePlanRegression   = gate.RulePlanRegression
	ruleQueryRegression  = gate.RuleQueryRegression
	ruleLoadRegression   = "load_regression"
	ruleEgressSurprise   = egress.RuleSurprise
	ruleMasking          = "masking"
	ruleCleanup          = "cleanup"

	ruleWorkflowsUnverified = "workflows_unverified"
)

// migrationFindings delegates to the deterministic evaluator.
//
// The evaluator moved to engine/internal/gate so that af ci and the MCP server
// rank a migration identically. This wrapper is what keeps the call sites and
// the tests in this package reading as they did.
func migrationFindings(full insights.Full, p report.Policy) ([]report.Finding, *report.Migration) {
	return gate.MigrationFindings(full, p)
}

// egressFinding delegates to the shared reader, so that af ci and the MCP
// server cannot disagree about what a surprise host is.
func egressFinding(e *report.Egress, p report.Policy) *report.Finding {
	return egress.Finding(e, p)
}

// workflowsUnverifiedFinding is a whole run that reached no verdict about the
// application.
//
// The per workflow rule is deliberately untouched: one blocked workflow is a
// gap in our tooling and is never counted against the application, because an
// incomplete environment must not be indistinguishable from a broken one.
//
// This is the other claim, and the two were being conflated. A run in which
// every workflow was blocked has not declined to blame the application; it has
// not looked at it, and exiting zero tells the pipeline that it did. Both of
// this repository's own answers were the wrong half of that: `af ci` on the
// control plane reported "6 workflows could not be carried through" and exited
// zero on every run, and the whole example corpus reported "Nothing ran" and
// went green.
//
// Counted from the workflows rather than from the count of results, so a
// manifest that declares none lands here too. That is the same failure wearing
// a different hat: nothing was tested either way.
func workflowsUnverifiedFinding(run report.Run, p report.Policy) *report.Finding {
	if p.WorkflowsUnverified == report.LevelIgnore {
		return nil
	}
	if !run.NothingVerified() {
		return nil
	}
	title := "No workflow reached a verdict about the application."
	detail := "Every workflow was blocked or proved nothing either way, so this run " +
		"says nothing about whether the application works."
	if len(run.Workflows) == 0 {
		title = "No workflows ran, so nothing about the application was checked."
		detail = "The manifest declares no workflows, so there was nothing to carry through."
	}
	return &report.Finding{
		Rule: ruleWorkflowsUnverified, Level: p.WorkflowsUnverified,
		Count: len(run.Workflows), Where: "the workflows",
		Title: title, Detail: detail,
		Fix: "Read the workflow rows for what stopped each one. If the project has no " +
			"workflows yet, set policy.workflows_unverified to warn so the choice is " +
			"recorded rather than assumed.",
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
	case ruleWorkflowsUnverified:
		return aferrors.Coded(aferrors.AFAGT007, "detail", f.Detail)
	default:
		// Every migration finding, including the seventeen lint rules, which have
		// rule names of their own.
		return aferrors.Coded(aferrors.AFDB031, "rule", f.Rule, "detail", f.Title)
	}
}

// plural renders a count with its noun, matching the report package's.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
