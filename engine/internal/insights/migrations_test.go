package insights_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func file(body string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(body)} }

func TestDiscover_Prisma(t *testing.T) {
	t.Parallel()
	set := insights.Discover(fstest.MapFS{
		"prisma/schema.prisma":                                      file("datasource db { provider = \"postgresql\" }"),
		"prisma/migrations/20240101120000_init/migration.sql":       file("CREATE TABLE users (id int);"),
		"prisma/migrations/20240202090000_add_orders/migration.sql": file("CREATE TABLE orders (id int);"),
	})
	require.Equal(t, insights.ToolPrisma, set.Tool)
	require.True(t, set.SQLAvailable())
	require.Len(t, set.Migrations, 2)
	// Prisma records the directory name verbatim in _prisma_migrations, so
	// that has to be the version or nothing is ever recognised as applied.
	require.Equal(t, "20240101120000_init", set.Migrations[0].Version)
	require.Equal(t, "20240202090000_add_orders", set.Migrations[1].Version)
	require.Contains(t, set.Migrations[1].SQL, "orders")
}

func TestDiscover_PrismaInAMonorepo(t *testing.T) {
	t.Parallel()
	// The migrations are not at the root, which is the normal case in a
	// repository with more than one service in it.
	set := insights.Discover(fstest.MapFS{
		"api/prisma/schema.prisma":                      file("datasource db {}"),
		"api/prisma/migrations/0001_init/migration.sql": file("CREATE TABLE t (id int);"),
		"web/package.json":                              file("{}"),
	})
	require.Equal(t, insights.ToolPrisma, set.Tool)
	require.Equal(t, "api/prisma/migrations", set.Dir)
	require.Len(t, set.Migrations, 1)
}

func TestDiscover_Supabase(t *testing.T) {
	t.Parallel()
	set := insights.Discover(fstest.MapFS{
		"supabase/config.toml":                        file("project_id = \"x\""),
		"supabase/migrations/20240101120000_init.sql": file("CREATE TABLE users (id int);"),
	})
	require.Equal(t, insights.ToolSupabase, set.Tool)
	require.Len(t, set.Migrations, 1)
	// supabase_migrations.schema_migrations holds the timestamp alone, not
	// the filename.
	require.Equal(t, "20240101120000", set.Migrations[0].Version)
}

func TestDiscover_Drizzle(t *testing.T) {
	t.Parallel()
	set := insights.Discover(fstest.MapFS{
		"drizzle.config.ts":          file("export default {}"),
		"drizzle/meta/_journal.json": file(`{"entries":[]}`),
		"drizzle/0000_init.sql":      file("CREATE TABLE users (id int);"),
		"drizzle/0001_orders.sql":    file("CREATE TABLE orders (id int);"),
	})
	require.Equal(t, insights.ToolDrizzle, set.Tool)
	require.Len(t, set.Migrations, 2)
	require.Equal(t, "0000_init", set.Migrations[0].Version)
}

func TestDiscover_Flyway(t *testing.T) {
	t.Parallel()
	set := insights.Discover(fstest.MapFS{
		"src/main/resources/db/migration/V1__init.sql":     file("CREATE TABLE users (id int);"),
		"src/main/resources/db/migration/V1_1__orders.sql": file("CREATE TABLE orders (id int);"),
	})
	require.Equal(t, insights.ToolFlyway, set.Tool)
	require.Len(t, set.Migrations, 2)
	// flyway_schema_history stores 1 and 1.1, with the underscore read as a
	// dot, so that is what has to come back out.
	require.Equal(t, "1", set.Migrations[0].Version)
	require.Equal(t, "1.1", set.Migrations[1].Version)
}

func TestDiscover_PlainSQLDirectory(t *testing.T) {
	t.Parallel()
	set := insights.Discover(fstest.MapFS{
		"migrations/001_init.sql":   file("CREATE TABLE users (id int);"),
		"migrations/002_orders.sql": file("CREATE TABLE orders (id int);"),
	})
	require.Equal(t, insights.ToolSQLDir, set.Tool)
	require.Len(t, set.Migrations, 2)
}

func TestDiscover_MigrationsAreInTheOrderTheToolAppliesThem(t *testing.T) {
	t.Parallel()
	// fs.ReadDir happens to sort, but the rehearsal depends on the order, so
	// it is asserted rather than assumed. A migration applied out of order is
	// a migration whose CREATE TABLE runs after the ALTER TABLE that needs it.
	set := insights.Discover(fstest.MapFS{
		"migrations/003_third.sql":  file("SELECT 3;"),
		"migrations/001_first.sql":  file("SELECT 1;"),
		"migrations/002_second.sql": file("SELECT 2;"),
	})
	require.Equal(t, []string{"001_first.sql", "002_second.sql", "003_third.sql"},
		[]string{set.Migrations[0].Name, set.Migrations[1].Name, set.Migrations[2].Name})
}

func TestDiscover_RailsSaysWhyItCannotReplayThem(t *testing.T) {
	t.Parallel()
	// The failure this guards against is reporting an empty migration set,
	// which reads exactly like a repository with no migrations at all.
	set := insights.Discover(fstest.MapFS{
		"Gemfile":                  file("gem 'rails'"),
		"db/migrate/001_create.rb": file("class Create < ActiveRecord::Migration; end"),
	})
	require.Equal(t, insights.ToolRails, set.Tool)
	require.False(t, set.SQLAvailable())
	require.Contains(t, set.Reason, "Ruby")
}

func TestDiscover_DjangoSaysWhyItCannotReplayThem(t *testing.T) {
	t.Parallel()
	set := insights.Discover(fstest.MapFS{"manage.py": file("import django")})
	require.Equal(t, insights.ToolDjango, set.Tool)
	require.False(t, set.SQLAvailable())
	require.Contains(t, set.Reason, "Python")
}

func TestDiscover_NothingRecognised(t *testing.T) {
	t.Parallel()
	set := insights.Discover(fstest.MapFS{"README.md": file("hello")})
	require.Equal(t, insights.ToolNone, set.Tool)
	require.False(t, set.SQLAvailable())
}

func TestPending_IsWhatTheDatabaseHasNotRecorded(t *testing.T) {
	t.Parallel()
	set := insights.Discover(fstest.MapFS{
		"migrations/001_init.sql":   file("SELECT 1;"),
		"migrations/002_orders.sql": file("SELECT 2;"),
		"migrations/003_index.sql":  file("SELECT 3;"),
	})
	pending := set.Pending(map[string]bool{"001_init": true, "002_orders": true})
	require.Len(t, pending, 1)
	require.Equal(t, "003_index.sql", pending[0].Name)
}

func TestPending_ANilHistoryMeansEverythingIsPending(t *testing.T) {
	t.Parallel()
	// A tool with no history table we know how to read, or a database that
	// refused the read. Treating that as "nothing is pending" would silently
	// rehearse nothing and report a clean bill of health.
	set := insights.Discover(fstest.MapFS{"migrations/001_init.sql": file("SELECT 1;")})
	require.Len(t, set.Pending(nil), 1)
}

func TestPending_AnEmptyHistoryMeansEverythingIsPending(t *testing.T) {
	t.Parallel()
	set := insights.Discover(fstest.MapFS{"migrations/001_init.sql": file("SELECT 1;")})
	require.Len(t, set.Pending(map[string]bool{}), 1)
}

func TestDiscover_FlywayOrdersByVersionRatherThanByFilename(t *testing.T) {
	t.Parallel()
	// V1_1__ sorts before V1__ as text, because the digit sorts before the
	// underscore, and Flyway applies V1 first because it compares versions
	// numerically. Applying them in filename order would run an ALTER TABLE
	// before the CREATE TABLE it needs.
	set := insights.Discover(fstest.MapFS{
		"db/migration/V10__later.sql":  file("SELECT 10;"),
		"db/migration/V2__second.sql":  file("SELECT 2;"),
		"db/migration/V1_1__patch.sql": file("SELECT 1.1;"),
		"db/migration/V1__first.sql":   file("SELECT 1;"),
	})
	require.Equal(t, insights.ToolFlyway, set.Tool)
	require.Equal(t, []string{"1", "1.1", "2", "10"}, []string{
		set.Migrations[0].Version, set.Migrations[1].Version,
		set.Migrations[2].Version, set.Migrations[3].Version,
	})
}

// A project with its own runner names its directory, and nothing is inferred.
//
// This repository is the case. Its migrations are numbered SQL files under
// web/packages/db/migrations applied by a forty line script, and the rehearsal
// answered "no migration tool was recognised" for the product's own control
// plane while the manifest declared lock thresholds for exactly those files.
func TestLocate_ReadsTheDirectoryTheManifestDeclares(t *testing.T) {
	t.Parallel()
	m := &schema.Manifest{
		Services: []schema.Service{{Name: "api", Path: "web/apps/api", Migrate: "node bootstrap.mjs"}},
		Database: &schema.Database{Migrations: &schema.Migrations{
			Dir: "web/packages/db/migrations", Format: "sql", Table: "schema_migrations",
		}},
	}
	set := insights.Locate(fstest.MapFS{
		"web/packages/db/migrations/0001_init.sql": file("CREATE TABLE users (id int);"),
		"web/packages/db/migrations/0002_rls.sql":  file("ALTER TABLE users ENABLE ROW LEVEL SECURITY;"),
		"web/packages/db/src/migrate.ts":           file("export async function migrate() {}"),
		// A Prisma directory elsewhere in the tree, which inference would
		// have chosen first. The declaration has to win over it.
		"tools/prisma/migrations/20240101000000_x/migration.sql": file("SELECT 1;"),
	}, m)
	require.Equal(t, insights.ToolSQLDir, set.Tool)
	require.True(t, set.Declared, "the report must be able to say the project named this directory")
	require.Equal(t, "web/packages/db/migrations", set.Dir)
	require.Equal(t, "schema_migrations", set.Ledger)
	require.Len(t, set.Migrations, 2)
	// The runner records the filename, so that is the version.
	require.Equal(t, "0001_init.sql", set.Migrations[0].Version)
	require.Equal(t, "0002_rls.sql", set.Migrations[1].Version)
	require.Empty(t, set.Reason)
}

func TestLocate_ADeclaredDirectoryThatIsMissingIsSaidNotGuessed(t *testing.T) {
	t.Parallel()
	m := &schema.Manifest{Database: &schema.Database{
		Migrations: &schema.Migrations{Dir: "db/migrations"},
	}}
	set := insights.Locate(fstest.MapFS{
		"migrations/0001_init.sql": file("SELECT 1;"),
		"migrations/0002_more.sql": file("SELECT 2;"),
	}, m)
	require.Equal(t, insights.ToolSQLDir, set.Tool)
	require.True(t, set.Declared)
	require.Empty(t, set.Migrations, "a wrong declaration must not fall back to a guess")
	require.Contains(t, set.Reason, "database.migrations.dir")
	require.Contains(t, set.Reason, "no such directory")

	empty := insights.Locate(fstest.MapFS{
		"db/migrations/README.md": file("nothing here yet"),
	}, m)
	require.Empty(t, empty.Migrations)
	require.Contains(t, empty.Reason, "holds no .sql files")
}

// Without a declaration, a directory of numbered files anywhere reasonable in
// the tree is recognised. The fixed root list never reached a monorepo's
// packages directory.
func TestDiscover_FindsANumberedSQLDirectoryDeepInTheTree(t *testing.T) {
	t.Parallel()
	set := insights.Discover(fstest.MapFS{
		"web/packages/db/migrations/0001_init.sql": file("CREATE TABLE t (id int);"),
		"web/packages/db/migrations/0002_more.sql": file("ALTER TABLE t ADD COLUMN c int;"),
		"web/packages/db/migrations/notes.md":      file("not sql"),
		// Other people's projects, which are not this one's migrations even
		// when they are shallower.
		"examples/go-api/migrations/0001_init.sql":   file("SELECT 1;"),
		"examples/go-api/migrations/0002_orders.sql": file("SELECT 2;"),
		"engine/testdata/migrations/0001_a.sql":      file("SELECT 1;"),
		"engine/testdata/migrations/0002_b.sql":      file("SELECT 2;"),
		"node_modules/pkg/migrations/0001_a.sql":     file("SELECT 1;"),
		"node_modules/pkg/migrations/0002_b.sql":     file("SELECT 2;"),
	})
	require.Equal(t, insights.ToolSQLDir, set.Tool)
	require.False(t, set.Declared)
	require.Equal(t, "web/packages/db/migrations", set.Dir)
	require.Len(t, set.Migrations, 2)
	require.Equal(t, "0001_init.sql", set.Migrations[0].Version)
}

func TestDiscover_OneNumberedFileIsNotAMigrationDirectory(t *testing.T) {
	t.Parallel()
	// A schema.sql beside a 001_seed.sql is a project with a schema and a
	// seed, not a sequence anybody applies in order.
	set := insights.Discover(fstest.MapFS{
		"db/schema.sql":   file("CREATE TABLE t (id int);"),
		"db/001_seed.sql": file("INSERT INTO t VALUES (1);"),
	})
	require.Equal(t, insights.ToolNone, set.Tool)
	require.Empty(t, set.Migrations)
}

func TestLocate_TheServicePathSteersBetweenTwoCandidates(t *testing.T) {
	t.Parallel()
	tree := fstest.MapFS{
		"billing/migrations/0001_init.sql": file("SELECT 1;"),
		"billing/migrations/0002_more.sql": file("SELECT 2;"),
		"shop/db/migrations/0001_init.sql": file("SELECT 1;"),
		"shop/db/migrations/0002_more.sql": file("SELECT 2;"),
	}
	// The service that migrates lives under shop, so shop's directory is the
	// one its migrate command applies, even though billing's is shallower.
	m := &schema.Manifest{Services: []schema.Service{{Name: "web", Path: "shop", Migrate: "./migrate.sh"}}}
	require.Equal(t, "shop/db/migrations", insights.Locate(tree, m).Dir)

	// With no service path to go on, the shallowest wins and the choice is
	// stable.
	require.Equal(t, "billing/migrations", insights.Locate(tree, &schema.Manifest{}).Dir)
	require.Equal(t, "billing/migrations", insights.Discover(tree).Dir)
}

// A hand written ledger records whichever of three things its author chose.
func TestPending_MatchesALedgerThatRecordsTheNameTheStemOrTheNumber(t *testing.T) {
	t.Parallel()
	set := insights.Locate(fstest.MapFS{
		"migrations/0001_init.sql":   file("SELECT 1;"),
		"migrations/0002_rls.sql":    file("SELECT 2;"),
		"migrations/0003_orders.sql": file("SELECT 3;"),
		"migrations/0004_index.sql":  file("SELECT 4;"),
	}, &schema.Manifest{Database: &schema.Database{Migrations: &schema.Migrations{Dir: "migrations"}}})
	require.Len(t, set.Migrations, 4)

	pending := set.Pending(map[string]bool{
		"0001_init.sql": true, // the filename, as this repository's runner writes it
		"0002_rls":      true, // the stem
		"0003":          true, // the leading number
	})
	require.Len(t, pending, 1)
	require.Equal(t, "0004_index.sql", pending[0].Name)

	// A short number is not evidence. A ledger holding "1" says nothing about
	// 1_init.sql, so a set numbered without padding stays pending.
	short := insights.Declared(fstest.MapFS{
		"m/1_init.sql": file("SELECT 1;"),
		"m/2_more.sql": file("SELECT 2;"),
	}, &schema.Migrations{Dir: "m"})
	require.Len(t, short.Pending(map[string]bool{"1": true, "2": true}), 2)

	// nil means no ledger was read, and everything is pending.
	require.Len(t, set.Pending(nil), 4)
}
