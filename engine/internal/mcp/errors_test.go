package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// heldLock is the error the engine returns when another process holds the
// branch lock, exactly as internal/lock builds it.
func heldLock() error {
	return aferrors.Coded(aferrors.AFRUN003,
		"pid", "55799", "since", "2026-09-06T05:38:35Z")
}

func TestFaultDocument_ACataloguedCauseIsExplainedTheWayTheCLIExplainsIt(t *testing.T) {
	t.Parallel()
	// The same held lock on 2026-09-06: af golden list printed AF-RUN-003
	// with the process id and "run 'af down'", and inspect_goldens returned
	// SAFETY_UNAVAILABLE and "the server log says why", a log no tool on the
	// server can read. The fault now carries the four lines the CLI prints.
	f := &Fault{
		Code: FaultSafetyUnavailable,
		Detail: "The golden versions could not be listed, so this says nothing about " +
			"what this project can branch. The server log says why.",
		Retryable: true, wrapped: heldLock(),
	}

	doc := f.document()

	require.NotNil(t, doc.Cause, "a catalogued cause is rendered")
	require.Equal(t, "AF-RUN-003", doc.Cause.Code)
	require.Contains(t, doc.Cause.Message, "process 55799")
	require.Contains(t, doc.Cause.Next, "'af down'")
	require.Equal(t, "https://antifailure.dev/docs/concepts/journal", doc.Cause.More)
	require.True(t, doc.Cause.Retryable, "the catalog says a held lock clears on its own")

	require.Contains(t, doc.Detail, "AF-RUN-003")
	require.Contains(t, doc.Detail, "process 55799")
	require.Contains(t, doc.Detail, "Next: Wait for it to finish")
	require.NotContains(t, doc.Detail, "server log")
}

func TestFaultDocument_ARawCauseStaysInTheServerLog(t *testing.T) {
	t.Parallel()
	// A driver's or the operating system's text can name a host or a path.
	// It is not catalogued, so it is not rendered, and the detail says
	// where it went instead of pointing at a log nobody here can open.
	f := &Fault{
		Code:      FaultSafetyUnavailable,
		Detail:    "The container runtime could not be reached. The server log says why.",
		Retryable: true, wrapped: errors.New("dial tcp 10.0.0.7:2375: connection refused"),
	}

	doc := f.document()

	require.Nil(t, doc.Cause)
	require.NotContains(t, doc.Detail, "10.0.0.7")
	require.NotContains(t, doc.Detail, "server log")
	require.Contains(t, doc.Detail, "standard error")
	require.Contains(t, doc.Detail, "at a terminal")
}

func TestFaultDocument_NoCauseLeavesTheDetailAlone(t *testing.T) {
	t.Parallel()
	f := faultf(FaultInvalidArgument, "question must be one of plan, sample or verify.")

	doc := f.document()

	require.Nil(t, doc.Cause)
	require.Equal(t, "question must be one of plan, sample or verify.", doc.Detail)
}

func TestWithCause_RemovesTheServerLogSentenceInEveryFormItTook(t *testing.T) {
	t.Parallel()
	// Every phrasing the first version used, joined to its sentence by a
	// full stop, a semicolon, or nothing at all. The regular expression is
	// the belt under the source edit that removed them, and it has to hold
	// for text that arrives at run time too.
	cases := map[string]string{
		"The database could not be reached. The server log says why.":                    "The database could not be reached.",
		"The provider has to be reachable for that; the server log says why it was not.": "The provider has to be reachable for that.",
		"It needs enough room. The server log says which was missing.":                   "It needs enough room.",
		"The server log says what stopped it.":                                           "",
		"It is reachable or it is not; the server log says which failed.":                "It is reachable or it is not.",
	}
	for in, want := range cases {
		require.Equal(t, want, withCause(in, nil), "input %q", in)
	}
}

func TestWithCause_AppendsTheCatalogExplanation(t *testing.T) {
	t.Parallel()
	got := withCause("The environment's database could not be reached. The server log says why.",
		heldLock())

	require.Equal(t, "The environment's database could not be reached. AF-RUN-003: Another "+
		"Antifailure process holds the lock for this branch (process 55799, since "+
		"2026-09-06T05:38:35Z). Next: Wait for it to finish, or stop it and run 'af down' "+
		"to clean up. More: https://antifailure.dev/docs/concepts/journal", got)
}

func TestWithCause_FindsTheCodeUnderAnOperationPath(t *testing.T) {
	t.Parallel()
	// The lock error rarely arrives bare. It is wrapped with the operation
	// that met it and often once more by the caller of that, and the code
	// has to be found through both, as errors.As finds it.
	err := aferrors.WithOp(heldLock(), "golden: list")
	err = errors.Join(err)

	require.NotNil(t, describeCause(err))
	require.Equal(t, "AF-RUN-003", describeCause(err).Code)
}

func TestInspectGoldens_AHeldLockIsExplainedWithThePidAndTheRemedy(t *testing.T) {
	t.Parallel()
	// The tool the evaluator called. Through the handler and the document,
	// so the assertion is about what a caller receives and not about a
	// helper.
	tool := newInspectGoldensTool(testProject(t),
		func(context.Context) ([]provider.GoldenVersion, string, error) {
			return nil, "", aferrors.WithOp(heldLock(), "golden: list")
		},
		noPublished(), policyOf(3, 0))

	_, fault := invoke(t, tool, `{"project_id":"test-project"}`)

	require.NotNil(t, fault)
	doc := fault.document()
	require.Equal(t, string(FaultSafetyUnavailable), doc.Code)
	require.NotNil(t, doc.Cause)
	require.Equal(t, "AF-RUN-003", doc.Cause.Code)
	require.Contains(t, doc.Cause.Message, "process 55799")
	require.Contains(t, doc.Cause.Next, "'af down'")
	require.Contains(t, doc.Detail, "process 55799")
	require.NotContains(t, doc.Detail, "server log")
}

func TestInvariantsUnavailable_CarriesTheLockHolder(t *testing.T) {
	t.Parallel()
	// check_data_invariants does not refuse; it answers INCONCLUSIVE with a
	// summary. The summary pointed at the server log too, and it now names
	// the holder the same way.
	got := invariantsUnavailable(heldLock())

	require.Contains(t, got, "AF-RUN-003")
	require.Contains(t, got, "process 55799")
	require.NotContains(t, got, "server log")
}
