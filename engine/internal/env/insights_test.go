package env

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The point of this file is that RunInsights has a caller and that the caller
// works. Everything the insights package does is proved in its own tests
// against a real Postgres; what is proved here is the part only the
// orchestrator does: find the golden, branch it, find the repository's
// migrations, rehearse them on that branch and not on the environment's, and
// take the branch away again.
//
// The last of those is why this test exists at all rather than being folded
// into the package's own suite. A rehearsal branch that outlives the check is a
// copy of production's data nobody is watching, and nothing inside the
// insights package can prove it was removed.

const insightsFixture = `
CREATE TABLE orders (
  id          bigserial PRIMARY KEY,
  user_id     bigint NOT NULL,
  total_cents int NOT NULL
);
INSERT INTO orders (user_id, total_cents)
  SELECT g, g FROM generate_series(1, 5000) g;
ANALYZE;
`

func TestRunInsightsRehearsesOnItsOwnBranchAndTakesItAway(t *testing.T) {
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	p, err := dockerdb.New(dockerdb.Options{Version: 17, Clock: clock.New(), PortFrom: 46900})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	golden, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: "insights-env-test",
		Mask: func(ctx context.Context, url secrets.Value) error {
			conn, cErr := pgx.Connect(ctx, url.Reveal())
			if cErr != nil {
				return cErr
			}
			defer func() { _ = conn.Close(context.Background()) }()
			_, eErr := conn.Exec(ctx, insightsFixture)
			return eErr
		},
		Verify: func(context.Context, secrets.Value) (string, error) { return `{"rows":0}`, nil },
	})
	if err != nil {
		t.Skipf("skipped: no golden could be made: %v", err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 3*time.Minute)
		defer done()
		_ = p.DestroyGolden(c, golden.ID)
	})

	// A repository with a migration in it, which is what Discover reads.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "migrations"), 0o755))
	// int to bigint: it succeeds, it rewrites every row, and whether it
	// rewrites depends on the type it is coming FROM, which is in the
	// catalogue and not in the statement. So one migration exercises the
	// applier, the event trigger and the catalogue aware lint at once.
	require.NoError(t, os.WriteFile(filepath.Join(root, "migrations", "001_widen.sql"),
		[]byte("ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint;\n"), 0o644))

	// Below the fixture's row count, so the lint has something real to say
	// about a table this size.
	rows := 1000
	o, err := New(Options{
		Root:   root,
		Branch: "main",
		Clock:  clock.New(),
		Manifest: &schema.Manifest{
			Name:     "insightsenv",
			Database: &schema.Database{Provider: schema.DBDocker, Version: 17},
			Insights: &schema.Insights{LargeTableRows: rows},
		},
		Secrets: secrets.NewChain(),
	})
	require.NoError(t, err)

	// The environment's own database, as af up would have left it.
	envBranch, err := p.Branch(ctx, golden.ID, o.EnvID())
	require.NoError(t, err)
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 3*time.Minute)
		defer done()
		_ = p.Destroy(c, envBranch)
	})

	full, err := o.RunInsights(ctx, InsightsOptions{Limit: 10})
	require.NoError(t, err)

	require.NotNil(t, full.Rehearsal, "the migrations were never rehearsed")
	require.False(t, full.Rehearsal.Failed, full.Rehearsal.Error)
	require.Equal(t, insights.ToolSQLDir, full.Rehearsal.Tool)
	require.Len(t, full.Rehearsal.Pending, 1)
	require.NotEmpty(t, full.Rehearsal.Statements, "no statement was timed")

	// The lint ran against the branch's real catalogue rather than against the
	// SQL alone. Nothing in the statement says the column is an int today, and
	// that is the fact that decides whether Postgres rewrites the table.
	require.Len(t, full.Rehearsal.Lint, 1)
	require.Equal(t, insights.RuleAlterColumnType, full.Rehearsal.Lint[0].Rule)
	require.Equal(t, "orders", full.Rehearsal.Lint[0].Table)
	require.Greater(t, full.Rehearsal.Lint[0].Rows, int64(rows),
		"the row count came from the branch, and it is what makes a rewrite an outage")

	// And Postgres itself said it rewrote the table, through the event trigger
	// the orchestrator installed on the branch it made.
	require.Contains(t, full.Rehearsal.Rewrote(), "orders")

	// The environment's own database was NOT migrated. Rehearsing on it would
	// change the thing somebody is using, which is not a rehearsal.
	envURL, err := p.ConnString(ctx, envBranch, provider.ConnDirect)
	require.NoError(t, err)
	conn, err := pgx.Connect(ctx, envURL.Reveal())
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()

	var dataType string
	require.NoError(t, conn.QueryRow(ctx,
		"SELECT data_type FROM information_schema.columns "+
			"WHERE table_name = 'orders' AND column_name = 'total_cents'").Scan(&dataType))
	require.Equal(t, "integer", dataType,
		"the migration was applied to the environment's own database, not to a rehearsal branch")

	// And the rehearsal branch is gone. A copy of production's data that
	// outlives the check is a copy nobody is watching.
	inventory, err := p.Inventory(ctx)
	require.NoError(t, err)
	for _, r := range inventory {
		require.NotContains(t, r.ID, o.EnvID()+"-rehearsal",
			"the rehearsal branch outlived the rehearsal")
	}
}

// applierFor is the decision that makes the container applier reachable at
// all, so it is tested on its own rather than only through a run that needs a
// daemon. A prebuilt image is returned by buildOne before it touches the
// session, which is what lets these stay offline, and an empty session stands
// in for a provider whose branches are not local containers: a nil interface
// fails the Attachable assertion, which is the hosted provider's path.

func repoWith(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	return root
}

func orchestratorFor(t *testing.T, root string, m *schema.Manifest) *Orchestrator {
	t.Helper()
	o, err := New(Options{
		Root: root, Branch: "main", Clock: clock.New(),
		Manifest: m, Secrets: secrets.NewChain(),
	})
	require.NoError(t, err)
	return o
}

func migratingManifest(image, command string) *schema.Manifest {
	return &schema.Manifest{
		Name: "app",
		Services: []schema.Service{{
			Name:    "web",
			Migrate: command,
			Build:   &schema.Build{Strategy: schema.BuildImage, Image: image},
		}},
		Database: &schema.Database{Provider: schema.DBDocker, URLEnv: "MY_DB_URL"},
	}
}

func TestApplierFor_ASQLDirectoryIsReplayedFromHere(t *testing.T) {
	root := repoWith(t, map[string]string{"migrations/001_a.sql": "SELECT 1;"})
	o := orchestratorFor(t, root, migratingManifest("nginx:alpine", "true"))

	set := insights.Discover(os.DirFS(root))
	applier, why, err := o.applierFor(t.Context(), &session{}, set, provider.Branch{})
	require.NoError(t, err)
	require.Empty(t, why)
	require.Equal(t, "sql", applier.Name())
}

func TestApplierFor_RailsIsRunByItsOwnToolInTheServicesImage(t *testing.T) {
	// The whole reason the container applier exists. Only ActiveRecord knows
	// what a Rails migration becomes, and the answer depends on the gems in
	// the image rather than on what is installed on this machine.
	root := repoWith(t, map[string]string{
		"Gemfile":                  "gem 'rails'",
		"db/migrate/001_create.rb": "class Create < ActiveRecord::Migration; end",
	})
	o := orchestratorFor(t, root, migratingManifest("myapp:built", "bin/rails db:migrate"))

	set := insights.Discover(os.DirFS(root))
	require.Equal(t, insights.ToolRails, set.Tool)

	applier, why, err := o.applierFor(t.Context(), &session{}, set, provider.Branch{})
	require.NoError(t, err)
	require.Empty(t, why)
	require.Equal(t, "container", applier.Name())

	c, ok := applier.(*insights.ContainerApplier)
	require.True(t, ok)
	require.Equal(t, "myapp:built", c.Image)
	require.Equal(t, "bin/rails db:migrate", c.Command)
	// The manifest names the variable because not every framework reads
	// DATABASE_URL, and handing Rails the wrong variable name would make it
	// migrate whatever its own config points at, which is nothing here.
	require.Equal(t, "MY_DB_URL", c.URLVar)
}

func TestApplierFor_SaysWhyWhenNoServiceDeclaresAMigrateCommand(t *testing.T) {
	// The failure this guards against is a report that reads like a rehearsal
	// that found nothing.
	root := repoWith(t, map[string]string{"manage.py": "import django"})
	o := orchestratorFor(t, root, &schema.Manifest{
		Name:     "app",
		Services: []schema.Service{{Name: "web"}},
	})

	applier, why, err := o.applierFor(t.Context(), &session{}, insights.Discover(os.DirFS(root)),
		provider.Branch{})
	require.NoError(t, err)
	require.Nil(t, applier)
	require.Contains(t, why, "django")
	require.Contains(t, why, "no service in the manifest declares a migrate command")
}

func TestApplierFor_SaysWhyWhenNoToolIsRecognised(t *testing.T) {
	root := repoWith(t, map[string]string{"README.md": "hello"})
	o := orchestratorFor(t, root, migratingManifest("nginx:alpine", "true"))

	applier, why, err := o.applierFor(t.Context(), &session{}, insights.Discover(os.DirFS(root)),
		provider.Branch{})
	require.NoError(t, err)
	require.Nil(t, applier)
	require.Contains(t, why, "no migration tool was recognised")
}
