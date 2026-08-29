package insights_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
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
