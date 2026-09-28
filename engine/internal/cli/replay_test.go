package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/replay"
	"github.com/stretchr/testify/require"
)

func replayCommandEnv(t *testing.T) (*Env, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "antifailure.yaml"), []byte("version: 1\nname: billing\nservices:\n  - name: agent\n    kind: web\n    port: 3000\n    build:\n      strategy: image\n      image: node:24-alpine\ndatabase:\n  provider: docker\n  version: 17\negress:\n  default: block\n"), 0o600))
	var out bytes.Buffer
	e := &Env{WorkDir: root, Clock: clock.New(), Out: NewOutput(&out, &out), Getenv: func(string) string { return "" }}
	e.Out.Format = FormatJSON
	return e, &out
}

func TestIncidentCLIImportsInspectsAndListsDamagedSiblings(t *testing.T) {
	e, out := replayCommandEnv(t)
	incident := replay.Incident{SchemaVersion: 1, RunID: "one", TraceID: strings.Repeat("a", 32), Project: "billing", Service: "agent", Commit: strings.Repeat("b", 40), ObservedAt: "2026-09-27T00:00:00Z", PolicyVersion: "1", InputHash: strings.Repeat("c", 64), Status: "complete", Clock: "sdk", Identity: "unmapped", Exchanges: []replay.Exchange{}, Issues: []string{}}
	body, err := json.Marshal(incident)
	require.NoError(t, err)
	path := filepath.Join(e.WorkDir, "capture.json")
	require.NoError(t, os.WriteFile(path, body, 0o600))
	for _, args := range [][]string{{"import", path}, {"import", path}, {"inspect", "one"}, {"list"}} {
		out.Reset()
		cmd := newIncidentCommand(e)
		cmd.SetArgs(args)
		require.NoError(t, cmd.ExecuteContext(context.Background()))
		require.True(t, json.Valid(out.Bytes()), out.String())
	}
	o, err := replayEngine(e)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(o.ReplayStore().Root, "incidents", "bad.json"), []byte(`[]`), 0o600))
	out.Reset()
	cmd := newIncidentCommand(e)
	cmd.SetArgs([]string{"list"})
	require.NoError(t, cmd.ExecuteContext(context.Background()))
	require.Contains(t, out.String(), `"error"`)
	var entries []replay.Entry
	require.NoError(t, json.Unmarshal(out.Bytes(), &entries))
	require.Equal(t, "one", entries[1].ID)
}

func TestReplayCLIMissingScenarioWritesOneJSONDocumentAndFails(t *testing.T) {
	e, out := replayCommandEnv(t)
	cmd := newReplayCommand(e)
	cmd.SetArgs([]string{"missing"})
	err := cmd.ExecuteContext(context.Background())
	require.Error(t, err)
	var report replay.Report
	require.NoError(t, json.Unmarshal(out.Bytes(), &report), out.String())
	require.Equal(t, "INCONCLUSIVE", report.Verdict)
	require.NotEmpty(t, report.ID)
}

func TestReplayTextNamesVerdictTeardownAndRecoveryEvidence(t *testing.T) {
	var out bytes.Buffer
	e := &Env{Out: NewOutput(&out, &out)}
	report := &replay.Report{ID: "rpl_test", Scenario: "billing", Verdict: "INCONCLUSIVE", Fidelity: "state-backed", Issues: []string{"teardown_unconfirmed"}}
	require.NoError(t, renderReplay(e, report))
	for _, text := range []string{"INCONCLUSIVE", "baseline false", "candidate false", "teardown_unconfirmed", "af replay inspect rpl_test"} {
		require.Contains(t, out.String(), text)
	}
}
