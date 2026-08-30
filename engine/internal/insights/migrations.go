package insights

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Tool is the migration tool a repository uses.
type Tool string

const (
	ToolPrisma   Tool = "prisma"
	ToolDrizzle  Tool = "drizzle"
	ToolSupabase Tool = "supabase"
	ToolFlyway   Tool = "flyway"
	ToolSQLDir   Tool = "sql"
	ToolRails    Tool = "rails"
	ToolDjango   Tool = "django"
	ToolAlembic  Tool = "alembic"
	ToolKnex     Tool = "knex"
	ToolNone     Tool = ""
)

// Migration is one migration as it exists in the repository.
type Migration struct {
	// Version is the identifier the tool records in its own history table.
	// It is how a migration on disk is matched to one already applied, so it
	// has to be the tool's identifier and not ours.
	Version string `json:"version"`
	// Name is what to call it in a report.
	Name string `json:"name"`
	// Path is where it came from, so a finding can be traced to a file.
	Path string `json:"path"`
	// SQL is the file's contents.
	SQL string `json:"-"`
}

// MigrationSet is what a repository's migrations look like from here.
type MigrationSet struct {
	// Tool is the migration tool, empty when none was recognised.
	Tool Tool `json:"tool,omitempty"`
	// Dir is where the migrations live, relative to the repository root.
	Dir string `json:"dir,omitempty"`
	// Migrations are every migration on disk, in the order the tool applies
	// them.
	Migrations []Migration `json:"migrations,omitempty"`
	// Reason says why there is no SQL to read, when there is none. A tool
	// whose migrations are Ruby or Python cannot be rehearsed by replaying
	// files, and saying so is better than reporting an empty set that reads
	// like a repository with no migrations at all.
	Reason string `json:"reason,omitempty"`
}

// SQLAvailable reports whether the migrations can be read as SQL.
//
// When they can, the rehearsal applies them statement by statement and times
// each one exactly. When they cannot, it has to run the tool's own command
// inside the service's image, and the timing comes from the database rather
// than from us.
func (s MigrationSet) SQLAvailable() bool { return len(s.Migrations) > 0 }

// Discover works out which migration tool a repository uses and reads its
// migrations.
//
// This deliberately asks a narrower question than engine/internal/detect,
// which decides what command to put in the manifest. The question here is
// where the SQL is, because a rehearsal replays SQL. The two agree on the
// tool and would be wrong to disagree, so the marker files are the same ones.
func Discover(fsys fs.FS) MigrationSet {
	for _, find := range []func(fs.FS) (MigrationSet, bool){
		findPrisma, findSupabase, findDrizzle, findFlyway,
		findRails, findDjango, findAlembic, findKnex, findSQLDir,
	} {
		if set, ok := find(fsys); ok {
			return set
		}
	}
	return MigrationSet{}
}

func findPrisma(fsys fs.FS) (MigrationSet, bool) {
	dir, ok := firstDirContaining(fsys, "prisma/migrations")
	if !ok {
		return MigrationSet{}, false
	}
	// Prisma names a directory per migration and records that directory name
	// verbatim in _prisma_migrations.migration_name, so the directory name is
	// the version.
	var out []Migration
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return MigrationSet{}, false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := path.Join(dir, e.Name(), "migration.sql")
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			continue
		}
		out = append(out, Migration{Version: e.Name(), Name: e.Name(), Path: p, SQL: string(body)})
	}
	sortMigrations(out)
	return MigrationSet{Tool: ToolPrisma, Dir: dir, Migrations: out}, true
}

func findSupabase(fsys fs.FS) (MigrationSet, bool) {
	if !exists(fsys, "supabase/config.toml") {
		return MigrationSet{}, false
	}
	// Supabase records the leading timestamp, not the whole filename, in
	// supabase_migrations.schema_migrations.version.
	set := sqlFilesIn(fsys, "supabase/migrations", leadingDigits)
	set.Tool = ToolSupabase
	return set, true
}

func findDrizzle(fsys fs.FS) (MigrationSet, bool) {
	dir, ok := firstDirContaining(fsys, "drizzle")
	if !ok {
		return MigrationSet{}, false
	}
	if !exists(fsys, path.Join(dir, "meta", "_journal.json")) {
		return MigrationSet{}, false
	}
	// Drizzle hashes the file contents into __drizzle_migrations, which is not
	// something we can recompute cheaply, so the file stem is the version and
	// pending is decided by name. That is what drizzle-kit's own journal does.
	set := sqlFilesIn(fsys, dir, stem)
	set.Tool = ToolDrizzle
	return set, true
}

func findFlyway(fsys fs.FS) (MigrationSet, bool) {
	for _, dir := range []string{
		"sql", "migrations", "db/migration",
		"src/main/resources/db/migration",
	} {
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			continue
		}
		found := false
		for _, e := range entries {
			if flywayName.MatchString(e.Name()) {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		// Flyway records the version between the leading V and the double
		// underscore, with dots and underscores interchangeable.
		set := sqlFilesIn(fsys, dir, flywayVersion)
		// Flyway is the one tool here that does not apply its migrations in
		// filename order. It compares versions component by component and
		// numerically, so V1.1 comes after V1, while the filenames sort the
		// other way round because the digit in V1_1__ sorts before the
		// underscore in V1__. Applying them in filename order would run an
		// ALTER TABLE before the CREATE TABLE it needs.
		sortByVersion(set.Migrations)
		set.Tool = ToolFlyway
		return set, true
	}
	return MigrationSet{}, false
}

func findRails(fsys fs.FS) (MigrationSet, bool) {
	dir, ok := firstDirContaining(fsys, "db/migrate")
	if !ok {
		return MigrationSet{}, false
	}
	return MigrationSet{
		Tool: ToolRails, Dir: dir,
		Reason: "Rails migrations are Ruby and only ActiveRecord knows what SQL they become, " +
			"so they are rehearsed by running the project's own migrate command in its image.",
	}, true
}

func findDjango(fsys fs.FS) (MigrationSet, bool) {
	if !exists(fsys, "manage.py") {
		return MigrationSet{}, false
	}
	return MigrationSet{
		Tool: ToolDjango,
		Reason: "Django migrations are Python and only Django knows what SQL they become, " +
			"so they are rehearsed by running the project's own migrate command in its image.",
	}, true
}

func findAlembic(fsys fs.FS) (MigrationSet, bool) {
	if !exists(fsys, "alembic.ini") {
		return MigrationSet{}, false
	}
	return MigrationSet{
		Tool: ToolAlembic,
		Reason: "Alembic revisions are Python, so they are rehearsed by running the " +
			"project's own migrate command in its image.",
	}, true
}

func findKnex(fsys fs.FS) (MigrationSet, bool) {
	if !exists(fsys, "knexfile.js") && !exists(fsys, "knexfile.ts") {
		return MigrationSet{}, false
	}
	return MigrationSet{
		Tool: ToolKnex,
		Reason: "Knex migrations are JavaScript, so they are rehearsed by running the " +
			"project's own migrate command in its image.",
	}, true
}

func findSQLDir(fsys fs.FS) (MigrationSet, bool) {
	// Last, because every tool above also has a directory of SQL somewhere
	// and a project with no tool at all is the case this is for.
	for _, dir := range []string{"migrations", "migrate", "db/migrations", "sql/migrations"} {
		set := sqlFilesIn(fsys, dir, stem)
		if len(set.Migrations) == 0 {
			continue
		}
		set.Tool = ToolSQLDir
		return set, true
	}
	return MigrationSet{}, false
}

// sqlFilesIn reads every .sql file in a directory, in name order, taking each
// one's version from the supplied function.
func sqlFilesIn(fsys fs.FS, dir string, version func(string) string) MigrationSet {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return MigrationSet{Dir: dir}
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		body, err := fs.ReadFile(fsys, path.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, Migration{
			Version: version(e.Name()), Name: e.Name(),
			Path: path.Join(dir, e.Name()), SQL: string(body),
		})
	}
	sortMigrations(out)
	return MigrationSet{Dir: dir, Migrations: out}
}

// sortMigrations puts migrations in the order every one of these tools applies
// them, which is lexicographic by filename. That is why they all put a
// timestamp or a zero padded number at the front.
func sortMigrations(m []Migration) {
	sort.Slice(m, func(i, j int) bool { return m[i].Name < m[j].Name })
}

// sortByVersion orders migrations the way a tool with dotted numeric versions
// does: by each component's value, not by the text of the whole thing.
func sortByVersion(m []Migration) {
	sort.SliceStable(m, func(i, j int) bool {
		return compareVersions(m[i].Version, m[j].Version) < 0
	})
}

// compareVersions compares two dotted numeric versions. A component that is
// not a number compares as text, so a version scheme nobody anticipated still
// gets a stable order rather than a panic.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		if aerr != nil || berr != nil {
			if as[i] != bs[i] {
				return strings.Compare(as[i], bs[i])
			}
			continue
		}
		if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
	}
	// A shorter version is the earlier one: V1 comes before V1.1.
	return len(as) - len(bs)
}

var (
	flywayName  = regexp.MustCompile(`^V[0-9]`)
	digitsFront = regexp.MustCompile(`^[0-9]+`)
	flywayRe    = regexp.MustCompile(`^V([0-9._]+)__`)
)

func leadingDigits(name string) string { return digitsFront.FindString(name) }

func stem(name string) string { return strings.TrimSuffix(name, ".sql") }

func flywayVersion(name string) string {
	if m := flywayRe.FindStringSubmatch(name); m != nil {
		return strings.ReplaceAll(m[1], "_", ".")
	}
	return stem(name)
}

func exists(fsys fs.FS, p string) bool {
	_, err := fs.Stat(fsys, p)
	return err == nil
}

// firstDirContaining finds suffix at the root or one level down, which is
// where it lives in the single service repository and in the monorepo.
func firstDirContaining(fsys fs.FS, suffix string) (string, bool) {
	if info, err := fs.Stat(fsys, suffix); err == nil && info.IsDir() {
		return suffix, true
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return "", false
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
			continue
		}
		p := path.Join(e.Name(), suffix)
		if info, err := fs.Stat(fsys, p); err == nil && info.IsDir() {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		return "", false
	}
	sort.Strings(found)
	return found[0], true
}

// historyQuery is the statement that lists what a tool has already applied.
//
// Pending migrations are decided from the database rather than from git,
// because the database is the thing that will actually run them. A branch of
// a golden carries production's history table, so what is pending against the
// branch is exactly what is pending against production. Working it out from a
// diff against the base branch gets this wrong the moment somebody applies a
// migration out of band, and that is the case where a rehearsal matters most.
func historyQuery(t Tool) (string, bool) {
	switch t {
	case ToolPrisma:
		return `SELECT migration_name FROM _prisma_migrations WHERE finished_at IS NOT NULL`, true
	case ToolSupabase:
		return `SELECT version FROM supabase_migrations.schema_migrations`, true
	case ToolDrizzle:
		return `SELECT hash FROM drizzle.__drizzle_migrations`, true
	case ToolFlyway:
		return `SELECT version FROM flyway_schema_history WHERE success`, true
	case ToolRails:
		return `SELECT version FROM schema_migrations`, true
	case ToolDjango:
		return `SELECT app || '.' || name FROM django_migrations`, true
	default:
		return "", false
	}
}

// Applied reads the versions a tool has already recorded against a database.
//
// A missing history table is not an error. It is a database the tool has never
// touched, which means every migration is pending, and that is the normal
// state of a fresh branch in a project that keeps its schema elsewhere.
func (s MigrationSet) Applied(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	query, ok := historyQuery(s.Tool)
	if !ok {
		return nil, nil
	}
	rows, err := conn.Query(ctx, query)
	if err != nil {
		if isUndefinedTable(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	defer rows.Close()

	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// Pending is the migrations on disk that the database has not recorded.
func (s MigrationSet) Pending(applied map[string]bool) []Migration {
	if applied == nil {
		return s.Migrations
	}
	var out []Migration
	for _, m := range s.Migrations {
		if !applied[m.Version] {
			out = append(out, m)
		}
	}
	return out
}

// isUndefinedTable reports whether the error is Postgres saying the relation
// or the schema does not exist, which is what a history table nobody has
// created looks like.
func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 42P01 undefined_table, 3F000 invalid_schema_name.
		return pgErr.Code == "42P01" || pgErr.Code == "3F000"
	}
	return false
}
