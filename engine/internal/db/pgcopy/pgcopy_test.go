package pgcopy

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// realSupabaseTOC is `pg_restore --list` of an actual dump taken from a
// Supabase branch, trimmed to the entries that matter.
//
// Captured rather than invented, because the whole point of the filter is to
// recognise what a real archive looks like, and a listing written from memory
// would agree with the filter by construction.
const realSupabaseTOC = `;
; Archive created at 2026-08-27 01:02:03 UTC
;     dbname: postgres
;
;
; Selected TOC Entries:
;
6; 3079 17486 EXTENSION - pg_net
3768; 0 0 COMMENT - EXTENSION "pg_net"
3769; 0 0 COMMENT - SCHEMA "public" pg_database_owner
287; 1259 17582 TABLE public conformance_events postgres
286; 1259 17566 TABLE public conformance_users postgres
3759; 0 17582 TABLE DATA public conformance_events postgres
3775; 0 0 SEQUENCE SET public conformance_users_id_seq postgres
3608; 2606 17590 FK CONSTRAINT public conformance_events conformance_events_user_id_fkey postgres
3591; 3466 16575 EVENT TRIGGER - issue_graphql_placeholder supabase_admin
3592; 3466 16576 EVENT TRIGGER - pgrst_ddl_watch supabase_admin
3593; 3466 16577 EVENT TRIGGER - pgrst_drop_watch supabase_admin
`

func TestTheFilterRemovesOnlyTheNamedKind(t *testing.T) {
	got := filterTOC(realSupabaseTOC, []string{"EVENT TRIGGER"})

	require.NotContains(t, got, "EVENT TRIGGER")
	// Everything else survives, and this is the half worth asserting: a filter
	// that dropped a TABLE DATA entry would produce a restore that succeeds and
	// a database that is missing rows, which no exit code would report.
	for _, kept := range []string{
		"TABLE public conformance_users",
		"TABLE DATA public conformance_events",
		"SEQUENCE SET public conformance_users_id_seq",
		"FK CONSTRAINT public conformance_events",
		"EXTENSION - pg_net",
		"COMMENT - SCHEMA \"public\"",
	} {
		require.Contains(t, got, kept)
	}
	// The comment header is left alone: pg_restore reads it as a comment and a
	// filter that ate it would be filtering by luck.
	require.Contains(t, got, "; Selected TOC Entries:")
}

func TestTheFilterMatchesTheDescriptionByPositionRatherThanAnywhere(t *testing.T) {
	// A table whose NAME contains the kind must survive. Dropping a table from
	// a restore because of what it is called would be data loss with no error
	// message, and searching the line for a substring is exactly how that
	// happens.
	listing := `290; 1259 17600 TABLE public event_trigger_audit postgres
291; 0 17600 TABLE DATA public event_trigger_audit postgres
3591; 3466 16575 EVENT TRIGGER - pgrst_ddl_watch supabase_admin
`
	got := filterTOC(listing, []string{"EVENT TRIGGER"})
	require.Contains(t, got, "TABLE public event_trigger_audit")
	require.Contains(t, got, "TABLE DATA public event_trigger_audit")
	require.NotContains(t, got, "pgrst_ddl_watch")
}

func TestNamingNoKindsChangesNothing(t *testing.T) {
	require.Equal(t, strings.TrimRight(realSupabaseTOC, "\n"),
		strings.TrimRight(filterTOC(realSupabaseTOC, nil), "\n"))
}

func TestAPlainCopyStillSendsExactlyTheFlagsItAlwaysDid(t *testing.T) {
	// The existing providers depend on this. A zero CopyOptions has to produce
	// the dump the Docker and Neon providers have been running all along, or
	// this additive change is not additive.
	require.Equal(t, []string{
		"--format=custom",
		"--no-owner", "--no-privileges", "--no-acl",
		"--quote-all-identifiers",
	}, dumpArgs(CopyOptions{}))
}

func TestASchemaOnlyCopySendsTheFlagThatLeavesTheRowsBehind(t *testing.T) {
	// CopySchema used to be its own dump with its own flag list. Merging the
	// two copy paths into one made it an option instead, and the whole point
	// of subsetting is that the rows do NOT come across: without this flag a
	// schema copy silently becomes a full copy of production, which is the
	// exact thing the feature exists to avoid.
	got := dumpArgs(CopyOptions{SchemaOnly: true})
	require.Contains(t, got, "--schema-only")
	// And the flags a plain copy sends are still all there.
	for _, flag := range []string{
		"--format=custom", "--no-owner", "--no-privileges", "--no-acl",
		"--quote-all-identifiers",
	} {
		require.Contains(t, got, flag)
	}
	// Not sent when it was not asked for.
	require.NotContains(t, dumpArgs(CopyOptions{}), "--schema-only")
}

func TestExcludingSchemasAlsoExcludesTheClusterWideObjectsThatComeWithThem(t *testing.T) {
	// A publication belongs to the cluster rather than to a schema, so
	// excluding a platform's schemas does not exclude the publication it
	// created, and the restore fails on one the target already has.
	got := dumpArgs(CopyOptions{ExcludeSchemas: []string{"auth", "storage"}})
	require.Contains(t, got, "--no-publications")
	require.Contains(t, got, "--no-subscriptions")
	require.Contains(t, got, "--exclude-schema=auth")
	require.Contains(t, got, "--exclude-schema=storage")
}

func TestARestoreAlwaysExitsOnError(t *testing.T) {
	// pg_restore reports a failed foreign key and carries on by default, which
	// lands the rows and silently omits the constraint. This flag is the only
	// thing standing between that and a published golden whose referential
	// integrity is not there.
	require.Contains(t, restoreArgs(), "--exit-on-error")
}

// The rest of this file needs a real Postgres, because what is being checked is
// what pg_dump and pg_restore do rather than what this package thinks they do.
// The test Postgres the contributing guide starts is the one it uses.

func testDatabaseURL(t *testing.T) secrets.Value {
	t.Helper()

	// Naming the server is a STATEMENT THAT ONE IS MEANT TO BE THERE, so an
	// unreachable one is a failure rather than a skip. The variable buys a
	// faster run, never a quieter one. Without it the default is a
	// convenience, and a machine that has not run `just db` skips by name.
	//
	// The asymmetry is lane 5's, arrived at from the other direction: their
	// suite was spending its time building containers it did not need, and the
	// same helper that lets you point at a standing server is the one that
	// silently reports success when you point it at nothing.
	raw, named := os.LookupEnv("AF_TEST_DATABASE_URL")
	if !named {
		raw = "postgres://postgres:test@127.0.0.1:55432/antifailure?sslmode=disable"
	}

	conn := secrets.New(raw)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Ping(ctx, conn); err != nil {
		if named {
			t.Fatalf("AF_TEST_DATABASE_URL names a Postgres that cannot be reached: %v", err)
		}
		t.Skip("skipped: no test Postgres (run `just db`, or set AF_TEST_DATABASE_URL)")
	}
	return conn
}

func TestCopyingPlatformTablesASourceDoesNotHaveIsNotAnError(t *testing.T) {
	// The bug this exists for: pg_dump --table with no match does not produce
	// an empty dump, it exits 1 with "no matching tables were found" and writes
	// a zero byte file. The restore then fails with "input file is too short"
	// and the real reason is two errors back.
	//
	// It matters because a golden's source is whatever the customer's
	// production database is. A Supabase provider whose refresh only works when
	// production is also Supabase would fail on the first run for anybody
	// moving onto the platform.
	conn := testDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	require.NoError(t, CopyTableData(ctx, conn, conn, []string{"auth.users", "auth.identities"}))
	require.NoError(t, CopyTableData(ctx, conn, conn, []string{"public.nothing_named_this"}))
}

func TestOnlyTheTablesThatExistAreAskedFor(t *testing.T) {
	conn := testDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	require.NoError(t, Exec(ctx, conn, `
		CREATE SCHEMA IF NOT EXISTS pgcopy_probe;
		CREATE TABLE IF NOT EXISTS pgcopy_probe.present (id int PRIMARY KEY);`))
	t.Cleanup(func() {
		_ = Exec(context.Background(), conn, `DROP SCHEMA IF EXISTS pgcopy_probe CASCADE`)
	})

	got, err := tablesThatExist(ctx, conn, []string{
		"pgcopy_probe.present", "pgcopy_probe.absent", "nosuchschema.whatever",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"pgcopy_probe.present"}, got)
}
