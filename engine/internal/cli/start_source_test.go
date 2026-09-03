package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// af start reported the database source as finished for a manifest that names
// production and a machine that does not hold it.
//
// The step's whole job is to say what was observed here and now. It answered
// "docker, so it comes from the daemon checked above", which is true about the
// provider and says nothing about the variable the manifest names, and the
// reader was pointed at the next command. Before AF-DB-016 the refresh that
// followed published an empty golden; after it, the refresh refuses, and this
// was the last place still saying the setup was fine.

const sourceManifest = startManifest + `database:
  provider: docker
  source_url_env: PRODUCTION_DATABASE_URL
`

func TestStart_ANamedSourceThatNothingHoldsIsNotFinished(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, sourceManifest)
	e, _ := startEnv(t, dir)

	s := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "the database source")
	require.Equal(t, StageBlocked, s.state,
		"the step named production and nothing holds it, and it said it was done")
	require.Contains(t, s.detail, "PRODUCTION_DATABASE_URL",
		"the reader has to be told which variable")
	require.NotEmpty(t, s.command, "a blocked step that names no command leaves the reader stuck")
}

func TestStart_ANamedSourceInDotEnvIsFinishedAndSaysWhereItCameFrom(t *testing.T) {
	// The ordering that matters. .env is the second source the chain reads and
	// the shell here is empty, so a step that only looked at the shell would
	// call this one blocked and send somebody to export a value they already
	// have on disk.
	dir := t.TempDir()
	writeManifest(t, dir, sourceManifest)
	write(t, dir, ".env", "PRODUCTION_DATABASE_URL=postgres://reader:secret@db.internal:5432/app\n")
	e, _ := startEnv(t, dir)

	s := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "the database source")
	require.Equal(t, StageDone, s.state)
	require.Contains(t, s.detail, ".env", "a reader who has two copies needs to know which one won")
	require.NotContains(t, s.detail, "secret", "the step reports where the value is, never the value")
}

func TestStart_AManifestThatNamesNoSourceIsUnchanged(t *testing.T) {
	// A project with no production behind it is a supported configuration, and
	// the docker provider answering for itself is the right report.
	dir := t.TempDir()
	writeManifest(t, dir, startManifest+"database:\n  provider: docker\n")
	e, _ := startEnv(t, dir)

	s := stageNamed(t, firstRun(t.Context(), e, startProbeFor(t, t.TempDir())), "the database source")
	require.Equal(t, StageDone, s.state)
	require.Contains(t, s.detail, "docker")
}
