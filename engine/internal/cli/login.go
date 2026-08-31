package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/antifailure/antifailure/engine/internal/auth"
	"github.com/antifailure/antifailure/engine/internal/controlplane"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// af login, af logout, af whoami.
//
// The device authorization grant, because a terminal has no browser and no
// cookie. `af login` asks the control plane for a pair of codes, prints the
// short one, opens the long one in a browser if there is one, and waits while a
// person approves it somewhere that does have a session.
//
// WHY NOT A PASTED TOKEN, which is the obvious alternative and is what most
// tools do. A token somebody pastes has to exist before it is pasted: it is
// created in a browser, selected with a mouse, put on a clipboard that every
// other application can read, and pasted into a shell that writes it to a
// history file. The credential is exposed at four points before it is ever
// used. The device grant never shows it to a person at all -- the terminal
// receives it over TLS and puts it straight into the credential store.
//
// The token this receives is scoped. By default it can read environments and
// runs and write events, and nothing else: it cannot manage members, change
// policy, or touch a provider key. --scope asks for more, from a closed list
// the control plane owns, and what was asked for is shown on the screen where
// somebody approves -- so a capability cannot be granted without being read.
//
// Nothing in that list reads a key back, and nothing ever will: storing a
// secret and retrieving one are different capabilities.

// defaultControlPlane is the hosted instance, used when nothing else says.
//
// Taken from the control plane package rather than written out again. It was
// written out again, and it drifted: this said app.dev.antifailure.dev, which
// is the STAGING instance, while everything that sends events went to
// app.antifailure.dev. So a plain af login signed a terminal in to staging, and
// the credential it stored was for an origin nothing else in the engine ever
// talks to. Two spellings of "the hosted instance" is one too many.
const defaultControlPlane = controlplane.DefaultBaseURL

// LoginJSON is the machine readable result of a login.
type LoginJSON struct {
	ControlPlane string   `json:"control_plane"`
	Login        string   `json:"login"`
	Organization string   `json:"organization"`
	Role         string   `json:"role"`
	Scopes       []string `json:"scopes"`
	StoredIn     string   `json:"stored_in"`
	UsesKeyring  bool     `json:"uses_keyring"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
}

// WhoamiJSON is the machine readable identity.
type WhoamiJSON struct {
	ControlPlane string   `json:"control_plane"`
	Login        string   `json:"login"`
	Name         string   `json:"name,omitempty"`
	Organization string   `json:"organization"`
	Role         string   `json:"role"`
	Scopes       []string `json:"scopes"`
	TokenPrefix  string   `json:"token_prefix"`
	ExpiresAt    string   `json:"expires_at,omitempty"`
	StoredIn     string   `json:"stored_in"`
	Source       string   `json:"source"`
}

// LogoutJSON is the machine readable result of a logout.
type LogoutJSON struct {
	ControlPlane    string `json:"control_plane"`
	RemovedLocal    bool   `json:"removed_local"`
	RevokedOnServer bool   `json:"revoked_on_server"`
	Note            string `json:"note,omitempty"`
}

func controlPlaneFor(e *Env, flag string) string {
	if flag != "" {
		return flag
	}
	if v := e.Getenv("AF_CONTROL_PLANE_URL"); v != "" {
		return v
	}
	return defaultControlPlane
}

func newLoginCommand(e *Env) *cobra.Command {
	var baseURL string
	var noBrowser bool
	var scopes []string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to a control plane from this terminal",
		Long: strings.TrimSpace(`
Signs this machine in to a control plane using the device authorization grant.

af login prints a short code and opens a browser. Approve it there, and the
token arrives here over TLS and goes straight into the operating system's
credential store. The credential is never shown, never copied through a
clipboard, and never written to a shell history file.

By default the token can read environments and runs and write events, and
nothing else: it cannot manage members, change policy, or touch a provider key.

--scope asks for more. The scope is shown on the screen where the login is
approved, so nobody grants a capability without seeing the words:

  af login --scope providers.write

Nothing reads a key back. There is no scope for it, because storing a secret and
retrieving one are different capabilities and a terminal needs only the first.

Run af logout to remove it from this machine and revoke it everywhere.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			origin := controlPlaneFor(e, baseURL)
			client := auth.NewClient(origin)
			store := e.CredentialStore()

			// Checked before a code is printed. The server intersects what it
			// is sent and would refuse a login asking only for scopes that do
			// not exist, but it would do so after a person had read a code out
			// loud, so a typo is caught here instead.
			asked, err := checkScopes(scopes)
			if err != nil {
				return err
			}

			label := clientLabel(e)
			start, err := client.Begin(ctx, label, asked)
			if err != nil {
				return fmt.Errorf("start the login: %w", err)
			}

			if e.Out.Format != FormatJSON {
				e.Out.Println("")
				e.Out.Printf("  Your code is  %s\n", e.Out.S(StyleBold, start.UserCode))
				e.Out.Println("")
				e.Out.Printf("  Approve it at %s\n", start.VerificationURI)
				e.Out.Println("")
			}

			if !noBrowser {
				// Best effort. A machine with no browser is exactly the case
				// this command is for, so failing to open one is not a failure
				// to log in and is not reported as one.
				openBrowser(start.VerificationURIComplete)
			}

			waited := false
			token, err := client.Poll(ctx, start, nil, func() {
				if e.Out.Format != FormatJSON && !waited {
					waited = true
					e.Out.Println("  Waiting for approval...")
				}
			})
			switch {
			case errors.Is(err, auth.ErrDeclined):
				return errors.New("that login was declined in the browser")
			case errors.Is(err, auth.ErrLoginExpired):
				return errors.New("nobody approved that login in time. Run af login again")
			case err != nil:
				return fmt.Errorf("complete the login: %w", err)
			}

			// Who the token is for, asked of the server rather than assumed, so
			// that what is stored is what the control plane believes.
			id, err := client.Whoami(ctx, token.AccessToken)
			if err != nil {
				return fmt.Errorf("the login succeeded and the token was refused immediately after: %w", err)
			}

			cred := auth.Credential{
				ControlPlane: auth.Normalise(origin),
				Token:        token.AccessToken,
				Login:        id.Login,
				Organization: id.Organization,
				Scopes:       id.Scopes,
			}
			if token.ExpiresIn > 0 {
				cred.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UTC()
			}
			if err := store.Save(cred); err != nil {
				return fmt.Errorf("store the credential: %w", err)
			}

			where := store.Location(cred.ControlPlane)
			if e.Out.Format == FormatJSON {
				out := LoginJSON{
					ControlPlane: cred.ControlPlane,
					Login:        id.Login,
					Organization: id.Organization,
					Role:         id.Role,
					Scopes:       id.Scopes,
					StoredIn:     where,
					UsesKeyring:  store.UsesKeyring(),
				}
				if !cred.ExpiresAt.IsZero() {
					out.ExpiresAt = cred.ExpiresAt.Format(time.RFC3339)
				}
				return e.Out.JSON(out)
			}

			e.Out.Println("")
			e.Out.Printf("  Signed in as %s in %s (%s)\n", id.Login, id.Organization, id.Role)
			e.Out.Printf("  Token stored in %s\n", where)
			if !store.UsesKeyring() {
				// Said out loud rather than buried. This platform has no
				// credential store the engine can use, so the token is
				// protected by file permissions alone, and somebody is entitled
				// to know that before deciding whether to run this on a shared
				// machine.
				e.Out.Println("")
				e.Out.Println("  This platform has no keyring the engine can use yet, so the token is")
				e.Out.Println("  in a file that only your user can read. It is not in the repository.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "",
		"The control plane to sign in to (default: AF_CONTROL_PLANE_URL, or the hosted instance)")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false,
		"Do not try to open a browser; print the address instead")
	cmd.Flags().StringSliceVar(&scopes, "scope", nil,
		"Ask for a capability beyond the default, e.g. providers.write. Repeatable.")
	return cmd
}

// checkScopes refuses a name that is not a scope.
//
// The list it checks against mirrors the server's, and the server remains the
// authority: this exists so that `af login --scope providers.wrote` fails in
// the terminal rather than issuing a code, waiting for somebody to approve it
// in a browser, and handing back a token that cannot do the thing.
func checkScopes(asked []string) ([]string, error) {
	if len(asked) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(asked))
	for _, raw := range asked {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		known := false
		for _, s := range auth.GrantableScopes {
			if s == name {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("%q is not a scope. Available: %s",
				name, strings.Join(auth.GrantableScopes, ", "))
		}
		out = append(out, name)
	}
	return out, nil
}

func newLogoutCommand(e *Env) *cobra.Command {
	var baseURL string

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove this machine's credential and revoke it",
		Long: strings.TrimSpace(`
Removes the stored token and tells the control plane to revoke it.

Both halves matter. Removing it locally stops this machine using it; revoking
it stops anybody who copied it. A logout that only deleted the local copy would
leave a working credential in whatever backup or screen recording captured it.

If the control plane cannot be reached, the local credential is still removed
and the command says the revocation did not happen, so nobody is left believing
a token is dead when it is not.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			origin := auth.Normalise(controlPlaneFor(e, baseURL))
			store := e.CredentialStore()

			cred, loadErr := store.Load(origin)
			if errors.Is(loadErr, auth.ErrNotSignedIn) {
				if e.Out.Format == FormatJSON {
					return e.Out.JSON(LogoutJSON{ControlPlane: origin, Note: "there was nothing stored for this control plane"})
				}
				e.Out.Printf("  Nothing stored for %s.\n", origin)
				return nil
			}

			revoked := false
			var note string
			if loadErr == nil && cred.Token != "" {
				if err := auth.NewClient(origin).Revoke(ctx, cred.Token); err != nil {
					// Reported, not fatal. The local copy still goes.
					note = fmt.Sprintf("the control plane could not be reached, so the token was NOT revoked: %v", err)
				} else {
					revoked = true
				}
			}

			removed, err := store.Delete(origin)
			if err != nil {
				return fmt.Errorf("remove the stored credential: %w", err)
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(LogoutJSON{
					ControlPlane: origin, RemovedLocal: removed,
					RevokedOnServer: revoked, Note: note,
				})
			}
			e.Out.Printf("  Removed the credential for %s.\n", origin)
			if revoked {
				e.Out.Println("  The token is revoked, so a copy of it is no longer valid anywhere.")
			}
			if note != "" {
				e.Out.Println("")
				e.Out.Printf("  %s\n", note)
				e.Out.Println("  Revoke it in the control plane under Settings, tokens.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "",
		"The control plane to sign out of (default: AF_CONTROL_PLANE_URL, or the hosted instance)")
	return cmd
}

func newWhoamiCommand(e *Env) *cobra.Command {
	var baseURL string
	var offline bool

	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Who this machine is signed in as",
		Long: strings.TrimSpace(`
Asks the control plane who the stored token belongs to.

It asks rather than reading the stored copy, because the stored copy is what
this machine believed at login time and the control plane is what is true now.
A token whose membership has been removed still looks perfectly good on disk,
and reporting it would tell somebody they have access they do not have.

--offline reports the stored copy without a network call, and says so.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			origin := auth.Normalise(controlPlaneFor(e, baseURL))
			store := e.CredentialStore()

			cred, err := store.Load(origin)
			if errors.Is(err, auth.ErrNotSignedIn) {
				return aferrors.Coded(aferrors.AFCPL004, "origin", origin, "command", "af login")
			}
			if err != nil {
				return err
			}
			if cred.Expired(time.Now()) {
				return fmt.Errorf("the credential for %s expired on %s. Run: af login",
					origin, cred.ExpiresAt.Format(time.RFC3339))
			}

			out := WhoamiJSON{
				ControlPlane: origin,
				Login:        cred.Login,
				Organization: cred.Organization,
				Scopes:       cred.Scopes,
				StoredIn:     store.Location(origin),
				Source:       "the control plane",
			}
			if !cred.ExpiresAt.IsZero() {
				out.ExpiresAt = cred.ExpiresAt.Format(time.RFC3339)
			}

			if offline {
				out.Source = "this machine, without asking the control plane"
			} else {
				id, err := auth.NewClient(origin).Whoami(ctx, cred.Token)
				if errors.Is(err, auth.ErrNotSignedIn) {
					return fmt.Errorf(
						"%s no longer accepts this token. It may have been revoked, or you may have "+
							"been removed from %s. Run: af login", origin, cred.Organization)
				}
				if err != nil {
					return err
				}
				out.Login, out.Name = id.Login, id.Name
				out.Organization, out.Role = id.Organization, id.Role
				out.Scopes, out.TokenPrefix = id.Scopes, id.TokenPrefix
				if id.ExpiresAt != "" {
					out.ExpiresAt = id.ExpiresAt
				}
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(out)
			}
			e.Out.Printf("  %s in %s\n", out.Login, out.Organization)
			if out.Role != "" {
				e.Out.Printf("  role           %s\n", out.Role)
			}
			e.Out.Printf("  control plane  %s\n", out.ControlPlane)
			if len(out.Scopes) > 0 {
				e.Out.Printf("  scopes         %s\n", strings.Join(out.Scopes, ", "))
			}
			if out.ExpiresAt != "" {
				e.Out.Printf("  expires        %s\n", out.ExpiresAt)
			}
			e.Out.Printf("  credential     %s\n", out.StoredIn)
			if offline {
				e.Out.Println("")
				e.Out.Println("  Read from this machine without asking the control plane, so it says")
				e.Out.Println("  what this machine believed at login rather than what is true now.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&baseURL, "control-plane", "",
		"The control plane to ask (default: AF_CONTROL_PLANE_URL, or the hosted instance)")
	cmd.Flags().BoolVar(&offline, "offline", false,
		"Report the stored credential without asking the control plane")
	return cmd
}

// clientLabel is what the approval screen shows the person approving.
//
// The hostname and the user, because "a terminal" tells somebody nothing about
// whether the request is theirs. Somebody approving a login needs to recognise
// the machine.
func clientLabel(e *Env) string {
	host := e.Getenv("HOSTNAME")
	if host == "" {
		host = e.Getenv("COMPUTERNAME")
	}
	user := e.Getenv("USER")
	if user == "" {
		user = e.Getenv("USERNAME")
	}
	switch {
	case host != "" && user != "":
		return fmt.Sprintf("%s on %s", user, host)
	case host != "":
		return host
	case user != "":
		return user
	}
	return "a terminal"
}

// openBrowser is best effort and deliberately ignores its error.
//
// The whole point of the device grant is that it works where there is no
// browser, so failing to open one is not a failure. The address has already
// been printed by the time this runs.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
