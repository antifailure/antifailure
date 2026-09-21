package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The SQL workload block, refused where it could not do what it says.
//
// Every refusal here has the same shape, and it is the shape this repository
// keeps arriving at by accident: a knob that is accepted, defaulted, rendered
// in the published reference, and read by nothing under the settings its author
// chose. The manifest's own history has two of them.
// load.thresholds.query_count_increase reached the schema, the Go type and the
// normalizer and was measured by nothing at all. load.thresholds.p95_increase
// was defaulted onto every manifest including the sources that carry no
// baseline, so it was listed in the report and could never fire.
//
// A SQL workload has four knobs that belong to one source and are read by
// nothing under the other, so it had four chances to repeat that.

func TestLoadSQL_RefusesASourceThatDoesNotExist(t *testing.T) {
	t.Parallel()
	body := minimal + "\nload:\n  sql:\n    source: pgbench\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "no SQL workload source called")
	require.Contains(t, msg, "declared")
	require.Contains(t, msg, "statement_statistics")
}

// TestLoadSQL_RefusesAKnobThatIsReadByNothingUnderItsSource.
func TestLoadSQL_RefusesAKnobThatIsReadByNothingUnderItsSource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			"a script under statement_statistics",
			"\nload:\n  sql:\n    source: statement_statistics\n    script: db/workload.yaml\n",
			[]string{"load.sql.script", "would never be read"},
		},
		{
			"no script under declared",
			"\nload:\n  sql:\n    source: declared\n",
			[]string{"load.sql.script", "no script is named"},
		},
		{
			"writes under declared",
			"\nload:\n  sql:\n    source: declared\n    script: db/w.yaml\n    writes: true\n",
			[]string{"load.sql.writes", "read by nothing here"},
		},
		{
			"max_statements under declared",
			"\nload:\n  sql:\n    source: declared\n    script: db/w.yaml\n    max_statements: 5\n",
			[]string{"load.sql.max_statements", "read by nothing here"},
		},
		{
			"mean_increase under declared",
			"\nload:\n  sql:\n    source: declared\n    script: db/w.yaml\n" +
				"    thresholds:\n      mean_increase: 0.5\n",
			[]string{"load.sql.thresholds.mean_increase", "never run", "can never fire"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := messages(problems(t, mustFail(t, minimal+tc.body)))
			for _, want := range tc.want {
				require.Contains(t, msg, want)
			}
		})
	}
}

// TestLoadSQL_DoesNotRefuseItsOwnDefault.
//
// The other half of the rule, and the half that makes the refusals above safe
// to ship. normalizeLoadSQL fills mean_increase under statement_statistics, so
// a validator that refused the key rather than the author's DECLARATION of it
// would refuse every derived workload on the planet.
func TestLoadSQL_DoesNotRefuseItsOwnDefault(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+"\nload:\n  sql:\n    source: declared\n    script: db/w.yaml\n")
	require.NotNil(t, m.Load.SQL)
	require.Equal(t, schema.SQLDeclared, m.Load.SQL.Source)
	// Filled, and not refused, because the engine wrote it rather than the
	// author. Zero under declared, because there is no baseline to compare
	// against and a default there would be a threshold nothing could measure.
	require.InDelta(t, 0, m.Load.SQL.Thresholds.MeanIncrease, 1e-9)
	require.InDelta(t, 0.01, m.Load.SQL.Thresholds.ErrorRate, 1e-9)

	derived := mustParse(t, minimal+"\nload:\n  sql:\n    source: statement_statistics\n")
	require.InDelta(t, 0.25, derived.Load.SQL.Thresholds.MeanIncrease, 1e-9,
		"the source that carries a baseline gets the default threshold")
	require.Equal(t, 20, derived.Load.SQL.MaxStatements)
}

func TestLoadSQL_FillsTheDefaultsAndLeavesATransactionCountAlone(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+"\nload:\n  sql:\n    source: statement_statistics\n")
	q := m.Load.SQL
	require.Equal(t, 8, q.Clients)
	require.Equal(t, "60s", q.Duration)
	require.Equal(t, "0ms", q.ThinkTime)

	// A workload sized in transactions and silent about time gets no duration
	// at all. Filling one in beside it would cap a run its author sized in
	// work, and the report would say it ran fewer transactions than it asked
	// for with nothing saying why.
	counted := mustParse(t, minimal+
		"\nload:\n  sql:\n    source: statement_statistics\n    transactions: 500\n")
	require.Equal(t, 500, counted.Load.SQL.Transactions)
	require.Empty(t, counted.Load.SQL.Duration)
}

func TestLoadSQL_RefusesADurationAboveTheCap(t *testing.T) {
	t.Parallel()
	msg := messages(problems(t, mustFail(t,
		minimal+"\nload:\n  sql:\n    source: statement_statistics\n    duration: 30m\n")))
	require.Contains(t, msg, "load.sql.duration")
	require.Contains(t, msg, "fifteen minute cap")
}

// TestLoadSQL_IsCheckedWhetherOrNotTheLoadBlockIsEnabled.
//
// af load sql runs the block whether or not load.enabled is set, exactly as af
// load run sends the mix on a manifest whose load.enabled is false. A block
// checked only when enabled is a block whose errors arrive twenty minutes into
// somebody's first real run.
func TestLoadSQL_IsCheckedWhetherOrNotTheLoadBlockIsEnabled(t *testing.T) {
	t.Parallel()
	body := minimal + "\nload:\n  enabled: false\n  sql:\n    source: pgbench\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "no SQL workload source called")
}

// TestLoadSQL_ATypoOfEveryNewKeyGetsASuggestion.
func TestLoadSQL_ATypoOfEveryNewKeyGetsASuggestion(t *testing.T) {
	t.Parallel()
	for typo, want := range map[string]string{
		"clents":          "clients",
		"scrpit":          "script",
		"think_tim":       "think_time",
		"max_statementss": "max_statements",
		"transaction":     "transactions",
		"writs":           "writes",
	} {
		t.Run(typo, func(t *testing.T) {
			body := minimal + "\nload:\n  sql:\n    " + typo + ": 4\n"
			msg := messages(problems(t, mustFail(t, body)))
			require.Contains(t, msg, want,
				"a typo of %s is refused with no suggestion, so the message says the key is unknown rather than that it is one letter out", want)
		})
	}
}
