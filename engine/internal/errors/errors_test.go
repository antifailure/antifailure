package errors_test

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/errors"
)

func TestCatalog_IsComplete(t *testing.T) {
	t.Parallel()
	all := errors.All()
	require.NotEmpty(t, all)
	seen := map[errors.Code]struct{}{}
	for _, e := range all {
		require.NotContains(t, seen, e.Code, "duplicate code %s", e.Code)
		seen[e.Code] = struct{}{}
		require.NotEmpty(t, e.Message, "%s has no message", e.Code)
		require.NotEmpty(t, e.NextStep, "%s has no next step", e.Code)
		require.NotEmpty(t, e.Docs, "%s has no documentation slug", e.Code)
		require.True(t, e.ExitCode >= 0 && e.ExitCode <= 10, "%s has exit code %d", e.Code, e.ExitCode)
	}
}

// Every user facing string in the project follows the same prose rules. The
// catalog is where they are easiest to break, so they are checked here as well
// as in the generator.
func TestCatalog_ProseRules(t *testing.T) {
	t.Parallel()
	for _, e := range errors.All() {
		for _, s := range []string{e.Message, e.NextStep} {
			require.NotContains(t, s, "—", "%s uses an em dash", e.Code)
			require.NotContains(t, strings.ToLower(s), "contact support",
				"%s tells the reader to contact support instead of naming an action", e.Code)
			require.NotContains(t, strings.ToLower(s), "todo", "%s contains a placeholder", e.Code)
		}
		require.False(t, strings.HasPrefix(e.NextStep, "You should"),
			"%s hedges; a next step is an instruction", e.Code)
	}
}

func TestCoded_FillsPlaceholders(t *testing.T) {
	t.Parallel()
	err := errors.Coded(errors.AFDB006, "limit", "3")
	require.Equal(t, "The provider's concurrent branch limit (3) is reached.", err.Message())
	require.Contains(t, err.Error(), "AF-DB-006")
	require.Contains(t, err.Error(), "(3)")
}

func TestCoded_UnknownPlaceholderIsLeftVisible(t *testing.T) {
	t.Parallel()
	// A missing field must look like the bug it is, not silently render a
	// sentence with a hole in it.
	err := errors.Coded(errors.AFDB006, "unrelated", "x")
	require.Contains(t, err.Message(), "{limit}")
}

func TestCoded_OddFieldCountDoesNotLoseTheError(t *testing.T) {
	t.Parallel()
	err := errors.Coded(errors.AFDB006, "limit")
	require.Contains(t, err.Error(), "AF-DB-006")
	require.Contains(t, err.Message(), "()")
}

func TestCoded_NoFieldsLeavesPlaceholdersAlone(t *testing.T) {
	t.Parallel()
	err := errors.Coded(errors.AFDB006)
	require.Contains(t, err.Message(), "{limit}")
}

func TestWrap_PreservesTheCause(t *testing.T) {
	t.Parallel()
	cause := stderrors.New("connection refused")
	err := errors.Wrap(cause, errors.AFRUN002, "endpoint", "unix:///var/run/docker.sock")
	require.ErrorIs(t, err, cause)
	require.Contains(t, err.Error(), "connection refused")
	require.Contains(t, err.Error(), "unix:///var/run/docker.sock")
}

func TestWithOp_BuildsAPathBackToTheCause(t *testing.T) {
	t.Parallel()
	err := error(errors.Coded(errors.AFDB004, "version", "gv_20260825120000_deadbeef"))
	err = errors.WithOp(err, "branch")
	err = errors.WithOp(err, "db.docker")
	require.True(t, strings.HasPrefix(err.Error(), "db.docker: branch: AF-DB-004"), err.Error())
}

func TestWithOp_OnANilErrorReturnsNil(t *testing.T) {
	t.Parallel()
	require.NoError(t, errors.WithOp(nil, "op"))
}

func TestWithOp_OnAPlainErrorWrapsIt(t *testing.T) {
	t.Parallel()
	err := errors.WithOp(stderrors.New("boom"), "provider")
	require.EqualError(t, err, "provider: boom")
	require.Equal(t, errors.ExitFailure, errors.ExitCodeOf(err))
}

func TestWithOp_DoesNotMutateTheOriginal(t *testing.T) {
	t.Parallel()
	orig := errors.Coded(errors.AFDB004)
	_ = errors.WithOp(orig, "outer")
	require.Empty(t, orig.Op, "WithOp must copy rather than mutate a shared error")
}

func TestIs_MatchesByCodeRegardlessOfFields(t *testing.T) {
	t.Parallel()
	err := errors.Coded(errors.AFDB006, "limit", "3")
	require.ErrorIs(t, err, errors.Coded(errors.AFDB006))
	require.NotErrorIs(t, err, errors.Coded(errors.AFDB004))
	require.False(t, err.Is(stderrors.New("unrelated")))
}

func TestIs_MatchesThroughWrapping(t *testing.T) {
	t.Parallel()
	inner := errors.Coded(errors.AFNET001, "host", "api.stripe.com", "rule", "default")
	outer := fmt.Errorf("egress: %w", inner)
	require.ErrorIs(t, outer, errors.Coded(errors.AFNET001))
}

func TestExitCodeOf(t *testing.T) {
	t.Parallel()
	require.Equal(t, errors.ExitSuccess, errors.ExitCodeOf(nil))
	require.Equal(t, errors.ExitFailure, errors.ExitCodeOf(stderrors.New("x")))
	require.Equal(t, errors.ExitPolicyDenied, errors.ExitCodeOf(errors.Coded(errors.AFNET001)))
	require.Equal(t, errors.ExitVerification, errors.ExitCodeOf(errors.Coded(errors.AFMSK001)))
	require.Equal(t, errors.ExitInterruptedDirty, errors.ExitCodeOf(errors.Coded(errors.AFRUN030)))
	require.Equal(t, errors.ExitInterruptedDirty, errors.Coded(errors.AFRUN030).ExitCode())
}

// Every code in the catalog must map to an exit status inside the registry,
// because a script that branches on an exit code cannot handle a number the
// documentation does not list.
func TestCatalog_EveryExitCodeIsInTheRegistry(t *testing.T) {
	t.Parallel()
	known := map[errors.ExitCode]struct{}{
		errors.ExitSuccess: {}, errors.ExitFailure: {}, errors.ExitUsage: {},
		errors.ExitConfiguration: {}, errors.ExitAuth: {}, errors.ExitProvider: {},
		errors.ExitPolicyDenied: {}, errors.ExitVerification: {}, errors.ExitTestFailure: {},
		errors.ExitInterruptedClean: {}, errors.ExitInterruptedDirty: {},
	}
	for _, e := range errors.All() {
		require.Contains(t, known, e.ExitCode, "%s maps to an unlisted exit code", e.Code)
		require.Equal(t, e.ExitCode, errors.Coded(e.Code).ExitCode())
	}
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()
	require.True(t, errors.IsRetryable(errors.Coded(errors.AFRUN002)))
	require.False(t, errors.IsRetryable(errors.Coded(errors.AFNET001)))
	require.False(t, errors.IsRetryable(stderrors.New("x")))
	require.True(t, errors.Coded(errors.AFINF002).Retryable())
}

func TestDocsURL(t *testing.T) {
	t.Parallel()
	require.Equal(t, "https://antifailure.dev/docs/concepts/egress",
		errors.Coded(errors.AFNET001).DocsURL())
}

func TestCodeAccessor(t *testing.T) {
	t.Parallel()
	require.Equal(t, errors.AFDB006, errors.Coded(errors.AFDB006).Code())
}

func TestNextStep_IsFilled(t *testing.T) {
	t.Parallel()
	err := errors.Coded(errors.AFSEC001, "names", "STRIPE_KEY", "sources", "keyring, env")
	require.Equal(t, "Add them to one of the searched sources: keyring, env.", err.NextStep())
}

func TestLookup_UnknownCodeDoesNotPanic(t *testing.T) {
	t.Parallel()
	// An error path is the worst place for a new crash. An unrecognised code
	// must degrade to a reportable message.
	e := errors.Lookup(errors.Code("AF-ZZZ-999"))
	require.Equal(t, "UNK", e.Area)
	require.Contains(t, e.Message, "AF-ZZZ-999")
	require.Equal(t, errors.ExitFailure, e.ExitCode)
}

func TestError_RendersWithoutFieldsOrOp(t *testing.T) {
	t.Parallel()
	err := &errors.Error{Entry: errors.Lookup(errors.AFRUN001)}
	require.Contains(t, err.Error(), "AF-RUN-001")
}

func TestError_UnwrapReturnsTheCause(t *testing.T) {
	t.Parallel()
	cause := stderrors.New("root")
	require.Equal(t, cause, errors.Unwrap(errors.Wrap(cause, errors.AFRUN002)))
	require.NoError(t, errors.Unwrap(errors.Coded(errors.AFRUN002)))
}

func TestFill_HandlesMalformedPlaceholders(t *testing.T) {
	t.Parallel()
	// An unclosed brace must render verbatim rather than truncate the message.
	e := &errors.Error{
		Entry:  errors.Entry{Code: "AF-X", Message: "value is {unclosed", NextStep: "n/a"},
		Fields: map[string]string{"unclosed": "v"},
	}
	require.Equal(t, "value is {unclosed", e.Message())
}

func TestReexportedHelpers(t *testing.T) {
	t.Parallel()
	base := errors.New("base")
	require.Error(t, base)
	joined := errors.Join(base, errors.Coded(errors.AFDB004))
	require.ErrorIs(t, joined, base)
	var target *errors.Error
	require.True(t, errors.As(joined, &target))
	require.True(t, errors.Is(joined, base))
}
