package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/insights"
)

// The format renders the run. It does not decide the run.
//
// `af insights -o json` returned as soon as the document was written, so a
// migration that failed on a branch with production's shape exited 0 as long
// as somebody asked for JSON. Nothing caught it because the early return was
// only reachable through a cobra RunE that needs a database, which is exactly
// why the tail is now a function that takes a buffer.

func jsonEnv() (*Env, *bytes.Buffer) {
	var out, errW bytes.Buffer
	e := &Env{Out: NewOutput(&out, &errW)}
	e.Out.Format = FormatJSON
	return e, &out
}

func TestInsightsResult_JSONStillFailsOnAProvenMigrationBreak(t *testing.T) {
	t.Parallel()
	e, out := jsonEnv()
	full := insights.Full{Rehearsal: &insights.Rehearsal{
		Failed: true, Error: "ERROR: relation \"users\" does not exist",
	}}

	err := insightsResult(e, full)

	require.Error(t, err, "-o json reported success on a migration that failed")
	require.Equal(t, aferrors.ExitProvider, aferrors.ExitCodeOf(err))
	// The document is still written. Failing is not an excuse to give a
	// script nothing to read.
	var doc map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	require.Contains(t, doc, "rehearsal")
}

func TestInsightsResult_JSONStillFailsOnABlockedCheck(t *testing.T) {
	t.Parallel()
	e, out := jsonEnv()
	full := insights.Full{Blocked: []string{"no migration tool was recognised"}}

	err := insightsResult(e, full)

	require.Error(t, err)
	require.Equal(t, aferrors.ExitVerification, aferrors.ExitCodeOf(err))
	var doc map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc))
	// The reason is in the document as well as in the exit, because a script
	// that branches on 7 still has to be able to say why.
	require.Contains(t, doc, "blocked")
}

func TestInsightsResult_JSONOnACleanRunExitsZeroAndWritesTheDocument(t *testing.T) {
	t.Parallel()
	e, out := jsonEnv()

	require.NoError(t, insightsResult(e, insights.Full{}))
	require.NotEmpty(t, out.Bytes())
	// And no summary line: the ok belongs to the text rendering only.
	require.NotContains(t, out.String(), "nothing to report")
}

func TestInsightsResult_TextAndJSONAgreeOnEveryVerdict(t *testing.T) {
	t.Parallel()
	// The property, rather than three more examples of it. Two commands in
	// one tool disagreeing about whether a break is a break is the defect;
	// one command disagreeing with itself is worse.
	for _, c := range []struct {
		name string
		full insights.Full
	}{
		{"clean", insights.Full{}},
		{"blocked", insights.Full{Blocked: []string{"nothing was rehearsed"}}},
		{"migration broke", insights.Full{
			Rehearsal: &insights.Rehearsal{Failed: true, Error: "boom"}}},
		{"a finding, which is not an exit here", insights.Full{
			PlanFindings: []insights.PlanFinding{{Statement: "SELECT 1"}}}},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var textOut, textErr bytes.Buffer
			text := &Env{Out: NewOutput(&textOut, &textErr)}
			j, _ := jsonEnv()

			require.Equal(t,
				aferrors.ExitCodeOf(insightsResult(text, c.full)),
				aferrors.ExitCodeOf(insightsResult(j, c.full)),
				"text and json exited differently for the same run")
		})
	}
}

// The rolling check's break had no test, and moving it into insightsBreaks is
// what surfaced that: a mutation replacing `full.Rolling.Failed()` with false
// left every test green. It is the other proven break this command reports and
// it had been relying on nothing.

func TestInsightsResult_ARollingBreakIsAProvenBreak(t *testing.T) {
	t.Parallel()
	full := insights.Full{Rolling: &insights.Rolling{
		Verdict: insights.RollingFail,
		Against: "abc1234",
		Workflows: []insights.RollingWorkflow{{
			Name: "checkout", Verdict: insights.RollingFail,
		}},
	}}

	err := insightsResult(&Env{Out: NewOutput(&bytes.Buffer{}, &bytes.Buffer{})}, full)

	require.Error(t, err, "the previous release failing against the migration exited zero")
	require.Equal(t, aferrors.ExitTestFailure, aferrors.ExitCodeOf(err))
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFDB032, coded.Entry.Code)
	// The workflow that failed is named, because "the previous release does
	// not survive this migration" with no which sends somebody to the logs.
	require.Contains(t, coded.Fields["detail"], "checkout")
}

func TestInsightsResult_ARollingCheckThatCouldNotRunIsNotABreak(t *testing.T) {
	t.Parallel()
	// The distinction this whole change is about, in the one place the
	// repository already had it. Blocked and off are not failures, and making
	// them exit like one is the mirror of printing ok over them.
	for _, v := range []insights.RollingVerdict{
		insights.RollingBlocked, insights.RollingOff, insights.RollingPass,
	} {
		v := v
		t.Run(string(v), func(t *testing.T) {
			t.Parallel()
			e := &Env{Out: NewOutput(&bytes.Buffer{}, &bytes.Buffer{})}
			require.NoError(t, insightsResult(e, insights.Full{
				Rolling: &insights.Rolling{Verdict: v, Reason: "no previous release"},
			}))
		})
	}
}
