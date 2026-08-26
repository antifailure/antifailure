package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/provider"
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

// orchestrator loads the manifest and prepares the lifecycle for this repo.
func orchestrator(env2 *Env, branch string, rebuild bool) (*env.Orchestrator, error) {
	path, err := manifest.Find(env2.WorkDir)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Load(path)
	if err != nil {
		return nil, err
	}
	root := repoRoot(path)
	if branch == "" {
		branch = currentBranch(root)
	}

	r := redact.New()
	return env.New(env.Options{
		Root: root, Manifest: m, Branch: branch, Clock: env2.Clock,
		Rebuild: rebuild, Redactor: r, Verbose: env2.Out.Verbose, Getenv: env2.Getenv,
		Progress: func(line string) {
			// Progress is prose, not data, so it is suppressed in JSON mode
			// rather than interleaved into a document a script is parsing.
			env2.Out.Printf("  %s\n", r.String(line))
		},
	})
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

func newUpCommand(e *Env) *cobra.Command {
	var branch string
	var rebuild bool
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
			o, err := orchestrator(e, branch, rebuild)
			if err != nil {
				return err
			}
			e.Out.Section("Bringing up " + o.EnvID())
			res, upErr := o.Up(cmd.Context())
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
		symbol, style := SymbolFail, StyleBad
		if s.Ready {
			symbol, style = SymbolOK, StyleGood
		}
		detail := s.URL
		if detail == "" {
			detail = s.State
		}
		e.Out.Status(e.Out.S(style, symbol), s.Name, detail)
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
			e.Out.Section("Tearing down " + o.EnvID())
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
			e.Out.Section(res.EnvID)
			if len(res.Services) == 0 {
				e.Out.Println("Nothing is running for this branch. Bring it up with 'af up'.")
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
