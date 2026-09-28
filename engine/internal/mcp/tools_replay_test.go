package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/replay"
	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/stretchr/testify/require"
)

func TestAgentIncidentInspectionIsPaginatedAndProjectBound(t *testing.T) {
	p := testProject(t)
	p.Manifest = &schema.Manifest{Version: 1, Name: p.ID}
	o, err := agentReplayOrchestrator(p)
	require.NoError(t, err)
	incident := replay.Incident{SchemaVersion: 1, RunID: "one", TraceID: strings.Repeat("a", 32), Project: p.ID, Service: "agent", Commit: strings.Repeat("b", 40), ObservedAt: "2026-09-27T00:00:00Z", PolicyVersion: "1", InputHash: strings.Repeat("c", 64), Status: "complete", Clock: "sdk", Identity: "synthetic"}
	for n := 0; n < 65; n++ {
		incident.Exchanges = append(incident.Exchanges, replay.Exchange{Seq: n, Name: "search", Version: "1", Kind: "tool", Key: strings.Repeat("d", 64), Provenance: "recorded"})
	}
	require.NoError(t, o.ReplayStore().ImportIncident(context.Background(), incident))
	tool := newInspectAgentIncidentTool(p)
	out := mustInvoke(t, tool, `{"project_id":"test-project","incident_id":"one"}`).(map[string]any)
	require.Len(t, out["boundaries"], 50)
	require.Equal(t, "50", out["next_cursor"])
	next := mustInvoke(t, tool, `{"project_id":"test-project","incident_id":"one","cursor":"50"}`).(map[string]any)
	require.Len(t, next["boundaries"], 15)
	require.Equal(t, "", next["next_cursor"])
	_, fault := tool.Handler(context.Background(), &Call{}, map[string]any{"project_id": "another-project", "incident_id": "one"})
	require.NotNil(t, fault)
}
