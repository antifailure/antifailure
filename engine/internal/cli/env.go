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
//
// Services was called "name", which is what the text table's column header
// never was: the key held the comma joined service list and the
// environment's identifier was already in env_id. The field was never in the
// documented output, so the stability page's promise did not cover it, and
// nothing in this repository read it; it is renamed rather than aliased so the
// document does not carry a key that contradicts its content for a release.
type EnvJSON struct {
	EnvID     string  `json:"env_id"`
	Kind      string  `json:"kind"`
	Services  string  `json:"services"`
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
	return groupEnvironments(items), nil
}

// groupEnvironments turns the daemon's flat inventory into environments.
//
// Pure, so that af env prune's plan can be driven by a test from a list of
// resources rather than from a daemon holding real ones for a day.
func groupEnvironments(items []provider.Resource) []environment {
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
	return out
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
						Services:  strings.Join(env.Services, ", "),
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
			e.Out.Hint("List what is older than a day, and how to remove it, with", "af env prune")
			return nil
		},
	}
}

// PruneEnvJSON is one environment af env prune named, either as something it
// would remove or as something it removed.
type PruneEnvJSON struct {
	EnvID     string   `json:"env_id"`
	CreatedAt string   `json:"created_at"`
	AgeHours  float64  `json:"age_hours"`
	Resources int      `json:"resources"`
	Running   int      `json:"running"`
	Services  []string `json:"services"`
	// Removed and Pending are filled only for an environment that was
	// removed. Error is why one was not, or empty.
	Removed int    `json:"removed,omitempty"`
	Pending int    `json:"pending,omitempty"`
	Error   string `json:"error,omitempty"`
}

// PruneJSON is the whole of one af env prune.
//
// The plan and the removal are one document with two lists, so that a caller
// cannot mistake one for the other: a bare run fills would_remove and leaves
// removed empty, and a run with --yes does the reverse. dry_run says which.
type PruneJSON struct {
	// OlderThan is the cutoff, as the flag was given or defaulted.
	OlderThan string `json:"older_than"`
	// Scope is always "machine": the daemon does not know which repository
	// made what, so the cutoff applies to every project's environments.
	Scope       string         `json:"scope"`
	DryRun      bool           `json:"dry_run"`
	WouldRemove []PruneEnvJSON `json:"would_remove"`
	Removed     []PruneEnvJSON `json:"removed"`
	// ResourcesRemoved and Pending total across Removed.
	ResourcesRemoved int `json:"resources_removed"`
	Pending          int `json:"pending"`
	// Proceed is the command that removes exactly what would_remove lists,
	// present only when there is something to remove and nothing was.
	Proceed string `json:"proceed,omitempty"`
}

// pruner is what af env prune reads and what it destroys through.
//
// An interface rather than the runtime itself, so that the decision, the plan
// and the stop before --yes can be driven by a test without a daemon. The
// failure that made this worth pinning: a bare `af env prune` in an empty
// directory removed nine environments belonging to other sessions on a shared
// machine, because the only gate was a default cutoff and the help text
// promised a preview that did not exist.
type pruner interface {
	environments(ctx context.Context) ([]environment, error)
	down(ctx context.Context, envID string) (provider.Teardown, error)
	close() error
}

// runtimePruner is the real one, over the runtime that holds this machine's
// environments.
type runtimePruner struct{ rt provider.Runtime }

func (p runtimePruner) environments(ctx context.Context) ([]environment, error) {
	items, err := p.rt.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	return groupEnvironments(items), nil
}

func (p runtimePruner) down(ctx context.Context, envID string) (provider.Teardown, error) {
	return p.rt.Down(ctx, envID)
}

func (p runtimePruner) close() error { return p.rt.Close() }

// pruneOptions is what the flags decided.
type pruneOptions struct {
	olderThan time.Duration
	// remove is true only when --yes was given and --dry-run was not. Every
	// other combination plans and stops.
	remove bool
}

func newEnvPruneCommand(e *Env) *cobra.Command {
	var olderThan time.Duration
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "List the environments older than a cutoff, and remove them with --yes",
		Long: strings.TrimSpace(`
An environment nobody tore down holds a database branch, a network, and a
container per service, and the machine that accumulates a dozen of them is a
machine somebody reboots to fix.

Run bare, it removes nothing. It lists every environment on this machine that
is older than the cutoff, whichever repository created it, and stops with the
command that would remove them. Removal needs --yes, and what --yes removes is
exactly what the bare run listed. --dry-run means the same as running bare and
is kept so that a script which passes it keeps working.

The cutoff is --older-than, a day when not given, and the plan prints it, so
the default is never something a reader has to remember. The scope is the
whole machine on purpose: this is the command for a laptop that is full, and
the daemon does not record which repository made what, so a cutoff from here
reaches every project's environments. For a sweep that reads each
environment's own lifetime instead, see af env reap.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := inventoryRuntime(e)
			if err != nil {
				return err
			}
			return runPrune(cmd.Context(), e, pruneOptions{
				olderThan: olderThan, remove: pruneRemoves(dryRun, yes),
			}, runtimePruner{rt: rt})
		},
	}
	cmd.Flags().DurationVar(&olderThan, "older-than", pruneCutoff,
		"Only consider environments older than this")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"List what would be removed and stop, which is also what running bare does")
	cmd.Flags().BoolVar(&yes, "yes", false,
		"Remove what the plan lists. Without it nothing is removed")
	return cmd
}

// pruneRemoves is the one decision the flags make. --dry-run beats --yes,
// because somebody who typed both is asking to look.
func pruneRemoves(dryRun, yes bool) bool { return yes && !dryRun }

// runPrune plans, prints the plan, and removes only when asked to.
//
// The plan is computed once and the removal walks that same list, so the
// environments --yes removes are the ones a bare run printed, not a second
// reading that could differ.
func runPrune(ctx context.Context, e *Env, opts pruneOptions, p pruner) error {
	defer func() { _ = p.close() }()

	envs, err := p.environments(ctx)
	if err != nil {
		return err
	}
	var stale []environment
	for _, env := range envs {
		if e.Clock.Since(env.Oldest) > opts.olderThan {
			stale = append(stale, env)
		}
	}
	cutoff := pruneCutoffLabel(opts.olderThan)
	proceed := fmt.Sprintf("af env prune --older-than %s --yes", cutoff)

	doc := PruneJSON{
		OlderThan: cutoff, Scope: "machine", DryRun: !opts.remove,
		WouldRemove: []PruneEnvJSON{}, Removed: []PruneEnvJSON{},
	}
	for _, env := range stale {
		doc.WouldRemove = append(doc.WouldRemove, pruneEnvJSON(e, env))
	}

	if !opts.remove {
		if len(stale) > 0 {
			doc.Proceed = proceed
		}
		if e.Out.Format == FormatJSON {
			return e.Out.JSON(doc)
		}
		if len(stale) == 0 {
			e.Out.Printf("Nothing on this machine is older than %s. Nothing was removed.\n", cutoff)
			return nil
		}
		e.Out.Printf("Older than %s on this machine, from every repository that has built here:\n\n", cutoff)
		printPrunePlan(e, stale)
		e.Out.Println("")
		e.Out.Printf("  %s would be removed, %d resources. Nothing has been removed.\n",
			plural(len(stale), "environment", "environments"), countResources(stale))
		e.Out.Hint("Remove exactly these with", proceed)
		return nil
	}

	if len(stale) == 0 {
		if e.Out.Format == FormatJSON {
			return e.Out.JSON(doc)
		}
		e.Out.Printf("Nothing on this machine is older than %s. Nothing was removed.\n", cutoff)
		return nil
	}

	pending := 0
	for _, env := range stale {
		td, downErr := p.down(ctx, env.ID)
		item := pruneEnvJSON(e, env)
		item.Removed, item.Pending = td.Removed, len(td.Pending)
		doc.ResourcesRemoved += td.Removed
		pending += len(td.Pending)
		if downErr != nil {
			item.Error = downErr.Error()
			e.Out.Printf("  %s %s: %v\n", e.Out.S(StyleWarn, SymbolWarn), env.ID, downErr)
		} else {
			e.Out.Printf("  removed %s (%d resources)\n", env.ID, td.Removed)
		}
		doc.Removed = append(doc.Removed, item)
	}
	doc.Pending = pending
	// The plan list is what was acted on, and the acted list now carries it,
	// so the document says the same thing once.
	doc.WouldRemove = []PruneEnvJSON{}

	if e.Out.Format == FormatJSON {
		if err := e.Out.JSON(doc); err != nil {
			return err
		}
	} else {
		e.Out.Printf("\n  %s removed, %d resources.\n",
			plural(len(stale), "environment", "environments"), doc.ResourcesRemoved)
	}
	if pending > 0 {
		return aferrors.Coded(aferrors.AFRUN030, "count", fmt.Sprint(pending))
	}
	return nil
}

// printPrunePlan is the table a bare run shows. It is the same shape as
// af env list, because that is the table a reader has already learned.
func printPrunePlan(e *Env, envs []environment) {
	rows := make([][]string, 0, len(envs))
	for _, env := range envs {
		rows = append(rows, []string{
			env.ID, fmt.Sprint(env.Resources), fmt.Sprint(env.Running),
			humanAge(e.Clock.Since(env.Oldest)), strings.Join(env.Services, ", "),
		})
	}
	e.Out.Table([]Column{
		Col("ENVIRONMENT"), Num("RESOURCES"), Num("RUNNING"), Num("AGE"), Flex("SERVICES"),
	}, rows)
}

func pruneEnvJSON(e *Env, env environment) PruneEnvJSON {
	services := env.Services
	if services == nil {
		services = []string{}
	}
	return PruneEnvJSON{
		EnvID: env.ID, CreatedAt: env.Oldest.UTC().Format(time.RFC3339),
		AgeHours: e.Clock.Since(env.Oldest).Hours(), Resources: env.Resources,
		Running: env.Running, Services: services,
	}
}

func countResources(envs []environment) int {
	n := 0
	for _, env := range envs {
		n += env.Resources
	}
	return n
}

// pruneCutoffLabel writes a duration the way it can be passed back to
// --older-than, and the way somebody would say it: 24h rather than 24h0m0s,
// and 0s rather than "just now".
func pruneCutoffLabel(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return d.String()
	}
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
