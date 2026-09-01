package env

// What a golden refresh puts on the event stream while it masks and verifies.
//
// The catalog in internal/events documents six masking types, the reference
// page under docs/reference/schemas is generated from that catalog, and the
// dashboard's database pane is drawn from nothing else. None of the six was
// ever emitted. maskDatabase and verifyDatabase reported their work with
// o.progress, which reaches a terminal and no sink, and dashboard mode silences
// the terminal, so the longest and least visible part of a refresh drew a pane
// that stayed empty and said unverified.
//
// These tests run against the standing Postgres rather than against a fixture,
// because the events carry counts that come from a real catalog and a real
// scan: a test that asserted the types alone would pass on a plan with no
// tables in it.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// maskEventsURL is the standing server `just db` starts, the same address the
// insights, invariant and subset suites use.
const maskEventsURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

func maskEventsBase() string {
	if u := os.Getenv("AF_TEST_DATABASE_URL"); u != "" {
		return u
	}
	return maskEventsURL
}

// maskEventsDatabase gives the test its own database with one table of real
// looking data, and returns the URL to it.
func maskEventsDatabase(t *testing.T, ddl string) secrets.Value {
	t.Helper()
	ctx := t.Context()
	base := maskEventsBase()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		// A machine with no test Postgres has not found a bug. A machine that
		// was supposed to have one has found a large one, which is what
		// AF_REQUIRE_DATABASE is for elsewhere in this repository.
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no usable Postgres: %v", err)
		}
		t.Skipf("no Postgres at %s: %v", base, err)
	}
	defer func() { _ = admin.Close(context.Background()) }()

	name := fmt.Sprintf("af_maskevents_%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, connErr := pgx.Connect(c, base)
		if connErr != nil {
			return
		}
		defer func() { _ = conn.Close(context.Background()) }()
		_, _ = conn.Exec(c, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})

	url := base[:strings.LastIndex(base, "/")] + "/" + name
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	_, err = conn.Exec(ctx, ddl)
	require.NoError(t, err)

	return secrets.New(url)
}

// maskEventsOrchestrator is the smallest orchestrator that can mask: a clock,
// a redactor, and somewhere for progress lines to go. No lock, no provider and
// no runtime, because maskDatabase and verifyDatabase take a connection URL and
// touch none of them.
func maskEventsOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()
	return &Orchestrator{
		envID:    "env_maskevents",
		opts:     Options{Root: t.TempDir(), Clock: clock.New(), Redactor: redact.New()},
		progress: func(string) {},
	}
}

func maskEventsRules(t *testing.T) *masking.RuleSet {
	t.Helper()
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	return rules
}

func maskEventsKey(t *testing.T) *masking.Key {
	t.Helper()
	key, err := masking.NewKey(secrets.New("a-project-key-long-enough-to-be-accepted"))
	require.NoError(t, err)
	return key
}

const maskEventsSchema = `
CREATE TABLE customers (
  id    bigserial PRIMARY KEY,
  email text NOT NULL,
  name  text NOT NULL
);
CREATE TABLE orders (
  id          bigserial PRIMARY KEY,
  customer_id bigint NOT NULL REFERENCES customers(id),
  email       text NOT NULL
);
INSERT INTO customers (email, name) VALUES
  ('ada@example.com','Ada Lovelace'), ('grace@example.com','Grace Hopper');
INSERT INTO orders (customer_id, email) VALUES
  (1,'ada@example.com'), (2,'grace@example.com');
`

// The test that would have caught it. Before this change the sink received
// nothing at all from a masking run, and every assertion below fails.
func TestMaskDatabase_PutsThePlanAndTheProgressOnTheStream(t *testing.T) {
	url := maskEventsDatabase(t, maskEventsSchema)
	o := maskEventsOrchestrator(t)

	bus := events.NewBus(o.opts.Clock)
	sink := events.NewMemorySink(256)
	bus.AddSink(sink)
	s := &session{bus: bus}

	rows, tables, err := o.maskDatabase(t.Context(), s, url, maskEventsKey(t), maskEventsRules(t), "h1")
	require.NoError(t, err)
	require.Positive(t, rows, "the fixture has rows, so masking should have rewritten some")
	require.NoError(t, bus.Close())

	planned := sink.OfType(events.MaskPlanned)
	require.Len(t, planned, 1, "a refresh announced no plan, so the pane has nothing to count against")
	require.Equal(t, tables, toIntField(t, planned[0].Data["tables"]))
	require.Positive(t, toIntField(t, planned[0].Data["columns"]),
		"a plan that masks no columns is not a plan")

	progress := sink.OfType(events.MaskProgress)
	require.Len(t, progress, tables, "one finished table, one progress event")
	require.Equal(t, 100, toIntField(t, progress[len(progress)-1].Data["percent"]),
		"the last table finishing should read as complete")

	applied := sink.OfType(events.MaskApplied)
	require.Len(t, applied, 1)
	require.Equal(t, rows, toInt64Field(t, applied[0].Data["rows"]))
	require.Equal(t, tables, toIntField(t, applied[0].Data["tables"]))

	for _, e := range append(append(planned, progress...), applied...) {
		require.Equal(t, o.EnvID(), e.Env,
			"an event with no environment cannot be filtered by any display")
	}
}

// Percentage rises rather than jumping from nothing to complete. The pane draws
// a bar from it, and a bar that is zero until it is one is a bar that says the
// same thing as no bar at all.
func TestMaskDatabase_ProgressRisesTableByTable(t *testing.T) {
	url := maskEventsDatabase(t, maskEventsSchema)
	o := maskEventsOrchestrator(t)

	bus := events.NewBus(o.opts.Clock)
	sink := events.NewMemorySink(256)
	bus.AddSink(sink)

	_, tables, err := o.maskDatabase(
		t.Context(), &session{bus: bus}, url, maskEventsKey(t), maskEventsRules(t), "h1")
	require.NoError(t, err)
	require.NoError(t, bus.Close())
	require.Greater(t, tables, 1, "the fixture needs more than one table for this to mean anything")

	previous := 0
	for _, e := range sink.OfType(events.MaskProgress) {
		got := toIntField(t, e.Data["percent"])
		require.Greater(t, got, previous, "progress went backwards or stalled")
		previous = got
	}
	require.Equal(t, 100, previous)
}

// A clean verification says so on the stream. The pane reads verified from
// exactly this event, and a run that never emits it draws a verified golden as
// unverified.
func TestVerifyDatabase_SaysVerifiedOnTheStream(t *testing.T) {
	url := maskEventsDatabase(t, maskEventsSchema)
	o := maskEventsOrchestrator(t)

	bus := events.NewBus(o.opts.Clock)
	sink := events.NewMemorySink(256)
	bus.AddSink(sink)
	s := &session{bus: bus}

	_, _, err := o.maskDatabase(t.Context(), s, url, maskEventsKey(t), maskEventsRules(t), "h1")
	require.NoError(t, err)

	report, attestation, err := o.verifyDatabase(t.Context(), s, url, "h1", "gp1-test")
	require.NoError(t, err)
	require.NotEmpty(t, attestation)
	require.NoError(t, bus.Close())

	require.Len(t, sink.OfType(events.MaskVerifying), 1,
		"the scan is the second longest part of a refresh and it announced nothing")

	verified := sink.OfType(events.MaskVerified)
	require.Len(t, verified, 1)
	require.Equal(t, true, verified[0].Data["verified"],
		"the pane reads this field, and without it a verified golden draws as unverified")
	require.Equal(t, report.Columns, toIntField(t, verified[0].Data["columns"]))
	require.Empty(t, sink.OfType(events.MaskFinding), "a clean scan should report no findings")
}

// A refusal reports every finding, at error level, not only the one the error
// names. Somebody looking at a failed refresh needs to know whether one column
// leaked or forty.
func TestVerifyDatabase_ReportsEveryFindingAtErrorLevel(t *testing.T) {
	// Not masked, and deliberately not on example.com. The detectors treat the
	// reserved test domains as evidence of nothing, correctly, so a fixture
	// using one would verify clean and this test would prove that findings
	// reach the stream by never producing one.
	url := maskEventsDatabase(t, strings.ReplaceAll(maskEventsSchema, "example.com", "acme-retail.co"))
	o := maskEventsOrchestrator(t)

	bus := events.NewBus(o.opts.Clock)
	sink := events.NewMemorySink(256)
	bus.AddSink(sink)

	report, _, err := o.verifyDatabase(t.Context(), &session{bus: bus}, url, "h1", "gp1-test")
	require.Error(t, err, "an unmasked database must not verify clean")
	require.NoError(t, bus.Close())

	findings := sink.OfType(events.MaskFinding)
	require.Len(t, findings, len(report.Findings),
		"the refusal names one finding and the stream should carry all of them")
	require.NotEmpty(t, findings)
	for _, e := range findings {
		require.Equal(t, events.LevelError, e.Level,
			"a finding at info level is a leak that draws in the scrolling tail")
		require.NotEmpty(t, e.Data["detector"])
		require.NotEmpty(t, e.Data["table"])
		require.NotEmpty(t, e.Data["column"])
	}
	require.Empty(t, sink.OfType(events.MaskVerified),
		"a refused scan must not report the golden as verified")
}

// Fields cross the bus as any, so a test that compared against an int would
// pass or fail on the encoding rather than on the value.
func toIntField(t *testing.T, v any) int {
	t.Helper()
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	t.Fatalf("%v is not a number", v)
	return 0
}

func toInt64Field(t *testing.T, v any) int64 {
	t.Helper()
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	t.Fatalf("%v is not a number", v)
	return 0
}
