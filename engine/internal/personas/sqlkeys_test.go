package personas_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/antifailure/antifailure/engine/internal/personas"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// recordingConn is a real connection that remembers every statement sent
// through it, with its arguments, so a test can ask the database how it would
// have run each one.
type recordingConn struct {
	*pgx.Conn
	mu   sync.Mutex
	sent []sentStatement
}

type sentStatement struct {
	SQL  string
	Args []any
}

func (r *recordingConn) note(sql string, args []any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, sentStatement{SQL: sql, Args: append([]any(nil), args...)})
}

func (r *recordingConn) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	r.note(sql, args)
	return r.Conn.Exec(ctx, sql, args...)
}

func (r *recordingConn) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	r.note(sql, args)
	return r.Conn.QueryRow(ctx, sql, args...)
}

func (r *recordingConn) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	r.note(sql, args)
	return r.Conn.Query(ctx, sql, args...)
}

// scan is one plan node that reads a relation.
type scan struct {
	Node string
	// Cond is the condition the index was searched with. A Bitmap Heap Scan's
	// is on the Bitmap Index Scan beneath it, and is taken from there.
	Cond string
}

// planScans walks an EXPLAIN (FORMAT JSON) plan and returns, for each relation
// by its unqualified name, the nodes that read it. The ModifyTable node an
// UPDATE puts on top names the relation too, and is not a read, so it is left
// out.
func planScans(t *testing.T, raw string) map[string][]scan {
	t.Helper()
	var doc []struct {
		Plan map[string]any `json:"Plan"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &doc))
	require.Len(t, doc, 1)

	children := func(node map[string]any) []map[string]any {
		var out []map[string]any
		list, _ := node["Plans"].([]any)
		for _, c := range list {
			if child, ok := c.(map[string]any); ok {
				out = append(out, child)
			}
		}
		return out
	}

	out := map[string][]scan{}
	var walk func(node map[string]any)
	walk = func(node map[string]any) {
		if rel, ok := node["Relation Name"].(string); ok && node["Node Type"] != "ModifyTable" {
			s := scan{}
			s.Node, _ = node["Node Type"].(string)
			s.Cond, _ = node["Index Cond"].(string)
			if s.Node == "Bitmap Heap Scan" {
				for _, child := range children(node) {
					if cond, ok := child["Index Cond"].(string); ok {
						s.Cond = cond
					}
				}
			}
			out[rel] = append(out[rel], s)
		}
		for _, child := range children(node) {
			walk(child)
		}
	}
	walk(doc[0].Plan)
	return out
}

// TestEveryKeyedStatementCanBeAnsweredFromAnIndex asks Postgres how it would
// run each statement the adapter addresses a row with, exactly as the adapter
// sent it.
//
// The failure it exists for: every one of these compared the key cast to text,
// which is an expression no index can be searched by, so each one read the
// whole table or, for the identity lookup, the whole of an index. Against a
// million Supabase users the medians were 185 ms for the users UPDATE, 89 to
// 126 ms for each identity lookup and 38 to 52 ms for each factor lookup, per
// persona and per branch, where the same statements searching the index take
// under 5 ms. Nothing failed; provisioning was only slow, in proportion to the
// customer's user count, which is the kind of defect no functional test sees.
//
// So the requirement is that the index is SEARCHED by the key, an index
// condition naming the key column, and not merely that the plan avoids a
// sequential scan. The identity lookup on main already used an index and read
// all of it, applying the cast as a filter to every entry.
//
// Sequential scans are switched off for the EXPLAIN, which makes the answer
// independent of table size. A planner with a usable index condition then
// always takes it, and a planner without one still has to read everything, so
// a small test table reports the same plan a large golden gets.
//
// The address lookup in find is not held to this. It matches lower(email),
// and Supabase's only index on that expression leads with instance_id, which
// the lookup does not constrain. Matching without case is deliberate, so that
// stays a sequential scan and is said here rather than silently skipped.
func TestEveryKeyedStatementCanBeAnsweredFromAnIndex(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	freshSupabase(t, conn)
	ctx := context.Background()

	rec := &recordingConn{Conn: conn}
	d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
	a := personas.NewSQLAdapter(rec, personas.SchemeSupabase, "Antifailure")

	p := owner()
	p.MFA = true

	// The first run inserts, so its identity and factor lookups find nothing.
	// The second reconciles, which is the only run that sends the three
	// UPDATEs. Both orders are recorded because both are what a branch does.
	_, err := personas.Provision(ctx, a, d, []schema.Persona{p})
	require.NoError(t, err)
	second, err := personas.Provision(ctx, a, d, []schema.Persona{p})
	require.NoError(t, err)
	require.True(t, second.Accounts[0].Reconciled, "the second run did not reconcile, so no UPDATE was sent")

	cases := []struct {
		name     string
		prefix   string
		relation string
		key      string
		// want is how many of these statements the two runs send. Counted so
		// that a statement renamed out from under the prefix fails here rather
		// than leaving a subtest that checked nothing.
		want int
	}{
		{"the users UPDATE", `UPDATE "auth"."users" `, "users", "id", 1},
		{"the identity lookup", `SELECT "user_id"::text FROM "auth"."identities" `, "identities", "user_id", 2},
		{"the identity UPDATE", `UPDATE "auth"."identities" `, "identities", "user_id", 1},
		{"the factor lookup", `SELECT 1 FROM "auth"."mfa_factors" `, "mfa_factors", "user_id", 2},
		{"the factor UPDATE", `UPDATE "auth"."mfa_factors" `, "mfa_factors", "user_id", 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var matched []sentStatement
			for _, s := range rec.sent {
				if strings.HasPrefix(s.SQL, c.prefix) {
					matched = append(matched, s)
				}
			}
			require.Len(t, matched, c.want, "statements starting %q", c.prefix)

			for _, s := range matched {
				tx, err := conn.Begin(ctx)
				require.NoError(t, err)
				_, err = tx.Exec(ctx, "SET LOCAL enable_seqscan = off")
				require.NoError(t, err)

				var raw string
				err = tx.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+s.SQL, s.Args...).Scan(&raw)
				_ = tx.Rollback(ctx)
				require.NoError(t, err, "explaining %s", s.SQL)

				scans := planScans(t, raw)
				require.NotEmpty(t, scans[c.relation], "the plan for %s never reads %s:\n%s", s.SQL, c.relation, raw)
				for _, node := range scans[c.relation] {
					require.Contains(t, node.Cond, "("+c.key+" = ",
						"%s reads all of %s (%s) instead of searching an index by %s:\n%s",
						s.SQL, c.relation, node.Node, c.key, raw)
				}
			}
		})
	}
}

// TestReconcilingFindsTheRowWhateverTheKeyType provisions onto a row that
// already holds the persona's address, for every kind of key a users table
// uses, and checks that the one row with that key changed and no other did.
//
// The keys are compared as their own type rather than as text, which is what
// lets an index answer. That is only safe if one string still matches exactly
// the row it names for every key type, so this is the guard on the change:
// an integer key must not match its neighbours, a text key must stay exact
// about case and spacing, and a padded character key must match the value its
// own text output produced.
//
// Run over both of pgx's ways of sending a parameter. The environment connects
// with the default, extended protocol, where the parameter takes its type from
// the column; the simple protocol sends it as a quoted literal instead, and a
// connection string can choose that.
func TestReconcilingFindsTheRowWhateverTheKeyType(t *testing.T) {
	conn, done := requireDatabase(t)
	defer done()
	ctx := context.Background()

	simpleConfig := conn.Config().Copy()
	simpleConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	simple, err := pgx.ConnectConfig(ctx, simpleConfig)
	require.NoError(t, err)
	defer func() { _ = simple.Close(context.Background()) }()

	keys := []struct {
		name string
		// column is the key's declaration, and table any extra constraint.
		column string
		table  string
		// target is the pre-existing row's key, and decoys are keys that a
		// wrong comparison could confuse with it.
		target string
		decoys []string
	}{
		{name: "bigint", column: "id bigint PRIMARY KEY", target: "7", decoys: []string{"70", "17", "77"}},
		{name: "integer", column: "id integer PRIMARY KEY", target: "7", decoys: []string{"70", "17", "77"}},
		{name: "uuid", column: "id uuid PRIMARY KEY",
			target: "7a3c1d4e-0b1f-4e7a-9c2d-5f6e7a8b9c0d",
			decoys: []string{"7a3c1d4e-0b1f-4e7a-9c2d-5f6e7a8b9c0e", "00000000-0000-0000-0000-000000000007"}},
		{name: "text", column: "id text PRIMARY KEY", target: "user-7", decoys: []string{"USER-7", "user-7 ", "user-70"}},
		{name: "varchar", column: "id varchar(64) PRIMARY KEY", target: "user-7", decoys: []string{"USER-7", "user-70"}},
		// char pads, and its text output drops the padding. The id handed back
		// is that output, so it has to match the padded value it came from.
		{name: "char", column: "id char(8) PRIMARY KEY", target: "user-7", decoys: []string{"user-70", "USER-7"}},
		// A composite primary key. The adapter addresses a row by one column,
		// so the scheme names the column that is unique on its own, and the
		// other half of the key must not widen or narrow the match.
		{name: "composite", column: "id bigint NOT NULL UNIQUE, tenant integer NOT NULL DEFAULT 1",
			table: ", PRIMARY KEY (tenant, id)", target: "7", decoys: []string{"70", "17"}},
	}

	modes := []struct {
		name string
		conn *pgx.Conn
	}{
		{"extended protocol", conn},
		{"simple protocol", simple},
	}

	for _, mode := range modes {
		for _, k := range keys {
			t.Run(mode.name+"/"+k.name, func(t *testing.T) {
				table := "keyed_users_" + k.name
				_, err := conn.Exec(ctx, fmt.Sprintf(`
					DROP TABLE IF EXISTS %[1]s;
					CREATE TABLE %[1]s (
					  %[2]s,
					  email      text NOT NULL UNIQUE,
					  password   text,
					  role       text,
					  updated_at timestamptz
					  %[3]s
					)`, table, k.column, k.table))
				require.NoError(t, err)
				t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })

				// The masked real user holding the persona's address, and the
				// neighbours whose keys look like it.
				_, err = conn.Exec(ctx, fmt.Sprintf(
					`INSERT INTO %s (id, email, role) VALUES ($1, 'owner@example.test', 'masked')`, table), k.target)
				require.NoError(t, err)
				for i, decoy := range k.decoys {
					_, err = conn.Exec(ctx, fmt.Sprintf(
						`INSERT INTO %s (id, email, role) VALUES ($1, $2, 'decoy')`, table),
						decoy, fmt.Sprintf("decoy%d@example.test", i))
					require.NoError(t, err)
				}

				var subject string
				require.NoError(t, conn.QueryRow(ctx, fmt.Sprintf(
					`SELECT id::text FROM %s WHERE email = 'owner@example.test'`, table)).Scan(&subject))

				scheme := personas.GenericScheme(personas.Table{
					Name: table, ID: "id", Email: "email",
					Password: "password", Role: "role",
					Timestamps: []string{"updated_at"},
				}, nil)
				d := personas.NewDeriver("env-abc", personas.PasswordPolicy{})
				a := personas.NewSQLAdapter(mode.conn, scheme, "Antifailure")

				got, err := personas.Provision(ctx, a, d, []schema.Persona{owner()})
				require.NoError(t, err)
				require.True(t, got.Accounts[0].Reconciled)
				require.Equal(t, subject, got.Accounts[0].Subject)

				// The row the id names is the row that changed: the role is the
				// persona's and the hash verifies against the password the runner
				// is told. An UPDATE matching nothing is not an error, so this is
				// the only thing that notices one.
				var role, hash string
				require.NoError(t, conn.QueryRow(ctx, fmt.Sprintf(
					`SELECT role, coalesce(password, '') FROM %s WHERE email = 'owner@example.test'`,
					table)).Scan(&role, &hash))
				require.Equal(t, "authenticated", role, "the row holding the address was not the row updated")
				require.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash),
					[]byte(got.Accounts[0].Password.Reveal())))

				// And no neighbour was touched.
				var touched []string
				rows, err := conn.Query(ctx, fmt.Sprintf(
					`SELECT id::text FROM %s WHERE email <> 'owner@example.test'
					 AND (role <> 'decoy' OR password IS NOT NULL OR updated_at IS NOT NULL)`, table))
				require.NoError(t, err)
				for rows.Next() {
					var id string
					require.NoError(t, rows.Scan(&id))
					touched = append(touched, id)
				}
				require.NoError(t, rows.Err())
				require.Empty(t, touched, "reconciling %s also rewrote these rows", subject)
			})
		}
	}
}
