package personas_test

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The end to end proof, and the reason it is worth its length.
//
// Everything else in this package proves that a row was written. That is not
// the claim the product makes. The claim is that an agent can sign in as a
// persona, and the only way to know is to create the persona with the real
// adapter, put a real application in front of it, and drive a real browser at
// it with the real runner.
//
// So this test does exactly that: a real Postgres, the real SQLAdapter, an
// application that verifies bcrypt hashes and TOTP codes the way an
// application does, Chromium through Playwright, and runner/src/main.ts
// invoked as a subprocess over the same JSON boundary the engine uses. What
// it asserts is the runner's own verdict.
//
// Each strategy gets its own application, which is not a shortcut. The runner
// always goes to /login, and a single page carrying both a "Send link" and a
// "Send code" button is ambiguous to the accessible name matcher, which is
// also true of a real login page. Applications pick one primary method.

// authApp is an application with users, sessions and a sign in page.
type authApp struct {
	conn     *pgx.Conn
	table    string
	strategy schema.LoginStrategy

	mu       sync.Mutex
	sessions map[string]string
	tokens   map[string]string
	codes    map[string]string
	pending  map[string]string
	messages []inboxMessage
	msgPath  string
}

// inboxMessage is the shape runner/src/inbox.ts reads.
type inboxMessage struct {
	Seq      int      `json:"seq"`
	At       string   `json:"at"`
	Provider string   `json:"provider"`
	Kind     string   `json:"kind"`
	To       []string `json:"to"`
	Subject  string   `json:"subject"`
	Text     string   `json:"text"`
	Link     string   `json:"link,omitempty"`
	Code     string   `json:"code,omitempty"`
}

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html><head><title>{{.Title}}</title></head><body>
<h1>{{.Title}}</h1>
{{if .Error}}<p role="alert">Invalid email or password. Please try again.</p>{{end}}
{{if .Note}}<p>{{.Note}}</p>{{end}}
<form method="post" action="{{.Action}}">
{{range .Fields}}
  <label for="{{.ID}}">{{.Label}}</label>
  <input id="{{.ID}}" name="{{.Name}}" type="{{.Type}}">
{{end}}
  <button type="submit">{{.Button}}</button>
</form>
</body></html>`))

type pageField struct{ ID, Label, Name, Type string }

type pageData struct {
	Title, Action, Button, Note string
	Error                       bool
	Fields                      []pageField
}

func (a *authApp) render(w http.ResponseWriter, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	require := pageTemplate.Execute(w, data)
	_ = require
}

// loginPage is what each strategy's sign in page looks like.
func (a *authApp) loginPage(bad bool) pageData {
	switch a.strategy {
	case schema.LoginMagicLink:
		return pageData{
			Title: "Sign in", Action: "/login", Button: "Send magic link", Error: bad,
			Fields: []pageField{{ID: "email", Label: "Email", Name: "email", Type: "email"}},
		}
	case schema.LoginEmailCode:
		return pageData{
			Title: "Sign in", Action: "/login", Button: "Send code", Error: bad,
			Fields: []pageField{{ID: "email", Label: "Email", Name: "email", Type: "email"}},
		}
	case schema.LoginSMSCode:
		return pageData{
			Title: "Sign in", Action: "/login", Button: "Send code", Error: bad,
			Fields: []pageField{{ID: "phone", Label: "Phone number", Name: "phone", Type: "tel"}},
		}
	default:
		return pageData{
			Title: "Sign in", Action: "/login", Button: "Sign in", Error: bad,
			Fields: []pageField{
				{ID: "email", Label: "Email", Name: "email", Type: "email"},
				{ID: "password", Label: "Password", Name: "password", Type: "password"},
			},
		}
	}
}

func (a *authApp) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			a.render(w, a.loginPage(false))
			return
		}
		_ = r.ParseForm()
		email := strings.TrimSpace(r.FormValue("email"))

		switch a.strategy {
		case schema.LoginMagicLink:
			token := fmt.Sprintf("mlk-%d", time.Now().UnixNano())
			a.remember(&a.tokens, token, email)
			a.send(email, "Sign in to the app",
				"Use this link to sign in.", "http://"+r.Host+"/magic?token="+token, "")
			a.render(w, pageData{Title: "Check your email",
				Note:   "We have sent you a link. Open it to finish signing in.",
				Action: "/login", Button: "Send magic link"})
			return

		case schema.LoginSMSCode:
			number := strings.TrimSpace(r.FormValue("phone"))
			code := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
			a.remember(&a.codes, number, code)
			// Addressed to the number, which is what a captured text is
			// addressed to and the whole reason personas carry one.
			a.sendTo("sms", number, "", "Your code is "+code+".", "", code)
			a.render(w, pageData{
				Title: "Enter your code", Action: "/code", Button: "Sign in",
				Note:   "We texted you a verification code.",
				Fields: []pageField{{ID: "code", Label: "Code", Name: "code", Type: "text"}},
			})
			return

		case schema.LoginEmailCode:
			code := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
			a.remember(&a.codes, email, code)
			a.send(email, "Your sign in code",
				"Your verification code is "+code+".", "", code)
			a.render(w, pageData{
				Title: "Enter your code", Action: "/code", Button: "Sign in",
				Note:   "We emailed you a verification code.",
				Fields: []pageField{{ID: "code", Label: "Code", Name: "code", Type: "text"}},
			})
			return
		}

		// Password, with or without a second factor.
		id, hash, secret, ok := a.lookup(r.Context(), email)
		if !ok || bcrypt.CompareHashAndPassword([]byte(hash), []byte(r.FormValue("password"))) != nil {
			w.WriteHeader(http.StatusUnauthorized)
			a.render(w, a.loginPage(true))
			return
		}
		if a.strategy == schema.LoginTOTP && secret != "" {
			ticket := fmt.Sprintf("mfa-%d", time.Now().UnixNano())
			a.remember(&a.pending, ticket, id)
			http.SetCookie(w, &http.Cookie{Name: "pending", Value: ticket, Path: "/"})
			a.render(w, pageData{
				Title: "Two factor authentication", Action: "/challenge", Button: "Sign in",
				Note:   "Enter the verification code from your authenticator app.",
				Fields: []pageField{{ID: "code", Label: "Verification code", Name: "code", Type: "text"}},
			})
			return
		}
		a.signIn(w, r, id)
	})

	mux.HandleFunc("/challenge", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		cookie, err := r.Cookie("pending")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		a.mu.Lock()
		id := a.pending[cookie.Value]
		a.mu.Unlock()

		_, _, secret, ok := a.lookupByID(r.Context(), id)
		if !ok || !personas.TOTPValid(secret, strings.TrimSpace(r.FormValue("code")), time.Now()) {
			w.WriteHeader(http.StatusUnauthorized)
			a.render(w, pageData{
				Title: "Two factor authentication", Action: "/challenge", Button: "Sign in",
				Error: true, Note: "Enter the verification code from your authenticator app.",
				Fields: []pageField{{ID: "code", Label: "Verification code", Name: "code", Type: "text"}},
			})
			return
		}
		a.signIn(w, r, id)
	})

	mux.HandleFunc("/code", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		submitted := strings.TrimSpace(r.FormValue("code"))
		a.mu.Lock()
		var recipient string
		for addr, code := range a.codes {
			if code == submitted {
				recipient = addr
			}
		}
		a.mu.Unlock()
		if recipient == "" {
			w.WriteHeader(http.StatusUnauthorized)
			a.render(w, pageData{Title: "Enter your code", Action: "/code", Button: "Sign in",
				Error:  true,
				Fields: []pageField{{ID: "code", Label: "Code", Name: "code", Type: "text"}}})
			return
		}
		id, ok := a.lookupRecipient(r.Context(), recipient)
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			a.render(w, pageData{Title: "Enter your code", Action: "/code", Button: "Sign in",
				Error:  true,
				Fields: []pageField{{ID: "code", Label: "Code", Name: "code", Type: "text"}}})
			return
		}
		a.signIn(w, r, id)
	})

	mux.HandleFunc("/magic", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		email := a.tokens[r.URL.Query().Get("token")]
		a.mu.Unlock()
		if email == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		id, _, _, _ := a.lookup(r.Context(), email)
		a.signIn(w, r, id)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		a.mu.Lock()
		id := a.sessions[cookie.Value]
		a.mu.Unlock()
		if id == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html><head><title>Dashboard</title></head><body>
			<h1>Dashboard</h1><p>Welcome back.</p>
			<a href="/logout">Sign out</a></body></html>`)
	})

	return mux
}

func (a *authApp) signIn(w http.ResponseWriter, r *http.Request, id string) {
	token := fmt.Sprintf("ses-%d", time.Now().UnixNano())
	a.remember(&a.sessions, token, id)
	http.SetCookie(w, &http.Cookie{Name: "session", Value: token, Path: "/"})
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (a *authApp) remember(m *map[string]string, key, value string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if *m == nil {
		*m = map[string]string{}
	}
	(*m)[key] = value
}

// send records an email the way capture mode records one.
func (a *authApp) send(to, subject, text, link, code string) {
	a.sendTo("email", to, subject, text, link, code)
}

// sendTo records a message of any kind, addressed to whatever that kind is
// addressed to: an address for mail, a number for a text.
func (a *authApp) sendTo(kind, to, subject, text, link, code string) {
	a.mu.Lock()
	a.messages = append(a.messages, inboxMessage{
		Seq: len(a.messages) + 1, At: time.Now().UTC().Format(time.RFC3339),
		Provider: "capture", Kind: kind, To: []string{to},
		Subject: subject, Text: text, Link: link, Code: code,
	})
	// Newest first, which is the order the real inbox lists in.
	out := make([]inboxMessage, len(a.messages))
	for i, m := range a.messages {
		out[len(a.messages)-1-i] = m
	}
	encoded, _ := json.Marshal(out)
	path := a.msgPath
	a.mu.Unlock()
	_ = os.WriteFile(path, encoded, 0o600)
}

func (a *authApp) lookup(ctx context.Context, email string) (id, hash, secret string, ok bool) {
	err := a.conn.QueryRow(ctx, fmt.Sprintf(
		`SELECT id::text, coalesce(password,''), coalesce(totp_secret,'')
		 FROM %s WHERE lower(email) = lower($1)`, a.table), email).Scan(&id, &hash, &secret)
	return id, hash, secret, err == nil
}

// lookupRecipient finds the account a code was sent to, by address or number.
func (a *authApp) lookupRecipient(ctx context.Context, recipient string) (string, bool) {
	var id string
	err := a.conn.QueryRow(ctx, fmt.Sprintf(
		`SELECT id::text FROM %s
		 WHERE lower(email) = lower($1) OR phone = $1`, a.table), recipient).Scan(&id)
	return id, err == nil
}

func (a *authApp) lookupByID(ctx context.Context, id string) (string, string, string, bool) {
	var hash, secret string
	err := a.conn.QueryRow(ctx, fmt.Sprintf(
		`SELECT coalesce(password,''), coalesce(totp_secret,'')
		 FROM %s WHERE id::text = $1`, a.table), id).Scan(&hash, &secret)
	return id, hash, secret, err == nil
}

// runnerReport is the document runner/src/main.ts writes.
type runnerReport struct {
	Results []struct {
		Workflow string `json:"workflow"`
		Outcome  struct {
			Verdict string `json:"verdict"`
			Cause   string `json:"cause"`
			Detail  string `json:"detail"`
		} `json:"outcome"`
		Steps []string `json:"steps"`
	} `json:"results"`
	Passed     int `json:"passed"`
	Failed     int `json:"failed"`
	Blocked    int `json:"blocked"`
	Flaky      int `json:"flaky"`
	Unverified int `json:"unverified"`
}

// signInEndToEnd provisions a persona, serves an application, and drives the
// real runner at it. It returns the runner's own report.
func signInEndToEnd(t *testing.T, strategy schema.LoginStrategy, mfa bool) runnerReport {
	return signInEndToEndWith(t, strategy, mfa, nil)
}

// signInEndToEndWith allows the test to interfere with what provisioning
// produced, which is how the negative control below proves this harness is
// measuring something.
func signInEndToEndWith(
	t *testing.T, strategy schema.LoginStrategy, mfa bool,
	sabotage func(ctx context.Context, conn *pgx.Conn, table string),
) runnerReport {
	t.Helper()
	if testing.Short() {
		// These start a browser and take about half a minute each, which is
		// the honest cost of proving a sign in rather than asserting one.
		t.Skip("skipped: -short")
	}
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)
	if _, err := os.Stat(filepath.Join(repoRoot, "runner", "node_modules", "playwright")); err != nil {
		// Named rather than silent, because a skip that reads as a pass is
		// how a suite stops proving anything without anybody noticing.
		//
		// To run these:
		//
		//	npm --prefix runner install
		//	npx --prefix runner playwright install chromium
		//	docker run -d --name af-cp-test -p 55432:5432 \
		//	  -e POSTGRES_PASSWORD=test -e POSTGRES_DB=antifailure postgres:17-alpine
		//
		// install rather than ci, and that is not a slip: runner/.gitignore
		// ignores package-lock.json, so a fresh clone has no lockfile and
		// `npm ci` refuses outright. CI's runner job uses install for the same
		// reason. This comment said ci until somebody ran it on a clone that
		// had never installed anything.
		t.Skip("skipped: the runner's dependencies are not installed " +
			"(npm --prefix runner install)")
	}

	table := "e2e_users_" + strings.ReplaceAll(string(strategy), "_", "")
	_, err = conn.Exec(ctx, fmt.Sprintf(`
		DROP TABLE IF EXISTS %s;
		CREATE TABLE %s (
		  id          bigserial PRIMARY KEY,
		  email       text NOT NULL UNIQUE,
		  password    text,
		  phone       text,
		  totp_secret text,
		  role        text,
		  created_at  timestamptz,
		  updated_at  timestamptz
		);
		CREATE TABLE IF NOT EXISTS %s_sessions (
		  token   text PRIMARY KEY,
		  user_id bigint NOT NULL
		)`, table, table, table))
	require.NoError(t, err)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = conn.Exec(c, fmt.Sprintf("DROP TABLE IF EXISTS %s, %s_sessions", table, table))
	})

	// The persona is created by the real adapter, hashing with the real
	// bcrypt cost and enrolling the real TOTP secret.
	scheme := personas.GenericScheme(personas.Table{
		Name: table, ID: "id", Email: "email", Password: "password",
		Phone: "phone", Role: "role",
		Timestamps: []string{"created_at", "updated_at"},
	}, []string{table + "_sessions"})
	// The second factor lives in a column on the same row for this
	// application, which is what an application that rolled its own MFA does.
	scheme.Factors = &personas.Table{
		Schema: "public", Name: table, ID: "id", Password: "totp_secret",
	}

	p := schema.Persona{
		Name: "owner", Email: "owner@example.test", Role: "admin",
		Login: strategy, MFA: mfa,
	}
	if strategy == schema.LoginSMSCode {
		// The reserved fictional block, the same one normalizePersonas
		// assigns from, so this exercises the number a real manifest gets.
		p.Phone = "+15550100"
	}
	deriver := personas.NewDeriver("e2e-"+string(strategy), personas.PasswordPolicy{})
	adapter := personas.NewSQLAdapter(conn, scheme, "Antifailure")

	result, err := personas.Provision(ctx, adapter, deriver, []schema.Persona{p})
	require.NoError(t, err)
	account := result.Accounts[0]

	// The factor table is the users table here, so enrolment updated the row
	// rather than inserting. Confirm the secret is where the application will
	// look for it, because a secret written somewhere else is a sign in that
	// fails for a reason nobody can see.
	if mfa || strategy == schema.LoginTOTP {
		var stored string
		require.NoError(t, conn.QueryRow(ctx,
			fmt.Sprintf("SELECT coalesce(totp_secret,'') FROM %s WHERE id::text = $1", table),
			account.Subject).Scan(&stored))
		require.Equal(t, account.TOTPSecret.Reveal(), stored)
	}

	if sabotage != nil {
		sabotage(ctx, conn, table)
	}

	work := t.TempDir()
	app := &authApp{
		conn: conn, table: table, strategy: strategy,
		msgPath: filepath.Join(work, "messages.json"),
	}
	require.NoError(t, os.WriteFile(app.msgPath, []byte("[]"), 0o600))

	server := httptest.NewServer(app.handler())
	defer server.Close()

	// A stand in for `af inbox list`, which is the only thing the runner uses
	// the engine binary for. The real one returns the messages the egress
	// proxy captured; this returns the ones the application sent. The
	// contract between them is the JSON, and that is what is being exercised.
	afPath := filepath.Join(work, "af")
	require.NoError(t, os.WriteFile(afPath,
		[]byte("#!/bin/sh\ncat "+app.msgPath+"\n"), 0o755))

	job := map[string]any{
		"base_url":  server.URL,
		"artifacts": filepath.Join(work, "artifacts"),
		"af":        afPath,
		"work_dir":  work,
		"headless":  true,
		"attempts":  1,
		"workflows": []map[string]any{{
			"name":        "sign-in",
			"description": "Sign in and reach the dashboard.",
			"persona":     "owner",
			"expect":      []string{"the dashboard is shown"},
			"startPath":   "/dashboard",
		}},
		"personas": []map[string]any{{
			"name":       account.Name,
			"email":      account.Email,
			"role":       account.Role,
			"login":      string(account.Login),
			"phone":      account.Phone,
			"password":   account.Password.Reveal(),
			"totpSecret": account.TOTPSecret.Reveal(),
		}},
	}
	body, err := json.Marshal(job)
	require.NoError(t, err)

	runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "node", "--experimental-strip-types",
		filepath.Join(repoRoot, "runner", "src", "main.ts"))
	cmd.Stdin = strings.NewReader(string(body))
	cmd.Dir = repoRoot
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	require.NotEmpty(t, stdout.String(),
		"the runner produced nothing. Its output was: %s", stderr.String())

	var report runnerReport
	require.NoError(t, json.Unmarshal([]byte(stdout.String()), &report),
		"the runner's output could not be read: %s", stdout.String())
	_ = runErr
	return report
}

// requireSignedIn asserts the runner got in, and says why it did not when it
// did not, because "failed" with no detail is a test nobody can act on.
func requireSignedIn(t *testing.T, report runnerReport) {
	t.Helper()
	require.NotEmpty(t, report.Results)
	outcome := report.Results[0].Outcome
	require.Equalf(t, 0, report.Blocked,
		"the run was blocked, which means the environment did not hold up its end: %s (%s)\nsteps: %v",
		outcome.Detail, outcome.Cause, report.Results[0].Steps)

	signedIn := false
	for _, step := range report.Results[0].Steps {
		if strings.Contains(step, "Sign in as owner") && strings.Contains(step, "Signed in as owner") {
			signedIn = true
		}
	}
	require.Truef(t, signedIn,
		"the runner did not sign in. Outcome: %s (%s) %s\nsteps: %v",
		outcome.Verdict, outcome.Cause, outcome.Detail, report.Results[0].Steps)
}

func TestAPersonaCanSignInWithAPassword(t *testing.T) {
	requireSignedIn(t, signInEndToEnd(t, schema.LoginPassword, false))
}

func TestAPersonaCanSignInWithAMagicLink(t *testing.T) {
	// The link was really emailed, which is exactly the link an agent cannot
	// read, so it comes back through the captured inbox instead.
	requireSignedIn(t, signInEndToEnd(t, schema.LoginMagicLink, false))
}

func TestAPersonaCanSignInWithAnEmailedCode(t *testing.T) {
	requireSignedIn(t, signInEndToEnd(t, schema.LoginEmailCode, false))
}

func TestAPersonaCanSignInWithATimeBasedCode(t *testing.T) {
	// The join this proves is the one a TOTP integration usually gets wrong:
	// the engine derives a secret, writes it where the application reads it,
	// and the runner independently derives the code the application then
	// verifies. Three separate pieces of arithmetic that have to agree.
	requireSignedIn(t, signInEndToEnd(t, schema.LoginTOTP, true))
}

// TestTheEndToEndProofCanActuallyFail is the negative control.
//
// Four tests above assert that a persona signs in. If the harness were wrong
// in the direction of always passing, all four would still be green and would
// prove nothing, which is the failure mode the database conformance suite's
// own comments describe: a filter that matched nothing turned the leak
// detector into a switch that was permanently off, and only a negative control
// found it.
//
// So: provision the persona properly, then overwrite the stored hash with one
// for a different password, and require that the runner notices. If this test
// ever passes for the wrong reason, the four above are worthless.
func TestTheEndToEndProofCanActuallyFail(t *testing.T) {
	report := signInEndToEndWith(t, schema.LoginPassword, false,
		func(ctx context.Context, conn *pgx.Conn, table string) {
			other, err := bcrypt.GenerateFromPassword([]byte("a different password"), personas.BcryptCost)
			require.NoError(t, err)
			_, err = conn.Exec(ctx,
				fmt.Sprintf("UPDATE %s SET password = $1", table), string(other))
			require.NoError(t, err)
		})

	require.NotEmpty(t, report.Results)
	steps := report.Results[0].Steps
	for _, step := range steps {
		require.NotContainsf(t, step, "Signed in as owner",
			"the runner reported signing in with a password that does not match the stored hash, "+
				"so the four proofs above are not measuring anything. Steps: %v", steps)
	}

	// And the failure is charged to the application rather than to the
	// environment, which is correct here and is the distinction that matters:
	// the environment did its job, and the application refused a password.
	// The bug this whole lane exists to fix was that a persona which had
	// never been created produced this same verdict.
	require.Equal(t, 0, report.Passed)
}

func TestAPersonaCanSignInWithATextedCode(t *testing.T) {
	// A text is addressed to a number, not to an address. Before personas
	// carried a phone number, this strategy waited on the persona's email for
	// a message the application had sent to a handset, which could never
	// match: sms_code was declared in the manifest schema, listed in the
	// documentation, and impossible to use.
	requireSignedIn(t, signInEndToEnd(t, schema.LoginSMSCode, false))
}
