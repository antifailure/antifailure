// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg_test

// A branch taken from a golden published moments ago.
//
// Microsoft documents two things that meet here. A new server's first snapshot
// backup "is scheduled immediately after a server is created", so a server is
// not restorable the instant it exists. And a restore time "earlier than the
// earliest restore point available on the source server" is answered with
// InternalServerError. A golden is a server this provider created by a restore,
// and a branch is a restore of it, so a branch taken right after RefreshGolden
// asks for a point in time the golden does not have yet. The one live run of
// the branch path, on 2026-09-12, failed with exactly that InternalServerError.
//
// fakeazurepg now models both: every server it creates by a restore reports a
// backup.earliestRestoreDate FirstBackupDelay after creation, and a restore
// before that point is refused with InternalServerError.
//
// The first two tests below once set that delay to 300ms and relied on it not
// having passed by the time Branch ran. It is counted from the golden's
// creation, and RefreshGolden still prepares, loads, masks, verifies and tags
// the golden after that, so on a loaded runner the 300ms was spent before
// Branch began. The backup was then already there, Branch never waited, one
// test failed with no progress lines at all and the other passed having tested
// nothing. So the golden is now held unrestorable for an hour and released by
// the test at the moment the provider says it is waiting, which makes the wait
// certain rather than likely, and costs nothing when the machine is fast.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/azurepg"
	"github.com/antifailure/antifailure/ee/engine/db/azurepg/fakeazurepg"
)

func newFakeWithBackupDelay(t *testing.T, delay time.Duration) *fakeazurepg.Server {
	t.Helper()
	server := newFake(t, seedSQL)
	fakeazurepg.SetFirstBackupDelayForTest(server, delay)
	return server
}

// firstBackupWaitStarts opens the line a restore reports when it begins
// waiting for a first backup.
const firstBackupWaitStarts = "waiting for Azure's first backup of"

// releasedWhenWaiting returns a provider over a fake whose golden has no backup
// until the provider reports that it is waiting for one, and the progress lines
// it reported. Whatever the steps before the restore cost, the branch has to
// wait, and the wait ends as soon as it has been announced.
func releasedWhenWaiting(t *testing.T) (*azurepg.Provider, *[]string) {
	t.Helper()
	server := newFakeWithBackupDelay(t, time.Hour)
	// Bounded well short of the hour, so a provider that never announces its
	// wait, and so is never released, fails here in seconds instead of
	// holding the suite for the default thirty minutes.
	opts := options(t, server)
	opts.RestoreReadyTimeout = 30 * time.Second
	p, err := azurepg.NewWithFixtureRoles(opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	var lines []string
	p.ReportProgressTo(func(line string) {
		lines = append(lines, line)
		// A prefix, not a substring: the heartbeat, "still waiting for Azure's
		// first backup of", contains this text, and matching it would release
		// the golden fifteen seconds late for a provider that never made the
		// opening announcement at all.
		if strings.HasPrefix(line, firstBackupWaitStarts) {
			fakeazurepg.ReleaseRestorePointsForTest(server)
		}
	})
	return p, &lines
}

func TestABranchWaitsForTheGoldensFirstBackup(t *testing.T) {
	p, _ := releasedWhenWaiting(t)
	ctx := context.Background()

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)

	// Immediately, with no pause: the golden has no backup until the provider
	// says it is waiting for one, so a branch that asked for now without
	// waiting is refused by the fake.
	_, err = p.Branch(ctx, version.ID, "env_waits_for_backup")
	require.NoError(t, err, "a branch taken right after the golden was published asked for a "+
		"restore time before the golden's earliest restore point")
}

func TestTheWaitForAFirstBackupIsReportedAsItHappens(t *testing.T) {
	p, reported := releasedWhenWaiting(t)
	ctx := context.Background()

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	_, err = p.Branch(ctx, version.ID, "env_reports_its_wait")
	require.NoError(t, err)

	lines := *reported
	started, ended := false, false
	for _, line := range lines {
		if strings.HasPrefix(line, firstBackupWaitStarts) {
			started = true
		}
		if strings.Contains(line, "is ready after") {
			ended = true
		}
	}
	require.Truef(t, started, "a restore waited for a first backup and never said it was waiting: %q", lines)
	require.Truef(t, ended, "a restore finished waiting for a first backup and never said so: %q", lines)
}

func TestARestorePointThatNeverArrivesIsReportedByName(t *testing.T) {
	server := newFakeWithBackupDelay(t, time.Hour)
	opts := options(t, server)
	opts.RestoreReadyTimeout = 200 * time.Millisecond
	p, err := azurepg.NewWithFixtureRoles(opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	ctx := context.Background()

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)

	_, err = p.Branch(ctx, version.ID, "env_never_restorable")
	require.Error(t, err)
	require.Contains(t, err.Error(), "backup.earliestRestoreDate",
		"a restore that could not start said nothing about why")
}
