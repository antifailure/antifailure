package env

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Exploration goes through the same runner subprocess a workflow does.
//
// The browser, the sign in, the evidence capture and the JSON boundary are all
// the same; only the planner differs. A second entry point would have meant a
// second place for the boundary to drift, and the first thing to drift would
// have been the evidence, because it is the part nobody exercises by hand.

// ExploreOptions configure an exploratory run.
type ExploreOptions struct {
	// Only runs just the named goals. Empty runs all of them.
	Only []string
	// Seed overrides every goal's seed, which is how a run is replayed from
	// what a report printed without editing the manifest.
	Seed string
	// Headed shows the browser, for somebody watching it wander.
	Headed bool
	// RunnerPath overrides where the runner lives.
	RunnerPath string
}

// goalDoc is what the runner reads. The field names are the runner's, not the
// manifest's, because this document is written for TypeScript to consume.
type goalDoc struct {
	Name      string `json:"name"`
	Goal      string `json:"goal"`
	Persona   string `json:"persona,omitempty"`
	Seed      string `json:"seed"`
	StartPath string `json:"startPath,omitempty"`
	MaxSteps  int    `json:"maxSteps,omitempty"`
	SlowMs    int    `json:"slowMs,omitempty"`
}

// resultDocument is the half of the runner's output an exploration cares
// about. The workflow half of the same document is decoded into TestReport by
// Test, which is why the counts and the results are not repeated here.
type resultDocument struct {
	Explorations []explore.Exploration `json:"explorations"`
}

// Explore sends agents at the manifest's goals with no declared workflow.
func (o *Orchestrator) Explore(ctx context.Context, opts ExploreOptions) (*explore.Report, error) {
	cfg := o.opts.Manifest.Explore
	if cfg == nil || !cfg.Enabled || len(cfg.Goals) == 0 {
		return nil, aferrors.Coded(aferrors.AFAGT020,
			"detail", "the manifest declares no goals under explore")
	}

	status, err := o.Status(ctx)
	if err != nil {
		return nil, err
	}
	if status.URL == "" {
		return nil, aferrors.Coded(aferrors.AFAGT020,
			"detail", "nothing is running for this branch; bring it up with 'af up' first")
	}

	goals := o.goalDocs(opts)
	if len(goals) == 0 {
		return nil, aferrors.Coded(aferrors.AFAGT021, "goal", strings.Join(opts.Only, ", "))
	}

	// The personas have to exist before the browser opens, for the same reason
	// a workflow's do: an exploration handed an account nobody created reports
	// a sign in the application refused, which reads as a finding about the
	// application and is a fact about the environment.
	provisioned, err := o.ProvisionPersonas(ctx)
	if err != nil {
		return nil, err
	}

	job := jobDocument{
		BaseURL: status.URL,
		// Explorations write into the same directory workflows do, so one run
		// leaves one place to look.
		Artifacts: filepath.Join(o.opts.Root, StateDir, "artifacts", o.envID),
		Goals:     goals,
		Personas:  o.personaDocs(provisioned),
		WorkDir:   o.opts.Root,
		Headless:  !opts.Headed,
	}
	if self, err := os.Executable(); err == nil {
		job.AF = self
	}

	out, err := o.invokeRunner(ctx, opts.RunnerPath, job)
	if err != nil {
		return nil, err
	}
	var doc resultDocument
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFAGT003,
			"detail", "the runner's output could not be read: "+err.Error())
	}
	return &explore.Report{Explorations: doc.Explorations}, nil
}

// Goals is what the manifest declares, for a caller that has to map a result
// back to the goal that produced it.
func (o *Orchestrator) Goals() []schema.Goal {
	if o.opts.Manifest.Explore == nil {
		return nil
	}
	return o.opts.Manifest.Explore.Goals
}

// goalDocs turns the manifest's goals into what the runner reads.
func (o *Orchestrator) goalDocs(opts ExploreOptions) []goalDoc {
	wanted := map[string]bool{}
	for _, n := range opts.Only {
		wanted[n] = true
	}
	var out []goalDoc
	for _, g := range o.opts.Manifest.Explore.Goals {
		if len(wanted) > 0 && !wanted[g.Name] {
			continue
		}
		doc := goalDoc{
			Name: g.Name, Goal: g.Goal, Persona: g.Persona,
			Seed: g.Seed, StartPath: g.StartPath, SlowMs: g.SlowMs,
		}
		if g.Budget != nil {
			doc.MaxSteps = g.Budget.Steps
		}
		// The override is applied here rather than in the manifest so that a
		// replay changes nothing on disk: somebody pastes the command a report
		// printed and gets the same path, with the file untouched.
		if opts.Seed != "" {
			doc.Seed = opts.Seed
		}
		out = append(out, doc)
	}
	return out
}

// runnerEnvironment is what the runner subprocess receives.
//
// This process's environment, plus the model key if one resolved from anywhere
// the engine can see. Appended rather than substituted, so a key that was
// already exported is passed through untouched and the two agree by
// construction: the process environment is the first source the chain asks, so
// a resolved key from a lower source is only ever added where there was none.
//
// Unlike the environment a service receives, this is a pass-through. The runner
// is af's own subprocess running on this machine, not a container in the
// preview environment, and it needs node's own configuration, a home directory
// and a browser cache. The rule that a service gets only what the manifest
// declares is about isolating the application under test and does not apply to
// the tool driving it.
func (o *Orchestrator) runnerEnvironment(ctx context.Context) []string {
	env := os.Environ()

	cfg, err := model.Resolve(ctx, o.secretChain())
	if err != nil || cfg == nil {
		// A key that cannot be resolved is not a reason to fail a run. With no
		// key the deterministic planner runs, which is a supported mode, and
		// stopping here would turn a locked keyring into a failed test suite.
		return env
	}
	// Registered before it is handed over, so that nothing the runner prints
	// on the way to AF-AGT-003 can carry it into an error message.
	o.opts.Redactor.Register(cfg.Key.Reveal())
	return append(env, cfg.Environment()...)
}

// invokeRunner runs the runner over the document boundary and returns what it
// wrote.
//
// Extracted so that Test and Explore cannot disagree about how the subprocess
// is started, what happens when it writes nothing, or whether its output is
// redacted. Two copies would have drifted first at the error path, which is
// the one nobody exercises by hand.
// runnerDocument marshals the job the runner reads.
//
// A nil slice marshals as null and an empty one as [], and the difference is
// fatal in the TypeScript that reads them. Every af explore run sent
// "workflows": null, because the exploration path never sets that field, and
// main.ts read doc.workflows.length before it looked at the goals. The command
// exited AF-AGT-003 with a TypeError every time, on every machine.
//
// Nothing caught it because nothing anywhere drove main.ts. The runner's own
// suite tests explore() directly, and the one Go test that reached a real
// subprocess replaced node with a shell script. Both halves worked and the
// document between them was never sent by a test.
//
// Fixed on both sides on purpose. Here, so the shape the engine promises is
// the shape it sends, and in the runner, which is tolerant on a boundary it
// does not control.
func (o *Orchestrator) runnerDocument(job jobDocument) ([]byte, error) {
	if job.Workflows == nil {
		job.Workflows = []workflowDoc{}
	}
	if job.Personas == nil {
		job.Personas = []personaDoc{}
	}
	if job.Goals == nil {
		job.Goals = []goalDoc{}
	}
	return json.Marshal(job)
}

func (o *Orchestrator) invokeRunner(
	ctx context.Context, runnerPath string, job jobDocument,
) ([]byte, error) {
	runner, err := o.findRunner(runnerPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(job.Artifacts, 0o755); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFAGT001, "detail", err.Error())
	}

	// A nil slice marshals as null and an empty one as [], and the runner reads
	// this document in TypeScript where the difference is fatal rather than
	// cosmetic. Every af explore run sent "workflows": null, because the
	// exploration path never sets that field, and main.ts reads
	// doc.workflows.length before it looks at the goals. So af explore could
	// not produce a report on any machine: it exited AF-AGT-003 with a
	// TypeError every time.
	//
	// Nothing caught it because nothing anywhere drives main.ts. The runner's
	// own suite tests explore() directly, and the one Go test that reaches a
	// subprocess replaces node with a shell script. Both halves work and the
	// document between them was never sent by a test.
	//
	// Fixed on both sides. Here, so the shape the engine promises is the shape
	// it sends, and in the runner, which is tolerant on a boundary it does not
	// control.
	body, err := o.runnerDocument(job)
	if err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "node", "--experimental-strip-types", runner)
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = o.opts.Root
	// The runner inherited this process's environment and nothing else, which
	// meant a model key was only ever reachable by exporting a variable. A key
	// in the keyring or the encrypted store resolved correctly everywhere the
	// engine looked and then never reached the one process that needed it, so
	// 'af model set' would have been a command that stored a key and changed
	// nothing about a run.
	cmd.Env = o.runnerEnvironment(ctx)

	// A non zero exit with a readable document is a result the caller decides
	// about, so the error is deliberately dropped. Only silence is fatal.
	_ = cmd.Run()
	if stdout.Len() == 0 {
		// The runner produced nothing, which is the runner's own failure and
		// not the application's. Its output is the only thing that explains it.
		return nil, aferrors.Coded(aferrors.AFAGT003,
			"detail", strings.TrimSpace(o.opts.Redactor.String(stderr.String())))
	}
	return stdout.Bytes(), nil
}
