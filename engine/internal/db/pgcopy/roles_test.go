package pgcopy

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// The roles a copy has to carry are cluster-wide, which shapes these tests in
// a way nothing else in the package is shaped.
//
// A role made in the source cluster already exists in the target cluster when
// the two are the same cluster, so a copy between two databases on one server
// succeeds whether or not ensureRoles did anything. The end to end proof needs
// a second server, and a workstation has one only when somebody starts it, so
// that test asks for AF_TEST_TARGET_CLUSTER_URL and says plainly when it is not
// there. Everything that does not need two clusters, which is the catalogue
// query and the shape of the role that gets made, runs on one.

// rolesTestFixture is a source database with one of each thing the query is
// meant to find and one of each thing it is meant to leave alone. The stamp
// keeps two runs, and two branches sharing a cluster, from meeting.
type rolesTestFixture struct {
	stamp      int64
	byGrant    string // named only by a GRANT, on a table, and by nothing else
	byDefault  string // named only by ALTER DEFAULT PRIVILEGES
	byFunction string // named only by a GRANT on a function
	bySchema   string // named only by a GRANT on a schema
	byPolicy   string // named only by a policy's TO clause, and a NOINHERIT member of byGrant
	bypasser   string // named by a GRANT, and BYPASSRLS in the source
	owner      string // owns everything, has byGrant as a member, is named by nothing else
	excluded   string // named only in a schema the copy leaves out
	sourceDB   string
}

func newRolesTestFixture(t *testing.T, ctx context.Context, admin string) rolesTestFixture {
	t.Helper()
	stamp := time.Now().UnixNano()
	f := rolesTestFixture{
		stamp:      stamp,
		byGrant:    fmt.Sprintf("af_roles_grant_%d", stamp),
		byDefault:  fmt.Sprintf("af_roles_default_%d", stamp),
		byFunction: fmt.Sprintf("af_roles_func_%d", stamp),
		bySchema:   fmt.Sprintf("af_roles_schema_%d", stamp),
		byPolicy:   fmt.Sprintf("af_roles_policy_%d", stamp),
		bypasser:   fmt.Sprintf("af_roles_bypass_%d", stamp),
		owner:      fmt.Sprintf("af_roles_owner_%d", stamp),
		excluded:   fmt.Sprintf("af_roles_excluded_%d", stamp),
		sourceDB:   fmt.Sprintf("af_roles_src_%d", stamp),
	}

	require.NoError(t, Exec(ctx, secrets.New(admin), fmt.Sprintf(`
		CREATE ROLE %[1]s NOLOGIN;
		CREATE ROLE %[2]s NOLOGIN;
		CREATE ROLE %[3]s NOLOGIN;
		CREATE ROLE %[4]s NOLOGIN;
		CREATE ROLE %[5]s NOLOGIN;
		CREATE ROLE %[6]s NOLOGIN BYPASSRLS;
		CREATE ROLE %[7]s NOLOGIN;
		CREATE ROLE %[8]s NOLOGIN;
		GRANT %[7]s TO CURRENT_USER;
		GRANT %[1]s TO %[5]s WITH INHERIT FALSE, SET TRUE;
		GRANT %[7]s TO %[1]s;`,
		f.byGrant, f.byDefault, f.byFunction, f.bySchema, f.byPolicy,
		f.bypasser, f.owner, f.excluded)))
	// On its own: a script runs in one transaction and CREATE DATABASE refuses
	// to be in one.
	require.NoError(t, Exec(ctx, secrets.New(admin), fmt.Sprintf(
		"CREATE DATABASE %s OWNER %s", f.sourceDB, f.owner)))
	t.Cleanup(func() {
		c := context.WithoutCancel(ctx)
		_ = Exec(c, secrets.New(admin), "DROP DATABASE IF EXISTS "+f.sourceDB+" WITH (FORCE)")
		for _, r := range f.roles() {
			_ = Exec(c, secrets.New(admin), "DROP ROLE IF EXISTS "+r)
		}
	})

	// The default privilege is declared LAST, after every table exists. Declared
	// first, it would land in each later table's ACL and the role would be found
	// through the table rather than through pg_default_acl, which is the half
	// of the query it is here to exercise.
	// Everything is owned by the owner role, so that the owner's own entry in
	// each ACL is present and has to be ignored on purpose rather than by
	// accident of being the connecting user.
	source := replaceDatabaseForTest(admin, f.sourceDB)
	require.NoError(t, Exec(ctx, secrets.New(source), fmt.Sprintf(`
		SET ROLE %[7]s;
		CREATE TABLE public.usage (id int PRIMARY KEY, who text);
		GRANT SELECT ON public.usage TO %[1]s;
		GRANT SELECT ON public.usage TO %[6]s;
		CREATE FUNCTION public.answer() RETURNS int LANGUAGE sql AS 'SELECT 42';
		REVOKE ALL ON FUNCTION public.answer() FROM PUBLIC;
		GRANT EXECUTE ON FUNCTION public.answer() TO %[3]s;
		CREATE SCHEMA reporting;
		GRANT USAGE ON SCHEMA reporting TO %[4]s;
		CREATE TABLE public.guarded (id int PRIMARY KEY, who text);
		ALTER TABLE public.guarded ENABLE ROW LEVEL SECURITY;
		CREATE POLICY guarded_for_one ON public.guarded FOR SELECT TO %[5]s USING (true);
		CREATE SCHEMA platform;
		CREATE TABLE platform.internal (id int PRIMARY KEY);
		GRANT SELECT ON platform.internal TO %[8]s;
		ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO %[2]s;`,
		f.byGrant, f.byDefault, f.byFunction, f.bySchema, f.byPolicy,
		f.bypasser, f.owner, f.excluded)))
	return f
}

func (f rolesTestFixture) roles() []string {
	return []string{f.byGrant, f.byDefault, f.byFunction, f.bySchema, f.byPolicy, f.bypasser, f.owner, f.excluded}
}

func (f rolesTestFixture) source(admin string) secrets.Value {
	return secrets.New(replaceDatabaseForTest(admin, f.sourceDB))
}

// adminForRolesTest is a connection that may create roles and databases. The
// same variable the rest of the package uses, and the same rule: naming a
// server is a statement that one is meant to be there.
func adminForRolesTest(t *testing.T) string {
	t.Helper()
	return testDatabaseURL(t).Reveal()
}

// The query, against a real catalogue, because a fixture written by hand
// agrees with whatever the query happened to say.
func TestRequiredRoles_ReadsGrantsAsWellAsPolicies(t *testing.T) {
	admin := adminForRolesTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	f := newRolesTestFixture(t, ctx, admin)

	names, err := requiredRoles(ctx, f.source(admin), []string{"platform"})
	require.NoError(t, err)

	require.Contains(t, names, f.byGrant,
		"a role named only by a GRANT on a table is the case that stopped this repository's own twin")
	require.Contains(t, names, f.byFunction, "a GRANT on a function names a role too")
	require.Contains(t, names, f.bySchema, "so does a GRANT on a schema")
	require.Contains(t, names, f.byDefault,
		"default privileges name a role before there is anything to grant on")
	require.Contains(t, names, f.byPolicy,
		"the policy half is still here, it is what the restore itself needs")
	require.Contains(t, names, f.bypasser)

	require.NotContains(t, names, f.owner,
		"ownership is dropped by the dump, so the owner's own ACL entry must not bring the owner along")
	require.NotContains(t, names, f.excluded,
		"a grant in a schema the copy leaves out must not bring that schema's roles along")
	for _, n := range names {
		require.NotEqual(t, "PUBLIC", n)
		require.NotEqual(t, "public", n)
	}
}

func TestRequiredRoles_LeavesOutTheConnectingUser(t *testing.T) {
	admin := adminForRolesTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	f := newRolesTestFixture(t, ctx, admin)

	// The connecting user is granted on a table like anybody else, and is
	// still not a role the target needs made.
	require.NoError(t, Exec(ctx, f.source(admin), `
		SET ROLE `+f.owner+`;
		GRANT SELECT ON public.usage TO `+currentUserForTest(t, ctx, admin)))

	names, err := requiredRoles(ctx, f.source(admin), nil)
	require.NoError(t, err)
	require.NotContains(t, names, currentUserForTest(t, ctx, admin))
	require.Contains(t, names, f.byGrant, "and the rest of the list is unaffected")
	require.Contains(t, names, f.excluded,
		"with nothing excluded, a grant in any schema counts: this is the nil slice case")
}

func currentUserForTest(t *testing.T, ctx context.Context, admin string) string {
	t.Helper()
	out, err := queryOneForTest(ctx, secrets.New(admin), "SELECT current_user")
	require.NoError(t, err)
	return out
}

// The shape of what gets made: a name and nothing else. The source role here
// has BYPASSRLS, and the copy of it must not.
func TestEnsureRoles_MakesNologinShellsWithNoAttributes(t *testing.T) {
	admin := adminForRolesTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	f := newRolesTestFixture(t, ctx, admin)

	// One cluster, so the roles already exist and ensureRoles has to be
	// shown a target where they do not. Drop the one with BYPASSRLS after
	// revoking what it holds, then ask ensureRoles to put it back from a
	// source where it is still named: the source catalogue is read first and
	// the role does not exist in the target when the CREATE runs, so this is
	// the real path with no second server. The drop has to come after the
	// read, which is why it is done between a read and a create by hand.
	names, err := requiredRoles(ctx, f.source(admin), nil)
	require.NoError(t, err)
	require.Contains(t, names, f.bypasser)

	require.NoError(t, Exec(ctx, f.source(admin), fmt.Sprintf(`
		REVOKE ALL ON public.usage FROM %[1]s;
		DROP ROLE %[1]s;`, f.bypasser)))
	require.NoError(t, createRoleShells(ctx, secrets.New(admin), []string{f.bypasser}, nil))

	shape := describeRoleForTest(t, ctx, admin, f.bypasser)
	require.Equal(t, "login=f super=f createrole=f createdb=f replication=f bypassrls=f inherit=t members=0 password=f", shape,
		"the copy of a role is a name that a GRANT or a policy can resolve, and not one attribute more")

	// A membership arrives with its options, and only when asked for.
	require.NoError(t, createRoleShells(ctx, secrets.New(admin), []string{f.bypasser, f.byGrant}, []membership{
		{role: f.byGrant, member: f.bypasser, inherit: false, set: true},
	}))
	require.Equal(t, "member=t usage=f", describeMembershipForTest(t, ctx, admin, f.bypasser, f.byGrant),
		"the member may SET ROLE to it and does not inherit from it, which is the shape 0041 checks")

	// And a second run over the same names and memberships is a no-op rather
	// than an error.
	require.NoError(t, createRoleShells(ctx, secrets.New(admin), []string{f.bypasser, f.byGrant}, []membership{
		{role: f.byGrant, member: f.bypasser, inherit: false, set: true},
	}))
	require.Equal(t, "member=t usage=f", describeMembershipForTest(t, ctx, admin, f.bypasser, f.byGrant))
}

func describeMembershipForTest(t *testing.T, ctx context.Context, admin, member, role string) string {
	t.Helper()
	out, err := queryOneForTest(ctx, secrets.New(admin), fmt.Sprintf(
		`SELECT format('member=%%s usage=%%s', pg_has_role(%[1]s, %[2]s, 'MEMBER'), pg_has_role(%[1]s, %[2]s, 'USAGE'))`,
		quoteLiteral(member), quoteLiteral(role)))
	require.NoError(t, err)
	return out
}

// Only memberships with both ends in the list travel. The owner has a member
// among the carried roles and is not carried itself, so that membership stays
// behind; the source's superuser would be the same case.
func TestRequiredMemberships_OnlyBetweenCarriedRoles(t *testing.T) {
	admin := adminForRolesTest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	f := newRolesTestFixture(t, ctx, admin)

	names, err := requiredRoles(ctx, f.source(admin), nil)
	require.NoError(t, err)
	require.NotContains(t, names, f.owner)

	members, err := requiredMemberships(ctx, f.source(admin), names)
	require.NoError(t, err)
	require.Contains(t, members, membership{role: f.byGrant, member: f.byPolicy, inherit: false, set: true, admin: false},
		"the membership between two carried roles travels, with INHERIT FALSE read from the grant rather than assumed")
	for _, m := range members {
		require.NotEqual(t, f.owner, m.role, "a membership in a role the copy does not carry must stay behind")
		require.NotEqual(t, f.owner, m.member)
	}

	require.Empty(t, mustMemberships(t, ctx, f.source(admin), nil), "no names, no memberships")
}

func mustMemberships(t *testing.T, ctx context.Context, conn secrets.Value, names []string) []membership {
	t.Helper()
	out, err := requiredMemberships(ctx, conn, names)
	require.NoError(t, err)
	return out
}

func describeRoleForTest(t *testing.T, ctx context.Context, admin, role string) string {
	t.Helper()
	out, err := queryOneForTest(ctx, secrets.New(admin), fmt.Sprintf(`
		SELECT format('login=%%s super=%%s createrole=%%s createdb=%%s replication=%%s bypassrls=%%s inherit=%%s members=%%s password=%%s',
			r.rolcanlogin, r.rolsuper, r.rolcreaterole, r.rolcreatedb, r.rolreplication, r.rolbypassrls, r.rolinherit,
			(SELECT count(*) FROM pg_auth_members m WHERE m.member = r.oid),
			(SELECT rolpassword IS NOT NULL FROM pg_authid a WHERE a.oid = r.oid))
		FROM pg_roles r WHERE r.rolname = %s`, quoteLiteral(role)))
	require.NoError(t, err, "the role should exist in the target")
	return out
}

// The whole thing, between two clusters, which is the only arrangement in
// which the target can lack a role the source has.
//
// It is the failure verbatim: a source with a role named only by a GRANT and
// by no policy, copied, and then a statement in the target that grants to the
// role, which is what migration 0037 did to every branch of this repository's
// own twin and what every branch answered with "does not exist".
func TestACopyCarriesARoleNamedOnlyByAGrant(t *testing.T) {
	admin := adminForRolesTest(t)
	targetAdmin, named := os.LookupEnv("AF_TEST_TARGET_CLUSTER_URL")
	if !named {
		t.Skip("skipped: AF_TEST_TARGET_CLUSTER_URL is not set, and a copy between two databases on ONE cluster cannot lack a role; " +
			"start a second Postgres and name it to run the two cluster proof")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	require.NoError(t, Ping(ctx, secrets.New(targetAdmin)),
		"AF_TEST_TARGET_CLUSTER_URL names a Postgres that cannot be reached")
	f := newRolesTestFixture(t, ctx, admin)

	targetDB := fmt.Sprintf("af_roles_dst_%d", f.stamp)
	require.NoError(t, Exec(ctx, secrets.New(targetAdmin), "CREATE DATABASE "+targetDB))
	t.Cleanup(func() {
		c := context.WithoutCancel(ctx)
		_ = Exec(c, secrets.New(targetAdmin), "DROP DATABASE IF EXISTS "+targetDB+" WITH (FORCE)")
		for _, r := range f.roles() {
			_ = Exec(c, secrets.New(targetAdmin), "DROP ROLE IF EXISTS "+r)
		}
	})
	target := secrets.New(replaceDatabaseForTest(targetAdmin, targetDB))

	// The premise: the target cluster has never heard of any of these roles.
	for _, r := range f.roles() {
		_, err := queryOneForTest(ctx, target, "SELECT rolname FROM pg_roles WHERE rolname = "+quoteLiteral(r))
		require.Error(t, err, "the target cluster already has %s, so this test proves nothing; use a fresh cluster", r)
	}

	require.NoError(t, CopyWith(ctx, f.source(admin), target, CopyOptions{ExcludeSchemas: []string{"platform"}}))

	// The role arrived, as a name and nothing else.
	require.Equal(t,
		"login=f super=f createrole=f createdb=f replication=f bypassrls=f inherit=t members=0 password=f",
		describeRoleForTest(t, ctx, replaceDatabaseForTest(targetAdmin, targetDB), f.byGrant))
	require.Equal(t,
		"login=f super=f createrole=f createdb=f replication=f bypassrls=f inherit=t members=0 password=f",
		describeRoleForTest(t, ctx, replaceDatabaseForTest(targetAdmin, targetDB), f.bypasser),
		"BYPASSRLS in the source is not BYPASSRLS in the copy")
	for _, r := range []string{f.byDefault, f.byFunction, f.bySchema, f.byPolicy} {
		_, err := queryOneForTest(ctx, target, "SELECT rolname FROM pg_roles WHERE rolname = "+quoteLiteral(r))
		require.NoError(t, err, "%s should have arrived", r)
	}

	// But its grants did not. The copy drops privileges, and a shell role
	// holding production's grants would be more of production than the copy
	// needs.
	acl, err := queryOneForTest(ctx, target, "SELECT coalesce(relacl::text, '') FROM pg_class WHERE oid = 'public.usage'::regclass")
	require.NoError(t, err)
	require.NotContains(t, acl, f.byGrant, "the grant itself must not travel, only the name it needs")

	// The owner and the excluded schema's role stayed behind.
	for _, r := range []string{f.owner, f.excluded} {
		_, err := queryOneForTest(ctx, target, "SELECT rolname FROM pg_roles WHERE rolname = "+quoteLiteral(r))
		require.Error(t, err, "%s should not have been made in the target", r)
	}

	// The membership between two carried roles arrived with its options, and
	// the one in a role the copy left behind did not.
	targetAdminDB := replaceDatabaseForTest(targetAdmin, targetDB)
	require.Equal(t, "member=t usage=f", describeMembershipForTest(t, ctx, targetAdminDB, f.byPolicy, f.byGrant),
		"0041's check, in the branch: may SET ROLE to it, does not inherit from it")
	members, err := queryOneForTest(ctx, target, fmt.Sprintf(
		"SELECT count(*)::text FROM pg_auth_members am JOIN pg_roles u ON u.oid = am.member WHERE u.rolname = %s",
		quoteLiteral(f.byGrant)))
	require.NoError(t, err)
	require.Equal(t, "0", members, "the carried role's membership in the owner, which was not carried, must not travel")

	// And the statement that failed in every branch now succeeds. This is the
	// line from migration 0037, with this fixture's names in it.
	require.NoError(t, Exec(ctx, target, fmt.Sprintf(
		"GRANT SELECT ON public.usage TO %s, %s", f.byGrant, f.bypasser)),
		"a migration that grants to a role the golden knew only through a grant has to succeed in the branch")
}

// queryOneForTest runs one statement and returns the first column of its first
// row as text, or an error when there is no row.
func queryOneForTest(ctx context.Context, conn secrets.Value, statement string) (string, error) {
	db, err := sql.Open("pgx", conn.Reveal())
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	var out string
	if err := db.QueryRowContext(ctx, statement).Scan(&out); err != nil {
		return "", fmt.Errorf("%s: %w", strings.SplitN(strings.TrimSpace(statement), "\n", 2)[0], err)
	}
	return out, nil
}
