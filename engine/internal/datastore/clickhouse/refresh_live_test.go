package clickhouse_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/datastore/clickhouse"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The whole lifecycle against a real server: a production shaped ClickHouse,
// a golden copied out of it, masked and verified, and an environment's branch
// of that golden with a chart read off it.
//
// This is the number the lane owes. Events in the twin was zero, because the
// events live in ClickHouse and ClickHouse came up empty; the count this test
// logs is what it is now, masked and verified, on a real server.
func TestRefreshLive_AGoldenIsCopiedMaskedVerifiedAndBranched(t *testing.T) {
	server := requireServer(t)
	ctx := context.Background()

	// Production, or the part of it this is about. Nothing below ever writes
	// to it, and the last assertion is that it still holds what it held.
	source := newScratch(t, server, "source")
	source.exec(eventsSchema)
	source.exec(insertEvents)
	source.exec("CREATE TABLE sessions (id UUID, distinct_id String, minutes UInt32) " +
		"ENGINE = MergeTree ORDER BY id")
	source.exec("INSERT INTO sessions VALUES " +
		"('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8c01', 'ada@lovelace-analytics.co.uk', 12)")
	// A view, which a golden does not hold and says so rather than failing.
	source.exec("CREATE VIEW recent AS SELECT * FROM events")

	p, err := clickhouse.New(clickhouse.Options{
		ServerURL: server, Name: "events",
		Progress: func(line string) { t.Log(line) },
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, p.Close()) })

	var masked clickhouse.MaskResult
	var report verify.Report
	spec := provider.GoldenSpec{
		SourceURL:  source.url,
		RulesHash:  "live",
		Provenance: "l4.2-refresh-live",
		Mask: func(ctx context.Context, candidate secrets.Value) error {
			var maskErr error
			masked, maskErr = clickhouse.Mask(ctx, candidate, clickhouse.MaskOptions{
				Key: testKey(t), Rules: liveRules(t), RulesHash: "live",
				Progress: func(line string) { t.Log(line) },
			})
			return maskErr
		},
		Verify: func(ctx context.Context, candidate secrets.Value) (string, error) {
			var scanErr error
			report, scanErr = clickhouse.Scan(ctx, candidate, clickhouse.ScanOptions{
				SampleSize: 200, Unruled: masked.CopiedUnchanged,
			})
			if scanErr != nil {
				return "", scanErr
			}
			if !report.Clean() {
				return "", errFindings(report)
			}
			return sign(t, report), nil
		},
	}

	started := time.Now()
	gv, err := p.RefreshGolden(ctx, spec)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })
	require.True(t, gv.Verified)
	require.NotEmpty(t, gv.Attestation)
	refreshed := time.Since(started)

	// The masking ran on the candidate and not on production.
	require.Equal(t, 2, masked.Tables, "the golden masked %d tables", masked.Tables)
	require.Equal(t, int64(4), masked.Rows)

	// The branch, which is what an environment is handed.
	started = time.Now()
	branch, err := p.Branch(ctx, gv.ID, "env_l42_refresh")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, p.Destroy(context.Background(), branch)) })
	branched := time.Since(started)

	conn, err := p.ConnString(ctx, branch)
	require.NoError(t, err)
	require.Contains(t, conn.Reveal(), "af_env_")
	env := &scratch{t: t, name: branch.ProviderRef, url: conn, base: server}

	// The events are in the twin. That is the sentence this lane exists for
	// and this is the assertion under it.
	rows := env.column("SELECT toString(count()) FROM events")
	require.Equal(t, []string{"3"}, rows)

	// A chart, read off the branch the way a workflow would: events per month
	// per event name, which is the query every analytics product's dashboard
	// is made of and which returns nothing at all against an empty store.
	chart := env.query(
		"SELECT event, toString(toStartOfMonth(ts)), toString(count()) " +
			"FROM events GROUP BY event, toStartOfMonth(ts) ORDER BY 2, 1")
	require.Equal(t, [][]string{
		{"click", "2026-08-01", "1"},
		{"pageview", "2026-08-01", "1"},
		{"pageview", "2026-09-01", "1"},
	}, chart, "the chart read off the twin does not match the shape production has")

	// And it is masked. Every address is gone from the branch, and the same
	// person is the same fake person in it.
	for _, address := range []string{
		"ada@lovelace-analytics.co.uk", "grace@hopper-systems.io", "alan@turing-labs.net",
	} {
		require.Equal(t, []string{"0"}, env.column(
			"SELECT toString(count()) FROM events WHERE distinct_id = '"+address+"'"),
			"%s reached the twin", address)
	}
	joined := env.column(
		"SELECT toString(count()) FROM events AS e INNER JOIN sessions AS s " +
			"ON e.distinct_id = s.distinct_id")
	require.Equal(t, []string{"1"}, joined,
		"the join between the two tables of the twin returns nothing, so one identity was "+
			"masked into two people inside one store")

	// The view is not in the golden and the golden says so rather than
	// leaving somebody to notice.
	require.Empty(t, env.column(
		"SELECT name FROM system.tables WHERE database = currentDatabase() AND name = 'recent'"))

	// A branch is the environment's own. Writing into it changes nothing
	// anybody else can see, which is what makes two environments of one golden
	// independent.
	env.exec("INSERT INTO events (uuid, event, distinct_id, properties, ts) VALUES " +
		"('01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a09', 'pageview', 'someone', '{}', now64(6))")
	require.Equal(t, []string{"4"}, env.column("SELECT toString(count()) FROM events"))
	second, err := p.Branch(ctx, gv.ID, "env_l42_refresh_two")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, p.Destroy(context.Background(), second)) })
	secondConn, err := p.ConnString(ctx, second)
	require.NoError(t, err)
	other := &scratch{t: t, name: second.ProviderRef, url: secondConn, base: server}
	require.Equal(t, []string{"3"}, other.column("SELECT toString(count()) FROM events"),
		"one environment's write reached another environment's branch")

	// Production is untouched. The copy read it and nothing else did.
	require.Equal(t, []string{"1"}, source.column(
		"SELECT toString(count()) FROM events WHERE distinct_id = 'ada@lovelace-analytics.co.uk'"),
		"the refresh wrote to the source, which is the one thing it must never do")

	t.Logf("events in the twin: %d, masked and verified. Refresh %s, branch %s, "+
		"scan clean over %d columns of %d tables",
		3, refreshed.Round(time.Millisecond), branched.Round(time.Millisecond),
		report.Columns, report.Tables)
}

// sign turns a clean report into the attestation a golden is published on.
//
// The same two calls the Postgres path makes, in the same order, because the
// attestation is what ListGoldens reads Verified back out of and a golden with
// an empty one is one nothing may branch.
func sign(t *testing.T, report verify.Report) string {
	t.Helper()
	_, priv, err := verify.GenerateKey()
	require.NoError(t, err)
	att, err := verify.Sign(report, "", "live", "l4.2-refresh-live", priv)
	require.NoError(t, err)
	body, err := json.Marshal(att)
	require.NoError(t, err)
	return string(body)
}

// errFindings renders what a scan found, without printing a value.
func errFindings(report verify.Report) error {
	var b strings.Builder
	b.WriteString("verification found ")
	for i, f := range report.Findings {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(f.Detector + " in " + f.Table + "." + f.Column)
	}
	return errString(b.String())
}

type errString string

func (e errString) Error() string { return string(e) }
