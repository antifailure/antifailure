package subset_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/subset"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The schema below is every shape that makes subsetting go wrong, in one
// database, because they interact and testing them one at a time proves less
// than it looks.
//
//   - countries is a small reference table nothing narrows.
//   - tenants has a COMPOSITE primary key, and three tables reference it with a
//     composite foreign key. A subset that treats (region, tenant_no) as two
//     independent conditions takes rows whose region matches one tenant and
//     whose number matches another.
//   - employees has a SELF REFERENCE, manager_id, which no copy order can
//     satisfy because the rows it needs are the rows being copied.
//   - employees and projects form a CYCLE: a project has a lead who is an
//     employee, and an employee has a primary project. No order loads both.
//   - nodes has a self reference that is NOT NULL, so a reference that does not
//     resolve cannot be cleared and the row has to go.
//   - events references employees and the schema does not say so. It is the
//     UNDECLARED relationship, and it is the one a naive subset empties
//     silently.
//   - invoices has an IDENTITY column and a STORED GENERATED column. COPY
//     accepts a value for the first and refuses one for the second.
//   - audit_log has NO PRIMARY KEY, so there is no order that would truncate it
//     the same way twice.
//   - feature_flags is connected to nothing.
const awkward = `
CREATE TABLE countries (
  code text PRIMARY KEY,
  name text NOT NULL
);

CREATE TABLE tenants (
  region       text NOT NULL,
  tenant_no    int  NOT NULL,
  name         text NOT NULL,
  country_code text REFERENCES countries(code),
  PRIMARY KEY (region, tenant_no)
);

CREATE TABLE employees (
  id         bigserial PRIMARY KEY,
  region     text NOT NULL,
  tenant_no  int  NOT NULL,
  manager_id bigint REFERENCES employees(id),
  email      text NOT NULL,
  FOREIGN KEY (region, tenant_no) REFERENCES tenants(region, tenant_no)
);

CREATE TABLE projects (
  id        bigserial PRIMARY KEY,
  region    text NOT NULL,
  tenant_no int  NOT NULL,
  lead_id   bigint NOT NULL REFERENCES employees(id),
  title     text NOT NULL,
  FOREIGN KEY (region, tenant_no) REFERENCES tenants(region, tenant_no)
);

ALTER TABLE employees ADD COLUMN primary_project_id bigint REFERENCES projects(id);

CREATE TABLE nodes (
  id        int PRIMARY KEY,
  parent_id int NOT NULL REFERENCES nodes(id),
  label     text NOT NULL
);

CREATE TABLE events (
  id          bigserial PRIMARY KEY,
  employee_id bigint NOT NULL,
  kind        text NOT NULL
);

CREATE TABLE invoices (
  id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  region    text NOT NULL,
  tenant_no int  NOT NULL,
  cents     bigint NOT NULL,
  dollars   numeric GENERATED ALWAYS AS (cents / 100.0) STORED,
  FOREIGN KEY (region, tenant_no) REFERENCES tenants(region, tenant_no)
);

CREATE TABLE audit_log (
  happened_at timestamptz NOT NULL DEFAULT now(),
  who         text NOT NULL
);

CREATE TABLE feature_flags (
  name    text PRIMARY KEY,
  enabled boolean NOT NULL
);
`

// The data spans two regions, so that a seed predicate on one of them is a
// real filter and not a filter that happens to match everything. A subset that
// silently matches everything passes every check except this one.
const awkwardData = `
INSERT INTO countries VALUES ('gb','United Kingdom'),('de','Germany'),('us','United States');
INSERT INTO tenants VALUES
  ('eu', 1, 'Northwind EU', 'gb'),
  ('eu', 2, 'Contoso EU',   'de'),
  ('us', 1, 'Northwind US', 'us'),
  ('us', 2, 'Fabrikam US',  NULL);

INSERT INTO employees (region, tenant_no, manager_id, email) VALUES
  ('eu',1,NULL,'ada@eu.example'),
  ('eu',1,1,   'grace@eu.example'),
  ('eu',2,NULL,'alan@eu.example'),
  ('us',1,NULL,'katherine@us.example'),
  ('us',1,4,   'dorothy@us.example'),
  ('us',2,NULL,'mary@us.example');

INSERT INTO projects (region, tenant_no, lead_id, title) VALUES
  ('eu',1,1,'EU billing'),
  ('eu',2,3,'EU onboarding'),
  ('us',1,4,'US billing');

UPDATE employees SET primary_project_id = 1 WHERE id IN (1,2);
UPDATE employees SET primary_project_id = 2 WHERE id = 3;
UPDATE employees SET primary_project_id = 3 WHERE id IN (4,5);

-- A root points at itself, which is the only way a NOT NULL self reference can
-- have a root at all.
INSERT INTO nodes VALUES (1,1,'root'),(2,1,'child'),(3,2,'grandchild');

INSERT INTO events (employee_id, kind) VALUES
  (1,'signed_in'),(2,'signed_in'),(3,'exported'),(4,'signed_in'),(6,'deleted');

INSERT INTO invoices (region, tenant_no, cents) VALUES
  ('eu',1,1999),('eu',2,2999),('us',1,3999),('us',2,4999);

INSERT INTO audit_log (who) VALUES ('ada@eu.example'),('katherine@us.example');
`

// testPostgresMajor is the server version these suites ask for.
//
// Deliberately not the newest. pg_dump REFUSES to read a server newer than
// itself, and Debian, Ubuntu and the GitHub runners all still ship a 16 client
// by default, so a suite that copies a schema out of a 17 server fails on a
// correctly set up machine. 16 is readable by every client this is likely to
// meet, and nothing being tested here is version specific: the catalogue
// queries, COPY, generated and identity columns, composite keys, materialized
// common table expressions and session_replication_role are all the same on 16
// and 17. The version skew itself is the product's problem rather than the
// test's, and pgcopy handles it by finding a client new enough for the server
// and saying which package supplies one when there is none.
const testPostgresMajor = 16

// pair is a source database and an empty candidate, both real.
type pair struct {
	SourceURL string
	TargetURL string
	Source    *pgx.Conn
	Target    *pgx.Conn
}

// One Postgres for the whole package, and a fresh pair of databases per test.
//
// Two databases rather than two containers, because what is being tested is a
// copy between two databases that share nothing but a wire protocol, and that
// is as true of two databases on one server as of two servers. One server
// rather than one per test, because standing up a golden and branching it
// takes most of two minutes and eleven of those is a suite nobody runs.
var shared struct {
	baseURL string
	stop    func()
	skip    string
}

func TestMain(m *testing.M) {
	code := func() int {
		if os.Getenv("AF_SKIP_DOCKER") != "" {
			shared.skip = "AF_SKIP_DOCKER is set"
			return m.Run()
		}
		p, err := dockerdb.New(dockerdb.Options{Version: testPostgresMajor, Clock: clock.New(), PortFrom: 46700})
		if err != nil {
			shared.skip = fmt.Sprintf("no Docker daemon is reachable: %v", err)
			return m.Run()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
			Version: testPostgresMajor, RulesHash: "subset-test",
			Mask:   func(context.Context, secrets.Value) error { return nil },
			Verify: func(context.Context, secrets.Value) (string, error) { return `{"rows":0}`, nil },
		})
		if err != nil {
			_ = p.Close()
			shared.skip = fmt.Sprintf("no golden could be made: %v", err)
			return m.Run()
		}
		branch, err := p.Branch(ctx, gv.ID, "subsettest")
		if err != nil {
			_ = p.DestroyGolden(ctx, gv.ID)
			_ = p.Close()
			shared.skip = fmt.Sprintf("no branch could be made: %v", err)
			return m.Run()
		}
		base, err := p.ConnString(ctx, branch, provider.ConnDirect)
		if err != nil {
			_ = p.Destroy(ctx, branch)
			_ = p.DestroyGolden(ctx, gv.ID)
			_ = p.Close()
			shared.skip = fmt.Sprintf("no connection string: %v", err)
			return m.Run()
		}
		shared.baseURL = base.Reveal()
		shared.stop = func() {
			c, cancel2 := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel2()
			_ = p.Destroy(c, branch)
			_ = p.DestroyGolden(c, gv.ID)
			_ = p.Close()
		}
		return m.Run()
	}()
	if shared.stop != nil {
		shared.stop()
	}
	os.Exit(code)
}

// databaseCounter names each test's pair. Tests in one package run in one
// process, so a counter is enough and a random name would only make a failure
// harder to go and look at.
var databaseCounter atomic.Int64

// requirePair makes a fresh source and candidate for one test.
func requirePair(t *testing.T) (*pair, func()) {
	t.Helper()
	if shared.skip != "" {
		t.Skip("skipped: " + shared.skip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)

	n := databaseCounter.Add(1)
	srcName := fmt.Sprintf("af_source_%d", n)
	dstName := fmt.Sprintf("af_candidate_%d", n)

	admin, err := pgx.Connect(ctx, withDatabase(shared.baseURL, "postgres"))
	require.NoError(t, err)
	for _, name := range []string{srcName, dstName} {
		_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
		require.NoError(t, err)
	}

	pr := &pair{
		SourceURL: withDatabase(shared.baseURL, srcName),
		TargetURL: withDatabase(shared.baseURL, dstName),
	}
	pr.Source, err = pgx.Connect(ctx, pr.SourceURL)
	require.NoError(t, err)
	pr.Target, err = pgx.Connect(ctx, pr.TargetURL)
	require.NoError(t, err)

	return pr, func() {
		_ = pr.Source.Close(context.Background())
		_ = pr.Target.Close(context.Background())
		c, cancel2 := context.WithTimeout(context.Background(), time.Minute)
		defer cancel2()
		for _, name := range []string{srcName, dstName} {
			_, _ = admin.Exec(c, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		}
		_ = admin.Close(c)
		cancel()
	}
}

// withDatabase swaps the database in a connection string. Textual rather than
// parsed, because the string is built by the provider two functions away and
// its shape is known.
func withDatabase(url, name string) string {
	at := strings.LastIndex(url, "@")
	rest := url[at+1:]
	slash := strings.Index(rest, "/")
	q := strings.Index(rest, "?")
	suffix := ""
	if q >= 0 {
		suffix = rest[q:]
	}
	return url[:at+1] + rest[:slash+1] + name + suffix
}

// load puts the awkward schema and its data on the source, and the schema
// alone on the candidate, which is the state a provider hands the subsetter.
func (p *pair) load(t *testing.T, ctx context.Context) {
	t.Helper()
	_, err := p.Source.Exec(ctx, awkward)
	require.NoError(t, err)
	_, err = p.Source.Exec(ctx, awkwardData)
	require.NoError(t, err)
	// ANALYZE so the row estimates the planner reads are real ones.
	_, err = p.Source.Exec(ctx, "ANALYZE")
	require.NoError(t, err)

	require.NoError(t, pgcopy.CopySchema(ctx,
		secrets.New(p.SourceURL), secrets.New(p.TargetURL)),
		"the candidate gets the structure and none of the rows")
}

// euOnly is the manifest's subset block for these tests: everything one region
// needs, and nothing the other one does.
func euOnly(maxRows int) subset.Config {
	return subset.Config{
		SeedTable:        "tenants",
		SeedWhere:        "region = 'eu'",
		MaxRows:          maxRows,
		FollowDependents: 3,
		Virtual: []subset.ForeignKey{{
			From: "public.events", FromColumns: []string{"employee_id"},
			To: "public.employees", ToColumns: []string{"id"},
		}},
	}
}

func TestExecute_EveryForeignKeyStillResolves(t *testing.T) {
	// The property the whole package exists to provide, on the schema that
	// makes it hard: a composite key, a self reference, a cycle, a required
	// self reference, and a relationship the schema does not declare.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)

	plan, err := subset.Build(cat, euOnly(1000))
	require.NoError(t, err)

	stats, err := subset.Execute(ctx, subset.Options{
		SourceURL: pr.SourceURL, TargetURL: pr.TargetURL, Plan: plan,
	})
	require.NoError(t, err, "Execute fails rather than returns when a key does not resolve")
	require.Empty(t, stats.Orphans)

	// Asked again, independently, against the loaded database rather than
	// against the plan's own bookkeeping. Execute checking itself and a test
	// checking Execute's answer would be one check wearing two hats.
	loaded, err := subset.ReadCatalog(ctx, pr.Target)
	require.NoError(t, err)
	orphans, err := subset.CheckIntegrity(ctx, pr.Target, loaded.Keys)
	require.NoError(t, err)
	require.Empty(t, orphans, "every declared foreign key resolves")

	// The undeclared one too, which the database cannot check and which is the
	// whole reason virtual relationships exist.
	orphans, err = subset.CheckIntegrity(ctx, pr.Target, []subset.ForeignKey{{
		From: "public.events", FromColumns: []string{"employee_id"},
		To: "public.employees", ToColumns: []string{"id"},
	}})
	require.NoError(t, err)
	require.Empty(t, orphans, "the declared relationship holds as well")
}

func TestExecute_TakesTheSeedRegionAndNotTheOther(t *testing.T) {
	// A subset whose predicate matches everything resolves every key too. The
	// difference between a subset and a full copy is this test.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)
	plan, err := subset.Build(cat, euOnly(1000))
	require.NoError(t, err)
	_, err = subset.Execute(ctx, subset.Options{
		SourceURL: pr.SourceURL, TargetURL: pr.TargetURL, Plan: plan,
	})
	require.NoError(t, err)

	require.Equal(t, int64(2), count(t, ctx, pr.Target, "tenants"),
		"two EU tenants, and neither US one")
	require.Equal(t, int64(0), count(t, ctx, pr.Target,
		"tenants WHERE region <> 'eu'"))
	require.Equal(t, int64(3), count(t, ctx, pr.Target, "employees"),
		"the three EU employees, reached downward from their tenant")
	require.Equal(t, int64(0), count(t, ctx, pr.Target,
		"employees WHERE region <> 'eu'"))
	require.Equal(t, int64(2), count(t, ctx, pr.Target, "projects"))
	require.Equal(t, int64(2), count(t, ctx, pr.Target, "invoices"))

	// The composite key is the point of this one. A subset that split
	// (region, tenant_no) into two independent conditions would take
	// ('us', 1) as well, because 'us' is not in the EU set but 1 is in the
	// tenant_no set and each condition passes on its own.
	require.Equal(t, int64(0), count(t, ctx, pr.Target,
		`employees e WHERE NOT EXISTS (
		   SELECT 1 FROM tenants t WHERE t.region = e.region AND t.tenant_no = e.tenant_no)`),
		"no row matched half of a composite key")

	// Reachable only through the declared relationship.
	require.Equal(t, int64(3), count(t, ctx, pr.Target, "events"),
		"the events of the three EU employees")
	require.Equal(t, int64(0), count(t, ctx, pr.Target,
		"events e WHERE NOT EXISTS (SELECT 1 FROM employees x WHERE x.id = e.employee_id)"))
}

func TestExecute_ClearsAnOptionalReferenceItCouldNotSatisfy(t *testing.T) {
	// employees.manager_id points at employees, which no copy order can
	// satisfy: the rows it needs are the rows being copied. It is deferred and
	// repaired, and because it is nullable the repair clears it rather than
	// removing the employee, which is the difference between a subset that
	// loses a person and one that loses a link.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)

	// Grace, whose manager is Ada, and Ada is not in the seed.
	plan, err := subset.Build(cat, subset.Config{
		SeedTable: "employees", SeedWhere: "email = 'grace@eu.example'", MaxRows: 1000,
	})
	require.NoError(t, err)
	require.NotEmpty(t, plan.Cycles, "the cycle is named rather than worked around silently")

	var deferredSelf bool
	for _, k := range plan.Deferred {
		if k.From == "public.employees" && k.To == "public.employees" {
			deferredSelf = true
		}
	}
	require.True(t, deferredSelf, "a self reference is always deferred: %+v", plan.Deferred)

	stats, err := subset.Execute(ctx, subset.Options{
		SourceURL: pr.SourceURL, TargetURL: pr.TargetURL, Plan: plan,
	})
	require.NoError(t, err)
	require.Empty(t, stats.Orphans)

	var cleared bool
	for _, r := range stats.Repairs {
		if strings.Contains(r.Key, "manager_id") {
			cleared = true
			require.Equal(t, int64(1), r.Rows)
			require.Contains(t, r.Detail, "cleared")
		}
	}
	require.True(t, cleared, "the repair is reported rather than done quietly: %+v", stats.Repairs)

	require.Equal(t, int64(1), count(t, ctx, pr.Target, "employees"),
		"the employee is kept")
	require.Equal(t, int64(1), count(t, ctx, pr.Target, "employees WHERE manager_id IS NULL"),
		"and the link that could not be kept is cleared rather than left dangling")

	// The cascade, which is the part that was wrong the first time. Grace's
	// primary project is led by Ada, Ada is not in the subset, and lead_id is
	// NOT NULL, so the project has to go. That leaves Grace pointing at a
	// project that is no longer there, through a key that WAS a condition on
	// the selection and so looked settled. A repair pass that only revisited
	// the deferred keys left it dangling and the run failed its own integrity
	// check, which is how this was found.
	require.Equal(t, int64(0), count(t, ctx, pr.Target, "projects"),
		"the project whose required lead was missing was removed")
	require.Equal(t, int64(1), count(t, ctx, pr.Target,
		"employees WHERE primary_project_id IS NULL"),
		"and the reference the removal broke was cleared in the same fixed point")

	var cascaded bool
	for _, r := range stats.Repairs {
		if strings.Contains(r.Key, "primary_project_id") {
			cascaded = true
		}
	}
	require.True(t, cascaded,
		"a repair caused by another repair is reported too: %+v", stats.Repairs)
}

func TestExecute_RemovesARowWhoseRequiredReferenceIsNotThere(t *testing.T) {
	// nodes.parent_id is NOT NULL and points at nodes. A budget that takes the
	// root and the child but not the grandchild is fine; one that takes the
	// grandchild and not its parent leaves a row that cannot be cleared, so it
	// has to go, and that has to be said out loud.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	// nodes is unreachable from tenants, so it is seeded directly, narrowed to
	// the deepest row, whose parent chain the subset does not take.
	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)
	plan, err := subset.Build(cat, subset.Config{
		SeedTable: "nodes", SeedWhere: "label = 'grandchild'", MaxRows: 1000,
	})
	require.NoError(t, err)

	stats, err := subset.Execute(ctx, subset.Options{
		SourceURL: pr.SourceURL, TargetURL: pr.TargetURL, Plan: plan,
	})
	require.NoError(t, err)
	require.Empty(t, stats.Orphans, "the result loads, which is the point")

	var removed bool
	for _, r := range stats.Repairs {
		if strings.Contains(r.Key, "parent_id") {
			removed = true
			require.Contains(t, r.Detail, "removed")
		}
	}
	require.True(t, removed, "removing a row is reported, never silent: %+v", stats.Repairs)
	require.Equal(t, int64(0), count(t, ctx, pr.Target, "nodes"),
		"the grandchild's parent was not taken, so the grandchild could not stay")
}

func TestExecute_MovesSequencesPastWhatItCopied(t *testing.T) {
	// A subset that loads cleanly and then collides on the application's first
	// insert has failed in the least useful place: after the tests pass.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)
	plan, err := subset.Build(cat, euOnly(1000))
	require.NoError(t, err)
	stats, err := subset.Execute(ctx, subset.Options{
		SourceURL: pr.SourceURL, TargetURL: pr.TargetURL, Plan: plan,
	})
	require.NoError(t, err)
	require.Positive(t, stats.Sequences)

	// A bigserial, and an identity column, which live in the catalog under
	// different dependency types and are both sequences that have to move.
	for _, ins := range []string{
		`INSERT INTO employees (region, tenant_no, email) VALUES ('eu',1,'new@eu.example') RETURNING id`,
		`INSERT INTO invoices (region, tenant_no, cents) VALUES ('eu',1,100) RETURNING id`,
	} {
		var id int64
		require.NoError(t, pr.Target.QueryRow(ctx, ins).Scan(&id), ins)
		require.Greater(t, id, int64(1000),
			"the sequence moved past the copied rows with a margin, so the "+
				"environment's own rows are distinguishable from production's")
	}
}

func TestExecute_WritesTheGeneratedColumnsItCannotCopy(t *testing.T) {
	// COPY refuses a stored generated column outright, so the copy names its
	// columns rather than taking the table's own order. The database computes
	// it from what did arrive, so nothing is lost, and this asserts that
	// rather than assuming it.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)
	plan, err := subset.Build(cat, euOnly(1000))
	require.NoError(t, err)
	_, err = subset.Execute(ctx, subset.Options{
		SourceURL: pr.SourceURL, TargetURL: pr.TargetURL, Plan: plan,
	})
	require.NoError(t, err)

	require.Equal(t, int64(0), count(t, ctx, pr.Target,
		"invoices WHERE dollars <> cents / 100.0"),
		"the generated column is right on every copied row")
	require.Equal(t, int64(2), count(t, ctx, pr.Target, "invoices WHERE dollars IS NOT NULL"))
}

func TestExecute_IsDeterministicUnderABudget(t *testing.T) {
	// Two runs of one plan have to take the same rows, or a golden cannot be
	// compared with the last one, which is most of what a masked copy is for.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)
	plan, err := subset.Build(cat, euOnly(2))
	require.NoError(t, err)

	first := runAndFingerprint(t, ctx, pr, plan)
	for i := 0; i < 3; i++ {
		require.NoError(t, truncateAll(ctx, pr.Target))
		require.Equal(t, first, runAndFingerprint(t, ctx, pr, plan),
			"run %d took different rows", i+2)
	}
}

func TestExecute_RefusesASeedPredicateTheDatabaseWillNotRun(t *testing.T) {
	// The ordinary mistake is a column that is not there, and finding it after
	// twenty minutes of copying is the difference between a typo and an
	// afternoon.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)
	plan, err := subset.Build(cat, subset.Config{
		SeedTable: "tenants", SeedWhere: "regoin = 'eu'", MaxRows: 100,
	})
	require.NoError(t, err)

	_, err = subset.Execute(ctx, subset.Options{
		SourceURL: pr.SourceURL, TargetURL: pr.TargetURL, Plan: plan,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "regoin", "the message names what the author wrote")
	require.Contains(t, err.Error(), "public.tenants")
	require.Equal(t, int64(0), count(t, ctx, pr.Target, "tenants"),
		"nothing was copied before the predicate was checked")
}

func TestExecute_WillNotWriteToTheSource(t *testing.T) {
	// The seed predicate is SQL out of a manifest, and a manifest is a file
	// somebody can open a pull request against. Every read happens in a read
	// only transaction, so the worst a predicate can do is fail.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)
	plan, err := subset.Build(cat, subset.Config{
		SeedTable: "tenants",
		SeedWhere: `region = 'eu') AND (SELECT count(*) FROM (DELETE FROM audit_log RETURNING 1) d) >= 0 AND (true`,
		MaxRows:   100,
	})
	require.NoError(t, err)

	_, execErr := subset.Execute(ctx, subset.Options{
		SourceURL: pr.SourceURL, TargetURL: pr.TargetURL, Plan: plan,
	})
	require.Error(t, execErr, "a predicate that tries to write is refused")
	require.Equal(t, int64(2), count(t, ctx, pr.Source, "audit_log"),
		"and the source still has its rows")
}

func TestExecute_ReportsWhatArrivedEmptyAndWhatEachKeyCovers(t *testing.T) {
	// A table that arrives empty and should not have is a bug somebody finds
	// three days later in a test that returns nothing.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)
	// No declared relationship this time, so events is unreachable and the
	// plan says so rather than quietly emptying it.
	cfg := euOnly(1000)
	cfg.Virtual = nil
	plan, err := subset.Build(cat, cfg)
	require.NoError(t, err)
	require.Contains(t, plan.Unreachable, "public.events")
	require.Contains(t, plan.Unreachable, "public.feature_flags")
	require.Contains(t, plan.Explain(), "virtual_relationships",
		"the explanation says what to do about it")

	stats, err := subset.Execute(ctx, subset.Options{
		SourceURL: pr.SourceURL, TargetURL: pr.TargetURL, Plan: plan,
	})
	require.NoError(t, err)

	var found bool
	for _, c := range stats.Coverage {
		if strings.Contains(c.Key, "employees") && strings.Contains(c.Key, "tenants") {
			found = true
			require.Positive(t, c.Rows)
			require.Equal(t, c.Rows, c.Resolved, "every referencing row found its parent")
		}
	}
	require.True(t, found, "coverage is reported per relationship: %+v", stats.Coverage)
}

func TestReadCatalog_KeepsACompositeKeyWhole(t *testing.T) {
	// The reason this package reads the catalog itself. internal/masking
	// flattens a foreign key onto its columns, which is right for deciding
	// that two columns must mask identically and wrong for a condition that
	// has to hold across both columns at once.
	pr, done := requirePair(t)
	defer done()
	ctx := context.Background()
	pr.load(t, ctx)

	cat, err := subset.ReadCatalog(ctx, pr.Source)
	require.NoError(t, err)

	var composite int
	for _, k := range cat.Keys {
		if k.From == "public.employees" && k.To == "public.tenants" {
			composite++
			require.Equal(t, []string{"region", "tenant_no"}, k.FromColumns)
			require.Equal(t, []string{"region", "tenant_no"}, k.ToColumns)
		}
	}
	require.Equal(t, 1, composite, "one constraint over two columns, not two constraints")

	tenants, ok := cat.Table("public.tenants")
	require.True(t, ok)
	require.Equal(t, []string{"region", "tenant_no"}, tenants.PrimaryKey)

	invoices, ok := cat.Table("public.invoices")
	require.True(t, ok)
	require.NotContains(t, invoices.Writable(), "dollars", "COPY cannot write a generated column")
	require.Contains(t, invoices.Writable(), "id", "COPY can write an identity column")
}

func count(t *testing.T, ctx context.Context, conn *pgx.Conn, from string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, conn.QueryRow(ctx, "SELECT count(*) FROM "+from).Scan(&n), from)
	return n
}

func runAndFingerprint(t *testing.T, ctx context.Context, pr *pair, plan subset.Plan) string {
	t.Helper()
	_, err := subset.Execute(ctx, subset.Options{
		SourceURL: pr.SourceURL, TargetURL: pr.TargetURL, Plan: plan,
	})
	require.NoError(t, err)

	var b strings.Builder
	for _, table := range []string{"tenants", "employees", "projects", "invoices"} {
		rows, err := pr.Target.Query(ctx,
			fmt.Sprintf("SELECT md5(string_agg(t::text, '|' ORDER BY t::text)) FROM %s t", table))
		require.NoError(t, err)
		for rows.Next() {
			var sum *string
			require.NoError(t, rows.Scan(&sum))
			fmt.Fprintf(&b, "%s=%v;", table, derefOr(sum, "empty"))
		}
		rows.Close()
	}
	return b.String()
}

func truncateAll(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' LOOP
    EXECUTE 'TRUNCATE TABLE public.' || quote_ident(r.tablename) || ' CASCADE';
  END LOOP;
END $$;`)
	return err
}

func derefOr(s *string, alt string) string {
	if s == nil {
		return alt
	}
	return *s
}
