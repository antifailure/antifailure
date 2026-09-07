package insights_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
)

// The three states a rehearsal can be in, and the reason they are three.
//
// It ran and found nothing is a pass. It was declined is a decision somebody
// made and recorded. It was asked for and did not run is neither, and the
// command that printed "ok  nothing to report" over the third one is the
// defect these tests exist for. The summary line is what a developer reads,
// and it was the only line in the output that was not true.

func TestRun_ARehearsalThatWasAskedForAndDidNotRunIsBlocked(t *testing.T) {
	db, done := requireDatabase(t, "insightsblocked")
	defer done()

	const reason = "the migrations were not rehearsed: no migration tool was recognised " +
		"in this repository"
	full, err := insights.Run(context.Background(), insights.Options{
		Config: insights.Configure(nil), Branch: db.conn, Limit: 5,
		NoRehearsalReason: reason,
	})
	require.NoError(t, err)
	require.Nil(t, full.Rehearsal)
	// Blocked, and the reason travels with it, because "blocked" with no
	// because sends somebody to read the code rather than the manifest.
	require.True(t, full.IsBlocked())
	require.Contains(t, full.Blocked, reason)
	// Clean stays true and that is correct: every check that ran found
	// nothing. It is the wrong question, which is why the caller now asks
	// IsBlocked first.
	require.True(t, full.Clean())
}

func TestRun_ARehearsalTheCallerDeclinedIsNotBlocked(t *testing.T) {
	db, done := requireDatabase(t, "insightsdeclined")
	defer done()

	// --no-rehearsal is somebody saying they know. Reporting that as blocked
	// would make the flag useless, because the only reason to pass it is to
	// get a run that ends cleanly without one.
	full, err := insights.Run(context.Background(), insights.Options{
		Config: insights.Configure(nil), Branch: db.conn, Limit: 5,
		NoRehearsalReason: "the migrations were not rehearsed, because --no-rehearsal was given",
		RehearsalDeclined: true,
	})
	require.NoError(t, err)
	require.Nil(t, full.Rehearsal)
	require.False(t, full.IsBlocked())
	require.Empty(t, full.Blocked)
	// Still said out loud in the body, which is the part that does not change:
	// a check that did not run is named whether or not it stops the exit code.
	require.Contains(t, full.Missing,
		"the migrations were not rehearsed, because --no-rehearsal was given")
}

func TestRun_ARehearsalTheManifestTurnedOffIsNotBlocked(t *testing.T) {
	db, done := requireDatabase(t, "insightsoffnotblocked")
	defer done()

	// The manifest turning the check off is a decision too, and it is already
	// reported under its own heading. Counting it as blocked would fail every
	// run of a project that deliberately does not rehearse.
	cfg := insights.Configure(nil)
	cfg.MigrationRehearsal = false
	full, err := insights.Run(context.Background(), insights.Options{
		Config: cfg, Branch: db.conn, Limit: 5,
	})
	require.NoError(t, err)
	require.False(t, full.IsBlocked())
	require.Contains(t, full.Off,
		"migration rehearsal, because insights.migration_rehearsal is false")
}

func TestRun_ARehearsalWithNothingToRehearseIsBlocked(t *testing.T) {
	db, done := requireDatabase(t, "insightsnotool")
	defer done()

	// The fourth state, and the one the plan asked for by name: discovery
	// genuinely failed, so a rehearsal branch WAS prepared, the rehearsal DID
	// run, and it timed nothing, sampled no locks and linted no statements.
	// Every check below it then reports no findings, and the run reads exactly
	// like a repository whose migrations are all safe.
	full, err := insights.Run(context.Background(), insights.Options{
		Config: insights.Configure(nil), Branch: db.conn, Limit: 5,
		Rehearsal: &insights.Target{
			Conn: db.conn, Watch: db.watch, URL: db.url,
			Set:     insights.MigrationSet{},
			Applier: &insights.SQLApplier{},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, full.Rehearsal, "the rehearsal ran, which is the point")
	require.Equal(t, insights.ToolNone, full.Rehearsal.Tool)
	require.True(t, full.IsBlocked())
	require.Len(t, full.Blocked, 1)
	// The message has to name a way out or it is a dead end. All three.
	require.Contains(t, full.Blocked[0], "no migration tool was recognised")
	require.Contains(t, full.Blocked[0], "database.migrations")
	require.Contains(t, full.Blocked[0], "insights.migration_rehearsal: false")
	require.Contains(t, full.Blocked[0], "--no-rehearsal")
}

func TestRun_ARehearsalOfAToolWhoseMigrationsAreNotSQLIsNotBlocked(t *testing.T) {
	db, done := requireDatabase(t, "insightsnotsql")
	defer done()

	// Rails, Django, Alembic and Knex are recognised and then rehearsed by
	// running the project's own migrate command, so the check DID run. Their
	// reason is reported in the body and must not become an exit code, or
	// every Rails repository would fail every run.
	full, err := insights.Run(context.Background(), insights.Options{
		Config: insights.Configure(nil), Branch: db.conn, Limit: 5,
		Rehearsal: &insights.Target{
			Conn: db.conn, Watch: db.watch, URL: db.url,
			Set: insights.MigrationSet{
				Tool:   insights.ToolRails,
				Dir:    "db/migrate",
				Reason: "Rails migrations are Ruby",
			},
			Applier: &insights.SQLApplier{},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, full.Rehearsal)
	require.False(t, full.IsBlocked())
	require.Empty(t, full.Blocked)
	require.Contains(t, full.Missing, "Rails migrations are Ruby")
}
