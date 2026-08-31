package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/hud"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// UpJSON is the machine readable form of af up.
type UpJSON struct {
	EnvID    string        `json:"env_id"`
	URL      string        `json:"url,omitempty"`
	Golden   string        `json:"golden,omitempty"`
	Proxied  bool          `json:"proxied"`
	Built    int           `json:"built"`
	Cached   int           `json:"cached"`
	Duration string        `json:"duration"`
	Services []ServiceJSON `json:"services"`
}

// ServiceJSON is one service in the JSON forms of up and status.
type ServiceJSON struct {
	Name   string `json:"name"`
	Kind   string `json:"kind,omitempty"`
	URL    string `json:"url,omitempty"`
	Ready  bool   `json:"ready"`
	State  string `json:"state,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// StatusJSON is the machine readable form of af status.
type StatusJSON struct {
	EnvID    string        `json:"env_id"`
	Running  bool          `json:"running"`
	URL      string        `json:"url,omitempty"`
	Proxied  bool          `json:"proxied"`
	Services []ServiceJSON `json:"services"`
}

// DownJSON is the machine readable form of af down.
type DownJSON struct {
	EnvID   string        `json:"env_id"`
	Removed int           `json:"removed"`
	Pending []PendingJSON `json:"pending"`
}

// PendingJSON is one resource teardown could not remove.
type PendingJSON struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// lifecycleOptions are the choices a command makes before the orchestrator
// exists. A struct rather than three positional booleans, because the next
// person to add one should not have to read every call site to find out which
// bool is which.
type lifecycleOptions struct {
	branch  string
	rebuild bool
	// silent suppresses the prose progress lines. The dashboard owns the
	// screen when it is running, and a Printf into a Bubble Tea frame corrupts
	// it, so the same information travels as events instead.
	silent bool
}

// orchestrator loads the manifest and prepares the lifecycle for this repo.
func orchestrator(env2 *Env, branch string, rebuild bool) (*env.Orchestrator, error) {
	o, _, err := orchestratorWithManifest2(env2, lifecycleOptions{branch: branch, rebuild: rebuild})
	return o, err
}

// orchestratorWithManifest3 builds an orchestrator from the full set of
// lifecycle choices, for the one command that makes more than two of them.
func orchestratorWithManifest3(env2 *Env, opts lifecycleOptions) (*env.Orchestrator, error) {
	o, _, err := orchestratorWithManifest2(env2, opts)
	return o, err
}

// orchestratorWithManifest also returns the manifest, for commands that read
// it directly rather than through the lifecycle.
func orchestratorWithManifest(env2 *Env, branch string) (*env.Orchestrator, *schema.Manifest, error) {
	return orchestratorWithManifest2(env2, lifecycleOptions{branch: branch})
}

// progressFor returns the status line a run reports through, remembering it on
// the Env so that the command can close it when the run ends.
//
// On the Env rather than returned alongside the orchestrator, because every
// one of the eleven commands that builds an orchestrator would otherwise have
// to thread a second value through, and the one that forgot would leave a
// goroutine rewriting a line under whatever the command printed next.
func progressFor(e *Env) *Progress {
	if e.Progress == nil {
		e.Progress = NewProgress(e.Out, e.Clock)
	}
	return e.Progress
}

func orchestratorWithManifest2(env2 *Env, opts lifecycleOptions) (*env.Orchestrator, *schema.Manifest, error) {
	branch, rebuild := opts.branch, opts.rebuild
	path, err := manifest.Find(env2.WorkDir)
	if err != nil {
		return nil, nil, err
	}
	m, err := manifest.Load(path)
	if err != nil {
		return nil, nil, err
	}
	root := repoRoot(path)
	if branch == "" {
		branch = currentBranch(root)
	}

	r := redact.New()
	o, err := env.New(env.Options{
		Root: root, Manifest: m, Branch: branch, Clock: env2.Clock,
		Rebuild: rebuild, Redactor: r, Verbose: env2.Out.Verbose, Getenv: env2.Getenv,
		Progress: func(line string) {
			// Progress is prose, not data, so it is suppressed in JSON mode
			// rather than interleaved into a document a script is parsing,
			// and in dashboard mode, where it would be written over a frame.
			if opts.silent {
				return
			}
			progressFor(env2).Step(r.String(line))
		},
	})
	if err != nil {
		return nil, nil, err
	}
	return o, m, err
}

// repoRoot is the directory holding the manifest.
func repoRoot(manifestPath string) string {
	if i := strings.LastIndexAny(manifestPath, "/\\"); i > 0 {
		return manifestPath[:i]
	}
	return "."
}

// currentBranch asks git what branch is checked out.
//
// A repository is the normal case and not a requirement: somebody trying the
// tool in a directory that is not one gets an environment called default
// rather than an error about git.
func currentBranch(root string) string {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "default"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "default"
	}
	return branch
}

// dashboard is the live view attached to a run, or nothing.
//
// One of program and plain is set when it is in use, never both: a terminal
// gets the full frame, and anything else gets the line per event fallback,
// because drawing a Bubble Tea frame into a CI log produces a file of cursor
// escapes and no information.
type dashboard struct {
	program *hud.Program
	plain   *hud.Plain
}

// attachDashboard wires the HUD to an orchestrator and reports what it built.
//
// The choice is made from the output stream rather than from a flag, because
// the person who typed --hud in a pipeline wants the events, and refusing them
// a display for want of a terminal would be a worse answer than the fallback
// that was written for exactly this.
func attachDashboard(e *Env, o *env.Orchestrator) *dashboard {
	if width, height, ok := terminalSize(e.Out.Out); ok {
		p := hud.NewProgram(hud.New(o.EnvID(), width, height), e.Stdin, e.Out.Out)
		o.AddSink(hud.Sink(p))
		return &dashboard{program: p}
	}
	pl := hud.NewPlain(e.Out.Out, o.EnvID())
	o.AddSink(hud.PlainSink(pl))
	return &dashboard{plain: pl}
}

// run performs the lifecycle with the display attached, and returns once both
// the work and the display have finished.
func (d *dashboard) run(ctx context.Context, work func() error) error {
	if d == nil || d.program == nil {
		return work()
	}
	done := make(chan error, 1)
	go func() {
		err := work()
		// Unconditional, and safe because Close is idempotent. The bus closes
		// the sink at the end of a successful run, but a failure before the
		// bus exists closes nothing, and without this the dashboard would
		// draw an empty frame forever on exactly the runs that failed worst.
		d.program.Close()
		done <- err
	}()
	// Bubble Tea holds the terminal, so it runs here rather than in the
	// goroutine: it restores the screen on the way out, and a program that
	// exits while its restore is still queued leaves the terminal in the
	// alternate screen with no cursor.
	runErr := d.program.Run(ctx)
	workErr := <-done
	if workErr != nil {
		return workErr
	}
	return runErr
}

// terminalSize reports the size of a writer that is a terminal.
//
// The boolean is the whole point: a writer that is a file, a pipe, or a test
// buffer has no size, and guessing one produces a frame laid out for a screen
// that does not exist.
func terminalSize(w io.Writer) (int, int, bool) {
	f, ok := w.(*os.File)
	if !ok {
		return 0, 0, false
	}
	width, height, err := term.GetSize(int(f.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func newUpCommand(e *Env) *cobra.Command {
	var branch string
	var rebuild bool
	var live bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Create an environment for the current branch",
		Long: strings.TrimSpace(`
Build every service, branch the database from its masked golden, seal the
network, and bring the environment up.

The environment is created under a lock for this branch, so two invocations
cannot fight over it, and every resource is journaled before it is made, so an
interrupt at any point leaves something af down can clean up.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if live && e.Out.Format == FormatJSON {
				return fmt.Errorf(
					"up: --hud draws a dashboard and --format json writes a document, " +
						"and one stream cannot be both; drop one of them")
			}
			o, err := orchestratorWithManifest3(e, lifecycleOptions{
				branch: branch, rebuild: rebuild, silent: live,
			})
			if err != nil {
				return err
			}

			var view *dashboard
			if live {
				view = attachDashboard(e, o)
			} else {
				e.Out.Subject("Bringing up "+o.EnvID(), "branch "+o.Branch())
			}

			var res *env.Result
			var upErr error
			runErr := view.run(cmd.Context(), func() error {
				res, upErr = o.Up(cmd.Context())
				// The lifecycle error is carried out in upErr rather than
				// returned, because it is the command's answer and not a
				// reason to treat the display as broken.
				return nil
			})
			if runErr != nil {
				return runErr
			}
			if upErr != nil {
				renderServices(e, res.Services)
				return upErr
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(UpJSON{
					EnvID: res.EnvID, URL: res.URL, Golden: res.Golden, Proxied: res.Proxied,
					Built: res.Built, Cached: res.Cached,
					Duration: res.Duration.Round(1e9).String(),
					Services: servicesJSON(res.Services),
				})
			}

			e.Out.Println("")
			renderServices(e, res.Services)
			e.Out.Println("")
			if res.URL != "" {
				e.Out.Printf("  %s  %s\n", e.Out.S(StyleBold, "Open"), e.Out.S(StyleAccent, res.URL))
			}
			if res.Proxied {
				e.Out.Printf("  %s\n", e.Out.Wrap(
					"Every outbound request goes through the egress proxy, and anything the policy "+
						"does not allow is refused with a decision you can read. Ask about one with "+
						"'af net explain'.", 2))
			}
			e.Out.Printf("  Tear it down with %s\n", e.Out.S(StyleBold, "af down"))
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to create the environment for, defaulting to the checked out one")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "Build every image again, even when an identical one exists")
	cmd.Flags().BoolVar(&live, "hud", false,
		"Watch the run on a live dashboard, or a line per event where there is no terminal")
	return cmd
}

func servicesJSON(services []provider.RunningService) []ServiceJSON {
	out := make([]ServiceJSON, 0, len(services))
	for _, s := range services {
		out = append(out, ServiceJSON{
			Name: s.Name, Kind: s.Kind, URL: s.URL,
			Ready: s.Ready, State: s.State, Detail: s.Detail,
		})
	}
	return out
}

func renderServices(e *Env, services []provider.RunningService) {
	if e.Out.Format == FormatJSON || len(services) == 0 {
		return
	}
	for _, s := range services {
		symbol := SymbolFail
		if s.Ready {
			symbol = SymbolOK
		}
		detail := s.URL
		if detail == "" {
			detail = s.State
		}
		e.Out.Status(symbol, s.Name, detail)
		if !s.Ready && s.Detail != "" {
			for _, line := range lastLines(s.Detail, 12) {
				e.Out.Printf("      %s\n", e.Out.S(StyleDim, line))
			}
		}
	}
}

// lastLines returns the tail of a block of output, which is where the reason
// for a failure is.
func lastLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func newDownCommand(e *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Remove the environment and everything it created",
		Long: strings.TrimSpace(`
Replay the journal in reverse and delete every resource the environment
created: database branches, containers, volumes, and networks.

Teardown never stops at the first failure. A provider that is unreachable must
not strand the other resources, so each is attempted and anything that could
not be removed stays recorded for the next run. Exit code 10 means resources
are still pending.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			e.Out.Subject("Tearing down "+o.EnvID(), "branch "+o.Branch())
			td, downErr := o.Down(cmd.Context())
			if downErr != nil {
				return downErr
			}

			if e.Out.Format == FormatJSON {
				pending := make([]PendingJSON, 0, len(td.Pending))
				for _, p := range td.Pending {
					pending = append(pending, PendingJSON{Kind: p.Kind, ID: p.ID, Reason: p.Reason})
				}
				if err := e.Out.JSON(DownJSON{
					EnvID: td.EnvID, Removed: td.Removed, Pending: pending,
				}); err != nil {
					return err
				}
			} else {
				e.Out.Println("")
				e.Out.Printf("  %d resources removed.\n", td.Removed)
				for _, p := range td.Pending {
					e.Out.Printf("  %s %s/%s: %s\n",
						e.Out.S(StyleBad, SymbolFail), p.Kind, p.ID, p.Reason)
				}
			}
			if len(td.Pending) > 0 {
				return aferrors.Coded(aferrors.AFRUN030, "count", fmt.Sprint(len(td.Pending)))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to tear down, defaulting to the checked out one")
	return cmd
}

func newStatusCommand(e *Env) *cobra.Command {
	var branch string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show what is running for this branch",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o, err := orchestrator(e, branch, false)
			if err != nil {
				return err
			}
			res, err := o.Status(cmd.Context())
			if err != nil {
				return err
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(StatusJSON{
					EnvID: res.EnvID, Running: len(res.Services) > 0, URL: res.URL,
					Proxied: res.Proxied, Services: servicesJSON(res.Services),
				})
			}
			// The branch, not only the environment identifier. The identifier
			// is the branch with the punctuation taken out and a hash on the
			// end, which nobody can check against the branch they think they
			// are on, and running on the wrong branch is the most common way
			// to be confused by this tool. The directory is deliberately not
			// here: it is in the reader's own shell prompt, and an absolute
			// path is a single unbreakable word that no wrapping can rescue.
			e.Out.Subject(res.EnvID, "branch "+o.Branch())
			if len(res.Services) == 0 {
				e.Out.Empty("Nothing is running for this branch.", "Bring it up with", "af up")
				return nil
			}
			renderServices(e, res.Services)
			if res.URL != "" {
				e.Out.Printf("\n  %s  %s\n", e.Out.S(StyleBold, "Open"), e.Out.S(StyleAccent, res.URL))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&branch, "branch", "", "Branch to report on, defaulting to the checked out one")
	return cmd
}
