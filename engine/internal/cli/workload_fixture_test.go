package cli

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/explore"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/sqlload"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// The wire fixture the control plane's decoder is tested against, produced by
// the engine rather than typed by somebody.
//
// WHY THIS TEST EXISTS AT ALL. web/apps/api/test/fixtures/engine-reports holds
// one `workload.finished` payload per kind, and its README says plainly that a
// fixture somebody wrote by hand is worthless here: both suites either side of
// that wire were green over a message neither had ever sent the other, and the
// decoder was reading the engine's NATIVE type rather than the result document.
// The README then says to "write a test in engine/internal/cli" to refresh
// them, and nobody ever did, so the four existing fixtures are bytes from a
// build in September 2026 that nothing can reproduce.
//
// This is that test, for the fifth kind. It builds a Result through the real
// workload.Execute and puts it through the real hostedPayload, so every name in
// the file comes from a Go struct tag rather than from anybody's memory of one.
//
// IT ASSERTS RATHER THAN ONLY WRITING, which is the half that makes it a check.
// Run with -update-wire-fixture it writes the file; run without, it produces
// the same document and requires the checked in bytes to equal it. So the
// fixture cannot rot: the day a field is renamed in workload.Measured, this
// goes red in the same commit rather than the control plane's suite going green
// over a document no engine would send.
//
// THE OTHER FOUR ARE DELIBERATELY NOT TOUCHED. They carry timestamps, an engine
// commit and numbers from the run that produced them, and rewriting them from a
// fixture this file invented would replace real bytes with synthetic ones,
// which is the exact property the README protects. They stay as they are.

var updateWireFixture = flag.Bool("update-wire-fixture", false,
	"rewrite the sql_workload wire fixture the control plane's decoder is tested against")

// wireFixturePath is where the control plane's suite reads it from.
func wireFixturePath() string {
	return filepath.Join("..", "..", "..", "web", "apps", "api", "test",
		"fixtures", "engine-reports", "sql-workload.json")
}

func TestTheSQLWorkloadWireFixtureIsWhatThisEngineWouldSend(t *testing.T) {
	payload := hostedPayload(sqlWorkloadResult(t), "3c0f1f6e-0f2e-4a1c-9d3a-9b0b7a4b1f11", "sql_workload")
	body, err := json.MarshalIndent(payload, "", "  ")
	require.NoError(t, err)
	body = append(body, '\n')

	path := wireFixturePath()
	if *updateWireFixture {
		require.NoError(t, os.WriteFile(path, body, 0o644))
		return
	}

	existing, err := os.ReadFile(path)
	require.NoErrorf(t, err,
		"%s is missing. Regenerate it with: go test ./internal/cli "+
			"-run TestTheSQLWorkloadWireFixtureIsWhatThisEngineWouldSend -update-wire-fixture", path)
	require.Equalf(t, string(body), string(existing),
		"the checked in wire fixture is not what this engine would send. The control plane's "+
			"decoder is tested against these bytes, so a difference here is the seam drifting. "+
			"Regenerate it with: go test ./internal/cli "+
			"-run TestTheSQLWorkloadWireFixtureIsWhatThisEngineWouldSend -update-wire-fixture")
}

// sqlWorkloadResult runs a SQL workload through the real Execute.
//
// The runner is a fake and the projection is not: what is being captured is the
// shape workload.Execute produces, and a hand built Result would be the same
// self-agreement the fixtures exist to avoid. The numbers are chosen to be
// distinguishable from one another, so a decoder that read the wrong field
// lands on a value the control plane's suite can name.
func sqlWorkloadResult(t *testing.T) *workload.Result {
	t.Helper()
	plan, err := workload.Parse(workload.Request{
		Kind: "sql_workload", Duration: "60s", Seed: "7", Concurrency: "8",
	})
	require.NoError(t, err)

	peak, seen := 8, 8
	res, err := workload.Execute(context.Background(), workload.Options{
		Plan: plan,
		Runner: &fixtureRunner{
			envID: "aftest-04e645",
			url:   "http://127.0.0.1:8081",
			sql: &sqlload.Result{
				Source:  sqlload.SourceStatementStatistics,
				Clients: 8, Transactions: 4212, TransactionsFailed: 18, Retries: 31,
				Deadlocks: 22, SerializationFailures: 9,
				Statements: 8424, StatementsFailed: 18, Rows: 51907,
				Duration: 60 * time.Second, TPS: 70.2, ErrorRate: 0.004255,
				Overall: load.Latency{P50Ms: 22.4, P90Ms: 61.8, P95Ms: 96.3, P99Ms: 210.5, MaxMs: 744},
				PerTransaction: []sqlload.TransactionResult{
					{
						Name: "SELECT id, status FROM orders WHERE id = $1", Executed: 3180,
						Weight:    4000,
						Latency:   load.Latency{P50Ms: 18.1, P90Ms: 44.2, P95Ms: 71.5, P99Ms: 160.2, MaxMs: 520},
						Baselines: sqlload.Baseline{MeanMs: 0.685, Has: true, MeanIncrease: 0.42},
					},
					{
						Name: "UPDATE orders SET status = $1 WHERE id = $2", Executed: 1032,
						Failed: 18, Retries: 31, Weight: 1200,
						Latency:   load.Latency{P50Ms: 31.7, P90Ms: 88.4, P95Ms: 140.1, P99Ms: 260.9, MaxMs: 744},
						Baselines: sqlload.Baseline{MeanMs: 3.471, Has: true, MeanIncrease: 0.11},
					},
				},
				PerStatement: []sqlload.StatementResult{
					{
						Transaction: "UPDATE orders SET status = $1 WHERE id = $2",
						Label:       "UPDATE orders SET status = $1 WHERE id = $2",
						Executed:    1032, Errors: 18, Rows: 1014,
						Latency: load.Latency{P50Ms: 31.7, P90Ms: 88.4, P95Ms: 140.1, P99Ms: 260.9, MaxMs: 744},
					},
					{
						Transaction: "SELECT id, status FROM orders WHERE id = $1",
						Label:       "SELECT id, status FROM orders WHERE id = $1",
						Executed:    3180, Rows: 50893,
						Latency: load.Latency{P50Ms: 18.1, P90Ms: 44.2, P95Ms: 71.5, P99Ms: 160.2, MaxMs: 520},
					},
				},
				Errors: map[string]int{"deadlock": 22, "serialization failure": 9, "SQLSTATE 23505": 18},
				Refused: []sqlload.Refused{{
					Statement: "DELETE FROM order_lines WHERE order_id = $1",
					Code:      sqlload.RefusedWrite,
					Reason: "the statement changes data and its values were normalised away, so " +
						"replaying it would write values nobody chose",
				}},
				PeakActiveBackends: &peak, PeakOpenTransactions: &peak, BackendsSeen: &seen,
			},
			meanIncrease: 0.25,
			errorRate:    0.01,
		},
		Clock:          clock.NewFake(time.Date(2026, time.September, 20, 6, 40, 0, 0, time.UTC)),
		Engine:         workload.Engine{Version: "0.0.0-dev", Commit: "4206c1ad", Edition: "oss"},
		Branch:         "w-sql-workload",
		ManifestDigest: "sha256:0d1e2f",
	})
	require.NoError(t, err)
	return res
}

// fixtureRunner answers the one path this fixture needs and refuses the rest.
//
// Refuses rather than returns nil, because a fixture generator that silently
// produced an empty document for the wrong kind is the kind of quiet the whole
// exercise is about.
type fixtureRunner struct {
	envID                   string
	url                     string
	sql                     *sqlload.Result
	meanIncrease, errorRate float64
}

func (f *fixtureRunner) EnvID() string { return f.envID }

func (f *fixtureRunner) Status(context.Context) (*env.Result, error) {
	return &env.Result{EnvID: f.envID, URL: f.url}, nil
}

func (f *fixtureRunner) Load(context.Context, env.LoadOptions) (*load.Result, []load.Route, error) {
	return nil, nil, errFixtureKind
}

func (f *fixtureRunner) Scenarios(context.Context, env.ScenarioOptions) ([]load.ScenarioResult, error) {
	return nil, errFixtureKind
}

func (f *fixtureRunner) Test(context.Context, env.TestOptions) (*env.TestReport, error) {
	return nil, errFixtureKind
}

func (f *fixtureRunner) Explore(context.Context, env.ExploreOptions) (*explore.Report, error) {
	return nil, errFixtureKind
}

func (f *fixtureRunner) SQLLoad(context.Context, env.SQLLoadOptions) (*sqlload.Result, *env.SQLLoadPlan, error) {
	return f.sql, &env.SQLLoadPlan{Clients: f.sql.Clients, Duration: f.sql.Duration}, nil
}

func (f *fixtureRunner) Thresholds() (float64, float64) { return 0, 0 }

func (f *fixtureRunner) SQLThresholds() (float64, float64) {
	return f.meanIncrease, f.errorRate
}

func (f *fixtureRunner) Down(context.Context) (*env.Teardown, error) { return nil, errFixtureKind }

var errFixtureKind = errFixture("this fixture runner answers the SQL workload path only")

type errFixture string

func (e errFixture) Error() string { return string(e) }
