package env

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The runner is a subprocess with a document boundary rather than a library.
//
// The engine is Go and the browser automation that actually works is
// TypeScript. The alternatives are a worse browser driver or a foreign
// function interface, and a JSON document in and a JSON document out is
// neither. It is also a boundary a person can drive by hand, which is worth
// more than it sounds the first time somebody has to debug it.

// TestOptions configure a run.
type TestOptions struct {
	// Only runs just the named workflows. Empty runs all of them.
	Only []string
	// Attempts is how many times a workflow is tried before being called
	// flaky or failed.
	Attempts int
	// Headed shows the browser, for somebody watching it work.
	Headed bool
	// RunnerPath overrides where the runner lives.
	RunnerPath string
}

// TestReport is what a run produced.
type TestReport struct {
	Results    []WorkflowResult `json:"results"`
	Passed     int              `json:"passed"`
	Failed     int              `json:"failed"`
	Flaky      int              `json:"flaky"`
	Blocked    int              `json:"blocked"`
	Unverified int              `json:"unverified"`
	Duration   time.Duration    `json:"-"`
}

// WorkflowResult is one workflow's outcome.
type WorkflowResult struct {
	Workflow string `json:"workflow"`
	Outcome  struct {
		Verdict      string   `json:"verdict"`
		Cause        string   `json:"cause"`
		Detail       string   `json:"detail"`
		Reproduction []string `json:"reproduction"`
	} `json:"outcome"`
	Steps    []string `json:"steps"`
	Evidence struct {
		Video      string   `json:"video"`
		Trace      string   `json:"trace"`
		Screenshot string   `json:"screenshot"`
		Console    []string `json:"console"`
		Failed     []string `json:"failed"`
	} `json:"evidence"`
	DurationMs int64 `json:"durationMs"`
}

// AnyFailed reports whether anything counted against the application.
//
// Blocked does not, and that is deliberate: an incomplete environment must not
// be indistinguishable from a broken application, or people stop reading
// either.
func (r TestReport) AnyFailed() bool { return r.Failed > 0 }

// jobDocument is what the runner reads.
type jobDocument struct {
	BaseURL   string        `json:"base_url"`
	Artifacts string        `json:"artifacts"`
	Workflows []workflowDoc `json:"workflows"`
	Personas  []personaDoc  `json:"personas"`
	AF        string        `json:"af,omitempty"`
	WorkDir   string        `json:"work_dir,omitempty"`
	Attempts  int           `json:"attempts,omitempty"`
	Headless  bool          `json:"headless"`
}

type workflowDoc struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Persona     string   `json:"persona,omitempty"`
	Expect      []string `json:"expect"`
	StartPath   string   `json:"startPath,omitempty"`
}

type personaDoc struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password,omitempty"`
	Role     string `json:"role,omitempty"`
	Login    string `json:"login"`
}

// Test runs the manifest's workflows against the running environment.
func (o *Orchestrator) Test(ctx context.Context, opts TestOptions) (*TestReport, error) {
	status, err := o.Status(ctx)
	if err != nil {
		return nil, err
	}
	if status.URL == "" {
		return nil, aferrors.Coded(aferrors.AFAGT001,
			"detail", "nothing is running for this branch; bring it up with 'af up' first")
	}

	workflows := o.workflowDocs(opts.Only)
	if len(workflows) == 0 {
		return nil, aferrors.Coded(aferrors.AFAGT001,
			"detail", "the manifest declares no workflows to run")
	}

	runner, err := o.findRunner(opts.RunnerPath)
	if err != nil {
		return nil, err
	}
	artifacts := filepath.Join(o.opts.Root, StateDir, "artifacts", o.envID)
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFAGT001, "detail", err.Error())
	}

	self, err := os.Executable()
	if err != nil {
		// Without the engine's own path the runner cannot read the inbox, so
		// a workflow waiting on a message is blocked rather than failed. That
		// is the right answer and it is said out loud rather than guessed at.
		self = ""
	}

	job := jobDocument{
		BaseURL: status.URL, Artifacts: artifacts,
		Workflows: workflows, Personas: o.personaDocs(),
		AF: self, WorkDir: o.opts.Root,
		Attempts: opts.Attempts, Headless: !opts.Headed,
	}
	body, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}

	started := o.opts.Clock.Now()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "node", "--experimental-strip-types", runner)
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = o.opts.Root

	runErr := cmd.Run()
	if stdout.Len() == 0 {
		// The runner produced nothing, which is the runner's own failure and
		// not the application's. Its output is the only thing that explains it.
		return nil, aferrors.Coded(aferrors.AFAGT003,
			"detail", strings.TrimSpace(o.opts.Redactor.String(stderr.String())))
	}

	var report TestReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFAGT003,
			"detail", "the runner's output could not be read: "+err.Error())
	}
	report.Duration = o.opts.Clock.Since(started)

	// A non zero exit with a readable report is a test failure, which the
	// caller decides about. Only an unreadable one is an error here.
	_ = runErr
	return &report, nil
}

// findRunner locates the runner's entry point.
//
// Looked for beside the repository first, so a checkout works with no
// installation, then on PATH, so an installed engine finds an installed
// runner. A clear refusal beats a mysterious exec failure.
func (o *Orchestrator) findRunner(override string) (string, error) {
	candidates := []string{override}
	if override == "" {
		candidates = []string{
			filepath.Join(o.opts.Root, "runner", "src", "main.ts"),
			filepath.Join(o.opts.Root, "..", "runner", "src", "main.ts"),
		}
		if home, err := os.UserHomeDir(); err == nil {
			candidates = append(candidates,
				filepath.Join(home, ".antifailure", "runner", "src", "main.ts"))
		}
		if self, err := os.Executable(); err == nil {
			// Beside the binary, for an installed release that ships the
			// runner next to it rather than fetching it separately.
			candidates = append(candidates,
				filepath.Join(filepath.Dir(self), "runner", "src", "main.ts"))
		}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", aferrors.Coded(aferrors.AFAGT004,
		"detail", "looked in "+strings.Join(candidates, ", "))
}

func (o *Orchestrator) workflowDocs(only []string) []workflowDoc {
	wanted := map[string]bool{}
	for _, n := range only {
		wanted[n] = true
	}
	var out []workflowDoc
	for _, w := range o.opts.Manifest.Workflows {
		if len(wanted) > 0 && !wanted[w.Name] {
			continue
		}
		out = append(out, workflowDoc{
			Name: w.Name, Description: w.Description, Persona: w.Persona,
			Expect: w.Expect, StartPath: w.StartPath,
		})
	}
	return out
}

func (o *Orchestrator) personaDocs() []personaDoc {
	var out []personaDoc
	for _, p := range o.opts.Manifest.Personas {
		login := string(p.Login)
		if login == "" {
			login = string(schema.LoginPassword)
		}
		doc := personaDoc{Name: p.Name, Email: p.Email, Role: p.Role, Login: login}
		if login == string(schema.LoginPassword) {
			// A password nobody set, derived from the environment so both the
			// seed and the runner arrive at the same one without anybody
			// writing it down twice.
			doc.Password = personaPassword(o.envID, p.Name)
		}
		out = append(out, doc)
	}
	return out
}

// personaPassword derives a persona's password from the environment.
func personaPassword(envID, persona string) string {
	sum := sha256.Sum256([]byte("antifailure/persona/v1\x00" + envID + "\x00" + persona))
	return "Af-" + hex.EncodeToString(sum[:8]) + "!1"
}

// LoadOptions configure a load run.
type LoadOptions struct {
	Duration time.Duration
	Scale    float64
	Seed     int64
	Progress func(load.Progress)
}

// Load sends traffic shaped like production's at the environment.
//
// The shape comes from the manifest's source when one is configured, and from
// a default otherwise. The default says so in the report, because a shape that
// is a guess and a shape that is production's should never be mistaken for
// each other.
func (o *Orchestrator) Load(ctx context.Context, opts LoadOptions) (*load.Result, []load.Route, error) {
	status, err := o.Status(ctx)
	if err != nil {
		return nil, nil, err
	}
	if status.URL == "" {
		return nil, nil, aferrors.Coded(aferrors.AFLOD010,
			"detail", "nothing is running for this branch; bring it up with 'af up' first")
	}

	shape, err := o.trafficShape()
	if err != nil {
		return nil, nil, err
	}
	cfg := o.opts.Manifest.Load
	var safe, unsafe []string
	if cfg != nil {
		safe, unsafe = cfg.SafeRoutes, cfg.UnsafeRoutes
	}
	if len(safe) == 0 {
		// Reads under the root, which is what a smoke test wants and is the
		// only thing that can be assumed safe without being told.
		safe = []string{"GET /**"}
	}
	sendable, refused := shape.Safe(safe, unsafe)
	if len(sendable.Routes) == 0 {
		return nil, refused, aferrors.Coded(aferrors.AFLOD010,
			"detail", "every route in the shape is unsafe to send; add safe_routes to the manifest")
	}

	duration := opts.Duration
	if duration <= 0 && cfg != nil && cfg.Duration != "" {
		if d, parseErr := time.ParseDuration(cfg.Duration); parseErr == nil {
			duration = d
		}
	}
	scale := opts.Scale
	if scale <= 0 && cfg != nil && cfg.Scale > 0 {
		scale = cfg.Scale
	}

	res, err := load.Run(ctx, load.Options{
		BaseURL: status.URL, Shape: sendable, Scale: scale, Duration: duration,
		Seed: opts.Seed, Clock: o.opts.Clock, Progress: opts.Progress,
	})
	return res, refused, err
}

// Thresholds returns the limits a load run is judged against.
func (o *Orchestrator) Thresholds() (p95Increase, errorRate float64) {
	if o.opts.Manifest.Load == nil || o.opts.Manifest.Load.Thresholds == nil {
		return 0, 0
	}
	t := o.opts.Manifest.Load.Thresholds
	return t.P95Increase, t.ErrorRate
}

// trafficShape reads the mix from whatever source is configured.
func (o *Orchestrator) trafficShape() (load.Shape, error) {
	cfg := o.opts.Manifest.Load
	if cfg == nil || cfg.Source == "" || cfg.Source == schema.LoadNone {
		return load.DefaultShape(), nil
	}
	switch cfg.Source {
	case schema.LoadAccessLog:
		path := cfg.SourceConfig["path"]
		if path == "" {
			return load.Shape{}, aferrors.Coded(aferrors.AFLOD010,
				"detail", "the load source is access_log and no path is configured")
		}
		body, err := os.ReadFile(filepath.Join(o.opts.Root, filepath.FromSlash(path)))
		if err != nil {
			return load.Shape{}, aferrors.Wrap(err, aferrors.AFLOD010, "detail", err.Error())
		}
		shape := load.FromAccessLog(strings.Split(string(body), "\n"))
		if len(shape.Routes) == 0 {
			return load.Shape{}, aferrors.Coded(aferrors.AFLOD010,
				"detail", "no requests could be read from "+path)
		}
		if shape.RequestsPerSecond == 0 {
			shape.RequestsPerSecond = 10
		}
		return shape, nil
	default:
		// A source that needs an account nobody has connected. Saying so beats
		// silently sending the default and calling it production's shape.
		return load.Shape{}, aferrors.Coded(aferrors.AFLOD010,
			"detail", string(cfg.Source)+" is not connected in this build; use access_log or none")
	}
}
