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
