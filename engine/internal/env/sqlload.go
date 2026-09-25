package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The SQL workload's seam with the orchestrator.
//
// Against the environment's own database rather than a branch of it, which is
// the same decision RunInvariants makes and the opposite of the one insights
// makes. A migration rehearsal needs a database the migrations have NOT been
// applied to, so it takes its own branch. A workload is a question about the
// database this change produced, so a branch would answer a question about a
// database nobody is shipping.
//
// It takes a read lock rather than the write lock, because it opens
// connections and runs statements and does not create, branch or destroy
// anything. A workload that waited on af down would hold up a teardown in
// order to measure a database that is about to be gone.

// SQLLoadOptions are the caller's knobs, each of which overrides the manifest.
//
// Pointers and zero values mean "the manifest decides", exactly as
// LoadOptions' Duration and Scale do. The precedence is caller, then manifest,
// then the engine's default, and ResolveSQLLoad is where it is spelled out
// rather than repeated at each call site.
type SQLLoadOptions struct {
	Clients      int
	Duration     time.Duration
	Transactions int
	ThinkTime    time.Duration
	Seed         int64
	// Select narrows the mix to these transaction names. Empty means all.
	Select []string
	// Progress receives a line a second and may be nil.
	Progress func(sqlload.Progress)

	// Mix, when set, is the exact workload to run, and nothing is built: the
	// manifest's source is not read, the document it names is not opened, and
	// pg_stat_statements on the branch is not queried.
	//
	// It exists for the two build comparison and for nothing else. A DERIVED
	// mix is read from the statistics of the database it is about to run
	// against, so two environments would produce two mixes, weighted by
	// whatever each one's own startup happened to execute. Comparing those and
	// calling the difference a regression would be comparing two different
	// workloads, which is the one thing a comparison must never do. So the
	// comparison builds the mix once, on this build, and hands the same object
	// to both sides.
	Mix *sqlload.Mix
	// Resolved says Clients, Duration, Transactions and ThinkTime above are
	// already settled and the manifest must not be consulted for any of them.
	//
	// Separate from "the field is non zero", because ZERO IS A CHOICE for
	// three of the four. A comparison that resolved think_time to nothing
	// would, without this, fall through to the manifest on each side and wait
	// between transactions on whichever side declared one. The same is true of
	// a transaction bound of zero and of a duration of zero, and each of those
	// silently compares two different workloads.
	Resolved bool
}

// SQLLoadPlan is a resolved workload: the mix, the knobs, and what the mix
// could not take.
type SQLLoadPlan struct {
	Mix *sqlload.Mix
	// Description is what the declared document calls itself, empty for a
	// derived mix.
	Description  string
	Clients      int
	Duration     time.Duration
	Transactions int
	ThinkTime    time.Duration
}

// SQLLoad builds the mix and runs it.
func (o *Orchestrator) SQLLoad(ctx context.Context, opts SQLLoadOptions) (*sqlload.Result, *SQLLoadPlan, error) {
	cfg := o.opts.Manifest.Load
	if cfg == nil || cfg.SQL == nil {
		return nil, nil, aferrors.Coded(aferrors.AFLOD017,
			"detail", "the manifest declares no load.sql block, so there is no workload to run")
	}

	s, err := o.openReading(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer s.close()

	url, err := s.dbProv.ConnString(ctx, provider.Branch{EnvID: o.envID}, provider.ConnDirect)
	if err != nil {
		return nil, nil, aferrors.Coded(aferrors.AFLOD017, "detail", err.Error())
	}
	// Registered before anything can print it. Every other caller of
	// ConnString does the same, and this one hands the string to a package
	// that opens a dozen connections with it.
	o.opts.Redactor.Register(url.Reveal())

	plan, err := o.sqlLoadPlan(ctx, cfg.SQL, url.Reveal(), opts)
	if err != nil {
		return nil, nil, err
	}

	res, err := sqlload.Run(ctx, sqlload.Options{
		URL: url.Reveal(), Mix: plan.Mix,
		Clients: plan.Clients, Duration: plan.Duration,
		Transactions: plan.Transactions, ThinkTime: plan.ThinkTime,
		Seed: opts.Seed, Clock: o.opts.Clock, Progress: opts.Progress,
	})
	if err != nil && res == nil {
		// The connection refusal has its own code, because it has its own
		// answer. AF-LOD-018 is retryable and says to lower the client count or
		// raise max_connections; AF-LOD-017 is a configuration problem and
		// exits three. Collapsing the two would send somebody to the manifest
		// for a server that was simply full.
		if errors.Is(err, sqlload.ErrClientsUnavailable) {
			return nil, plan, aferrors.Coded(aferrors.AFLOD018, "detail", err.Error())
		}
		return nil, plan, aferrors.Coded(aferrors.AFLOD017, "detail", err.Error())
	}
	return res, plan, err
}

// sqlLoadMix resolves the workload without running it.
//
// Unexported, and that is deliberate rather than an oversight. It is a step
// inside the two build comparison, not a capability anybody can ask for: there
// is no command and no tool that resolves a workload and stops. Exporting it
// would put a method on the orchestrator's surface that only one caller in
// this package uses, which is the shape tools/paritycheck exists to find.
//
// It exists for the two build comparison, which has to settle the mix and
// every knob ONCE, on this build, and then hand the same values to both sides.
// Resolving them per side is how two environments end up running two
// workloads: a derived mix is read from the database it is about to run
// against, so each side would weight the statements by its own startup.
//
// It opens a connection because a derived mix needs one, and closes it again.
// A declared mix needs none and opens one anyway rather than branching on the
// source here, because the connection string is also what proves the database
// this comparison is about is reachable at all, before the first round is sent
// rather than after the warm-up.
func (o *Orchestrator) sqlLoadMix(ctx context.Context, opts SQLLoadOptions) (*SQLLoadPlan, error) {
	cfg := o.opts.Manifest.Load
	if cfg == nil || cfg.SQL == nil {
		return nil, aferrors.Coded(aferrors.AFLOD017,
			"detail", "the manifest declares no load.sql block, so there is no workload to run")
	}
	s, err := o.openReading(ctx)
	if err != nil {
		return nil, err
	}
	defer s.close()

	url, err := s.dbProv.ConnString(ctx, provider.Branch{EnvID: o.envID}, provider.ConnDirect)
	if err != nil {
		return nil, aferrors.Coded(aferrors.AFLOD017, "detail", err.Error())
	}
	o.opts.Redactor.Register(url.Reveal())
	return o.sqlLoadPlan(ctx, cfg.SQL, url.Reveal(), opts)
}

// sqlLoadPlan resolves every knob and builds the mix.
func (o *Orchestrator) sqlLoadPlan(ctx context.Context, cfg *schema.LoadSQL, url string, opts SQLLoadOptions) (*SQLLoadPlan, error) {
	plan := ResolveSQLLoad(cfg, opts)

	var mix *sqlload.Mix
	var description string
	var err error
	switch {
	case opts.Mix != nil:
		// Handed in whole, so neither the document nor the statistics is read
		// on this side. See SQLLoadOptions.Mix.
		mix = opts.Mix
	case cfg.Source == schema.SQLStatementStatistics:
		mix, err = o.deriveSQLMix(ctx, cfg, url)
	default:
		mix, description, err = readSQLScript(o.opts.Root, cfg.Script)
	}
	if err != nil {
		return nil, err
	}

	// Narrowed after the mix is built rather than while it is, so a name that
	// matches nothing is an error naming the names that exist rather than a
	// run that sent nothing and reported no problems.
	narrowed, err := mix.Select(opts.Select)
	if err != nil {
		return nil, aferrors.Coded(aferrors.AFLOD017, "detail", err.Error())
	}
	plan.Mix = narrowed
	plan.Description = description
	return plan, nil
}

// ResolveSQLLoad settles every knob: the caller's, then the manifest's, then
// the engine's default.
//
// Exported and pure so the command can show what a run WILL do without opening
// anything, and so the test that proves the precedence needs no environment.
// The same shape ResolveLoadRate already has, and for the same reason: the
// precedence was written three times before that function existed and two of
// the three disagreed.
func ResolveSQLLoad(cfg *schema.LoadSQL, opts SQLLoadOptions) *SQLLoadPlan {
	if opts.Resolved {
		// Nothing below runs. A caller that has already settled every knob is
		// saying so precisely because it must hand both sides of a comparison
		// the same values, and reading the manifest here for the one field
		// that happens to be zero is how the two sides diverge.
		return &SQLLoadPlan{
			Clients: opts.Clients, Duration: opts.Duration,
			Transactions: opts.Transactions, ThinkTime: opts.ThinkTime,
		}
	}
	plan := &SQLLoadPlan{Clients: 8, Duration: 60 * time.Second}
	if cfg != nil {
		if cfg.Clients > 0 {
			plan.Clients = cfg.Clients
		}
		// time.ParseDuration rather than the manifest package's, which would
		// make this package depend on the parser it is a consumer of. The
		// schema's own patterns for these two keys are ^[0-9]+(s|m)$ and
		// ^[0-9]+(ms|s)$, and every string either matches is one Go reads the
		// same way, so the subset the manifest parser adds does not apply
		// here.
		if d, err := time.ParseDuration(cfg.Duration); err == nil && d > 0 {
			plan.Duration = d
		}
		if cfg.Transactions > 0 {
			plan.Transactions = cfg.Transactions
			if cfg.Duration == "" {
				// A manifest that sized the run in transactions and said
				// nothing about time gets no time bound at all. Leaving the
				// default sixty seconds there would cap a run its author sized
				// in work, and the report would say it ran fewer transactions
				// than it asked for with nothing saying why.
				plan.Duration = 0
			}
		}
		if d, err := time.ParseDuration(cfg.ThinkTime); err == nil {
			plan.ThinkTime = d
		}
	}
	if opts.Clients > 0 {
		plan.Clients = opts.Clients
	}
	if opts.Duration > 0 {
		plan.Duration = opts.Duration
	}
	if opts.Transactions > 0 {
		plan.Transactions = opts.Transactions
	}
	if opts.ThinkTime > 0 {
		plan.ThinkTime = opts.ThinkTime
	}
	return plan
}

// deriveSQLMix reads the mix from pg_stat_statements on the environment.
func (o *Orchestrator) deriveSQLMix(ctx context.Context, cfg *schema.LoadSQL, url string) (*sqlload.Mix, error) {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFDB004, "env", o.envID)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	return sqlload.Derive(ctx, conn, sqlload.DeriveOptions{
		MaxStatements: cfg.MaxStatements,
		Writes:        cfg.Writes,
	})
}

// readSQLScript reads and parses the declared workload document.
func readSQLScript(root, script string) (*sqlload.Mix, string, error) {
	clean := strings.TrimSpace(script)
	if clean == "" {
		return nil, "", aferrors.Coded(aferrors.AFLOD017,
			"detail", "load.sql.source is declared and load.sql.script names no document")
	}
	path := filepath.Join(root, filepath.FromSlash(clean))
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, "", aferrors.Coded(aferrors.AFLOD013, "path", clean, "detail", err.Error())
	}
	mix, description, err := sqlload.ParseScript(body)
	if err != nil {
		return nil, "", aferrors.Coded(aferrors.AFLOD013, "path", clean, "detail", err.Error())
	}
	return mix, description, nil
}

// SQLThresholds returns the limits a SQL workload is judged against.
func (o *Orchestrator) SQLThresholds() (meanIncrease, errorRate float64) {
	cfg := o.opts.Manifest.Load
	if cfg == nil || cfg.SQL == nil || cfg.SQL.Thresholds == nil {
		return 0, 0
	}
	t := cfg.SQL.Thresholds
	return t.MeanIncrease, t.ErrorRate
}
