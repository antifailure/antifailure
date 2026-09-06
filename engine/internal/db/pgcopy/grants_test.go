package pgcopy

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The privileges a copy carries are database objects rather than cluster
// ones, which is the opposite of the roles in roles_test.go: a copy between
// two databases on ONE cluster restores every object with an empty ACL, so
// a target on the same cluster lacks every grant the source has, and the
// whole thing can be proved without a second server. The two cluster test
// carries the sweeper case anyway, because that is where the roles are made
// as well as granted to.

// The query, against a real catalogue: one of each shape it has to find, the
// owner's own entry it has to leave out, and the schema it has to leave alone.
func TestRequiredGrants_ReadsEveryShape(t *testing.T) {
	admin := adminForRolesTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	f := newRolesTestFixture(t, ctx, admin)

	names, err := requiredRoles(ctx, f.source(admin), []string{"platform"})
	require.NoError(t, err)
	// The owner is passed as if it were carried, which it would be were it
	// granted on any other object, so that its own entry in each ACL is shown
	// to be left out on purpose rather than by not being in the list.
	grants, err := requiredGrants(ctx, f.source(admin), append(names, f.owner), []string{"platform"})
	require.NoError(t, err)

	require.Contains(t, grants, grant{kind: "relation", schema: "public", name: "usage", target: "TABLE",
		privilege: "SELECT", grantee: f.byGrant},
		"SELECT on a table, the grant that reached the twin as a name and nothing else")
	require.Contains(t, grants, grant{kind: "relation", schema: "public", name: "usage", target: "TABLE",
		privilege: "DELETE", grantee: f.deleter},
		"a different privilege on the same table to a different role")
	require.Contains(t, grants, grant{kind: "relation", schema: "public", name: "usage_seq", target: "SEQUENCE",
		privilege: "USAGE", grantee: f.deleter, grantable: true},
		"a sequence is granted ON SEQUENCE, and WITH GRANT OPTION is read from is_grantable")
	require.Contains(t, grants, grant{kind: "column", schema: "public", name: "sessions", column: "expires_at",
		target: "TABLE", privilege: "SELECT", grantee: f.sweeper},
		"the column grant is the half of 0024 that row level security cannot do")
	require.Contains(t, grants, grant{kind: "routine", schema: "public", name: "answer", target: "ROUTINE",
		privilege: "EXECUTE", grantee: f.byFunction})
	require.Contains(t, grants, grant{kind: "routine", schema: "public", name: "greet", signature: "who text",
		target: "ROUTINE", privilege: "EXECUTE", grantee: f.byFunction},
		"the signature carries the argument name, which is how this repository declares every function")
	require.Contains(t, grants, grant{kind: "typ", schema: "public", name: "email", target: "TYPE",
		privilege: "USAGE", grantee: f.byFunction}, "a domain's ACL is in pg_type like any type's")
	require.Contains(t, grants, grant{kind: "schema", schema: "reporting", name: "reporting", target: "SCHEMA",
		privilege: "USAGE", grantee: f.bySchema})
	require.Contains(t, grants, grant{kind: "defaults", schema: "public", target: "TABLES",
		privilege: "SELECT", grantee: f.byDefault},
		"a default privilege is what a table made by a branch's migration will get")
	require.Contains(t, grants, grant{kind: "relation", schema: "public", name: "notice", target: "TABLE",
		privilege: "SELECT", grantee: "PUBLIC"},
		"a grant to PUBLIC is carried: it is not a credential and every other role stands on it")

	// The resets: one per object whose ACL the source has touched, ahead of
	// every grant, so the REVOKE FROM PUBLIC on the function survives the copy.
	require.Contains(t, grants, grant{kind: "routine", schema: "public", name: "answer", target: "ROUTINE",
		grantee: "PUBLIC", reset: true},
		"a function whose EXECUTE was revoked from PUBLIC has to be reset before its grants")
	require.Contains(t, grants, grant{kind: "typ", schema: "public", name: "email", target: "TYPE",
		grantee: "PUBLIC", reset: true})
	require.Contains(t, grants, grant{kind: "defaults", schema: "", target: "ROUTINES",
		grantee: "PUBLIC", reset: true},
		"a default privilege revoked from PUBLIC is an absence in pg_default_acl, and the reset is the only way to carry it; "+
			"database wide, because a per schema default only ever adds to the global one and cannot revoke from it")
	lastReset, firstGrant := -1, len(grants)
	for i, g := range grants {
		if g.reset && i > lastReset {
			lastReset = i
		}
		if !g.reset && i < firstGrant {
			firstGrant = i
		}
	}
	require.Less(t, lastReset, firstGrant, "every reset has to come before the first grant, or a grant to PUBLIC is revoked again")

	for _, g := range grants {
		require.NotEqual(t, f.owner, g.grantee, "the owner's own entry is what ownership confers, and ownership did not travel: %+v", g)
		require.NotEqual(t, "platform", g.schema, "a grant in a schema the copy leaves out must stay behind: %+v", g)
		require.NotEqual(t, f.excluded, g.grantee)
		if g.reset {
			require.Empty(t, g.privilege)
			require.Equal(t, "PUBLIC", g.grantee)
		} else {
			require.Regexp(t, `^[A-Z]+$`, g.privilege)
		}
	}
}

// The statements, as text, because a quoting mistake here is a statement that
// runs against somebody else's database with a name of their choosing in it.
func TestGrantStatements_QuoteEveryIdentifierAndNeverPUBLIC(t *testing.T) {
	odd := grant{kind: "relation", schema: `Mixed "Case`, name: `x"; DROP TABLE y`, target: "TABLE",
		privilege: "SELECT", grantee: `who"ever`, grantable: true}
	require.Equal(t, `GRANT SELECT ON TABLE "Mixed ""Case"."x""; DROP TABLE y" TO "who""ever" WITH GRANT OPTION`, odd.statement())
	require.Equal(t, `to_regclass('"Mixed ""Case"."x""; DROP TABLE y"') IS NOT NULL`, odd.existsCheck())

	col := grant{kind: "column", schema: "public", name: "sessions", column: "expires_at", target: "TABLE",
		privilege: "SELECT", grantee: "sweeper"}
	require.Equal(t, `GRANT SELECT ("expires_at") ON TABLE "public"."sessions" TO "sweeper"`, col.statement())

	fn := grant{kind: "routine", schema: "public", name: "answer", signature: "integer, text", target: "ROUTINE",
		privilege: "EXECUTE", grantee: "PUBLIC"}
	require.Equal(t, `GRANT EXECUTE ON ROUTINE "public"."answer"(integer, text) TO PUBLIC`, fn.statement(),
		"PUBLIC is a keyword: quoted, it would be a role called PUBLIC, and there is no such role")
	require.Equal(t, `EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace`+
		` WHERE n.nspname = 'public' AND p.proname = 'answer' AND pg_get_function_identity_arguments(p.oid) = 'integer, text')`,
		fn.existsCheck(), "to_regprocedure cannot read an argument name, and the identity arguments carry them")

	require.Equal(t, `REVOKE ALL ON ROUTINE "public"."answer"(integer, text) FROM PUBLIC`,
		grant{kind: "routine", schema: "public", name: "answer", signature: "integer, text", target: "ROUTINE",
			grantee: "PUBLIC", reset: true}.statement())
	require.Equal(t, `GRANT USAGE ON SCHEMA "reporting" TO "r"`,
		grant{kind: "schema", schema: "reporting", name: "reporting", target: "SCHEMA", privilege: "USAGE", grantee: "r"}.statement())
	require.Equal(t, `GRANT USAGE ON TYPE "public"."email" TO "r"`,
		grant{kind: "typ", schema: "public", name: "email", target: "TYPE", privilege: "USAGE", grantee: "r"}.statement())
	require.Equal(t, `ALTER DEFAULT PRIVILEGES IN SCHEMA "public" GRANT SELECT ON TABLES TO "r"`,
		grant{kind: "defaults", schema: "public", target: "TABLES", privilege: "SELECT", grantee: "r"}.statement())
	require.Equal(t, `ALTER DEFAULT PRIVILEGES REVOKE ALL ON ROUTINES FROM PUBLIC`,
		grant{kind: "defaults", target: "ROUTINES", grantee: "PUBLIC", reset: true}.statement(),
		"a database wide default has no IN SCHEMA")
	require.Equal(t, "TRUE", grant{kind: "defaults", target: "ROUTINES", grantee: "PUBLIC", reset: true}.existsCheck())
}

// A grantee that is the target's own connecting user is left out, the way
// ensureRoles leaves out the source's. Shown on a table the connecting user
// does not own, because on one it owns a GRANT to itself changes nothing and
// there would be nothing to see.
func TestApplyGrants_LeavesOutTheConnectingUser(t *testing.T) {
	admin := adminForRolesTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	f := newRolesTestFixture(t, ctx, admin)
	me := currentUserForTest(t, ctx, admin)

	// The fixture's table is owned by the owner role, and the connecting user
	// is a superuser with no entry in its ACL.
	require.NoError(t, applyGrants(ctx, f.source(admin), []grant{
		{kind: "relation", schema: "public", name: "usage", target: "TABLE", privilege: "SELECT", grantee: me},
		{kind: "relation", schema: "public", name: "usage", target: "TABLE", privilege: "INSERT", grantee: f.deleter},
		{kind: "relation", schema: "public", name: "gone", target: "TABLE", privilege: "SELECT", grantee: f.deleter},
	}))
	acl, err := queryOneForTest(ctx, f.source(admin),
		"SELECT coalesce(relacl::text, '') FROM pg_class WHERE oid = 'public.usage'::regclass")
	require.NoError(t, err)
	require.NotContains(t, acl, me+"=", "the connecting user owns the restore and needs no grant")
	require.Contains(t, acl, f.deleter+"=ad/", "and the grant beside it was applied, so the skip is the skip and not a failure")
	// The third grant named a table that does not exist, and the block ran to
	// the end: an object the copy did not carry is skipped, not fatal.
}

// The whole thing, on one cluster: a fresh target database has none of the
// source's ACLs however many of its roles it shares, so after the copy each
// role is entered and asked to do what the source allowed and what it did
// not. Both halves are asserted, because a grant that travels is only half of
// fidelity: the other half is that nothing else came with it.
func TestACopyCarriesTheGrantsOfTheRolesItCarries(t *testing.T) {
	admin := adminForRolesTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	f := newRolesTestFixture(t, ctx, admin)

	targetDB := fmt.Sprintf("af_grants_dst_%d", f.stamp)
	require.NoError(t, Exec(ctx, secrets.New(admin), "CREATE DATABASE "+targetDB))
	t.Cleanup(func() {
		_ = Exec(context.WithoutCancel(ctx), secrets.New(admin), "DROP DATABASE IF EXISTS "+targetDB+" WITH (FORCE)")
	})
	target := secrets.New(replaceDatabaseForTest(admin, targetDB))

	require.NoError(t, CopyWith(ctx, f.source(admin), target, CopyOptions{ExcludeSchemas: []string{"platform"}}))

	as := func(role, statement string) error {
		return Exec(ctx, target, "SET ROLE "+role+"; "+statement)
	}

	// The table: SELECT to one role, DELETE to another, and neither has the
	// other's.
	require.NoError(t, as(f.byGrant, "SELECT * FROM public.usage"))
	requireDeniedForTest(t, as(f.byGrant, "DELETE FROM public.usage WHERE false"), "byGrant was granted SELECT and nothing else")
	require.NoError(t, as(f.deleter, "DELETE FROM public.usage WHERE false"))
	requireDeniedForTest(t, as(f.deleter, "SELECT * FROM public.usage"), "deleter was granted DELETE and nothing else")

	// The sequence, with its grant option.
	require.NoError(t, as(f.deleter, "SELECT nextval('public.usage_seq')"))
	requireDeniedForTest(t, as(f.byGrant, "SELECT nextval('public.usage_seq')"))
	grantable, err := queryOneForTest(ctx, target, fmt.Sprintf(
		"SELECT has_sequence_privilege(%s, 'public.usage_seq', 'USAGE WITH GRANT OPTION')::text", quoteLiteral(f.deleter)))
	require.NoError(t, err)
	require.Equal(t, "true", grantable, "WITH GRANT OPTION has to travel, it is part of the privilege")

	// The function: granted to one role, and revoked from PUBLIC in the
	// source, which a fresh copy of the function undoes unless the copy
	// carries the revoke.
	require.NoError(t, as(f.byFunction, "SELECT public.answer()"))
	require.NoError(t, as(f.byFunction, "SELECT public.greet('x')"),
		"a function with a named argument is the shape the first real source had, and the existence check refused it")
	requireDeniedForTest(t, as(f.deleter, "SELECT public.answer()"),
		"EXECUTE was revoked from PUBLIC in the source, and a fresh function hands it back to everybody")

	// The domain, the same shape. USAGE on a type is checked when the type is
	// used to make something, not when a value is cast to it, so the probe is
	// a temporary table with a column of it.
	require.NoError(t, as(f.byFunction, "CREATE TEMP TABLE typed_ok (e public.email)"))
	requireDeniedForTest(t, as(f.deleter, "CREATE TEMP TABLE typed_no (e public.email)"),
		"USAGE was revoked from PUBLIC in the source, and a fresh domain hands it back")

	// The schema.
	usage, err := queryOneForTest(ctx, target, fmt.Sprintf(
		"SELECT has_schema_privilege(%s, 'reporting', 'USAGE')::text || has_schema_privilege(%s, 'reporting', 'USAGE')::text",
		quoteLiteral(f.bySchema), quoteLiteral(f.deleter)))
	require.NoError(t, err)
	require.Equal(t, "truefalse", usage)

	// The grant to PUBLIC travelled: a role granted nothing on the table
	// itself reads it through PUBLIC, as in the source.
	require.NoError(t, as(f.deleter, "SELECT * FROM public.notice"))

	// The default privilege: a table made in the target by the connecting
	// user, as a branch's migration would make it, is readable by the role
	// the source's default named.
	require.NoError(t, Exec(ctx, target, "CREATE TABLE public.made_later (id int)"))
	require.NoError(t, as(f.byDefault, "SELECT * FROM public.made_later"),
		"a default privilege declared in the source has to apply to what a migration creates in the branch")
	requireDeniedForTest(t, as(f.byGrant, "SELECT * FROM public.made_later"))
	require.NoError(t, Exec(ctx, target, "CREATE FUNCTION public.made_later_fn() RETURNS int LANGUAGE sql AS 'SELECT 1'"))
	requireDeniedForTest(t, as(f.deleter, "SELECT public.made_later_fn()"),
		"the source revoked EXECUTE on new functions from PUBLIC by default, and a function a migration adds in the branch has to be as closed")

	// And the excluded schema's grant stayed behind with its schema.
	_, err = queryOneForTest(ctx, target, "SELECT 1 FROM pg_namespace WHERE nspname = 'platform'")
	require.Error(t, err, "the excluded schema is not in the copy at all")

	// Ownership stayed with the connecting user, deliberately.
	owner, err := queryOneForTest(ctx, target, "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid = 'public.usage'::regclass")
	require.NoError(t, err)
	require.Equal(t, currentUserForTest(t, ctx, admin), owner)

	// A second copy over the same target is idempotent: every statement is a
	// GRANT or a REVOKE, and neither minds being repeated.
	require.NoError(t, ensureGrants(ctx, f.source(admin), target, mustRoles(t, ctx, f.source(admin)), []string{"platform"}))
	require.NoError(t, as(f.deleter, "SELECT * FROM public.notice"))
}

func mustRoles(t *testing.T, ctx context.Context, conn secrets.Value) []string {
	t.Helper()
	names, err := requiredRoles(ctx, conn, []string{"platform"})
	require.NoError(t, err)
	return names
}

// requireDeniedForTest asserts that err is Postgres refusing a privilege,
// SQLSTATE 42501, and not some other failure. A missing table is 42P01 and
// would pass a bare require.Error while proving that the object, not the
// grant, was absent.
func requireDeniedForTest(t *testing.T, err error, msgAndArgs ...any) {
	t.Helper()
	require.Error(t, err, msgAndArgs...)
	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "not a Postgres error: %v", err)
	require.Equal(t, "42501", pgErr.Code, "expected insufficient_privilege, got %s: %v", pgErr.Code, err)
}
