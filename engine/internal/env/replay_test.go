package env

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/oracle"
	"github.com/antifailure/antifailure/engine/internal/replay"
	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/stretchr/testify/require"
)

func TestSaveIncidentDoesNotRelockPublicationOrRepublishRetiredIdentity(t *testing.T) {
	o, err := New(Options{Root: t.TempDir(), Manifest: replayManifest()})
	require.NoError(t, err)
	incident := replay.Incident{SchemaVersion: 1, RunID: "one", TraceID: strings.Repeat("a", 32), Project: "billing", Service: "agent", Commit: strings.Repeat("b", 40), ObservedAt: "2026-09-27T00:00:00Z", PolicyVersion: "1", Input: json.RawMessage(`{"id":1}`), InputHash: strings.Repeat("c", 64), Status: "complete", Clock: "sdk", Identity: "synthetic"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = o.SaveIncident(ctx, incident, "case", "", "/af-replay", "local", replay.Assertion{Baseline: json.RawMessage(`false`), Expected: json.RawMessage(`true`)}, []string{"subscriptions"})
	require.ErrorContains(t, err, "pin a verified golden", "the save passes import and publication locking before the expected validation error")
	require.NoError(t, o.ReplayStore().Put("retired", "prior-case", map[string]any{"id": "prior-case", "incidents": []string{"one"}}))
	_, err = o.SaveIncident(ctx, incident, "new-case", "gv-one", "/af-replay", "local", replay.Assertion{Baseline: json.RawMessage(`false`), Expected: json.RawMessage(`true`)}, []string{"subscriptions"})
	require.ErrorContains(t, err, "incident is retired")
	_, err = o.ReplayStore().Read("scenarios", "new-case")
	require.Error(t, err)
}

func TestReplayDatabaseFindingsNeverCopyRowValuesIntoReports(t *testing.T) {
	snapshot := func(password string) *oracle.Snapshot {
		return &oracle.Snapshot{Tables: []oracle.Table{{Schema: "public", Name: "accounts", Key: []string{"id"},
			Columns: []oracle.Column{{Name: "id", Type: "text"}, {Name: "password", Type: "text"}},
			Rows:    map[string]map[string]any{`["private-row-key"]`: {"id": "private-row-key", "password": password}}, RowCount: 1,
		}}}
	}
	before, after := snapshot("short-before"), snapshot("short-after")
	comparison := oracle.Compare(oracle.Input{BaselineBefore: before, BaselineAfter: before, CandidateBefore: before, CandidateAfter: after, Database: oracle.DatabaseOptions{Include: []string{"accounts"}}})
	raw, err := json.Marshal(comparison)
	require.NoError(t, err)
	require.Contains(t, string(raw), "short-after", "the real comparator must exercise the leaking display shape")
	require.NotEmpty(t, comparison.Findings)
	body, err := replayDatabaseEvidence(comparison)
	require.NoError(t, err)
	report := replay.Report{ID: "attempt", Verdict: "FAIL", Database: body}
	s := replay.Store{Root: t.TempDir()}
	require.NoError(t, s.Put("attempts", report.ID, report))
	written, err := s.Read("attempts", report.ID)
	require.NoError(t, err)
	for _, value := range []string{"short-before", "short-after", "private-row-key"} {
		require.NotContains(t, string(written), value)
	}
	require.Contains(t, string(written), "public.accounts")
	require.Contains(t, string(written), `"verdict":"FAIL"`)
}

func replayManifest() *schema.Manifest {
	return &schema.Manifest{Name: "billing", Version: 1, Database: &schema.Database{Provider: "docker", Version: 17}, Services: []schema.Service{{Name: "agent", Kind: "web"}}, Egress: &schema.Egress{Default: "block"}}
}

func TestReplayAcceptsThePrimaryDatastoreAddedByTheRealManifestLoader(t *testing.T) {
	m, err := manifest.Parse([]byte("version: 1\nname: billing\nservices:\n  - name: agent\n    port: 3000\n    build:\n      strategy: image\n      image: node:24-alpine\ndatabase:\n  provider: docker\n  version: 17\negress:\n  default: block\n"), "antifailure.yaml", t.TempDir())
	require.NoError(t, err)
	require.NotEmpty(t, m.Datastores)
	require.NoError(t, replayHarness(m))
}

func TestReplayHarnessRefusesUncontrolledSourcesBeforeStartingAnything(t *testing.T) {
	cases := []struct {
		name string
		edit func(*schema.Manifest)
	}{
		{"live egress", func(m *schema.Manifest) { m.Egress.Default = "allow" }},
		{"sandbox credential", func(m *schema.Manifest) {
			m.Egress.Rules = []schema.EgressRule{{Host: "api.example.test", Mode: "sandbox"}}
		}},
		{"remote database", func(m *schema.Manifest) { m.Database.Provider = "pgurl" }},
		{"secret lookup", func(m *schema.Manifest) { m.Services[0].Env = []schema.EnvVar{{Name: "API_KEY"}} }},
		{"literal password", func(m *schema.Manifest) {
			m.Services[0].Env = []schema.EnvVar{{Name: "PASSWORD", Value: "real-password"}}
		}},
		{"remote runtime", func(m *schema.Manifest) { m.Runtime = &schema.Runtime{Provider: "kubernetes"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { m := replayManifest(); tc.edit(m); require.Error(t, replayHarness(m)) })
	}
}

func TestReplayConstructsNoProductionSecretSourceAndPreservesGoldenIdentityInputs(t *testing.T) {
	m := replayManifest()
	m.Database.SourceURLEnv = "PRODUCTION_DATABASE_URL"
	parent, err := New(Options{Root: t.TempDir(), Manifest: m, Getenv: func(string) string { t.Fatal("production environment was read"); return "" }})
	require.NoError(t, err)
	child, err := parent.replayOrchestrator(m, "attempt-baseline", "gv-pinned", "")
	require.NoError(t, err)
	require.NotNil(t, child.opts.Secrets)
	require.Equal(t, "", child.opts.Getenv("PRODUCTION_DATABASE_URL"))
	require.Equal(t, "gv-pinned", child.opts.PinGolden)
	require.Equal(t, "PRODUCTION_DATABASE_URL", child.opts.Manifest.Database.SourceURLEnv)
	require.Empty(t, child.opts.ControlPlaneURL)
	require.NotNil(t, child.opts.Extensions)
}

func TestIncompleteDatabaseCaptureIsNotPositiveEvidence(t *testing.T) {
	for _, snapshot := range []*oracle.Snapshot{nil, {Notes: []string{"read failed"}}, {Tables: []oracle.Table{{Schema: "public", Name: "subscriptions", Truncated: true}}}, {Tables: []oracle.Table{}}} {
		require.Error(t, completeSnapshot(snapshot, []string{"subscriptions"}))
	}
	require.NoError(t, completeSnapshot(&oracle.Snapshot{Tables: []oracle.Table{{Schema: "public", Name: "subscriptions"}}}, []string{"subscriptions"}))
}

func TestMissingScenarioHasAnInspectableInconclusiveReport(t *testing.T) {
	o, err := New(Options{Root: t.TempDir(), Manifest: replayManifest()})
	require.NoError(t, err)
	report, err := o.Replay(context.Background(), "missing", "HEAD")
	require.NoError(t, err)
	require.Equal(t, "INCONCLUSIVE", report.Verdict)
	require.NotEmpty(t, report.Issues)
	body, err := o.ReplayStore().Read("attempts", report.ID)
	require.NoError(t, err)
	var restored replay.Report
	require.NoError(t, replay.Decode(body, &restored))
	require.Equal(t, "INCONCLUSIVE", restored.Verdict)
}
