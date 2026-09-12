package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The rung that named a host and never asked what it was.
//
// af start's database rung reported the host server and called it done whenever
// the variable held a connection string. Point that variable at a Heroku
// Postgres or a Tiger Cloud service, both of which document that a second
// database cannot be created there, and the rung said the setup was fine.
//
// What it does NOT claim to save is the dump. pgurl.New probes the role at
// construction and refuses before RefreshGolden reads anything, so af up was
// already safe. What this rung adds is an answer WITHOUT CONNECTING, from the
// one command whose whole job is to say where you are, to somebody who has not
// finished configuring yet and is exactly who reads it.
//
// The second thing these tests found is where the line comes from. A manifest
// that names a source, which is nearly all of them, gets the SOURCE rung's
// sentence with the host server appended, so a vendor clause written only in
// pgurlState's own branch is a clause nobody with a source ever sees. It was
// written there first and only there, and the DigitalOcean test below is what
// caught it.

func TestStart_PgURLBlocksOnAVendorThatDocumentsItCannotHostTheGoldens(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, pgurlManifest)
	write(t, dir, ".env",
		"AF_START_TEST_SOURCE_URL=postgres://reader:secret@db.internal:5432/app\n"+
			"PGURL_ADMIN_URL=postgres://tsdbadmin:hunter2@service.project.tsdb.cloud.timescale.com:30477/tsdb\n")
	e, _ := startEnv(t, dir)

	s := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "the database source")
	require.Equal(t, StageBlocked, s.state,
		"the host is a Tiger Cloud service, whose own troubleshooting page says a "+
			"service holds one database, and the rung called it done")
	require.Contains(t, s.detail, "Tiger Cloud",
		"the reader's first question is which of their two servers this is about")
	require.Contains(t, s.prose, "source_url_env",
		"and the remedy is the one that works rather than a refusal with no way out: "+
			"the vendor stays as the source, which needs read access only")
	require.Contains(t, s.prose, "tigerdata.com",
		"with the page it was read from, because a verdict about somebody else's "+
			"product goes stale and the reader has to be able to check it")
	require.NotContains(t, s.detail, "hunter2", "the rung printed the password")
}

func TestStart_PgURLNamesAVendorItCanHostOnWithoutBlocking(t *testing.T) {
	// The other side, and it is the side that stops the registry making things
	// worse. DigitalOcean documents that a cluster holds many databases and
	// that doadmin may add them, so pointing the variable there is a setup that
	// works, and a rung that blocked on "this is a managed vendor" would refuse
	// it.
	dir := t.TempDir()
	writeManifest(t, dir, pgurlManifest)
	write(t, dir, ".env",
		"AF_START_TEST_SOURCE_URL=postgres://reader:secret@db.internal:5432/app\n"+
			"PGURL_ADMIN_URL=postgres://doadmin:hunter2@spare-do-user-0.db.ondigitalocean.com:25060/defaultdb\n")
	e, _ := startEnv(t, dir)

	s := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "the database source")
	require.Equal(t, StageDone, s.state,
		"DigitalOcean documents that doadmin may create databases, so this is a setup "+
			"that works and the rung must not stand in front of it")
	require.Contains(t, s.detail, "which is DigitalOcean Managed Databases for PostgreSQL",
		"it is still worth naming, because a reader who mistyped one host for another "+
			"finds out here. This is the APPENDED half of the line: a manifest with a "+
			"source configured, which is nearly all of them, gets the source rung's "+
			"sentence with the host server added to it, so a vendor clause written only "+
			"in the other branch is one nobody ever reads.")
	require.Empty(t, s.command, "a done rung names no command to fix it")
}

func TestStart_PgURLOnAnUnrecognisedServerReadsExactlyAsItDidBefore(t *testing.T) {
	// Most Postgres is nobody's product. A change that added a vendor clause to
	// this line had to leave the line alone when there is no vendor, and the
	// cheapest way to be sure is to assert the absence rather than to read the
	// diff.
	dir := t.TempDir()
	writeManifest(t, dir, pgurlManifest)
	write(t, dir, ".env",
		"AF_START_TEST_SOURCE_URL=postgres://reader:secret@db.internal:5432/app\n"+
			"PGURL_ADMIN_URL=postgres://af:hunter2@spare.internal:6543/postgres\n")
	e, _ := startEnv(t, dir)

	s := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "the database source")
	require.Equal(t, StageDone, s.state)
	require.Equal(t,
		"pgurl, copying the database named by AF_START_TEST_SOURCE_URL, found in .env, "+
			"into spare.internal:6543", s.detail,
		"an unrecognised host gets the line it always got, with no 'which is' clause "+
			"and no vendor guessed from a hostname nobody published")
}
