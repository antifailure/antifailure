package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
)

// af env answers the question somebody asks a week later: what is still
// running, and why.
//
// It reads the daemon rather than a registry, because the daemon is the thing
// that actually has them. A registry can be wrong; a container either exists
// or it does not, and a list that disagrees with reality is worse than no list.

// EnvJSON is one environment.
type EnvJSON struct {
	EnvID     string  `json:"env_id"`
	Kind      string  `json:"kind"`
	Name      string  `json:"name"`
	Service   string  `json:"service,omitempty"`
	State     string  `json:"state,omitempty"`
	CreatedAt string  `json:"created_at"`
	AgeHours  float64 `json:"age_hours"`
}

func newEnvCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "See and clean up the environments on this machine",
		Long: strings.TrimSpace(`
Reads the daemon rather than a registry, because the daemon is the thing that
actually has them. A registry can be wrong; a container either exists or it
does not, and a list that disagrees with reality is worse than no list.`),
	}
	cmd.AddCommand(newEnvListCommand(e))
	cmd.AddCommand(newEnvPruneCommand(e))
	cmd.AddCommand(&cobra.Command{
		Use:   "pull",
		Short: "Pull an environment's configuration from the control plane",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return notYetAvailable("af env pull")
		},
	})
	return cmd
}

// environments groups what the daemon holds by environment.
type environment struct {
	ID        string
	Resources int
	Services  []string
	Oldest    time.Time
	Running   int
}

func listEnvironments(cmd *cobra.Command, e *Env) ([]environment, error) {
	rt, err := local.New(local.Options{Clock: e.Clock})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close() }()

	items, err := rt.Inventory(cmd.Context())
	if err != nil {
		return nil, err
	}
	byEnv := map[string]*environment{}
	for _, item := range items {
		id := item.EnvID
		if id == "" {
			// A resource with no environment belongs to the machine rather
			// than to any one run: the sidecar image, mostly. Counting it
			// under an environment would make teardown look incomplete.
			continue
		}
		env, ok := byEnv[id]
		if !ok {
			env = &environment{ID: id, Oldest: item.CreatedAt}
			byEnv[id] = env
		}
		env.Resources++
		if item.CreatedAt.Before(env.Oldest) {
			env.Oldest = item.CreatedAt
		}
		if name := item.Labels["service"]; name != "" && !contains(env.Services, name) {
			env.Services = append(env.Services, name)
		}
		if item.Labels["state"] == "running" {
			env.Running++
		}
	}

	out := make([]environment, 0, len(byEnv))
	for _, env := range byEnv {
		sort.Strings(env.Services)
		out = append(out, *env)
	}
	// Oldest first, because the one worth removing is the one that has been
	// there longest and the one somebody forgot.
	sort.Slice(out, func(i, j int) bool { return out[i].Oldest.Before(out[j].Oldest) })
	return out, nil
}

func newEnvListCommand(e *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the environments this machine is holding",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			envs, err := listEnvironments(cmd, e)
			if err != nil {
				return err
			}
			if e.Out.Format == FormatJSON {
				docs := make([]EnvJSON, 0, len(envs))
				for _, env := range envs {
					docs = append(docs, EnvJSON{
						EnvID: env.ID, Kind: "environment",
						Name:      strings.Join(env.Services, ", "),
						State:     fmt.Sprintf("%d of %d running", env.Running, env.Resources),
						CreatedAt: env.Oldest.UTC().Format(time.RFC3339),
						AgeHours:  e.Clock.Since(env.Oldest).Hours(),
					})
				}
				return e.Out.JSON(docs)
			}
			if len(envs) == 0 {
				e.Out.Println("Nothing is running. Bring one up with 'af up'.")
				return nil
			}
			rows := make([][]string, 0, len(envs))
			for _, env := range envs {
				age := e.Clock.Since(env.Oldest)
				rows = append(rows, []string{
					env.ID, fmt.Sprint(env.Resources), fmt.Sprint(env.Running),
					humanAge(age), strings.Join(env.Services, ", "),
				})
			}
			e.Out.Table([]string{"ENVIRONMENT", "RESOURCES", "RUNNING", "AGE", "SERVICES"}, rows)
			e.Out.Println("")
			e.Out.Println("  Remove one with: af down --branch <branch>")
			e.Out.Println("  Remove everything older than a day with: af env prune")
			return nil
		},
	}
}

func newEnvPruneCommand(e *Env) *cobra.Command {
	var olderThan time.Duration
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove environments older than a cutoff",
		Long: strings.TrimSpace(`
An environment nobody tore down holds a database branch, a network, and a
container per service, and the machine that accumulates a dozen of them is a
machine somebody reboots to fix.

It refuses to remove anything without a cutoff, and prints what it would do
before doing it, because removing somebody's environment while they are looking
at it is the kind of help nobody wants.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			envs, err := listEnvironments(cmd, e)
			if err != nil {
				return err
			}
			var stale []environment
			for _, env := range envs {
				if e.Clock.Since(env.Oldest) > olderThan {
					stale = append(stale, env)
				}
			}
			if len(stale) == 0 {
				e.Out.Printf("Nothing is older than %s.\n", humanAge(olderThan))
				return nil
			}

			rt, err := local.New(local.Options{Clock: e.Clock})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			removed, pending := 0, 0
			for _, env := range stale {
				if dryRun {
					e.Out.Printf("  would remove %s (%s old, %d resources)\n",
						env.ID, humanAge(e.Clock.Since(env.Oldest)), env.Resources)
					continue
				}
				td, downErr := rt.Down(cmd.Context(), env.ID)
				removed += td.Removed
				pending += len(td.Pending)
				if downErr != nil {
					e.Out.Printf("  %s %s: %v\n", e.Out.S(StyleWarn, SymbolWarn), env.ID, downErr)
					continue
				}
				e.Out.Printf("  removed %s (%d resources)\n", env.ID, td.Removed)
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(map[string]any{
					"environments": len(stale), "resources_removed": removed,
					"pending": pending, "dry_run": dryRun,
				})
			}
			if dryRun {
				e.Out.Printf("\n  %d environments would be removed. Run without --dry-run to do it.\n",
					len(stale))
				return nil
			}
			e.Out.Printf("\n  %d environments removed, %d resources.\n", len(stale), removed)
			if pending > 0 {
				return aferrors.Coded(aferrors.AFRUN030, "count", fmt.Sprint(pending))
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&olderThan, "older-than", 24*time.Hour, "Only remove environments older than this")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be removed without removing it")
	return cmd
}

// humanAge reads the way somebody would say it.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func contains(items []string, want string) bool {
	for _, s := range items {
		if s == want {
			return true
		}
	}
	return false
}
