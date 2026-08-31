package cli

// af model: your own model key, on this machine, with no control plane.
//
// The agents read a page and decide what a person would do next, and that takes
// a model. Until this existed there was exactly one way to give the engine a
// key, which was to export a variable, and there was no way at all to find out
// what the engine would do with it: no command said which key was configured,
// where it came from, or whether it worked. A person pasted a key into a shell
// profile and found out from a run twenty minutes later.
//
// This is the free path and the self-hosted one. `af provider` is the other
// half of bring-your-own-key and it is a different arrangement: the key is
// sealed on a control plane, a monthly cap is checked before it is decrypted,
// and runs reach the model through it. Anybody with a control plane should
// prefer that, because a cap you hold is a cap and a cap on a build machine is
// a hope. Anybody without one gets this, and it has to be as good.
//
// THE KEY IS NEVER AN ARGUMENT, here for the same reasons it is never one in
// `af secret` or `af provider`: a secret on a command line is in the shell's
// history file, is visible in ps to every other user on the machine, and is in
// any recording of the terminal.
//
// There is no `af model get`. The last four characters and a fingerprint answer
// the question this is asked to answer, which is whether the key here is the
// one you think it is, and a command that prints a key is one screenshot away
// from not being a store.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/auth"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/model"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// ModelShowJSON is what `af model show --output json` reports.
//
// There is no key field and no partial key field. A JSON shape is what somebody
// pipes into a log aggregator, and a field that exists is a field that ends up
// somewhere it was not meant to.
type ModelShowJSON struct {
	// Configured is false when no key was found anywhere, which is a supported
	// mode rather than a failure.
	Configured bool   `json:"configured"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	BaseURL    string `json:"base_url,omitempty"`
	// Custom reports whether BaseURL is somewhere other than the provider's.
	Custom bool `json:"custom_endpoint,omitempty"`
	// Capped reports whether calls go through a control plane, where a monthly
	// cap is checked before the sealed key is decrypted. False means straight
	// to the provider with no cap at all, which is worth a field of its own
	// rather than something a reader has to infer from base_url.
	Capped bool `json:"capped_by_control_plane"`
	// UncappedDespiteControlPlane names a control plane this machine is signed
	// in to whose cap is not in force, because this key goes direct.
	UncappedDespiteControlPlane string `json:"uncapped_despite_control_plane,omitempty"`
	// Source names where the key was found, never what it is.
	Source      string `json:"source,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// VerifiedAt is when 'af model test' last proved this exact key works.
	// Absent when it never has, or when the key has changed since.
	VerifiedAt string `json:"verified_at,omitempty"`
	// Planner is what a run will actually use, which is the question somebody
	// is really asking.
	Planner string `json:"planner"`
	// Shadowing names a lower priority source that also holds a key, so a
	// script sees what the terminal warns about. Reported in one place and not
	// the other is how a dashboard ends up saying everything is fine while the
	// person beside it is being told otherwise.
	Shadowing string `json:"also_set_in,omitempty"`
	// Searched names every source that was asked, so a "not configured" answer
	// says where a key could go rather than only that there is not one.
	Searched []string `json:"searched"`
}

// ModelTestJSON is what `af model test --output json` reports.
type ModelTestJSON struct {
	OK         bool   `json:"ok"`
	Outcome    string `json:"outcome"`
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	BaseURL    string `json:"base_url"`
	Status     int    `json:"status,omitempty"`
	LatencyMS  int64  `json:"latency_ms"`
	Detail     string `json:"detail,omitempty"`
	NextStep   string `json:"next_step,omitempty"`
	VerifiedAt string `json:"verified_at,omitempty"`
}

// ModelSetJSON is the result of storing a key.
type ModelSetJSON struct {
	Provider string `json:"provider"`
	// StoredIn names the place, because the keyring and the encrypted file have
	// very different properties and a user has to be told which one they got.
	StoredIn string `json:"stored_in"`
	// Shadowed is set when a higher priority source will answer instead, which
	// makes the value just stored have no effect until that one is removed.
	Shadowed string `json:"shadowed_by,omitempty"`
}

// ModelRemoveJSON is the result of removing one.
type ModelRemoveJSON struct {
	Provider   string   `json:"provider"`
	RemovedRaw []string `json:"removed_from"`
	// Remaining names a source that still answers, which is the thing somebody
	// removing a leaked key most needs to be told.
	Remaining string `json:"still_supplied_by,omitempty"`
}

func newModelCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "The model key the agents use on this machine",
		Long: strings.TrimSpace(`
The agents can read a page and decide what a person would do next, which takes a
model. The key is yours: it is stored on this machine, the call goes straight to
the provider, and nothing hosted is involved.

With no key the deterministic planner runs instead. That is a supported mode,
not a broken one: workflows still run, still drive a real browser and still
produce a verdict. The model is what turns a workflow written as a sentence into
one the runner follows without being told every field.

  af model show     what is configured, and what a run will use
  af model test     prove the key works, with one cheap call
  af model set      store a key, without it touching the command line
  af model rm       remove a stored key

If you have a control plane, 'af provider' is the better place for a key: it
seals it, caps what may be spent on it per month, and checks that cap before the
key is ever decrypted. This command is the one that needs nothing but a terminal.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newModelShowCommand(e),
		newModelTestCommand(e),
		newModelSetCommand(e),
		newModelRemoveCommand(e),
	)
	return cmd
}

// modelChain is the chain a key is looked up in.
//
// The same one af up resolves DATABASE_URL against, deliberately. There is one
// precedence rule in this product and this is it: an export beats .env, .env
// beats the encrypted store, the store beats the keyring, and anything an
// enterprise build registered comes last. A second ordering invented for model
// keys would be a second thing to learn and a second thing to get wrong.
func modelChain(e *Env) *secrets.Chain {
	return secrets.LocalChain(e.WorkDir, e.Getenv, extension.Default, e.Keyring())
}

// ---------------------------------------------------------------------------
// af model show
// ---------------------------------------------------------------------------

func newModelShowCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "What is configured, and what a run will use",
		Long: strings.TrimSpace(`
Reports the provider, the model, the endpoint, where the key was found and when
it was last proven to work.

It does not show the key and there is no flag that would. The fingerprint
answers the question this is usually asked to answer, which is whether the key
here is the one you think it is, and it answers it without either person having
to read a secret out loud.

"Where it came from" is worth as much as the rest together. A key exported in
one shell and a key in the keyring look identical from a run's point of view
until they disagree, and then the only useful sentence is which one won.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := contextOf(cmd)
			chain := modelChain(e)
			cfg, err := model.Resolve(ctx, chain)
			if err != nil {
				return err
			}
			searched := chain.Considered(ctx)

			if cfg == nil {
				if e.Out.Format == FormatJSON {
					return e.Out.JSON(ModelShowJSON{
						Configured: false,
						Planner:    "deterministic",
						Searched:   searched,
					})
				}
				e.Out.Section("Model")
				e.Out.Println("  No key is configured, so runs use the deterministic planner.")
				e.Out.Println("  That is a supported mode: workflows still run and still produce a verdict.")
				e.Out.Println("")
				// Considered rather than Sources: the place a key most often
				// belongs is the .env that does not exist yet, and a list of
				// only the usable sources would never mention it.
				e.Out.Println("  Where a key can go, in the order they are asked:")
				for _, s := range searched {
					e.Out.Printf("    %s\n", s)
				}
				e.Out.Println("")
				e.Out.Printf("  Store one with: af model set %s\n", model.Providers[0].Name)
				return nil
			}

			record := model.ReadRecord(e.WorkDir, cfg.Fingerprint)
			verified := ""
			if record != nil {
				verified = record.VerifiedAt.Format(time.RFC3339)
			}

			shadow := shadowedBy(ctx, chain, cfg)
			uncapped := uncappedControlPlane(e, cfg)

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(ModelShowJSON{
					Configured:                  true,
					Provider:                    cfg.Provider.Name,
					Model:                       cfg.Model,
					BaseURL:                     cfg.BaseURL,
					Custom:                      cfg.Custom(),
					Capped:                      cfg.ThroughControlPlane(),
					UncappedDespiteControlPlane: uncapped,
					Source:                      cfg.Source,
					Fingerprint:                 cfg.Fingerprint,
					VerifiedAt:                  verified,
					Planner:                     "model",
					Shadowing:                   shadow,
					Searched:                    searched,
				})
			}

			e.Out.Section("Model")
			rows := [][]string{
				{"Provider", cfg.Provider.Name},
				{"Model", cfg.Model},
			}
			endpoint := cfg.BaseURL
			switch {
			case cfg.ThroughControlPlane():
				endpoint += "  (your control plane, where the monthly cap applies)"
			case cfg.Custom():
				endpoint += "  (a custom endpoint, not " + cfg.Provider.DefaultBaseURL + ")"
			}
			rows = append(rows,
				[]string{"Endpoint", endpoint},
				[]string{"Key from", cfg.Source},
				[]string{"Fingerprint", cfg.Fingerprint},
			)
			// Said as a sentence rather than a dash. A dash in this row reads
			// as "nothing to report" and what it actually means is that nobody
			// has ever established that this key works.
			if record != nil {
				rows = append(rows, []string{"Last verified",
					record.VerifiedAt.Format(time.RFC3339) + " as " + record.Model})
			} else {
				rows = append(rows, []string{"Last verified", "never; run 'af model test'"})
			}
			e.Out.Table([]string{"", ""}, rows)

			// The shadowing note. Somebody who has just run 'af model set' and
			// is looking at a key from somewhere else needs this said out loud,
			// because everything above it is correct and none of it explains
			// why the key they stored is not the one in use.
			if shadow != "" {
				e.Out.Println("")
				e.Out.Printf("  %s is also set in %s, which is asked after\n",
					cfg.Provider.KeyVar, shadow)
				e.Out.Printf("  %s. Unset the one here to use that one instead.\n", cfg.Source)
			}
			if uncapped != "" {
				e.Out.Println("")
				e.Out.Printf("  You are signed in to %s, and a key stored\n", uncapped)
				e.Out.Println("  there with 'af provider' has a monthly cap. This key is not that")
				e.Out.Println("  one: it goes straight to the provider and no cap applies.")
				e.Out.Println("")
				e.Out.Println("  To use the capped key instead, put an Antifailure token where the")
				e.Out.Printf("  provider key goes, and point %s at:\n", cfg.Provider.BaseURLVar)
				// The URL alone on its line. Prose wrapped around it would run
				// past eighty columns for any control plane with a real domain,
				// and this is a line people copy.
				e.Out.Printf("    %s/byok/%s\n", uncapped, cfg.Provider.Name)
			}
			return nil
		},
	}
	return cmd
}

// shadowedBy names a lower priority source that also holds a key, or nothing.
//
// It exists for one specific confusion, which is the most likely thing to
// happen the first time somebody uses this: they have ANTHROPIC_API_KEY
// exported from months ago, they run 'af model set anthropic' and paste a new
// one, and every run keeps using the old key. Nothing is broken and nothing
// says anything, which is the worst combination.
func shadowedBy(ctx context.Context, chain *secrets.Chain, cfg *model.Config) string {
	found := false
	for _, source := range chain.Sources(ctx) {
		if source == cfg.Source {
			found = true
			continue
		}
		if !found {
			continue
		}
		// Everything after the winning source, asked directly, so this reports
		// a real second copy rather than guessing that one might exist.
		if value, _, ok, err := chain.LookupIn(ctx, source, cfg.Provider.KeyVar); err == nil && ok {
			if strings.TrimSpace(value.Reveal()) != "" {
				return source
			}
		}
	}
	return ""
}

// uncappedControlPlane names a control plane whose cap this key is bypassing.
//
// The failure it exists for is quiet and expensive. Somebody runs 'af provider
// set anthropic' and 'af provider budget anthropic 50', and believes they have
// a fifty dollar ceiling. Nothing routes a run through the control plane on its
// own: reaching the sealed key means pointing the base URL at the gateway by
// hand, and the documentation for it says so in a code block people skim. So a
// local key, which is the thing this whole command family makes easy to have,
// sends every run straight to the provider with no ceiling whatsoever, and the
// only evidence is a bill at the end of the month.
//
// The cap was the entire reason to prefer the hosted arrangement. A cap that
// silently is not applied is worse than no cap, because the person stopped
// watching.
//
// Local only. It reads the credential this machine already stored, makes no
// request, and says nothing about whether a provider key exists on that control
// plane, because finding that out needs a network call and a scope. "You are
// signed in to somewhere that can cap this and this key is not capped" is both
// true and enough.
func uncappedControlPlane(e *Env, cfg *model.Config) string {
	if cfg.ThroughControlPlane() {
		return ""
	}
	origin := auth.Normalise(controlPlaneFor(e, ""))
	cred, err := e.CredentialStore().Load(origin)
	if err != nil || cred.Expired(e.Clock.Now()) {
		// Not signed in, or signed in with something that has lapsed. Neither
		// is a state where a cap could have been in force, so there is nothing
		// to warn about and saying so anyway would be noise on every machine
		// that has never seen a control plane.
		return ""
	}
	return origin
}

// ---------------------------------------------------------------------------
// af model test
// ---------------------------------------------------------------------------

func newModelTestCommand(e *Env) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Prove the key works, with one cheap call",
		Long: strings.TrimSpace(`
Sends one completion of a single token and reports what came back.

A real call rather than a check of the key's shape, because a well formed key
that was revoked this morning passes every shape check there is. It costs a
fraction of a cent, which is the point: this is meant to be run whenever you are
unsure, and a check people avoid because of the price is a check nobody runs.

What it can tell apart matters more than that it runs. A revoked key, an empty
balance, a model name that does not exist, a throttle, a provider outage and an
endpoint nothing answers on all fail, they all have different fixes, and being
told only that the call failed sends you to the wrong one first.

On success it writes down that this exact key worked, and 'af model show'
reports it. Rotating the key discards that, because a previous key's success
says nothing about the new one.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := contextOf(cmd)
			cfg, err := model.Resolve(ctx, modelChain(e))
			if err != nil {
				return err
			}
			if cfg == nil {
				// Not an error. Somebody with no key has a working product and
				// telling them their setup failed would be false.
				if e.Out.Format == FormatJSON {
					return e.Out.JSON(ModelTestJSON{
						OK: false, Outcome: "not-configured",
						NextStep: "Store a key with 'af model set anthropic', or run without one.",
					})
				}
				e.Out.Section("Model")
				e.Out.Println("  No key is configured, so there is nothing to test.")
				e.Out.Println("  Runs use the deterministic planner, which needs no key.")
				e.Out.Println("")
				e.Out.Printf("  Store one with: af model set %s\n", model.Providers[0].Name)
				return nil
			}

			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			e.Out.Section("Model")
			e.Out.Printf("  Asking %s for one token as %s...\n", cfg.BaseURL, cfg.Model)

			result := model.Probe(ctx, &http.Client{Timeout: timeout}, *cfg, e.Clock.Now)

			verified := ""
			if result.OK() {
				now := e.Clock.Now()
				if err := model.WriteRecord(e.WorkDir, *cfg, now); err != nil {
					// Reported and not fatal. The call succeeded, which is what
					// was asked; failing the command because a note could not
					// be written would report a working key as broken.
					e.Out.Printf("  (the result could not be recorded: %v)\n", err)
				} else {
					verified = now.UTC().Format(time.RFC3339)
				}
			}

			if e.Out.Format == FormatJSON {
				out := ModelTestJSON{
					OK: result.OK(), Outcome: string(result.Outcome),
					Provider: cfg.Provider.Name, Model: cfg.Model, BaseURL: cfg.BaseURL,
					Status: result.Status, LatencyMS: result.Latency.Milliseconds(),
					Detail: result.Detail, NextStep: result.NextStep,
					VerifiedAt: verified,
				}
				if err := e.Out.JSON(out); err != nil {
					return err
				}
				if result.OK() {
					return nil
				}
				return silent(modelProbeError(cfg.Provider.Name, result))
			}

			if result.OK() {
				e.Out.Println("")
				e.Out.Printf("  The key works. %s\n", result.Detail)
				e.Out.Printf("  %d ms, fingerprint %s.\n",
					result.Latency.Milliseconds(), cfg.Fingerprint)
				return nil
			}
			return modelProbeError(cfg.Provider.Display, result)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second,
		"How long to wait for the endpoint, which a local model may need more of")
	return cmd
}

// modelProbeError turns a probe result into the coded error for its kind.
//
// Two codes rather than one, because the exit code is read by scripts and a
// revoked key and a provider having a bad afternoon deserve different answers:
// one is a configuration failure nobody should retry and the other is exactly
// the thing to retry.
func modelProbeError(provider string, r model.Result) error {
	code := aferrors.AFAGT005
	if r.Outcome == model.OutcomeUnreachable ||
		r.Outcome == model.OutcomeTimedOut ||
		r.Outcome == model.OutcomeProviderDown ||
		r.Outcome == model.OutcomeRateLimited {
		code = aferrors.AFAGT006
	}
	return aferrors.Coded(code,
		"provider", provider,
		"detail", r.Detail,
		"next_step", r.NextStep)
}

// ---------------------------------------------------------------------------
// af model set
// ---------------------------------------------------------------------------

func newModelSetCommand(e *Env) *cobra.Command {
	var fromEnv string
	var fromStdin bool

	cmd := &cobra.Command{
		Use:   "set <provider>",
		Short: "Store a key, without it touching the command line",
		Long: strings.TrimSpace(`
Stores a key in the system keyring where this platform has one, and in the
encrypted local store where it does not.

The key is never an argument. There is no --key flag, deliberately: a secret on
a command line is written to your shell's history file, is visible in ps to
every other user on the machine, and is captured by any recording of the
terminal. So there are three ways to give it, and none of them put it in the
argument vector:

  af model set anthropic                      asks, without echoing
  af model set anthropic --stdin < key.txt    reads one line
  af model set anthropic --from-env NAME      reads that environment variable

Where it lands is reported rather than assumed, because the two places are not
equivalent. macOS gates the keychain on the login keychain, Linux on the session
keyring daemon, and Windows on the user's credentials. The encrypted local store
is a file, and it is only as strong as the passphrase protecting it.

This does not reach the provider. Storing a key here does not create one and
removing it does not revoke one.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := contextOf(cmd)
			provider, ok := model.Lookup(args[0])
			if !ok {
				// Checked before a key is read, rather than after somebody has
				// pasted one into a prompt.
				return fmt.Errorf("%q is not a provider the agents can use. Known: %s",
					args[0], strings.Join(model.Names(), ", "))
			}

			key, err := readProviderKey(e, provider.Name, fromEnv, fromStdin)
			if err != nil {
				return err
			}

			where, err := model.Store(e.WorkDir, e.Getenv, e.Keyring(), provider, key)
			if err != nil {
				if errors.Is(err, model.ErrNoStore) {
					return aferrors.Coded(aferrors.AFSEC004)
				}
				return err
			}
			// Registered so that nothing downstream in this process can print
			// it, including an error from a source that echoes what it was
			// given.
			e.Redactor.Register(key)

			// Resolved again afterwards rather than assumed, because the thing
			// worth reporting is not what was stored, it is what a run will now
			// use. Those differ whenever a higher priority source holds a key,
			// and that is the single most likely first-use confusion there is.
			shadow := ""
			if cfg, resolveErr := model.Resolve(ctx, modelChain(e)); resolveErr == nil &&
				cfg != nil && cfg.Provider.Name == provider.Name && cfg.Source != where {
				shadow = cfg.Source
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(ModelSetJSON{
					Provider: provider.Name, StoredIn: where, Shadowed: shadow,
				})
			}

			e.Out.Println("")
			e.Out.Printf("  Stored the %s key in %s.\n", provider.Name, where)
			if shadow != "" {
				e.Out.Println("")
				e.Out.Printf("  It is not the key runs will use. %s is also set\n", provider.KeyVar)
				e.Out.Printf("  in %s, which is asked first.\n", shadow)
				e.Out.Println("  Unset it there, or storing this one has no effect.")
				return nil
			}
			e.Out.Println("")
			e.Out.Println("  Check it works: af model test")
			return nil
		},
	}
	cmd.Flags().StringVar(&fromEnv, "from-env", "",
		"Read the key from this environment variable instead of asking")
	cmd.Flags().BoolVar(&fromStdin, "stdin", false,
		"Read the key from standard input, one line")
	return cmd
}

// ---------------------------------------------------------------------------
// af model rm
// ---------------------------------------------------------------------------

func newModelRemoveCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm <provider>",
		Aliases: []string{"remove"},
		Short:   "Remove a stored key",
		Long: strings.TrimSpace(`
Removes the key from every place this command can write it, not from the first
one that answers. A key left in the encrypted store after the keyring entry was
removed is a key the next run silently uses, which is the exact failure somebody
is trying to prevent when they type this.

It cannot remove a key from a shell you exported it in or from a .env file, and
it says so when one is still there rather than reporting a removal that changed
nothing.

Removing a key that is not there is not an error. This is a command people run
in a hurry, and a retry after a timeout must not report failure for reaching the
state you asked for.

This does not reach the provider. If the key leaked, revoke it at Anthropic or
OpenAI as well: removing it here stops this machine using it and stops nobody
else.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := contextOf(cmd)
			provider, ok := model.Lookup(args[0])
			if !ok {
				return fmt.Errorf("%q is not a provider the agents can use. Known: %s",
					args[0], strings.Join(model.Names(), ", "))
			}

			removed, err := model.Remove(e.WorkDir, e.Getenv, e.Keyring(), provider)
			if err != nil {
				return err
			}
			// The verification note is about a key that is gone. Left behind it
			// would attach to whatever key is stored next if the fingerprints
			// ever collided, and it is meaningless either way.
			if err := model.ForgetRecord(e.WorkDir); err != nil {
				return err
			}

			// What still answers is the important half of this output. Somebody
			// removing a leaked key needs to know that their shell still has
			// it far more than they need to know the keyring no longer does.
			remaining := ""
			if cfg, resolveErr := model.Resolve(ctx, modelChain(e)); resolveErr == nil &&
				cfg != nil && cfg.Provider.Name == provider.Name {
				remaining = cfg.Source
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(ModelRemoveJSON{
					Provider: provider.Name, RemovedRaw: removed, Remaining: remaining,
				})
			}

			e.Out.Println("")
			if len(removed) == 0 {
				e.Out.Printf("  There was no stored %s key to remove.\n", provider.Name)
			} else {
				e.Out.Printf("  Removed the %s key from %s.\n",
					provider.Name, strings.Join(removed, " and "))
			}
			if remaining != "" {
				e.Out.Println("")
				e.Out.Printf("  %s is still set in %s, so runs\n", provider.KeyVar, remaining)
				e.Out.Println("  will keep using a key. This command cannot reach there:")
				e.Out.Println("  unset it yourself.")
				return nil
			}
			if len(removed) > 0 {
				e.Out.Println("  Revoke it at the provider too: this does not reach them.")
			}
			return nil
		},
	}
	return cmd
}
