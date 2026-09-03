package pgcopy

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Which of the two remedies a refusal needs depends on what the role is
// missing, and a role can be missing both at once.
//
// A grant does not confer BYPASSRLS and BYPASSRLS is not a grant, so a reader
// told only one of them fixes half the problem and runs the copy again to be
// told the other half.
func TestPrivilegeError_ChoosesTheCodeByWhatIsActuallyMissing(t *testing.T) {
	cause := context.Canceled

	require.Nil(t, privilegeError(nil, cause), "nothing missing is nothing to say")

	only := []string{"row level security applies to public.customers"}
	err := privilegeError(only, cause)
	require.Equal(t, aferrors.AFDB018, codeOfTest(t, err),
		"a role that is only blocked by policies does not need a GRANT")

	mixed := []string{
		"no SELECT on sequence \"Mixed Case Schema\".\"UserProfile_Id_seq\"",
		"row level security applies to public.customers",
	}
	err = privilegeError(mixed, cause)
	require.Equal(t, aferrors.AFDB017, codeOfTest(t, err))

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Contains(t, coded.Message(), "UserProfile_Id_seq")
	require.Contains(t, coded.Message(), "public.customers",
		"both halves have to be in one message or the reader fixes one and runs again")
	require.Contains(t, coded.NextStep(), "BYPASSRLS",
		"the grant remedy has to carry the policy remedy, because this list holds both")
}

func codeOfTest(t *testing.T, err error) aferrors.Code {
	t.Helper()
	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded), "not a coded error: %v", err)
	return coded.Code()
}

// The query itself, against a real Postgres, because what it is for is being
// right about a real catalogue. A hand written fixture would agree with
// whatever the query happened to say.
//
// It plants one of each shape it is meant to find: a schema the role has no
// USAGE on, a sequence in a schema it can enter, and a table with row level
// security. The mixed case names are deliberate: an unquoted name in the
// output sends the reader to grant on a schema that does not exist.
const privilegeTestDatabaseURL = "postgres://postgres:test@127.0.0.1:55432/antifailure"

func TestPrivilegeProblems_FindsEveryShapeAgainstARealCatalogue(t *testing.T) {
	admin := privilegeTestDatabaseURL
	if u := os.Getenv("AF_TEST_SEED_DATABASE_URL"); u != "" {
		admin = u
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if err := Ping(ctx, secrets.New(admin)); err != nil {
		if os.Getenv("AF_REQUIRE_DATABASE") != "" {
			t.Fatalf("AF_REQUIRE_DATABASE is set and there is no usable Postgres: %v", err)
		}
		t.Skipf("no Postgres to read a catalogue from: %v", err)
	}

	stamp := time.Now().UnixNano()
	dbName := fmt.Sprintf("af_priv_%d", stamp)
	role := fmt.Sprintf("af_priv_reader_%d", stamp)

	require.NoError(t, Exec(ctx, secrets.New(admin), fmt.Sprintf(
		`CREATE ROLE %s LOGIN PASSWORD 'reader'`, role)))
	require.NoError(t, Exec(ctx, secrets.New(admin), "CREATE DATABASE "+dbName))
	t.Cleanup(func() {
		c := context.WithoutCancel(ctx)
		_ = Exec(c, secrets.New(admin), "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		_ = Exec(c, secrets.New(admin), "DROP ROLE IF EXISTS "+role)
	})

	owner := replaceDatabaseForTest(admin, dbName)
	require.NoError(t, Exec(ctx, secrets.New(owner), fmt.Sprintf(`
		CREATE SCHEMA "Locked Away";
		CREATE TABLE "Locked Away".hidden (id int);
		CREATE SEQUENCE public.ungranted_seq;
		CREATE TABLE public.guarded (id int, owner_email text);
		ALTER TABLE public.guarded ENABLE ROW LEVEL SECURITY;
		CREATE POLICY guarded_self ON public.guarded
		  USING (owner_email = current_setting('app.email', true));
		GRANT CONNECT ON DATABASE %s TO %s;
		GRANT USAGE ON SCHEMA public TO %s;
		GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s;
	`, dbName, role, role, role)))

	reader := strings.Replace(owner, "postgres:test@", role+":reader@", 1)
	problems := privilegeProblems(ctx, secrets.New(reader))
	joined := strings.Join(problems, "\n")

	require.Contains(t, joined, `no USAGE on schema "Locked Away"`,
		"a schema the role cannot enter has to be named, and quoted so the grant can be pasted")
	require.Contains(t, joined, "no SELECT on sequence public.ungranted_seq",
		"pg_dump reads every sequence, and this is the grant everybody forgets")
	require.Contains(t, joined, "row level security applies to public.guarded")

	// And it must not invent work. The table the role CAN read is not listed.
	require.NotContains(t, joined, "no SELECT on table public.guarded")

	// A superuser is missing nothing, which is what keeps this from reporting
	// on a setup that works.
	require.Empty(t, privilegeProblems(ctx, secrets.New(owner)),
		"a role that can read everything was told it could not")
}

// replaceDatabaseForTest swaps the database at the end of a connection string.
func replaceDatabaseForTest(url, name string) string {
	i := strings.LastIndex(url, "/")
	return url[:i+1] + name
}
