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

// TestVerify_TheOutageIsWhatTheDatabaseSaidNotTheSettle holds the probe to
// the database's own account. Postgres logs the instant its checkpointer was
// killed and the instant it was ready to accept connections again, on its own
// clock, and the probe's outage must match that span to its resolution.
//
// The settle is three seconds and the database comes back in well under one.
// The number this replaced was timed from the fault to the first query after
// the settle, so it could never be less than three seconds, and it read 3.1s
// for a database that was measured ready 1.66s after the kill.
func TestVerify_TheOutageIsWhatTheDatabaseSaidNotTheSettle(t *testing.T) {
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

	require.True(t, a.Unreachable, "the probe never saw the crashed database refuse a query")
	require.True(t, a.Recovered)
	// The database's own account is the reference. When it came back well
	// inside the settle, the outage must be inside it too, which the old
	// number never was. When it did not, which a loaded host has produced
	// (Postgres syncing its data directory said 3.66s), the agreement below
	// is the whole test.
	if said < settle-time.Second {
		require.Less(t, res.Downtime, settle,
			"the outage is at least the settle, so it was timed through the settle rather than measured")
	}
	require.InDelta(t, float64(said), float64(res.Downtime), float64(a.Interval+200*time.Millisecond),
		"the database said %s and the probe measured %s", said, res.Downtime)
}
