package env

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A name af logs is asked for is checked against the manifest.
//
// The runtime filters its containers by a label and answers a name it has no
// container for with zero lines and no error, which is also its answer for a
// service that has written nothing. So `af logs database`, in a project whose
// database is its database block, was answered as though a service by that
// name existed and was silent. These run Logs through a registered runtime, so
// they need no daemon and they prove where the refusal happens: before the
// runtime is asked anything.

// logRT is a runtime that can read logs and records who it was asked about.
type logRT struct {
	fakeRT
	lines []provider.LogLine
	asked []string
}

func (f *logRT) Logs(_ context.Context, _ string, service string, _ int) ([]provider.LogLine, error) {
	f.asked = append(f.asked, service)
	var out []provider.LogLine
	for _, l := range f.lines {
		if service == "" || l.Service == service {
			out = append(out, l)
		}
	}
	return out, nil
}

type logRTProvider struct{ rt *logRT }

func (p *logRTProvider) Name() string { return "logsrt" }

func (p *logRTProvider) Open(context.Context, extension.RuntimeConfig) (provider.Runtime, error) {
	return p.rt, nil
}

func logsOrchestrator(t *testing.T, m *schema.Manifest) (*Orchestrator, *logRT) {
	t.Helper()
	rt := &logRT{fakeRT: fakeRT{name: "logsrt"}, lines: []provider.LogLine{
		{Service: "ledger", Stream: "stdout", Text: "posted entry 42"},
	}}
	reg := extension.NewRegistry()
	reg.AddRuntimeProvider(&logRTProvider{rt: rt})
	m.Runtime = &schema.Runtime{Provider: "logsrt"}
	o, err := New(Options{
		Root: t.TempDir(), Manifest: m, Branch: "main",
		Clock: clock.New(), Redactor: redact.New(),
		Progress:   func(string) {},
		Extensions: reg,
		Getenv:     func(string) string { return "" },
	})
	require.NoError(t, err)
	return o, rt
}

// ledgerManifest is the project the defect was observed in: one service and a
// database block.
func ledgerManifest() *schema.Manifest {
	return &schema.Manifest{
		Version:  1,
		Name:     "ledger",
		Services: []schema.Service{{Name: "ledger"}, {Name: "web"}},
		Database: &schema.Database{Provider: schema.DBDocker},
	}
}

func TestLogsRefusesAnUndeclaredNameAndListsTheDeclaredOnes(t *testing.T) {
	o, rt := logsOrchestrator(t, ledgerManifest())
	_, err := o.Logs(context.Background(), "ledgr", 60)
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFRUN050, coded.Entry.Code)
	require.Contains(t, err.Error(), "no service called ledgr")
	require.Contains(t, err.Error(), "The services it declares are ledger and web.")
	require.Empty(t, rt.asked, "the runtime was asked for a name the manifest does not declare")
}

func TestLogsSaysTheDatabaseIsNotAService(t *testing.T) {
	o, rt := logsOrchestrator(t, ledgerManifest())
	_, err := o.Logs(context.Background(), "database", 60)
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFRUN051, coded.Entry.Code)
	require.Contains(t, err.Error(), "database is the manifest's database block, not a service")
	require.Contains(t, coded.NextStep(), "Run 'af logs' with no name to read every service: ledger and web.")
	require.Empty(t, rt.asked)
}

func TestLogsSaysADatastoreIsNotAService(t *testing.T) {
	m := ledgerManifest()
	m.Datastores = []schema.Datastore{{Name: "events", Engine: "clickhouse"}}
	o, _ := logsOrchestrator(t, m)
	_, err := o.Logs(context.Background(), "events", 60)
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFRUN051, coded.Entry.Code)
	require.Contains(t, err.Error(), "events is a clickhouse datastore the manifest declares")
}

func TestLogsReadsADeclaredServiceNamedLikeTheDatabase(t *testing.T) {
	// A declared service always wins over the database's names.
	m := ledgerManifest()
	m.Services = append(m.Services, schema.Service{Name: "db"})
	o, rt := logsOrchestrator(t, m)
	_, err := o.Logs(context.Background(), "db", 60)
	require.NoError(t, err)
	require.Equal(t, []string{"db"}, rt.asked)
}

func TestLogsStillReadsADeclaredServiceEverythingAndTheSidecar(t *testing.T) {
	o, rt := logsOrchestrator(t, ledgerManifest())
	lines, err := o.Logs(context.Background(), "ledger", 60)
	require.NoError(t, err)
	require.Equal(t, []provider.LogLine{{Service: "ledger", Stream: "stdout", Text: "posted entry 42"}}, lines)

	_, err = o.Logs(context.Background(), "", 60)
	require.NoError(t, err)
	_, err = o.Logs(context.Background(), local.ProxyAlias, 60)
	require.NoError(t, err, "the sidecar is readable by its alias and is not a service")
	require.Equal(t, []string{"ledger", "", local.ProxyAlias}, rt.asked)
}
