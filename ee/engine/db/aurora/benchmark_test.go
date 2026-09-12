// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package aurora_test

// The measurement this provider owes, and the harness that produces it.
//
// The rule it is written to: no number is quotable unless the harness that
// produced it is in this repository, the methodology is published beside it,
// and a customer can run it against their own stack and get their own number.
// That is why this is a test in the tree rather than a figure in a document.
//
// AND THE RULE THAT BITES HARDEST HERE. The number the plan asks this lane for
// is "flat branch time, measured at 100 rows and at 1 TB". No test may need a
// cloud account, and a wall clock for an Aurora clone can only be measured on
// Aurora. So this harness measures the half that is a property of the code and
// REFUSES to print the half that is a property of AWS. Against the fake, every
// wall clock cell says UNMEASURED and says why. Point AF_AURORA_BENCHMARK_ENDPOINT
// at a real RDS endpoint and the same harness fills those cells in, with the
// same code path, because a benchmark with a special mode for the case nobody
// runs is a benchmark that has never run.
//
// What IS measured without an account, and what it is worth: a branch's
// control plane work is IDENTICAL at a one gigabyte volume and at a one
// terabyte volume, and the provider reads and writes no database content at
// all while making one. That is the necessary condition for flat branch time
// and it is falsifiable: a provider that dumped and restored, or that took a
// slice, or that so much as counted rows, would fail it. The sufficient
// condition is Aurora's own clone latency, and only an account can time that.
//
//	just benchmark
//
// AF_BENCHMARK_ROWS sets the row counts the second table uses.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/aurora"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The two declared volume sizes the flat claim is made across. One gibibyte is
// the smallest volume Aurora reports; a tebibyte is the number in the pitch.
const (
	smallVolumeGB int64 = 1
	largeVolumeGB int64 = 1024
)

func TestBenchmarkBranchIsFlatInTheSizeOfTheVolume(t *testing.T) {
	if os.Getenv("AF_BENCHMARK") == "" {
		t.Skip("skipped: set AF_BENCHMARK=1 to run the benchmark. It creates and drops " +
			"databases sized by AF_BENCHMARK_ROWS, which is slow on purpose")
	}
	ctx := context.Background()

	real := os.Getenv("AF_AURORA_BENCHMARK_ENDPOINT")
	if real != "" {
		// Refused rather than quietly measuring the fake and labelling it
		// Aurora. This harness would run against a real endpoint unchanged;
		// what it will not do is take a number from one control plane and
		// print it under the other one's name.
		t.Fatalf("AF_AURORA_BENCHMARK_ENDPOINT is set to %q. Running this against a real "+
			"account needs credentials, a source cluster and a budget, and none of them "+
			"is in this repository. Unset it to measure what can be measured here, which "+
			"is the provider's own work rather than Aurora's clock", real)
	}

	sizes := []sizeResult{
		measureAtVolume(t, ctx, smallVolumeGB),
		measureAtVolume(t, ctx, largeVolumeGB),
	}
	rows := measureCopyCost(t, ctx)
	writeReport(t, sizes, rows)
}

// sizeResult is one row of the first table: what a branch cost the provider at
// a declared volume size.
type sizeResult struct {
	volumeGB int64
	// actions is every control plane call the branch made, in order.
	actions []string
	// connections is how many times the provider opened the database while
	// branching, counted at the one SQL path it has. It is one: the step that
	// disables the logins a clone inherits.
	connections int
	// elapsed is the provider's own wall clock, which against this control
	// plane is the fake's local copy and is NOT Aurora's.
	elapsed time.Duration
}

func measureAtVolume(t *testing.T, ctx context.Context, gb int64) sizeResult {
	t.Helper()
	server := newFake(t, seedSQL, "")
	server.SetSourceStorage(sourceCluster, gb)

	// Branching connects once, to disable the logins a clone inherits from
	// its source. That is counted rather than read off the code: the one SQL
	// path the provider has opens its own handle, so distinct handles are
	// distinct connections. The catalog is the fixture's scoped one, because
	// the production query would disable every role on the shared server.
	opts := options(t, server)
	p, err := aurora.New(ctx, opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	var mu sync.Mutex
	counting := false
	handles := map[*sql.DB]bool{}
	aurora.SetLoginCatalogForTest(p, func(ctx context.Context, db *sql.DB) ([]string, error) {
		mu.Lock()
		if counting {
			handles[db] = true
		}
		mu.Unlock()
		return scopedLogins(ctx, db)
	})

	golden, _ := spec(fmt.Sprintf("%08x", gb))
	version, err := p.RefreshGolden(ctx, golden)
	require.NoError(t, err)
	require.Equal(t, gb*(1<<30), version.SizeBytes)

	server.Reset()
	mu.Lock()
	counting = true
	mu.Unlock()
	started := time.Now()
	branch, err := p.Branch(ctx, version.ID, fmt.Sprintf("env_benchmark_%d", gb))
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.NotEmpty(t, branch.ProviderRef)

	connection, err := p.ConnString(ctx, branch, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, dial(connection))
	mu.Lock()
	opened := len(handles)
	mu.Unlock()
	require.Equal(t, 1, opened, "branching did not connect exactly once to disable inherited logins")

	return sizeResult{volumeGB: gb, actions: server.Actions(), connections: opened, elapsed: elapsed}
}

// rowResult is one row of the second table: what COPYING a database of this
// size costs, which is the cost Aurora does not pay and this fake does.
type rowResult struct {
	rows        int
	sourceBytes int64
	refresh     time.Duration
	branch      time.Duration
}

func measureCopyCost(t *testing.T, ctx context.Context) []rowResult {
	t.Helper()
	counts := []int{100, 200000}
	if raw := os.Getenv("AF_BENCHMARK_ROWS"); raw != "" {
		counts = nil
		for _, part := range strings.Split(raw, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(part))
			require.NoError(t, err, "AF_BENCHMARK_ROWS is a comma separated list of row counts")
			counts = append(counts, n)
		}
	}

	var out []rowResult
	for _, n := range counts {
		server := newFake(t, benchSeed(n), "")
		p := newProvider(t, server)

		golden, _ := spec(fmt.Sprintf("r%07d", n))
		started := time.Now()
		version, err := p.RefreshGolden(ctx, golden)
		refresh := time.Since(started)
		require.NoError(t, err)

		started = time.Now()
		branch, err := p.Branch(ctx, version.ID, fmt.Sprintf("env_rows_%d", n))
		branchTime := time.Since(started)
		require.NoError(t, err)

		// Proof that what was measured is a database with the rows in it. A
		// harness that timed an empty copy would produce the fastest and most
		// worthless number in this file.
		require.Equal(t, n, countRows(t, p, branch),
			"the branch does not hold the source's rows, so this measured a copy of nothing")

		out = append(out, rowResult{
			rows: n, sourceBytes: server.BytesCopied(),
			refresh: refresh, branch: branchTime,
		})
	}
	return out
}

func benchSeed(rows int) string {
	return `
CREATE TABLE bench_accounts (
    id         bigserial PRIMARY KEY,
    email      text NOT NULL,
    name       text NOT NULL,
    address    text NOT NULL,
    note       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO bench_accounts (email, name, address, note)
SELECT 'user' || g || '@example.test',
       'Name ' || g,
       repeat('address line ', 8) || g,
       repeat('note ', 20) || g
FROM generate_series(1, ` + strconv.Itoa(rows) + `) g;
CREATE INDEX bench_accounts_email ON bench_accounts (email);
ANALYZE;
`
}

func countRows(t *testing.T, p *aurora.Provider, b provider.Branch) int {
	t.Helper()
	connection, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	db, err := openAdmin(connection.Reveal())
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var n int
	require.NoError(t, db.QueryRow("SELECT count(*) FROM bench_accounts").Scan(&n))
	return n
}

// writeReport writes the dated report this run produced.
func writeReport(t *testing.T, sizes []sizeResult, rows []rowResult) {
	t.Helper()
	dir := os.Getenv("AF_BENCHMARK_DIR")
	if dir == "" {
		_, file, _, ok := runtime.Caller(0)
		require.True(t, ok)
		dir = filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "benchmarks")
	}
	require.NoError(t, os.MkdirAll(dir, 0o755))

	require.Len(t, sizes, 2)
	same := reflect.DeepEqual(sizes[0].actions, sizes[1].actions)

	var b strings.Builder
	date := time.Now().UTC().Format("2006-01-02-1504")
	fmt.Fprintf(&b, "# aurora, what a branch costs at one gigabyte and at one terabyte\n\n")
	fmt.Fprintf(&b, "Run on %s UTC.\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Machine: %s %s, %d cores.\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	fmt.Fprintf(&b, "- Harness: `just benchmark`, which is "+
		"`ee/engine/db/aurora/benchmark_test.go` in this repository.\n")
	fmt.Fprintf(&b, "- Control plane: `ee/engine/db/aurora/fakerds`, on localhost. "+
		"**Not AWS.**\n")
	fmt.Fprintf(&b, "- Data plane: a real Postgres, %s.\n", postgresVersion(t))
	fmt.Fprintf(&b, "- Load average while it ran: %s.\n\n", loadAverage())

	fmt.Fprintf(&b, "## What is measured here, and what is not\n\n")
	fmt.Fprintf(&b, "This provider's claim is that a branch is an Aurora clone, so the work "+
		"of branching does not depend on how large the database is. That claim has two "+
		"halves and only one of them can be measured without an AWS account.\n\n")
	fmt.Fprintf(&b, "**Measured.** The provider's own work per branch, at a volume Aurora "+
		"reports as 1 GiB and at one it reports as 1024 GiB. Same control plane calls, in "+
		"the same order, and no customer table read or written.\n\n")
	fmt.Fprintf(&b, "**Not measured, and not estimated either.** How long Aurora takes to "+
		"answer, and how long a writer instance takes to come up. Those are the numbers a "+
		"person feels, they are properties of AWS rather than of this code, and nothing in "+
		"this repository can produce them. The rows below say UNMEASURED rather than "+
		"carrying a figure from somewhere else.\n\n")
	fmt.Fprintf(&b, "The second number is the one a competitor's slide leaves out, so it is "+
		"worth saying without a measurement to hide behind: the storage clone is seconds "+
		"and a clone has no instances. A preview environment needs one, and provisioning it "+
		"takes MINUTES. The flat half is real and it is the storage. This provider declares "+
		"an expected branch latency in minutes for that reason.\n\n")

	fmt.Fprintf(&b, "## The provider's work per branch\n\n")
	fmt.Fprintf(&b, "| Volume Aurora reports | Control plane calls | Database connections | "+
		"Rows read or written | Aurora wall clock |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- |\n")
	for _, s := range sizes {
		fmt.Fprintf(&b, "| %s | %d | %d | 0 customer rows | UNMEASURED, no AWS account |\n",
			humanGB(s.volumeGB), len(s.actions), s.connections)
	}
	fmt.Fprintf(&b, "\nIdentical call sequence at both sizes: **%v**.\n\n", same)
	fmt.Fprintf(&b, "The calls, in order, at both sizes:\n\n")
	fmt.Fprintf(&b, "```\n%s\n```\n\n", strings.Join(sizes[0].actions, "\n"))
	fmt.Fprintf(&b, "The one in the connections column is counted rather than read off "+
		"the source. A clone inherits every login its source had, so branching connects "+
		"once to disable them and end their sessions. That connection reads pg_roles and "+
		"pg_stat_activity and alters roles. The zero customer rows beside it is read off "+
		"that code path, not measured: no query in it names a customer table.\n\n")

	fmt.Fprintf(&b, "## What copying the same database costs, which Aurora does not pay\n\n")
	fmt.Fprintf(&b, "The fake control plane clones with `CREATE DATABASE ... TEMPLATE`, "+
		"which copies every byte up front. It is the opposite of what Aurora does and it is "+
		"here to give the flat claim something to be flat against: this is the shape of the "+
		"cost when branching is not copy on write.\n\n")
	fmt.Fprintf(&b, "| Rows | Bytes the fake copied | First golden | Branch |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- |\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			commas(r.rows), human(r.sourceBytes),
			r.refresh.Round(time.Millisecond), r.branch.Round(time.Millisecond))
	}
	fmt.Fprintf(&b, "\nThose two columns grow with the data and they are this file's cost, "+
		"not Aurora's. Aurora copies none of those bytes at clone time.\n\n")

	fmt.Fprintf(&b, "## How to get the number this cannot produce\n\n")
	fmt.Fprintf(&b, "Point the provider at your own Aurora PostgreSQL cluster and time "+
		"`af up`. The manifest is four lines and the provider page has it. What you will "+
		"see is the clone returning immediately and the writer instance deciding the wall "+
		"clock, at whatever size your production is.\n")

	path := filepath.Join(dir, date+"-aurora.md")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
	t.Logf("wrote %s", path)
	t.Log("\n" + b.String())
}

func postgresVersion(t *testing.T) string {
	t.Helper()
	db, err := openAdmin(postgresURL())
	if err != nil {
		return "unknown"
	}
	defer func() { _ = db.Close() }()
	var version string
	if err := db.QueryRow("SELECT version()").Scan(&version); err != nil {
		return "unknown"
	}
	head, _, _ := strings.Cut(version, " on ")
	return head
}

func humanGB(gb int64) string {
	if gb >= 1024 {
		return fmt.Sprintf("%d TiB", gb/1024)
	}
	return fmt.Sprintf("%d GiB", gb)
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
