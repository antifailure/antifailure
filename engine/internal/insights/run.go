package insights

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// PlanStatementLimit is how many statements the plan diff explains, taken by
// total time. Fifty is the spec's figure. Past that the tail is statements
// that ran once, and a plan change on a statement that ran once is not what
// takes a site down.
const PlanStatementLimit = 50

// The defaults, which MIRROR engine/internal/manifest's. They have to be
// stated twice because the manifest package fills in a manifest and this one
// fills in a block that reached it some other way, and a test asserts the two
// agree. They did not, once: the manifest normalised to 1.5 and 5 while this
// package used 2 and 10, so `af explain` printed one pair of thresholds and a
// caller that had not been through the normaliser used another.
const (
	DefaultRegressionFactor = 1.5
	DefaultRegressionMinMS  = 5
)

// Config is the manifest's insights block with every default resolved.
//
// It exists so that "is this check on" is answered in one place. Every field
// in the manifest is a *bool precisely so that false can be told from unset,
// and honouring that means never reading the pointer twice in two places and
// getting a different answer.
type Config struct {
	Enabled            bool
	MigrationRehearsal bool
	QueryRegression    bool
	PlanDiff           bool
	RegressionFactor   float64
	RegressionMinMS    float64
	LargeTableRows     int
}

// Configure resolves the manifest block. A nil block is the default, which is
// everything on: a project that has said nothing about insights gets them.
func Configure(in *schema.Insights) Config {
	c := Config{
		Enabled: true, MigrationRehearsal: true, QueryRegression: true, PlanDiff: true,
		RegressionFactor: DefaultRegressionFactor,
		RegressionMinMS:  DefaultRegressionMinMS,
		LargeTableRows:   LargeTableRows,
	}
	if in == nil {
		return c
	}
	on := func(p *bool, def bool) bool {
		if p == nil {
			return def
		}
		return *p
	}
	c.Enabled = on(in.Enabled, true)
	c.MigrationRehearsal = on(in.MigrationRehearsal, true)
	c.QueryRegression = on(in.QueryRegression, true)
	c.PlanDiff = on(in.PlanDiff, true)
	if in.RegressionFactor > 0 {
		c.RegressionFactor = in.RegressionFactor
	}
	if in.RegressionMinMS > 0 {
		c.RegressionMinMS = in.RegressionMinMS
	}
	if in.LargeTableRows > 0 {
		c.LargeTableRows = in.LargeTableRows
	}
	return c
}

// Thresholds is the query regression configuration in the form the diff wants.
func (c Config) Thresholds() Thresholds {
	return Thresholds{
		CallGrowth: c.RegressionFactor,
		TimeGrowth: c.RegressionFactor,
		MinMS:      c.RegressionMinMS,
	}
}

// Baseline is a report saved from an earlier run, to compare against.
//
// It carries the plans as well as the statistics because the two questions a
// pull request asks are "does this run more queries than main did" and "does
// main's plan for this query still hold", and answering the second one needs
// main's plans. The file is the transport because main and the pull request
// are two runs on two machines, and a file is what crosses between them.
type Baseline struct {
	Report Report `json:"report"`
	Plans  []Plan `json:"plans,omitempty"`
}

// Target is a database to rehearse migrations against.
//
// It must be a FRESH branch. Migrations are not required to be idempotent and
// most are not, so rehearsing against a branch something has already migrated
// either fails on the first statement or measures a no-op.
type Target struct {
	// Conn runs the schema capture and the plan captures.
	Conn *pgx.Conn
	// Watch is a second connection to the same database, for the lock
	// sampler. It has to be a second one: a lock held by a statement in
	// flight is invisible to the session holding it.
	Watch *pgx.Conn
	// URL is what the applier connects with.
	URL secrets.Value
	// Set is the repository's migrations.
	Set MigrationSet
	// Applier runs them.
	Applier Applier
}

// Options is everything Run needs.
type Options struct {
	Config Config
	// Branch is the environment's database, where the workload ran. It is
	// where the statistics come from.
	Branch *pgx.Conn
	// Limit is how many statements to report.
	Limit int
	// Rehearsal, when set, is the fresh branch to rehearse against. Nil means
	// no rehearsal was possible, which Run reports rather than passes over.
	Rehearsal *Target
	// Baseline is an earlier report to compare against, or nil.
	Baseline *Baseline
	// NoRehearsalReason says why Rehearsal is nil, when the caller knows.
	// Empty falls back to a general message, which is right when the caller
	// simply had nothing to rehearse.
	NoRehearsalReason string
	// Progress receives lines already safe to print.
	Progress func(string)
}

// Full is every check's answer.
type Full struct {
	// Stats is what the database noticed while the environment ran.
	Stats Report `json:"stats"`
	// Plans are this run's plans, for saving as somebody else's baseline.
	Plans []Plan `json:"plans,omitempty"`
	// Rehearsal is what happened when the migrations ran, or nil.
	Rehearsal *Rehearsal `json:"rehearsal,omitempty"`
	// Regression is what got worse against the baseline, or nil when there
	// was nothing to compare against.
	Regression *Diff `json:"regression,omitempty"`
	// PlanFindings are the plans that got worse.
	PlanFindings []PlanFinding `json:"plan_findings,omitempty"`
	// Off names the checks the manifest turned off. A report that silently
	// omits a check reads like a check that found nothing.
	Off []string `json:"off,omitempty"`
	// Missing names what could not be measured, and why.
	Missing []string `json:"missing,omitempty"`
}

// Clean reports whether every check that ran found nothing.
func (f Full) Clean() bool {
	if f.Rehearsal != nil && (f.Rehearsal.Failed || len(f.Rehearsal.Lint) > 0) {
		return false
	}
	if f.Regression != nil && !f.Regression.Empty() {
		return false
	}
	return len(f.PlanFindings) == 0
}

// Run performs every check the manifest leaves on.
func Run(ctx context.Context, opts Options) (Full, error) {
	var f Full
	if !opts.Config.Enabled {
		f.Off = append(f.Off, "every check, because insights.enabled is false")
		return f, nil
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}

	stats, err := Collect(ctx, opts.Branch, opts.Limit)
	if err != nil {
		return f, err
	}
	f.Stats = stats

	if opts.Config.QueryRegression {
		if opts.Baseline != nil {
			diff := stats.CompareTo(opts.Baseline.Report, opts.Config.Thresholds())
			f.Regression = &diff
		}
	} else {
		f.Off = append(f.Off, "query regression, because insights.query_regression is false")
	}

	// The statements to explain are the ones that cost the most, which is
	// where a plan change matters. They come from pg_stat_statements on the
	// environment's own database, so the set is the queries this application
	// actually ran rather than a list somebody maintains by hand: a list
	// maintained by hand goes stale exactly when a new query is added, which
	// is the change most likely to plan badly.
	var toExplain []string
	for i, q := range stats.Queries {
		if i >= PlanStatementLimit {
			break
		}
		if explainable(q.Text) {
			toExplain = append(toExplain, q.Text)
		}
	}

	if !opts.Config.MigrationRehearsal {
		f.Off = append(f.Off, "migration rehearsal, because insights.migration_rehearsal is false")
	}
	if !opts.Config.PlanDiff {
		f.Off = append(f.Off, "the plan diff, because insights.plan_diff is false")
	}

	// A rehearsal branch is the ideal base for a plan diff: it is the same
	// data as the branch, so the only difference between the plan before and
	// the plan after is the migrations. Nothing else has to be held equal.
	if opts.Rehearsal != nil && (opts.Config.MigrationRehearsal || opts.Config.PlanDiff) {
		t := opts.Rehearsal
		var basePlans []Plan
		if opts.Config.PlanDiff && len(toExplain) > 0 {
			progress("explaining on the branch before the migrations")
			basePlans, err = CapturePlans(ctx, t.Conn, toExplain)
			if err != nil {
				f.Missing = append(f.Missing, "plans before the migrations: "+short(err))
			}
		}

		if opts.Config.MigrationRehearsal {
			progress("rehearsing the migrations")
			r, err := Rehearse(ctx, t.Conn, t.Watch, t.URL, t.Set, t.Applier,
				opts.Config.LargeTableRows)
			if err != nil {
				return f, err
			}
			f.Rehearsal = &r
		} else if opts.Config.PlanDiff {
			// Plan diff without a rehearsal has nothing to change the plan,
			// so say so rather than reporting no findings.
			f.Missing = append(f.Missing,
				"the plan diff had nothing to compare: migration rehearsal is off, so the "+
					"branch's schema never changed")
		}

		if opts.Config.PlanDiff && opts.Config.MigrationRehearsal && len(basePlans) > 0 {
			progress("explaining on the branch after the migrations")
			after, err := CapturePlans(ctx, t.Conn, toExplain)
			if err != nil {
				f.Missing = append(f.Missing, "plans after the migrations: "+short(err))
			} else {
				schema, serr := CaptureSchema(ctx, t.Conn)
				rows := map[string]int64{}
				if serr == nil {
					rows = schema.Rows
				}
				f.Plans = after
				f.PlanFindings = DiffPlans(basePlans, after, PlanOptions{
					LargeTableRows: opts.Config.LargeTableRows,
					CostFactor:     opts.Config.RegressionFactor,
					Rows:           rows,
				})
			}
		}
	} else if opts.Config.MigrationRehearsal {
		reason := opts.NoRehearsalReason
		if reason == "" {
			reason = "the migrations were not rehearsed, because no fresh branch was available " +
				"to rehearse them against"
		}
		f.Missing = append(f.Missing, reason)
	}

	// With no rehearsal branch, a saved baseline is the other way to have two
	// sides to compare. It is what a pull request check has: the plans main
	// captured, carried across as a file.
	if opts.Config.PlanDiff && f.PlanFindings == nil && opts.Rehearsal == nil &&
		opts.Baseline != nil && len(opts.Baseline.Plans) > 0 && len(toExplain) > 0 {
		progress("explaining against the saved baseline")
		now, err := CapturePlans(ctx, opts.Branch, toExplain)
		if err != nil {
			f.Missing = append(f.Missing, "plans on this branch: "+short(err))
		} else {
			schema, serr := CaptureSchema(ctx, opts.Branch)
			rows := map[string]int64{}
			if serr == nil {
				rows = schema.Rows
			}
			f.Plans = now
			f.PlanFindings = DiffPlans(opts.Baseline.Plans, now, PlanOptions{
				LargeTableRows: opts.Config.LargeTableRows,
				CostFactor:     opts.Config.RegressionFactor,
				Rows:           rows,
			})
		}
	}

	// Plans are captured even with nothing to compare against, so that this
	// run can be somebody else's baseline. That is the whole point of running
	// it on main.
	if opts.Config.PlanDiff && f.Plans == nil && len(toExplain) > 0 {
		if now, err := CapturePlans(ctx, opts.Branch, toExplain); err == nil {
			f.Plans = now
		}
	}

	f.Missing = append(f.Missing, f.Stats.Missing...)
	if f.Rehearsal != nil {
		f.Missing = append(f.Missing, f.Rehearsal.Missing...)
	}
	return f, nil
}

// explainable rejects the statements EXPLAIN will refuse.
//
// Utility statements have no plan, and a report full of "the planner refused"
// for every COMMIT the application sent is noise that hides the one query that
// really could not be explained.
func explainable(text string) bool {
	switch firstWord(text) {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "MERGE", "TABLE", "VALUES":
		return true
	default:
		return false
	}
}

func firstWord(s string) string {
	upper := fold(s)
	for i := 0; i < len(upper); i++ {
		if upper[i] == ' ' || upper[i] == '(' {
			return upper[:i]
		}
	}
	return upper
}
