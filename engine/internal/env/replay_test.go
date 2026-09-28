package env

import (
	"context"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/oracle"
	"github.com/antifailure/antifailure/engine/internal/replay"
	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/stretchr/testify/require"
)

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
