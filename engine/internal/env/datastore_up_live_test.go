package env

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/datastore/clickhouse"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The lane's second acceptance, and the sentence the whole wave was added for:
// an environment holding a masked Postgres AND a masked ClickHouse, with a
// workflow that reads a chart and sees data.
//
// It is one `af up` against real everything: a real production Postgres and a
// real production ClickHouse to copy from, the Docker database provider, the
// local runtime, the egress sidecar, and a service container that reads the
// chart itself, over the environment's own network, through the name the
// manifest gave the store. The service is not told anything special: it reads
// the variable the engine sets and reaches a host called events, which is what
// an application already talking to a ClickHouse called events does.
//
// Before this lane that environment held a masked Postgres and zero events.

// upManifest is the analytics shaped stack, written the way somebody with one
// would write it.
//
// The service declaring ClickHouse is present ON PURPOSE, because it is how
// the documentation's own example declares this stack: a service running the
// stock image, and a datastore saying what its contents should be. The
// environment provides the store, so the service is not started, and the test
// below asserts that rather than leaving it to be discovered.
const upManifest = `
version: 1
name: analyticstwin
services:
  - name: charts
    kind: worker
    build:
      strategy: image
      image: alpine:3.20
    command: |
      sh -c 'U=$AF_DATASTORE_EVENTS_URL; DB=${U##*/}; BASE=${U%/*};
      wget -qO- "$BASE/?database=$DB&query=SELECT+event,+count()+FROM+events+GROUP+BY+event+ORDER+BY+event+FORMAT+TSV" || echo CHART-FAILED;
      sleep 3600'
  - name: events
    kind: worker
    build:
      strategy: image
      image: clickhouse/clickhouse-server:25.3-alpine
database:
  provider: docker
  version: 17
  source_url_env: PRODUCTION_DATABASE_URL
  masking_rules: ./masking.yaml
datastores:
  - name: events
    engine: clickhouse
    stance: golden
    source_url_env: PRODUCTION_CLICKHOUSE_URL
egress:
  default: block
`

// upMaskingRules is the one file that covers both stores.
const upMaskingRules = `
rules:
  - column: distinct_id
    transform: email
    link: person
    why: "this product's distinct id is the address a real person reads"

  - column: email
    transform: email
    link: person
    why: "the address itself"

  - column: properties
    transform: empty_json
    why: "per person fields, jsonb in Postgres and a String in ClickHouse"

  - column: event
    transform: preserve
    why: "an event name is not a person and a chart of nothing is not a twin"
`

func TestUpLive_AnEnvironmentHoldsAMaskedPostgresAndAMaskedClickHouse(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set and this brings an environment up")
	}
	// Forty rather than twenty, and the budget is here to stop a hang rather
	// than to police the clock.
	//
	// This test now takes two full inventories as well as an environment,
	// before the up and after it, and each of those asks the runtime what is
	// running, the database provider where the branch came from and the store
	// provider what its branch holds. A CI runner does the whole thing in
	// minutes. A laptop running several Docker suites at once has taken this
	// test twenty six minutes on its own, and the twenty minute budget then
	// expired in the middle of a query that had nothing to do with anything
	// already proved, which reads as a broken twin rather than as a slow
	// machine.
	//
	// It is deliberately above the engine suite's own thirty minute timeout,
	// which means a genuine hang on CI is caught by the suite rather than by
	// this deadline. That is the right way round: the suite's timeout is the
	// one that cannot be outrun, and this one exists so that a developer
	// waiting on a loaded machine gets a result rather than a deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	pgSource := productionPostgres(t)
	chSource, chServer := productionClickHouse(t, ctx)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "antifailure.yaml"),
		[]byte(strings.TrimSpace(upManifest)+"\n"), 0o644))
	// ONE rules file, both stores. This product's distinct id is the person's
	// address, in a column no default rule names, and it is the same column in
	// the Postgres and in the events. Without the rule the Postgres refuses to
	// publish, which is the guarantee working; with it, one identity masks to
	// one person in both stores, which is what the join at the end checks.
	require.NoError(t, os.WriteFile(filepath.Join(root, "masking.yaml"),
		[]byte(strings.TrimSpace(upMaskingRules)+"\n"), 0o644))
	m, err := manifest.Load(filepath.Join(root, "antifailure.yaml"))
	require.NoError(t, err)

	o, err := New(Options{
		Root: root, Manifest: m, Branch: "l42-datastores",
		Clock: clock.New(), Redactor: redact.New(),
		Progress: func(line string) { t.Log(line) },
		Getenv: func(k string) string {
			switch k {
			case MaskingKeyEnv:
				return "a-project-key-long-enough-to-be-accepted"
			case "PRODUCTION_DATABASE_URL":
				return pgSource
			case "PRODUCTION_CLICKHOUSE_URL":
				return chSource.Reveal()
			}
			return ""
		},
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		down, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		td, err := o.Down(down)
		if err != nil {
			t.Errorf("the environment could not be taken down: %v", err)
			return
		}
		for _, p := range td.Pending {
			t.Errorf("teardown left %s %s behind: %s", p.Kind, p.ID, p.Reason)
		}
	})

	// The refresh first, which is what a person does and what `af up` refuses
	// to do for them: a golden is a copy of production and reading production
	// is not something an ordinary up does behind somebody's back.
	golden, err := o.RefreshGolden(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, golden.Version, "the primary database has no golden")
	require.Len(t, golden.Datastores, 1,
		"af golden refresh refreshed the metadata and not the events, which is the half of "+
			"the twin this wave exists for")
	require.Equal(t, "events", golden.Datastores[0].Name)
	require.Equal(t, int64(3), golden.Datastores[0].Rows)
	require.False(t, golden.Datastores[0].Empty)
	require.Positive(t, golden.Datastores[0].Columns)

	// The report BEFORE the environment exists, on this manifest, with a
	// golden of the events already made and nothing branched. This is the
	// first half of the pair: the store is absent, and the report names the
	// four things it does not have.
	beforeInv, err := o.Fidelity(ctx)
	require.NoError(t, err)
	beforeReport := beforeInv.Explain()
	t.Log("the report with no branch\n\n" + beforeReport)
	beforeStore := datastoreComponent(t, beforeInv, "events")
	require.Equal(t, fidelity.Absent, beforeStore.State,
		"a store nothing branched is not reported as one the environment does not hold")
	require.Contains(t, beforeStore.Detail, "no golden, no attestation, no tables and no rows")

	result, err := o.Up(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, result.Golden)

	// And the report AFTER, on the same manifest and the same command. The
	// defect this pair is the check for: the datastores dimension was built
	// from the manifest alone, so both halves of this pair used to be the
	// first one, and the instrument said absent about a store holding a
	// masked, verified copy of production.
	afterInv, err := o.Fidelity(ctx)
	require.NoError(t, err)
	afterReport := afterInv.Explain()
	t.Log("the report after af up\n\n" + afterReport)

	afterData := datastoreComponent(t, afterInv, "events data")
	require.Contains(t, afterData.Detail, "1 table over 3 rows",
		"the environment holds a masked ClickHouse and the report does not say what is in it")
	require.Contains(t, afterData.Detail, "branched from "+golden.Datastores[0].Version)
	// Unmeasured rather than reproduced, and the report says why: nothing here
	// records what production's events store holds, so the branch has not been
	// shown to reproduce it. The primary database's data component is the same
	// unknown for the same reason on a manifest with no volume profile.
	require.Equal(t, fidelity.Unmeasured, afterData.State)
	require.Contains(t, afterData.Detail, "nothing here says what production's events holds")

	afterProvenance := datastoreComponent(t, afterInv, "events provenance")
	require.Equal(t, fidelity.Reproduced, afterProvenance.State)
	require.Contains(t, afterProvenance.Detail, "golden "+golden.Datastores[0].Version)
	require.Contains(t, afterProvenance.Detail, "still matching its signature")

	// The store the environment provides is not also started as a service, so
	// there is one ClickHouse on the network and it is the one holding a
	// masked copy of production.
	names := make([]string, 0, len(result.Services))
	for _, svc := range result.Services {
		names = append(names, svc.Name)
	}
	require.Equal(t, []string{"charts"}, names,
		"the empty ClickHouse service was started beside the store the environment provides, "+
			"so two containers answer to one name")

	// The Postgres half, read through the branch the environment is using.
	branchURL := branchConnString(t, ctx, o)
	conn, err := pgx.Connect(ctx, branchURL)
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	var people int
	require.NoError(t, conn.QueryRow(ctx, "SELECT count(*) FROM person").Scan(&people))
	require.Equal(t, 3, people, "the twin's Postgres does not hold production's rows")
	var leaked int
	require.NoError(t, conn.QueryRow(ctx,
		"SELECT count(*) FROM person WHERE email LIKE '%lovelace-analytics%'").Scan(&leaked))
	require.Zero(t, leaked, "a real address reached the twin's Postgres")

	// The ClickHouse half, read through the branch the environment is using.
	// This is the number: it was zero, because the events live in ClickHouse
	// and ClickHouse came up empty.
	eventsURL := datastoreConnString(t, ctx, o)
	require.Equal(t, "3", chQuery(t, eventsURL, "SELECT count() FROM events"),
		"the twin's ClickHouse holds no events, which is the state this lane exists to end")
	require.Equal(t, "0", chQuery(t, eventsURL,
		"SELECT count() FROM events WHERE distinct_id LIKE '%lovelace-analytics%'"),
		"a real address reached the twin's ClickHouse")

	// One identity is one person across both stores. A twin whose two stores
	// disagree about who somebody is would answer every joined question with
	// nothing, which is worse than an empty store because it looks like data.
	var pgIdentity string
	require.NoError(t, conn.QueryRow(ctx,
		"SELECT distinct_id FROM person ORDER BY id LIMIT 1").Scan(&pgIdentity))
	require.Equal(t, "1", chQuery(t, eventsURL, fmt.Sprintf(
		"SELECT count() FROM events WHERE distinct_id = '%s'", pgIdentity)),
		"the person Postgres calls %s does not exist in the events store, so every join "+
			"across the twin returns nothing", pgIdentity)

	// And the workflow: the service read the chart itself, from inside the
	// environment, over the network, by the store's name, with nothing in its
	// image knowing anything about Antifailure.
	chart := serviceOutput(t, ctx, o, "charts")
	require.NotContains(t, chart, "CHART-FAILED",
		"the service could not read the chart: %s", chart)
	require.Contains(t, chart, "click\t1")
	require.Contains(t, chart, "pageview\t2")

	t.Logf("events in the twin: 3, masked and verified, read as a chart from inside the "+
		"environment by a service that reached %q by name", "events")
	_ = chServer
}

// productionPostgres creates the database this environment's golden is copied
// from, holding three people whose addresses a detector recognises.
func productionPostgres(t *testing.T) string {
	t.Helper()
	admin := "postgres://postgres:test@127.0.0.1:55432/antifailure"
	if u := os.Getenv("AF_TEST_SEED_DATABASE_URL"); u != "" {
		admin = u
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no usable Postgres: %v", err)
		}
		t.Skipf("skipped: no Postgres to stand in for production: %v", err)
	}
	name := fmt.Sprintf("af_l42_prod_%d", time.Now().UnixNano()%1e9)
	_, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)
	require.NoError(t, conn.Close(ctx))
	t.Cleanup(func() {
		c := context.WithoutCancel(ctx)
		if a, err := pgx.Connect(c, admin); err == nil {
			_, _ = a.Exec(c, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+
				" WITH (FORCE)")
			_ = a.Close(c)
		}
	})

	own := replaceDatabase(admin, name)
	prod, err := pgx.Connect(ctx, own)
	require.NoError(t, err)
	defer func() { _ = prod.Close(context.Background()) }()
	_, err = prod.Exec(ctx, `
CREATE TABLE person (
  id          bigserial PRIMARY KEY,
  distinct_id text NOT NULL UNIQUE,
  email       text NOT NULL,
  properties  jsonb
);
INSERT INTO person (distinct_id, email, properties) VALUES
  ('ada@lovelace-analytics.co.uk',  'ada@lovelace-analytics.co.uk',  '{"plan":"team"}'),
  ('grace@hopper-systems.io', 'grace@hopper-systems.io', '{"plan":"free"}'),
  ('alan@turing-labs.net',  'alan@turing-labs.net',  '{"plan":"team"}');
`)
	require.NoError(t, err)
	return own
}

// productionClickHouse creates the events store the golden is copied from.
//
// On the machine's own managed ClickHouse, in a database of its own. That is
// not a shortcut: what a source URL names is a server somewhere, and pointing
// it at a second database on the same server exercises the same code path a
// remote one does, because the copy reads the source's own CREATE statement
// and streams its rows over HTTP either way.
func productionClickHouse(t *testing.T, ctx context.Context) (secrets.Value, clickhouse.LocalServer) {
	t.Helper()
	server, err := clickhouse.EnsureLocalServer(ctx, clickhouse.LocalServerOptions{
		Progress: func(line string) { t.Log(line) },
	})
	if err != nil {
		if os.Getenv("AF_REQUIRE_DOCKER") != "" {
			// The same rule the rest of this package follows: a machine that
			// was supposed to have a daemon and does not is a failure, because
			// a skip prints nothing and the package reports ok having examined
			// nothing.
			t.Fatalf("AF_REQUIRE_DOCKER is set, so this cannot be skipped: "+
				"no ClickHouse to stand in for production: %v", err)
		}
		t.Skipf("skipped: no ClickHouse to stand in for production: %v", err)
	}
	name := fmt.Sprintf("af_l42_prod_%d", time.Now().UnixNano()%1e9)
	chExec(t, server.URL, "CREATE DATABASE "+name+" ENGINE = Atomic")
	t.Cleanup(func() { chExec(t, server.URL, "DROP DATABASE IF EXISTS "+name) })

	source := chDatabaseURL(t, server.URL, name)
	chExec(t, source, `
CREATE TABLE events (
  uuid        UUID,
  event       LowCardinality(String),
  distinct_id String,
  properties  String,
  ts          DateTime64(6)
) ENGINE = MergeTree ORDER BY uuid`)
	chExec(t, source, `INSERT INTO events VALUES
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a01', 'pageview', 'ada@lovelace-analytics.co.uk',
   '{"plan":"team"}', '2026-09-07 10:00:00'),
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a02', 'pageview', 'grace@hopper-systems.io',
   '{"plan":"free"}', '2026-09-07 10:01:00'),
  ('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a03', 'click', 'alan@turing-labs.net',
   '{"plan":"team"}', '2026-09-07 10:02:00')`)
	return source, server
}

// chDatabaseURL points a server URL at one database.
func chDatabaseURL(t *testing.T, server secrets.Value, name string) secrets.Value {
	t.Helper()
	u, err := url.Parse(server.Reveal())
	require.NoError(t, err)
	u.Path = "/" + name
	return secrets.New(u.String())
}

// chExec runs a statement, and chQuery runs one and returns its output.
//
// Written against the ClickHouse HTTP interface rather than through the
// package under test, so that a test asserting what is in the store does not
// take the store's own client's word for it.
func chExec(t *testing.T, target secrets.Value, sql string) {
	t.Helper()
	_, err := chRequest(target, sql)
	require.NoError(t, err, sql)
}

func chQuery(t *testing.T, target secrets.Value, sql string) string {
	t.Helper()
	out, err := chRequest(target, sql+" FORMAT TSV")
	require.NoError(t, err, sql)
	return strings.TrimSpace(out)
}

func chRequest(target secrets.Value, sql string) (string, error) {
	u, err := url.Parse(target.Reveal())
	if err != nil {
		return "", err
	}
	password, _ := u.User.Password()
	q := url.Values{}
	q.Set("database", strings.TrimPrefix(u.Path, "/"))
	req, err := http.NewRequest(http.MethodPost,
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
	body := make([]byte, 64<<10)
	n, _ := resp.Body.Read(body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the server said %d: %s", resp.StatusCode,
			strings.TrimSpace(string(body[:n])))
	}
	return string(body[:n]), nil
}

// branchConnString is the address of the environment's own Postgres.
//
// The failure it reports carries the daemon's own list, because the one time
// this failed it failed here, with AF-DB-014 for an environment `af up` had
// just reported ready, and nothing in this test's path removes a branch. A
// machine running several of these suites at once is the likely answer and a
// listing taken at the instant it happens is the only thing that could say so:
// whether the container is absent, stopped, or there under another
// environment's label.
func branchConnString(t *testing.T, ctx context.Context, o *Orchestrator) string {
	t.Helper()
	s, err := o.openReading(ctx)
	require.NoError(t, err)
	defer s.close()
	url, err := s.dbProv.ConnString(ctx, provider.Branch{EnvID: o.envID}, provider.ConnDirect)
	require.NoError(t, err, "the environment came up and its branch is not there. "+
		"What the daemon holds now:\n%s", managedContainers(t))
	return url.Reveal()
}

// managedContainers lists what this repository has running, for a failure that
// needs to say whether something else took it away.
func managedContainers(t *testing.T) string {
	t.Helper()
	cli, err := dockerutil.Client()
	if err != nil {
		return "the daemon could not be reached: " + err.Error()
	}
	defer func() { _ = cli.Close() }()
	list, err := cli.ContainerList(context.Background(), container.ListOptions{
		All:     true,
		Filters: dockerutil.Filter(dockerutil.LabelManaged, dockerutil.ManagedValue),
	})
	if err != nil {
		return "the daemon would not list containers: " + err.Error()
	}
	var b strings.Builder
	for _, c := range list {
		fmt.Fprintf(&b, "  %s %s kind=%s env=%s state=%s\n",
			c.ID[:12], strings.TrimPrefix(dockerutil.FirstName(c.Names), "/"),
			c.Labels[dockerutil.LabelKind], c.Labels[dockerutil.LabelEnv], c.State)
	}
	if b.Len() == 0 {
		return "  nothing at all, so something removed every managed container"
	}
	return b.String()
}

// datastoreConnString is the address of the environment's own ClickHouse.
func datastoreConnString(t *testing.T, ctx context.Context, o *Orchestrator) secrets.Value {
	t.Helper()
	s, err := o.openReading(ctx)
	require.NoError(t, err)
	defer s.close()
	require.NoError(t, o.openDatastores(ctx, s, true))
	require.Len(t, s.stores, 1)
	url, err := s.stores[0].prov.ConnString(ctx, provider.Branch{EnvID: o.envID})
	require.NoError(t, err)
	return url
}

// serviceOutput reads what a service printed, which is where the chart the
// workflow read comes back from.
func serviceOutput(t *testing.T, ctx context.Context, o *Orchestrator, service string) string {
	t.Helper()
	cli, err := dockerutil.Client()
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	name := "af-svc-" + o.envID + "-" + service
	deadline := time.Now().Add(2 * time.Minute)
	var out string
	for {
		rc, logErr := cli.ContainerLogs(ctx, name, container.LogsOptions{
			ShowStdout: true, ShowStderr: true,
		})
		if logErr == nil {
			body, _ := io.ReadAll(rc)
			_ = rc.Close()
			// Docker multiplexes stdout and stderr with an eight byte header
			// per frame. The payloads are what a reader wants and the headers
			// are binary, so they are stripped rather than shown.
			out = stripDockerFrames(body)
		}
		if strings.Contains(out, "\t") || strings.Contains(out, "CHART-FAILED") {
			return out
		}
		if time.Now().After(deadline) {
			return out
		}
		time.Sleep(2 * time.Second)
	}
}

// stripDockerFrames removes the stream headers from an unmultiplexed log.
func stripDockerFrames(body []byte) string {
	var b strings.Builder
	for len(body) >= 8 {
		size := int(body[4])<<24 | int(body[5])<<16 | int(body[6])<<8 | int(body[7])
		if size < 0 || size > len(body)-8 {
			b.Write(body[8:])
			break
		}
		b.Write(body[8 : 8+size])
		body = body[8+size:]
	}
	return b.String()
}

// datastoreComponent pulls one component out of the datastores dimension.
func datastoreComponent(
	t *testing.T, inv fidelity.Inventory, name string,
) fidelity.Component {
	t.Helper()
	d, ok := inv.Dimension(schema.FidelityDatastores)
	require.True(t, ok, "the inventory has no datastores dimension")
	for _, c := range d.Components {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("the datastores dimension has no component %q: %+v", name, d.Components)
	return fidelity.Component{}
}
