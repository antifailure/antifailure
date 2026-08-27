package personas

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The escape hatch, for the authentication scheme nobody anticipated.
//
// The adapters above cover the schemes worth naming, and there will always be
// one they do not: a homegrown session table, an internal identity service, a
// framework that has not been written yet. Without a way out, the answer to
// all of those is "Antifailure does not work here", which is a bad answer to
// give somebody whose only difference from a supported project is a column
// name.
//
// So the manifest can name a command. It is run once per persona with the
// persona in the environment, against the branch that has just been created,
// and whatever it does is the provisioning. The contract is small and stated
// in the documentation: it must be idempotent, because it runs again on every
// branch, and it must exit non zero if it did not create the account.

// SeedAdapter provisions personas by running a command.
type SeedAdapter struct {
	command string
	dir     string
	// databaseURL is handed to the command, because the usual seed script is
	// one that writes rows and needs somewhere to write them.
	databaseURL secrets.Value
	timeout     time.Duration
	// environ is the base environment, injectable so a test does not inherit
	// the developer's shell.
	environ []string
}

// SeedOptions configure the escape hatch.
type SeedOptions struct {
	// Command is the shell command from the manifest.
	Command string
	// Dir is the working directory, normally the repository root.
	Dir string
	// DatabaseURL is the branch the command should write to.
	DatabaseURL secrets.Value
	// Timeout bounds one persona's run. A seed script that hangs would
	// otherwise hang the whole environment with no explanation.
	Timeout time.Duration
	// Environ overrides the inherited environment.
	Environ []string
}

// NewSeedAdapter returns an adapter that runs a command.
func NewSeedAdapter(opts SeedOptions) *SeedAdapter {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	environ := opts.Environ
	if environ == nil {
		environ = os.Environ()
	}
	return &SeedAdapter{
		command: opts.Command, dir: opts.Dir,
		databaseURL: opts.DatabaseURL, timeout: timeout, environ: environ,
	}
}

// Name identifies the adapter.
func (s *SeedAdapter) Name() string { return "seed" }

// Provision runs the command for one persona.
//
// The credentials go through the environment rather than the command line,
// because a command line is visible in the process table to every other user
// on the machine and an environment is not.
func (s *SeedAdapter) Provision(
	ctx context.Context, p schema.Persona, want Credentials,
) (*Account, error) {
	if strings.TrimSpace(s.command) == "" {
		return nil, fmt.Errorf("the seed adapter was selected and no command is configured")
	}

	account := &Account{
		Name: p.Name, Email: p.Email, Phone: p.Phone, Role: p.Role,
		Login: p.Login, Adapter: "seed",
	}
	if account.Login == "" {
		account.Login = schema.LoginPassword
	}
	if needsPassword(account.Login) {
		account.Password = want.Password
	}
	if p.MFA || account.Login == schema.LoginTOTP {
		account.TOTPSecret = want.TOTPSecret
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", s.command)
	cmd.Dir = s.dir
	cmd.Env = append(append([]string{}, s.environ...), s.environment(p, want)...)

	// The timeout has to bound the whole tree the command starts, not just
	// the shell. See seed_unix.go: killing the shell alone leaves a forked
	// grandchild holding the pipe this is reading, and Wait then blocks until
	// that grandchild finishes, which is exactly the hang the timeout exists
	// to prevent.
	isolateProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	// The backstop for the case the group kill cannot cover: a grandchild
	// that escaped into its own session still holds the pipe, and without a
	// delay Wait would keep waiting on it. After this, the pipes are closed
	// and Run returns whatever it has.
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// The script's own output is the only thing that explains what it
		// did, so it is carried rather than swallowed. It is not redacted
		// here because the caller redacts on the way to a report, and doing
		// it twice would hide what a person needs to debug their own script.
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf(
				"the seed command for persona %q did not finish within %s", p.Name, s.timeout)
		}
		return nil, fmt.Errorf("the seed command for persona %q failed: %v: %s",
			p.Name, err, detail)
	}

	// A script that knows the account's identifier can say so on stdout, and
	// one that does not simply prints nothing. Trimmed to one line because a
	// script that also logs would otherwise make its whole log the subject.
	if out := strings.TrimSpace(stdout.String()); out != "" {
		lines := strings.Split(out, "\n")
		account.Subject = strings.TrimSpace(lines[len(lines)-1])
	}
	return account, nil
}

// environment is what the seed command is told.
//
// Every name is prefixed AF_PERSONA_ except the database, so a script can tell
// at a glance which variables are ours and a variable we add later cannot
// collide with one the project already had.
func (s *SeedAdapter) environment(p schema.Persona, want Credentials) []string {
	login := string(p.Login)
	if login == "" {
		login = string(schema.LoginPassword)
	}
	env := []string{
		"AF_PERSONA_NAME=" + p.Name,
		"AF_PERSONA_EMAIL=" + p.Email,
		"AF_PERSONA_PHONE=" + p.Phone,
		"AF_PERSONA_ROLE=" + p.Role,
		"AF_PERSONA_LOGIN=" + login,
		"AF_PERSONA_ATTRIBUTES=" + jsonObject(p.Attributes),
	}
	if needsPassword(schema.LoginStrategy(login)) {
		env = append(env, "AF_PERSONA_PASSWORD="+want.Password.Reveal())
	}
	if p.MFA || login == string(schema.LoginTOTP) {
		env = append(env, "AF_PERSONA_TOTP_SECRET="+want.TOTPSecret.Reveal())
		env = append(env, "AF_PERSONA_MFA=1")
	}
	if url := s.databaseURL.Reveal(); url != "" {
		env = append(env, "AF_DATABASE_URL="+url, "DATABASE_URL="+url)
	}
	return env
}
