package env

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Running the PREVIOUS release against the migrated schema.
//
// The rehearsal proves what a migration costs. This proves the thing a deploy
// depends on and nothing else in the product proved: during a rolling deploy
// there are minutes when the migration has applied and the last old instance
// has not stopped, and in those minutes the previous release is talking to the
// new schema. `RuleRenameColumnInUse` says that sentence and cannot check it.
//
// The experiment is the whole design, and it is a controlled one on purpose:
//
//	branch A of the golden -> personas -> apply the pending migrations ->
//	  bring up the PREVIOUS commit's images -> run that release's workflows
//
// and, only if something failed,
//
//	branch B of the SAME golden -> personas -> no migrations ->
//	  bring up the same images -> re-run only the workflows that failed
//
// A and B differ in exactly one thing. A workflow that passes on B and fails
// on A fails because of the migration; there is nothing else it could be. A
// workflow that fails on both is a workflow the previous release does not pass
// anyway, and it is reported as unverified rather than counted, because a
// check whose first finding turns out to be a pre-existing failure is a check
// nobody reads a second time.
//
// The control is paid for only when something has already failed. Confirming a
// pass would double the cost of the common case to learn nothing.

// rollingSuffix names the environment the previous release runs in.
//
// A separate environment identifier rather than the caller's, because every
// resource is labelled with it and teardown of one environment must never
// touch another's. The environment under test is still running while this one
// is up.
const rollingSuffix = "-prev"

// rollingControlSuffix names the control environment.
const rollingControlSuffix = "-prevbase"

// rollingInputs is what the caller already knows and this check needs.
type rollingInputs struct {
	// Config is the resolved rolling_compatibility block.
	Config insights.RollingConfig
	// Golden is the version the environment's own database came from. The
	// same one, for the same reason the rehearsal insists on it: two branches
	// of different goldens are two databases that were never the same.
	Golden string
	// Statements are the pending migrations, as statements.
	Statements []insights.Statement
	// Set is this repository's migrations, for the applier.
	Set insights.MigrationSet
	// RunnerPath overrides where the agent runner lives.
	RunnerPath string
}

// rollingCheck answers whether the previous release survives this migration.
//
// It never returns an error for anything that is our own failure. A previous
// commit that cannot be resolved, an image that will not build, a runner that
// will not start: each of those comes back as a Rolling with verdict blocked
// and a sentence saying which. That distinction is the difference between a
// check people trust and a check people disable, and getting it wrong once is
// enough to lose the argument permanently.
func (o *Orchestrator) rollingCheck(
	ctx context.Context, s *session, in rollingInputs,
) *insights.Rolling {
	blocked := func(format string, args ...any) *insights.Rolling {
		return &insights.Rolling{
			Verdict: insights.RollingBlocked,
			Reason:  fmt.Sprintf(format, args...),
		}
	}
	changes := insights.NarrowingChanges(in.Statements)

	if in.Golden == "" {
		return blocked("%s", "the environment's golden is not known, and branching a different "+
			"one would run the previous release against a database it was never going to meet")
	}

	ref, how, err := o.previousCommit(in.Config.Against)
	if err != nil {
		return blocked("%s", err.Error())
	}

	tree, cleanup, err := o.exportCommit(ref)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return blocked("%s", err.Error())
	}

	prevManifest, err := manifest.Load(filepath.Join(tree, manifest.FileName))
	if err != nil {
		return blocked("the manifest at %s could not be read, so there is nothing to say what "+
			"the previous release ran or what its workflows were: %s", shortSHA(ref), err.Error())
	}
	if len(prevManifest.Workflows) == 0 {
		return blocked("%s declares no workflows, so there is nothing to drive through the "+
			"previous release", shortSHA(ref))
	}
	if len(prevManifest.Services) == 0 {
		return blocked("%s declares no services, so there is no previous release to run",
			shortSHA(ref))
	}

	runner, err := o.findRunner(in.RunnerPath)
	if err != nil {
		return blocked("%s", "the agent runner could not be found, and it is what drives the "+
			"workflows: "+err.Error())
	}

	// Built before anything is branched, so a build failure costs no database.
	// It is also the failure most likely to happen, because the previous
	// commit's build is one nothing in this run has exercised.
	specs, err := o.buildPreviousRelease(ctx, s, tree, prevManifest)
	if err != nil {
		return blocked("the previous release's image at %s would not build, which is a fact "+
			"about that commit rather than about this migration: %s",
			shortSHA(ref), o.opts.Redactor.String(err.Error()))
	}

	migrated, release, err := o.bringUpPreviousRelease(ctx, s, previousRelease{
		envID: o.envID + rollingSuffix, tree: tree, manifest: prevManifest,
		golden: in.Golden, specs: specs, set: in.Set, applier: o,
	})
	if release == nil {
		release = func() {}
	}
	defer release()
	if err != nil {
		return blocked("the previous release could not be brought up against the migrated "+
			"branch: %s", o.opts.Redactor.String(err.Error()))
	}

	o.progress("driving the previous release's workflows against the migrated branch")
	report, err := o.driveRunner(ctx, runnerJob{
		Runner: runner, BaseURL: migrated.url, Artifacts: o.artifactsDir(migrated.envID),
		Workflows: migrated.workflows, Personas: migrated.personas,
		WorkDir: o.opts.Root, Attempts: 1, Headless: true,
	})
	if err != nil {
		return blocked("the agent runner did not produce a result against the previous "+
			"release: %s", o.opts.Redactor.String(err.Error()))
	}

	outcomes := rollingOutcomes(report)
	output, unreadable := o.serviceOutput(ctx, s, migrated.envID)

	// Torn down before the control starts rather than at the end of this
	// function. The control is a whole second environment, and holding two of
	// them plus the environment under test at once is three copies of the
	// application on one machine for no reason: everything this one had to say
	// has already been read. Idempotent, so the defer above is still correct.
	release()

	control := map[string]insights.RunnerOutcome{}
	controlFailed := ""
	if failed := insights.NeedsControl(outcomes); len(failed) > 0 {
		control, controlFailed = o.rollingControl(ctx, s, failed, controlInputs{
			tree: tree, manifest: prevManifest, golden: in.Golden,
			specs: specs, runner: runner,
		})
	}

	graded := insights.GradeRolling(outcomes, control, changes, output, shortSHA(ref))
	graded.Against, graded.How = ref, how
	if unreadable != "" {
		graded.Missing = append(graded.Missing, unreadable)
	}
	if controlFailed != "" {
		graded.Missing = append(graded.Missing, controlFailed)
	}
	return &graded
}

// controlInputs is what the control run needs that the measurement already
// worked out.
type controlInputs struct {
	tree     string
	manifest *schema.Manifest
	golden   string
	specs    []provider.ServiceSpec
	runner   string
}

// previousOrchestrator is the same session seen through the previous commit's
// manifest and tree.
//
// Everything that reads the manifest, which is the personas, the workflows,
// the egress policy and the migration applier, then reads the previous
// release's own. Reading this branch's manifest instead would drive workflows
// the previous release never had and apply migrations it never shipped.
func (o *Orchestrator) previousOrchestrator(envID, tree string, m *schema.Manifest) *Orchestrator {
	prev := &Orchestrator{opts: o.opts, envID: envID, progress: o.progress, sinks: o.sinks}
	prev.opts.Root = tree
	prev.opts.Manifest = m
	return prev
}

// rollingControl re-runs the failing workflows against a branch of the same
// golden carrying the schema the previous release was deployed against.
//
// A failure here leaves the map empty, which grades every failure as
// unverified rather than as a finding. That is the right direction to fail in:
// without the control there is no evidence that the migration is what changed,
// and saying so costs a reader one line while claiming it costs the check its
// credibility.
func (o *Orchestrator) rollingControl(
	ctx context.Context, s *session, only []string, in controlInputs,
) (map[string]insights.RunnerOutcome, string) {
	out := map[string]insights.RunnerOutcome{}

	o.progress(fmt.Sprintf(
		"%d workflow(s) failed, so the same release is being run against the same golden "+
			"carrying the schema it was deployed against, to see whether this branch's "+
			"migrations are the difference",
		len(only)))

	base, release, err := o.bringUpPreviousRelease(ctx, s, previousRelease{
		envID: o.envID + rollingControlSuffix, tree: in.tree, manifest: in.manifest,
		golden: in.golden, specs: in.specs, only: only,
		set: insights.Discover(os.DirFS(in.tree)),
	})
	if release != nil {
		defer release()
	}
	if err != nil {
		return out, "the control run could not be brought up, so the failures above stay " +
			"unverified: " + o.opts.Redactor.String(err.Error())
	}

	report, err := o.driveRunner(ctx, runnerJob{
		Runner: in.runner, BaseURL: base.url, Artifacts: o.artifactsDir(base.envID),
		Workflows: base.workflows, Personas: base.personas,
		WorkDir: o.opts.Root, Attempts: 1, Headless: true,
	})
	if err != nil {
		return out, "the control run produced no result, so the failures above stay " +
			"unverified: " + o.opts.Redactor.String(err.Error())
	}
	for _, r := range rollingOutcomes(report) {
		out[r.Name] = r
	}
	return out, ""
}

// previousRelease describes one environment running the previous commit.
type previousRelease struct {
	envID    string
	tree     string
	manifest *schema.Manifest
	golden   string
	specs    []provider.ServiceSpec
	// set is the migrations to apply to the branch before the services start,
	// and applier is the orchestrator that knows how to run them.
	//
	// The measurement applies THIS branch's pending migrations. The control
	// applies the PREVIOUS commit's, which is the schema that release was
	// deployed against. On a golden taken from production the second is
	// already applied and applying it again is a no-op, which is the case that
	// makes it look unnecessary. On a golden with no production behind it,
	// where the schema arrives with the migrations, it is the only thing that
	// gives the control a schema at all, and without it every control run
	// fails and every finding comes back unverified.
	set     insights.MigrationSet
	applier *Orchestrator
	// only limits the workflows to these names. Empty runs all of them.
	only []string
}

// stagedRelease is a running previous release and what the runner needs to
// drive it.
type stagedRelease struct {
	envID     string
	url       string
	workflows []workflowDoc
	personas  []personaDoc
}

// bringUpPreviousRelease branches the golden, puts the branch in the state the
// experiment needs, and starts the previous commit's services against it.
//
// The order is production's. The personas exist first, because in production
// the users are there before the migration runs, and because a migration that
// adds a NOT NULL column to the users table would refuse an insert afterwards
// and the refusal would be ours rather than the application's.
//
// The returned function tears down everything this made, and is never nil once
// anything has been created.
func (o *Orchestrator) bringUpPreviousRelease(
	ctx context.Context, s *session, in previousRelease,
) (*stagedRelease, func(), error) {
	prev := o.previousOrchestrator(in.envID, in.tree, in.manifest)

	o.progress("branching " + in.golden + " for " + in.envID)
	branch, err := s.dbProv.Branch(ctx, in.golden, in.envID)
	if err != nil {
		return nil, nil, err
	}
	torn := false
	release := func() {
		if torn {
			return
		}
		torn = true
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		if _, derr := s.runtime.Down(c, in.envID); derr != nil {
			o.progress("the previous release's environment could not be fully removed: " +
				o.opts.Redactor.String(derr.Error()))
		}
		// Destroyed whether or not the check passed, for the reason the
		// rehearsal branch is: a copy of the data that outlives the check is a
		// copy of the data nobody is watching.
		if derr := s.dbProv.Destroy(c, branch); derr != nil {
			o.progress("the previous release's branch could not be removed: " +
				o.opts.Redactor.String(derr.Error()))
		}
	}

	direct, err := s.dbProv.ConnString(ctx, branch, provider.ConnDirect)
	if err != nil {
		return nil, release, err
	}
	o.opts.Redactor.Register(direct.Reveal())

	// Nil means the previous release's own tree, which is the control. The
	// measurement passes this orchestrator, because the migrations it applies
	// are this pull request's.
	applier := in.applier
	if applier == nil {
		applier = prev
	}
	if err := applier.applyPending(ctx, s, in.set, branch, direct); err != nil {
		return nil, release, err
	}

	// After the migrations rather than before, which is the order af up uses:
	// a persona is a row in a table the migrations create, and on a golden
	// with no production behind it there is no table to write into until they
	// have run.
	//
	// A persona that does not exist means every workflow fails at the sign in,
	// and it fails with a finding against the application. That is the most
	// expensive wrong answer this product can give, so it is a refusal here
	// rather than a run that produces one.
	provisioned, err := prev.provisionPersonas(ctx, s)
	if err != nil {
		return nil, release, fmt.Errorf("the previous release's personas could not be "+
			"created, so no workflow could sign in: %w", err)
	}

	spec, err := o.previousSpec(ctx, s, prev, in, branch, direct)
	if err != nil {
		return nil, release, err
	}
	env, err := s.runtime.Up(ctx, spec)
	if err != nil {
		return nil, release, err
	}
	url := env.URL()
	if url == "" {
		return nil, release, fmt.Errorf(
			"no web service in the previous release answered, so there is nothing to drive")
	}

	return &stagedRelease{
		envID:     in.envID,
		url:       url,
		workflows: prev.workflowDocs(in.only),
		personas:  prev.personaDocs(provisioned),
	}, release, nil
}

// previousSpec builds the environment specification for the previous release.
//
// It deliberately drops each service's migrate command. The branch is already
// in exactly the state the experiment wants, which for the measurement is the
// golden plus THIS pull request's migrations, and letting the previous
// release's own migrate command run against it would either be a no-op or, for
// a tool that reads its history table differently, would change the one thing
// the experiment is holding fixed.
func (o *Orchestrator) previousSpec(
	ctx context.Context, s *session, prev *Orchestrator,
	in previousRelease, branch provider.Branch, direct secrets.Value,
) (provider.EnvSpec, error) {
	services := make([]provider.ServiceSpec, 0, len(in.specs))
	for _, svc := range in.specs {
		svc.Migrate = ""
		services = append(services, svc)
	}

	spec := provider.EnvSpec{
		EnvID: in.envID, Branch: o.opts.Branch, Services: services,
		Egress:      in.manifest.Egress,
		DatabaseURL: direct,
		Journal: func(kind, id string) error {
			_, jerr := s.journal.Intent(ctx, in.envID, s.runtime.Name(),
				journal.Kind(kind), id, nil)
			return jerr
		},
		Progress: o.progress,
	}

	resolved, err := prev.resolveSecrets(ctx)
	if err != nil {
		return spec, err
	}
	for i := range spec.Services {
		for name := range spec.Services[i].Env {
			if value, ok := resolved.Service[name]; ok {
				spec.Services[i].Env[name] = value
			}
		}
	}
	spec.SandboxCredentials = resolved.Sidecar
	spec.ModelEnv = o.modelEnv()

	packs, err := prev.mockPacks()
	if err != nil {
		return spec, err
	}
	spec.MockPacks = packs

	// The same attachment the environment itself gets. A branch that is a
	// container on this machine is reachable from inside another container
	// only by its address on a shared network, and the connection string the
	// provider hands out names the host's loopback, which from inside a
	// container is the container.
	attachable, isLocalBranch := s.dbProv.(local.Attachable)
	if !isLocalBranch {
		return spec, nil
	}
	if !s.runtime.Capabilities().AttachesLocalDatabase {
		return spec, aferrors.Coded(aferrors.AFRUN044, "detail", fmt.Sprintf(
			"the database provider makes branches as containers on this machine and the "+
				"%s runtime cannot reach them", s.runtime.Name()))
	}
	networked, ok := s.runtime.(interface {
		EnsureNetworks(context.Context, string, func(string, string) error) (string, error)
		AttachDatabase(context.Context, local.Attachable, string, string, secrets.Value) (secrets.Value, error)
	})
	if !ok {
		return spec, aferrors.Coded(aferrors.AFRUN044, "detail", fmt.Sprintf(
			"the %s runtime declares that it attaches a local database and does not "+
				"implement it", s.runtime.Name()))
	}
	networkID, err := networked.EnsureNetworks(ctx, in.envID, spec.Journal)
	if err != nil {
		return spec, err
	}
	inside, err := networked.AttachDatabase(ctx, attachable, branch.ProviderRef, networkID, direct)
	if err != nil {
		return spec, err
	}
	o.opts.Redactor.Register(inside.Reveal())
	spec.DatabaseURL = inside
	spec.MigrationDatabaseURL = inside
	return spec, nil
}

// applyPending applies this pull request's pending migrations to a branch.
//
// The same applier the rehearsal uses, which for a repository whose migrations
// are SQL replays them statement by statement and for one whose migrations are
// Ruby or Python runs the project's own tool inside the image. It has to be
// the same one: a rolling check that applied the migrations differently from
// the rehearsal would be answering a question about a schema neither the
// rehearsal nor the deploy produces.
func (o *Orchestrator) applyPending(
	ctx context.Context, s *session, set insights.MigrationSet,
	branch provider.Branch, url secrets.Value,
) error {
	applier, why, err := o.applierFor(ctx, s, set, branch)
	if err != nil {
		return err
	}
	if applier == nil {
		return fmt.Errorf("%s", why)
	}
	if len(set.Migrations) == 0 && set.Tool == insights.ToolNone {
		// Nothing to apply and nothing that could be applied. The branch is
		// the golden, which is the right state for a repository whose schema
		// lives in the golden rather than in migration files.
		return nil
	}

	conn, err := pgx.Connect(ctx, url.Reveal())
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFDB004, "env", branch.EnvID)
	}
	applied, aerr := set.Applied(ctx, conn)
	_ = conn.Close(context.WithoutCancel(ctx))
	if aerr != nil {
		// Every migration on disk is treated as pending, which is what the
		// rehearsal does with the same failure.
		applied = nil
	}
	pending := set.Pending(applied)
	if len(pending) == 0 {
		return nil
	}

	o.progress(fmt.Sprintf("applying %d migration(s) to %s", len(pending), branch.EnvID))
	if _, err := applier.Apply(ctx, url, pending); err != nil {
		return fmt.Errorf("the migrations did not apply to this branch, so there is no "+
			"migrated schema to run the previous release against: %w", err)
	}
	return nil
}

// buildPreviousRelease builds an image for every service the previous commit
// declared.
func (o *Orchestrator) buildPreviousRelease(
	ctx context.Context, s *session, tree string, m *schema.Manifest,
) ([]provider.ServiceSpec, error) {
	ig, err := readIgnore(tree)
	if err != nil {
		return nil, err
	}
	var specs []provider.ServiceSpec
	for _, svc := range m.Services {
		image, _, err := o.buildOneFrom(ctx, s, tree, svc, ig)
		if err != nil {
			return nil, err
		}
		specs = append(specs, provider.ServiceSpec{
			Name:       svc.Name,
			Image:      image,
			Kind:       string(orDefault(string(svc.Kind), "worker")),
			Command:    svc.Command,
			Port:       svc.Port,
			HealthPath: svc.HealthPath,
			DependsOn:  svc.DependsOn,
			Env:        serviceEnv(svc),
		})
	}
	return specs, nil
}

// serviceOutput is what the previous release printed.
//
// It is the evidence half of every sentence this check prints. A driver puts
// the server's own message into the application's output, and those messages
// are composed by Postgres rather than by the application, so `column "email"
// does not exist` is the server's wording wherever it appears. An application
// that swallows the error produces nothing here, which is reported as a
// failure with no identified cause rather than as a cause nobody can see.
//
// A runtime that cannot read logs is not an error. The check still works; it
// just cannot name the column, and the report says which of the two happened.
func (o *Orchestrator) serviceOutput(
	ctx context.Context, s *session, envID string,
) (lines []string, missing string) {
	reader, ok := s.runtime.(provider.LogReader)
	if !ok {
		return nil, "the " + s.runtime.Name() + " runtime cannot read a service's output, so a " +
			"failure here cannot be traced to the statement that caused it"
	}
	read, err := reader.Logs(ctx, envID, "", 400)
	if err != nil {
		return nil, "the previous release's output could not be read, so a failure here " +
			"cannot be traced to the statement that caused it: " +
			o.opts.Redactor.String(err.Error())
	}
	out := make([]string, 0, len(read))
	for _, l := range read {
		out = append(out, l.Text)
	}
	return out, ""
}

// rollingOutcomes reduces the runner's report to what grading needs.
func rollingOutcomes(r *TestReport) []insights.RunnerOutcome {
	if r == nil {
		return nil
	}
	out := make([]insights.RunnerOutcome, 0, len(r.Results))
	for _, w := range r.Results {
		out = append(out, insights.RunnerOutcome{
			Name: w.Workflow, Verdict: w.Outcome.Verdict, Detail: w.Outcome.Detail,
		})
	}
	return out
}

func (o *Orchestrator) artifactsDir(envID string) string {
	dir := filepath.Join(o.opts.Root, StateDir, "artifacts", envID)
	// A failure here is not worth refusing the run over: the runner creates
	// what it needs and falls back to writing nothing.
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// previousCommit resolves which commit the previous release is.
//
// The three answers are different questions. `merge-base` is the commit this
// branch was cut from, which under continuous deployment is a commit that was
// deployed. `previous-commit` is HEAD's first parent, which is what a
// repository that deploys every commit on a trunk means by the previous
// release. Anything else is handed to git, so a team that deploys from tags
// writes the tag.
//
// It never guesses. A revision that will not resolve comes back as an error
// with the git message in it, and the caller reports that as blocked, because
// a check that silently compared against the wrong commit would be worse than
// one that did not run.
func (o *Orchestrator) previousCommit(against string) (ref, how string, err error) {
	head, err := o.git("rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("this is not a git checkout, so there is no previous "+
			"release to compare against: %w", err)
	}

	switch against {
	case "", insights.DefaultRollingAgainst:
		base := o.baseBranch()
		if base == "" {
			return "", "", fmt.Errorf("no base branch could be found, so the merge base " +
				"cannot be taken. Set insights.rolling_compatibility.against to a revision, " +
				"or fetch the default branch")
		}
		ref, err = o.git("merge-base", "HEAD", base)
		if err != nil {
			return "", "", fmt.Errorf("the merge base of HEAD and %s could not be taken. A "+
				"shallow checkout is the usual reason; actions/checkout needs fetch-depth: 0 "+
				"for this check: %w", base, err)
		}
		how = "the merge base with " + base
	case "previous-commit":
		ref, err = o.git("rev-parse", "HEAD^")
		if err != nil {
			return "", "", fmt.Errorf("HEAD has no parent in this checkout, which a shallow "+
				"clone also produces: %w", err)
		}
		how = "the commit before HEAD"
	default:
		ref, err = o.git("rev-parse", against+"^{commit}")
		if err != nil {
			return "", "", fmt.Errorf("the revision %q named by "+
				"insights.rolling_compatibility.against does not resolve here: %w", against, err)
		}
		how = "the revision " + against + " named in the manifest"
	}

	if ref == head {
		return "", "", fmt.Errorf("the previous release resolves to this commit, so there "+
			"is no previous release to run. %s", howToFix(against))
	}
	return ref, how, nil
}

func howToFix(against string) string {
	if against == "" || against == insights.DefaultRollingAgainst {
		return "This branch has no commits of its own yet, or it is the base branch."
	}
	return "Point insights.rolling_compatibility.against at an earlier revision."
}

// baseBranch is the branch a pull request is against.
//
// The pull request's own base comes first, because it is the only one of these
// that is a fact rather than a convention: GitHub sets GITHUB_BASE_REF to the
// branch the pull request will merge into. The rest are the conventional names,
// tried in the order that is right most often.
func (o *Orchestrator) baseBranch() string {
	getenv := o.opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if ref := getenv("GITHUB_BASE_REF"); ref != "" {
		for _, candidate := range []string{"origin/" + ref, ref} {
			if _, err := o.git("rev-parse", "--verify", candidate+"^{commit}"); err == nil {
				return candidate
			}
		}
	}
	if head, err := o.git("symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return head
	}
	for _, candidate := range []string{"origin/main", "origin/master", "main", "master"} {
		if _, err := o.git("rev-parse", "--verify", candidate+"^{commit}"); err == nil {
			return candidate
		}
	}
	return ""
}

// exportCommit checks a commit out somewhere the build can read it.
//
// A linked worktree rather than `git archive`, because a worktree is what the
// commit actually looked like. `git archive` applies export-ignore, so a
// repository that keeps a file out of its release tarballs would have that
// file missing from a build that never misses it in reality, and the check
// would report a build failure that only this check can produce.
//
// Outside the repository, and that is not a detail. Nothing here excludes the
// state directory from a build context, so a second checkout under it would be
// tarred up and sent to the daemon by the next build of the repository itself,
// which for a project whose migrations run in its own image happens a few
// lines below this one.
//
// The registration a killed process leaves behind is cleared by `git worktree
// prune`, which both the cleanup here and af down call.
func (o *Orchestrator) exportCommit(ref string) (string, func(), error) {
	top, err := o.git("rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil, fmt.Errorf("the repository root could not be found: %w", err)
	}
	// Through the symlinks on both sides before they are compared. git reports
	// the resolved path and the manifest's root is whatever the caller typed,
	// and on macOS /tmp resolves to /private/tmp, so comparing the two
	// unresolved produces a relative path that climbs out of the repository.
	root := o.opts.Root
	if resolved, rerr := filepath.EvalSymlinks(root); rerr == nil {
		root = resolved
	}
	if resolved, rerr := filepath.EvalSymlinks(top); rerr == nil {
		top = resolved
	}
	inner, err := filepath.Rel(top, root)
	if err != nil {
		return "", nil, fmt.Errorf("the manifest is not inside the repository: %w", err)
	}

	// Deterministic rather than random, so a second run cleans up after a
	// first one that was killed instead of leaving a new directory each time.
	dir := filepath.Join(os.TempDir(), "af-previous-"+o.envID)
	// A directory left by an interrupted run holds the path, and git refuses
	// to add a worktree over one.
	_, _ = o.git("worktree", "remove", "--force", dir)
	_ = os.RemoveAll(dir)
	_, _ = o.git("worktree", "prune")

	if _, err := o.git("worktree", "add", "--detach", dir, ref); err != nil {
		return "", nil, fmt.Errorf("%s could not be checked out. A shallow checkout is the "+
			"usual reason; actions/checkout needs fetch-depth: 0 for this check: %w",
			shortSHA(ref), err)
	}
	cleanup := func() {
		if _, err := o.git("worktree", "remove", "--force", dir); err != nil {
			_ = os.RemoveAll(dir)
			_, _ = o.git("worktree", "prune")
		}
	}
	return filepath.Join(dir, inner), cleanup, nil
}

// git runs one git command in the repository and returns its trimmed output.
func (o *Orchestrator) git(args ...string) (string, error) {
	full := append([]string{"-C", o.opts.Root}, args...)
	cmd := exec.Command("git", full...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), detail)
	}
	return strings.TrimSpace(string(out)), nil
}

func shortSHA(ref string) string {
	if len(ref) > 12 {
		return ref[:12]
	}
	return ref
}

// rollingStatements is the statement list the check reads to decide what a
// migration does.
//
// The migration files come first, because their SQL is complete: a statement
// timing is normalised for display and cut at 160 characters, and an ALTER
// TABLE with several actions can lose one to the cut. The recorded statements
// are the fallback and are the only source for a repository whose migrations
// are Ruby or Python, where there is no SQL on disk to read and the server's
// own record of what ran is all there is.
func rollingStatements(r *insights.Rehearsal) []insights.Statement {
	if r == nil {
		return nil
	}
	var out []insights.Statement
	for _, m := range r.Pending {
		out = append(out, insights.Split(m.Name, m.SQL)...)
	}
	if len(out) > 0 {
		return out
	}
	for i, st := range r.Statements {
		out = append(out, insights.Statement{
			Migration: st.Migration, Index: i + 1, SQL: st.SQL,
		})
	}
	return out
}

// rollingDown removes anything a previous rolling check left behind.
//
// Called from Down, because the environments this check makes carry their own
// identifiers and a teardown of the environment under test would not touch
// them. An interrupted run is exactly when they exist, and an environment that
// outlives its pull request is the leak this product exists to prevent.
func (o *Orchestrator) rollingDown(ctx context.Context, s *session, td *Teardown) {
	for _, suffix := range []string{rollingSuffix, rollingControlSuffix} {
		envID := o.envID + suffix
		if rt, err := s.runtime.Down(ctx, envID); err == nil {
			td.Removed += rt.Removed
			td.Pending = append(td.Pending, rt.Pending...)
		}
		if err := s.dbProv.Destroy(ctx, provider.Branch{EnvID: envID}); err != nil {
			td.Pending = append(td.Pending, provider.PendingResource{
				Kind: "database", ID: envID, Reason: err.Error(),
			})
		}
	}
	dir := filepath.Join(os.TempDir(), "af-previous-"+o.envID)
	if _, err := os.Stat(dir); err == nil {
		if _, err := o.git("worktree", "remove", "--force", dir); err != nil {
			_ = os.RemoveAll(dir)
		}
	}
	// Unconditionally, because the registration outlives the directory: a
	// killed run leaves git believing in a worktree whose files a temporary
	// directory sweeper has already taken away.
	_, _ = o.git("worktree", "prune")
}
