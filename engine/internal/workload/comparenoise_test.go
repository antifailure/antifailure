package workload_test

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// How small a regression this instrument can actually resolve.
//
// This exists because the first version of the identical build arm beside it
// FAILED. Two servers running identical code, sent the same requests under the
// same seed, reported a p95 79.9 percent apart over a two second run. Nothing
// was wrong with the comparison: that is what a tail percentile over a few
// hundred samples on a shared laptop does. A default threshold of 0.25 would
// have failed both of those builds, and a check that fails two identical
// builds is one people turn off, which is worse than not shipping it.
//
// So the noise floor is a number this product measures rather than assumes. It
// is guarded by an environment variable because it deliberately runs for
// minutes and its output is a measurement rather than an assertion: on a
// different machine the numbers differ, and that is the point. What it asserts
// is only the thing that must hold everywhere: that a longer run is quieter
// than a short one, so the advice the documentation gives is true.
//
//	AF_NOISE_FLOOR=1 go test ./internal/workload -run TestNoiseFloor -v
//
// The lesson it produced is in concepts/load: measure your own floor before
// trusting a threshold near it.

type spread struct {
	duration  time.Duration
	p95Ratios []float64
	drops     []float64
}

func worst(vs []float64) float64 {
	m := 0.0
	for _, v := range vs {
		if a := math.Abs(v); a > m {
			m = a
		}
	}
	return m
}

func median(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	s := append([]float64(nil), vs...)
	for i := range s {
		s[i] = math.Abs(s[i])
	}
	sort.Float64s(s)
	return s[len(s)/2]
}

func TestNoiseFloorOfTheIdenticalBuildComparison(t *testing.T) {
	if os.Getenv("AF_NOISE_FLOOR") == "" {
		t.Skip("set AF_NOISE_FLOOR=1 to measure this machine's floor; it runs for minutes")
	}
	const repeats = 5
	durations := []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}

	// The conditions, printed with the numbers rather than left to a reader to
	// assume. A floor measured on a machine running six other builds is that
	// machine's floor and an upper bound on anybody else's, and a figure
	// reported without the load it was taken under is the kind of
	// unprovenanced number that devalues every number beside it.
	t.Logf("conditions: %d cores, load average %s, container virtualisation %s of one core",
		runtime.NumCPU(), loadAverage(), virtualisationCPU())

	results := make([]spread, 0, len(durations))
	for _, d := range durations {
		s := spread{duration: d}
		for i := 0; i < repeats; i++ {
			// Two servers with identical behaviour. Any difference between
			// them is the machine, not the code.
			base := buildServer(t, map[string]time.Duration{"/orders": 5 * time.Millisecond})
			cand := buildServer(t, map[string]time.Duration{"/orders": 5 * time.Millisecond})
			baseRes := sendMixFor(t, base.URL, 20, d)
			candRes := sendMixFor(t, cand.URL, 20, d)
			c := compareSides(t, baseRes, candRes)
			for _, r := range c.Routes {
				if r.P95Ratio != nil {
					s.p95Ratios = append(s.p95Ratios, *r.P95Ratio)
				}
			}
			if baseRes.Rate > 0 {
				s.drops = append(s.drops, (baseRes.Rate-candRes.Rate)/baseRes.Rate)
			}
		}
		results = append(results, s)
		t.Logf("%-5s over %d runs: worst p95 difference %+.1f%%, median %.1f%%; "+
			"worst throughput difference %+.1f%%, median %.1f%%; load average %s",
			d, repeats, worst(s.p95Ratios)*100, median(s.p95Ratios)*100,
			worst(s.drops)*100, median(s.drops)*100,
			loadAverage()+", virtualisation "+virtualisationCPU())
	}

	fmt.Println(noiseAdvice(results))

	// The one thing that must hold on any machine: more samples is quieter.
	// If this ever fails, the advice in concepts/load is wrong and the
	// defaults were chosen against a floor that does not exist.
	shortest, longest := results[0], results[len(results)-1]
	require.Less(t, median(longest.p95Ratios), median(shortest.p95Ratios),
		"a longer run must resolve a smaller regression than a short one, "+
			"or the advice to lengthen the run is false")
}

func noiseAdvice(rs []spread) string {
	out := fmt.Sprintf(
		"\nthe smallest p95 regression THIS machine, at load average %s over %d cores,\n"+
			"can resolve, per run length. A quieter machine resolves a smaller one:\n",
		loadAverage(), runtime.NumCPU())
	for _, s := range rs {
		out += fmt.Sprintf("  %-5s  anything under about %.0f%% is indistinguishable from noise\n",
			s.duration, worst(s.p95Ratios)*100)
	}
	return out
}

// loadAverage reads the machine's one, five and fifteen minute load, so every
// cell above carries the contention it was measured under.
func loadAverage() string {
	out, err := exec.Command("uptime").Output()
	if err != nil {
		return "unknown"
	}
	line := string(out)
	if i := strings.LastIndex(line, ": "); i >= 0 {
		return strings.TrimSpace(line[i+2:])
	}
	return "unknown"
}

// virtualisationCPU is what the container VM is taking while this measures.
//
// Recorded beside the load average because on a laptop the two say different
// things. Load average counts everything; this one number says whether the
// thing competing for the cores is the container runtime, which is what a
// machine running other people's environments looks like, and it is the
// difference between a figure that describes this product's instrument and one
// that describes a busy afternoon.
func virtualisationCPU() string {
	out, err := exec.Command("ps", "-eo", "pcpu,comm").Output()
	if err != nil {
		return "unknown"
	}
	total := 0.0
	found := false
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "Virtualization") && !strings.Contains(line, "docker") {
			continue
		}
		var pct float64
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "%f", &pct); err == nil {
			total += pct
			found = true
		}
	}
	if !found {
		return "none"
	}
	return fmt.Sprintf("%.0f%%", total)
}
