package cli

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/crossstore"
	"github.com/antifailure/antifailure/engine/internal/env"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/masking"
)

// The verdict this command turns into an exit code.
//
// Two codes rather than one, because the two outcomes are different facts and
// this repository has already paid for conflating them. A pair that disagreed
// is a statement about the data; a run that compared nothing is a statement
// about what could be reached. Telling somebody their stores disagree when the
// truth is that nobody looked would be the defect one level up from the one
// this command was written to close.

func agreeing() *env.CrossStoreResult {
	return &env.CrossStoreResult{
		Declared: []string{"primary", "events"},
		Report: crossstore.Report{
			Read: []string{"primary", "events"},
			Cross: masking.CrossStoreReport{
				Stores: []string{"primary", "events"}, Checked: 4, Identical: 4,
			},
		},
	}
}

func TestCrossStoreFailure_AgreementIsNotAFailure(t *testing.T) {
	t.Parallel()
	require.NoError(t, crossStoreFailure(agreeing()))
}

func TestCrossStoreFailure_ADisagreementIsAVerificationFailure(t *testing.T) {
	t.Parallel()
	res := agreeing()
	res.Report.Cross.Identical = 3
	res.Report.Cross.Pairs = []masking.JoinPair{{
		Key: "email",
		A:   masking.JoinColumn{Store: "primary", Table: "public.person", Column: "email"},
		B:   masking.JoinColumn{Store: "events", Table: "af.events", Column: "email"},
		Reason: "the two are masked with different transforms, email and hash_hex, " +
			"so one identity becomes two people",
	}}

	err := codedError(t, crossStoreFailure(res))
	require.Equal(t, aferrors.AFMSK014, err.Code())
	require.Equal(t, aferrors.ExitCode(7), err.ExitCode(),
		"a store that disagrees is a verification failure")
	require.Contains(t, err.Error(), "af.events.email")
	require.Contains(t, err.Error(), "one identity becomes two people")
}

// The case an ordinary implementation gets wrong. A report with no pairs has
// no mismatches either, so testing for mismatches first returns nil for
// exactly the run that proved nothing.
func TestCrossStoreFailure_NothingComparedIsItsOwnCodeAndNotASuccess(t *testing.T) {
	t.Parallel()
	res := &env.CrossStoreResult{
		Declared:      []string{"primary", "events"},
		WithoutSource: []string{"events"},
		Report: crossstore.Report{
			Read:  []string{"primary"},
			Cross: masking.CrossStoreReport{Stores: []string{"primary"}},
		},
	}
	err := codedError(t, crossStoreFailure(res))
	require.Equal(t, aferrors.AFMSK015, err.Code())
	require.Equal(t, aferrors.ExitCode(1), err.ExitCode())
	require.Contains(t, err.Error(), "nothing is proved")
}

// Two stores read that share no identifier is zero of zero, which is the same
// answer: nothing was compared. It reaches the code through a different branch
// than the one above, so it is asserted separately.
func TestCrossStoreFailure_TwoStoresWithNothingInCommonComparedNothing(t *testing.T) {
	t.Parallel()
	res := agreeing()
	res.Report.Cross.Checked = 0
	res.Report.Cross.Identical = 0

	err := codedError(t, crossStoreFailure(res))
	require.Equal(t, aferrors.AFMSK015, err.Code(),
		"two stores sharing no identifier have proved nothing, and a percentage over "+
			"zero pairs is not a pass")
}

// The document a script reads. percent is absent rather than zero when nothing
// was compared, because zero percent and no comparison are opposite facts and
// a field that renders 0.0 for both is unreadable.
func TestCrossStoreJSON_SaysNullRatherThanZeroWhenNothingWasCompared(t *testing.T) {
	t.Parallel()
	e, out := jsonEnv()
	res := agreeing()
	res.Report.Read = []string{"primary"}
	res.Report.Cross = masking.CrossStoreReport{Stores: []string{"primary"}}

	_ = reportCrossStore(e, res)
	require.Contains(t, out.String(), `"percent": null`)
	require.Contains(t, out.String(), `"ok": false`)
	require.Contains(t, out.String(), `"rows_read"`)
}

func TestCrossStoreJSON_CarriesTheNumberAndTheCoverage(t *testing.T) {
	t.Parallel()
	e, out := jsonEnv()
	res := agreeing()
	res.Report.Tables, res.Report.Columns = 12, 96
	res.WithoutSource = []string{"cache"}
	res.Report.Unread = []crossstore.Unread{{Store: "search", Engine: "elasticsearch", Why: "no route to host"}}

	require.NoError(t, reportCrossStore(e, res))
	body := out.String()
	require.Contains(t, body, `"percent": 100`)
	require.Contains(t, body, `"join_keys_identical": 4`)
	require.Contains(t, body, `"stores_without_source_url_env"`)
	require.Contains(t, body, "no route to host",
		"a store that could not be read has to appear beside the number, or the number "+
			"is a true answer to a smaller question")
}

// Two stores agreeing while a third could not be read is not a pass, and it
// carries the coverage code rather than the disagreement one: nobody looked at
// the third store, and telling somebody their stores disagree when that is the
// truth would be the conflation this command exists to avoid.
func TestCrossStoreFailure_AnUnreadStoreRefusesEvenWhenTheRestAgree(t *testing.T) {
	t.Parallel()
	res := agreeing()
	res.Declared = append(res.Declared, "search")
	res.Report.Unread = []crossstore.Unread{
		{Store: "search", Engine: "elasticsearch", Why: "no route to host"},
	}

	err := codedError(t, crossStoreFailure(res))
	require.Equal(t, aferrors.AFMSK015, err.Code(),
		"a store nothing opened is a coverage gap and not a disagreement")
	require.Contains(t, err.Error(), "search")
	require.Contains(t, err.Error(), "no route to host")
	require.Contains(t, err.Error(), "4 of 4 join keys agreed",
		"the number it did get belongs in the message, or the reader cannot tell a "+
			"partial answer from no answer at all")
}
