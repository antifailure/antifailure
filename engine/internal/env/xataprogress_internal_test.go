package env

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	xatadb "github.com/antifailure/antifailure/engine/internal/db/xata"
	"github.com/antifailure/antifailure/engine/internal/db/xata/fakexata"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// A built in provider's wait reaches the event stream.
//
// TestAProviderWaitReachesTheEventStreamAsProgress proves the attach with a
// provider that arrives through the extension registry. xata arrives through
// the engine's own switch instead, and nothing had shown that a built in
// provider's lines take the same road. A real Up cannot show it here: the
// switch builds xata against api.xata.tech, and a base URL setting added to
// make a test reachable would be a user facing claim of self hosted Xata
// support. So this builds the session the way openLocking leaves it, a bus and
// the database provider, puts the real Xata provider over its fake control
// plane in it, and calls the exact function openLocking calls.

// requireXataProgressPostgres is the local Postgres the fake keeps its branches
// on, or a skip, or a failure where AF_REQUIRE_DATABASE says a skip would be a
// green run that proved nothing.
func requireXataProgressPostgres(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("AF_XATA_TEST_DATABASE_URL")
	if raw == "" {
		raw = os.Getenv("AF_TEST_DATABASE_URL")
	}
	if raw == "" {
		raw = "postgres://postgres:test@127.0.0.1:55432/antifailure"
	}
	db, err := sql.Open("pgx", raw)
	if err == nil {
		defer func() { _ = db.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = db.PingContext(ctx)
	}
	if err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and the local Postgres did not answer: %v", err)
		}
		t.Skipf("skipped: no Postgres answered: %v", err)
	}
	return raw
}

func TestAXataWaitReachesTheEventStreamThroughTheAttach(t *testing.T) {
	admin := requireXataProgressPostgres(t)
	ctx := context.Background()

	f, err := fakexata.New(fakexata.Options{AdminURL: admin})
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, problem := range f.Close() {
			t.Errorf("the fake control plane could not clean up: %v", problem)
		}
	})
	f.SetPendingPolls(3)

	c := clock.New()
	database, err := xatadb.New(xatadb.Options{
		APIKey: secrets.New("xau_env_progress"), OrgID: f.Org(), ProjectID: f.Project(),
		BaseURL: f.URL(), Clock: c,
		SeedSQL:      "CREATE TABLE IF NOT EXISTS af_progress_probe (id int)",
		PollInterval: 10 * time.Millisecond, PollTimeout: 10 * time.Second,
	})
	require.NoError(t, err)

	bus := events.NewBus(c)
	sink := events.NewMemorySink(256)
	bus.AddSink(sink)
	var printed []string
	o, err := New(Options{
		Root: t.TempDir(), Branch: "main", Clock: c, Redactor: redact.New(),
		Manifest: &schema.Manifest{Name: "app", Database: &schema.Database{
			Provider: schema.DBXata, Project: f.Org() + "/" + f.Project(),
		}},
		Getenv:   func(string) string { return "" },
		Progress: func(line string) { printed = append(printed, line) },
	})
	require.NoError(t, err)
	s := &session{bus: bus, dbProv: database}

	o.attachDatabaseProgress(s)

	var major int
	probe, err := sql.Open("pgx", admin)
	require.NoError(t, err)
	require.NoError(t, probe.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int / 10000").Scan(&major))
	_ = probe.Close()

	gv, err := database.RefreshGolden(ctx, provider.GoldenSpec{Version: major, RulesHash: "envprog1"})
	require.NoError(t, err)
	_, err = database.Branch(ctx, gv.ID, "env_progress")
	require.NoError(t, err)

	var published []string
	for _, e := range sink.OfType(events.Progress) {
		published = append(published, e.Msg)
	}
	contains := func(lines []string, fragment string) bool {
		for _, l := range lines {
			if strings.Contains(l, fragment) {
				return true
			}
		}
		return false
	}
	require.Truef(t, contains(published, "xata: waiting for branch "),
		"the Xata provider waited for a branch and no engine.progress event carried it, so "+
			"a built in provider's wait never reached the stream the dashboard and af up read. "+
			"Progress events seen: %q", published)
	require.Truef(t, contains(published, " is ready after "),
		"and the end of the wait was not published either. Progress events seen: %q", published)
	require.Truef(t, contains(printed, "xata: waiting for branch "),
		"the line reached the event stream and was not printed, so a terminal running af up "+
			"stayed silent. Printed: %q", printed)
}
