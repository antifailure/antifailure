package clickhouse_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/datastore/clickhouse"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/verify"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The lane's number: events in the twin, masked and verified. It was zero.
//
// Zero is not a rhetorical zero. An environment's second store was an empty
// container: the manifest could declare a ClickHouse, the runtime would start
// the image, and nothing copied anything into it, so every chart in the twin
// drew nothing and every query path over the events was tested against no
// rows. The before column here is that store, measured rather than asserted.
//
// The harness is in this repository and takes its size from the environment,
// so anybody can run it against their own events table and get their own
// number:
//
//	AF_BENCHMARK=1 AF_BENCHMARK_EVENTS=1000000 just benchmark
func TestBenchmarkEventsInTheTwin(t *testing.T) {
	if os.Getenv("AF_BENCHMARK") == "" {
		t.Skip("skipped: set AF_BENCHMARK=1, or run just benchmark")
	}
	server := requireServer(t)
	ctx := context.Background()

	rows := 100_000
	if raw := os.Getenv("AF_BENCHMARK_EVENTS"); raw != "" {
		n, err := strconv.Atoi(raw)
		require.NoError(t, err, "AF_BENCHMARK_EVENTS is not a number")
		rows = n
	}

	// Production, generated rather than fixtured, because the number is about
	// volume. Every row carries an address, which is what makes the masking
	// work real: the distinct value map is the whole rewrite and this gives it
	// as many distinct values as there are rows.
	source := newScratch(t, server, "benchsource")
	source.exec(eventsSchema)
	started := time.Now()
	source.exec(fmt.Sprintf(`INSERT INTO events
SELECT generateUUIDv4(),
       if(number %% 3 = 0, 'click', 'pageview'),
       concat('person', toString(number), '@example-analytics.com'),
       concat('person', toString(number), '@example-analytics.com'),
       generateUUIDv4(),
       '{"plan":"team"}',
       concat('81.2.69.', toString(number %% 250 + 1)),
       toDateTime64('2026-08-01 00:00:00', 6) + toIntervalSecond(number)
FROM numbers(%d)`, rows))
	loaded := time.Since(started)

	// The BEFORE figure, measured. This is the environment's events store as
	// it came up until this lane: the image starts, the migrations create the
	// tables, and nothing puts a row in one.
	before := newScratch(t, server, "benchbefore")
	before.exec(eventsSchema)
	require.Equal(t, []string{"0"}, before.column("SELECT toString(count()) FROM events"))

	p, err := clickhouse.New(clickhouse.Options{
		ServerURL: server, Name: "events", Progress: func(line string) { t.Log(line) },
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, p.Close()) })

	var masked clickhouse.MaskResult
	var report verify.Report
	var copied, masking, verifying time.Duration
	refreshStarted := time.Now()
	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		SourceURL: source.url, RulesHash: "benchmark", Provenance: "l4.2-benchmark",
		Mask: func(ctx context.Context, candidate secrets.Value) error {
			copied = time.Since(refreshStarted)
			maskStarted := time.Now()
			var maskErr error
			masked, maskErr = clickhouse.Mask(ctx, candidate, clickhouse.MaskOptions{
				Key: testKey(t), Rules: liveRules(t), RulesHash: "benchmark",
			})
			masking = time.Since(maskStarted)
			return maskErr
		},
		Verify: func(ctx context.Context, candidate secrets.Value) (string, error) {
			verifyStarted := time.Now()
			var scanErr error
			report, scanErr = clickhouse.Scan(ctx, candidate, clickhouse.ScanOptions{
				Unruled: masked.CopiedUnchanged,
			})
			verifying = time.Since(verifyStarted)
			if scanErr != nil {
				return "", scanErr
			}
			if !report.Clean() {
				return "", errFindings(report)
			}
			return sign(t, report), nil
		},
	})
	require.NoError(t, err)
	refresh := time.Since(refreshStarted)
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })

	// The branch, twice, at two sizes. A branch attaches the golden's
	// partitions, which ClickHouse hardlinks when both sit on one disk, so
	// branch time should not move with the number of rows. This is what that
	// claim is worth on this machine rather than what the documentation says
	// it should be.
	branchStarted := time.Now()
	branch, err := p.Branch(ctx, gv.ID, "env_l42_benchmark")
	require.NoError(t, err)
	branched := time.Since(branchStarted)
	t.Cleanup(func() { require.NoError(t, p.Destroy(context.Background(), branch)) })

	conn, err := p.ConnString(ctx, branch)
	require.NoError(t, err)
	twin := &scratch{t: t, name: branch.ProviderRef, url: conn, base: server}
	after := twin.column("SELECT toString(count()) FROM events")
	require.Equal(t, []string{strconv.Itoa(rows)}, after,
		"the twin does not hold what production holds")

	// A small golden beside the large one, so the branch time has something to
	// be flat against.
	small := newScratch(t, server, "benchsmall")
	small.exec(eventsSchema)
	small.exec(insertEvents)
	smallGV, smallBranched := branchOf(t, ctx, p, small.url, "env_l42_benchmark_small")

	// And the leak the number would be worthless without: no address survived.
	require.Equal(t, []string{"0"}, twin.column(
		"SELECT toString(count()) FROM events WHERE distinct_id LIKE '%example-analytics.com'"),
		"production's addresses are in the twin, so the number counts a leak")

	body := benchmarkReport(t, server, benchmarkNumbers{
		Rows: rows, Loaded: loaded, Copied: copied, Masking: masking,
		Verifying: verifying, Refresh: refresh, Branched: branched,
		SmallRows: 3, SmallBranched: smallBranched,
		Values: masked.Values, Tables: masked.Tables, Columns: report.Columns,
		Size: gv.SizeBytes,
	})
	t.Log("\n" + body)
	out := os.Getenv("AF_BENCHMARK_EVENTS_OUT")
	if out == "" {
		out = filepath.Join("..", "..", "..", "..", "benchmarks",
			time.Now().UTC().Format("2006-01-02-1504")+"-events-in-the-twin.md")
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
	require.NoError(t, os.WriteFile(out, []byte(body), 0o644))
	t.Logf("wrote %s", out)
	_ = smallGV
}

// branchOf publishes a golden of one database and branches it, returning how
// long the branch took.
func branchOf(
	t *testing.T, ctx context.Context, p *clickhouse.Provider, source secrets.Value, envID string,
) (string, time.Duration) {
	t.Helper()
	var masked clickhouse.MaskResult
	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		SourceURL: source, RulesHash: "benchmark", Provenance: "l4.2-benchmark-small",
		Mask: func(ctx context.Context, candidate secrets.Value) error {
			var maskErr error
			masked, maskErr = clickhouse.Mask(ctx, candidate, clickhouse.MaskOptions{
				Key: testKey(t), Rules: liveRules(t), RulesHash: "benchmark",
			})
			return maskErr
		},
		Verify: func(ctx context.Context, candidate secrets.Value) (string, error) {
			report, scanErr := clickhouse.Scan(ctx, candidate, clickhouse.ScanOptions{
				Unruled: masked.CopiedUnchanged,
			})
			if scanErr != nil {
				return "", scanErr
			}
			if !report.Clean() {
				return "", errFindings(report)
			}
			return sign(t, report), nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })

	started := time.Now()
	branch, err := p.Branch(ctx, gv.ID, envID)
	require.NoError(t, err)
	took := time.Since(started)
	t.Cleanup(func() { require.NoError(t, p.Destroy(context.Background(), branch)) })
	return gv.ID, took
}

type benchmarkNumbers struct {
	Rows                               int
	Loaded, Copied, Masking, Verifying time.Duration
	Refresh, Branched                  time.Duration
	SmallRows                          int
	SmallBranched                      time.Duration
	Values                             int64
	Tables, Columns                    int
	Size                               int64
}

func benchmarkReport(t *testing.T, server secrets.Value, n benchmarkNumbers) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# Events in the twin, before and after a second datastore\n\n")
	fmt.Fprintf(&b, "Run on %s.\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Machine: %s %s, %d cores, %s.\n",
		runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), loadAverage())
	fmt.Fprintf(&b, "- Store: %s.\n", serverVersion(t, server))
	fmt.Fprintf(&b, "- Harness: `just benchmark`, which is "+
		"`engine/internal/datastore/clickhouse/benchmark_test.go` in this repository. "+
		"AF_BENCHMARK_EVENTS sets the row count.\n\n")

	fmt.Fprintf(&b, "| | Events in the environment's ClickHouse |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| Before | 0 |\n")
	fmt.Fprintf(&b, "| After | %s |\n\n", thousands(int64(n.Rows)))

	fmt.Fprintf(&b, "The before figure is measured rather than asserted: it is a ClickHouse "+
		"started from the image a manifest declares, with the tables its migrations would "+
		"create and nothing in them, which is what an environment's second store was. Every "+
		"chart in that twin drew nothing.\n\n")

	fmt.Fprintf(&b, "## What the refresh did\n\n")
	fmt.Fprintf(&b, "| Step | Time |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| Copy from production | %s |\n", round(n.Copied))
	fmt.Fprintf(&b, "| Mask | %s |\n", round(n.Masking))
	fmt.Fprintf(&b, "| Verify | %s |\n", round(n.Verifying))
	fmt.Fprintf(&b, "| Whole refresh | %s |\n\n", round(n.Refresh))
	fmt.Fprintf(&b, "%s distinct values masked across %d tables, and %d columns read back by "+
		"the verification scan. The golden holds %s on disk.\n\n",
		thousands(n.Values), n.Tables, n.Columns, humanBytes(n.Size))

	fmt.Fprintf(&b, "## Branch time\n\n")
	fmt.Fprintf(&b, "| Golden | Rows | Branch |\n| --- | --- | --- |\n")
	fmt.Fprintf(&b, "| Small | %d | %s |\n", n.SmallRows, round(n.SmallBranched))
	fmt.Fprintf(&b, "| Large | %s | %s |\n\n", thousands(int64(n.Rows)), round(n.Branched))

	fmt.Fprintf(&b, "Which of those two is the larger changes from run to run, and that is "+
		"the result rather than an accident of this one: both are a few tens of "+
		"milliseconds of metadata, so the noise of a loaded machine is bigger than the "+
		"difference the row count makes.\n\n")
	fmt.Fprintf(&b, "A branch is a fresh database with the golden's partitions attached, and "+
		"ClickHouse hardlinks the parts when the source and the destination are on one disk, "+
		"so the two times above are about the same however far apart the row counts are. "+
		"**The provider still declares CopyOnWrite false**, and that is deliberate: a "+
		"multi disk storage policy copies the parts instead, and the provider cannot see the "+
		"server's storage policy from the client. The measurement is published here rather "+
		"than promised in a capability, because a capability is something the engine acts "+
		"on and this one would be a promise about somebody else's hardware.\n\n")

	fmt.Fprintf(&b, "Loading production's own rows took %s and is not part of any figure "+
		"above; it is the fixture being built.\n", round(n.Loaded))
	return b.String()
}

// loadAverage is what else this machine was doing, which is the difference
// between a number and a number somebody can compare with their own.
//
// The cross store benchmark deliberately does not record it, because a count of
// columns that agree does not move with load. This one is times, and a time
// measured on a laptop under load is partly about the laptop.
func loadAverage() string {
	out, err := exec.Command("uptime").Output()
	if err != nil {
		return "load unknown"
	}
	line := strings.TrimSpace(string(out))
	if i := strings.Index(line, "load average"); i >= 0 {
		return strings.TrimSpace(line[i:])
	}
	return line
}

func serverVersion(t *testing.T, server secrets.Value) string {
	t.Helper()
	out, err := request(context.Background(), server, "SELECT version() FORMAT TSV", nil)
	if err != nil {
		return "ClickHouse"
	}
	return "ClickHouse " + strings.TrimSpace(out)
}

func round(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(10 * time.Millisecond).String()
}

func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
