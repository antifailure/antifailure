package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/cli"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/pkg/edition"
)

// af --version is the first thing typed after an installer finishes, and it
// answered "unknown flag" while af version answered correctly. These tests hold
// the two spellings to one answer.
//
// Each assertion is its own test rather than a run of require calls in one,
// because require stops the function at the first failure and a later
// assertion in the same body can never be seen to go red. One break, one test.

// runVersion is the two spellings, given identical trailing arguments.
func runVersion(t *testing.T, args ...string) (flagSpelling, subcommand result) {
	t.Helper()
	dir := t.TempDir()
	return runCLI(t, dir, nil, append([]string{"--version"}, args...)...),
		runCLI(t, dir, nil, append([]string{"version"}, args...)...)
}

func TestVersionFlag_AnswersAtAllRatherThanRefusingTheFlag(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "--version")
	require.Zero(t, got.code, "af --version exited non zero: %s", got.stderr)
	require.NotContains(t, got.stderr, "unknown flag",
		"af --version still reports the flag as unknown, which is the defect")
	require.Contains(t, got.stdout, "antifailure")
}

func TestVersionFlag_TextIsByteForByteTheSubcommand(t *testing.T) {
	t.Parallel()
	flagSpelling, subcommand := runVersion(t)
	require.Equal(t, subcommand.stdout, flagSpelling.stdout)
}

func TestVersionFlag_JSONIsByteForByteTheSubcommand(t *testing.T) {
	t.Parallel()
	flagSpelling, subcommand := runVersion(t, "-o", "json")
	require.Equal(t, subcommand.stdout, flagSpelling.stdout)
}

// The JSON shape, not just the bytes. Two spellings could agree on a
// malformed object and this says which object it is.
func TestVersionFlag_JSONIsTheSameVersionInfoObject(t *testing.T) {
	t.Parallel()
	flagSpelling, subcommand := runVersion(t, "-o", "json")

	var fromFlag, fromSubcommand cli.VersionInfo
	require.NoError(t, json.Unmarshal([]byte(flagSpelling.stdout), &fromFlag))
	require.NoError(t, json.Unmarshal([]byte(subcommand.stdout), &fromSubcommand))
	require.Equal(t, fromSubcommand, fromFlag)
	require.NotEmpty(t, fromFlag.Platform)
	require.NotEmpty(t, fromFlag.Edition)
}

func TestVersionFlag_ShortIsByteForByteTheSubcommand(t *testing.T) {
	t.Parallel()
	flagSpelling, subcommand := runVersion(t, "--short")
	require.Equal(t, subcommand.stdout, flagSpelling.stdout)
	require.Equal(t, "dev\n", flagSpelling.stdout)
}

// The assertion that matters.
//
// cobra's built in --version prints a template from a package variable. That
// variable is never stamped by any build, so a --version implemented on it
// would print "community" out of an enterprise binary, which is the defect
// engine/internal/cli/commands.go describes having already shipped once in
// af version. Reaching the other branch means attaching what the enterprise
// binary attaches at startup, which is what this does.
func TestVersionFlag_ReportsTheEditionTheBinaryDeclared(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	ctx := edition.With(context.Background(), edition.Status{Name: "enterprise", State: "active"})
	code := cli.Execute(ctx, []string{"--version", "-o", "json"}, cli.Options{
		Stdout:  &out,
		Stderr:  &bytes.Buffer{},
		Stdin:   strings.NewReader(""),
		Getenv:  func(string) string { return "" },
		Clock:   clock.NewFake(epoch),
		WorkDir: t.TempDir(),
	})
	require.Zero(t, code)

	var info cli.VersionInfo
	require.NoError(t, json.Unmarshal(out.Bytes(), &info))
	require.Equal(t, "enterprise", info.Edition,
		"af --version read the edition from a build time variable rather than from the running binary")
}

// The text rendering, for the same reason. A JSON only assertion would pass a
// --version whose human readable form still printed the package variable, and
// the human readable form is the one an auditor reads.
func TestVersionFlag_TextRenderingCarriesTheDeclaredEdition(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	ctx := edition.With(context.Background(), edition.Status{Name: "enterprise", State: "active"})
	code := cli.Execute(ctx, []string{"--version"}, cli.Options{
		Stdout:  &out,
		Stderr:  &bytes.Buffer{},
		Stdin:   strings.NewReader(""),
		Getenv:  func(string) string { return "" },
		Clock:   clock.NewFake(epoch),
		WorkDir: t.TempDir(),
	})
	require.Zero(t, code)
	require.Contains(t, out.String(), "enterprise edition")
}

// --short on its own would otherwise reach the help, which is a flag that
// silently means something other than what it says.
func TestVersionFlag_ShortWithoutVersionIsRefusedByName(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "--short")
	require.NotZero(t, got.code, "--short on its own printed the help and exited zero")
	require.Contains(t, prose(got.stderr), "--version",
		"the refusal does not name the flag that makes --short work")
}

// -v stays the shorthand for --verbose, decided rather than inherited.
//
// It was never swallowed. It is a documented persistent flag on every command
// in the tree, and af -v prints the help because a bare af prints the help.
// Giving the letter to --version would break --verbose everywhere to save six
// keystrokes in one place.
func TestVerboseKeepsTheVShorthand(t *testing.T) {
	t.Parallel()

	flag := cli.RootForDocs().PersistentFlags().ShorthandLookup("v")
	require.NotNil(t, flag, "-v is no longer bound to anything")
	require.Equal(t, "verbose", flag.Name, "-v was taken from --verbose")
}

// And it still works where verbosity is actually consumed, so the shorthand is
// not merely declared.
func TestVerboseShorthandIsAcceptedOnASubcommand(t *testing.T) {
	t.Parallel()
	got := runCLI(t, t.TempDir(), nil, "version", "-v")
	require.Zero(t, got.code, "af version -v was refused: %s", got.stderr)
	require.Contains(t, got.stdout, "antifailure")
}
