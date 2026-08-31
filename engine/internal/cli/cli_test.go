package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type result struct {
	code   int
	stdout string
	stderr string
}

// runCLI executes the command line with substituted streams, which is what
// makes every command testable without a process and without touching the
// real filesystem outside a temporary directory.
func runCLI(t *testing.T, workDir string, env map[string]string, args ...string) result {
	t.Helper()
	var out, errW bytes.Buffer
	code := cli.Execute(context.Background(), args, cli.Options{
		Stdout:  &out,
		Stderr:  &errW,
		Stdin:   strings.NewReader(""),
		Getenv:  func(k string) string { return env[k] },
		Clock:   clock.NewFake(epoch),
		WorkDir: workDir,
	})
	return result{code: code, stdout: out.String(), stderr: errW.String()}
}

func TestVersion_RendersInBothFormats(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	text := runCLI(t, dir, nil, "version")
	require.Zero(t, text.code)
	require.Contains(t, text.stdout, "antifailure")
	require.Contains(t, text.stdout, "community edition")

	short := runCLI(t, dir, nil, "version", "--short")
	require.Equal(t, "dev\n", short.stdout)

	js := runCLI(t, dir, nil, "version", "-o", "json")
	require.Zero(t, js.code)
	var info cli.VersionInfo
	require.NoError(t, json.Unmarshal([]byte(js.stdout), &info))
	require.Equal(t, "community", info.Edition)
	require.NotEmpty(t, info.Platform)
}

// Output must be stable for the same input. A timestamp or a duration in the
// default rendering would make every snapshot test and every diff meaningless.
func TestVersion_TextOutputIsStable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := runCLI(t, dir, nil, "version").stdout
	for i := 0; i < 5; i++ {
		require.Equal(t, first, runCLI(t, dir, nil, "version").stdout)
	}
}

// The four rendering modes the CLI has to be correct in. Escape codes in a CI
// log are the most common way a tool looks broken to someone who did not run
// it themselves.
func TestOutput_ColorRespectsTheEnvironment(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		env      map[string]string
		wantANSI bool
	}{
		"a buffer is not a terminal":  {nil, false},
		"NO_COLOR wins over anything": {map[string]string{"NO_COLOR": "1", "AF_FORCE_COLOR": "1"}, false},
		"a dumb terminal":             {map[string]string{"TERM": "dumb"}, false},
		"forced":                      {map[string]string{"AF_FORCE_COLOR": "1", "TERM": "xterm"}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := runCLI(t, t.TempDir(), tc.env, "doctor")
			hasANSI := strings.Contains(got.stdout, "\x1b[")
			require.Equal(t, tc.wantANSI, hasANSI, "%s: ANSI presence is wrong", name)
		})
	}
}

func TestOutput_NoColorFlagOverridesForcedColor(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), map[string]string{"AF_FORCE_COLOR": "1", "TERM": "xterm"},
		"doctor", "--no-color")
	require.NotContains(t, got.stdout, "\x1b[")
}

// Mixing prose into a JSON stream is how a script that pipes into jq breaks.
func TestOutput_JSONModeEmitsOnlyJSON(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), map[string]string{"AF_FORCE_COLOR": "1"}, "doctor", "-o", "json")
	require.NotContains(t, got.stdout, "\x1b[", "JSON mode must never carry escape codes")
	var report cli.DoctorReport
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &report),
		"JSON mode emitted something that is not JSON:\n%s", got.stdout)
}

func TestRoot_UnknownCommandIsAUsageError(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "nonsense")
	require.Equal(t, int(aferrors.ExitUsage), got.code)
}

func TestRoot_UnknownFlagIsAUsageError(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "version", "--nonsense")
	require.Equal(t, int(aferrors.ExitUsage), got.code)
}

func TestRoot_AnUnknownOutputFormatIsAUsageError(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "version", "-o", "xml")
	require.Equal(t, int(aferrors.ExitUsage), got.code)
}

func TestRoot_NoArgumentsPrintsHelp(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil)
	require.Zero(t, got.code, "af with no arguments is how people find out what it does")
	require.Contains(t, got.stdout, "af init")
	require.Contains(t, got.stdout, "af up")
}

func TestRoot_DirectoryFlagChangesWhereCommandsLook(t *testing.T) {
	t.Parallel()
	other := t.TempDir()
	got := runCLI(t, t.TempDir(), nil, "explain", "-C", other)
	require.Equal(t, int(aferrors.ExitConfiguration), got.code)
	require.Contains(t, got.stderr, other)
}

func TestRoot_DirectoryFlagRejectsANonDirectory(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	got := runCLI(t, t.TempDir(), nil, "version", "-C", f)
	require.Equal(t, int(aferrors.ExitUsage), got.code)
}

// Every command in the tree exists from the first release, including the ones
// whose engines have not landed. A command that says "not yet available" is
// honest; a missing one makes a user think they have the wrong version, and one
// that silently does nothing is the failure this product exists to prevent.
//
// The list is empty. It is kept, rather than deleted with the last entry,
// because the next command added is the next candidate for it, and because an
// empty list is the assertion: nothing in the tree is a placeholder.
func TestUnimplementedCommands_SayNotYetAvailableRatherThanPretending(t *testing.T) {
	t.Parallel()
	commands := [][]string{}

	for _, args := range commands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			got := runCLI(t, t.TempDir(), nil, args...)
			require.Equal(t, int(aferrors.ExitUsage), got.code,
				"af %s must exit 2", strings.Join(args, " "))
			require.Contains(t, got.stderr, "AF-RUN-001")
			require.Contains(t, got.stderr, "not available in this version")
			require.NotContains(t, strings.ToLower(got.stdout), "success")
		})
	}

	require.Empty(t, commands,
		"a command in this list is a placeholder; when the list empties, every command does something")
}

// af env pull is the one command that needs a control plane, so it is also the
// one that has to explain its absence rather than failing with a network error.
func TestEnvPull_SaysWhatIsMissingRatherThanFailingToConnect(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "env", "pull", "af-example")
	require.Equal(t, int(aferrors.ExitConfiguration), got.code)
	require.Contains(t, got.stderr, "AF-CPL-001")
	// The next step is to create a token, not to check the network.
	require.Contains(t, got.stderr, "AF_CONTROL_PLANE_TOKEN")
	// And it has to say that this is the only thing that needs one, so nobody
	// concludes the product requires a hosted service.
	require.Contains(t, strings.ToLower(got.stderr), "without one")
}

// A token must never be sent to a plain HTTP host that is not this machine.
func TestEnvPull_RefusesToSendATokenInTheClear(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"AF_CONTROL_PLANE_TOKEN": "aft_" + strings.Repeat("a", 40),
		"AF_CONTROL_PLANE_URL":   "http://control.example.com",
	}
	got := runCLI(t, t.TempDir(), env, "env", "pull", "af-example")
	require.NotZero(t, got.code)
	require.Contains(t, got.stderr, "not https")
	require.NotContains(t, got.stderr, "aaaaaaaa")
}

// An error message that names the problem and stops is the difference between
// a user fixing something in thirty seconds and a user opening an issue.
func TestError_RenderingCarriesTheCodeCauseAndNextStep(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "explain")
	require.Equal(t, int(aferrors.ExitConfiguration), got.code)
	require.Contains(t, got.stderr, "AF-MAN-001")
	require.Contains(t, got.stderr, "No antifailure.yaml was found")
	require.Contains(t, got.stderr, "Next:")
	require.Contains(t, got.stderr, "af init")
	require.Contains(t, got.stderr, "https://antifailure.dev/docs/")
}

func TestError_JSONFormCarriesTheSameInformation(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "explain", "-o", "json")
	require.Equal(t, int(aferrors.ExitConfiguration), got.code)
	var doc cli.ErrorJSON
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &doc))
	require.Equal(t, "AF-MAN-001", doc.Code)
	require.NotEmpty(t, doc.NextStep)
	require.NotEmpty(t, doc.Docs)
	require.Equal(t, int(aferrors.ExitConfiguration), doc.ExitCode)
}

func TestExplain_RendersTheEffectiveConfiguration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, `
version: 1
name: shop
services:
  - name: web
    port: 3000
egress:
  rules:
    - host: api.stripe.com
      mode: mock
`)
	got := runCLI(t, dir, nil, "explain")
	require.Zero(t, got.code, got.stderr)
	require.Contains(t, got.stdout, "Application  shop")
	require.Contains(t, got.stdout, "api.stripe.com")
	// The whole point: a default nobody set is shown with its resolved value.
	require.Contains(t, got.stdout, "default      block")
	require.Contains(t, got.stdout, "lifetime     24h")
}

func TestExplain_JSONFormIsTheNormalizedManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "version: 1\nname: shop\nservices:\n  - name: web\n    port: 3000\n")
	got := runCLI(t, dir, nil, "explain", "-o", "json")
	require.Zero(t, got.code, got.stderr)

	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &m))
	egress, ok := m["egress"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "block", egress["default"], "the JSON form carries resolved defaults too")
}

func TestExplain_ReportsAnInvalidManifestWithTheLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "version: 1\nname: shop\nservices:\n  - name: web\n    prot: 3000\n")
	got := runCLI(t, dir, nil, "explain")
	require.NotZero(t, got.code)
	require.Contains(t, got.stderr, "line 5")
	require.Contains(t, got.stderr, "prot")
	require.Contains(t, got.stderr, "port", "a near miss must be suggested")
}

func TestDoctor_ReportsEveryCheckWithARemediation(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "doctor", "-o", "json")
	var report cli.DoctorReport
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &report))
	require.NotEmpty(t, report.Checks)
	for _, c := range report.Checks {
		require.NotEmpty(t, c.Name)
		require.NotEmpty(t, c.Detail, "check %q reports no detail", c.Name)
		// A diagnostic that tells you something is wrong and stops is worse
		// than no diagnostic: it costs the same attention and yields nothing.
		require.NotEmpty(t, c.Remediation, "check %q names no remediation", c.Name)
	}
}

func TestDoctor_ExitsNonZeroWhenACheckFails(t *testing.T) {
	t.Parallel()
	// The disk check runs against the working directory, so a directory that
	// does not exist makes at least one check fail.
	missing := filepath.Join(t.TempDir(), "gone")
	var out, errW bytes.Buffer
	code := cli.Execute(context.Background(), []string{"doctor", "-o", "json"}, cli.Options{
		Stdout: &out, Stderr: &errW, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" },
		Clock:  clock.NewFake(epoch), WorkDir: missing,
	})
	var report cli.DoctorReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	if !report.OK {
		require.NotZero(t, code)
	}
}

func TestInit_WritesAValidManifestAndIgnoresTheStateDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "shopfront",
  "scripts": {"start": "next start"},
  "dependencies": {"next": "15.1.0", "stripe": "17.5.0", "resend": "4.0.1"}
}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env.example"),
		[]byte("DATABASE_URL=\nSTRIPE_SECRET_KEY=\n"), 0o600))

	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.Zero(t, got.code, got.stderr)

	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	// The header is what a reader who did not run the command sees first, and
	// the thing they most need to know is the default deny.
	require.Contains(t, string(body), "Everything else is refused")
	require.Contains(t, string(body), "name: shopfront",
		"the application is named after its package, not the checkout directory")
	require.Contains(t, string(body), "mode: sandbox")
	require.Contains(t, string(body), "mode: capture")

	// af init must never produce a file af up would then refuse.
	explained := runCLI(t, dir, nil, "explain")
	require.Zero(t, explained.code, explained.stderr)

	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	require.Contains(t, string(ignore), ".antifailure/")

	readme, err := os.ReadFile(filepath.Join(dir, ".antifailure", "README.md"))
	require.NoError(t, err)
	require.Contains(t, string(readme), "never holds a secret")
}

func TestInit_RefusesToOverwriteAnExistingManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"a","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`), 0o600))
	writeManifest(t, dir, "version: 1\nname: mine\nservices:\n  - name: web\n    port: 9999\n")

	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.NotZero(t, got.code)
	require.Contains(t, got.stderr, "already exists")

	// The user's edits are the most valuable thing in the file and detection
	// cannot reproduce them, so they must survive.
	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(body), "9999")
}

func TestInit_ReportsWhenNothingWasDetected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# nothing"), 0o600))
	got := runCLI(t, dir, nil, "init", "--non-interactive")
	require.NotZero(t, got.code)
	require.Contains(t, got.stderr, "AF-DET-001")
	require.Contains(t, got.stderr, "by hand")
}

// A question needs somewhere to ask it. Without a terminal the read blocks
// forever, which in CI looks exactly like a hang.
func TestInit_RefusesToPromptWithNoTerminal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"a","scripts":{"start":"node server.js"},"dependencies":{"pino":"9.0.0"}}`), 0o600))

	done := make(chan result, 1)
	go func() { done <- runCLI(t, dir, nil, "init") }()
	select {
	case got := <-done:
		require.NotZero(t, got.code)
		require.Contains(t, got.stderr, "AF-MAN-004")
		require.Contains(t, got.stderr, "non-interactive")
	case <-time.After(20 * time.Second):
		t.Fatal("af init blocked waiting for input that will never arrive")
	}
}

func TestInit_AnswerFlagAvoidsAPrompt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"a","scripts":{"start":"node server.js"},"dependencies":{"pino":"9.0.0"}}`), 0o600))

	got := runCLI(t, dir, nil, "init", "--answer", "service.a.port=4321", "--non-interactive")
	require.Zero(t, got.code, got.stderr)
	body, err := os.ReadFile(filepath.Join(dir, "antifailure.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(body), "port: 4321")
}

func TestInit_JSONFormReportsWhatWasAssumed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"shop","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`), 0o600))

	got := runCLI(t, dir, nil, "init", "-o", "json")
	require.Zero(t, got.code, got.stderr)
	var report cli.InitReport
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &report))
	require.Contains(t, report.Services, "shop")
	require.Positive(t, report.Findings)
	require.NotEmpty(t, report.Assumed, "a non interactive run must say what it guessed")
}

func TestInit_WritingIsAtomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"shop","scripts":{"start":"next start"},"dependencies":{"next":"15.0.0"}}`), 0o600))
	require.Zero(t, runCLI(t, dir, nil, "init", "--non-interactive").code)

	// A temporary file left behind would be committed by whoever runs git add.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), ".af-"),
			"an atomic write left %s behind", e.Name())
	}
}

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "antifailure.yaml"), []byte(body), 0o600))
}

// The community edition answers af license rather than saying the command does
// not exist. "Unknown command" reads as a broken install and sends somebody to
// the issue tracker; an answer reads as a product decision, which it is.
func TestLicense_TheCommunityEditionSaysItNeedsNoLicense(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "license", "status")
	require.Zero(t, got.code, "asking about the license is not an error")
	require.Contains(t, got.stdout, "community edition")
	require.Contains(t, got.stdout, "needs none")
	// It must not read as a trial or a crippled version, because it is neither.
	lower := strings.ToLower(got.stdout)
	require.NotContains(t, lower, "trial")
	require.NotContains(t, lower, "upgrade to unlock")
	require.Contains(t, lower, "does not expire")
}

func TestLicense_InstallRefusesRatherThanStoringAKeyItCannotUse(t *testing.T) {
	t.Parallel()
	// Storing a key this binary can never act on would leave somebody believing
	// their enterprise features are on, and they would find out during the
	// rollout they bought it for.
	got := runCLI(t, t.TempDir(), nil, "license", "install", "aflic_whatever.signature")
	require.Contains(t, got.stdout, "Nothing was stored")
	require.Contains(t, got.stdout, "enterprise binary")
	require.NotContains(t, got.stdout, "installed successfully")
}

func TestLicense_StatusInJSONNamesTheEdition(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "license", "status", "-o", "json")
	var doc cli.LicenseJSON
	require.NoError(t, json.Unmarshal([]byte(got.stdout), &doc))
	require.Equal(t, "community", doc.Edition)
	require.Equal(t, "none", doc.State)
	require.Empty(t, doc.Extensions, "the stock build has nothing registered at the extension points")
}

// af explain answers the question somebody has right after AF-SEC-001, which is
// not "which variables does this need" but "where would each one come from".
func TestExplain_SaysWhereEachSecretWouldComeFrom(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, `
version: 1
name: shop
services:
  - name: web
    kind: web
    command: npm start
    port: 3000
    env:
      - name: DATABASE_URL
      - name: STRIPE_SECRET_KEY
      - name: SENTRY_DSN
        required: false
      - name: NODE_ENV
        value: preview
egress:
  default: block
  rules:
    - host: api.stripe.com
      mode: sandbox
      credential: STRIPE_SECRET_KEY
`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"),
		[]byte("STRIPE_SECRET_KEY=sk_test_from_the_file\n"), 0o600))

	got := runCLI(t, dir, map[string]string{"DATABASE_URL": "postgres://from-shell"}, "explain")
	require.Zero(t, got.code)

	require.Contains(t, got.stdout, "DATABASE_URL")
	require.Contains(t, got.stdout, "this shell's environment")
	require.Contains(t, got.stdout, ".env")
	// A literal in the manifest is not a secret and says so.
	require.Contains(t, got.stdout, "the manifest")
	// Optional and missing read differently, or a warning looks like an error.
	require.Contains(t, got.stdout, "not set, and not required")
	// The single most surprising thing about how this works, said every time.
	require.Contains(t, got.stdout, "the service gets a marker")

	// And never a value, because this is printed on a terminal somebody may be
	// sharing and it goes into support bundles.
	require.NotContains(t, got.stdout, "sk_test_from_the_file")
	require.NotContains(t, got.stdout, "postgres://from-shell")
}

func TestExplain_SaysWhereItLookedWhenSomethingIsMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, `
version: 1
name: shop
services:
  - name: web
    kind: web
    command: npm start
    port: 3000
    env:
      - name: DATABASE_URL
`)
	got := runCLI(t, dir, nil, "explain")
	require.Zero(t, got.code, "explain reports; it does not fail on a missing variable")
	require.Contains(t, got.stdout, "not found")
	// Including the file that does not exist yet, which is very often where the
	// value belongs.
	require.Contains(t, got.stdout, ".env (not present)")
}

func TestExplain_WithNoDeclaredVariablesSaysNothingAboutSecrets(t *testing.T) {
	t.Parallel()
	// A section that is always there and always empty is noise, and noise in
	// the output of a command whose whole job is clarity is a bug.
	dir := t.TempDir()
	writeManifest(t, dir, `
version: 1
name: shop
services:
  - name: web
    kind: web
    command: npm start
    port: 3000
`)
	got := runCLI(t, dir, nil, "explain")
	require.Zero(t, got.code)
	require.NotContains(t, got.stdout, "Secrets")
}

// Execute uses the arguments it was given and never the process's own.
//
// Cobra falls back to os.Args when handed a nil slice, which for a function
// that takes an argument list is surprising, and inside a test binary means the
// test runner's flags reach the command tree. It cost a red build that pointed
// nowhere near the cause: adding a flag to this package's tests made an
// unrelated test start failing.
func TestExecute_IgnoresTheProcessArguments(t *testing.T) {
	t.Parallel()
	var out, errW bytes.Buffer
	code := cli.Execute(context.Background(), nil, cli.Options{
		Stdout: &out, Stderr: &errW, Stdin: strings.NewReader(""),
		Getenv: func(string) string { return "" },
		Clock:  clock.NewFake(epoch), WorkDir: t.TempDir(),
	})
	require.Zero(t, code,
		"a nil argument list picked up this test binary's flags instead of meaning none")
	require.Contains(t, out.String(), "af")
}

// -o means the same thing on every command.
//
// The regression: a local --output on `af ci` shadowed the persistent one, so
// -o meant "a file to write" there and "text or json" everywhere else.
// `af ci -o json` wrote the pull request comment to a file called `json`,
// silently, and the one command written for CI had no machine readable
// output.
func TestCI_DoesNotShadowTheGlobalOutputFlag(t *testing.T) {
	t.Parallel()
	help := runCLI(t, t.TempDir(), nil, "ci", "--help")
	require.Zero(t, help.code)

	require.Contains(t, help.stdout, "--report",
		"the report path needs a name of its own")
	require.Contains(t, help.stdout, "Output format: text or json",
		"-o has to still mean on af ci what it means everywhere else")

	// The local flag, if one is ever added back, must not take -o. The help
	// lists global flags separately, so a shorthand in the command's own
	// section is the thing to refuse.
	own, _, found := strings.Cut(help.stdout, "Global flags:")
	require.True(t, found, "the help separates a command's flags from the global ones")
	require.NotContains(t, own, "-o, --output",
		"af ci defines its own -o again, which is the collision this guards")
}
