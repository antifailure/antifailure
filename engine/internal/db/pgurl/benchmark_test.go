package pgurl

import (
	"context"
	"database/sql"
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

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The measurement this provider owes, and the harness that produces it.
//
// The rule it is written to: no number is quotable unless the harness that
// produced it is in this repository, the methodology is published beside it,
// and a customer can run it against their own stack and get their own number.
// That is why this is a test in the tree rather than a figure in a document.
// It writes a dated report, it names the machine, and a number older than the
// code that produced it is withdrawn rather than rounded.
//
// Two numbers per size, because the two are different claims. The FIRST GOLDEN
// is a dump and a restore of the source, which is the cost a customer pays
// once. A BRANCH is a server side file copy of that golden, which is the cost
// they pay per environment, and for this provider it is proportional to the
// database rather than flat. Publishing the second one unflattered is the
// point: somebody whose production is a terabyte has to learn from us that
// branching it here takes minutes, not from their own trial.
//
//	just benchmark
//
// AF_BENCHMARK_ROWS sets the sizes, as a comma separated list of row counts.
// Each row is about 250 bytes, so a million rows is roughly a quarter of a
// gigabyte.
func TestBenchmarkFirstGoldenAndBranch(t *testing.T) {
	if os.Getenv("AF_BENCHMARK") == "" {
		t.Skip("skipped: set AF_BENCHMARK=1 to run the benchmark. It creates and drops " +
			"databases sized by AF_BENCHMARK_ROWS, which is slow on purpose")
	}
	admin := requirePostgres(t)
	ctx := context.Background()

	rows := []int{1000, 1000000}
	if raw := os.Getenv("AF_BENCHMARK_ROWS"); raw != "" {
		rows = nil
		for _, part := range strings.Split(raw, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(part))
			require.NoError(t, err, "AF_BENCHMARK_ROWS is a comma separated list of row counts")
			rows = append(rows, n)
		}
	}

	p, err := New(ctx, Options{
		AdminURL: secrets.New(admin), Variable: "AF_PGURL_ADMIN_URL", Clock: clock.New(),
	})
	require.NoError(t, err)
	defer func() { _ = p.Close() }()

	var results []measurement
	for _, n := range rows {
		results = append(results, measureOneSize(t, p, ctx, admin, n))
	}
	writeReport(t, p, results)
}

// measurement is one row of the report.
type measurement struct {
	rows         int
	sourceBytes  int64
	goldenBytes  int64
	refresh      time.Duration
	branch       time.Duration
	secondBranch time.Duration
}

func measureOneSize(t *testing.T, p *Provider, ctx context.Context, admin string, rows int) measurement {
	t.Helper()

	// The source is a database on the same server, named outside every prefix
	// this provider owns, so the provider could not touch it even if it tried.
	// Same server on purpose: a benchmark that also measured somebody's
	// network would be measuring the network.
	const source = "af_bench_source"
	execAdmin(t, admin, "DROP DATABASE IF EXISTS "+source+" WITH (FORCE)")
	execAdmin(t, admin, "CREATE DATABASE "+source)
	t.Cleanup(func() { execAdmin(t, admin, "DROP DATABASE IF EXISTS "+source+" WITH (FORCE)") })

	sourceURL := p.urlFor(source).Reveal()
	execURL(t, sourceURL, `
CREATE TABLE bench_accounts (
    id         bigserial PRIMARY KEY,
    email      text NOT NULL,
    name       text NOT NULL,
    address    text NOT NULL,
    note       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);`)
	execURL(t, sourceURL, fmt.Sprintf(`
INSERT INTO bench_accounts (email, name, address, note)
SELECT 'user' || g || '@example.test',
       'Name ' || g,
       repeat('address line ', 8) || g,
       repeat('note ', 20) || g
FROM generate_series(1, %d) g;`, rows))
	execURL(t, sourceURL, "CREATE INDEX bench_accounts_email ON bench_accounts (email)")
	execURL(t, sourceURL, "ANALYZE")

	m := measurement{rows: rows, sourceBytes: sizeOfDatabase(t, admin, source)}

	spec := provider.GoldenSpec{
		SourceURL: secrets.New(sourceURL),
		Version:   p.major,
		RulesHash: fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff),
		Verify: func(context.Context, secrets.Value) (string, error) {
			return `{"scanner":"benchmark","findings":0}`, nil
		},
	}
	start := time.Now()
	gv, err := p.RefreshGolden(ctx, spec)
	m.refresh = time.Since(start)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.DestroyGolden(context.Background(), gv.ID) })
	m.goldenBytes = gv.SizeBytes

	// One environment identifier per size, and the branches are destroyed
	// before the next size rather than at the end of the test. Branch is
	// idempotent by environment, so a shared identifier means the second size
	// silently gets the FIRST size's branch back: the timing is then a lookup
	// rather than a copy, and the number is fast and meaningless. That is not
	// hypothetical, it is what this harness did on its first run, and the row
	// count check below is what caught it.
	one := fmt.Sprintf("env_bench_%d_one", rows)
	two := fmt.Sprintf("env_bench_%d_two", rows)

	start = time.Now()
	b, err := p.Branch(ctx, gv.ID, one)
	m.branch = time.Since(start)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Destroy(context.Background(), b) })

	// A second branch of the same golden, because the first one warms the
	// page cache and a customer's tenth environment is the honest case.
	start = time.Now()
	b2, err := p.Branch(ctx, gv.ID, two)
	m.secondBranch = time.Since(start)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Destroy(context.Background(), b2) })

	// Proof that what was measured is a database with the rows in it. A
	// benchmark that timed an empty copy would be the fastest and the most
	// worthless number in this file.
	require.Equal(t, rows, countRows(t, p.urlFor(b.ProviderRef).Reveal()),
		"the branch does not hold the source's rows, so this measured a copy of nothing")

	// Removed here rather than at the end of the run, so that each size is
	// measured against a server this harness left as it found it.
	require.NoError(t, p.Destroy(ctx, b))
	require.NoError(t, p.Destroy(ctx, b2))
	require.NoError(t, p.DestroyGolden(ctx, gv.ID))

	t.Logf("%d rows: source %s, golden %s, first golden %s, branch %s, second branch %s",
		rows, human(m.sourceBytes), human(m.goldenBytes),
		m.refresh.Round(time.Millisecond), m.branch.Round(time.Millisecond),
		m.secondBranch.Round(time.Millisecond))
	return m
}

// writeReport writes the dated report this run produced.
func writeReport(t *testing.T, p *Provider, results []measurement) {
	t.Helper()
	dir := os.Getenv("AF_BENCHMARK_DIR")
	if dir == "" {
		_, file, _, ok := runtime.Caller(0)
		require.True(t, ok)
		dir = filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "benchmarks")
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))

	var b strings.Builder
	// The minute is in the name, not only the date. Two runs on one day are
	// the normal case while something is being tuned, and a report that
	// overwrote the earlier one would hide the thing worth seeing: how much
	// the same code varies on the same machine under different load.
	date := time.Now().UTC().Format("2006-01-02-1504")
	fmt.Fprintf(&b, "# pgurl, first golden and branch time\n\n")
	fmt.Fprintf(&b, "Run on %s UTC.\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Machine: %s %s, %d cores.\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	fmt.Fprintf(&b, "- Server: Postgres %d at %s.\n", p.major, p.hostport)
	fmt.Fprintf(&b, "- Client tools: %s, which is what does the dump and the restore.\n", clientTools())
	fmt.Fprintf(&b, "- Harness: `just benchmark`, which is "+
		"`engine/internal/db/pgurl/benchmark_test.go` in this repository.\n")
	// What else the machine was doing. A number measured on a busy machine is
	// a slower number than the same code on an idle one, and a report that did
	// not say so would be quoted as if it had been measured on an idle one.
	fmt.Fprintf(&b, "- Load average while it ran: %s.\n\n", loadAverage())
	fmt.Fprintf(&b, "| Rows | Source | Golden | First golden | Per GB | Branch | Second branch | Branch per GB |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, m := range results {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			commas(m.rows), human(m.sourceBytes), human(m.goldenBytes),
			m.refresh.Round(time.Millisecond), perGB(m.refresh, m.sourceBytes),
			m.branch.Round(time.Millisecond), m.secondBranch.Round(time.Millisecond),
			perGB(m.branch, m.goldenBytes))
	}
	fmt.Fprintf(&b, "\nThe first golden is a `pg_dump` piped into `pg_restore`. A branch is "+
		"`CREATE DATABASE ... TEMPLATE`, which copies files, so branch time grows with the "+
		"database. It is not flat and this provider does not claim it is.\n")
	fmt.Fprintf(&b, "\nRead the per gigabyte column from the LARGEST row. A small database is "+
		"mostly fixed cost, the empty template Postgres copies whichever database you asked "+
		"for, so dividing a few seconds by a few megabytes produces a rate that is real for "+
		"nothing. The rate the larger rows agree on is the one that carries.\n")

	path := filepath.Join(dir, date+"-pgurl.md")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
	t.Logf("wrote %s", path)
	t.Log("\n" + b.String())
}

func sizeOfDatabase(t *testing.T, admin, name string) int64 {
	t.Helper()
	var n int64
	queryAdmin(t, admin, "SELECT pg_database_size($1)", name, &n)
	return n
}

func countRows(t *testing.T, url string) int {
	t.Helper()
	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(),
		"SELECT count(*) FROM bench_accounts").Scan(&n))
	return n
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func perGB(d time.Duration, bytes int64) string {
	if bytes <= 0 {
		return "n/a"
	}
	gb := float64(bytes) / float64(1<<30)
	return fmt.Sprintf("%.0f s", d.Seconds()/gb)
}

func commas(n int) string {
	s := strconv.Itoa(n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

// loadAverage is what else this machine was doing, which is the difference
// between a number and a number somebody can compare with their own.
func loadAverage() string {
	out, err := exec.Command("uptime").Output()
	if err != nil {
		return "unknown"
	}
	line := strings.TrimSpace(string(out))
	if i := strings.Index(line, "load average"); i >= 0 {
		return strings.TrimSpace(line[i:])
	}
	return line
}

// clientTools names the pg_dump this run used, so a report can say which one
// produced the number.
func clientTools() string {
	out, err := exec.Command("pg_dump", "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
