package fidelity_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The number this lane owes, measured rather than estimated.
//
// The rule it is written to, from the plan: no number is quotable unless the
// harness that produced it is in this repository, the methodology is published
// beside it, and a customer can run it against their own stack and get their
// own number. So this is a test rather than a paragraph, it reads real
// manifests rather than a scenario invented for it, and `just benchmark`
// writes a dated report.
//
// WHAT IS COUNTED, per manifest, and why each column is separate.
//
// HELD is every store the environment holds: the primary database, plus every
// datastore the manifest declares, plus every service running an image this
// build recognises as a store. It is what somebody has, and it is the same
// number before and after this change.
//
// ADDRESSABLE is how many of those the engine has a contract for. Before this
// change it was ONE, on every manifest ever written, because `database` was a
// single struct and there was no second thing to name: one golden, one masking
// pass, one verification scan, one branch. This is the lane's number.
//
// REPRODUCED is how many of them anything actually fills with production's
// data, and it is still ONE. That column is the unflattering one and it is
// published beside the other two on purpose. A number that can only come back
// flattering is marketing, and the gap between ADDRESSABLE and REPRODUCED is
// exactly the work the lanes after this one do.

// benchmarkOutEnv names the file the report is written to. Unset, the test
// still runs and still asserts; it just does not write.
const benchmarkOutEnv = "AF_BENCHMARK_OUT"

// measurement is one manifest's row.
type measurement struct {
	name        string
	path        string
	held        int
	addressable int
	before      int
	reproduced  int
	stores      []string
}

// measure counts one manifest.
//
// It counts through fidelity.Build rather than by reading the manifest struct
// directly, because Build is the instrument the product itself uses to answer
// this question. A benchmark that counted some other way could report a number
// the product does not agree with, which is the failure this repository keeps
// finding in its own instruments.
func measure(t *testing.T, path string) measurement {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	m, err := manifest.Parse(body, path, "")
	require.NoErrorf(t, err, "%s does not parse, so it cannot be measured", path)

	obs := full()
	obs.Manifest = m
	inv := fidelity.Build(obs)

	// The primary is measured by the database dimension, so it is counted here
	// rather than read out of the datastores one.
	held, reproduced := 1, 1
	var stores []string
	if d, ok := inv.Dimension(schema.FidelityDatastores); ok {
		held += len(d.Components)
		for _, c := range d.Components {
			stores = append(stores, c.Name)
		}
	}
	sort.Strings(stores)

	// What the single Database struct could name, which is what this was
	// before the datastores list existed.
	before := 0
	if m.Database != nil {
		before = 1
	}

	return measurement{
		name:        m.Name,
		path:        path,
		held:        held,
		addressable: len(m.Datastores),
		before:      before,
		reproduced:  reproduced,
		stores:      stores,
	}
}

// TestBenchmarkDatastoresPerEnvironment is `just benchmark`.
func TestBenchmarkDatastoresPerEnvironment(t *testing.T) {
	t.Parallel()

	// Four levels up from engine/internal/fidelity is the repository root.
	root := filepath.Join("..", "..", "..")
	paths := []string{
		filepath.Join(root, "antifailure.yaml"),
		filepath.Join(root, "examples", "next-app", "antifailure.yaml"),
		filepath.Join(root, "examples", "go-api", "antifailure.yaml"),
		filepath.Join(root, "examples", "django-api", "antifailure.yaml"),
		filepath.Join("testdata", "analytics-stack.yaml"),
	}

	rows := make([]measurement, 0, len(paths))
	for _, p := range paths {
		rows = append(rows, measure(t, p))
	}

	// The assertions that keep the report from being a printout.
	//
	// Every manifest written before this change addresses exactly one store,
	// which is the "was 1" the number is quoted against, and it has to keep
	// being true of them or the comparison is against something else.
	for _, r := range rows[:4] {
		require.Equalf(t, 1, r.addressable,
			"%s declares no datastores and must still address exactly the primary", r.path)
		require.Equalf(t, 1, r.before,
			"%s has no database block, so the before number is not 1 for it", r.path)
	}

	// The analytics stack is the case the wave exists for, and it is the row
	// where the two numbers separate.
	stack := rows[4]
	require.Equal(t, 4, stack.addressable,
		"the analytics stack declares three stores beside the primary")
	require.Equal(t, 4, stack.held)
	require.Equal(t, 1, stack.before,
		"before this change the engine could name one store whatever the manifest held")
	require.Equal(t, []string{"bus", "cache", "events"}, stack.stores)

	report := renderBenchmark(rows)
	t.Log("\n" + report)
	if out := os.Getenv(benchmarkOutEnv); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o750))
		require.NoError(t, os.WriteFile(out, []byte(report), 0o600))
	}
}

// renderBenchmark writes the report, dated, because a number older than the
// code that produced it is withdrawn rather than rounded.
func renderBenchmark(rows []measurement) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Datastores per environment\n\nMeasured %s by\n",
		time.Now().UTC().Format("2006-01-02"))
	b.WriteString("`go test ./internal/fidelity -run TestBenchmarkDatastoresPerEnvironment`,\n")
	b.WriteString("from engine/internal/fidelity/benchmark_test.go, over this repository's\nown manifest, the three examples, and one analytics shaped stack.\n\n")
	b.WriteString("HELD is what the environment holds. ADDRESSABLE is how many of those the\n")
	b.WriteString("engine has a contract for; it was 1 on every manifest before the datastores\n")
	b.WriteString("list existed. REPRODUCED is how many anything fills with production's data,\n")
	b.WriteString("and it is still 1.\n\n")
	b.WriteString("| Manifest | Held | Addressable, before | Addressable, now | Reproduced |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n",
			r.name, r.held, r.before, r.addressable, r.reproduced)
	}
	b.WriteString("\nStores beside the primary, per manifest:\n\n")
	for _, r := range rows {
		if len(r.stores) == 0 {
			fmt.Fprintf(&b, "- %s: none\n", r.name)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", r.name, strings.Join(r.stores, ", "))
	}
	b.WriteString("\nWhat would move the last column: L4.2 fills a ClickHouse, L4.4 makes the\n")
	b.WriteString("stances that are not clones into first class outcomes. Until then a\n")
	b.WriteString("declared store is reported unmeasured with its reason, which keeps it out\n")
	b.WriteString("of the fidelity score in both directions.\n")
	return b.String()
}
