package pgcopy

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// grant is one privilege the source holds for one of the roles the copy
// carries, on one object the copy carries.
type grant struct {
	// kind is one of relation, column, routine, typ, schema, or defaults.
	kind string
	// schema and name locate the object. For a schema they are the same. For
	// a default privilege, schema is the schema it is scoped to and empty for
	// a database wide one, and name is empty.
	schema, name string
	// column is set for a column privilege and empty otherwise.
	column string
	// signature is a routine's identity argument list, as
	// pg_get_function_identity_arguments renders it, and empty otherwise.
	signature string
	// target is the word after ON in the statement: TABLE, SEQUENCE, ROUTINE,
	// TYPE or SCHEMA. For a default privilege it is the plural object class
	// ALTER DEFAULT PRIVILEGES takes: TABLES, SEQUENCES, ROUTINES, TYPES or
	// SCHEMAS.
	target string
	// privilege is the privilege type as aclexplode names it: SELECT, DELETE,
	// USAGE, EXECUTE and so on.
	privilege string
	// grantee is a role name, or PUBLIC.
	grantee   string
	grantable bool
	// reset marks the statement that runs before an object's grants: REVOKE
	// ALL FROM PUBLIC, so that PUBLIC holds in the target what it holds in the
	// source and not what a fresh object hands it.
	reset bool
}

// ensureGrants gives each role the copy carries, in the target, the object
// privileges the source holds for it.
//
// The dump is taken with --no-owner and --no-privileges, and that is right:
// ownership in the target belongs to whoever runs the restore, because the
// source's owner is a role the copy does not create, and pg_restore's own
// GRANT statements would be issued as that owner and fail. What the flags
// throw away with the owner's grants is every other role's too. So a twin
// arrives with the roles production names, the memberships between them, the
// row level security policies that mention them, and not one privilege:
// antifailure_sweeper exists and is refused DELETE on sessions with 42501 on
// the first sweep, while the application, connected as the target's superuser,
// is refused nothing. A migration that runs after the copy re-grants only what
// it grants itself; a grant made by a migration the ledger says is done is
// gone in every branch. The copy was running the application under a
// privilege shape that was production's in neither direction, and the half of
// it that failed loudly was the half that was tightest in production.
//
// This reads the source's ACLs and issues the equivalent GRANT in the target:
// on relations of every kind, on columns, on routines, on types, on the
// schemas themselves, and the default privileges, so that a table a branch's
// migration creates gets what production's would. Only grantees in names,
// which is the set ensureRoles made sure of, and PUBLIC are carried, and only
// on objects in the schemas the copy dumped. PUBLIC is carried in both
// directions: a grant to it is not a credential, it is what every other role
// stands on, and a fresh function or type hands PUBLIC a privilege the source
// may have revoked, so an object with an explicit ACL in the source has
// PUBLIC's privileges revoked in the target before its grants are issued.
// Production's REVOKE EXECUTE ON FUNCTION FROM PUBLIC is otherwise undone by
// the copy without a word. The owner's own entry in each ACL is left
// out as it is in requiredRoles: it is what ownership confers, and ownership
// did not travel. The grantor of every carried grant is the target's
// connecting user rather than the source's grantor, for the same reason.
//
// Ownership stays with the connecting user on purpose. The alternative, ALTER
// OWNER to a shell of the source's owner, would put every object in the twin
// under a NOLOGIN role that nothing connects as, and a migration run by the
// connecting user would then need to be a member of it to alter anything.
// Grants are what the application feels; ownership is what the migration
// runner feels, and the second already works.
//
// Default privileges are re-declared for the target's connecting user rather
// than for the source's defaclrole. A default privilege attaches to a creating
// role, and in the target every object is created by the connecting user, so a
// declaration FOR ROLE the source's owner would sit on a role that creates
// nothing here and a branch's new tables would arrive bare.
//
// A grantee that is the target's own connecting user is skipped, as ensureRoles
// skips the source's: whoever runs the restore owns everything already, and a
// GRANT to oneself on one's own table is a no-op that would still be written.
// An object the source held a grant on and the copy does not have, because an
// extension in the target is a different version or an archive kind was left
// out, is skipped by the existence check in front of each statement rather
// than failing the copy; every object the dump carried is there by the time
// this runs.
//
// Large objects are not carried. pg_largeobject_metadata has an ACL too, and
// nothing this product copies has ever used one; a copy that needs it will
// find this comment.
func ensureGrants(ctx context.Context, source, target secrets.Value, names, excludeSchemas []string) error {
	// No early return on an empty names: PUBLIC's grants and revokes are read
	// whether or not the source names any role of its own.
	grants, err := requiredGrants(ctx, source, names, excludeSchemas)
	if err != nil {
		return err
	}
	return applyGrants(ctx, target, grants)
}

// requiredGrants reads, from the source, every privilege held by one of the
// named roles, or by PUBLIC, on an object in a copied schema, preceded by one
// reset per object that has an explicit ACL at all.
//
// The reset is what carries a REVOKE. An ACL is a list of what is granted, a
// revoked default privilege is an absence in it, and an absence cannot be read
// out by aclexplode. So every object whose ACL the source has touched gets
// REVOKE ALL FROM PUBLIC in the target first, and PUBLIC's own entries are then
// granted back with everybody else's. An object whose ACL is NULL in the
// source is at its defaults there, and a fresh copy of it is at the same
// defaults here, so it is left alone.
//
// The five catalogues are the same five requiredRoles reads, so a role that
// was created for a grant is always a role whose grant is read; columns are
// added because a column grant is how this repository keeps a sweeper from
// reading a token, and a column's ACL names no role a table's ACL would not
// have named already through a policy or another grant. A relation's ACL is
// read for tables, partitioned tables, views, materialized views, foreign
// tables and sequences, which is every relkind an ACL can sit on; indexes,
// TOAST tables and composite types have none.
func requiredGrants(ctx context.Context, conn secrets.Value, names, excludeSchemas []string) ([]grant, error) {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return nil, connectError(err, "the address in database.source_url_env")
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	// Never nil, for the reason on requiredRoles: a nil slice is NULL and
	// NOT (x = ANY (NULL)) is NULL for every schema.
	excluded := make([]string, 0, len(excludeSchemas))
	excluded = append(excluded, excludeSchemas...)
	grantees := make([]string, 0, len(names))
	grantees = append(grantees, names...)

	rows, err := db.QueryContext(ctx, `
		WITH copied AS (
			SELECT oid, nspname
			FROM pg_namespace
			WHERE nspname NOT IN ('pg_catalog', 'information_schema')
			  AND nspname NOT LIKE 'pg\_toast%'
			  AND nspname NOT LIKE 'pg\_temp\_%'
			  AND NOT (nspname = ANY ($1::text[]))
		),
		relations AS (
			SELECT s.nspname, c.relname, c.relacl, c.relowner,
			       CASE WHEN c.relkind = 'S' THEN 'SEQUENCE' ELSE 'TABLE' END AS target
			FROM pg_class c
			JOIN copied s ON s.oid = c.relnamespace
			WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
		),
		routines AS (
			SELECT s.nspname, p.proname, pg_get_function_identity_arguments(p.oid) AS sig, p.proacl, p.proowner
			FROM pg_proc p
			JOIN copied s ON s.oid = p.pronamespace
		),
		types AS (
			SELECT s.nspname, t.typname, t.typacl, t.typowner
			FROM pg_type t
			JOIN copied s ON s.oid = t.typnamespace
		),
		schemas AS (
			SELECT n.nspname, n.nspacl, n.nspowner
			FROM pg_namespace n
			JOIN copied s ON s.oid = n.oid
		),
		defaults AS (
			SELECT coalesce(s.nspname, '') AS nspname, d.defaclacl, d.defaclrole,
			       CASE d.defaclobjtype
			         WHEN 'r' THEN 'TABLES' WHEN 'S' THEN 'SEQUENCES' WHEN 'f' THEN 'ROUTINES'
			         WHEN 'T' THEN 'TYPES' WHEN 'n' THEN 'SCHEMAS' END AS target
			FROM pg_default_acl d
			LEFT JOIN copied s ON s.oid = d.defaclnamespace
			WHERE d.defaclnamespace = 0 OR s.oid IS NOT NULL
		),
		held AS (
			SELECT 0 AS ord, 'relation' AS kind, nspname AS schema, relname AS name, '' AS col, '' AS sig, target,
			       '' AS privilege_type, 0::oid AS grantee, false AS is_grantable, true AS reset
			FROM relations WHERE relacl IS NOT NULL
			UNION ALL
			SELECT 0, 'routine', nspname, proname, '', sig, 'ROUTINE', '', 0, false, true
			FROM routines WHERE proacl IS NOT NULL
			UNION ALL
			SELECT 0, 'typ', nspname, typname, '', '', 'TYPE', '', 0, false, true
			FROM types WHERE typacl IS NOT NULL
			UNION ALL
			SELECT 0, 'schema', nspname, nspname, '', '', 'SCHEMA', '', 0, false, true
			FROM schemas WHERE nspacl IS NOT NULL
			UNION ALL
			SELECT 0, 'defaults', nspname, '', '', '', target, '', 0, false, true
			FROM defaults
			UNION ALL
			SELECT 1, 'relation', x.nspname, x.relname, '', '', x.target,
			       a.privilege_type, a.grantee, a.is_grantable, false
			FROM relations x CROSS JOIN LATERAL aclexplode(x.relacl) a
			WHERE a.grantee <> x.relowner
			UNION ALL
			SELECT 1, 'column', s.nspname, c.relname, at.attname, '', 'TABLE',
			       a.privilege_type, a.grantee, a.is_grantable, false
			FROM pg_attribute at
			JOIN pg_class c ON c.oid = at.attrelid
			JOIN copied s ON s.oid = c.relnamespace
			CROSS JOIN LATERAL aclexplode(at.attacl) a
			WHERE at.attnum > 0 AND NOT at.attisdropped
			  AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
			  AND a.grantee <> c.relowner
			UNION ALL
			SELECT 1, 'routine', x.nspname, x.proname, '', x.sig, 'ROUTINE',
			       a.privilege_type, a.grantee, a.is_grantable, false
			FROM routines x CROSS JOIN LATERAL aclexplode(x.proacl) a
			WHERE a.grantee <> x.proowner
			UNION ALL
			SELECT 1, 'typ', x.nspname, x.typname, '', '', 'TYPE',
			       a.privilege_type, a.grantee, a.is_grantable, false
			FROM types x CROSS JOIN LATERAL aclexplode(x.typacl) a
			WHERE a.grantee <> x.typowner
			UNION ALL
			SELECT 1, 'schema', x.nspname, x.nspname, '', '', 'SCHEMA',
			       a.privilege_type, a.grantee, a.is_grantable, false
			FROM schemas x CROSS JOIN LATERAL aclexplode(x.nspacl) a
			WHERE a.grantee <> x.nspowner
			UNION ALL
			SELECT 1, 'defaults', x.nspname, '', '', '', x.target,
			       a.privilege_type, a.grantee, a.is_grantable, false
			FROM defaults x CROSS JOIN LATERAL aclexplode(x.defaclacl) a
			WHERE a.grantee <> x.defaclrole
		)
		SELECT h.kind, h.schema, h.name, h.col, h.sig, h.target, h.privilege_type,
		       coalesce(r.rolname, 'PUBLIC'), h.is_grantable, h.reset
		FROM held h
		LEFT JOIN pg_roles r ON r.oid = h.grantee
		WHERE (h.grantee = 0 OR r.rolname = ANY ($2::text[]))
		  AND h.target IS NOT NULL
		ORDER BY h.ord, h.kind, h.schema, h.name, h.col, h.sig, r.rolname, h.privilege_type`,
		excluded, grantees)
	if err != nil {
		return nil, fmt.Errorf("pgcopy: read the privileges the source holds for the roles it names: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []grant
	for rows.Next() {
		var g grant
		if err := rows.Scan(&g.kind, &g.schema, &g.name, &g.column, &g.signature, &g.target,
			&g.privilege, &g.grantee, &g.grantable, &g.reset); err != nil {
			return nil, fmt.Errorf("pgcopy: read a privilege: %w", err)
		}
		if !g.reset && !privilegeWord.MatchString(g.privilege) {
			// aclexplode's vocabulary is fixed and upper case. Anything else is
			// a catalogue this code does not understand, and it goes into a
			// statement, so it is refused rather than quoted.
			return nil, fmt.Errorf("pgcopy: the source reports a privilege type %q, which this copy does not know how to grant", g.privilege)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgcopy: read the privileges the source holds for the roles it names: %w", err)
	}
	return out, nil
}

// privilegeWord is the shape of every privilege type aclexplode can return.
var privilegeWord = regexp.MustCompile(`^[A-Z]+$`)

// applyGrants issues each grant in the target.
//
// The resets come first, in the query's order, and the grants after them, so
// PUBLIC's own grant on an object is issued after the revoke that cleared it
// and not before. One DO block and therefore one transaction, so a copy whose
// grants half applied cannot be published as complete: a privilege the target cannot
// grant, MAINTAIN from a 17 source into a 16 target say, fails the copy with
// the statement in the error rather than leaving a twin that is right about
// most tables. Each statement is preceded by a check that its object exists,
// for the reason on ensureGrants, and a grantee equal to the connecting user
// is skipped there rather than in the query, because the query runs against
// the source and the connecting user in question is the target's.
//
// The identifiers are quoted here rather than through format(%I) because they
// are spliced into a static statement rather than an EXECUTE, and a static
// GRANT is what plpgsql runs directly. Every name came out of the source's
// catalogue, and a table called `x"; DROP` is a table somebody was allowed to
// create, so the quoting is not optional.
func applyGrants(ctx context.Context, target secrets.Value, grants []grant) error {
	if len(grants) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("DO $af$\nBEGIN\n")
	for _, g := range grants {
		fmt.Fprintf(&b, "  IF current_user <> %s AND %s THEN\n    %s;\n  END IF;\n",
			quoteLiteral(g.grantee), g.existsCheck(), g.statement())
	}
	b.WriteString("END\n$af$;")
	if err := Exec(ctx, target, b.String()); err != nil {
		return fmt.Errorf("pgcopy: carry the privileges the source holds for the roles it names: %w", err)
	}
	return nil
}

// existsCheck is a boolean expression, true when the object the grant is on is
// present in the target.
func (g grant) existsCheck() string {
	qualified := quoteLiteral(quoteIdent(g.schema) + "." + quoteIdent(g.name))
	switch g.kind {
	case "relation":
		return fmt.Sprintf("to_regclass(%s) IS NOT NULL", qualified)
	case "column":
		return fmt.Sprintf(
			"EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = to_regclass(%s) AND attname = %s AND NOT attisdropped)",
			qualified, quoteLiteral(g.column))
	case "routine":
		// Not to_regprocedure: it parses a type list and refuses the argument
		// NAMES pg_get_function_identity_arguments renders, so a function
		// declared as f(target_email text) was "syntax error at or near text"
		// on the first real source this ran against. The catalogue is asked
		// for the same rendering instead.
		return fmt.Sprintf(
			"EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace"+
				" WHERE n.nspname = %s AND p.proname = %s AND pg_get_function_identity_arguments(p.oid) = %s)",
			quoteLiteral(g.schema), quoteLiteral(g.name), quoteLiteral(g.signature))
	case "typ":
		return fmt.Sprintf("to_regtype(%s) IS NOT NULL", qualified)
	case "schema":
		return fmt.Sprintf("EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = %s)", quoteLiteral(g.schema))
	case "defaults":
		if g.schema == "" {
			return "TRUE"
		}
		return fmt.Sprintf("EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = %s)", quoteLiteral(g.schema))
	}
	return "FALSE"
}

// statement is the GRANT, or the ALTER DEFAULT PRIVILEGES, that puts this
// privilege in the target, or the REVOKE that clears PUBLIC's before it.
//
// PUBLIC is a keyword rather than a name, so it is the one grantee written
// without quotes: a quoted "PUBLIC" would be a role called PUBLIC, and there is
// no such role.
func (g grant) statement() string {
	if g.reset {
		switch g.kind {
		case "schema":
			return fmt.Sprintf("REVOKE ALL ON SCHEMA %s FROM PUBLIC", quoteIdent(g.schema))
		case "routine":
			return fmt.Sprintf("REVOKE ALL ON ROUTINE %s.%s(%s) FROM PUBLIC",
				quoteIdent(g.schema), quoteIdent(g.name), g.signature)
		case "defaults":
			in := ""
			if g.schema != "" {
				in = " IN SCHEMA " + quoteIdent(g.schema)
			}
			return fmt.Sprintf("ALTER DEFAULT PRIVILEGES%s REVOKE ALL ON %s FROM PUBLIC", in, g.target)
		default:
			return fmt.Sprintf("REVOKE ALL ON %s %s.%s FROM PUBLIC",
				g.target, quoteIdent(g.schema), quoteIdent(g.name))
		}
	}
	option := ""
	if g.grantable {
		option = " WITH GRANT OPTION"
	}
	grantee := "PUBLIC"
	if g.grantee != "PUBLIC" {
		grantee = quoteIdent(g.grantee)
	}
	switch g.kind {
	case "column":
		return fmt.Sprintf("GRANT %s (%s) ON TABLE %s.%s TO %s%s",
			g.privilege, quoteIdent(g.column), quoteIdent(g.schema), quoteIdent(g.name), grantee, option)
	case "routine":
		return fmt.Sprintf("GRANT %s ON ROUTINE %s.%s(%s) TO %s%s",
			g.privilege, quoteIdent(g.schema), quoteIdent(g.name), g.signature, grantee, option)
	case "schema":
		return fmt.Sprintf("GRANT %s ON SCHEMA %s TO %s%s", g.privilege, quoteIdent(g.schema), grantee, option)
	case "defaults":
		in := ""
		if g.schema != "" {
			in = " IN SCHEMA " + quoteIdent(g.schema)
		}
		return fmt.Sprintf("ALTER DEFAULT PRIVILEGES%s GRANT %s ON %s TO %s%s",
			in, g.privilege, g.target, grantee, option)
	default:
		return fmt.Sprintf("GRANT %s ON %s %s.%s TO %s%s",
			g.privilege, g.target, quoteIdent(g.schema), quoteIdent(g.name), grantee, option)
	}
}

// quoteIdent renders a string as a Postgres identifier.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
