package pgcrash_test

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fault"
	"github.com/antifailure/antifailure/engine/internal/pgcrash"
)

var rePGTime = regexp.MustCompile(`(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}) UTC`)

// pgTime is the timestamp Postgres wrote on its own log line, which is the
// database's clock rather than this process's.
func pgTime(t *testing.T, line string) time.Time {
	t.Helper()
	m := rePGTime.FindStringSubmatch(line)
	require.NotNil(t, m, "no Postgres timestamp in %q", line)
	at, err := time.Parse("2006-01-02 15:04:05.000", m[1])
	require.NoError(t, err)
	return at
}

// TestVerify_TheProbeReportsWhatItObservedNotTheSettle checks a real crash
// against both the probe and Postgres's log. The log times process recovery;
// the probe times unanswered queries, which may begin later. A short restart
// can fall entirely between two attempts, so exact equality to the log is
// not a valid assertion about this sampler.
//
// The settle is three seconds. The number this replaced was timed from the
// fault to the first query after the settle, so it could never be less than
// three seconds, and it read 3.1s
// for a database that was measured ready 1.66s after the kill.
func TestVerify_TheProbeReportsWhatItObservedNotTheSettle(t *testing.T) {
	cli := requireDocker(t)
	envID := "pgo" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36)
	db := startDatabase(t, cli, envID, testKind, "")
	inj, err := fault.New(cli, envID)
	require.NoError(t, err)
	sh := shell(t, cli, db)

	const settle = 3 * time.Second
	res, err := pgcrash.Verify(t.Context(), pgcrash.Options{
		URL: db.url, Runner: runner{sh}, DataDir: pgData,
		Workload:    pgcrash.WorkloadOptions{Writers: 4},
		WarmCommits: 200, WarmTimeout: 60 * time.Second,
		Settle: settle, ReadyTimeout: 2 * time.Minute,
		ExpectCrash: true, FaultName: "postgres-crash",
		Inject: func(ctx context.Context) (pgcrash.Injected, error) {
			in, err := inj.Inject(ctx, fault.Fault{
				Name: "postgres-crash", Kind: fault.KindProcessKill,
				Target: fault.Target{Role: fault.RoleDatabase}, Process: "postgres: checkpointer",
			})
			if err != nil {
				return pgcrash.Injected{}, err
			}
			return pgcrash.Injected{Evidence: in.Evidence, KilledSignal: in.KilledSignal}, nil
		},
	})
	require.NoError(t, err)
	report(t, res)

	var ready string
	for _, line := range res.Recovery.Lines {
		if strings.Contains(line, "ready to accept connections") {
			ready = line
		}
	}
	require.NotEmpty(t, res.Recovery.CrashLine, "the log carries no crash to time from")
	require.NotEmpty(t, ready, "the log carries no ready line to time to")
	said := pgTime(t, ready).Sub(pgTime(t, res.Recovery.CrashLine))
	a := res.Availability
	t.Logf("the database said it was down for %s, the probe measured %s (every %s)", said, a.For, a.Interval)

	require.Equal(t, 100*time.Millisecond, a.Interval)
	// Postgres logged a 70 ms recovery on one runner and both probes answered.
	// Never turn an unobserved refusal into an invented outage.
	if !a.Unreachable {
		require.Zero(t, a.For)
		require.False(t, a.Recovered)
		require.Zero(t, res.Downtime)
		return
	}
	require.True(t, a.Recovered)
	require.Equal(t, a.For, res.Downtime, "the report must use what the probe observed")
	// A quick restart must not be reported as the whole settle. When recovery
	// takes longer, the probe is still the source for query availability, not
	// the process-recovery timestamps from Postgres's log.
	if said < settle-time.Second {
		require.Less(t, res.Downtime, settle,
			"the outage is at least the settle, so it was timed through the settle rather than measured")
	}
}
