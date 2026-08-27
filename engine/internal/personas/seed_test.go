package personas_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func seedPersona() schema.Persona {
	return schema.Persona{
		Name: "owner", Email: "owner@example.test", Role: "admin",
		Login: schema.LoginPassword, Attributes: map[string]string{"plan": "pro"},
	}
}

func TestTheSeedCommandIsToldEverythingItNeedsToCreateTheAccount(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "seen.txt")

	// A script that records its environment, which is exactly what a real
	// seed script reads to do its work.
	a := personas.NewSeedAdapter(personas.SeedOptions{
		Command:     "env | grep -E '^(AF_PERSONA_|AF_DATABASE_URL|DATABASE_URL)' | sort > " + out,
		Dir:         dir,
		DatabaseURL: secrets.New("postgres://u:p@localhost:5432/branch"),
		Environ:     []string{"PATH=" + os.Getenv("PATH")},
	})
	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})

	got, err := personas.Provision(context.Background(), a, d, []schema.Persona{seedPersona()})
	require.NoError(t, err)
	require.Equal(t, "seed", got.Accounts[0].Adapter)

	body, err := os.ReadFile(out)
	require.NoError(t, err)
	seen := string(body)

	require.Contains(t, seen, "AF_PERSONA_NAME=owner")
	require.Contains(t, seen, "AF_PERSONA_EMAIL=owner@example.test")
	require.Contains(t, seen, "AF_PERSONA_ROLE=admin")
	require.Contains(t, seen, "AF_PERSONA_LOGIN=password")
	require.Contains(t, seen, `AF_PERSONA_ATTRIBUTES={"plan":"pro"}`)
	require.Contains(t, seen, "AF_DATABASE_URL=postgres://u:p@localhost:5432/branch")

	// The password the script is given is the password the runner will type.
	// If these two ever came from different derivations the account would be
	// created with one and signed into with another, and the failure would
	// look like the application refusing a correct password.
	require.Contains(t, seen, "AF_PERSONA_PASSWORD="+got.Accounts[0].Password.Reveal())
}

func TestASecondFactorIsOnlyOfferedToAPersonaThatAskedForOne(t *testing.T) {
	dir := t.TempDir()
	run := func(p schema.Persona) string {
		out := filepath.Join(dir, strings.ReplaceAll(p.Name, " ", "")+".txt")
		a := personas.NewSeedAdapter(personas.SeedOptions{
			Command: "env | grep '^AF_PERSONA_' | sort > " + out,
			Dir:     dir, Environ: []string{"PATH=" + os.Getenv("PATH")},
		})
		d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
		_, err := personas.Provision(context.Background(), a, d, []schema.Persona{p})
		require.NoError(t, err)
		body, err := os.ReadFile(out)
		require.NoError(t, err)
		return string(body)
	}

	plain := seedPersona()
	require.NotContains(t, run(plain), "AF_PERSONA_TOTP_SECRET",
		"a persona with no second factor was handed a secret, which invites a "+
			"seed script to enrol one nobody asked for")

	withMFA := seedPersona()
	withMFA.Name = "secured"
	withMFA.MFA = true
	seen := run(withMFA)
	require.Contains(t, seen, "AF_PERSONA_TOTP_SECRET=")
	require.Contains(t, seen, "AF_PERSONA_MFA=1")
}

func TestTheCredentialsGoThroughTheEnvironmentAndNotTheCommandLine(t *testing.T) {
	// A command line is visible in the process table to every other user on
	// the machine. An environment is not. This is the one place the password
	// crosses a process boundary, so it is worth a test of its own rather
	// than a comment.
	dir := t.TempDir()
	out := filepath.Join(dir, "cmdline.txt")

	a := personas.NewSeedAdapter(personas.SeedOptions{
		// "$0 $@" is how the shell was invoked, which is what would show in
		// the process table.
		Command: "echo \"$0 $*\" > " + out,
		Dir:     dir, Environ: []string{"PATH=" + os.Getenv("PATH")},
	})
	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	got, err := personas.Provision(context.Background(), a, d, []schema.Persona{seedPersona()})
	require.NoError(t, err)

	body, err := os.ReadFile(out)
	require.NoError(t, err)
	require.NotContains(t, string(body), got.Accounts[0].Password.Reveal())
}

func TestAFailingSeedCommandSaysWhatItPrinted(t *testing.T) {
	// The script's own output is the only thing that explains what it did,
	// and a seed step that fails with "exit status 1" is a support ticket.
	a := personas.NewSeedAdapter(personas.SeedOptions{
		Command: "echo 'no such table: users' >&2; exit 1",
		Dir:     t.TempDir(), Environ: []string{"PATH=" + os.Getenv("PATH")},
	})
	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	_, err := personas.Provision(context.Background(), a, d, []schema.Persona{seedPersona()})

	require.Error(t, err)
	require.Contains(t, err.Error(), "owner", "the message does not say which persona failed")
	require.Contains(t, err.Error(), "no such table: users")
}

func TestASeedCommandThatHangsIsCutOffWithAnExplanation(t *testing.T) {
	// Without a bound, a seed script waiting on something that will never
	// arrive hangs the whole environment with no output at all.
	a := personas.NewSeedAdapter(personas.SeedOptions{
		Command: "sleep 30",
		Dir:     t.TempDir(), Timeout: 300 * time.Millisecond,
		Environ: []string{"PATH=" + os.Getenv("PATH")},
	})
	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})

	started := time.Now()
	_, err := personas.Provision(context.Background(), a, d, []schema.Persona{seedPersona()})
	require.Error(t, err)
	require.Less(t, time.Since(started), 10*time.Second)
	require.Contains(t, err.Error(), "did not finish within")
}

func TestASeedAdapterWithNoCommandSaysSoRatherThanSucceedingQuietly(t *testing.T) {
	// Reporting success here would mean the runner is told about an account
	// that was never created, which is the failure this whole package exists
	// to remove.
	a := personas.NewSeedAdapter(personas.SeedOptions{Dir: t.TempDir()})
	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	_, err := personas.Provision(context.Background(), a, d, []schema.Persona{seedPersona()})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no command is configured")
}

func TestTheSeedCommandCanReportTheAccountItCreated(t *testing.T) {
	// A script that knows the identifier says so on its last line, and one
	// that does not simply prints nothing. Taking the last line rather than
	// the whole of stdout means a script that also logs does not make its
	// log the subject.
	a := personas.NewSeedAdapter(personas.SeedOptions{
		Command: "echo 'creating owner...'; echo 'usr_42'",
		Dir:     t.TempDir(), Environ: []string{"PATH=" + os.Getenv("PATH")},
	})
	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	got, err := personas.Provision(context.Background(), a, d, []schema.Persona{seedPersona()})
	require.NoError(t, err)
	require.Equal(t, "usr_42", got.Accounts[0].Subject)
}

func TestAPolicyTheGeneratorCannotSatisfyFailsBeforeTheSignIn(t *testing.T) {
	// The failure this prevents is a quiet one: the adapter writes a hash for
	// a password the application will refuse, the agent types it, and the run
	// reports the application rejecting a correct password. Failing here names
	// the real cause, which is the manifest.
	a := personas.NewSeedAdapter(personas.SeedOptions{
		Command: "true", Dir: t.TempDir(), Environ: []string{"PATH=" + os.Getenv("PATH")},
	})
	// A policy that forbids every character the generator has to build with.
	// There is no password to produce here, and the honest answer is to say
	// so rather than to hand the adapter something that cannot work.
	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{
		MinLength: 60,
		Forbid:    "abcdefghijklmnopqrstuvwxyzABCDEF0123456789-!#$%*?@",
	})

	_, err := personas.Provision(context.Background(), a, d, []schema.Persona{seedPersona()})
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not satisfy auth.password")
	require.Contains(t, err.Error(), "owner")
}

func TestAStricterPolicyIsSatisfiedRatherThanRefused(t *testing.T) {
	// A policy the generator can meet is met, by shaping the password rather
	// than by failing. Otherwise every stricter application would be unusable.
	a := personas.NewSeedAdapter(personas.SeedOptions{
		Command: "true", Dir: t.TempDir(), Environ: []string{"PATH=" + os.Getenv("PATH")},
	})
	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{MinLength: 40, Forbid: "!"})

	got, err := personas.Provision(context.Background(), a, d, []schema.Persona{seedPersona()})
	require.NoError(t, err)

	password := got.Accounts[0].Password.Reveal()
	require.GreaterOrEqual(t, len(password), 40)
	require.NotContains(t, password, "!")
}

func TestPaddingNeverUsesAForbiddenCharacter(t *testing.T) {
	// Padding to a minimum length with a fixed letter produces a password
	// that fails the very rule the padding was applied to satisfy, for
	// anybody whose policy happens to forbid that one letter. It shows up
	// only for them, and it shows up as the application refusing a correct
	// password.
	a := personas.NewSeedAdapter(personas.SeedOptions{
		Command: "true", Dir: t.TempDir(), Environ: []string{"PATH=" + os.Getenv("PATH")},
	})
	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{MinLength: 48, Forbid: "x"})

	got, err := personas.Provision(context.Background(), a, d, []schema.Persona{seedPersona()})
	require.NoError(t, err)

	password := got.Accounts[0].Password.Reveal()
	require.GreaterOrEqual(t, len(password), 48)
	require.NotContains(t, password, "x")
}
