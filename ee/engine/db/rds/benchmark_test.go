// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package rds_test

// The measurement this provider owes, and the harness that produces it.
//
// The rule it is written to: no number is quotable unless the harness that
// produced it is in this repository, the methodology is published beside it,
// and a customer can run it against their own stack and get their own number.
// That is why this is a test in the tree rather than a figure in a document.
//
// AND THE RULE THAT BITES HARDEST HERE. The wave asks every provider for two
// numbers, time to the first golden per 100 GB and time to branch, at a small
// database and a large one. No test may need a cloud account, and a wall clock
// for an RDS snapshot and restore can only be measured on RDS. So this harness
// measures what is a property of the code and REFUSES to print what is a
// property of AWS. Every cell that would be an RDS wall clock says UNMEASURED
// and says why. A figure computed around something nobody measured is an upper
// bound wearing the clothes of an answer.
//
// THE HALF THAT CAN BE MEASURED IS WORTH HAVING, AND IT IS THE UNFLATTERING
// HALF. A branch's control plane work here is IDENTICAL at twenty gibibytes
// and at a tebibyte: one restore, one wait, one rotation. The DATA work is not,
// and this harness shows it directly by branching goldens of two real sizes and
// reporting how the wall clock moved. That is the shape of the cost when
// branching is not copy on write, and it is why this provider declares
// CopyOnWrite false and goes into the wave's comparison table with minutes
// rather than seconds. A company on plain RDS has to learn that here rather
// than from their own trial.
//
//	AF_BENCHMARK=1 go test ./db/rds -run TestBenchmark -v
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

	"github.com/antifailure/antifailure/ee/engine/db/rds"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// The two declared storage sizes the control plane claim is made across.
// Twenty gibibytes is the smallest allocation a general purpose RDS instance
// takes; a tebibyte is a size an enterprise Postgres actually is.
const (
	smallStorageGB int64 = 20
	largeStorageGB int64 = 1024
)

func TestBenchmarkWhatABranchCosts(t *testing.T) {
	if os.Getenv("AF_BENCHMARK") == "" {
		t.Skip("skipped: set AF_BENCHMARK=1 to run the benchmark. It creates and drops " +
			"databases sized by AF_BENCHMARK_ROWS, which is slow on purpose")
	}
	if real := os.Getenv("AF_RDS_BENCHMARK_ENDPOINT"); real != "" {
		// Refused rather than quietly measuring the fake and labelling it RDS.
		// This harness would run against a real endpoint unchanged; what it
		// will not do is take a number from one control plane and print it
		// under the other one's name.
		t.Fatalf("AF_RDS_BENCHMARK_ENDPOINT is set to %q. Running this against a real "+
			"account needs credentials, a source instance and a budget, and none of them "+
			"is in this repository. Unset it to measure what can be measured here, which "+
			"is the provider's own work rather than RDS's clock", real)
	}
	ctx := context.Background()

	sizes := []sizeResult{
		measureAtStorage(t, ctx, smallStorageGB),
		measureAtStorage(t, ctx, largeStorageGB),
	}
	rows := measureCopyCost(t, ctx)
	writeReport(t, sizes, rows)
}

// sizeResult is one row of the first table: what a branch cost the provider at
// a declared storage size.
type sizeResult struct {
	storageGB int64
	// actions is every control plane call the branch made, in order.
	actions []string
	// connections is how many times the provider opened the database while
	// branching, counted at the one SQL path it has. It is one: the step that
	// closes the logins a restore inherits from production.
	connections int
}

func measureAtStorage(t *testing.T, ctx context.Context, gb int64) sizeResult {
	t.Helper()
	server := newFake(t, benchSeed(1), "")
	server.SetStorage(sourceInstance, gb)

	// Branching connects once, to close the logins a restore inherits from its
	// source. That is counted rather than read off the code: the one SQL path
	// the provider has opens its own handle, so distinct handles are distinct
	// connections. The catalog is the fixture's scoped one, because the
	// production query would reach for every role on the shared server.
	p, err := scopedNew(ctx, options(t, server))
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() })
	var mu sync.Mutex
	counting := false
	handles := map[*sql.DB]bool{}
	rds.SetLoginCatalogForTest(p, func(ctx context.Context, db *sql.DB) ([]string, error) {
		mu.Lock()
		if counting {
			handles[db] = true
		}
		mu.Unlock()
		return scopedLogins(ctx, db)
	})

	version, err := p.RefreshGolden(ctx, benchSpec(fmt.Sprintf("%08x", gb)))
	require.NoError(t, err)
	require.Equal(t, gb*(1<<30), version.SizeBytes)

	server.Reset()
	mu.Lock()
	counting = true
	mu.Unlock()
	b, err := p.Branch(ctx, version.ID, "env_benchmark")
	require.NoError(t, err)
	require.NotEmpty(t, b.ProviderRef)
	mu.Lock()
	opened := len(handles)
	counting = false
	mu.Unlock()

	// The branch is usable, which is what stops the count above describing a
	// branch that never finished.
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	require.NoError(t, err)
	require.NoError(t, reachable(conn.Reveal()))
	require.Equal(t, 1, opened,
		"branching did not connect exactly once to close the logins the restore inherited")

	return sizeResult{storageGB: gb, actions: server.Actions(), connections: opened}
}

// rowResult is one row of the second table: what the real bytes cost.
type rowResult struct {
	rows        int
	sourceBytes int64
	refresh     time.Duration
	branch      time.Duration
	copied      int64
}

// measureCopyCost is the half the plan calls unflattering, and it is the half
// a buyer on plain RDS needs.
//
// It builds a golden from a source of a real size and branches it, at two row
// counts, and reports the wall clock at each. Against this control plane the
// copy is a local CREATE DATABASE TEMPLATE rather than a hydration from S3, so
// the SECONDS are this machine's and not RDS's. What is not this machine's is
// the SHAPE: both numbers grow with the data, because a snapshot restore
// copies every byte, and no amount of AWS makes a copy free.
func measureCopyCost(t *testing.T, ctx context.Context) []rowResult {
	t.Helper()
	counts := []int{20000, 200000}
	if raw := os.Getenv("AF_BENCHMARK_ROWS"); raw != "" {
		counts = nil
		for _, field := range strings.Split(raw, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(field))
			require.NoError(t, err, "AF_BENCHMARK_ROWS is not a list of numbers")
			counts = append(counts, n)
		}
	}

	var out []rowResult
	for _, n := range counts {
		server := newFake(t, benchSeed(n), "")
		p := newProvider(t, server)
		database, ok := server.DatabaseOf(sourceInstance)
		require.True(t, ok)

		size := databaseSize(t, server.AdminURLFor(database))

		server.Reset()
		start := time.Now()
		version, err := p.RefreshGolden(ctx, benchSpec(fmt.Sprintf("%08x", n)))
		require.NoError(t, err)
		refresh := time.Since(start)

		start = time.Now()
		b, err := p.Branch(ctx, version.ID, "env_benchmark")
		require.NoError(t, err)
		branch := time.Since(start)

		require.Equal(t, n, countBenchRows(t, p, b),
			"the branch does not hold the source's rows, so the time above measured "+
				"something other than a copy of them")

		out = append(out, rowResult{
			rows: n, sourceBytes: size, refresh: refresh, branch: branch,
			copied: server.BytesCopied(),
		})
	}
	return out
}

func benchSpec(hash string) provider.GoldenSpec {
	return provider.GoldenSpec{
		Version:    17,
		RulesHash:  hash,
		Provenance: "gp1-rds-benchmark",
		Mask:       func(context.Context, secret.Value) error { return nil },
		Verify: func(context.Context, secret.Value) (string, error) {
			return `{"scanner":"benchmark","findings":0}`, nil
		},
	}
}

func benchSeed(rows int) string {
	return `
CREATE TABLE bench_accounts (
    id      bigserial PRIMARY KEY,
    email   text NOT NULL,
    name    text NOT NULL,
    address text NOT NULL,
    note    text NOT NULL
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

func databaseSize(t *testing.T, url string) int64 {
	t.Helper()
	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	var size int64
	require.NoError(t, db.QueryRow("SELECT pg_database_size(current_database())").Scan(&size))
	return size
}

func countBenchRows(t *testing.T, p *rds.Provider, b provider.Branch) int {
	t.Helper()
	conn, err := p.ConnString(context.Background(), b, provider.ConnDirect)
	require.NoError(t, err)
	db, err := sql.Open("pgx", conn.Reveal())
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
	fmt.Fprintf(&b, "# rds, what a branch costs, and the number this cannot produce\n\n")
	fmt.Fprintf(&b, "Run on %s UTC.\n\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Machine: %s %s, %d cores.\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	fmt.Fprintf(&b, "- Harness: `ee/engine/db/rds/benchmark_test.go` in this repository.\n")
	fmt.Fprintf(&b, "- Control plane: `ee/engine/db/rds/fakerds`, on localhost. **Not AWS.**\n")
	fmt.Fprintf(&b, "- Data plane: a real Postgres, %s.\n", postgresVersion(t))
	fmt.Fprintf(&b, "- Load average while it ran: %s.\n\n", loadAverage())

	fmt.Fprintf(&b, "## The refusal, first, because it is the point\n\n")
	fmt.Fprintf(&b, "The wave asks every database provider for two numbers: time to the "+
		"first golden per 100 GB, and time to branch, at a small database and a large one. "+
		"**This harness cannot produce either of them for RDS, and it publishes the refusal "+
		"rather than an estimate.**\n\n")
	fmt.Fprintf(&b, "A branch here is `RestoreDBInstanceFromDBSnapshot`. What that costs is "+
		"AWS provisioning an instance and hydrating a volume from a snapshot in S3, and "+
		"neither of those happens on this machine. Timing the fake would be timing "+
		"`CREATE DATABASE ... TEMPLATE` on one local disk and printing it under Amazon's "+
		"name. A figure computed around something nobody measured is an upper bound "+
		"wearing the clothes of an answer.\n\n")
	fmt.Fprintf(&b, "What the provider DECLARES, and undertakes to fail rather than exceed, "+
		"is an expected branch latency of **%s**. That is a ceiling in the code, asserted "+
		"by the shared conformance suite's `Branch_IsWithinTheDeclaredLatency`, so an "+
		"account that got slower fails a test instead of degrading quietly. It is not a "+
		"measurement and it is not offered as one.\n\n", rds.DefaultBranchLatency())

	fmt.Fprintf(&b, "## What IS measured, and what it is worth\n\n")
	fmt.Fprintf(&b, "**Measured.** The provider's own control plane work per branch, at a "+
		"storage size RDS reports as %d GiB and at one it reports as %d GiB, and the wall "+
		"clock of a refresh and a branch over real data of two real sizes.\n\n",
		smallStorageGB, largeStorageGB)
	fmt.Fprintf(&b, "**Not measured and not estimated.** How long RDS takes to answer, how "+
		"long an instance takes to provision, and how long a volume takes to hydrate. "+
		"Those are the numbers a person feels and they are properties of AWS.\n\n")

	fmt.Fprintf(&b, "## The provider's control plane work per branch\n\n")
	fmt.Fprintf(&b, "| Storage RDS reports | Control plane calls | Database connections | "+
		"RDS wall clock |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- |\n")
	for _, s := range sizes {
		fmt.Fprintf(&b, "| %s | %d | %d | UNMEASURED, no AWS account |\n",
			humanGB(s.storageGB), len(s.actions), s.connections)
	}
	fmt.Fprintf(&b, "\nIdentical call sequence at both sizes: **%v**.\n\n", same)
	fmt.Fprintf(&b, "The calls, in order, at both sizes:\n\n")
	fmt.Fprintf(&b, "```\n%s\n```\n\n", strings.Join(sizes[0].actions, "\n"))
	fmt.Fprintf(&b, "That row is the one thing about this provider that IS flat, and it is "+
		"worth saying precisely because it is not the thing a buyer cares about. The "+
		"control plane work does not depend on the size of the database. The RESTORE does, "+
		"and the restore is what somebody waits for.\n\n")
	fmt.Fprintf(&b, "The one in the connections column is counted rather than read off "+
		"the source. A restore inherits every login its source had, so branching connects "+
		"once to close them before the branch is handed out, and that connection reads the "+
		"role catalog and no customer table. The harness counts the distinct database "+
		"handles that step opened while branching.\n\n")

	fmt.Fprintf(&b, "## What copying the data costs, which is the half RDS charges for\n\n")
	fmt.Fprintf(&b, "The fake takes a snapshot and restores it with "+
		"`CREATE DATABASE ... TEMPLATE`, which copies every byte, exactly as many times as "+
		"RDS does: twice to build a golden and once per branch. The SECONDS below are this "+
		"machine's and not RDS's. The SHAPE is the point: both columns grow with the data, "+
		"because a snapshot restore is a copy and no amount of AWS makes a copy free.\n\n")
	fmt.Fprintf(&b, "| Rows | Source size | First golden | Branch | Bytes copied |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- |\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s (local) | %s (local) | %s |\n",
			commas(r.rows), human(r.sourceBytes),
			r.refresh.Round(time.Millisecond), r.branch.Round(time.Millisecond),
			human(r.copied))
	}
	fmt.Fprintf(&b, "\nEvery number in that table is labelled local and none of them is an "+
		"RDS number.\n\n")

	fmt.Fprintf(&b, "## How to get the number this cannot produce\n\n")
	fmt.Fprintf(&b, "Point the provider at your own RDS for PostgreSQL instance and time "+
		"`af up`. The manifest is four lines. What you will see is a snapshot, then an "+
		"instance provisioning, then a volume hydrating, and the wall clock will be "+
		"minutes at any size and more than that at a large one. That is the honest "+
		"position for this provider: if the number matters more than the price, the "+
		"aurora provider clones instead of copying, and its row in the comparison table "+
		"is the one to read.\n")

	path := filepath.Join(dir, date+"-rds.md")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
	t.Logf("wrote %s", path)
	t.Log("\n" + b.String())
}

func postgresVersion(t *testing.T) string {
	t.Helper()
	db, err := sql.Open("pgx", postgresURL())
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
