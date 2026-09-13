package env

import (
	"context"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/stretchr/testify/require"
)

// progressDB is a provider that waits inside Branch and says so, the shape of a
// managed database waiting for its cloud's first backup.
type progressDB struct {
	*fakeDB
	report func(string)
}

func (d *progressDB) ReportProgressTo(report func(string)) { d.report = report }

func (d *progressDB) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	if d.report != nil {
		d.report("waiting for the test cloud's first backup of " + version)
	}
	return d.fakeDB.Branch(ctx, version, envID)
}

func TestAProviderWaitReachesTheEventStreamAsProgress(t *testing.T) {
	database := &progressDB{fakeDB: newFakeDB("acmedb")}
	runtime := &fakeRT{name: "acmert"}
	registry := extension.NewRegistry()
	registry.AddDatabaseProvider(trustRegistration{database})
	registry.AddRuntimeProvider(&fakeRTProvider{name: "acmert", rt: runtime})
	orchestrator := registeredOrchestrator(t, registry)
	sink := events.NewMemorySink(1024)
	orchestrator.AddSink(sink)

	_, err := orchestrator.Up(context.Background())
	require.NoError(t, err)

	var lines []string
	for _, e := range sink.OfType(events.Progress) {
		lines = append(lines, e.Msg)
	}
	found := false
	for _, line := range lines {
		if strings.Contains(line, "waiting for the test cloud's first backup of") {
			found = true
		}
	}
	require.Truef(t, found, "a provider reported a wait and no engine.progress event carried it, so "+
		"af up would have been silent for as long as the wait lasted. Progress events seen: %q", lines)
}
