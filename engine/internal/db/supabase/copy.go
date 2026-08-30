package supabase

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the pgx driver

	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Copying a database into a Supabase branch is not pg_dump piped into
// pg_restore, and every line of this file exists because that was tried against
// two real branches and failed.
//
// A Supabase database is not empty when you get it. The platform owns auth,
// storage, realtime, graphql, extensions, vault and supabase_migrations in the
// source AND in the target, so a plain copy dies immediately on `schema "auth"
// already exists`. Excluding those schemas is necessary and not sufficient: the
// archive still carries cluster wide objects, a publication and six event
// triggers owned by supabase_admin, which fail the same way. pg_dump has a flag
// for publications and none for event triggers, so they are filtered out of the
// archive's table of contents instead.
//
// pg_restore --clean is not available either: it emits DROP EVENT TRIGGER for
// the platform's own triggers and fails with "must be owner of event trigger
// pgrst_drop_watch". So making a target ready to receive a golden is this
// provider's job, and it is done by dropping the application's objects while
// leaving the schemas themselves alone. Dropping the schema would be simpler and
// is a trap: Supabase's grants to anon, authenticated and service_role live
// partly in pg_default_acl rows keyed to the schema's OID, so DROP SCHEMA public
// takes the default privileges with it and every table created afterwards is
// invisible to the REST API, with nothing in any log to say why.

// managedSchemas are the schemas Supabase owns in every project.
//
// They exist identically in the source and the target, so they are excluded
// from the copy. The list is the observed set on Postgres 17 plus the ones
// Supabase creates for optional extensions, because a project with pg_cron
// enabled has a cron schema and a project without it does not, and naming one
// that is absent costs nothing.
//
// It is a floor rather than the whole answer. A hardcoded list goes stale the
// day Supabase ships a new schema, and the failure would be a copy that dies on
// `schema "whatever" already exists` until somebody cut a release. So the list
// is combined with what the source database says about itself: see
// managedSchemasIn.
var managedSchemas = []string{
	"auth", "extensions", "graphql", "graphql_public", "net", "pgbouncer",
	"pgsodium", "pgsodium_masks", "realtime", "storage", "supabase_functions",
	"supabase_migrations", "vault", "cron", "dbdev", "pgtle",
	"_analytics", "_realtime", "_supavisor",
}

// carriedPlatformTables are the platform owned tables whose ROWS travel with a
// golden even though their schema does not.
//
// auth.users is here because of the single most common shape in a Supabase
// application: a public table with a foreign key to it. Without the rows, the
// restore reaches `ALTER TABLE public.profiles ADD CONSTRAINT ... REFERENCES
// auth.users` and fails the foreign key, and the way it fails is the dangerous
// part. pg_restore reports the error and carries on with the rest of the
// archive, so the data lands and the constraint does not. A golden published
// from that has referential integrity that silently is not there.
//
// auth.identities comes along because a user without one cannot sign in, which
// makes the persona a row rather than an account.
//
// Nothing else does. auth.sessions, auth.refresh_tokens and the rest of GoTrue's
// state are deliberately absent: a session token is not personal data by any
// rule the verification scanner applies, so masking would not touch it, and a
// golden carrying live sessions hands anybody who can reach a branch a working
// login as a real customer.
var carriedPlatformTables = []string{"auth.users", "auth.identities"}

// archiveKindsTheTargetAlreadyHas are table of contents entry kinds that must
// not be replayed into a Supabase branch.
//
// Event triggers are created by the platform in both databases and are owned by
// supabase_admin, so recreating them fails on ownership rather than on
// existence, which no pg_restore flag suppresses.
var archiveKindsTheTargetAlreadyHas = []string{"EVENT TRIGGER"}

// restore makes target hold exactly what source holds, for the application's
// own schemas.
//
// One function for all three cases, which is the reason to trust it: filling a
// golden candidate from production, filling an environment branch from a
// golden, and returning an environment branch to its golden are the same
// operation with different endpoints, so the conformance suite's reset
// behaviour exercises the same code the first branch of the day used.
func (p *Provider) restore(ctx context.Context, source, target secrets.Value) error {
	excluded, err := p.managedSchemasIn(ctx, source)
	if err != nil {
		return err
	}
	if err := p.emptyTarget(ctx, target, excluded); err != nil {
		return err
	}
	// Rows first, then the application's schemas, because the foreign keys in
	// the second half point at the rows in the first.
	if err := pgcopy.CopyTableData(ctx, source, target, carriedPlatformTables); err != nil {
		return fmt.Errorf("db.supabase: copy the carried platform rows: %w", err)
	}
	err = pgcopy.CopyWith(ctx, source, target, pgcopy.CopyOptions{
		ExcludeSchemas:      excluded,
		ExcludeArchiveKinds: archiveKindsTheTargetAlreadyHas,
	})
	if err != nil {
		return fmt.Errorf("db.supabase: copy the application schemas: %w", err)
	}
	return nil
}

// managedSchemasIn returns the schemas to exclude when copying out of source.
//
// The constant list, plus every schema the source says is owned by one of the
// platform's own roles. Ownership is the rule that keeps working: Supabase
// creates auth as supabase_auth_admin, storage as supabase_storage_admin, and
// extensions, graphql, realtime and vault as supabase_admin, and it will create
// whatever it adds next the same way. An application's own schemas belong to
// postgres, so nothing of yours is caught by it.
//
// A source that is not Supabase at all, which is the normal case for the
// production database a golden is built from, has none of those roles and
// contributes nothing. Naming a schema that does not exist costs pg_dump
// nothing, so the constant list is still safe to send.
func (p *Provider) managedSchemasIn(ctx context.Context, source secrets.Value) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(managedSchemas)+4)
	for _, s := range managedSchemas {
		seen[s] = true
		out = append(out, s)
	}

	db, err := sql.Open("pgx", source.Reveal())
	if err != nil {
		return nil, fmt.Errorf("db.supabase: open the source: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	rows, err := db.QueryContext(ctx, `
		SELECT nspname FROM pg_namespace
		WHERE pg_get_userbyid(nspowner) LIKE 'supabase\_%'
		ORDER BY nspname`)
	if err != nil {
		return nil, fmt.Errorf("db.supabase: ask the source which schemas the platform owns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("db.supabase: read the platform's schemas: %w", err)
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db.supabase: read the platform's schemas: %w", err)
	}
	return out, nil
}

// emptyTarget removes everything a previous restore put into a branch.
//
// It runs before every restore rather than only before a reset, because a
// branch is not reliably empty when it is created: a project with a migration
// history gives its branches the migrated schema, and restoring a golden's
// version of the same tables on top of that fails on objects that already
// exist. Running it always means a first branch and a reset take the same path,
// and the path that is used twice is the one that works.
func (p *Provider) emptyTarget(ctx context.Context, target secrets.Value, managed []string) error {
	db, err := sql.Open("pgx", target.Reveal())
	if err != nil {
		return fmt.Errorf("db.supabase: open the target branch: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	// The carried platform tables are truncated rather than dropped: the
	// platform owns the table and we own only the rows in it. Truncating
	// cascades to the rest of GoTrue's state, which is the intended effect,
	// since a reset that left yesterday's sessions pointing at rewound users
	// would be worse than one that cleared them.
	for _, table := range carriedPlatformTables {
		var exists bool
		if err := db.QueryRowContext(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			return fmt.Errorf("db.supabase: look for %s: %w", table, err)
		}
		if !exists {
			continue
		}
		if _, err := db.ExecContext(ctx, "TRUNCATE "+table+" CASCADE"); err != nil {
			return fmt.Errorf("db.supabase: empty %s: %w", table, err)
		}
	}

	statements, err := p.dropStatements(ctx, db, managed)
	if err != nil {
		return err
	}
	for _, stmt := range statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("db.supabase: %s: %w", stmt, err)
		}
	}
	return nil
}

// dropStatements asks the target which of its objects belong to the
// application.
//
// Generated by the database rather than assembled here, so that a schema this
// provider has never heard of is still emptied, and so that an object created
// by an extension is left alone: pg_depend knows which objects an extension
// owns and a hardcoded list never would.
func (p *Provider) dropStatements(ctx context.Context, db *sql.DB, managed []string) ([]string, error) {
	rows, err := db.QueryContext(ctx, dropStatementQuery, managed)
	if err != nil {
		return nil, fmt.Errorf("db.supabase: list the objects to remove: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var stmt string
		if err := rows.Scan(&stmt); err != nil {
			return nil, fmt.Errorf("db.supabase: read the objects to remove: %w", err)
		}
		out = append(out, stmt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db.supabase: read the objects to remove: %w", err)
	}
	return out, nil
}

// dropStatementQuery lists, in dependency safe order, a DROP for every object
// in the application's schemas, and then the schemas themselves.
//
// Tables and relations first with CASCADE, which takes their views, constraints
// and indexes; then routines; then types; then every application schema except
// public. Anything owned by an extension is excluded through pg_depend, and so
// is anything in a schema the platform owns.
//
// The schemas go last and public is exempt, and both halves of that are load
// bearing.
//
// Last, because the statements are all generated from the state before any of
// them run, and dropping a schema first would leave the statements after it
// naming objects in a schema that is gone.
//
// Every other schema goes, because pg_dump emits CREATE SCHEMA for one and the
// restore fails if it is already there. That is not hypothetical: a golden
// carries the _antifailure schema holding its own attestation, so the first
// branch of a golden worked and the reset of that same branch failed on
// `schema "_antifailure" already exists`. The conformance suite found it, which
// is the whole argument for having one: a behaviour that only appears the
// second time round is exactly what a hand written test forgets.
//
// public is exempt because dropping it is a documented trap on Supabase: the
// platform's grants to anon, authenticated and service_role are partly default
// privileges keyed to that schema, so DROP SCHEMA public takes them with it and
// every table created afterwards is invisible to the REST API. Emptying it
// object by object leaves the privileges exactly as they were, which was
// checked against a real branch by comparing the ACL before and after.
const dropStatementQuery = `
WITH app AS (
    SELECT n.oid, n.nspname
    FROM pg_namespace n
    WHERE NOT (n.nspname = ANY($1))
      AND n.nspname NOT LIKE 'pg\_%'
      AND n.nspname <> 'information_schema'
      AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = n.oid AND d.deptype = 'e')
)
SELECT stmt FROM (
    SELECT 1 AS ord, format('DROP %s IF EXISTS %I.%I CASCADE',
               CASE c.relkind
                   WHEN 'v' THEN 'VIEW'
                   WHEN 'm' THEN 'MATERIALIZED VIEW'
                   WHEN 'S' THEN 'SEQUENCE'
                   WHEN 'f' THEN 'FOREIGN TABLE'
                   ELSE 'TABLE'
               END, a.nspname, c.relname) AS stmt
    FROM pg_class c
    JOIN app a ON a.oid = c.relnamespace
    WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
      AND c.relispartition IS NOT TRUE
      AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = c.oid AND d.deptype = 'e')
    UNION ALL
    SELECT 2, format('DROP ROUTINE IF EXISTS %I.%I(%s) CASCADE',
               a.nspname, p.proname, pg_get_function_identity_arguments(p.oid))
    FROM pg_proc p
    JOIN app a ON a.oid = p.pronamespace
    WHERE NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e')
    UNION ALL
    SELECT 3, format('DROP TYPE IF EXISTS %I.%I CASCADE', a.nspname, t.typname)
    FROM pg_type t
    JOIN app a ON a.oid = t.typnamespace
    WHERE t.typtype IN ('e', 'c', 'd', 'r')
      AND NOT EXISTS (SELECT 1 FROM pg_class c WHERE c.oid = t.typrelid AND c.relkind <> 'c')
      AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = t.oid AND d.deptype = 'e')
    UNION ALL
    SELECT 4, format('DROP SCHEMA IF EXISTS %I CASCADE', a.nspname)
    FROM app a
    WHERE a.nspname <> 'public'
) s
ORDER BY ord`

// connString assembles a direct connection string for a branch.
//
// Assembled rather than asked for, because unlike Neon there is no endpoint
// that returns a usable one. The password is URL escaped: a Supabase generated
// password is base64ish and a stray + or / would silently truncate the host.
func connString(d Detail) secrets.Value {
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(d.DBUser, d.DBPass.Reveal()),
		Host:     fmt.Sprintf("%s:%d", d.DBHost, d.DBPort),
		Path:     "/postgres",
		RawQuery: "sslmode=require",
	}
	return secrets.NewFrom(u.String(), "supabase")
}

// pooledConnString assembles a pooled connection string from the pooler
// configuration and the branch's password.
//
// Supabase returns a connection_string with the literal text [YOUR-PASSWORD] in
// it, so the fields are used and the string is not. Returning that string
// verbatim would satisfy every test that checks a pooled string differs from a
// direct one, and fail the first time anything connected.
func pooledConnString(p Pooler, pass secrets.Value) secrets.Value {
	name := p.DBName
	if name == "" {
		name = "postgres"
	}
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(p.DBUser, pass.Reveal()),
		Host:     fmt.Sprintf("%s:%d", p.DBHost, p.DBPort),
		Path:     "/" + name,
		RawQuery: "sslmode=require",
	}
	return secrets.NewFrom(u.String(), "supabase")
}

// primaryPooler picks the pooler entry for the branch's own primary database.
//
// A project can report a read replica's pooler alongside the primary's, and
// handing an application a read replica is a bug that looks like a permissions
// problem the first time it writes.
func primaryPooler(entries []Pooler) (Pooler, bool) {
	for _, e := range entries {
		if strings.EqualFold(e.DatabaseType, "PRIMARY") {
			return e, true
		}
	}
	if len(entries) == 1 {
		return entries[0], true
	}
	return Pooler{}, false
}
