package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// af start answers one question: where am I on the first run, and what is the
// next command.
//
// The first run is eight or nine commands long and every one of them can be
// interrupted: a laptop closes, a download fails, somebody reads the docs for
// twenty minutes and comes back. Before this, coming back meant reconstructing
// the state from memory, and the failure mode was not confusion but repetition,
// because running af init again on a repository that already has a manifest is
// refused, running af up again on a running environment is a no-op nobody
// recognises as one, and neither of those tells you where you actually are.
//
// It derives every answer from the machine rather than from a saved cursor,
// which is the whole reason it is trustworthy. A cursor file records what this
// command last did; the machine records what is true. Somebody who ran af down
// by hand, deleted the manifest, or switched branches has moved, and a cursor
// would keep pointing at the old rung with total confidence.
//
// It runs nothing. No environment is created, no dependency installed, no file
// written. That is not timidity, it is the only way this command can be honest:
// a wizard that runs the steps for you has to report on work it did, and this
// repository has already shipped a green run over a stage that never happened.
// A command that only observes cannot make that mistake, and the observation is
// the part that was missing.
//
// Every rung reports one of four states and never collapses one into another:
//
//	done         observed to be true
//	not yet      observed to be false, and that is where you are
//	blocked      observed to be wrong, and it has to be fixed before the next
//	not checked  deliberately not looked at, with the reason and the command
//
// The fourth is the one that matters. Two rungs cannot be answered here without
// side effects, and answering them with a guess would be worse than the gap.

// StageState is what af start observed about one rung of the first run.
type StageState string

const (
	// StageDone means the rung was observed to be finished.
	StageDone StageState = "done"
	// StagePending means the rung was observed not to be finished, which is
	// where the reader is rather than something wrong.
	StagePending StageState = "pending"
	// StageBlocked means something has to be fixed before the next command
	// can work.
	StageBlocked StageState = "blocked"
	// StageUnchecked means this command deliberately did not look, and says
	// why and what to run instead.
	StageUnchecked StageState = "unchecked"
)

// StartStageJSON is one rung.
type StartStageJSON struct {
	Name    string     `json:"name"`
	State   StageState `json:"state"`
	Detail  string     `json:"detail"`
	Command string     `json:"command,omitempty"`
	// Why is filled in only for an unchecked rung: the reason this command did
	// not look. An unchecked rung with no reason would be a skip pretending to
	// be a decision.
	Why string `json:"why,omitempty"`
	// Optional marks a rung that is genuinely fine left undone, so a reader
	// and a script can both tell "not required" from "not yet".
	Optional bool `json:"optional,omitempty"`
	// Downstream marks an unchecked rung that is waiting on an earlier one
	// rather than one this command declined to read.
	Downstream bool `json:"downstream,omitempty"`
}

// StartJSON is the machine readable form.
type StartJSON struct {
	Stages []StartStageJSON `json:"stages"`
	// Next is the one command to run, empty once the path is walked.
	Next string `json:"next,omitempty"`
	// Complete is true when every required rung is done. It is deliberately
	// not "OK": a first run with three rungs left is going exactly as it
	// should, and a field named OK would read as a failure.
	Complete bool `json:"complete"`
	// Blocked is true when any rung needs fixing, which is what the non zero
	// exit means.
	Blocked bool `json:"blocked"`
}

// stage is one rung, before it is rendered.
type stage struct {
	name    string
	state   StageState
	detail  string
	command string // what to run to move past it
	why     string // why an unchecked rung was not checked
	// prose is the paragraph printed under the next command, explaining what
	// the reader is about to spend time on.
	prose    string
	optional bool
	// downstream marks a rung that could not be read because an earlier one is
	// not done yet, as opposed to one this command declined to read. Both are
	// unchecked and only one of them is worth a paragraph: five rungs each
	// explaining that there is no manifest yet, printed under a next step that
	// is "write a manifest", is noise sitting exactly where the reader is
	// looking for the one thing to do.
	downstream bool
}

func newStartCommand(e *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Say where you are on the first run, and what to run next",
		Long: strings.TrimSpace(`
The first run is a sequence, and a sequence can be interrupted. This reports
each step of it as observed on this machine right now, and names the one command
that moves you forward.

It runs nothing and writes nothing. Every answer comes from the machine rather
than from a record of what this command last did, so closing the laptop,
switching branches, or tearing an environment down by hand all move the answer
with you.

A step that cannot be answered without side effects is reported as not checked,
with the reason and the command that does answer them. That is the point rather
than a gap: a step reported as fine because nothing looked at it is how a green
run over nothing happens.

Exit 0 means every step is either done or simply not reached yet, which is the
normal state of a first run in progress. Exit 3 means a step is broken and the
next command cannot work until it is fixed.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stages := firstRun(cmd.Context(), e, systemStartProbe(e))
			return renderStart(e, stages)
		},
	}
}

// startProbe is everything af start reads outside its own process.
//
// Doctor's Prober covers the machine, and the runtime inventory is the one
// thing it does not carry, so it is added here rather than widened into
// Prober: every existing implementation of that interface would otherwise have
// to grow a method for one command.
type startProbe struct {
	Prober
	// environments is the runtime inventory, which needs a live daemon. A
	// failure here is reported as not checked, never as nothing being held.
	environments func(context.Context, *Env) ([]environment, error)
	// home is where an installed af and the runner live. Read through this
	// rather than through os.UserHomeDir so that a test can put a fixture
	// install somewhere and have every rung agree about where it is.
	home func() (string, error)
}

func systemStartProbe(e *Env) startProbe {
	return startProbe{
		Prober:       systemProber{getenv: e.Getenv},
		environments: listEnvironments,
		home:         os.UserHomeDir,
	}
}

// firstRun walks the path in order and reports each rung.
//
// In order, and every rung is evaluated even when an earlier one is blocked,
// because the reader is entitled to the whole picture. Reporting only up to the
// first problem is how somebody fixes Docker, runs this again, and discovers
// their node is too old, twice in a row.
func firstRun(ctx context.Context, e *Env, p startProbe) []stage {
	m, root, manifestStage := manifestState(e)
	return []stage{
		installState(e, p),
		dockerState(ctx, e, p),
		runnerState(ctx, e, p),
		manifestStage,
		databaseState(ctx, e, m),
		goldenState(e, m, root),
		environmentState(ctx, e, m, p),
		workflowState(m),
		modelState(ctx, e),
		evidenceState(e, m, root),
		cleanupState(ctx, e, p),
	}
}

// installState reports whether the shell will find af by name.
//
// The installer writes the PATH line itself now, but the terminal that ran
// curl | sh predates the line it wrote, and a container or a CI job may have
// neither. Somebody in that terminal is running this by its full path, which
// works and which will stop working the moment they open a new one or paste a
// command from the docs, so it is worth saying while they can still act on it.
//
// The remedy names the INSTALLED af rather than the running one. The first
// version printed the directory the running binary sat in, which on a
// development build told the reader to put /tmp on their PATH: correct about
// the fact and useless as advice. A binary run out of a build directory is not
// an install with a PATH problem, and this says so rather than inventing a step
// for it.
func installState(e *Env, p startProbe) stage {
	s := stage{name: "af on your PATH"}
	self, err := os.Executable()
	if err != nil {
		s.state, s.why = StageUnchecked, "this platform did not report the running binary's own path"
		s.detail = "not checked"
		return s
	}
	if found, lookErr := p.LookPath("af"); lookErr == nil {
		if same, cmpErr := sameFile(found, self); cmpErr == nil && same {
			s.state, s.detail = StageDone, short(e.WorkDir, found)
			return s
		}
		// A different af earlier on PATH is worse than none, because every
		// command in the docs would run the other one and nothing would say so.
		s.state = StageBlocked
		s.detail = fmt.Sprintf("%s is first on your PATH, and this is %s",
			short(e.WorkDir, found), short(e.WorkDir, self))
		s.prose = "Two copies of af are installed and the shell picks the other one, so every " +
			"command below would run a different build from this one."
		s.command = "which -a af"
		return s
	}

	installed, ok := installedBinary(e, p)
	if !ok {
		s.state = StageUnchecked
		s.detail = "not checked"
		s.why = fmt.Sprintf("you are running %s, which is a build rather than an installed af, "+
			"so there is no install for this to have a view about", short(e.WorkDir, self))
		return s
	}
	s.state = StagePending
	s.detail = "not on your PATH, although " + short(e.WorkDir, installed) + " is installed"
	s.prose = "The terminal you installed in started before the installer wrote its line, so it " +
		"does not know about af yet. Paste this, or open a new terminal."
	s.command = fmt.Sprintf(`export PATH="%s:$PATH"`, filepath.Dir(installed))
	return s
}

// installedBinary is where install.sh puts af, honouring the same two variables
// it reads so that a prefix somebody chose is the prefix this looks in.
func installedBinary(e *Env, p startProbe) (string, bool) {
	dir := e.Getenv("AF_BIN_DIR")
	if dir == "" {
		prefix := e.Getenv("AF_PREFIX")
		if prefix == "" {
			home, err := p.home()
			if err != nil {
				return "", false
			}
			prefix = filepath.Join(home, ".antifailure")
		}
		dir = filepath.Join(prefix, "bin")
	}
	path := filepath.Join(dir, "af")
	if _, err := p.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

func sameFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

// dockerState reuses doctor's own check rather than asking again, so the two
// commands cannot disagree about the same daemon.
func dockerState(ctx context.Context, e *Env, p startProbe) stage {
	r := checkDocker(ctx, e, p)
	s := stage{name: "Docker", detail: r.Detail}
	switch r.Status {
	case CheckPass:
		s.state = StageDone
	case CheckSkip:
		s.state, s.why = StageUnchecked, r.Detail
	default:
		s.state = StageBlocked
		s.prose = r.Remediation
		s.command = "af doctor"
	}
	return s
}

// runnerState reuses af runner check, for the same reason.
//
// The runner is the step the README omitted entirely and the one the installer
// used to point at a path where nothing could be installed from, so it gets a
// rung of its own rather than being folded into the machine check.
func runnerState(ctx context.Context, e *Env, p startProbe) stage {
	s := stage{name: "the agent runner", command: "af runner install"}
	if _, err := p.home(); err != nil {
		s.state, s.why = StageUnchecked, "this platform did not report a home directory"
		s.detail, s.command = "not checked", ""
		return s
	}
	// The runner af test would use from here, not ~/.antifailure/runner
	// unconditionally. This rung reported "installed" while a run in the same
	// directory took a nearer runner with no dependencies and died in node.
	target, passedOver, err := runnerToCheck(e.WorkDir)
	if err != nil {
		s.state, s.why = StageUnchecked, err.Error()
		s.detail, s.command = "not checked", ""
		return s
	}
	results := append(passedOverChecks(passedOver), checkRunner(ctx, target)...)
	var blockers, warnings []string
	for _, r := range results {
		if r.symbol == SymbolOK || r.symbol == SymbolSkip {
			continue
		}
		if r.decides {
			blockers = append(blockers, r.label+": "+r.detail)
			continue
		}
		warnings = append(warnings, r.label+": "+r.detail)
	}
	switch {
	case runnerVerdict(results) == VerdictUndetermined && len(blockers) == 0:
		// A deciding question that could not be answered. This rung used to
		// skip past a `skip` with the other unremarkable answers and report
		// "installed", so a runner whose package.json cannot be parsed made
		// af start say the runner step was done. Every other rung in this
		// command already has StageUnchecked for exactly this; the runner rung
		// was the one that did not use it.
		s.state, s.why = StageUnchecked, strings.Join(unanswered(results), "; ")
		s.detail, s.command = "not checked", ""
	case len(blockers) > 0:
		// Pending rather than blocked. Nothing is wrong with the machine; the
		// runner has simply not been installed yet, and af runner install is
		// the ordinary next command rather than a repair.
		s.state, s.detail = StagePending, strings.Join(blockers, "; ")
		s.prose = "Copies the runner beside af, installs exactly what its lockfile names, " +
			"and downloads chromium. It needs node 22.6 or newer."
	case len(warnings) > 0:
		s.state, s.detail = StageDone, "installed, with "+strings.Join(warnings, "; ")
		s.command = ""
	default:
		s.state, s.detail = StageDone, "installed at "+short(e.WorkDir, target)
		s.command = ""
	}
	return s
}

// manifestState loads the manifest once and hands it to every rung below, so a
// run of af start parses it exactly once.
func manifestState(e *Env) (m *schema.Manifest, root string, s stage) {
	s = stage{name: "a manifest", command: "af init"}
	path, err := manifest.Find(e.WorkDir)
	if err != nil {
		s.state = StagePending
		s.detail = "no antifailure.yaml here or in any parent directory"
		s.prose = "Reads the repository and writes antifailure.yaml: your services, how they " +
			"build, where the database comes from, and what the environment may reach."
		return nil, "", s
	}
	root = repoRoot(path)
	m, err = manifest.Load(path)
	if err != nil {
		s.state = StageBlocked
		var problems *manifest.Errors
		if errors.As(err, &problems) {
			s.detail = fmt.Sprintf("%s has %s", short(e.WorkDir, path),
				plural(len(problems.Problems), "problem", "problems"))
			if len(problems.Problems) > 0 {
				p := problems.Problems[0]
				s.detail += fmt.Sprintf(", the first at line %d: %s", p.Line, p.Message)
			}
			s.prose = "Fix the manifest, or write a fresh one over it with af init --force. " +
				"af explain reads it back to you once it parses."
			s.command = "af explain"
			return nil, root, s
		}
		s.detail = err.Error()
		s.prose = "The manifest could not be read."
		s.command = "af explain"
		return nil, root, s
	}
	s.state = StageDone
	s.detail = fmt.Sprintf("%s, %s", short(e.WorkDir, path),
		plural(len(m.Services), "service", "services"))
	s.command = ""
	return m, root, s
}

// waitingOnManifest is the state every rung below the manifest reports until
// there is one. One sentence rather than five, because they all say the same
// thing and the manifest rung above already says it once.
func waitingOnManifest(name string) stage {
	return stage{
		name: name, state: StageUnchecked, downstream: true,
		detail: "after the manifest",
		why:    "there is no manifest to read it from yet",
	}
}

// databaseState reports whether the manifest's database source can be reached
// for, without reaching for it.
//
// Constructing the provider is what proves it, and constructing it creates a
// client, a lock and a state database, so this checks the inputs that
// construction reads instead: the provider kind, and for a hosted one the
// project and the key it names. Getting those wrong is the failure a first run
// actually hits, and it is decidable from the manifest and the secret chain
// with nothing created.
func databaseState(ctx context.Context, e *Env, m *schema.Manifest) stage {
	if m == nil {
		return waitingOnManifest("the database source")
	}
	s := stage{name: "the database source"}
	if m.Database == nil {
		// Not every manifest wants one. A service-only manifest is valid and
		// af up will build it, so this is not pending: there is nothing to do.
		s.state, s.detail = StageDone, "none declared, so no database branch is made"
		return s
	}
	provider := m.Database.Provider
	if provider == "" {
		provider = schema.DBDocker
	}
	// The variable naming production is checked before the provider, because
	// it decides the same thing for every one of them: whether there is
	// anything to copy. This step reported "docker, so it comes from the
	// daemon checked above" for a manifest that named production and a shell
	// that did not hold it, which is a step reported as finished while the
	// thing it names is missing. The refresh that followed used to publish an
	// empty golden from it, and now refuses with AF-DB-016, so the command
	// whose whole job is to say where you are was the last thing still saying
	// this was fine.
	if src := sourceState(ctx, e, m, string(provider)); src != nil {
		return *src
	}
	if provider == schema.DBDocker {
		s.state = StageDone
		s.detail = "docker, so it comes from the daemon checked above"
		return s
	}
	// A hosted provider needs a project and a key, and both are read from
	// the manifest and the same secret chain the orchestrator would use.
	if m.Database.Project == "" {
		s.state = StageBlocked
		s.detail = fmt.Sprintf("%s needs database.project, which the manifest does not set", provider)
		s.prose = "Add the project to the database block in antifailure.yaml."
		s.command = "af explain"
		return s
	}
	name := m.Database.APIKeyEnv
	if name == "" {
		name = strings.ToUpper(string(provider)) + "_API_KEY"
	}
	chain := modelChain(e)
	if _, _, found, err := chain.Lookup(ctx, name); err != nil {
		s.state, s.why = StageUnchecked, "a source in the chain could not be read: "+err.Error()
		s.detail = "not checked"
		return s
	} else if !found {
		s.state = StageBlocked
		s.detail = fmt.Sprintf("%s is configured and %s was not found", provider, name)
		s.prose = fmt.Sprintf("Set %s in this shell, in .env, or in the encrypted store, "+
			"then run this again. Antifailure never asks you to paste a key on a command line.", name)
		s.command = "af secret set " + name
		return s
	}
	s.state = StageDone
	s.detail = fmt.Sprintf("%s, project %s, %s found", provider, m.Database.Project, name)
	return s
}

// goldenState is one of the two rungs this command will not answer.
//
// Whether a golden exists is knowable only through the orchestrator, and
// Orchestrator.Goldens goes through open: it creates .antifailure, takes this
// branch's lock file, and migrates the state database. A status command that
// does that is one somebody cannot run while af up is halfway through, and it
// would be the second thing on this machine to fail with AF-RUN-003 for a
// reason nobody could see. So this reports the golden CONFIGURATION, which is
// decidable from the manifest, and says plainly that existence was not checked.
func goldenState(e *Env, m *schema.Manifest, root string) stage {
	s := stage{
		name: "a golden",
		why: "listing goldens takes this branch's lock, so a command meant to be safe to " +
			"run while af up is in flight cannot ask",
		command: "af golden list",
	}
	if m == nil {
		return waitingOnManifest("a golden")
	}
	if m.Database == nil {
		s.state, s.detail = StageDone, "no database, so nothing is branched from a golden"
		s.why, s.command = "", ""
		return s
	}
	// Reported, not judged. The first version of this rung called an absent
	// masking file a blocker, which was a false finding and the one thing a
	// gate cannot afford: env/golden.go reads that path and treats
	// os.IsNotExist as "use the built in rules", saying in its own comment that
	// a missing file is the common case and not an error. So a manifest that
	// af init has just written, which names masking.yaml by way of the
	// normaliser's default and does not create it, was reported as broken on
	// every first run.
	//
	// What is worth saying is which rules a branch would be masked by, because
	// the two are different and neither is visible from the manifest alone: the
	// file names a path whether or not anybody wrote one.
	rules := m.Database.MaskingRules
	path := rules
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, rules)
	}
	s.state = StageUnchecked
	if _, err := os.Stat(path); err != nil {
		s.detail = "masked by the built in rules, since " + rules + " is not there"
		return s
	}
	s.detail = "masked by " + rules + "; whether a golden exists was not checked"
	return s
}

// environmentState asks the runtime what is standing, which needs no lock.
func environmentState(ctx context.Context, e *Env, m *schema.Manifest, p startProbe) stage {
	if m == nil {
		return waitingOnManifest("an environment")
	}
	s := stage{name: "an environment", command: "af up"}
	envs, err := p.environments(ctx, e)
	if err != nil {
		s.state, s.why = StageUnchecked, "the runtime did not answer: "+err.Error()
		s.detail, s.command = "not checked", ""
		return s
	}
	want := env.EnvID(m.Name, currentBranch(e.WorkDir))
	for _, found := range envs {
		if found.ID != want {
			continue
		}
		if found.Running > 0 {
			s.state = StageDone
			s.detail = fmt.Sprintf("%s, %s running", found.ID,
				plural(found.Running, "service", "services"))
			s.command = ""
			return s
		}
		// Held and not running is its own state, and the two have different
		// remedies. Reporting it as absent would send somebody to af up, which
		// is right, without telling them there is already a half built
		// environment underneath that af up will reuse.
		s.state = StagePending
		s.detail = fmt.Sprintf("%s exists with %s and nothing running",
			found.ID, plural(found.Resources, "resource", "resources"))
		s.prose = "An environment for this branch is half built, from a run that did not " +
			"finish. af up carries on from where it stopped; af down clears it."
		return s
	}
	s.state = StagePending
	s.detail = "none for branch " + currentBranch(e.WorkDir)
	s.prose = "Builds the environment for this branch: a database branch made from the " +
		"golden, your services built and started, and a sealed network. The first one " +
		"takes a few minutes because the images are built."
	return s
}

// workflowState is the rung that stops a green run over nothing.
//
// A manifest with no workflows makes af test refuse, which is right, but it
// refuses at the end of a build that took several minutes. Saying it here costs
// nothing and is the difference between finding out now and finding out after
// af up.
func workflowState(m *schema.Manifest) stage {
	if m == nil {
		return waitingOnManifest("workflows to run")
	}
	s := stage{name: "workflows to run", command: "af test"}
	if len(m.Workflows) == 0 {
		s.state = StageBlocked
		s.detail = "none declared, so af test would have nothing to run"
		s.prose = "Add a workflows block to antifailure.yaml. Each one is a sentence naming " +
			"a persona and what they do, and af test refuses a manifest with none rather " +
			"than reporting a run that examined nothing."
		s.command = "af explain"
		return s
	}
	// A workflow naming a persona the manifest does not declare is caught by
	// validation, so reaching here means every name resolves.
	s.state = StageDone
	s.detail = fmt.Sprintf("%s, %s",
		plural(len(m.Workflows), "workflow", "workflows"),
		plural(len(m.Personas), "persona", "personas"))
	return s
}

// modelState reports the key without ever reading it, and without asking for
// one.
//
// Optional on purpose and marked so. Everything on this path works with no key:
// the workflows run, the agents plan deterministically, and the verdicts are
// real. Presenting it as a missing step would be false, and asking for the key
// would be worse, so this says what is set, what it changes, and the one
// command that sets it, which reads the key without putting it on a command
// line.
func modelState(ctx context.Context, e *Env) stage {
	s := stage{name: "a model key", optional: true, command: "af model set anthropic"}
	cfg, err := model.Resolve(ctx, modelChain(e))
	if err != nil {
		s.state, s.why = StageUnchecked, "a source in the chain could not be read: "+err.Error()
		s.detail, s.command = "not checked", ""
		return s
	}
	if cfg == nil {
		// Done rather than pending. There is nothing outstanding here: the
		// deterministic planner is a supported mode, not a degraded one, and a
		// rung that reads as unfinished forever is a rung people learn to
		// ignore.
		s.state = StageDone
		s.detail = "none set, so agents use the deterministic planner"
		s.prose = "Optional. Workflows run and return real verdicts without one. A key lets " +
			"the agents read a page they have not seen before, and af model set reads it " +
			"without putting it on a command line or in a file."
		return s
	}
	s.state, s.command = StageDone, ""
	s.detail = fmt.Sprintf("%s %s, from %s", cfg.Provider.Name, cfg.Model, cfg.Source)
	if rec := model.ReadRecord(e.WorkDir, cfg.Fingerprint); rec == nil {
		s.detail += ", never verified"
		s.command = "af model test"
	}
	return s
}

// evidenceState looks for what a run leaves behind.
//
// The artifacts directory rather than a report, because af test writes no
// report file: the videos, traces and screenshots are the durable evidence, and
// af ci writes markdown only where somebody names a path. Saying "no run has
// left any yet" is therefore an honest reading of the disk rather than a claim
// that no run happened.
func evidenceState(e *Env, m *schema.Manifest, root string) stage {
	if m == nil {
		return waitingOnManifest("evidence on disk")
	}
	s := stage{name: "evidence on disk", command: "af test"}
	dir := filepath.Join(root, ".antifailure", "artifacts")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		s.state = StagePending
		s.detail = "none yet, under " + short(e.WorkDir, dir)
		s.prose = "Agents drive the application and return pass, fail, flaky, blocked or " +
			"unverified, each with a video, a trace and steps to reproduce it. Only a real " +
			"failure exits non zero."
		return s
	}
	s.state = StageDone
	s.detail = fmt.Sprintf("artifacts from %s, under %s",
		plural(len(entries), "run", "runs"), short(e.WorkDir, dir))
	s.command = ""
	return s
}

// cleanupState is last because it is the one rung that is done at the start and
// becomes undone by using the product.
func cleanupState(ctx context.Context, e *Env, p startProbe) stage {
	s := stage{name: "nothing left behind", command: "af env prune"}
	envs, err := p.environments(ctx, e)
	if err != nil {
		s.state, s.why = StageUnchecked, "the runtime did not answer: "+err.Error()
		s.detail, s.command = "not checked", ""
		return s
	}
	status, detail := leftoverVerdict(envs, e.Clock.Now())
	s.detail = detail
	if status == CheckWarn {
		s.state = StagePending
		s.prose = "Environments from runs that did not tear down are still holding a database " +
			"branch and a network each. af env prune takes anything older than a day."
		return s
	}
	s.state, s.command = StageDone, ""
	return s
}

// renderStart prints the ladder, then the one command that moves the reader
// forward, then every rung this command declined to answer.
//
// The declined rungs are printed last and in full rather than left as a symbol
// in the list, because a reader who scans the symbols and stops has been told
// nothing about them, and this repository's most expensive defects have all
// been a check that looked like it passed while examining nothing.
func renderStart(e *Env, stages []stage) error {
	next, blocked := nextStep(stages)

	if e.Out.Format == FormatJSON {
		doc := StartJSON{Blocked: blocked, Complete: next == nil}
		for _, s := range stages {
			doc.Stages = append(doc.Stages, StartStageJSON{
				Name: s.name, State: s.state, Detail: s.detail,
				Command: s.command, Why: s.why, Optional: s.optional,
				Downstream: s.downstream,
			})
		}
		if next != nil {
			doc.Next = next.command
		}
		if err := e.Out.JSON(doc); err != nil {
			return err
		}
		if blocked {
			return &silentError{code: aferrors.ExitConfiguration}
		}
		return nil
	}

	e.Out.Section("Your first run")
	for _, s := range stages {
		e.Out.Status(symbolForStage(s.state), s.name, s.detail)
	}

	if next != nil {
		e.Out.Section("Next")
		e.Out.Printf("\n    %s\n\n", e.Out.S(StyleBold, next.command))
		if next.prose != "" {
			e.Out.Printf("  %s\n", e.Out.Wrap(next.prose, 2))
		}
	} else {
		e.Out.Section("Next")
		e.Out.Printf("\n  %s\n\n",
			e.Out.S(StyleGood, "You have walked the whole first run on this machine."))
		e.Out.Printf("  %s\n", e.Out.Wrap(
			"Everything above was observed rather than remembered, so this stays true only "+
				"while it is true. When you are finished with the environment, af down removes "+
				"every resource it created.", 2))
	}

	var unchecked []stage
	for _, s := range stages {
		if s.state == StageUnchecked && !s.downstream {
			unchecked = append(unchecked, s)
		}
	}
	if len(unchecked) > 0 {
		e.Out.Section("Not checked here")
		for _, s := range unchecked {
			e.Out.Printf("  %s\n", e.Out.S(StyleBold, s.name))
			e.Out.Printf("  %s\n", e.Out.Wrap(s.why, 2))
			if s.command != "" {
				e.Out.Hint("  Ask with", s.command)
			}
			e.Out.Println("")
		}
	}

	if blocked {
		return &silentError{code: aferrors.ExitConfiguration}
	}
	return nil
}

// nextStep picks the one command to print.
//
// The first blocked rung wins over the first pending one wherever both exist,
// because a blocked rung is what would make the pending one fail. An optional
// rung is never the next step: it is offered in the list and never stands
// between somebody and their first verdict.
func nextStep(stages []stage) (next *stage, blocked bool) {
	var pending *stage
	for i := range stages {
		s := &stages[i]
		if s.optional {
			continue
		}
		switch s.state {
		case StageBlocked:
			blocked = true
			if next == nil {
				next = s
			}
		case StagePending:
			if pending == nil {
				pending = s
			}
		}
	}
	if next != nil {
		return next, blocked
	}
	return pending, blocked
}

// short abbreviates a path for the ladder.
//
// The details column is what is left of eighty after a label, and a full
// artifacts path under a temp directory wrapped onto three lines, which pushed
// the useful half of every other row out of the reader's eye. Home becomes ~,
// and a path under the working directory becomes relative to it, which is how
// the reader thinks of it anyway.
func short(workDir, path string) string {
	if rel, err := filepath.Rel(workDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		if rel == "." {
			return "."
		}
		return rel
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, relErr := filepath.Rel(home, path); relErr == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
}

func symbolForStage(s StageState) string {
	switch s {
	case StageDone:
		return SymbolOK
	case StageBlocked:
		return SymbolFail
	case StagePending:
		return SymbolPending
	default:
		return SymbolSkip
	}
}

// sourceState reports the variable naming production, or nil when the manifest
// names none and the provider's own check should answer instead.
//
// It reads the same chain the refresh reads, through the same constructor, so
// the two cannot describe different places. A step that said the value was in
// .env while the refresh looked only at the shell would be the same defect
// pointing the other way.
func sourceState(ctx context.Context, e *Env, m *schema.Manifest, provider string) *stage {
	name := m.Database.SourceURLEnv
	if name == "" {
		return nil
	}
	s := stage{name: "the database source"}
	value, res, found, err := modelChain(e).Lookup(ctx, name)
	switch {
	case err != nil:
		s.state, s.why = StageUnchecked, "a source in the chain could not be read: "+err.Error()
		s.detail = "not checked"
	case !found || strings.TrimSpace(value.Reveal()) == "":
		s.state = StageBlocked
		s.detail = fmt.Sprintf(
			"%s copies the database named by %s, and no configured source has it",
			provider, name)
		s.prose = fmt.Sprintf(
			"Put production's read only connection string in %s, in this shell, "+
				"in .env, or in the encrypted store. Without it a refresh has "+
				"nothing to copy and refuses rather than making an empty golden.", name)
		s.command = "af secret set " + name
	default:
		// The fingerprint rather than the value, for the same reason the model
		// key is reported that way: it is enough to tell two credentials apart
		// and it is not the credential.
		s.state = StageDone
		s.detail = fmt.Sprintf("%s, copying the database named by %s, found in %s",
			provider, name, res.Source)
	}
	return &s
}
