// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// The waits a refresh and a branch spend minutes in say so while they happen.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/rds/fakerds"
	"github.com/antifailure/antifailure/engine/conformance"
)

// collector is a progress sink a test can read after the call returns.
type collector struct {
	mu    sync.Mutex
	lines []string
}

func (c *collector) add(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, line)
}

func (c *collector) has(substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.lines {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

// A restore and a snapshot each report that they are waiting and that they
// finished, so a first af up against RDS is not silent for the minutes AWS takes.
func TestARefreshAndABranchReportTheirWaits(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	var got collector
	p.ReportProgressTo(got.add)

	version := refresh(t, p)
	b, err := p.Branch(context.Background(), version.ID, "env_reports_its_wait")
	require.NoError(t, err)

	for _, want := range []string{
		"waiting for RDS to finish the snapshot",
		"RDS reports the snapshot",
		"waiting for RDS to bring up the instance " + b.ProviderRef,
		"RDS reports the instance " + b.ProviderRef + " available after",
	} {
		require.Truef(t, got.has(want), "no progress line containing %q in %q", want, got.lines)
	}
}

// A wait that goes on keeps saying so. The clock jumps twenty seconds on every
// read, so a heartbeat of fifteen seconds is due on every poll without the test
// waiting for wall time.
func TestALongWaitReportsAHeartbeat(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, fakerds.FaultInstanceNeverBecomesAvailable)
	opts := options(t, server)
	var mu sync.Mutex
	clock := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	opts.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(20 * time.Second)
		return clock
	}
	opts.ReadyTimeout = 10 * time.Minute
	p, err := scopedNew(context.Background(), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	var got collector
	p.ReportProgressTo(got.add)

	var masked, verified int
	_, err = p.RefreshGolden(context.Background(), spec(&masked, &verified, `{"findings":0}`))
	require.Error(t, err, "an instance that never became available produced a golden")
	require.Truef(t, got.has("still waiting for the instance"),
		"a wait that went on for minutes never repeated that it was still waiting: %q", got.lines)
}

// With nowhere to report, a wait reports nowhere and still completes.
func TestAWaitWithNoProgressSinkStillCompletes(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL, "")
	p := newProvider(t, server)
	p.ReportProgressTo(nil)
	refresh(t, p)
}
