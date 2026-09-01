package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/manifest"
)

// Validation catches these because 'af doctor' is where somebody finds out
// their manifest is wrong, and a load run is twenty minutes into a pull
// request check. The engine refuses them again at run time, which is a
// different guarantee: it covers a manifest that reached the engine without
// being validated.

func TestParse_RejectsALoadSourceThatDoesNotExist(t *testing.T) {
	t.Parallel()
	// Datadog and New Relic were accepted by the schema and refused by the
	// engine, which is worse than not offering them: a key a person can set
	// that cannot work reads as a broken product rather than an unfinished
	// one.
	for _, source := range []string{"datadog", "newrelic", "splunk"} {
		body := minimal + "\nload:\n  enabled: true\n  source: " + source + "\n"
		msg := messages(problems(t, mustFail(t, body)))
		require.Contains(t, msg, "no load source called")
		require.Contains(t, msg, "otel")
		require.Contains(t, msg, "access_log")
	}
}

func TestParse_AcceptsTheTwoSourcesThatWork(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"otel", "access_log"} {
		m := mustParse(t, minimal+
			"\nload:\n  enabled: true\n  source: "+source+
			"\n  source_config:\n    path: traffic/sample\n")
		require.Equal(t, source, string(m.Load.Source))
	}
}

func TestParse_RejectsAFileSourceWithNoPath(t *testing.T) {
	t.Parallel()
	// Without it the run fails at the point of reading, which is after the
	// environment is up and several minutes have gone.
	body := minimal + "\nload:\n  enabled: true\n  source: otel\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))), "no path is configured")
}

func TestParse_RejectsAScenarioFileThatIsNotThere(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "antifailure.yaml"), nil, 0o600))

	body := minimal + "\nload:\n  enabled: true\n  scenarios:\n    - path: scenarios/missing.yaml\n"
	_, err := manifest.Parse([]byte(body), "antifailure.yaml", root)
	require.Contains(t, messages(problems(t, err)), "There is no file at scenarios/missing.yaml")
}

func TestParse_RejectsAScenarioWithNoPathAndABadStartAfter(t *testing.T) {
	t.Parallel()
	body := minimal + "\nload:\n  enabled: true\n  scenarios:\n    - path: \"\"\n" +
		"    - path: a.yaml\n      start_after: soon\n"
	msg := messages(problems(t, mustFail(t, body)))
	require.Contains(t, msg, "The scenario has no path.")
	require.Contains(t, msg, `The value "soon" is not a duration.`)
}

func TestParse_AcceptsAScenarioTheEngineWouldRun(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "scenarios"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "scenarios", "impatient_upgrade.yaml"), []byte("scenario: x\n"), 0o600))

	m, err := manifest.Parse([]byte(minimal+
		"\nload:\n  enabled: true\n  scenarios:\n"+
		"    - path: scenarios/impatient_upgrade.yaml\n      sessions: 50\n      iterations: 4\n"+
		"      start_after: 10s\n"), "antifailure.yaml", root)
	require.NoError(t, err)
	require.Len(t, m.Load.Scenarios, 1)
	require.Equal(t, 50, m.Load.Scenarios[0].Sessions)
	require.Equal(t, 4, m.Load.Scenarios[0].Iterations)
	require.Equal(t, "10s", m.Load.Scenarios[0].StartAfter)
}

// A threshold that cannot fire is the defect these cover. p95_increase divides
// a measured p95 by production's own p95 for that route, and only a trace
// export carries one, so under access_log or none the check was listed in the
// manifest and skipped every route it was meant to judge.

func TestParse_RefusesP95IncreaseUnderASourceWithNoBaseline(t *testing.T) {
	t.Parallel()
	for source, want := range map[string]string{
		"access_log": "The load source is access_log and p95_increase is set.",
		"none":       "The load source is none and p95_increase is set.",
	} {
		body := minimal + "\nload:\n  enabled: true\n  source: " + source +
			"\n  source_config:\n    path: traffic/sample\n" +
			"  thresholds:\n    p95_increase: 0.25\n"
		msg := messages(problems(t, mustFail(t, body)))
		require.Contains(t, msg, want)
		require.Contains(t, msg, "can never fire")
	}
}

func TestParse_RefusesP95IncreaseWhenNoSourceIsNamedAtAll(t *testing.T) {
	t.Parallel()
	// normalizeLoad turns an absent source into none, and an author who never
	// wrote a source is in exactly the position the message describes.
	body := minimal + "\nload:\n  enabled: true\n  thresholds:\n    p95_increase: 0.5\n"
	require.Contains(t, messages(problems(t, mustFail(t, body))),
		"The load source is none and p95_increase is set.")
}

func TestParse_AcceptsP95IncreaseUnderTheSourceThatCarriesABaseline(t *testing.T) {
	t.Parallel()
	m := mustParse(t, minimal+"\nload:\n  enabled: true\n  source: otel\n"+
		"  source_config:\n    path: traffic/production.otlp.json\n"+
		"  thresholds:\n    p95_increase: 0.25\n")
	require.InDelta(t, 0.25, m.Load.Thresholds.P95Increase, 1e-9)
}

func TestParse_AcceptsAccessLogWithAThresholdThatNeedsNoBaseline(t *testing.T) {
	t.Parallel()
	// error_rate is computed from the run's own responses, so it works under
	// every source. Refusing the whole thresholds block would have been the
	// easy fix and the wrong one.
	m := mustParse(t, minimal+"\nload:\n  enabled: true\n  source: access_log\n"+
		"  source_config:\n    path: ops/access.log\n"+
		"  thresholds:\n    error_rate: 0.02\n")
	require.InDelta(t, 0.02, m.Load.Thresholds.ErrorRate, 1e-9)
}

func TestNormalize_DoesNotDefaultP95IncreaseWhereItCannotBeMeasured(t *testing.T) {
	t.Parallel()
	// The half the validator cannot see. Nobody wrote this threshold, so
	// refusing the manifest would be refusing the engine's own choice; the
	// fix is for the engine to stop making it.
	for _, source := range []string{"access_log", "none"} {
		m := mustParse(t, minimal+"\nload:\n  enabled: true\n  source: "+source+
			"\n  source_config:\n    path: traffic/sample\n")
		require.Zero(t, m.Load.Thresholds.P95Increase,
			"source %s carries no baseline, so a default threshold is one that can never fire", source)
		require.InDelta(t, 0.01, m.Load.Thresholds.ErrorRate, 1e-9,
			"source %s still gets the threshold it can measure", source)
	}

	m := mustParse(t, minimal+"\nload:\n  enabled: true\n  source: otel\n"+
		"  source_config:\n    path: traffic/production.otlp.json\n")
	require.InDelta(t, 0.25, m.Load.Thresholds.P95Increase, 1e-9)
}

func TestParse_RefusesQueryCountIncreaseBecauseNothingReadsIt(t *testing.T) {
	t.Parallel()
	// It reached the schema, the Go type and the normalizer, and no load run
	// counts statements. Every source, because the problem is not the source.
	for _, source := range []string{"otel", "access_log", "none"} {
		body := minimal + "\nload:\n  enabled: true\n  source: " + source +
			"\n  source_config:\n    path: traffic/sample\n" +
			"  thresholds:\n    query_count_increase: 0.2\n"
		msg := messages(problems(t, mustFail(t, body)))
		require.Contains(t, msg, "Nothing measures query_count_increase.")
		require.Contains(t, msg, "insights.query_regression")
	}
}

func TestNormalize_LeavesQueryCountIncreaseAloneWhenNobodySetIt(t *testing.T) {
	t.Parallel()
	// The normalizer used to fill it in, which meant every manifest carried a
	// threshold nothing read.
	m := mustParse(t, minimal+"\nload:\n  enabled: true\n  source: otel\n"+
		"  source_config:\n    path: traffic/production.otlp.json\n")
	require.Zero(t, m.Load.Thresholds.QueryCountIncrease)
}
