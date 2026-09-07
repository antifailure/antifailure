package env

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// af volume record, end to end, against a real Postgres.
//
// Everything either side of the collector is already covered without a server:
// the refusals are in internal/cli, the arithmetic is in internal/volume, and
// the catalog queries are in internal/volume's own Postgres suite. What is NOT
// covered by any of those is the glue, which is where the two mistakes that
// matter live. It has to read the variable database.source_url_env names and
// not some other one, and it must never put the value of that variable into
// the artifact, because the artifact is committed.
//
// A fake would agree with whatever the glue did.

// productionScratch makes a database on the scratch server and returns its URL.
func productionScratch(t *testing.T, name string, setup string) string {
	t.Helper()
	url := fidelityTestDatabaseURL
	if u := os.Getenv("AF_TEST_FIDELITY_DATABASE_URL"); u != "" {
		url = u
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no usable Postgres: %v", err)
		}
		t.Skipf("no Postgres at %s: %v", url, err)
	}
	_, err = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	require.NoError(t, err)
	_, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)
	require.NoError(t, admin.Close(ctx))

	own := strings.Replace(url, "/antifailure", "/"+name, 1)
	conn, err := pgx.Connect(ctx, own)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, setup)
	require.NoError(t, err)
	require.NoError(t, conn.Close(ctx))

	t.Cleanup(func() {
		c := context.WithoutCancel(context.Background())
		if a, err := pgx.Connect(c, url); err == nil {
			_, _ = a.Exec(c, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			_ = a.Close(c)
		}
	})
	return own
}

func TestRecordVolume_ReadsTheDatabaseTheManifestNamesAndKeepsItsURLOut(t *testing.T) {
	dsn := productionScratch(t, "af_volume_record", `
CREATE TABLE users (id int primary key, email text);
CREATE TABLE orders (id int primary key, user_id int references users (id));
INSERT INTO users SELECT g, 'a' || g || '@example.test' FROM generate_series(1, 700) g;
INSERT INTO orders SELECT g, 1 + (g % 700) FROM generate_series(1, 300) g;
ANALYZE;`)

	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app", Database: &schema.Database{SourceURLEnv: "PRODUCTION_DATABASE_URL"}},
		Branch:   "main",
		Clock:    clock.New(),
		Redactor: redact.New(),
		Getenv: func(k string) string {
			if k == "PRODUCTION_DATABASE_URL" {
				return dsn
			}
			return ""
		},
	})
	require.NoError(t, err)

	p, err := o.RecordVolume(t.Context())
	require.NoError(t, err)

	require.False(t, p.CollectedAt.IsZero(), "a profile with no date on it is refused when it is read")
	require.Contains(t, p.ServerVersion, "PostgreSQL")

	users, ok := p.Find("public.users")
	require.True(t, ok, "the profile does not name a table this database has: %+v", p.Tables)
	require.EqualValues(t, 700, users.Rows)
	orders, ok := p.Find("public.orders")
	require.True(t, ok)
	require.EqualValues(t, 300, orders.Rows)
	require.NotEmpty(t, orders.Keys, "the foreign key is not in the profile")

	// The one thing a committed artifact must never carry. The Source names
	// the VARIABLE, and nothing anywhere in the profile is the value of it.
	require.Equal(t, "the database named by PRODUCTION_DATABASE_URL", p.Source)
	require.NotContains(t, p.Source, "://")
	body, err := json.Marshal(p)
	require.NoError(t, err)
	require.NotContains(t, string(body), dsn, "the connection string reached the artifact")
	require.NotContains(t, string(body), "postgres://", "a connection string reached the artifact")
}

// A variable that names a database nothing can reach is an error naming the
// address, not a profile of nothing. A profile of nothing is a denominator of
// zero.
func TestRecordVolume_ANamedSourceThatCannotBeReachedIsAnError(t *testing.T) {
	o, err := New(Options{
		Root:     t.TempDir(),
		Manifest: &schema.Manifest{Name: "app", Database: &schema.Database{SourceURLEnv: "PRODUCTION_DATABASE_URL"}},
		Branch:   "main",
		Clock:    clock.New(),
		Redactor: redact.New(),
		Getenv: func(k string) string {
			if k == "PRODUCTION_DATABASE_URL" {
				return "postgres://someone:secret@127.0.0.1:1/prod"
			}
			return ""
		},
	})
	require.NoError(t, err)

	p, err := o.RecordVolume(t.Context())
	require.Error(t, err, "an unreachable source produced a profile")
	require.Empty(t, p.Tables)
	require.Contains(t, err.Error(), "127.0.0.1:1", "the error does not say where it tried to reach")
	require.NotContains(t, err.Error(), "secret", "the password reached the error message")
}
