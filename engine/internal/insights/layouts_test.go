package insights_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
)

// layout is one repository shape, named after a real project that has it.
//
// The list is the measurement this lane owes, so it is drawn from repositories
// somebody would actually install this against rather than from shapes that
// were convenient to write. Every one of them puts its migrations somewhere
// other than the root, which is the whole finding: a monorepo was answered
// with "no migration tool was recognised" and the run then summarised as ok.
type layout struct {
	// name is the layout, written the way a person would say it.
	name string
	// tree is the repository, cut down to the files discovery reads.
	tree fstest.MapFS
	// tool is what has to be recognised.
	tool insights.Tool
	// dir is where the migrations have to be found, empty where the tool's
	// migrations are not SQL and the answer is a reason instead.
	dir string
}

func layouts() []layout {
	return []layout{{
		name: "packages/database/prisma/migrations, the Cal.com shape",
		tree: fstest.MapFS{
			"package.json":                    file(`{"workspaces":["apps/*","packages/*"]}`),
			"apps/web/package.json":           file("{}"),
			"packages/database/schema.prisma": file("datasource db {}"),
			"packages/database/prisma/migrations/20240101000000_init/migration.sql": file(
				"CREATE TABLE users (id int);"),
			"packages/database/prisma/migrations/20240202000000_bookings/migration.sql": file(
				"CREATE TABLE bookings (id int);"),
		},
		tool: insights.ToolPrisma,
		dir:  "packages/database/prisma/migrations",
	}, {
		name: "apps/api/prisma/migrations, the Documenso shape",
		tree: fstest.MapFS{
			"package.json":          file("{}"),
			"apps/web/package.json": file("{}"),
			"apps/api/prisma/migrations/20240101000000_init/migration.sql": file(
				"CREATE TABLE documents (id int);"),
		},
		tool: insights.ToolPrisma,
		dir:  "apps/api/prisma/migrations",
	}, {
		name: "apps/api/drizzle, a Drizzle package below the root",
		tree: fstest.MapFS{
			"package.json":                        file("{}"),
			"apps/api/drizzle/meta/_journal.json": file(`{"entries":[]}`),
			"apps/api/drizzle/0000_init.sql":      file("CREATE TABLE t (id int);"),
			"apps/api/drizzle/0001_more.sql":      file("ALTER TABLE t ADD c int;"),
		},
		tool: insights.ToolDrizzle,
		dir:  "apps/api/drizzle",
	}, {
		name: "services/worker/db/migrate, a Rails service in a monorepo",
		tree: fstest.MapFS{
			"README.md":               file("x"),
			"services/worker/Gemfile": file("source 'x'"),
			"services/worker/db/migrate/20240101000000_create_users.rb": file(
				"class CreateUsers < ActiveRecord::Migration; end"),
		},
		tool: insights.ToolRails,
	}, {
		name: "apps/backend/supabase, a Supabase project below the root",
		tree: fstest.MapFS{
			"package.json":                      file("{}"),
			"apps/backend/supabase/config.toml": file(`project_id = "x"`),
			"apps/backend/supabase/migrations/20240101120000_init.sql": file(
				"CREATE TABLE users (id int);"),
		},
		tool: insights.ToolSupabase,
		dir:  "apps/backend/supabase/migrations",
	}, {
		name: "services/api/alembic.ini, Alembic in a Python monorepo",
		tree: fstest.MapFS{
			"README.md":                   file("x"),
			"services/api/alembic.ini":    file("[alembic]"),
			"services/api/alembic/env.py": file("# env"),
		},
		tool: insights.ToolAlembic,
	}, {
		name: "apps/server/manage.py, Django below the root",
		tree: fstest.MapFS{
			"README.md": file("x"),
			// A real manage.py, cut to the line that identifies it. The name
			// alone is not distinctive enough to trust anywhere in a tree, so
			// discovery reads the file, and this is what it reads.
			"apps/server/manage.py": file(
				"import os\nos.environ.setdefault('DJANGO_SETTINGS_MODULE', 'server.settings')\n"),
		},
		tool: insights.ToolDjango,
	}, {
		name: "packages/db/knexfile.js, Knex in a workspace package",
		tree: fstest.MapFS{
			"package.json":                file("{}"),
			"packages/db/knexfile.js":     file("module.exports = {};"),
			"packages/db/migrations/x.js": file("exports.up = () => {};"),
		},
		tool: insights.ToolKnex,
	}, {
		name: "services/api/src/main/resources/db/migration, Flyway in a JVM service",
		tree: fstest.MapFS{
			"pom.xml": file("<project/>"),
			"services/api/src/main/resources/db/migration/V1__init.sql": file(
				"CREATE TABLE t (id int);"),
			"services/api/src/main/resources/db/migration/V2__more.sql": file(
				"ALTER TABLE t ADD c int;"),
		},
		tool: insights.ToolFlyway,
		dir:  "services/api/src/main/resources/db/migration",
	}, {
		name: "web/packages/db/migrations, this repository's own shape",
		tree: fstest.MapFS{
			"package.json": file("{}"),
			"web/packages/db/migrations/0001_init.sql": file("CREATE TABLE t (id int);"),
			"web/packages/db/migrations/0002_rls.sql":  file("ALTER TABLE t ENABLE ROW LEVEL SECURITY;"),
		},
		tool: insights.ToolSQLDir,
		dir:  "web/packages/db/migrations",
	}}
}

// TestDiscover_TheLayoutsRealRepositoriesUse is the lane's measurement.
//
// It fails loudly rather than reporting a fraction, because a fraction is what
// the summary line did: an honest number beside a headline that reads as a
// pass is the defect this lane exists to fix. The count is logged so the
// number can be quoted, and the assertion is that it is all of them.
func TestDiscover_TheLayoutsRealRepositoriesUse(t *testing.T) {
	t.Parallel()
	all := layouts()
	found := 0
	for _, l := range all {
		l := l
		t.Run(l.name, func(t *testing.T) {
			t.Parallel()
			set := insights.Discover(l.tree)
			require.Equal(t, l.tool, set.Tool, "the tool was not recognised")
			if l.dir != "" {
				require.Equal(t, l.dir, set.Dir, "the migrations were found somewhere else")
				require.NotEmpty(t, set.Migrations, "the directory was named and read as empty")
			} else {
				require.NotEmpty(t, set.Reason,
					"a tool whose migrations are not SQL has to say why it cannot replay them")
			}
		})
		if set := insights.Discover(l.tree); set.Tool == l.tool {
			found++
		}
	}
	t.Logf("nested migration layouts discovered: %d of %d", found, len(all))
	require.Equal(t, len(all), found)
}

// The two guards the deeper search needed, each proved able to say no.
//
// Searching the whole tree for a marker file is only safe while the marker is
// distinctive. Going deeper made two of them stop being distinctive on their
// own, so each gained a predicate, and a predicate with no test that fails
// without it is the same unfalsifiable check this lane is about.

func TestDiscover_AManagePyThatIsNotDjangoIsNotDjango(t *testing.T) {
	t.Parallel()
	// manage.py is a name anybody can use for a script. At the root that was
	// unlikely enough to ignore; anywhere in a monorepo it is not, and
	// answering "django" here would send the rehearsal to run a migrate
	// command that does not exist.
	set := insights.Discover(fstest.MapFS{
		"README.md":           file("x"),
		"tools/ops/manage.py": file("#!/usr/bin/env python\nprint('restart the queue')\n"),
	})
	require.Equal(t, insights.ToolNone, set.Tool)
}

func TestDiscover_ADrizzleDirectoryWithNoJournalDoesNotEndTheSearch(t *testing.T) {
	t.Parallel()
	// Two directories called drizzle, one of them a package that vendors the
	// name and has no journal. Finding the first and giving up is what the
	// old search did, because the journal was checked after the search rather
	// than inside it.
	set := insights.Discover(fstest.MapFS{
		"apps/admin/drizzle/readme.md":        file("not a migrations directory"),
		"apps/api/drizzle/meta/_journal.json": file(`{"entries":[]}`),
		"apps/api/drizzle/0000_init.sql":      file("CREATE TABLE t (id int);"),
	})
	require.Equal(t, insights.ToolDrizzle, set.Tool)
	require.Equal(t, "apps/api/drizzle", set.Dir)
}

func TestDiscover_AMarkerInsideADependencyIsNotThisRepositorysMigrations(t *testing.T) {
	t.Parallel()
	// The skip list existed before the search went deeper, and it guarded one
	// fallback. Now every marker is looked for at every depth, so it guards
	// all of them: a dependency that ships its own prisma directory is a
	// marker file sitting in the tree, and rehearsing its migrations against
	// this repository's database would be worse than finding nothing.
	set := insights.Discover(fstest.MapFS{
		"package.json": file("{}"),
		"node_modules/@acme/billing/prisma/migrations/20200101000000_init/migration.sql": file(
			"CREATE TABLE acme_invoices (id int);"),
		"packages/db/prisma/migrations/20240101000000_init/migration.sql": file(
			"CREATE TABLE users (id int);"),
	})
	require.Equal(t, insights.ToolPrisma, set.Tool)
	require.Equal(t, "packages/db/prisma/migrations", set.Dir)

	// And when the dependency's copy is the only one, the answer is that
	// nothing was recognised, not somebody else's schema.
	only := insights.Discover(fstest.MapFS{
		"package.json": file("{}"),
		"node_modules/@acme/billing/prisma/migrations/20200101000000_init/migration.sql": file(
			"CREATE TABLE acme_invoices (id int);"),
	})
	require.Equal(t, insights.ToolNone, only.Tool)
}

func TestDiscover_TwoCandidatesAtTheSameDepthAnswerTheSameWayEveryTime(t *testing.T) {
	t.Parallel()
	// The outcome, not the mechanism. best() no longer compares names, it
	// relies on fs.WalkDir's documented lexical order and a stable sort, so
	// there is no line here a mutation could break. What can still be pinned
	// is that the answer is the same one every time, which is what stops a
	// monorepo rehearsing a different directory on a different machine.
	tree := fstest.MapFS{
		"package.json": file("{}"),
		"apps/api/prisma/migrations/20240101000000_init/migration.sql":   file("CREATE TABLE a (id int);"),
		"apps/admin/prisma/migrations/20240101000000_init/migration.sql": file("CREATE TABLE b (id int);"),
	}
	first := insights.Discover(tree)
	require.Equal(t, insights.ToolPrisma, first.Tool)
	require.Equal(t, "apps/admin/prisma/migrations", first.Dir)
	for i := 0; i < 20; i++ {
		require.Equal(t, first.Dir, insights.Discover(tree).Dir,
			"the same repository answered two different directories")
	}
}
