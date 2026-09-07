package fidelity_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The number this lane owes: the fidelity score before and after, on the same
// environment.
//
// The rule it is written to, from the plan: no number is quotable unless the
// harness that produced it is in this repository, the methodology is published
// beside it, and a customer can run it against their own stack and get their
// own number. So the environment is a manifest anybody can read, the
// observation is written out rather than assembled by a helper chain, and
// `just benchmark` writes a dated report.
//
// HOW THE BEFORE NUMBER WAS PRODUCED, because a before number computed by the
// after code is a simulation of the old instrument rather than a measurement
// of it. testdata/score-before-c2233c50.txt is the report the instrument on
// main printed for this exact observation, written by running the recorder
// below in a worktree at c2233c50 with -score-out. Nothing on this branch can
// produce that file: guardBefore asserts it carries no topology dimension,
// which the instrument here always emits, so a rerun of the recorder on this
// branch fails rather than quietly restating the after number as the before.

var scoreOut = flag.String("score-out", "",
	"write the reports for the analytics twin into this directory, for recording what an older instrument printed")

// The two files the recorder writes, and the two the benchmark reads back.
const (
	beforeHealthy     = "score-before-c2233c50.txt"
	beforeHalfStarted = "score-before-half-started-c2233c50.txt"
)

// analyticsTwin is the environment both instruments are asked about.
//
// Everything the engine can reproduce, reproduced: four services up, the count
// each asked for actually running, a branch from a verified golden, the
// persona present. The two things it cannot are the two this lane is about,
// and they are the difference between the two numbers.
func analyticsTwin(t *testing.T) fidelity.Observation {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "product-analytics.yaml"))
	require.NoError(t, err)
	m, err := manifest.Parse(body, "antifailure.yaml", "")
	require.NoError(t, err)

	return fidelity.Observation{
		EnvID:    "product-analytics-main-7f2c1a",
		Manifest: m,
		Running: []provider.RunningService{
			{Name: "web", Kind: "web", Ready: true, URL: "http://127.0.0.1:8080", Instances: 3},
			{Name: "worker", Kind: "worker", Ready: true, Instances: 2},
			{Name: "events", Kind: "worker", Ready: true, Instances: 1},
			{Name: "cache", Kind: "worker", Ready: true, Instances: 1},
		},
		Runtime:     "the local runtime, which publishes an address this machine can reach",
		Golden:      "gv_20260830120000_abcd1234",
		Attested:    true,
		Attestation: "41 columns read back over 82000 rows sampled",
		Tables:      12,
		Rows:        184000,
		Personas: []fidelity.Persona{
			{Name: "buyer", Login: schema.LoginPassword, Present: true, Table: "public.users"},
		},
	}
}

// halfStarted is the same environment with one instance of each multi instance
// service, which is what EVERY environment looked like before c2233c50: both
// runtimes hardcoded one and a manifest asking for three silently got it.
func halfStarted(t *testing.T) fidelity.Observation {
	obs := analyticsTwin(t)
	obs.Running[0].Instances = 1
	obs.Running[1].Instances = 1
	return obs
}

// TestRecordTheScoreOnAnAnalyticsTwin writes the report for the twin.
//
// It exists to be run against an OLDER checkout with -score-out, which is how
// testdata/score-before-c2233c50.txt was made. On this branch it asserts and
// writes nothing, so a run with no flag is still a check rather than a no op.
func TestRecordTheScoreOnAnAnalyticsTwin(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		file string
		obs  fidelity.Observation
	}{
		{beforeHealthy, analyticsTwin(t)},
		{beforeHalfStarted, halfStarted(t)},
	} {
		report := fidelity.Build(c.obs).Explain()
		t.Log(c.file + "\n\n" + report)
		if *scoreOut != "" {
			require.NoError(t, os.WriteFile(filepath.Join(*scoreOut, c.file), []byte(report), 0o600))
		}
	}
}

// scoreOf renders one inventory's headline numbers.
type scoreOf struct {
	reproduced int
	counted    int
	percent    int
}

func measureScore(t *testing.T, inv fidelity.Inventory) scoreOf {
	t.Helper()
	s := inv.Score()
	pct, ok := s.Percent()
	require.True(t, ok, "nothing was counted, so there is no score to quote")
	return scoreOf{reproduced: s.Reproduced, counted: s.Counted, percent: pct}
}

// recordedBefore reads a report an older instrument printed, and refuses one
// this branch could have written.
//
// The guard is the point. A before number produced by the after code is a
// simulation of the old instrument rather than a measurement of it, and the
// difference is invisible in the file: both are a rendered report. Every
// inventory this build produces carries a topology dimension, always, in
// AllFidelityDimensions order, so a file without one cannot have come from
// here. Rerunning the recorder on this branch overwrites the file and fails
// this check on the next run rather than quietly restating the after number.
func recordedBefore(t *testing.T, file string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", file))
	require.NoErrorf(t, err, "%s is missing, so there is no before number to compare against", file)
	text := string(body)
	require.NotContainsf(t, text, string(schema.FidelityTopology),
		"%s names the topology dimension, so it was written by this build rather than by the one it claims to record", file)
	require.Containsf(t, text, string(schema.FidelityDatastores),
		"%s has no datastores dimension, so it predates the instrument this lane changed rather than being the one before it", file)
	return text
}

// headlineOf pulls the one line a salesperson would say out loud.
func headlineOf(t *testing.T, report string) string {
	t.Helper()
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(line, "measured components are production's own") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("the recorded report carries no headline:\n%s", report)
	return ""
}

// TestBenchmarkTheFidelityScoreBeforeAndAfter is the second half of
// `just benchmark`.
//
// A SCORE THAT GOES DOWN IS THE DELIVERABLE. No competitor ships a product
// that tells you it is not good enough yet, and this is the number that does.
// Both rows below scored 100 percent on the instrument as it stood at
// c2233c50, and the second of them is the environment every user of this
// engine had until that commit landed: a manifest asking for three instances
// while the runtime hardcoded one.
func TestBenchmarkTheFidelityScoreBeforeAndAfter(t *testing.T) {
	t.Parallel()

	healthy := measureScore(t, fidelity.Build(analyticsTwin(t)))
	half := measureScore(t, fidelity.Build(halfStarted(t)))

	beforeHealthyText := recordedBefore(t, beforeHealthy)
	beforeHalfText := recordedBefore(t, beforeHalfStarted)

	// The before number, quoted from the file rather than restated here, so
	// that a change to the recording cannot leave this assertion agreeing with
	// a number nobody measured.
	require.Contains(t, beforeHealthyText,
		"7 of 7 measured components are production's own, which is 100 percent.")
	require.Contains(t, beforeHalfText,
		"7 of 7 measured components are production's own, which is 100 percent.",
		"an environment running one of three instances scored anything but 100 before this lane, "+
			"which would mean the instrument could already see the gap")

	// The after numbers. Written out rather than derived, because a benchmark
	// that computes both sides with the same code proves only that the code
	// agrees with itself.
	require.Equal(t, scoreOf{reproduced: 9, counted: 10, percent: 90}, healthy)
	require.Equal(t, scoreOf{reproduced: 7, counted: 10, percent: 70}, half)

	// The drop is the deliverable, so it is asserted rather than merely
	// printed. A future change that makes either environment score its old
	// 100 again fails here.
	require.Less(t, healthy.percent, 100)
	require.Less(t, half.percent, healthy.percent,
		"running one of three instances must score below running three of three")

	// And the two things it names, by name, in the words somebody has to act
	// on. A number that drops without saying which component dropped it is a
	// worse report than the one it replaced.
	events := componentState(t, fidelity.Build(analyticsTwin(t)), schema.FidelityDatastores, "events")
	require.Equal(t, fidelity.Absent, events.State)
	require.Contains(t, events.Detail, "no golden, no attestation, no tables and no rows")

	web := componentState(t, fidelity.Build(halfStarted(t)), schema.FidelityTopology, "web")
	require.Equal(t, fidelity.Absent, web.State)
	require.Contains(t, web.Detail, "1 of 3 instances")

	report := renderScoreBenchmark(
		headlineOf(t, beforeHealthyText), healthy,
		headlineOf(t, beforeHalfText), half)
	t.Log("\n" + report)
	if out := os.Getenv(scoreBenchmarkOutEnv); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o750))
		require.NoError(t, os.WriteFile(out, []byte(report), 0o600))
	}
}

// scoreBenchmarkOutEnv names the file the report is written to. Unset, the
// test still runs and still asserts; it just does not write.
const scoreBenchmarkOutEnv = "AF_SCORE_BENCHMARK_OUT"

// renderScoreBenchmark writes the report, dated, because a number older than
// the code that produced it is withdrawn rather than rounded.
func renderScoreBenchmark(beforeOne string, one scoreOf, beforeTwo string, two scoreOf) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# The fidelity score, before and after\n\nMeasured %s by\n",
		time.Now().UTC().Format("2006-01-02"))
	b.WriteString("`go test ./internal/fidelity -run TestBenchmarkTheFidelityScoreBeforeAndAfter`,\n")
	b.WriteString("from engine/internal/fidelity/score_test.go, on the manifest in\n")
	b.WriteString("engine/internal/fidelity/testdata/product-analytics.yaml: a web tier, a\n")
	b.WriteString("worker, the primary Postgres, a ClickHouse holding every event and a Redis\n")
	b.WriteString("declared empty on purpose.\n\n")
	b.WriteString("BEFORE is the report the instrument printed at c2233c50, recorded in\n")
	b.WriteString("engine/internal/fidelity/testdata/ by running the recorder in this file\n")
	b.WriteString("against a worktree at that commit. AFTER is this build, on the same\n")
	b.WriteString("observation, byte for byte the same manifest.\n\n")

	b.WriteString("| Environment | Before | After |\n| --- | --- | --- |\n")
	fmt.Fprintf(&b, "| Everything the engine can start, started | %s | %s |\n",
		quoteScore(beforeOne), renderScore(one))
	fmt.Fprintf(&b, "| The same, running one instance of each multi instance service | %s | %s |\n",
		quoteScore(beforeTwo), renderScore(two))

	b.WriteString("\nThe second row is the environment every user of this engine had until\n")
	b.WriteString("c2233c50: both runtimes hardcoded one instance, so a manifest asking for\n")
	b.WriteString("three silently got one, and the report called it a faithful twin.\n\n")
	b.WriteString("What moved: the ClickHouse the manifest declares `golden` is counted as\n")
	b.WriteString("absent rather than excluded as unmeasured, because nothing here built a\n")
	b.WriteString("golden for it and that is a fact about the environment; and the topology\n")
	b.WriteString("dimension counts instances against the count each service asked for.\n\n")
	b.WriteString("What would move it back up, honestly: L4.2 filling the ClickHouse with a\n")
	b.WriteString("masked, verified copy, which is the one component holding the first row\n")
	b.WriteString("below 100.\n")
	return b.String()
}

func renderScore(s scoreOf) string {
	return fmt.Sprintf("**%d of %d, %d percent**", s.reproduced, s.counted, s.percent)
}

// quoteScore trims the recorded headline down to the part that is a number.
func quoteScore(headline string) string {
	if i := strings.Index(headline, " measured components"); i > 0 {
		rest := headline[i:]
		if j := strings.Index(rest, "which is "); j > 0 {
			pct := strings.TrimSuffix(strings.TrimSpace(rest[j+len("which is "):]), ".")
			if k := strings.Index(pct, "."); k > 0 {
				pct = pct[:k]
			}
			return fmt.Sprintf("%s, %s", headline[:i], pct)
		}
	}
	return headline
}
