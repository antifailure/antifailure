package masking

// AN INTERRUPTED RUN CARRIES ITS CANCELLATION WHATEVER THE STORE SAID.
//
// The live test cannot prove this on its own. When a run is cancelled the driver
// usually reports the cancellation itself, so errors.Is finds context.Canceled
// through the store's error even if InterruptedError drops it, and the assertion
// passes on a broken unwrap. The case that matters is the one from the demo: the
// connection went down with the cancellation and the store said only "unexpected
// EOF", with no cancellation anywhere in it. That is built here directly, which
// needs the unexported fields and so needs this file to be in the package.

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInterruptedError_CarriesTheCancellationWhenTheStoreDoesNot(t *testing.T) {
	t.Parallel()
	err := &InterruptedError{
		Table: "public.customers", Rows: 6,
		cause: context.Canceled, store: io.ErrUnexpectedEOF,
	}

	require.ErrorIs(t, err, context.Canceled,
		"a caller cannot tell an interrupted run from a broken database when the store never said cancelled")
	require.ErrorIs(t, err, io.ErrUnexpectedEOF,
		"the store's own error is no longer reachable for anyone debugging it")

	// The sentence a person reads says nothing about the store's error.
	require.Contains(t, err.Error(), "interrupted")
	require.Contains(t, err.Error(), "public.customers")
	require.Contains(t, err.Error(), "6 rows")
	require.NotContains(t, err.Error(), io.ErrUnexpectedEOF.Error())

	// A deadline reads as one, and still carries the reason it stopped.
	deadline := &InterruptedError{Table: "public.customers", cause: context.DeadlineExceeded}
	require.ErrorIs(t, deadline, context.DeadlineExceeded)
	require.Contains(t, deadline.Error(), "deadline")
	require.False(t, errors.Is(deadline, context.Canceled),
		"a run that ran out of time reads as one somebody cancelled")
}
