package env

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The wiring these prove is the part that was dead. The manifest offered four
// traffic sources and three of them were refused at run time, so the shape a
// person configured and the shape the engine sent were different things and
// nothing said so. In-package because trafficShape and scenarioRuns are not
// exported; what is exported is the manifest they read.

func loadOrchestrator(t *testing.T, root string, cfg *schema.Load) *Orchestrator {
	t.Helper()
	o, err := New(Options{
		Root:     root,
		Manifest: &schema.Manifest{Name: "app", Load: cfg},
		Branch:   "main",
		Clock:    clock.New(),
	})
	require.NoError(t, err)
	return o
}

func writeFile(t *testing.T, root, name, body string) string {
	t.Helper()
	full := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	return name
}

func TestTrafficShape_ReadsAnOpenTelemetryExportFromTheManifest(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "traces/prod.otlp.json",
		`{"resourceSpans":[{"scopeSpans":[{"spans":[`+
			`{"name":"GET /cart","kind":"SPAN_KIND_SERVER","startTimeUnixNano":"0","endTimeUnixNano":"4000000",`+
			`"attributes":[{"key":"http.request.method","value":{"stringValue":"GET"}},`+
			`{"key":"http.route","value":{"stringValue":"/cart"}}]}]}]}]}`)

	shape, err := loadOrchestrator(t, root, &schema.Load{
		Enabled: true, Source: schema.LoadOTel,
		SourceConfig: map[string]string{"path": "traces/prod.otlp.json"},
	}).trafficShape()
	require.NoError(t, err)
	require.Equal(t, "otel", shape.Source)
	require.Len(t, shape.Routes, 1)
	require.Equal(t, "GET /cart", shape.Routes[0].String())
}

func TestTrafficShape_SaysWhyAnExportProducedNothing(t *testing.T) {
	t.Parallel()
	// An export full of client spans produces an empty shape, and the count
	// is the only thing that explains why. "No requests were found" is a much
	// harder message to act on than "two spans were not server spans".
	root := t.TempDir()
	writeFile(t, root, "traces.json",
		`{"resourceSpans":[{"scopeSpans":[{"spans":[`+
			`{"name":"GET /v1/charges","kind":"SPAN_KIND_CLIENT","startTimeUnixNano":"0","endTimeUnixNano":"1"},`+
			`{"name":"GET /v1/refunds","kind":"SPAN_KIND_CLIENT","startTimeUnixNano":"0","endTimeUnixNano":"1"}]}]}]}`)

	_, err := loadOrchestrator(t, root, &schema.Load{
		Enabled: true, Source: schema.LoadOTel,
		SourceConfig: map[string]string{"path": "traces.json"},
	}).trafficShape()
	require.Error(t, err)
	require.Equal(t, aferrors.AFLOD010, codeOf(err))
	require.ErrorContains(t, err, "2 not a server span")
}

func TestTrafficShape_RefusesASourceThisBuildCannotReadAndNamesTheOnesItCan(t *testing.T) {
	t.Parallel()
	// Datadog and New Relic were in the schema, so a person could set them,
	// and refused at run time, so they could never work. They are out of the
	// schema now, and a manifest that reaches the engine unvalidated still
	// gets an answer that names what does work.
	for _, source := range []schema.LoadSource{"datadog", "newrelic", "splunk"} {
		_, err := loadOrchestrator(t, t.TempDir(), &schema.Load{
			Enabled: true, Source: source,
		}).trafficShape()
		require.Error(t, err)
		require.Equal(t, aferrors.AFLOD012, codeOf(err))
		require.ErrorContains(t, err, string(source))

		// The next step is the half of the message a person acts on, and it
		// names what does work rather than only what does not.
		var e *aferrors.Error
		require.True(t, aferrors.As(err, &e))
		require.Contains(t, e.NextStep(), "otel")
		require.Contains(t, e.NextStep(), "access_log")
	}
}

func TestTrafficShape_CountsTheArrivalRateOutOfTheLogAndSaysWhenItCannot(t *testing.T) {
	t.Parallel()
	// Before this the rate was assumed and the assumption reached the report
	// as production's rate.
	root := t.TempDir()
	writeFile(t, root, "access.log",
		`1.2.3.4 - - [01/Jun/2026:12:00:00 +0000] "GET /a HTTP/1.1" 200 12`+"\n"+
			`1.2.3.4 - - [01/Jun/2026:12:00:05 +0000] "GET /b HTTP/1.1" 200 12`+"\n"+
			`1.2.3.4 - - [01/Jun/2026:12:00:10 +0000] "GET /a HTTP/1.1" 200 12`+"\n")
	shape, err := loadOrchestrator(t, root, &schema.Load{
		Enabled: true, Source: schema.LoadAccessLog,
		SourceConfig: map[string]string{"path": "access.log"},
	}).trafficShape()
	require.NoError(t, err)
	require.Equal(t, "access_log", shape.Source)
	require.InDelta(t, 0.3, shape.RequestsPerSecond, 0.001, "three requests across ten seconds")

	writeFile(t, root, "notime.log", `"GET /a HTTP/1.1" 200 12`+"\n")
	guessed, err := loadOrchestrator(t, root, &schema.Load{
		Enabled: true, Source: schema.LoadAccessLog,
		SourceConfig: map[string]string{"path": "notime.log"},
	}).trafficShape()
	require.NoError(t, err)
	require.Equal(t, "access_log, arrival rate assumed", guessed.Source,
		"a guessed rate says it is a guess rather than arriving as production's")
}

func TestTrafficShape_ADefaultShapeSaysItIsADefault(t *testing.T) {
	t.Parallel()
	shape, err := loadOrchestrator(t, t.TempDir(), nil).trafficShape()
	require.NoError(t, err)
	require.Equal(t, "default", shape.Source)
}

func TestScenarioRuns_ReadsEveryDocumentBeforeAnyOfThemRuns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "scenarios/one.yaml", "scenario: one\nsteps:\n  - request: GET /a\n")
	writeFile(t, root, "scenarios/two.yaml", "scenario: two\nsteps:\n  - request: GET /b\n")

	o := loadOrchestrator(t, root, &schema.Load{Enabled: true, Scenarios: []schema.LoadScenario{
		{Path: "scenarios/one.yaml", Sessions: 4, Iterations: 2},
		{Path: "scenarios/two.yaml", Sessions: 1, StartAfter: "250ms"},
	}})

	runs, err := o.scenarioRuns(nil)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	require.Equal(t, "one", runs[0].Scenario.Name)
	require.Equal(t, 4, runs[0].Sessions)
	require.Equal(t, 2, runs[0].Iterations)
	require.Equal(t, 250*time.Millisecond, runs[1].StartAfter)

	only, err := o.scenarioRuns([]string{"two"})
	require.NoError(t, err)
	require.Len(t, only, 1)
	require.Equal(t, "two", only[0].Scenario.Name)
}

func TestScenarioRuns_ABadDocumentIsAMessageRatherThanAHalfRun(t *testing.T) {
	t.Parallel()
	// All of them are read before any of them runs, so a typo in the second
	// document is a message rather than a run that sends one journey and then
	// stops.
	root := t.TempDir()
	writeFile(t, root, "good.yaml", "scenario: good\nsteps:\n  - request: GET /a\n")
	writeFile(t, root, "bad.yaml", "scenario: bad\nsteps:\n  - request: not a request\n")

	_, err := loadOrchestrator(t, root, &schema.Load{Enabled: true, Scenarios: []schema.LoadScenario{
		{Path: "good.yaml"}, {Path: "bad.yaml"},
	}}).scenarioRuns(nil)
	require.Error(t, err)
	require.Equal(t, aferrors.AFLOD013, codeOf(err))
	require.ErrorContains(t, err, "bad.yaml")
}

func TestScenarioRuns_RefusesTwoScenariosWithOneName(t *testing.T) {
	t.Parallel()
	// Two results under one name in a report is unreadable, and --only would
	// have no way to pick between them.
	root := t.TempDir()
	writeFile(t, root, "a.yaml", "scenario: same\nsteps:\n  - request: GET /a\n")
	writeFile(t, root, "b.yaml", "scenario: same\nsteps:\n  - request: GET /b\n")

	_, err := loadOrchestrator(t, root, &schema.Load{Enabled: true, Scenarios: []schema.LoadScenario{
		{Path: "a.yaml"}, {Path: "b.yaml"},
	}}).scenarioRuns(nil)
	require.Error(t, err)
	require.Equal(t, aferrors.AFLOD013, codeOf(err))
	require.ErrorContains(t, err, "already used by a.yaml")
}

func TestScenarioRuns_SaysSoWhenTheFileIsNotThere(t *testing.T) {
	t.Parallel()
	_, err := loadOrchestrator(t, t.TempDir(), &schema.Load{
		Enabled:   true,
		Scenarios: []schema.LoadScenario{{Path: "scenarios/missing.yaml"}},
	}).scenarioRuns(nil)
	require.Error(t, err)
	require.Equal(t, aferrors.AFLOD013, codeOf(err))
}
