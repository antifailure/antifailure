package clickhouse_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/datastore/clickhouse"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The live tests, which are the only kind worth having here.
//
// Everything this package does is a statement a server either accepts or does
// not: a CREATE TABLE retargeted at another database, an ATTACH PARTITION, an
// EXCHANGE TABLES, a join written against a value map. Text that has never
// been executed is a guess about a grammar, and this repository has shipped a
// host pattern that did not compile and an audit sink nothing called.
//
// They skip by name when no ClickHouse is reachable, the way every cloud
// conformance test here does, so the community suite needs nothing installed.
// AF_TEST_CLICKHOUSE_URL points them at a server somebody already has;
// otherwise the engine's own managed server is started, which is the same code
// path `af up` takes and is therefore worth exercising on every run.

// requireServer returns the admin URL of a ClickHouse, or skips by name.
func requireServer(t *testing.T) secrets.Value {
	t.Helper()
	if u := os.Getenv("AF_TEST_CLICKHOUSE_URL"); u != "" {
		return secrets.New(u)
	}
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set and AF_TEST_CLICKHOUSE_URL names no server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	server, err := clickhouse.EnsureLocalServer(ctx, clickhouse.LocalServerOptions{
		Progress: func(line string) { t.Log(line) },
	})
	if err != nil {
		// On a laptop with no Docker this is a skip: that machine has not
		// found a bug. On a machine that was SUPPOSED to have one it is a
		// failure, because a skip prints nothing and the package reports ok
		// having examined nothing, which is the state this whole file exists
		// to avoid being in.
		if os.Getenv("AF_REQUIRE_DOCKER") != "" {
			t.Fatalf("AF_REQUIRE_DOCKER is set, so this cannot be skipped: "+
				"no ClickHouse is reachable and one could not be started: %v", err)
		}
		t.Skipf("skipped: no ClickHouse is reachable and one could not be started: %v", err)
	}
	// The container is NOT removed. It is the machine's server, shared by
	// every environment and every other run on it, and removing it would take
	// every golden on the machine with it.
	return server.URL
}

// scratch is a database a test owns, dropped when it finishes.
type scratch struct {
	t    *testing.T
	name string
	url  secrets.Value
	base secrets.Value
}

// newScratch creates a database for one test.
//
// Named after the test and the clock, because these run against a server that
// other tests and other checkouts are also using, and a fixed name would mean
// two runs masking each other's tables.
func newScratch(t *testing.T, server secrets.Value, label string) *scratch {
	t.Helper()
	name := fmt.Sprintf("af_test_%s_%d", label, time.Now().UnixNano()%1e9)
	s := &scratch{t: t, name: name, base: server}
	s.url = withDatabase(t, server, name)
	s.execOn(server, "CREATE DATABASE "+name+" ENGINE = Atomic")
	t.Cleanup(func() { s.execOn(server, "DROP DATABASE IF EXISTS "+name) })
	return s
}

// exec runs a statement against the scratch database.
func (s *scratch) exec(sql string) { s.execOn(s.url, sql) }

func (s *scratch) execOn(target secrets.Value, sql string) {
	s.t.Helper()
	_, err := request(context.Background(), target, sql, nil)
	require.NoError(s.t, err, sql)
}

// query returns the rows of a statement as strings, in TSV, which is enough
// for a test asserting on values it wrote itself.
func (s *scratch) query(sql string) [][]string {
	s.t.Helper()
	body, err := request(context.Background(), s.url, sql+" FORMAT TSV", nil)
	require.NoError(s.t, err, sql)
	var out [][]string
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if line == "" {
			continue
		}
		out = append(out, strings.Split(line, "\t"))
	}
	return out
}

// column reads one column of a statement's result.
func (s *scratch) column(sql string) []string {
	rows := s.query(sql)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r[0])
	}
	return out
}

// request is the test's own HTTP client, deliberately separate from the
// package's.
//
// A test that read the server through the same code it is testing would agree
// with that code by construction. This one is written from the ClickHouse HTTP
// interface's documentation rather than from client.go.
func request(ctx context.Context, target secrets.Value, sql string, settings map[string]string) (string, error) {
	u, err := url.Parse(target.Reveal())
	if err != nil {
		return "", err
	}
	password, _ := u.User.Password()
	q := url.Values{}
	q.Set("database", strings.TrimPrefix(u.Path, "/"))
	q.Set("mutations_sync", "2")
	for k, v := range settings {
		q.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		u.Scheme+"://"+u.Host+"/?"+q.Encode(), strings.NewReader(sql))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(u.User.Username(), password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the server said %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

// withDatabase points a server URL at one database.
func withDatabase(t *testing.T, server secrets.Value, name string) secrets.Value {
	t.Helper()
	u, err := url.Parse(server.Reveal())
	require.NoError(t, err)
	u.Path = "/" + name
	return secrets.New(u.String())
}

// testKey is the project key the transforms derive from.
//
// Fixed, so that a test asserting one address masks to one other address gets
// the same answer on every run and a failure means the masking changed.
func testKey(t *testing.T) *masking.Key {
	t.Helper()
	key, err := masking.NewKey(secrets.New("l4.2-clickhouse-datastore-test-key"))
	require.NoError(t, err)
	return key
}

// eventsSchema is the shape this whole wave exists for.
//
// An events table with the identifier that joins it to the Postgres beside it,
// the properties blob an analytics product keeps its per person fields in, and
// a LowCardinality column, which is what a real schema gives every event name
// and which is the type most likely to be handled wrong by anything reading
// ClickHouse.
const eventsSchema = `
CREATE TABLE events (
  uuid        UUID,
  event       LowCardinality(String),
  distinct_id String,
  email       Nullable(String),
  person_id   Nullable(UUID),
  properties  String,
  ip          Nullable(String),
  ts          DateTime64(6)
) ENGINE = MergeTree PARTITION BY toYYYYMM(ts) ORDER BY uuid`

// insertEvents fills it with people whose addresses a detector recognises.
const insertEvents = `INSERT INTO events VALUES
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a01', 'pageview', 'ada@lovelace-analytics.co.uk',
   'ada@lovelace-analytics.co.uk', '01890fa1-9e40-7d3c-8b9a-2f5c6d7e8b01',
   '{"plan":"team"}', '81.2.69.142', '2026-09-07 10:00:00'),
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a02', 'pageview', 'grace@hopper-systems.io',
   'grace@hopper-systems.io', '01890fa1-9e40-7d3c-8b9a-2f5c6d7e8b02',
   '{"plan":"free"}', '81.2.69.160', '2026-08-14 10:01:00'),
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a03', 'click', 'alan@turing-labs.net',
   NULL, NULL,
   '{"plan":"team"}', NULL, '2026-08-14 10:02:00')`

// insertEventsWithoutNulls is the same three people with every column present.
//
// It exists for the equivalence test, and the reason is a limit of the
// dialect's per row statement rather than a convenience: ALTER TABLE ... UPDATE
// sets each planned column to a String parameter, and a ClickHouse query
// parameter has no way to say null. So the per row shape cannot write a null
// back at all, and comparing the two shapes on a fixture holding one would
// compare a shape that can express the value against a shape that cannot.
// What a null does is asserted against the requirement instead, in the test
// above, which is the stronger of the two checks anyway.
const insertEventsWithoutNulls = `INSERT INTO events VALUES
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a01', 'pageview', 'ada@lovelace-analytics.co.uk',
   'ada@lovelace-analytics.co.uk', '01890fa1-9e40-7d3c-8b9a-2f5c6d7e8b01',
   '{"plan":"team"}', '81.2.69.142', '2026-09-07 10:00:00'),
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a02', 'pageview', 'grace@hopper-systems.io',
   'grace@hopper-systems.io', '01890fa1-9e40-7d3c-8b9a-2f5c6d7e8b02',
   '{"plan":"free"}', '81.2.69.160', '2026-08-14 10:01:00'),
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a03', 'click', 'alan@turing-labs.net',
   'alan@turing-labs.net', '01890fa1-9e40-7d3c-8b9a-2f5c6d7e8b03',
   '{"plan":"team"}', '81.2.69.188', '2026-08-14 10:02:00')`

// liveRules are the rules these tests mask with.
//
// The properties rule is the one a person with this schema has to write: the
// same blob is jsonb in Postgres and a String in ClickHouse, and without it
// the classifier empties one as JSON and the other as free text.
func liveRules(t *testing.T) *masking.RuleSet {
	t.Helper()
	rules, err := masking.NewRuleSet([]masking.Rule{
		{Column: "distinct_id", Transform: "email", Link: "email",
			Why: "This product's distinct id is the person's address."},
		{Column: "properties", Type: "text", Transform: "empty_json",
			Why: "ClickHouse holds this JSON in a String."},
		{Column: "event", Transform: "preserve",
			Why: "An event name is not a person and a twin is useless without it."},
	})
	require.NoError(t, err)
	return rules
}

// mustURL is a connection string a test wrote itself.
func mustURL(s string) secrets.Value { return secrets.New(s) }
