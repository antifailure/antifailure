package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/controlplane"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
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
	cmd.AddCommand(newEnvReapCommand(e))
	cmd.AddCommand(newEnvExtendCommand(e))
	cmd.AddCommand(newEnvPullCommand(e))
	return cmd
}

// af env pull answers "what does the control plane think is running", which is
// a different question from "what is running on this machine" and worth being
// able to ask separately. When they disagree, that disagreement is the finding.
func newEnvPullCommand(e *Env) *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:   "pull <environment>",
		Short: "Read an environment's record from the control plane",
		Long: strings.TrimSpace(`
Reads what the control plane holds for one environment: its branch, its state,
its preview URL, and the golden version it was built from.

This never changes anything locally. The control plane is a record of what
happened, not a source of configuration: what an environment does comes from
the manifest in the repository, on the machine the environment is on. A control
plane that could change what an environment runs would be a control plane that
could change what it masks.

Needs a credential. Run af login, or set AF_CONTROL_PLANE_TOKEN to an engine
token, which is what a build machine with nobody sitting at it uses.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := controlPlaneClient(e, baseURL)
			if err != nil {
				return err
			}

			env, err := client.Pull(cmd.Context(), args[0])
			if err != nil {
				var missing *controlplane.NotFound
				if errors.As(err, &missing) {
					return aferrors.Coded(aferrors.AFCPL002, "env", args[0])
				}
				return err
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(env)
			}
			e.Out.Printf("  %s\n", env.EnvID)
			e.Out.Printf("  repository     %s\n", env.Repository)
			e.Out.Printf("  branch         %s\n", env.Branch)
			if env.PullRequest != nil {
				e.Out.Printf("  pull request   #%d\n", *env.PullRequest)
			}
			e.Out.Printf("  state          %s\n", env.State)
			if env.PreviewURL != "" {
				e.Out.Printf("  preview        %s\n", env.PreviewURL)
			}
			if env.Runtime != "" {
				e.Out.Printf("  runtime        %s\n", env.Runtime)
			}
			if env.GoldenVersion != "" {
				e.Out.Printf("  golden         %s\n", env.GoldenVersion)
			}
			e.Out.Printf("  created        %s\n", env.CreatedAt.UTC().Format(time.RFC3339))
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "",
		"The control plane to read from (default: AF_CONTROL_PLANE_URL, or the hosted instance)")
	return cmd
}

// controlPlaneClient builds a client, or explains what is missing.
//
// The environment first, then the credential `af login` stored. In that order
// on purpose: a token exported into a shell is somebody deliberately overriding
// what is on the machine, usually because they are debugging or because they
// are in CI, and the explicit thing must win.
//
// The stored credential is the OS keyring where there is one, and a file with
// mode 0600 where there is not. NEITHER IS A CONFIGURATION FILE IN THE
// REPOSITORY, which is what the older version of this comment was guarding
// against and is still the rule: nothing here reads or writes a token inside
// the working tree, so there is nothing for a commit or a support bundle to
// pick up.
func controlPlaneClient(e *Env, baseURL string) (*controlplane.Client, error) {
	// Resolved through the same function af login and af token resolve it
	// through, flag then environment then the hosted instance, so that the
	// origin the credential is looked up under is the origin it was stored
	// under. This used to read the environment alone and then consult the
	// store only when that produced something, so somebody who had run af
	// login and set nothing else was told AF-CPL-001, no control plane token
	// is configured, while holding one: the default was filled in afterwards,
	// deeper down, where the lookup could no longer see it.
	baseURL = controlPlaneFor(e, baseURL)
	token := controlplane.TokenFromEnvironment(func(k string) (string, bool) {
		v := e.Getenv(k)
		return v, v != ""
	})
	if token == "" {
		// A credential stored by af login. Expiry is checked inside storedToken
		// so that the failure is "your session expired, run af login" rather
		// than a 401 from a server the user then goes and investigates.
		token = storedToken(e, baseURL)
	}

	client, err := controlplane.New(controlplane.Options{
		BaseURL:  baseURL,
		Token:    token,
		Clock:    e.Clock,
		Redactor: e.Redactor,
	})
	if errors.Is(err, controlplane.ErrNotConfigured) {
		return nil, aferrors.Coded(aferrors.AFCPL001)
	}
	return client, err
}

// environments groups what the daemon holds by environment.
type environment struct {
	ID        string
	Resources int
	Services  []string
	Oldest    time.Time
	Running   int
}

func listEnvironments(ctx context.Context, e *Env) ([]environment, error) {
	rt, err := inventoryRuntime(e)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close() }()

	items, err := rt.Inventory(ctx)
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
			envs, err := listEnvironments(cmd.Context(), e)
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
				e.Out.Empty("Nothing is running on this machine.", "Bring one up with", "af up")
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
			e.Out.Table([]Column{
				Col("ENVIRONMENT"), Num("RESOURCES"), Num("RUNNING"), Num("AGE"), Flex("SERVICES"),
			}, rows)
			e.Out.Println("")
			e.Out.Hint("Remove one with", "af down --branch <branch>")
			e.Out.Hint("Remove everything older than a day with", "af env prune")
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
			envs, err := listEnvironments(cmd.Context(), e)
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

			rt, err := inventoryRuntime(e)
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

// inventoryRuntime builds the runtime that would be holding this machine's
// environments.
//
// af env list and af env prune are the two commands that are about a machine
// rather than about one environment, and they still have to ask the right
// runtime: with runtime.provider set to kubernetes, the environments are
// namespaces on a cluster and there is nothing on the local daemon to find. A
// manifest is what says which, so it is read when there is one.
func inventoryRuntime(e *Env) (provider.Runtime, error) {
	if o, _, err := orchestratorWithManifest(e, ""); err == nil {
		return o.Runtime()
	}
	// Outside a repository there is no manifest to ask, and the only runtime
	// that could be holding anything on this machine is the local one.
	return local.New(local.Options{Clock: e.Clock, Getenv: e.Getenv})
}
