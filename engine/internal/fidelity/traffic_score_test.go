package fidelity_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/manifest"
	"github.com/antifailure/antifailure/engine/internal/traffic"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The number this lane owes: the routes in the load profile that production
// actually served, out of the routes production served.
//
// Before this lane that fraction was unknown and the report said "reproduced".
// The evidence for that sentence is a file rather than a claim:
// testdata/score-before-traffic-bfa35d94.txt is the report the instrument on
// main printed for this exact observation, written by running the recorder
// below in a worktree at bfa35d94. Nothing on this branch can produce it: this
// build always emits an arrival rate component beside the mix, and
// recordedByTheOlderTrafficInstrument refuses a file that carries one, so a
// rerun of the recorder here fails rather than quietly restating the after
// number as the before.
//
// The rule from the plan that shapes it: no number is quotable unless the
// harness is in this repository, the methodology is published beside it, and a
// customer can run it against their own stack and get their own number. So the
// production side is a committed artifact anybody can read, the run's side is
// written out route by route, and the report is dated.

const (
	trafficBeforeReport   = "score-before-traffic-bfa35d94.txt"
	trafficBeforeCommit   = "bfa35d94"
	productionTrafficFile = "production-traffic.json"
	trafficManifestFile   = "product-analytics-traffic.yaml"
)

// trafficSentenceFile is the recorded sentence the older instrument produced
// for this repository's own manifest.
//
// It lives under internal/env because that is the package that produces it,
// and it is read from here rather than copied, because the same string in two
// places is how a report ends up quoting a figure nothing measured. The env
// test beside it asserts what the file holds; this one only needs the words.
const trafficSentenceFile = "../env/testdata/traffic-before-bfa35d94.txt"

// twinSends is every route a load run would send at this environment.
//
// The four safe_routes the fixture manifest names, written out rather than
// derived, so a reader can count them: four, against the fourteen production
// serves. Concrete paths, because a generator cannot send a template, and both
// sides of the comparison collapse an identifier so they meet production's.
var twinSends = []traffic.Endpoint{
	{Method: "GET", Path: "/health"},
	{Method: "GET", Path: "/api/projects/1/dashboards"},
	{Method: "GET", Path: "/api/organizations/1/members"},
	{Method: "GET", Path: "/shared_dashboard/9f3c2a7b41e84d6a"},
}

// twinSendRate is what the run sends at: the default shape's five requests a
// second, times the manifest's scale of one.
const twinSendRate = 5.0

// trafficTwin is the environment both instruments are asked about.
//
// The same analytics stack score_test.go uses, with a load block copied from
// this repository's own antifailure.yaml. Everything the engine can reproduce
// is reproduced; the one thing it cannot is the traffic, and that is the
// difference between the two numbers.
//
// It uses no field this lane added, so it compiles and runs on the older tree,
// which is what makes the recorded before a measurement rather than a
// simulation.
func trafficTwin(t *testing.T) fidelity.Observation {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", trafficManifestFile))
	require.NoError(t, err)
	m, err := manifest.Parse(body, "antifailure.yaml", "")
	require.NoError(t, err)

	return fidelity.Observation{
		EnvID:    "product-analytics-traffic-main-7f2c1a",
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
		// What the older instrument was told about the traffic, which is the
		// sentence it printed with a verdict of reproduced beside it.
		Traffic: recordedTrafficSentence(t),
	}
}

// recordedTrafficSentence reads what the instrument at bfa35d94 said about
// this repository's own four hand written routes.
func recordedTrafficSentence(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(trafficSentenceFile)
	require.NoErrorf(t, err, "%s is missing, so nothing says what the older instrument printed",
		trafficSentenceFile)
	return strings.TrimSpace(string(body))
}

// productionTraffic is the committed profile the run is measured against.
func productionTraffic(t *testing.T) traffic.Profile {
	t.Helper()
	p, err := traffic.Read(filepath.Join("testdata", productionTrafficFile))
	require.NoError(t, err, "the committed traffic profile is missing, so there is no denominator")
	return p
}

// trafficTwinWithProfile is the same environment with the one thing this lane
// adds: something that says what production serves.
func trafficTwinWithProfile(t *testing.T) fidelity.Observation {
	t.Helper()
	obs := trafficTwin(t)
	p := productionTraffic(t)
	obs.Sent = twinSends
	obs.SentRate = twinSendRate
	obs.TrafficProfile = &p
	return obs
}

// TestRecordTheTrafficScoreOnAnAnalyticsTwin writes the report for the twin.
//
// It exists to be run against an OLDER checkout with -score-out, which is how
// testdata/score-before-traffic-bfa35d94.txt was made. On this branch it
// asserts nothing about the verdict and writes nothing without the flag.
func TestRecordTheTrafficScoreOnAnAnalyticsTwin(t *testing.T) {
	t.Parallel()
	report := fidelity.Build(trafficTwin(t)).Explain()
	t.Log(trafficBeforeReport + "\n\n" + report)
	if *scoreOut != "" {
		require.NoError(t, os.WriteFile(
			filepath.Join(*scoreOut, trafficBeforeReport), []byte(report), 0o600))
	}
}

// recordedByTheOlderTrafficInstrument reports whether a report could have come
// from the build before this lane.
//
// Two ways it could not. Every inventory this build produces carries an
// arrival rate component, always, so a file with one was written here. And a
// file with no traffic dimension at all predates the instrument this lane
// changed rather than being the one before it.
func recordedByTheOlderTrafficInstrument(name, text string) (bool, string) {
	if strings.Contains(text, "arrival rate") {
		return false, name + " names the arrival rate component, so it was written by this " +
			"build rather than by the one it claims to record"
	}
	if !strings.Contains(text, "endpoint mix") {
		return false, name + " has no endpoint mix component, so it predates " +
			trafficBeforeCommit + " rather than being the report at it"
	}
	return true, ""
}

// beforeTrafficReport reads the recorded report and refuses one this build
// could have written.
func beforeTrafficReport(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", trafficBeforeReport))
	require.NoErrorf(t, err, "%s is missing, so there is no before number to compare against",
		trafficBeforeReport)
	ok, why := recordedByTheOlderTrafficInstrument(trafficBeforeReport, string(body))
	require.True(t, ok, why)
	return string(body)
}

// The guard has to be able to refuse, or it protects nothing. Rerunning the
// recorder on this branch overwrites the fixture with this build's report,
// which would put an after number under a before heading, and nothing in the
// bytes says which build wrote it.
func TestTheGuardRefusesATrafficReportThisBuildWrote(t *testing.T) {
	t.Parallel()
	mine := fidelity.Build(trafficTwinWithProfile(t)).Explain()
	ok, why := recordedByTheOlderTrafficInstrument("a rerun", mine)
	require.False(t, ok, "the guard accepted a report this build wrote")
	require.Contains(t, why, "written by this build")

	// And with no profile, which is the other report this build can produce
	// for the same environment.
	ok, why = recordedByTheOlderTrafficInstrument("a rerun", fidelity.Build(trafficTwin(t)).Explain())
	require.False(t, ok, "the guard accepted this build's report for a twin with no profile")
	require.Contains(t, why, "written by this build")

	// And the recorded fixture passes, so the guard is not refusing everything.
	recorded, err := os.ReadFile(filepath.Join("testdata", trafficBeforeReport))
	require.NoError(t, err)
	ok, why = recordedByTheOlderTrafficInstrument(trafficBeforeReport, string(recorded))
	require.True(t, ok, why)
}

// The before instrument called four routes somebody wrote from memory a
// reproduction of production's traffic, in those words.
func TestTheInstrumentBeforeThisLaneCalledFourHandWrittenRoutesAReproduction(t *testing.T) {
	t.Parallel()
	text := beforeTrafficReport(t)
	require.Contains(t, text, "4 routes read from , at 5 requests a second",
		"the recorded before report does not carry the sentence this lane exists to change")
	require.Regexp(t, `endpoint mix\s+reproduced`, text,
		"the recorded before report does not carry the verdict this lane exists to change")
}

// TestBenchmarkTheShareOfProductionTheRunSends is this lane's half of
// `just benchmark`.
func TestBenchmarkTheShareOfProductionTheRunSends(t *testing.T) {
	t.Parallel()
	profile := productionTraffic(t)
	cov := traffic.Compare(twinSends, profile)

	share, ok := cov.Share()
	require.True(t, ok, "nothing was compared, so there is no share to quote")
	require.EqualValues(t, 145_152_200, cov.Requests)
	require.EqualValues(t, 601_000, cov.Covered)
	require.Equal(t, "0.41 percent", traffic.Percent(share))
	require.False(t, cov.Covers())
	require.Len(t, cov.Routes, 14)
	require.Len(t, cov.Uncovered(), 10)

	heaviest := cov.Uncovered()[0]
	require.Equal(t, "POST /capture", heaviest.Route.String())
	heaviestShare, ok := heaviest.Share(cov.Requests)
	require.True(t, ok)
	require.Equal(t, "63 percent", traffic.Percent(heaviestShare))

	// The verdict on the same environment, three ways. The before is read from
	// the recorded file; the two afters are this build, on the same manifest,
	// differing only in whether anything says what production serves.
	before := beforeTrafficReport(t)
	withoutProfile := measureScore(t, fidelity.Build(trafficTwin(t)))
	withProfile := measureScore(t, fidelity.Build(trafficTwinWithProfile(t)))

	require.Equal(t, scoreOf{reproduced: 8, counted: 9, percent: 89}, withoutProfile)
	require.Equal(t, scoreOf{reproduced: 8, counted: 11, percent: 72}, withProfile)

	// The components themselves, because a score that drops without saying
	// which component dropped it is a worse report than the one it replaced.
	inv := fidelity.Build(trafficTwinWithProfile(t))
	mix := componentState(t, inv, schema.FidelityTraffic, "endpoint mix")
	require.Equal(t, fidelity.Substituted, mix.State)
	require.Contains(t, mix.Detail, "this run sends 4 routes of the 14 production served")
	require.Contains(t, mix.Detail, "carrying 0.41 percent of its requests")
	require.Contains(t, mix.Detail, "The heaviest it never sends is POST /capture")
	require.Contains(t, mix.Detail, "A run cannot fail on a route it never sends")

	rateComponent := componentState(t, inv, schema.FidelityTraffic, "arrival rate")
	require.Equal(t, fidelity.Substituted, rateComponent.State)
	require.Contains(t, rateComponent.Detail, "5.0 requests a second against production's 240 requests a second")
	require.Contains(t, rateComponent.Detail, "which is 2.0 percent of it")
	require.Contains(t, rateComponent.Detail, "90 requests in flight at once at its peak")

	report := renderTrafficBenchmark(t, cov, profile, before, withoutProfile, withProfile,
		mix.Detail, rateComponent.Detail)
	t.Log("\n" + report)
	if out := os.Getenv(trafficBenchmarkOutEnv); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o750))
		require.NoError(t, os.WriteFile(out, []byte(report), 0o600))
	}
}

// trafficBenchmarkOutEnv names the file the report is written to. Unset, the
// test still runs and still asserts; it just does not write.
const trafficBenchmarkOutEnv = "AF_TRAFFIC_BENCHMARK_OUT"

// renderTrafficBenchmark writes the report, dated, because a number older than
// the code that produced it is withdrawn rather than rounded.
func renderTrafficBenchmark(
	t *testing.T, cov traffic.Coverage, profile traffic.Profile, before string,
	withoutProfile, withProfile scoreOf, mixDetail, rateDetail string,
) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# The share of production's traffic a load run actually sends\n\nMeasured %s by\n",
		time.Now().UTC().Format("2006-01-02"))
	b.WriteString("`go test ./internal/fidelity -run TestBenchmarkTheShareOfProductionTheRunSends`,\n")
	b.WriteString("from engine/internal/fidelity/traffic_score_test.go, on the manifest in\n")
	b.WriteString("engine/internal/fidelity/testdata/product-analytics-traffic.yaml against the\n")
	b.WriteString("traffic profile in engine/internal/fidelity/testdata/production-traffic.json.\n\n")
	b.WriteString("WHERE THE PROFILE CAME FROM, because a synthesised number that does not say\n")
	b.WriteString("so is worse than none. production-traffic.json is a WRITTEN fixture at the\n")
	b.WriteString("shape and scale of an analytics product's week. It is not a recording of any\n")
	b.WriteString("real production, and no traffic profile of a real production is committed in\n")
	b.WriteString("this repository. What is real is the instrument: `af traffic record` produces\n")
	b.WriteString("exactly this document from an OpenTelemetry export or an access log, which is\n")
	b.WriteString("checked by a test against the recorder's own output, and the arithmetic below\n")
	b.WriteString("is the arithmetic a customer's own export gets. Run it on yours and the table\n")
	b.WriteString("is yours.\n\n")

	fmt.Fprintf(&b, "Collected %s, over %s, %s requests.\n\n",
		profile.CollectedAt.UTC().Format("2006-01-02"),
		profile.Window().Round(time.Hour), traffic.Count(profile.Requests))
	b.WriteString("| Route | Production | Share | This run |\n| --- | --- | --- | --- |\n")
	for _, r := range cov.Routes {
		sent := "never sent"
		if r.Sent {
			sent = "sent"
		}
		s, _ := r.Share(cov.Requests)
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			r.Route, traffic.Count(r.Route.Requests), traffic.Percent(s), sent)
	}
	share, _ := cov.Share()
	fmt.Fprintf(&b, "| **all fourteen** | **%s** | **100 percent** | **%s covered** |\n\n",
		traffic.Count(cov.Requests), traffic.Percent(share))

	b.WriteString("What the fidelity report says about that traffic, on the same environment,\n")
	b.WriteString("three ways:\n\n")
	b.WriteString("| Instrument | The traffic dimension | Score |\n| --- | --- | --- |\n")
	fmt.Fprintf(&b, "| At %s, before this lane | `reproduced`, 4 routes read from a source it could not name | %s |\n",
		trafficBeforeCommit, quoteScore(headlineOf(t, before)))
	fmt.Fprintf(&b, "| This build, no traffic profile | `unmeasured`, and it says nothing compared it | %s |\n",
		renderScore(withoutProfile))
	fmt.Fprintf(&b, "| This build, with the profile | `substituted`, with the fraction | %s |\n\n",
		renderScore(withProfile))

	b.WriteString("The two sentences the last row prints, in full:\n\n> ")
	b.WriteString(mixDetail)
	b.WriteString("\n\n> ")
	b.WriteString(rateDetail)
	b.WriteString("\n\nTwo things changed at once and the table names both. The sentence gained a\n")
	b.WriteString("source it can name, because the old one read \"4 routes read from , at 5\n")
	b.WriteString("requests a second\" with nothing between the words. And the verdict gained a\n")
	b.WriteString("denominator, which is the part that matters.\n\n")
	b.WriteString("The middle row is the one worth arguing about. An environment nobody has\n")
	b.WriteString("given a profile scores HIGHER than one that has, because an unmeasured\n")
	b.WriteString("component leaves the denominator while a substituted one stays in it. That\n")
	b.WriteString("is deliberate: an unknown is not a smaller pass, and the report names the\n")
	b.WriteString("component and how to measure it rather than scoring a guess.\n\n")
	b.WriteString("Measured on this repository on 2026-09-06: a migration held\n")
	b.WriteString("AccessExclusiveLock on nine relations for thirty seconds and `af load smoke`\n")
	b.WriteString("reported 0.0 percent failed, with p95 improving from 41ms to 17ms, because\n")
	b.WriteString("none of its four hand written routes reads the locked table. The row above\n")
	b.WriteString("is that failure with a number on it.\n")
	return b.String()
}

// The committed profile has to be the shape the recorder writes, or the
// benchmark is measured against a file nothing produces.
func TestTheCommittedTrafficProfileIsWhatTheRecorderWrites(t *testing.T) {
	t.Parallel()
	p := productionTraffic(t)

	// Round tripped through the writer, so the committed bytes and the bytes
	// af traffic record produces are the same document.
	path := filepath.Join(t.TempDir(), "traffic.json")
	require.NoError(t, traffic.Write(path, p))
	written, err := os.ReadFile(path)
	require.NoError(t, err)
	committed, err := os.ReadFile(filepath.Join("testdata", productionTrafficFile))
	require.NoError(t, err)
	require.Equal(t, string(committed), string(written),
		"the committed profile is not what traffic.Write produces, so it is a file nothing writes")

	// And the totals agree with themselves, because a profile whose routes do
	// not add up to its own total is a denominator nobody can check.
	var total int64
	for _, r := range p.Routes {
		total += r.Requests
	}
	require.Equal(t, p.Requests, total,
		"the profile's routes do not add up to the total it reports")
}
