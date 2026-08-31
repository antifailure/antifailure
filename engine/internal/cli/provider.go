package cli

// af provider: your own Anthropic and OpenAI keys, from a terminal.
//
// The same capability the console has. It exists because the console is not
// always where the person is: a key gets rotated from a laptop at the end of an
// incident, and telling somebody to open a browser to do it is how a rotation
// gets postponed until tomorrow.
//
// THE KEY IS NEVER AN ARGUMENT. Not a flag, not a positional, not even
// optionally. A secret on a command line is written to the shell's history
// file, is visible in `ps` to every other user on the machine, and is captured
// by any terminal recording. So it is read from a terminal without echo, or
// from a pipe, or out of a named environment variable -- three ways of getting
// a key here that all leave it out of the argument vector.
//
// There is no `af provider get`, and there will not be. The control plane has
// no route that returns a key and no scope that would grant one; storing a
// secret and reading a secret are different capabilities and a terminal needs
// only the first.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/auth"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// knownProviders is checked here so a typo is caught before a key is read from
// a terminal, rather than after somebody has pasted one.
var knownProviders = []string{"anthropic", "openai"}

// ProviderKeyJSON is one stored key, as `af provider list --json` reports it.
type ProviderKeyJSON struct {
	Provider    string `json:"provider"`
	Last4       string `json:"last4"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"created_at,omitempty"`
	RotatedAt   string `json:"rotated_at,omitempty"`
}

// ProviderBudgetJSON is one month's cap.
type ProviderBudgetJSON struct {
	Provider     string  `json:"provider"`
	Period       string  `json:"period"`
	CapUSD       float64 `json:"cap_usd"`
	SpentUSD     float64 `json:"spent_usd"`
	RemainingUSD float64 `json:"remaining_usd"`
}

// ProviderListJSON is the whole listing.
type ProviderListJSON struct {
	ControlPlane string               `json:"control_plane"`
	Organization string               `json:"organization,omitempty"`
	Sealing      bool                 `json:"sealing"`
	Keys         []ProviderKeyJSON    `json:"keys"`
	Budgets      []ProviderBudgetJSON `json:"budgets"`
}

// ProviderSetJSON is the result of storing one.
type ProviderSetJSON struct {
	ControlPlane string `json:"control_plane"`
	Provider     string `json:"provider"`
	Last4        string `json:"last4"`
	Fingerprint  string `json:"fingerprint"`
	Replaced     bool   `json:"replaced"`
	SameAsBefore bool   `json:"same_as_before"`
}

// ProviderRemoveJSON is the result of removing one.
type ProviderRemoveJSON struct {
	ControlPlane string `json:"control_plane"`
	Provider     string `json:"provider"`
	Revoked      bool   `json:"revoked"`
}

func newProviderCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Your own model provider keys and their monthly caps",
		Long: strings.TrimSpace(`
Stores your Anthropic and OpenAI keys on the control plane, sealed with a secret
that is not in its database, and caps what may be spent on each one per month.

Runs use your key. We never see it after you save it: what any screen or any
command here can read is the last four characters and a fingerprint.

These commands need a token that asked for the capability:

  af login --scope providers.write

A token from a plain af login cannot reach a key, which is deliberate. The scope
appears on the screen where the login is approved, so nobody grants this without
seeing the words.`),
	}
	cmd.AddCommand(newProviderListCommand(e))
	cmd.AddCommand(newProviderSetCommand(e))
	cmd.AddCommand(newProviderRemoveCommand(e))
	cmd.AddCommand(newProviderBudgetCommand(e))
	return cmd
}

// providerSession is the credential and client for one control plane.
type providerSession struct {
	client *auth.Client
	cred   auth.Credential
	origin string
}

func providerSessionFor(e *Env, flag string) (providerSession, error) {
	origin := auth.Normalise(controlPlaneFor(e, flag))
	store := e.CredentialStore()
	cred, err := store.Load(origin)
	if errors.Is(err, auth.ErrNotSignedIn) {
		// The scope is named rather than left to the generic next step,
		// because a sign in without providers.write succeeds and then fails
		// here again on the next command, which reads as the fix not working.
		return providerSession{}, aferrors.Coded(aferrors.AFCPL004,
			"origin", origin,
			"command", "af login --control-plane "+origin+" --scope providers.write")
	}
	if err != nil {
		return providerSession{}, err
	}
	// Checked here rather than left to a 401, so that an expired credential
	// says what to do instead of looking like a permissions problem.
	if cred.Expired(e.Clock.Now()) {
		return providerSession{}, fmt.Errorf(
			"the credential for %s expired. Run: af login --scope providers.write", origin)
	}
	return providerSession{client: auth.NewClient(origin), cred: cred, origin: origin}, nil
}

// explainScope turns the server's refusal into the command that fixes it.
//
// The server already names the scope; this keeps the control plane in the
// message, because somebody with two of them signed in needs to know which one
// refused.
func (s providerSession) explain(err error) error {
	if errors.Is(err, auth.ErrScopeMissing) {
		return fmt.Errorf("%w\n\nRun: af login --control-plane %s --scope providers.write", err, s.origin)
	}
	if errors.Is(err, auth.ErrNotSignedIn) {
		return fmt.Errorf("the credential for %s is not valid any more. Run: af login", s.origin)
	}
	return err
}

func checkProvider(name string) (string, error) {
	got := strings.ToLower(strings.TrimSpace(name))
	for _, p := range knownProviders {
		if p == got {
			return p, nil
		}
	}
	return "", fmt.Errorf("%q is not a provider this stores. Known: %s",
		name, strings.Join(knownProviders, ", "))
}

// ---------------------------------------------------------------------------
// af provider list
// ---------------------------------------------------------------------------

func newProviderListCommand(e *Env) *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "What is stored, and what it may spend this month",
		Long: strings.TrimSpace(`
Shows which providers have a key, the last four characters of each, and the
monthly cap against what has been spent.

It does not show a key, and there is no flag that would. The last four and the
fingerprint are enough to answer the question this is usually asked to answer:
whether the key here is the one you think it is.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := contextOf(cmd)
			session, err := providerSessionFor(e, baseURL)
			if err != nil {
				return err
			}
			got, err := session.client.ListProviders(ctx, session.cred.Token)
			if err != nil {
				return session.explain(err)
			}

			if e.Out.Format == FormatJSON {
				out := ProviderListJSON{
					ControlPlane: session.origin,
					Organization: session.cred.Organization,
					Sealing:      got.Sealing,
					Keys:         make([]ProviderKeyJSON, 0, len(got.Keys)),
					Budgets:      make([]ProviderBudgetJSON, 0, len(got.Budgets)),
				}
				for _, k := range got.Keys {
					out.Keys = append(out.Keys, ProviderKeyJSON{
						Provider: k.Provider, Last4: k.Last4, Fingerprint: k.Fingerprint,
						CreatedAt: k.CreatedAt, RotatedAt: k.RotatedAt,
					})
				}
				for _, b := range got.Budgets {
					out.Budgets = append(out.Budgets, ProviderBudgetJSON{
						Provider: b.Provider, Period: b.Period, CapUSD: b.CapUSD,
						SpentUSD: b.SpentUSD, RemainingUSD: b.RemainingUSD,
					})
				}
				return e.Out.JSON(out)
			}

			if !got.Sealing {
				e.Out.Println("")
				e.Out.Println("  This control plane has no sealing secret, so it cannot store a key.")
				e.Out.Println("  Set AF_PROVIDER_KEY_SECRET on it and restart it.")
			}

			rows := make([][]string, 0, len(knownProviders))
			for _, p := range knownProviders {
				key := findKey(got.Keys, p)
				budget := findBudget(got.Budgets, p)

				stored := "not set"
				if key != nil {
					// Asterisks rather than bullet characters, for the reason
					// the status symbols are ASCII: this is read in CI logs and
					// pasted into pull request comments as often as it is read
					// on a terminal, and a bullet is not guaranteed in either.
					stored = "********" + key.Last4
				}
				// A provider with no budget row cannot spend anything. Said as
				// "none, so nothing may be spent" rather than as a dash,
				// because a dash reads as "unlimited" and it is the opposite.
				cap := "none, so nothing may be spent"
				spent := "not tracked"
				if budget != nil {
					cap = fmt.Sprintf("%.2f USD", budget.CapUSD)
					spent = fmt.Sprintf("%.2f USD", budget.SpentUSD)
				}
				rows = append(rows, []string{p, stored, cap, spent})
			}
			e.Out.Table([]Column{
				Col("PROVIDER"), Flex("KEY"), Num("MONTHLY CAP"), Num("SPENT"),
			}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "", controlPlaneFlagHelp)
	return cmd
}

func findKey(keys []auth.ProviderKey, provider string) *auth.ProviderKey {
	for i := range keys {
		if keys[i].Provider == provider {
			return &keys[i]
		}
	}
	return nil
}

func findBudget(budgets []auth.ProviderBudget, provider string) *auth.ProviderBudget {
	for i := range budgets {
		if budgets[i].Provider == provider {
			return &budgets[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// af provider set
// ---------------------------------------------------------------------------

func newProviderSetCommand(e *Env) *cobra.Command {
	var baseURL string
	var fromEnv string
	var fromStdin bool

	cmd := &cobra.Command{
		Use:   "set <provider>",
		Short: "Store or rotate a key, without it touching the command line",
		Long: strings.TrimSpace(`
Stores a key for anthropic or openai, replacing whatever was there.

The key is never an argument. There is no --key flag, deliberately: a secret on
a command line is in the shell's history file, is visible in ps to everybody
else on the machine, and is in any recording of the terminal. So there are three
ways to give it, and none of them put it in the argument vector: it is asked
for without echoing, read as one line from stdin, or read from an environment
variable this process already has.

Rotating stores the new key and revokes the old one together. If the key given
is the one already stored, that is reported rather than accepted quietly: it is
the mistake people make at the moment they believe they have replaced a leaked
key.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := contextOf(cmd)
			provider, err := checkProvider(args[0])
			if err != nil {
				return err
			}
			// Before reading the key, so a login problem is not discovered
			// after somebody has pasted a secret into a prompt.
			session, err := providerSessionFor(e, baseURL)
			if err != nil {
				return err
			}

			key, err := readProviderKey(e, provider, fromEnv, fromStdin)
			if err != nil {
				return err
			}

			saved, err := session.client.SetProviderKey(ctx, session.cred.Token, provider, key)
			if err != nil {
				return session.explain(err)
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(ProviderSetJSON{
					ControlPlane: session.origin, Provider: saved.Provider,
					Last4: saved.Last4, Fingerprint: saved.Fingerprint,
					Replaced: saved.Replaced, SameAsBefore: saved.SameAsBefore,
				})
			}

			e.Out.Println("")
			switch {
			case saved.SameAsBefore:
				e.Out.Printf("  That is the key that was already stored for %s (••••••••%s).\n",
					provider, saved.Last4)
				e.Out.Println("  Nothing changed. If you meant to rotate, paste the NEW key.")
			case saved.Replaced:
				e.Out.Printf("  Rotated the %s key to ••••••••%s.\n", provider, saved.Last4)
				e.Out.Println("  The old one no longer works here. Revoke it at the provider too:")
				e.Out.Println("  this does not reach them.")
			default:
				e.Out.Printf("  Stored the %s key, ••••••••%s.\n", provider, saved.Last4)
			}
			e.Out.Printf("  Fingerprint %s\n", saved.Fingerprint)
			e.Out.Println("")
			e.Out.Printf("  Set a cap before anything can be spent: af provider budget %s <usd>\n", provider)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "", controlPlaneFlagHelp)
	cmd.Flags().StringVar(&fromEnv, "from-env", "",
		"Read the key from this environment variable instead of asking")
	cmd.Flags().BoolVar(&fromStdin, "stdin", false,
		"Read the key from standard input, one line")
	return cmd
}

// readProviderKey gets a key without it appearing in the argument vector.
func readProviderKey(e *Env, provider, fromEnv string, fromStdin bool) (string, error) {
	if fromEnv != "" {
		value := strings.TrimSpace(e.Getenv(fromEnv))
		if value == "" {
			// Named rather than "the key was empty", because the fix is to
			// export that variable and the message has to say which one.
			return "", fmt.Errorf("%s is not set, or is empty", fromEnv)
		}
		return value, nil
	}

	// No terminal and no --stdin is the CI case, and it must refuse rather than
	// read: a read from a closed stdin either blocks forever or returns nothing
	// at once, and both look like a hang to whoever is watching the log.
	if !fromStdin && !e.Interactive() {
		return "", errors.New(
			"there is no terminal to ask on. Use --stdin or --from-env NAME")
	}

	if !fromStdin {
		e.Out.Println("")
		e.Out.Printf("  Paste your %s key. It is not echoed and not stored on this machine.\n", provider)
	}
	key, err := readSecret(e, fromStdin)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(key) == "" {
		return "", errors.New("no key was given")
	}
	return strings.TrimSpace(key), nil
}

// ---------------------------------------------------------------------------
// af provider rm
// ---------------------------------------------------------------------------

func newProviderRemoveCommand(e *Env) *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:     "rm <provider>",
		Aliases: []string{"remove", "revoke"},
		Short:   "Remove a stored key",
		Long: strings.TrimSpace(`
Removes the stored key. Runs that need this provider are refused afterwards,
with a message saying why, rather than falling back to a key of ours.

This does not reach the provider. If the key leaked, revoke it there as well:
removing it here stops us using it and stops nobody else.

Removing a key that is not there is not an error. This is the command somebody
runs in a hurry, and a retry after a timeout must not report failure for
reaching the state they asked for.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := contextOf(cmd)
			provider, err := checkProvider(args[0])
			if err != nil {
				return err
			}
			session, err := providerSessionFor(e, baseURL)
			if err != nil {
				return err
			}
			revoked, err := session.client.RemoveProviderKey(ctx, session.cred.Token, provider)
			if err != nil {
				return session.explain(err)
			}
			if e.Out.Format == FormatJSON {
				return e.Out.JSON(ProviderRemoveJSON{
					ControlPlane: session.origin, Provider: provider, Revoked: revoked,
				})
			}
			e.Out.Println("")
			if revoked {
				e.Out.Printf("  Removed the %s key. It cannot be used from here again.\n", provider)
				e.Out.Println("  Revoke it at the provider too: this does not reach them.")
			} else {
				e.Out.Printf("  There was no %s key stored.\n", provider)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "", controlPlaneFlagHelp)
	return cmd
}

// ---------------------------------------------------------------------------
// af provider budget
// ---------------------------------------------------------------------------

func newProviderBudgetCommand(e *Env) *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:   "budget <provider> <usd>",
		Short: "Cap what may be spent on a provider this month",
		Long: strings.TrimSpace(`
Sets the monthly cap in US dollars. The cap is checked BEFORE the key is
decrypted, so a run with no allowance never causes the key to exist in the
control plane's memory at all. That ordering is the difference between a cap and
a suggestion.

A provider with no cap cannot spend anything. A missing cap reads as zero rather
than as unlimited, because the alternative on somebody else's key is an
unbounded bill.

A cap of zero is allowed and means exactly that: spend nothing on this provider.`),
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := contextOf(cmd)
			provider, err := checkProvider(args[0])
			if err != nil {
				return err
			}
			// Parsed strictly. Anything that is not a number is refused rather
			// than coerced, because the coercions all land on zero and a silent
			// cap of zero looks like a working setup until every run is
			// refused for having no allowance.
			amount := strings.TrimPrefix(strings.TrimSpace(args[1]), "$")
			capUSD, err := strconv.ParseFloat(amount, 64)
			if err != nil || capUSD < 0 {
				return fmt.Errorf("%q is not an amount in US dollars, zero or more", args[1])
			}

			session, err := providerSessionFor(e, baseURL)
			if err != nil {
				return err
			}
			budget, err := session.client.SetProviderBudget(ctx, session.cred.Token, provider, capUSD)
			if err != nil {
				return session.explain(err)
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(ProviderBudgetJSON{
					Provider: budget.Provider, Period: budget.Period, CapUSD: budget.CapUSD,
					SpentUSD: budget.SpentUSD, RemainingUSD: budget.RemainingUSD,
				})
			}
			e.Out.Println("")
			e.Out.Printf("  %s may spend %.2f USD this month.\n", provider, budget.CapUSD)
			e.Out.Printf("  %.2f USD spent so far, %.2f left.\n", budget.SpentUSD, budget.RemainingUSD)
			if budget.CapUSD == 0 {
				e.Out.Println("")
				e.Out.Printf("  A cap of zero means runs needing %s are refused.\n", provider)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "", controlPlaneFlagHelp)
	return cmd
}

const controlPlaneFlagHelp = "The control plane to use (default: AF_CONTROL_PLANE_URL, or the hosted instance)"

// contextOf returns the command's context, or a background one.
//
// cobra leaves it nil when a command is executed without one, which happens in
// tests and in any embedding that calls Execute rather than ExecuteContext.
func contextOf(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
