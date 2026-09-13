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

func TestABranchWaitsForTheGoldensFirstBackup(t *testing.T) {
	server := newFakeWithBackupDelay(t, 300*time.Millisecond)
	p := newProvider(t, server)
	ctx := context.Background()

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)

	// Immediately, with no pause: the golden's first backup is 300ms away, so a
	// branch that asked for now without waiting is refused by the fake.
	_, err = p.Branch(ctx, version.ID, "env_waits_for_backup")
	require.NoError(t, err, "a branch taken right after the golden was published asked for a "+
		"restore time before the golden's earliest restore point")
}

func TestTheWaitForAFirstBackupIsReportedAsItHappens(t *testing.T) {
	server := newFakeWithBackupDelay(t, 300*time.Millisecond)
	p := newProvider(t, server)
	var lines []string
	p.ReportProgressTo(func(line string) { lines = append(lines, line) })
	ctx := context.Background()

	version, err := p.RefreshGolden(ctx, goldenSpec())
	require.NoError(t, err)
	_, err = p.Branch(ctx, version.ID, "env_reports_its_wait")
	require.NoError(t, err)

	started, ended := false, false
	for _, line := range lines {
		if strings.Contains(line, "waiting for Azure's first backup of") {
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
