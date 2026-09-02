package cli

// af token: the engine tokens a CI job or a self-hosted engine presents.
//
// The gap this closes. Three places told somebody to "create an engine token in
// the control plane": the self-hosting page, the next step on AF-CPL-001, and
// the next step on AF-CP-002. Nothing anywhere could create one. The console
// has no page for them and the tRPC router only lists and revokes, so the
// instruction pointed at a screen that does not exist and a self-hoster who
// followed it reached a control plane their engine could never authenticate to.
//
// A terminal is the right place for it rather than a second choice. What the
// token goes into is an environment variable in CI, so the person minting one
// is already at a shell, and a value that has to be copied out of a browser and
// into a secret store passes through a clipboard on the way.
//
// The scope is asked for by name for the same reason the provider scopes are:
// this mints a credential, and the words appear on the screen where the login
// is approved. A token from a plain af login cannot mint another one, so a
// leaked terminal credential does not become a credential factory.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/auth"
)

func newTokenCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Engine tokens, which is what CI and a self-hosted engine present",
		Long: strings.TrimSpace(`
An engine token is what goes in AF_CONTROL_PLANE_TOKEN. It belongs to the
organization rather than to you, so it keeps working after you leave, and it
carries no identity: it can send events and read an environment back, and it
cannot reach a key, a member, or another token.

These commands need a token that asked for the capability:

  af login --scope tokens.manage

A token from a plain af login cannot mint one, which is deliberate. A credential
that can make more credentials is a credential worth stealing twice.`),
	}
	cmd.AddCommand(newTokenCreateCommand(e))
	cmd.AddCommand(newTokenListCommand(e))
	cmd.AddCommand(newTokenRemoveCommand(e))
	return cmd
}

// tokenSessionFor is providerSessionFor with the other scope named.
//
// A separate function rather than a parameter, because what makes these
// messages worth anything is that they name the exact command that fixes them,
// and a shared one would have to say "the scope you need" instead of
// tokens.manage.
func tokenSessionFor(e *Env, flag string) (providerSession, error) {
	origin := auth.Normalise(controlPlaneFor(e, flag))
	cred, err := e.CredentialStore().Load(origin)
	if errors.Is(err, auth.ErrNotSignedIn) {
		return providerSession{}, fmt.Errorf(
			"not signed in to %s. Run: af login --control-plane %s --scope tokens.manage",
			origin, origin)
	}
	if err != nil {
		return providerSession{}, err
	}
	if cred.Expired(e.Clock.Now()) {
		return providerSession{}, fmt.Errorf(
			"the credential for %s expired. Run: af login --scope tokens.manage", origin)
	}
	return providerSession{client: auth.NewClient(origin), cred: cred, origin: origin}, nil
}

func explainToken(s providerSession, err error) error {
	if errors.Is(err, auth.ErrScopeMissing) {
		return fmt.Errorf("%w\n\nRun: af login --control-plane %s --scope tokens.manage",
			err, s.origin)
	}
	if errors.Is(err, auth.ErrNotSignedIn) {
		return fmt.Errorf("the credential for %s is not valid any more. Run: af login", s.origin)
	}
	return err
}

// ---------------------------------------------------------------------------
// af token create
// ---------------------------------------------------------------------------

// TokenCreateJSON is the machine readable result of a mint.
type TokenCreateJSON struct {
	ControlPlane string `json:"control_plane"`
	Organization string `json:"organization"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Prefix       string `json:"prefix"`
	// The whole token, and the only time anything will ever carry it.
	Token string `json:"token"`
}

func newTokenCreateCommand(e *Env) *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Mint an engine token and show it once",
		Long: strings.TrimSpace(`
Mints a token and prints it. Only its hash is stored, so this is the one and
only time it can be read: there is no command and no screen that will show it
again. If you lose it, mint another and revoke this one.

The name is a label you will read in a list months from now, so name it after
where it is going rather than after today.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := contextOf(cmd)
			session, err := tokenSessionFor(e, baseURL)
			if err != nil {
				return err
			}
			made, err := session.client.CreateEngineToken(ctx, session.cred.Token, args[0])
			if err != nil {
				return explainToken(session, err)
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(TokenCreateJSON{
					ControlPlane: session.origin,
					Organization: session.cred.Organization,
					ID:           made.ID, Name: made.Name,
					Prefix: made.Prefix, Token: made.Token,
				})
			}

			e.Out.Println("")
			e.Out.Printf("  %s\n", made.Token)
			e.Out.Println("")
			e.Out.Printf("  Named %s on %s.\n", made.Name, session.origin)
			e.Out.Println("")
			// The export line rather than a description of it. What the token
			// is for is putting it in this variable, and a person who has to
			// retype the variable name from prose gets it wrong once.
			e.Out.Println("  Put it where the engine runs:")
			e.Out.Println("")
			e.Out.Printf("    export AF_CONTROL_PLANE_URL=%s\n", session.origin)
			e.Out.Printf("    export AF_CONTROL_PLANE_TOKEN=%s\n", made.Token)
			e.Out.Println("")
			e.Out.Println("  Nothing can show it again. Only its hash was stored, so losing it")
			e.Out.Printf("  means minting another and revoking %s.\n", made.Prefix)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "", controlPlaneFlagHelp)
	return cmd
}

// ---------------------------------------------------------------------------
// af token list
// ---------------------------------------------------------------------------

// TokenListJSON is the machine readable listing.
type TokenListJSON struct {
	ControlPlane string          `json:"control_plane"`
	Organization string          `json:"organization"`
	Tokens       []TokenItemJSON `json:"tokens"`
}

// TokenItemJSON is one token. There is deliberately no token field.
type TokenItemJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	RevokedAt  string `json:"revoked_at,omitempty"`
}

func newTokenListCommand(e *Env) *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "What engine tokens exist, and when each was last used",
		Long: strings.TrimSpace(`
Shows every engine token, revoked ones included. A revoked one is shown rather
than hidden, because the question this is usually asked is whether the token
that stopped working is the one you revoked.

It does not show a token and there is no flag that would. The prefix is what
tells two of them apart, and it is what af token rm accepts.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := contextOf(cmd)
			session, err := tokenSessionFor(e, baseURL)
			if err != nil {
				return err
			}
			tokens, err := session.client.ListEngineTokens(ctx, session.cred.Token)
			if err != nil {
				return explainToken(session, err)
			}

			if e.Out.Format == FormatJSON {
				out := TokenListJSON{
					ControlPlane: session.origin,
					Organization: session.cred.Organization,
					Tokens:       make([]TokenItemJSON, 0, len(tokens)),
				}
				for _, t := range tokens {
					out.Tokens = append(out.Tokens, TokenItemJSON{
						ID: t.ID, Name: t.Name, Prefix: t.Prefix,
						CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt,
					})
				}
				return e.Out.JSON(out)
			}

			if len(tokens) == 0 {
				e.Out.Println("")
				e.Out.Printf("  No engine tokens on %s.\n", session.origin)
				e.Out.Println("")
				e.Out.Println("  Mint one with: af token create ci")
				return nil
			}

			rows := make([][]string, 0, len(tokens))
			for _, t := range tokens {
				// "never" rather than a dash. A token that has never been used
				// is the one somebody is looking for when CI cannot reach the
				// control plane, and a dash does not say that.
				used := "never"
				if t.LastUsedAt != "" {
					used = shortDay(t.LastUsedAt)
				}
				state := "active"
				if t.RevokedAt != "" {
					state = "revoked " + shortDay(t.RevokedAt)
				}
				rows = append(rows, []string{t.Name, t.Prefix, state, used})
			}
			// PREFIX is deliberately not flexible. It is exactly what
			// 'af token rm' takes as an argument, so a shortened one would
			// print something that does not work when it is pasted back.
			// NAME is the only unbounded column, a label somebody types, and
			// STATE can lose its revocation date without losing the word that
			// matters, so those two give up width to each other first.
			e.Out.Table([]Column{
				Flex("NAME"), Col("PREFIX"), Flex("STATE"), Col("LAST USED"),
			}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "", controlPlaneFlagHelp)
	return cmd
}

// shortDay keeps the date as well as the clock, unlike shortTime next door in
// net.go. A network decision list is one run and every row shares a date; a
// token list spans months, and the row somebody is looking for is usually the
// one that was last used a long time ago.
func shortDay(raw string) string {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return raw
	}
	return t.Local().Format("2006-01-02 15:04")
}

// ---------------------------------------------------------------------------
// af token rm
// ---------------------------------------------------------------------------

func newTokenRemoveCommand(e *Env) *cobra.Command {
	var baseURL string
	cmd := &cobra.Command{
		Use:     "rm <id or prefix>",
		Aliases: []string{"remove", "revoke"},
		Short:   "Revoke an engine token",
		Long: strings.TrimSpace(`
Revokes a token immediately. Anything presenting it stops being accepted on the
next request rather than at the end of a cache window.

Takes the prefix af token list shows, or the full id. Running it twice is not an
error: the second run says it was already revoked, because during an incident
the same command gets run twice and the second must not read as a new problem.`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := contextOf(cmd)
			session, err := tokenSessionFor(e, baseURL)
			if err != nil {
				return err
			}
			name, already, err := session.client.RevokeEngineToken(ctx, session.cred.Token, args[0])
			if err != nil {
				return explainToken(session, err)
			}
			if e.Out.Format == FormatJSON {
				return e.Out.JSON(map[string]any{
					"control_plane": session.origin,
					"revoked":       true,
					"name":          name,
					"already":       already,
				})
			}
			if already {
				e.Out.Printf("  %s was already revoked. Nothing changed.\n", name)
				return nil
			}
			e.Out.Printf("  %s is revoked. Anything holding it is refused from now on.\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "", controlPlaneFlagHelp)
	return cmd
}
