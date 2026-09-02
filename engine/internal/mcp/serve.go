package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/state"
)

// Config is what the command hands the server.
type Config struct {
	// WorkDir is where to look for the manifest.
	WorkDir string
	// In and Out are the protocol streams.
	//
	// Out carries protocol frames and nothing else. Nothing in this package
	// writes anything else to it, and the command that fills this in must not
	// pass the same stream as Log.
	In  io.Reader
	Out io.Writer
	// Log is where every diagnostic goes, which in practice is standard
	// error.
	Log     io.Writer
	Clock   clock.Clock
	Getenv  func(string) string
	Version string
}

// Serve binds a project, restores its runs and serves until the input ends.
//
// The order matters. The project is bound first, so that a checkout with no
// manifest fails at startup rather than accepting calls it will refuse one by
// one. Interrupted runs are settled next, before a single frame is read, so
// that a caller polling a run from a previous process is told the truth on its
// first request instead of after a timeout.
func Serve(ctx context.Context, cfg Config) error {
	if cfg.Clock == nil {
		cfg.Clock = clock.New()
	}
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	SetBuildVersion(cfg.Version)

	project, err := BindProject(cfg.WorkDir)
	if err != nil {
		return err
	}

	stateDir := filepath.Join(project.Root, state.DirName)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", stateDir, err)
	}
	db, err := state.Open(ctx, stateDir)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	store := NewStore(db, cfg.Clock)
	settled, err := store.RecoverInterrupted(ctx)
	if err != nil {
		// Not fatal. A store that could not be tidied still answers every
		// question correctly except the status of runs a dead process left,
		// and refusing to start would be a worse answer than that.
		_, _ = fmt.Fprintf(cfg.Log, "af mcp: settling interrupted runs: %v\n", err)
	} else if settled > 0 {
		_, _ = fmt.Fprintf(cfg.Log,
			"af mcp: settled %d run(s) left in flight by an earlier process, "+
				"each reported INCONCLUSIVE\n", settled)
	}

	// The experiments run under a context that outlives any one call, so a
	// submitted rehearsal is not cancelled by the tool call returning.
	runCtx, stopRuns := context.WithCancel(context.WithoutCancel(ctx))
	defer stopRuns()

	engine := NewEngine(runCtx, project, store, cfg.Log)
	orch := &orchestratorFactory{project: project, cfg: cfg}

	server := NewServer(project.ID, store, cfg.Log)
	server.Register(newGetRunTool(project, store))
	server.Register(newCancelRunTool(project, store))
	server.Register(newInspectEgressTool(project, orch.observe))
	server.Register(newRehearseMigrationTool(project, engine, orch.rehearse))

	_, _ = fmt.Fprintf(cfg.Log, "af mcp: serving project %q from %s\n", project.ID, project.Root)

	serveErr := server.Serve(ctx, cfg.In, cfg.Out)

	// The client has gone. Experiments already started are given the chance to
	// finish and tear their environments down, because an environment
	// abandoned mid run is the leak this product exists to prevent, and a
	// process that exits out from under its own work is how one is abandoned.
	_, _ = fmt.Fprintln(cfg.Log, "af mcp: the client disconnected, waiting for running experiments")
	engine.Wait()
	return serveErr
}

// orchestratorFactory builds an orchestrator per call.
//
// Per call rather than once, because an orchestrator holds no connections
// until something asks it to and because the branch can change under a long
// lived server when somebody checks out something else.
type orchestratorFactory struct {
	project *Project
	cfg     Config
}

// build constructs the orchestrator for the bound project.
//
// Every field here is decided by this server, not by a caller. There is no
// argument in any tool schema that reaches this function, which is what makes
// it impossible for a call to point an experiment at a different repository,
// a different database or a weaker policy.
func (f *orchestratorFactory) build() (*env.Orchestrator, error) {
	r := redact.New()
	return env.New(env.Options{
		Root:       f.project.Root,
		Manifest:   f.project.Manifest,
		Branch:     currentBranch(f.project.Root),
		Repository: strings.TrimSpace(f.cfg.Getenv("GITHUB_REPOSITORY")),
		Clock:      f.cfg.Clock,
		Getenv:     f.cfg.Getenv,
		Redactor:   r,
		Version:    f.cfg.Version,
		Progress: func(line string) {
			// To the log, never to the protocol stream. This is the single
			// most dangerous line in the package: the engine emits progress
			// prose freely, and one line of it on standard output would
			// corrupt the session in a way that surfaces as an unrelated
			// parse error in the client.
			_, _ = fmt.Fprintf(f.cfg.Log, "af mcp: %s\n", r.String(line))
		},
	})
}

// observe reads the decision log, saying whether it could be read at all.
func (f *orchestratorFactory) observe(
	ctx context.Context, limit int,
) ([]local.Decision, bool, error) {
	o, err := f.build()
	if err != nil {
		return nil, false, err
	}
	decisions, err := o.Decisions(ctx, limit)
	if err != nil {
		// Unavailable, not empty. The difference is the whole point: an empty
		// log means nothing was reached, and an unreadable one means nobody
		// knows, and reporting the second as the first would be a monitoring
		// failure presented as a clean bill of health.
		return nil, false, err
	}
	if decisions == nil {
		// The runtime returns nil with no error when no environment is
		// running, which is a real state and not a failure. It is still not
		// an observation, so it is reported as unavailable with no error.
		return nil, false, nil
	}
	return decisions, true, nil
}

// rehearse runs the migration rehearsal through the orchestrator.
//
// SkipRehearsal is deliberately not set and there is no argument that could
// set it. Its own comment in engine/internal/env says it exists for the case
// where a second branch cannot be made and is not a way to disable the check,
// so exposing it here would be exactly the safety control a model must not be
// able to reach.
func (f *orchestratorFactory) rehearse(ctx context.Context, _ string) (insights.Full, error) {
	o, err := f.build()
	if err != nil {
		return insights.Full{}, err
	}
	return o.RunInsights(ctx, env.InsightsOptions{Limit: 20})
}

// currentBranch asks git what is checked out.
//
// A fixed executable and an argument array, never a shell. The only variable
// is the repository root, which this server was started against and which no
// tool argument can influence, so there is nothing here a caller can steer.
func currentBranch(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "default"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "default"
	}
	return branch
}
