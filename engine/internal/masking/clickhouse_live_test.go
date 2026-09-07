package masking_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

// These run against a real ClickHouse, because the thing being checked is a
// vocabulary and a statement shape, and neither can be checked against a
// fixture written by the same person who wrote the code.
//
// A type table tested only against type names somebody typed into a test file
// proves that the two agree with each other. What it has to agree with is the
// server: system.columns is what a catalog reader will actually see, and a name
// this build spells differently is a column nothing classifies. The same goes
// for the statements. Text that has never been executed is a guess about a
// grammar, and this repository has shipped a host pattern that did not compile
// and an audit sink nothing called.
//
// They skip by name when no ClickHouse is reachable, the way every cloud
// conformance test here does, so the community suite needs nothing installed.
// AF_TEST_CLICKHOUSE_URL points them at a server somebody already has;
// otherwise one container is started and removed.

// clickhouseImage is the server these run against.
//
// Pinned to a minor version rather than latest, because a type this dialect
// places is a claim about a version and "whatever was newest that morning" is
// not a version anybody can reproduce.
const clickhouseImage = "clickhouse/clickhouse-server:25.3-alpine"

// chServer is a ClickHouse reachable over its HTTP interface.
//
// HTTP rather than a driver, and deliberately in the test rather than in the
// package. A ClickHouse client is a dependency decision that belongs with the
// provider that will need one, which is the lane after this. What this lane
// owes is that the statements and the type mapping are right, and the HTTP
// interface proves both without adding a module to the engine.
type chServer struct {
	base     string
	user     string
	password string
	database string
	// client is this server's own, rather than http.DefaultClient, so that the
	// connections it keeps alive can be closed when the test that made them
	// finishes. The package's TestMain checks for leaked goroutines, and a
	// pooled connection's read loop is one.
	client *http.Client
}

func (c *chServer) do(
	ctx context.Context, sql string, params, settings map[string]string,
) ([]byte, error) {
	q := url.Values{}
	q.Set("database", c.database)
	for k, v := range params {
		q.Set("param_"+k, v)
	}
	for k, v := range settings {
		q.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/?"+q.Encode(), strings.NewReader(sql))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.password)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clickhouse said %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// exec runs a statement that returns nothing.
//
// mutations_sync makes an ALTER UPDATE wait for its own mutation. A ClickHouse
// mutation is asynchronous by default, so without this the statement returns
// before it has rewritten anything and a test that read the rows back would be
// reading whichever half of the truth arrived first.
func (c *chServer) exec(t *testing.T, sql string, params map[string]string) {
	t.Helper()
	_, err := c.do(context.Background(), sql, params, map[string]string{"mutations_sync": "2"})
	require.NoError(t, err, sql)
}

// rows runs a statement and decodes RowBinaryWithNamesAndTypes.
//
// Binary rather than TSV, because a String in ClickHouse is bytes and the
// verification scanner's whole opinion about that type is that bytes which do
// not decode as text are bytes it could not read. A transport that escaped them
// would decide that question before the scanner saw it.
func (c *chServer) rows(ctx context.Context, sql string) ([]verify.Row, error) {
	body, err := c.do(ctx, sql+" FORMAT RowBinaryWithNamesAndTypes", nil, nil)
	if err != nil {
		return nil, err
	}
	return decodeRowBinary(body)
}

// decodeRowBinary reads the header and then the rows.
//
// It handles String and Nullable(String) and REFUSES anything else rather than
// guessing at a width, because a decoder that skipped an unexpected type would
// silently misalign every column after it.
func decodeRowBinary(body []byte) ([]verify.Row, error) {
	buf := bytes.NewBuffer(body)
	n, err := binary.ReadUvarint(buf)
	if err != nil {
		return nil, fmt.Errorf("reading the column count: %w", err)
	}
	for i := uint64(0); i < n; i++ {
		if _, err := readCHString(buf); err != nil {
			return nil, fmt.Errorf("reading column name %d: %w", i, err)
		}
	}
	nullable := make([]bool, n)
	for i := uint64(0); i < n; i++ {
		typ, err := readCHString(buf)
		if err != nil {
			return nil, fmt.Errorf("reading column type %d: %w", i, err)
		}
		switch string(typ) {
		case "String":
		case "Nullable(String)":
			nullable[i] = true
		default:
			return nil, fmt.Errorf(
				"column %d came back as %s and this decoder reads String and "+
					"Nullable(String) only; a statement that returns anything else has to "+
					"render it, because guessing at a width misaligns every column after it",
				i, typ)
		}
	}

	var out []verify.Row
	for buf.Len() > 0 {
		row := make(verify.Row, 0, n)
		for i := uint64(0); i < n; i++ {
			if nullable[i] {
				flag, err := buf.ReadByte()
				if err != nil {
					return nil, err
				}
				if flag == 1 {
					row = append(row, nil)
					continue
				}
			}
			v, err := readCHString(buf)
			if err != nil {
				return nil, err
			}
			row = append(row, v)
		}
		out = append(out, row)
	}
	return out, nil
}

func readCHString(buf *bytes.Buffer) ([]byte, error) {
	n, err := binary.ReadUvarint(buf)
	if err != nil {
		return nil, err
	}
	v := make([]byte, n)
	if _, err := io.ReadFull(buf, v); err != nil {
		return nil, err
	}
	return v, nil
}

// source is the verification source for this server.
//
// The rows are decoded from one response body, so this hands them on one at a
// time rather than streaming from the wire. That is the test harness being
// simple, not the interface: a provider reading the native protocol yields as
// it reads, and the scan holds one value whichever it is.
func (c *chServer) source() verify.Source {
	return verify.NewSource(verify.ClickHouse,
		func(ctx context.Context, sql string, yield func(verify.Row) error) error {
			rows, err := c.rows(ctx, sql)
			if err != nil {
				return err
			}
			for _, r := range rows {
				if err := yield(r); err != nil {
					return err
				}
			}
			return nil
		})
}

// catalog reads the tables and columns, the way a ClickHouse provider will.
//
// In the test rather than in the package for the reason the HTTP client is: the
// catalog reader belongs with the provider that opens the connection. What it
// is here for is to feed the dialect the server's OWN type names rather than
// ones written into a fixture.
func (c *chServer) catalog(t *testing.T) []masking.Table {
	t.Helper()
	ctx := context.Background()

	keys, err := c.rows(ctx, `
SELECT database, name, sorting_key FROM system.tables
WHERE database = currentDatabase() AND engine NOT LIKE '%View'`)
	require.NoError(t, err)
	sortingKey := map[string][]string{}
	for _, r := range keys {
		var cols []string
		for _, part := range strings.Split(string(r[2]), ",") {
			if part = strings.TrimSpace(part); part != "" {
				cols = append(cols, part)
			}
		}
		sortingKey[string(r[0])+"."+string(r[1])] = cols
	}

	cols, err := c.rows(ctx, `
SELECT database, table, name, type FROM system.columns
WHERE database = currentDatabase() ORDER BY database, table, position`)
	require.NoError(t, err)

	byTable := map[string]*masking.Table{}
	var order []string
	for _, r := range cols {
		schema, table := string(r[0]), string(r[1])
		key := schema + "." + table
		if _, ok := sortingKey[key]; !ok {
			continue
		}
		if _, ok := byTable[key]; !ok {
			byTable[key] = &masking.Table{
				Engine: "clickhouse", Schema: schema, Name: table,
				PrimaryKey: sortingKey[key],
			}
			order = append(order, key)
		}
		typ := string(r[3])
		byTable[key].Columns = append(byTable[key].Columns, masking.ColumnInfo{
			Name: string(r[2]), Type: typ,
			Nullable: strings.HasPrefix(typ, "Nullable(") ||
				strings.Contains(typ, "(Nullable("),
		})
	}
	out := make([]masking.Table, 0, len(order))
	for _, key := range order {
		out = append(out, *byTable[key])
	}
	return out
}

// requireClickHouse returns a server, starting one when the environment does
// not name one, and skipping by name when neither is possible.
func requireClickHouse(t *testing.T) *chServer {
	t.Helper()
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 2 * time.Minute}

	if u := os.Getenv("AF_TEST_CLICKHOUSE_URL"); u != "" {
		parsed, err := url.Parse(u)
		require.NoError(t, err, "AF_TEST_CLICKHOUSE_URL is not a URL")
		password, _ := parsed.User.Password()
		db := strings.TrimPrefix(parsed.Path, "/")
		if db == "" {
			db = "default"
		}
		return &chServer{
			base:     parsed.Scheme + "://" + parsed.Host,
			user:     parsed.User.Username(),
			password: password,
			database: db,
			client:   client,
		}
	}
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set and AF_TEST_CLICKHOUSE_URL names no server")
	}

	cli, err := dockerutil.Client()
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	// Closed, because the daemon client holds an idle connection whose read
	// loop is a goroutine, and this package checks for leaked goroutines. It is
	// registered before the container cleanup so that it runs after it.
	t.Cleanup(func() { _ = cli.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)

	if _, err := cli.ImageInspect(ctx, clickhouseImage); err != nil {
		rc, pullErr := cli.ImagePull(ctx, clickhouseImage, image.PullOptions{})
		if pullErr != nil {
			t.Skipf("skipped: %s could not be pulled: %v", clickhouseImage, pullErr)
		}
		dockerutil.Discard(rc)
	}

	port := freePort(t)
	name := fmt.Sprintf("af-mask-ch-%d", time.Now().UnixNano()%1e9)
	const password = "af-masking-test"
	httpPort := nat.Port("8123/tcp")
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: clickhouseImage,
			// Labelled as ours, and that is not cosmetic: RemoveContainer
			// REFUSES a container it cannot see a label on, so an unlabelled
			// one is created, used, and then left running. Nine of them
			// accumulated on this machine in three runs, four were killed for
			// memory, and the two tests that came after them skipped with a
			// timeout that said nothing about the cause. The cleanup below
			// reports its own failure for the same reason.
			Labels: dockerutil.Managed("masking-test", name, time.Now()),
			Env: []string{
				"CLICKHOUSE_PASSWORD=" + password,
				"CLICKHOUSE_DB=af_masking",
			},
			ExposedPorts: nat.PortSet{httpPort: struct{}{}},
		},
		&container.HostConfig{
			PortBindings: nat.PortMap{httpPort: []nat.PortBinding{{
				// Loopback only, which is what makes a fixed password
				// acceptable: the server is unreachable from anywhere but
				// this machine.
				HostIP: "127.0.0.1", HostPort: strconv.Itoa(port),
			}}},
			RestartPolicy: container.RestartPolicy{Name: "no"},
		}, nil, nil, name)
	if err != nil {
		t.Skipf("skipped: no ClickHouse container could be created: %v", err)
	}
	t.Cleanup(func() {
		if err := dockerutil.RemoveContainer(context.WithoutCancel(ctx), cli, resp.ID); err != nil {
			t.Errorf("the ClickHouse container %s was left behind: %v", name, err)
		}
	})
	require.NoError(t, cli.ContainerStart(ctx, resp.ID, container.StartOptions{}))

	server := &chServer{
		base:     "http://127.0.0.1:" + strconv.Itoa(port),
		user:     "default",
		password: password,
		database: "af_masking",
		client:   client,
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, err = server.do(ctx, "SELECT 1", nil, nil); err == nil {
			break
		}
		if time.Now().After(deadline) {
			// The server's own last words rather than only the connection
			// error. A container killed for memory and a container still
			// starting produce the same EOF from here, and skipping on the
			// EOF alone is how a real failure reads as an absent dependency.
			t.Skipf("skipped: ClickHouse did not answer in ninety seconds: %v\ncontainer log:\n%s",
				err, containerLog(ctx, cli, resp.ID))
		}
		time.Sleep(500 * time.Millisecond)
	}
	return server
}

// eventsSchema is the shape this whole wave exists for: an events table with
// the identifier that joins it to the Postgres beside it, and the properties
// blob an analytics product keeps its per person fields in.
const eventsSchema = `
CREATE TABLE events (
  uuid        UUID,
  event       String,
  distinct_id String,
  email       Nullable(String),
  person_id   Nullable(UUID),
  properties  String,
  ip          Nullable(String),
  ts          DateTime64(6)
) ENGINE = MergeTree ORDER BY uuid`

func insertEvents(t *testing.T, ch *chServer) {
	t.Helper()
	ch.exec(t, `INSERT INTO events VALUES
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a01', 'pageview', 'ada@lovelace-analytics.co.uk',
   'ada@lovelace-analytics.co.uk', '01890fa1-9e40-7d3c-8b9a-2f5c6d7e8b01',
   '{"plan":"team"}', '81.2.69.142', '2026-09-07 10:00:00'),
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a02', 'pageview', 'grace@hopper-systems.io',
   'grace@hopper-systems.io', '01890fa1-9e40-7d3c-8b9a-2f5c6d7e8b02',
   '{"plan":"free"}', '81.2.69.160', '2026-09-07 10:01:00')`, nil)
}

// TestClickHouseLive_TheDialectPlacesEveryTypeTheServerReports is the external
// grader for the type table.
//
// Every type name here is read back out of system.columns rather than written
// into the test, so the mapping is checked against the spelling the server
// actually uses. A table agreeing with a fixture proves the fixture.
func TestClickHouseLive_TheDialectPlacesEveryTypeTheServerReports(t *testing.T) {
	ch := requireClickHouse(t)
	d := clickhouse(t)

	var cols []string
	for i, c := range clickhouseTypeCases {
		if strings.HasPrefix(c.raw, "AggregateFunction") {
			// Needs an aggregating engine to hold one, and it is here for the
			// opposite reason to the others: it is the row that must NOT be
			// placed.
			continue
		}
		cols = append(cols, fmt.Sprintf("c%d %s", i, c.raw))
	}
	ch.exec(t, "CREATE TABLE every_type (id String, "+strings.Join(cols, ", ")+
		") ENGINE = MergeTree ORDER BY id", nil)

	rows, err := ch.rows(context.Background(),
		"SELECT name, type FROM system.columns WHERE database = currentDatabase() "+
			"AND table = 'every_type'")
	require.NoError(t, err)
	reported := map[string]string{}
	for _, r := range rows {
		reported[string(r[0])] = string(r[1])
	}

	for i, c := range clickhouseTypeCases {
		if strings.HasPrefix(c.raw, "AggregateFunction") {
			continue
		}
		name := fmt.Sprintf("c%d", i)
		serverType, ok := reported[name]
		require.True(t, ok, "the server did not report %s at all", name)

		require.Equal(t, c.canonical, d.Canonical(serverType),
			"the server calls this %q and the masking dialect places it somewhere else; "+
				"a type nothing places is a column nothing classifies", serverType)
		require.Equal(t, c.canonical, verify.ClickHouse.Canonical(serverType),
			"the server calls this %q and the scanner places it somewhere else", serverType)
	}
}

// TestClickHouseLive_TheSameDetectorsFindRealDataInASecondStore is the scanning
// half of the lane, end to end against a server.
func TestClickHouseLive_TheSameDetectorsFindRealDataInASecondStore(t *testing.T) {
	ch := requireClickHouse(t)
	ch.exec(t, eventsSchema, nil)
	insertEvents(t, ch)

	report, err := verify.ScanSource(context.Background(), ch.source(), verify.Options{
		SampleSize: 100,
	})
	require.NoError(t, err)
	require.Equal(t, "clickhouse", report.Engine,
		"a report that does not say which store it read cannot be told from another store's")
	require.Empty(t, report.Skipped, "%v", report.Skipped)
	require.False(t, report.Clean(),
		"a ClickHouse holding real addresses came back clean, so the golden it gates "+
			"would be published")

	found := map[string][]string{}
	for _, f := range report.Findings {
		found[f.Column] = append(found[f.Column], f.Detector)
	}
	require.Contains(t, found, "email", "the address column produced no finding: %+v", report.Findings)
	require.Contains(t, found, "distinct_id",
		"an identifier that is an address produced no finding, and it is the column the "+
			"join is on")
	// A routable address, deliberately not one of the documentation ranges the
	// ip transform produces: the detector does not fire on those, which is what
	// makes it able to tell a masked address from a real one.
	require.Contains(t, found, "ip")

	// And nothing was quietly passed over. Every column is either read or
	// listed as unread, which is the property the Postgres scanner had to
	// learn the hard way.
	require.Positive(t, report.Columns)
	require.Positive(t, report.RowsSampled)
}

// TestClickHouseLive_TheStatementsTheDialectCompilesRun is the other half: the
// text is not a guess about a grammar.
func TestClickHouseLive_TheStatementsTheDialectCompilesRun(t *testing.T) {
	ch := requireClickHouse(t)
	ch.exec(t, eventsSchema, nil)
	insertEvents(t, ch)

	rs, err := masking.NewRuleSet([]masking.Rule{
		{Column: "distinct_id", Transform: "email", Link: "email",
			Why: "This product's distinct id is the person's address."},
		{Column: "properties", Type: "text", Transform: "empty_json",
			Why: "Free form JSON in a String."},
		// A masked column that is NOT a String, so the cast the dialect writes
		// for one is executed rather than only compared as text. Postgres
		// infers the column's type from the assignment and ClickHouse does not.
		{Column: "person_id", Transform: "uuid_remap",
			Why: "The person a row is about."},
	})
	require.NoError(t, err)

	tables := ch.catalog(t)
	plan := masking.BuildPlan(tables, rs.Assign(tables), "live")
	require.True(t, plan.Runnable(), masking.DescribeProblems(plan.Problems))
	require.Len(t, plan.Tables, 1)
	tp := plan.Tables[0]

	// The read the dialect compiles, run against the server.
	d := clickhouse(t)
	read := d.SelectChunk(tp, "")
	rows, err := ch.rows(context.Background(), read.SQL)
	require.NoError(t, err, read.SQL)
	require.Len(t, rows, 2, "the compiled read did not return the rows")

	// And the rewrite, applied one row at a time exactly as the executor
	// would, with the values computed in Go from a key the server never sees.
	key := testKey(t)
	stmt := tp.Compile()
	for _, row := range rows {
		params := map[string]string{"p1": string(row[0])}
		for i, c := range tp.Columns {
			transform, ok := masking.Lookup(c.Transform)
			require.True(t, ok, c.Transform)
			in := string(row[1+i])
			out, applyErr := transform.Apply(key, masking.Column{
				Schema: tp.Table.Schema, Table: tp.Table.Name,
				Name: c.Column.Name, Link: c.Link,
			}, &in)
			require.NoError(t, applyErr)
			require.NotNil(t, out)
			params[fmt.Sprintf("p%d", i+2)] = *out
		}
		ch.exec(t, stmt.SQL, params)
	}

	after, err := verify.ScanSource(context.Background(), ch.source(), verify.Options{
		SampleSize: 100,
	})
	require.NoError(t, err)
	require.Empty(t, after.Findings,
		"the masked store still holds something that looks real: %v", after.Findings)
	require.True(t, after.Clean(), "%v %v", after.Findings, after.Skipped)
}

// containerLog returns the tail of a container's output, for a message that
// would otherwise say only that nothing answered.
func containerLog(ctx context.Context, cli *client.Client, id string) string {
	rc, err := cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: "20",
	})
	if err != nil {
		return "unavailable: " + err.Error()
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	return string(body)
}
