package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/gate"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/report"
)

// rehearseMigrations runs the migration rehearsal for one submitted run.
//
// It is a function value so that the tool can be built against a fake in
// tests. The real one is in command.go and calls Orchestrator.RunInsights,
// which is the same code path af insights and af ci take.
type rehearseMigrations func(ctx context.Context, runID string) (insights.Full, error)

// newRehearseMigrationTool builds rehearse_migration_safety.
func newRehearseMigrationTool(p *Project, eng *Engine, run rehearseMigrations) *Tool {
	return &Tool{
		Name:  "rehearse_migration_safety",
		Title: "Rehearse a migration",
		Description: "Apply this branch's pending migrations to a throwaway branch of a " +
			"sanitized copy of production, and report what they would do: which statements " +
			"were slow, which tables Postgres rewrote, which locks were held and for how " +
			"long, and what the schema linter objected to at production's table sizes. " +
			"Nothing is applied to a real database and no production credential is used. " +
			"Thresholds come from the project's manifest and cannot be set from here. " +
			"This takes minutes, so it returns a run_id immediately: poll it with " +
			"get_rehearsal_run. A verdict of INCONCLUSIVE means the rehearsal did not " +
			"finish and says nothing about the migration.",
		Input: &Schema{
			Type: "object",
			Properties: map[string]*Schema{
				"project_id":      projectIDSchema(),
				"idempotency_key": idempotencyKeySchema(),
				"repository_file": {
					Type: "string", MaxLength: 1024, MinLength: 1,
					Description: "Optional. A migration file in this repository that the " +
						"rehearsal is about, recorded with the hash of its bytes so the run " +
						"can be tied to an exact revision. It must resolve to a regular file " +
						"inside the checkout. It does not select which migrations run: every " +
						"pending migration is rehearsed, because a migration cannot be judged " +
						"apart from the ones that run before it.",
				},
				"hypothesis": {
					Type: "string", MaxLength: 2000,
					Description: "Optional. What you expect this migration to do, in your own " +
						"words. Recorded with the run so the result can be read against the " +
						"expectation. It is never executed and never changes what is measured.",
				},
			},
		},
		Handler: func(_ context.Context, call *Call, args map[string]any) (any, *Fault) {
			if fault := p.checkAssertion(args); fault != nil {
				return nil, fault
			}

			// Resolved at submission rather than inside the experiment, so a
			// bad path is refused immediately with a useful message instead of
			// being reported minutes later as a failed run.
			var file *CheckedFile
			if raw, ok := args["repository_file"].(string); ok && raw != "" {
				var fault *Fault
				file, fault = resolveInRoot(p.Root, raw)
				if fault != nil {
					return nil, fault
				}
			}
			hypothesis, _ := args["hypothesis"].(string)

			return eng.Submit(call, "rehearse_migration_safety", args,
				func(ctx context.Context, runID string) (string, *ResultBody, *Fault) {
					return runMigrationRehearsal(ctx, p, eng, run, runID, file, hypothesis)
				})
		},
	}
}

func runMigrationRehearsal(
	ctx context.Context, p *Project, eng *Engine, run rehearseMigrations,
	runID string, file *CheckedFile, hypothesis string,
) (string, *ResultBody, *Fault) {
	if eng.Cancelled(ctx, runID) {
		return "", nil, faultf(FaultRunNotCancellable, "This run was cancelled before it started.")
	}
	eng.Phase(ctx, runID, "rehearsing the migrations against a branch of the golden")

	full, err := run(ctx, runID)
	if err != nil {
		// The rehearsal could not be performed. That is not evidence about the
		// migration, so it is a failed run and its verdict is INCONCLUSIVE.
		// Reporting it as a pass because nothing was found would be the
		// monitoring failure this system exists to refuse.
		return "", nil, &Fault{
			Code: FaultSafetyUnavailable,
			Detail: "The rehearsal could not be run, so this says nothing about the " +
				"migration. A rehearsal needs a verified golden to branch from and a " +
				"database provider to branch it with. The server log says which was missing.",
			Retryable: true,
			wrapped:   err,
		}
	}

	eng.Phase(ctx, runID, "ranking what the rehearsal found")

	// The deterministic evaluator, with the project's own thresholds. This is
	// the same call af ci makes, so a verdict here and a verdict in CI cannot
	// disagree about the same migration.
	findings, migration := gate.MigrationFindings(full, p.Gate)
	page, withheld := safeFindings(findings, migration)

	body := &ResultBody{
		Findings: page,
		Metrics:  migrationMetrics(full, migration, p.Gate),
		Evidence: migrationEvidence(full, migration, file),
		Detail:   safeMigration(migration),
	}

	native := nativeVerdict(full, findings)
	body.Summary = migrationSummary(full, migration, findings, native, withheld, file, hypothesis)
	return native, body, nil
}

// nativeVerdict maps the rehearsal onto the engine's own vocabulary.
//
// It does not call report.Run.Verdict, and that is deliberate rather than
// laziness. Verdict is written for a whole CI run and answers "blocked" when
// there are no workflows, which is correct there and wrong here: a rehearsal
// has no workflows by design. What is reused instead is Counts, which is the
// rule Verdict itself applies to findings, so the ranking is shared even
// though the shape of the run is not.
func nativeVerdict(full insights.Full, findings []report.Finding) string {
	if full.Rehearsal == nil {
		// Nothing was rehearsed. Every other field may be populated and none
		// of it is evidence about the migrations, so this is unverified
		// rather than a pass with no findings.
		return report.VerdictUnverified
	}
	fail, warn := report.Run{Findings: findings}.Counts()
	switch {
	case fail > 0:
		return report.VerdictFail
	case warn > 0:
		return report.VerdictWarn
	default:
		return report.VerdictPass
	}
}

func migrationMetrics(full insights.Full, m *report.Migration, p report.Policy) []Metric {
	if m == nil {
		return nil
	}
	out := []Metric{
		{Name: "migration_total_ms", Value: m.TotalMS, Unit: "ms"},
		{Name: "pending_migrations", Value: float64(m.Pending), Unit: "migrations"},
	}
	// The worst lock is the measurement the verdict most often turns on, so
	// it is reported against the manifest's own threshold rather than left
	// for a caller to compare by hand.
	var worst float64
	for _, l := range m.Locks {
		if l.HeldMS > worst {
			worst = l.HeldMS
		}
	}
	failMS := p.LockFailMS
	out = append(out, Metric{
		Name: "longest_lock_held_ms", Value: worst, Unit: "ms",
		Threshold: &failMS, Breached: worst >= p.LockFailMS,
	})
	if full.Rehearsal != nil {
		out = append(out, Metric{
			Name: "lint_findings", Value: float64(len(full.Rehearsal.Lint)), Unit: "findings",
		})
		out = append(out, Metric{
			Name: "tables_locked", Value: float64(len(full.Rehearsal.Locks)), Unit: "tables",
		})
	}
	return out
}

// migrationEvidence points at where the detail lives, and never carries it.
func migrationEvidence(full insights.Full, m *report.Migration, file *CheckedFile) []Evidence {
	var out []Evidence
	if file != nil {
		out = append(out, Evidence{
			URI: "repo://" + file.Rel, Kind: "migration_file",
			Note: fmt.Sprintf(
				"The file this run was submitted about, sha256 %s. Its contents are not "+
					"reproduced in this result.", file.SHA256),
		})
	}
	out = append(out, Evidence{
		URI: "af://insights", Kind: "command",
		Note: "Run af insights for the full rehearsal, including the statement text this " +
			"result withholds.",
	})
	if m != nil && len(m.Locks) > 0 {
		out = append(out, Evidence{
			URI: "af://insights#locks", Kind: "command",
			Note: fmt.Sprintf("%d tables were locked during the rehearsal.", len(m.Locks)),
		})
	}
	if full.Rehearsal != nil && len(full.Rehearsal.Statements) > 0 {
		out = append(out, Evidence{
			URI: "af://insights#statements", Kind: "command",
			Note: fmt.Sprintf("%d statements ran, timed individually.",
				len(full.Rehearsal.Statements)),
		})
	}
	return out
}

func migrationSummary(
	full insights.Full, m *report.Migration, findings []report.Finding,
	native string, withheld int, file *CheckedFile, hypothesis string,
) string {
	var b strings.Builder

	if full.Rehearsal == nil {
		b.WriteString("The migrations were not rehearsed, so this says nothing about them. ")
		for _, note := range full.Missing {
			fmt.Fprintf(&b, "%s ", neutralize(note, 300))
		}
		if len(full.Off) > 0 {
			fmt.Fprintf(&b, "Turned off in the manifest: %s. ",
				neutralize(strings.Join(full.Off, ", "), 200))
		}
		return strings.TrimSpace(b.String())
	}

	fmt.Fprintf(&b, "Rehearsed %d pending %s against a throwaway branch of the golden, "+
		"taking %.0fms in total. ",
		m.Pending, plural(m.Pending, "migration", "migrations"), m.TotalMS)

	if len(m.Locks) > 0 {
		var worst float64
		worstTable := ""
		for _, l := range m.Locks {
			if l.HeldMS > worst {
				worst, worstTable = l.HeldMS, l.Table
			}
		}
		fmt.Fprintf(&b, "The longest lock was held on %s for at least %.0fms. ",
			neutralize(worstTable, maxIdentifierBytes), worst)
	}

	fail, warn := report.Run{Findings: findings}.Counts()
	switch {
	case fail > 0:
		fmt.Fprintf(&b, "%d %s stop a merge under this project's policy and %d %s reported only. ",
			fail, plural(fail, "finding", "findings"), warn, plural(warn, "is", "are"))
	case warn > 0:
		fmt.Fprintf(&b, "Nothing stops a merge. %d %s reported for attention. ",
			warn, plural(warn, "finding is", "findings are"))
	default:
		b.WriteString("Nothing the project's policy treats as a problem. ")
	}

	if withheld > 0 {
		fmt.Fprintf(&b,
			"%d %s had their detail withheld because it quotes the candidate branch, "+
				"which is untrusted input; read those with af insights. ",
			withheld, plural(withheld, "finding", "findings"))
	}
	if file != nil {
		fmt.Fprintf(&b, "Submitted about %s at sha256 %s. ", file.Rel, short(file.SHA256))
	}
	if hypothesis != "" {
		// Echoed back neutralised, and labelled as the caller's own words so
		// that nothing in it reads as a finding this server made.
		fmt.Fprintf(&b, "Your stated hypothesis, unevaluated: %q.",
			neutralize(hypothesis, 500))
	}
	return strings.TrimSpace(b.String())
}

// plural picks the noun.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// short abbreviates a hash for prose. The full one is in the evidence.
func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}
