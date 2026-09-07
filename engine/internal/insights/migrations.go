package insights

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/antifailure/antifailure/engine/pkg/schema"
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
	// Declared says the manifest named the directory, so nothing here was
	// inferred. A report reads differently when the project said where its
	// migrations are and when the engine guessed.
	Declared bool `json:"declared,omitempty"`
	// Ledger is the table a project's own runner records applied files in,
	// for a plain SQL directory. Empty means probe the usual names.
	Ledger string `json:"ledger,omitempty"`
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
	return discover(fsys, nil)
}

// Locate is Discover with the manifest in hand.
//
// A manifest that declares database.migrations wins outright, and nothing is
// inferred: the project said where its files are, and a guess that disagreed
// would rehearse the wrong directory with a straight face. Without the
// declaration, the services' paths steer the fallback search for a plain SQL
// directory, so a monorepo with two candidates picks the one beside the
// service that migrates.
//
// This is the entry point every caller should use. Discover alone is kept for
// the callers and tests that have a tree and no manifest.
func Locate(fsys fs.FS, m *schema.Manifest) MigrationSet {
	var hints []string
	if m != nil {
		if m.Database != nil && m.Database.Migrations != nil {
			return Declared(fsys, m.Database.Migrations)
		}
		for _, svc := range m.Services {
			if svc.Path != "" {
				hints = append(hints, path.Clean(svc.Path))
			}
		}
	}
	return discover(fsys, hints)
}

// Declared reads the directory the manifest names.
//
// The result is a plain SQL set whether or not the directory exists or holds
// anything: an empty declared directory is reported as declared and empty,
// which is a different fact from "no tool was recognised" and has a different
// fix. The versions are the file names, which is what a runner that records
// filenames writes to its ledger; Applied matches the stem and the leading
// number too, for a runner that records one of those instead.
func Declared(fsys fs.FS, d *schema.Migrations) MigrationSet {
	dir := path.Clean(d.Dir)
	set := sqlFilesIn(fsys, dir, func(name string) string { return name })
	set.Tool = ToolSQLDir
	set.Declared = true
	set.Ledger = d.Table
	if len(set.Migrations) == 0 {
		if _, err := fs.Stat(fsys, dir); err != nil {
			set.Reason = "the manifest declares database.migrations.dir as " + dir +
				", and there is no such directory in this checkout"
		} else {
			set.Reason = "the manifest declares database.migrations.dir as " + dir +
				", and it holds no .sql files"
		}
	}
	return set
}

func discover(fsys fs.FS, hints []string) MigrationSet {
	l := &locator{fsys: fsys, hints: hints}
	for _, find := range []func() (MigrationSet, bool){
		l.findPrisma, l.findSupabase, l.findDrizzle, l.findFlyway,
		l.findRails, l.findDjango, l.findAlembic, l.findKnex, l.findSQLDir,
	} {
		if set, ok := find(); ok {
			return set
		}
	}
	if set, ok := l.findNumberedSQLDir(); ok {
		return set
	}
	return MigrationSet{}
}

// locator is a repository being searched, together with what the manifest says
// about where its services live.
//
// Every finder is a method rather than a free function for one reason: the
// search for a marker has to be the SAME search in all of them, and it has to
// reach the hints. The version this replaced looked for prisma/migrations at
// the root and exactly one level below, which finds it in a single service
// repository and misses packages/database/prisma/migrations, apps/api/drizzle,
// services/worker/db/migrate and every other ordinary workspace layout. The
// marker FILES were worse still: supabase/config.toml, alembic.ini, manage.py
// and knexfile.js were only ever looked for at the root, so a Python service
// under services/api was answered with "no migration tool was recognised".
//
// Measured before this change, against the ten layouts in layouts_test.go
// drawn from repositories on the prospect list: 1 of 10 discovered.
type locator struct {
	fsys  fs.FS
	hints []string
	// dirCache is every directory worth looking in, computed once. Nine
	// finders each walking the tree is nine walks of a repository that can be
	// large, and they would all get the same answer.
	dirCache []searchDir
	walked   bool
}

// searchDir is one directory the search will look in, with its depth.
type searchDir struct {
	path  string
	depth int
}

// candidate is one place a marker was found, with what ranks it.
type candidate struct {
	dir   string
	depth int
	hint  bool
}

// dirs is the repository root and every directory within maxMigrationDepth of
// it that is not somebody else's project.
func (l *locator) dirs() []searchDir {
	if l.walked {
		return l.dirCache
	}
	l.walked = true
	_ = fs.WalkDir(l.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != "." && skipForMigrations(path.Base(p)) {
			return fs.SkipDir
		}
		depth := 0
		if p != "." {
			depth = strings.Count(p, "/") + 1
		}
		if depth > maxMigrationDepth {
			return fs.SkipDir
		}
		l.dirCache = append(l.dirCache, searchDir{path: p, depth: depth})
		return nil
	})
	return l.dirCache
}

// best ranks the places a marker was found and returns the one to use.
//
// A directory under a path the manifest's services name wins, because a
// monorepo with two of them should rehearse the one beside the service that
// migrates. Among the rest the shallowest wins, because a repository that has
// both a real one and a copy under a tool's own directory means the shallow
// one. Ties break by name, so the answer does not depend on the order the
// filesystem happened to hand back.
func (l *locator) best(found []candidate) (string, bool) {
	if len(found) == 0 {
		return "", false
	}
	// Stable, and the stability IS the tie break. fs.WalkDir documents that
	// it walks in lexical order and dirs() appends in that order, so equal
	// candidates arrive sorted by name already. An explicit name comparison
	// here was a line no fixture could ever fail, on fstest.MapFS or on
	// os.DirFS, because both sort their directory entries: it agreed with the
	// order it was breaking ties within. A line that cannot say no is the
	// thing this file is being changed to stop shipping, so it is gone and
	// the guarantee is by construction instead.
	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.hint != b.hint {
			return a.hint
		}
		return a.depth < b.depth
	})
	return found[0].dir, true
}

// mark builds a candidate for a path, deciding whether a hinted service claims
// it. The hint is matched against the whole path rather than the base
// directory, because "the service under shop" means shop and everything below.
func (l *locator) mark(dir string, depth int) candidate {
	c := candidate{dir: dir, depth: depth}
	for _, h := range l.hints {
		if dir == h || strings.HasPrefix(dir, h+"/") {
			c.hint = true
		}
	}
	return c
}

// dirNamed finds rel at the repository root or under any directory near it,
// and returns the best candidate rather than the first one the walk reached.
//
// valid may be nil. Where it is not, a directory that fails it is not a
// candidate and the search continues, which is the difference between "there
// is a drizzle directory and it has no journal, so give up" and "find the
// drizzle directory that has one".
func (l *locator) dirNamed(rel string, valid func(dir string) bool) (string, bool) {
	var found []candidate
	for _, d := range l.dirs() {
		p := rel
		if d.path != "." {
			p = path.Join(d.path, rel)
		}
		info, err := fs.Stat(l.fsys, p)
		if err != nil || !info.IsDir() {
			continue
		}
		if valid != nil && !valid(p) {
			continue
		}
		found = append(found, l.mark(p, d.depth))
	}
	return l.best(found)
}

// fileNamed finds a marker file at the root or under any directory near it,
// and returns the directory it is relative to, "." for the root.
//
// The directory rather than the file, because every caller wants to look
// beside it: supabase/config.toml says its migrations are in
// supabase/migrations, and a manage.py says the Django project is the tree
// around it.
func (l *locator) fileNamed(rel string, valid func(p string) bool) (string, bool) {
	var found []candidate
	for _, d := range l.dirs() {
		p := rel
		if d.path != "." {
			p = path.Join(d.path, rel)
		}
		info, err := fs.Stat(l.fsys, p)
		if err != nil || info.IsDir() {
			continue
		}
		if valid != nil && !valid(p) {
			continue
		}
		found = append(found, l.mark(d.path, d.depth))
	}
	return l.best(found)
}

// under joins a directory found by fileNamed with a path relative to it,
// keeping "." out of the result.
func under(dir, rel string) string {
	if dir == "." || dir == "" {
		return rel
	}
	return path.Join(dir, rel)
}

// contains reports whether a file holds a string, case insensitively. It is
// used where a marker file's NAME is not distinctive enough to be trusted on
// its own once the search goes deeper than the root.
func (l *locator) contains(p, want string) bool {
	body, err := fs.ReadFile(l.fsys, p)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(body)), strings.ToLower(want))
}

func (l *locator) findPrisma() (MigrationSet, bool) {
	fsys := l.fsys
	dir, ok := l.dirNamed("prisma/migrations", nil)
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

func (l *locator) findSupabase() (MigrationSet, bool) {
	fsys := l.fsys
	base, ok := l.fileNamed("supabase/config.toml", nil)
	if !ok {
		return MigrationSet{}, false
	}
	// Supabase records the leading timestamp, not the whole filename, in
	// supabase_migrations.schema_migrations.version.
	set := sqlFilesIn(fsys, under(base, "supabase/migrations"), leadingDigits)
	set.Tool = ToolSupabase
	return set, true
}

func (l *locator) findDrizzle() (MigrationSet, bool) {
	fsys := l.fsys
	// The journal is part of the predicate rather than a check afterwards. A
	// repository with a drizzle directory that holds no journal used to end
	// the search there, so a second one that did have a journal was never
	// reached.
	dir, ok := l.dirNamed("drizzle", func(d string) bool {
		return exists(l.fsys, path.Join(d, "meta", "_journal.json"))
	})
	if !ok {
		return MigrationSet{}, false
	}
	// Drizzle hashes the file contents into __drizzle_migrations, which is not
	// something we can recompute cheaply, so the file stem is the version and
	// pending is decided by name. That is what drizzle-kit's own journal does.
	set := sqlFilesIn(fsys, dir, stem)
	set.Tool = ToolDrizzle
	return set, true
}

func (l *locator) findFlyway() (MigrationSet, bool) {
	fsys := l.fsys
	for _, rel := range []string{
		"sql", "migrations", "db/migration",
		"src/main/resources/db/migration",
	} {
		dir, ok := l.dirNamed(rel, l.holdsAFlywayMigration)
		if !ok {
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

func (l *locator) findRails() (MigrationSet, bool) {
	dir, ok := l.dirNamed("db/migrate", nil)
	if !ok {
		return MigrationSet{}, false
	}
	return MigrationSet{
		Tool: ToolRails, Dir: dir,
		Reason: "Rails migrations are Ruby and only ActiveRecord knows what SQL they become, " +
			"so they are rehearsed by running the project's own migrate command in its image.",
	}, true
}

func (l *locator) findDjango() (MigrationSet, bool) {
	// manage.py is not a distinctive enough name to trust anywhere in a tree
	// the way supabase/config.toml or alembic.ini are, so the file has to say
	// django. Every manage.py django-admin has ever generated does.
	if _, ok := l.fileNamed("manage.py", func(p string) bool {
		return l.contains(p, "django")
	}); !ok {
		return MigrationSet{}, false
	}
	return MigrationSet{
		Tool: ToolDjango,
		Reason: "Django migrations are Python and only Django knows what SQL they become, " +
			"so they are rehearsed by running the project's own migrate command in its image.",
	}, true
}

func (l *locator) findAlembic() (MigrationSet, bool) {
	if _, ok := l.fileNamed("alembic.ini", nil); !ok {
		return MigrationSet{}, false
	}
	return MigrationSet{
		Tool: ToolAlembic,
		Reason: "Alembic revisions are Python, so they are rehearsed by running the " +
			"project's own migrate command in its image.",
	}, true
}

func (l *locator) findKnex() (MigrationSet, bool) {
	_, js := l.fileNamed("knexfile.js", nil)
	_, ts := l.fileNamed("knexfile.ts", nil)
	if !js && !ts {
		return MigrationSet{}, false
	}
	return MigrationSet{
		Tool: ToolKnex,
		Reason: "Knex migrations are JavaScript, so they are rehearsed by running the " +
			"project's own migrate command in its image.",
	}, true
}

func (l *locator) findSQLDir() (MigrationSet, bool) {
	fsys := l.fsys
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

// findNumberedSQLDir is the last resort: a directory of numbered SQL files
// anywhere in the tree, applied by a script of the project's own.
//
// This is the shape a project has when it outgrew a tool or never wanted one:
// 0001_init.sql, 0002_rls.sql, and a forty line runner that applies whatever
// the ledger has not seen. The fixed list in findSQLDir looks at the root
// only, so a monorepo keeping its files at web/packages/db/migrations was
// answered with "no migration tool was recognised" while the manifest declared
// lock thresholds for exactly those files.
//
// The name of the directory is not required to be "migrations", because the
// files' own names are the stronger signal: two or more .sql files that begin
// with a number and an underscore are a sequence somebody applies in order.
// A single such file is not enough, since a schema.sql beside a 001_seed.sql is
// not a migration directory.
//
// Candidates are ranked, not merged. A directory under one of the hinted
// service paths wins; among the rest the shallowest wins; ties break by name.
// Directories that hold other people's projects are skipped: examples,
// fixtures, test data, vendored code and anything dot prefixed.
func (l *locator) findNumberedSQLDir() (MigrationSet, bool) {
	var found []candidate
	for _, d := range l.dirs() {
		entries, err := fs.ReadDir(l.fsys, d.path)
		if err != nil {
			continue
		}
		n := 0
		for _, e := range entries {
			if !e.IsDir() && numberedSQL.MatchString(e.Name()) {
				n++
			}
		}
		if n < 2 {
			continue
		}
		found = append(found, l.mark(d.path, d.depth))
	}
	dir, ok := l.best(found)
	if !ok {
		return MigrationSet{}, false
	}
	set := sqlFilesIn(l.fsys, dir, func(name string) string { return name })
	set.Tool = ToolSQLDir
	return set, true
}

// holdsAFlywayMigration reports whether a directory holds a file Flyway would
// apply, which is what makes a directory named "sql" a migration directory
// rather than a directory of queries.
func (l *locator) holdsAFlywayMigration(dir string) bool {
	entries, err := fs.ReadDir(l.fsys, dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && flywayName.MatchString(e.Name()) {
			return true
		}
	}
	return false
}

// maxMigrationDepth bounds the fallback walk. Five levels reaches
// web/packages/db/migrations with room to spare and stops short of walking a
// whole node_modules that escaped the skip list under another name.
const maxMigrationDepth = 5

// skipForMigrations names the directories the search never looks inside,
// because what they hold is somebody else's project or a build of this one.
//
// It used to guard one fallback. It now guards every marker, which is what
// deepening the search made necessary: a dependency shipping its own
// prisma/migrations is that dependency's schema, and answering with it would
// rehearse somebody else's migrations with a straight face.
//
// docs/src/content/docs/concepts/insights.md prints this list in full. A
// document that names seven of thirteen reads as though it named all of them.
func skipForMigrations(base string) bool {
	if strings.HasPrefix(base, ".") {
		return true
	}
	switch base {
	case "node_modules", "vendor", "examples", "example", "testdata", "fixtures",
		"fixture", "dist", "build", "target", "tmp", "docs", "__pycache__":
		return true
	}
	return false
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
	numberedSQL = regexp.MustCompile(`^[0-9]+_.*\.sql$`)
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
	if s.Tool == ToolSQLDir {
		return s.appliedFromLedger(ctx, conn)
	}
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
//
// A plain SQL directory has no tool to say what its runner writes to the
// ledger, so a file counts as applied when the ledger holds its name, its stem
// or its leading number. Those are the three things a forty line runner
// records, and the leading number is only consulted when it is at least three
// digits, because a ledger holding "1" says nothing about 1_init.sql.
func (s MigrationSet) Pending(applied map[string]bool) []Migration {
	if applied == nil {
		return s.Migrations
	}
	var out []Migration
	for _, m := range s.Migrations {
		if applied[m.Version] {
			continue
		}
		if s.Tool == ToolSQLDir && recordedUnderAnotherName(m, applied) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func recordedUnderAnotherName(m Migration, applied map[string]bool) bool {
	if applied[m.Name] || applied[stem(m.Name)] {
		return true
	}
	if digits := leadingDigits(m.Name); len(digits) >= 3 && applied[digits] {
		return true
	}
	return false
}

// ledgerTables are the names a hand written runner gives its history table,
// tried in order when the manifest does not say.
var ledgerTables = []string{"schema_migrations", "migrations"}

// ledgerColumns are the columns such a table records the file under, tried
// in order. The first one the table has is read.
var ledgerColumns = []string{"name", "version", "filename", "migration", "id"}

// appliedFromLedger reads a plain SQL directory's history from the table the
// project's own runner keeps.
//
// The engine cannot know what a script it has never seen writes, so it looks
// for the shape every such script converges on: one table, one text column
// holding the file. This repository's own runner is the case that produced
// it: schema_migrations(name text primary key, digest text, applied_at
// timestamptz), and until the rehearsal read it every file from 0001 was
// pending against a branch that already held forty of them.
//
// A ledger that is absent, or present under a shape not recognised here, is
// not an error. The result is nil, which Pending reads as "everything on disk
// is pending", and Rehearse says so in the report rather than failing.
func (s MigrationSet) appliedFromLedger(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	tables := ledgerTables
	if s.Ledger != "" {
		tables = []string{s.Ledger}
	}
	for _, table := range tables {
		column, ok, err := ledgerColumn(ctx, conn, table)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		rows, err := conn.Query(ctx,
			"SELECT "+quoteIdent(column)+"::text FROM "+quoteQualified(table))
		if err != nil {
			if isUndefinedTable(err) {
				continue
			}
			return nil, err
		}
		applied := map[string]bool{}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return nil, err
			}
			applied[v] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return applied, nil
	}
	if s.Ledger != "" {
		return nil, fmt.Errorf("the ledger table %s named by database.migrations.table does not "+
			"exist on the branch, or has none of the columns %s", s.Ledger,
			strings.Join(ledgerColumns, ", "))
	}
	return nil, nil
}

// ledgerColumn finds which of the known columns a ledger table has, using the
// catalog rather than a probing SELECT so that a missing table is a plain
// false and not an error to classify.
func ledgerColumn(ctx context.Context, conn *pgx.Conn, table string) (string, bool, error) {
	schemaName, tableName := splitQualified(table)
	rows, err := conn.Query(ctx, `
SELECT column_name FROM information_schema.columns
WHERE table_name = $1
  AND table_schema = COALESCE($2, current_schema())
  AND data_type IN ('text', 'character varying', 'character', 'integer', 'bigint', 'numeric')`,
		tableName, nullIfEmpty(schemaName))
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return "", false, err
		}
		have[c] = true
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	for _, c := range ledgerColumns {
		if have[c] {
			return c, true, nil
		}
	}
	return "", false, nil
}

func splitQualified(name string) (schemaName, table string) {
	if i := strings.IndexByte(name, '.'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteQualified(name string) string {
	schemaName, table := splitQualified(name)
	if schemaName == "" {
		return quoteIdent(table)
	}
	return quoteIdent(schemaName) + "." + quoteIdent(table)
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
