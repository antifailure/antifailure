package env

import (
	"context"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// InsightsOptions are the parts of an insights run the caller decides.
type InsightsOptions struct {
	// Limit is how many statements to report.
	Limit int
	// Baseline is a report saved on another branch, or nil.
	Baseline *insights.Baseline
	// SkipRehearsal runs everything except the migration rehearsal. It exists
	// for the case where a second branch cannot be made, not as a way to
	// disable the check: that is what the manifest is for.
	SkipRehearsal bool
	// Against overrides which commit the rolling deploy check treats as the
	// previous release, for the run where somebody is checking a specific one.
	// Empty uses the manifest.
	Against string
	// RunnerPath overrides where the agent runner lives. The rolling deploy
	// check drives workflows, so it needs the runner the same way af test
	// does.
	RunnerPath string
}

// RunInsights performs every check the manifest leaves on.
//
// The rehearsal branch is the important part of this function. Migrations are
// rehearsed against a branch made for the rehearsal and destroyed after it,
// never against the environment's own database, for two reasons. Migrations
// are not required to be idempotent, so a rehearsal against a database they
// have already touched measures nothing. And the environment's database is
// the one somebody is using, so applying a pull request's migrations to it
// twice, once by af up and once by af insights, is a change to a running
// thing rather than a rehearsal of one.
//
// It is also what makes the plan diff mean something. The rehearsal branch
// holds the same rows as the environment, so the plans captured on it before
// the migrations and after them differ only because of the migrations. There
// is nothing else to hold equal, which is the one thing a plan comparison is
// usually missing.
func (o *Orchestrator) RunInsights(
	ctx context.Context, opts InsightsOptions,
) (insights.Full, error) {
	s, err := o.open(ctx, "af insights")
	if err != nil {
		return insights.Full{}, err
	}
	defer s.close()

	conn, err := connectSession(ctx, o, s)
	if err != nil {
		return insights.Full{}, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	runOpts := insights.Options{
		Config:   insights.Configure(o.opts.Manifest.Insights),
		Branch:   conn,
		Limit:    opts.Limit,
		Baseline: opts.Baseline,
		Progress: o.progress,
	}

	if !opts.SkipRehearsal && runOpts.Config.Enabled &&
		(runOpts.Config.MigrationRehearsal || runOpts.Config.PlanDiff) {
		target, release, why, err := o.rehearsalBranch(ctx, s)
		if err != nil {
			return insights.Full{}, err
		}
		if release != nil {
			defer release()
		}
		runOpts.Rehearsal = target
		runOpts.NoRehearsalReason = why
	} else if opts.SkipRehearsal {
		runOpts.NoRehearsalReason = "the migrations were not rehearsed, because --no-rehearsal was given"
	}

	full, err := insights.Run(ctx, runOpts)
	if err != nil {
		return full, err
	}
	full.Rolling, full.Off = o.rolling(ctx, s, runOpts.Config, full, opts)
	return full, nil
}

// rollingDecision is what the check settles before it spends anything.
//
// It is separated from the run because the two failures it can have are
// invisible in the output and expensive in opposite directions. Deciding to
// run when nothing narrowed doubles every pipeline that ships an additive
// migration; deciding not to run when something did silently skips the check
// on the migration it exists for. Both produce a report that looks fine, so
// both need a test that does not need a daemon.
type rollingDecision struct {
	// run says the check is worth paying for.
	run bool
	// verdict and reason are what to report when it is not.
	verdict insights.RollingVerdict
	reason  string
	// manifestOff says the reason belongs in the report's "turned off in the
	// manifest" list. Only a manifest that turned the check off does: a
	// migration that takes nothing away is not the manifest's doing, and
	// putting it under that heading printed a sentence that was both
	// duplicated and untrue.
	manifestOff bool
	// statements are the pending migrations, for the run that follows.
	statements []insights.Statement
}

// decideRolling answers whether to run the check, from the manifest and from
// what the rehearsal found.
func decideRolling(rc insights.RollingConfig, r *insights.Rehearsal) rollingDecision {
	off := func(reason string) rollingDecision {
		return rollingDecision{verdict: insights.RollingOff, reason: reason}
	}
	blocked := func(reason string) rollingDecision {
		return rollingDecision{verdict: insights.RollingBlocked, reason: reason}
	}

	if rc.When == insights.RollingNever {
		d := off("insights.rolling_compatibility.when is never")
		d.manifestOff = true
		return d
	}
	// Blocked rather than off, and the difference matters: off says there was
	// nothing to check, blocked says the check could not be made. Reporting
	// the second as the first would hide a migration that did not apply behind
	// a line that reads like a clean bill of health.
	if r == nil {
		return blocked("the migrations were not rehearsed, so there is no migrated schema to " +
			"run the previous release against")
	}
	if r.Failed {
		return blocked("the migrations did not apply, so there is no migrated schema to run " +
			"the previous release against")
	}

	stmts := rollingStatements(r)
	if len(stmts) == 0 {
		return off("this branch has no migration pending against the golden the environment " +
			"came from, so there is nothing for the previous release to meet")
	}
	if !rc.On(insights.Narrowing(stmts)) {
		// The expensive branch not taken, and said out loud. Every statement
		// in these migrations only adds something, and code that never heard
		// of it cannot notice it, so running a second build and a second
		// environment would cost a doubled pipeline for a result that is
		// already known.
		return off("these migrations only add things the previous release cannot notice. " +
			"Set insights.rolling_compatibility.when to always to run it regardless")
	}
	return rollingDecision{run: true, statements: stmts}
}

// rolling decides whether the rolling deploy check runs, and runs it.
//
// It is here rather than inside insights.Run because proving the previous
// release still works needs a second image, a second environment and a
// browser, and the insights package deliberately holds only the questions a
// database can answer on its own.
//
// A check that does not run is named rather than left out, which is the rule
// every other check in this report follows: a report that silently omits a
// check reads exactly like a check that found nothing.
func (o *Orchestrator) rolling(
	ctx context.Context, s *session, cfg insights.Config, full insights.Full,
	opts InsightsOptions,
) (*insights.Rolling, []string) {
	rc := cfg.Rolling
	if opts.Against != "" {
		rc.Against = opts.Against
	}

	d := decideRolling(rc, full.Rehearsal)
	if !d.run {
		off := full.Off
		if d.manifestOff {
			off = append(off, "the rolling deploy check, because "+d.reason)
		}
		return &insights.Rolling{Verdict: d.verdict, Reason: d.reason}, off
	}

	golden, why, err := o.environmentGolden(ctx, s)
	if err != nil {
		return &insights.Rolling{
			Verdict: insights.RollingBlocked,
			Reason:  o.opts.Redactor.String(err.Error()),
		}, full.Off
	}
	if golden == "" {
		return &insights.Rolling{Verdict: insights.RollingBlocked, Reason: why}, full.Off
	}
	return o.rollingCheck(ctx, s, rollingInputs{
		Config: rc, Golden: golden, Statements: d.statements,
		Set: insights.Discover(os.DirFS(o.opts.Root)), RunnerPath: opts.RunnerPath,
	}), full.Off
}

// rehearsalBranch makes a throwaway branch of the golden and everything the
// rehearsal needs to run against it.
//
// A nil target with a nil error means there was nothing to rehearse against,
// and the returned reason says which of the several reasons it was. Run puts
// that in the report rather than passing over it, because a report with no
// rehearsal section reads exactly like a rehearsal that found nothing.
func (o *Orchestrator) rehearsalBranch(
	ctx context.Context, s *session,
) (*insights.Target, func(), string, error) {
	set := insights.Discover(os.DirFS(o.opts.Root))

	version, why, err := o.environmentGolden(ctx, s)
	if err != nil {
		return nil, nil, "", err
	}
	if version == "" {
		return nil, nil, why, nil
	}

	o.progress("branching " + version + " to rehearse the migrations against")
	branch, err := s.dbProv.Branch(ctx, version, o.envID+"-rehearsal")
	if err != nil {
		return nil, nil, "", err
	}
	destroy := func() {
		// Removed whether or not the rehearsal passed, for the same reason a
		// verification branch is: a copy of the data that outlives the check
		// is a copy of the data nobody is watching.
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		_ = s.dbProv.Destroy(c, branch)
	}

	url, err := s.dbProv.ConnString(ctx, branch, provider.ConnDirect)
	if err != nil {
		destroy()
		return nil, nil, "", err
	}
	o.opts.Redactor.Register(url.Reveal())

	applier, why, err := o.applierFor(ctx, s, set, branch)
	if err != nil {
		destroy()
		return nil, nil, "", err
	}
	if applier == nil {
		destroy()
		return nil, nil, why, nil
	}

	conn, err := pgx.Connect(ctx, url.Reveal())
	if err != nil {
		destroy()
		return nil, nil, "", aferrors.Wrap(err, aferrors.AFDB004, "env", o.envID+"-rehearsal")
	}
	// A second connection, because the lock sampler has to ask what is locked
	// while a statement is still running, and the session running it cannot
	// answer until it finishes.
	watch, err := pgx.Connect(ctx, url.Reveal())
	if err != nil {
		_ = conn.Close(context.WithoutCancel(ctx))
		destroy()
		return nil, nil, "", aferrors.Wrap(err, aferrors.AFDB004, "env", o.envID+"-rehearsal")
	}

	release := func() {
		_ = watch.Close(context.WithoutCancel(ctx))
		_ = conn.Close(context.WithoutCancel(ctx))
		destroy()
	}
	return &insights.Target{
		Conn: conn, Watch: watch, URL: url, Set: set, Applier: applier,
	}, release, "", nil
}

// applierFor decides how the pending migrations get applied.
//
// A project whose migrations are SQL files has them replayed from here, one
// statement at a time, which is the only way to time each one exactly. A
// project whose migrations are Ruby, Python or JavaScript has to be applied by
// its own tool inside its own image, because only that tool knows what they
// become and the answer depends on the gems and packages in the image rather
// than on whatever is installed on this machine. Running the tool on the
// workstation would rehearse something the deploy does not do, which is worse
// than not rehearsing, because it produces a result somebody would believe.
func (o *Orchestrator) applierFor(
	ctx context.Context, s *session, set insights.MigrationSet, branch provider.Branch,
) (insights.Applier, string, error) {
	if set.SQLAvailable() {
		return &insights.SQLApplier{Progress: o.progress}, "", nil
	}
	if set.Tool == insights.ToolNone {
		return nil, "the migrations were not rehearsed: no migration tool was recognised in " +
			"this repository", nil
	}

	svc, ok := migratingService(o.opts.Manifest)
	if !ok {
		return nil, "the migrations were not rehearsed: " + string(set.Tool) + " migrations have " +
			"to be run by their own tool inside the service's image, and no service in the " +
			"manifest declares a migrate command", nil
	}

	ig, err := readIgnore(o.opts.Root)
	if err != nil {
		return nil, "", err
	}
	// The environment is already up, so this is a cache lookup rather than a
	// build: the image reference is derived from the build context's digest,
	// so the same tree produces the same tag.
	image, _, err := o.buildOne(ctx, s, svc, ig)
	if err != nil {
		return nil, "", err
	}

	applier := &insights.ContainerApplier{
		Image:    image,
		Command:  svc.Migrate,
		URLVar:   databaseURLVar(o.opts.Manifest),
		Env:      serviceEnv(svc),
		EnvID:    o.envID,
		Progress: o.progress,
	}
	// A provider whose branches are local containers has to put this one on a
	// network with the migration, because its connection string points at the
	// host's loopback and that is the container itself from inside.
	if attachable, isLocal := s.dbProv.(insights.Attachable); isLocal {
		applier.Database = attachable
		applier.DatabaseRef = branch.ProviderRef
	}
	return applier, "", nil
}

// migratingService is the service whose migrate command builds the schema.
//
// The first one, because a manifest with two migrating services is a project
// with two databases and this rehearsal is about one branch. Reporting which
// was chosen matters more than choosing cleverly, and the applier's name and
// image are both in the report.
func migratingService(m *schema.Manifest) (schema.Service, bool) {
	for _, svc := range m.Services {
		if svc.Migrate != "" {
			return svc, true
		}
	}
	return schema.Service{}, false
}

// databaseURLVar is the variable the application reads its connection string
// from, which the manifest names because not every framework reads
// DATABASE_URL.
func databaseURLVar(m *schema.Manifest) string {
	if m.Database != nil && m.Database.URLEnv != "" {
		return m.Database.URLEnv
	}
	return ""
}

// environmentGolden is the golden version the environment's own database came
// from, which is the only one worth rehearsing against.
//
// The newest verified golden is NOT an acceptable substitute, and reaching for
// it was a real bug here rather than a hypothetical one: the rehearsal branched
// from a golden the environment had never seen and the first migration failed
// with `relation "orders" does not exist`. A failure is the lucky version of
// that. The unlucky version is a golden that has the tables and different rows,
// where every statement applies, every plan is captured, and the whole plan
// diff is a comparison between two databases that were never the same. The
// entire point of comparing plans on a branch is that the data on both sides is
// identical.
//
// A provider that does not record where a branch came from gets a reason rather
// than a guess.
func (o *Orchestrator) environmentGolden(
	ctx context.Context, s *session,
) (version, why string, err error) {
	inventory, err := s.dbProv.Inventory(ctx)
	if err != nil {
		return "", "", err
	}
	for _, r := range inventory {
		if r.EnvID != o.envID {
			continue
		}
		if v := r.Labels["golden"]; v != "" {
			version = v
			break
		}
	}
	if version == "" {
		return "", "the migrations were not rehearsed: this provider does not record which " +
			"golden the environment's database came from, and rehearsing against a different " +
			"one would compare two databases that were never the same", nil
	}

	goldens, err := s.dbProv.ListGoldens(ctx)
	if err != nil {
		return "", "", err
	}
	for _, g := range goldens {
		if g.ID == version && g.Verified {
			return version, "", nil
		}
	}
	return "", "the migrations were not rehearsed: the golden this environment came from, " +
		version + ", is no longer present or no longer verified", nil
}
