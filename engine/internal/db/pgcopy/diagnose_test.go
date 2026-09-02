package pgcopy

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// Every transcript below was copied out of a real run against a Postgres 17
// holding what a production schema holds: several schemas, an extension the
// target image does not carry, row level security, and a read only role of the
// kind the generated manifest tells you to use. They are the exact bytes the
// subprocess wrote, wrapping and all, because a classifier tested against a
// paraphrase is a classifier tested against nothing.

const (
	extensionTranscript = `pg_restore: error: could not execute query: ERROR:  extension "postgis" is not available
DETAIL:  Could not open extension control file "/usr/local/share/postgresql/extension/postgis.control": No such file or directory.
HINT:  The extension must first be installed on the system where PostgreSQL is running.
Command was: CREATE EXTENSION IF NOT EXISTS "postgis" WITH SCHEMA "public";`

	sequencePermissionTranscript = `pg_dump: error: query failed: ERROR:  permission denied for sequence UserProfile_Id_seq
pg_dump: detail: Query was: SELECT last_value, is_called FROM "Mixed Case Schema"."UserProfile_Id_seq"`

	tablePermissionTranscript = `pg_dump: error: query failed: ERROR:  permission denied for table customers
pg_dump: detail: Query was: LOCK TABLE "public"."customers" IN ACCESS SHARE MODE`

	rowSecurityTranscript = `pg_dump: error: query failed: ERROR:  query would be affected by row-level security policy for table "customers"
pg_dump: detail: Query was: COPY "public"."customers" ("id", "email", "full_name") TO stdout;`

	// Postgres prints an object name unquoted, so a schema whose name has
	// spaces in it ends the line with three words and nothing marks where the
	// name began. Taking the first word alone names a schema that does not
	// exist, in a remedy whose whole value is the schema it names.
	schemaPermissionTranscript = `pg_dump: error: query failed: ERROR:  permission denied for schema Mixed Case Schema
pg_dump: detail: Query was: LOCK TABLE "public"."customers", "Mixed Case Schema"."UserProfile" IN ACCESS SHARE MODE`

	unknownTranscript = `pg_dump: error: connection to server at "db.example.test" failed: something nobody has seen`
)

func TestTranscriptError_ClassifiesWhatARealDatabaseSaid(t *testing.T) {
	tests := []struct {
		name       string
		program    string
		transcript string
		want       aferrors.Code
		says       string
	}{
		{
			name:       "an extension the golden's Postgres does not carry",
			program:    "pg_restore",
			transcript: extensionTranscript,
			want:       aferrors.AFDB007,
			says:       "postgis",
		},
		{
			name:       "a read only role that cannot read a sequence",
			program:    "pg_dump",
			transcript: sequencePermissionTranscript,
			want:       aferrors.AFDB017,
			says:       "sequence UserProfile_Id_seq",
		},
		{
			name:       "a read only role that cannot read a table",
			program:    "pg_dump",
			transcript: tablePermissionTranscript,
			want:       aferrors.AFDB017,
			says:       "table customers",
		},
		{
			name:       "a schema whose name has spaces in it",
			program:    "pg_dump",
			transcript: schemaPermissionTranscript,
			want:       aferrors.AFDB017,
			says:       "schema Mixed Case Schema",
		},
		{
			// The more specific of the two has to win. A policy refusal is a
			// permission problem in the ordinary sense, and BYPASSRLS is not
			// what a GRANT gives you.
			name:       "row level security refusing the dump",
			program:    "pg_dump",
			transcript: rowSecurityTranscript,
			want:       aferrors.AFDB018,
			says:       "customers",
		},
		{
			name:       "anything else keeps its transcript",
			program:    "pg_dump",
			transcript: unknownTranscript,
			want:       aferrors.AFDB019,
			says:       "something nobody has seen",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := transcriptError(t.Context(), secrets.Value{}, tc.program, tc.transcript)
			require.Error(t, err)

			var coded *aferrors.Error
			require.True(t, aferrors.As(err, &coded),
				"an uncoded error carries no next step, which is the whole defect")
			require.Equal(t, tc.want, coded.Code())
			require.Contains(t, coded.Message()+" "+coded.NextStep(), tc.says,
				"the reader has to be told which object stopped the copy")
			require.NotEmpty(t, coded.NextStep(), "a message with no next step is a churn event")

			// The transcript stays reachable under -v on every path, including
			// the ones where a code has replaced the headline.
			require.NotNil(t, aferrors.Unwrap(coded), "the transcript was thrown away")
		})
	}
}

// The remedy AF-DB-017 prints has to be the one that resolves it. A read only
// role missing SELECT on sequences is the case that produced the transcript, so
// the sentence has to name sequences and not tables alone.
func TestTranscriptError_ThePermissionRemedyNamesSequencesAndEverySchema(t *testing.T) {
	err := transcriptError(t.Context(), secrets.Value{}, "pg_dump", sequencePermissionTranscript)
	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))

	next := coded.NextStep()
	require.Contains(t, next, "ALL SEQUENCES",
		"granting SELECT on tables alone leaves the dump failing on the first sequence")
	require.Contains(t, next, "USAGE ON SCHEMA",
		"a role with SELECT and no USAGE is refused at the schema, which is the "+
			"same code with a different remedy, so the remedy has to carry both")
	require.Contains(t, strings.ToLower(next), "not only public",
		"a real database has schemas beyond public and a per schema grant misses them")
}

func TestConnectError_SaysWhichOfTheThreeItIs(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want aferrors.Code
	}{
		{
			// Four lines for two addresses and two protocols, which is what a
			// mistyped port actually looks like.
			name: "a host that is not listening",
			err: errors.New(`failed to connect to ` + "`user=postgres database=shopdb`" + `:
	[::1]:59999 (localhost): dial error: dial tcp [::1]:59999: connect: connection refused
	127.0.0.1:59999 (localhost): dial error: dial tcp 127.0.0.1:59999: connect: connection refused`),
			want: aferrors.AFDB002,
		},
		{
			name: "a server that answered and refused the password",
			err: errors.New(`failed to connect to ` + "`user=postgres database=shopdb`" + `:
	[::1]:55990 (localhost): tls error: server refused TLS connection
	127.0.0.1:55990 (localhost): failed SASL auth: FATAL: password authentication failed for user "postgres" (SQLSTATE 28P01)`),
			want: aferrors.AFDB023,
		},
		{
			name: "a database name that is not on the server",
			err:  errors.New(`failed to connect: FATAL: database "shopdb_typo" does not exist (SQLSTATE 3D000)`),
			want: aferrors.AFDB023,
		},
		{
			name: "a value that is not a connection string at all",
			err:  errors.New("cannot parse `localhost:55990/shopdb`: failed to parse as keyword/value (invalid keyword/value)"),
			want: aferrors.AFDB024,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := connectError(tc.err, "the address in database.source_url_env")
			var coded *aferrors.Error
			require.True(t, aferrors.As(err, &coded))
			require.Equal(t, tc.want, coded.Code())
			require.NotEmpty(t, coded.NextStep())
		})
	}
}

func TestConnectError_NothingWrongStaysNothingWrong(t *testing.T) {
	require.NoError(t, connectError(nil, "the address in database.source_url_env"))
}
