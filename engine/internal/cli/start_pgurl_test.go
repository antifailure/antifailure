package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The rung that would never have run.
//
// af start reports one line for the database, and for every provider whose
// manifest names a source that line came from the SOURCE check, which returns
// for all of them. A provider check placed after it therefore never ran on the
// configuration people actually write, which is a manifest with a source in it.
//
// For pgurl that is not a cosmetic ordering problem. The server the goldens and
// the branches live on is a SECOND variable, af up refuses without it, and the
// rung whose whole job is to say what is missing would have said the setup was
// fine. It is the same defect the source rung itself was written for, one
// provider along.

const pgurlManifest = startManifest + `database:
  provider: pgurl
  version: 17
  source_url_env: AF_START_TEST_SOURCE_URL
`

func TestStart_PgURLWithoutItsServerIsBlockedEvenWhenTheSourceIsSet(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, pgurlManifest)
	// The source IS present, which is what made the old ordering report the
	// whole rung as done.
	write(t, dir, ".env", "AF_START_TEST_SOURCE_URL=postgres://reader:secret@db.internal:5432/app\n")
	e, _ := startEnv(t, dir)

	s := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "the database source")
	require.Equal(t, StageBlocked, s.state,
		"the manifest names pgurl, nothing holds the server it copies into, and the rung said it was done")
	require.Contains(t, s.detail, "PGURL_ADMIN_URL",
		"the reader has to be told which variable, and it is not the source one")
	require.NotEmpty(t, s.command, "a blocked step that names no command leaves the reader stuck")
}

func TestStart_PgURLSaysWhereTheCopiesGoAndNeverThePassword(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, pgurlManifest)
	write(t, dir, ".env",
		"AF_START_TEST_SOURCE_URL=postgres://reader:secret@db.internal:5432/app\n"+
			"PGURL_ADMIN_URL=postgres://af:hunter2@spare.internal:6543/postgres\n")
	e, _ := startEnv(t, dir)

	s := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "the database source")
	require.Equal(t, StageDone, s.state)
	require.Contains(t, s.detail, "spare.internal:6543",
		"for this provider the copy comes from one server and lands on another, "+
			"and one line about the database should name both")
	// The value is a connection string with a password in it and this line is
	// printed on every af start.
	require.NotContains(t, s.detail, "hunter2", "the rung printed the password")
	require.NotContains(t, s.detail, "secret", "the rung printed the source's password")
	require.False(t, strings.Contains(s.detail, "af:"), "the rung printed the role and credential")
}

func TestStart_PgURLWithNoSourceAtAllStillNamesItsServer(t *testing.T) {
	// A project that has not connected production yet is a supported
	// configuration: the golden is seeded rather than copied. The rung still
	// has to say where the goldens will live, because that is the part af up
	// refuses without.
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+"database:\n  provider: pgurl\n")
	write(t, dir, ".env", "PGURL_ADMIN_URL=postgres://af:hunter2@spare.internal:6543/postgres\n")
	e, _ := startEnv(t, dir)

	s := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "the database source")
	require.Equal(t, StageDone, s.state)
	require.Contains(t, s.detail, "pgurl")
	require.Contains(t, s.detail, "spare.internal:6543")
	require.NotContains(t, s.detail, "hunter2")
}
