// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// A master password RDS has accepted and not yet put in force.
//
// A live run against real RDS found the provider connecting with the new
// password before it worked. ModifyDBInstance returned, the next describe 0.6
// seconds later still answered available, and the first connection was refused
// with SQLSTATE 28P01. The fake now lags the same way, in the two shapes that
// matter, and each test says which of the provider's two waits did the work:
//
//   - RDS lists the password as pending until it applies it. The pending wait
//     must hold the provider back, so no connection is ever refused.
//   - RDS shows nothing at all. Only trying the credential can tell, so the
//     refusal is seen and waited out.
//
// Each runs a refresh and a branch, because both rotate: the refresh's
// candidate and the branch are both restores that carry the source's password.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/rds/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// refusedLine is the progress line the provider writes when the derived
// password was refused, which only happens when it connected before RDS had
// put the password in force.
const refusedLine = "has not put it in force yet"

func newPasswordLagFake(t *testing.T, lag int, invisible bool) *fakerds.Server {
	t.Helper()
	return newFakeWith(t, fakerds.Options{
		AdminURL:               requirePostgres(t),
		Prefix:                 "af_rds_" + randomSuffix(t) + "_",
		Region:                 testRegion,
		Credentials:            testCredentials,
		PasswordLag:            lag,
		PasswordLagIsInvisible: invisible,
	}, conformance.DefaultSeedSQL)
}

// branchThroughLag refreshes and branches against a lagging fake, requires the
// branch to open with its derived password, and returns every progress line.
func branchThroughLag(t *testing.T, server *fakerds.Server, envID string) []string {
	t.Helper()
	p := newProvider(t, server)
	var mu sync.Mutex
	var lines []string
	p.ReportProgressTo(func(line string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, line)
	})
	ctx := context.Background()
	version := refresh(t, p)

	b, err := p.Branch(ctx, version.ID, envID)
	require.NoError(t, err)
	connection, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, reachable(connection.Reveal()))

	// Both rotations lagged, or this checked less than its name says.
	require.Equal(t, 2, server.PasswordLagsApplied(),
		"the candidate's and the branch's rotations did not both lag")
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), lines...)
}

func countContaining(lines []string, part string) int {
	n := 0
	for _, line := range lines {
		if strings.Contains(line, part) {
			n++
		}
	}
	return n
}

// RDS lists the password as pending, and the provider waits for it to clear
// rather than connecting and being refused.
func TestARotationRDSListsAsPendingIsWaitedForBeforeConnecting(t *testing.T) {
	lines := branchThroughLag(t, newPasswordLagFake(t, 3, false), "env_password_pending")
	require.Zero(t, countContaining(lines, refusedLine),
		"the provider connected while RDS still listed the password as pending: %q", lines)
}

// RDS never shows the change, and the password comes into force some describes
// later. The provider finds out from the credential and waits the refusal out.
func TestARotationRDSNeverShowsIsWaitedForByItsCredential(t *testing.T) {
	lines := branchThroughLag(t, newPasswordLagFake(t, 12, true), "env_password_invisible")
	require.Equal(t, 2, countContaining(lines, refusedLine),
		"the invisible lag was not met as a refused credential on both rotations, so this "+
			"did not exercise the credential wait: %q", lines)
}

// A rotation that never takes effect fails, bounded by the ready timeout, with a
// sentence that names the rotation rather than a bare authentication error.
//
// The ready timeout is lowered to seconds, so the bound is proved to end the
// wait rather than the test's own timeout ending it.
func TestARotationThatNeverTakesEffectIsReportedAsTheRotation(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, fakerds.FaultPasswordIsNotRotated)
	opts := options(t, server)
	opts.ReadyTimeout = 3 * time.Second
	p, err := scopedNew(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })

	var masked, verified int
	started := time.Now()
	_, err = p.RefreshGolden(context.Background(), spec(&masked, &verified, `{"findings":0}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "was still refused 3s after ModifyDBInstance set it, so the rotation did not take effect")
	require.Less(t, time.Since(started), 30*time.Second, "the refresh was not ended by the bound")
	require.Zero(t, masked, "the masking step ran on an instance whose rotation never took effect")
}
