package env

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/load"
	"github.com/antifailure/antifailure/engine/internal/traffic"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// What this repository's own manifest tells the fidelity report about its own
// traffic, before and after.
//
// The before is recorded rather than reasoned about. trafficOut writes the
// sentence the instrument produced, and testdata/traffic-before-bfa35d94.txt
// is the file that came out of running this recorder in a worktree checked out
// at bfa35d94, which is main before this lane. Nothing on this branch can
// write that file: the guard below refuses one carrying this build's wording,
// so a rerun of the recorder here fails loudly rather than restating the after
// sentence as the before.

var trafficOut = flag.String("traffic-out", "",
	"write the traffic sentence for this repository's own manifest into this directory, "+
		"for recording what an older instrument printed")

// trafficBeforeFile is the recorded sentence, and the commit it was recorded
// at.
const (
	trafficBeforeFile   = "traffic-before-bfa35d94.txt"
	trafficBeforeCommit = "bfa35d94"
)

// ownLoadBlock is this repository's own load configuration, copied from
// antifailure.yaml.
//
// Copied rather than read, so that a change to the manifest cannot silently
// change what the recorded before number was measured on. The four routes are
// the four the plan calls "4 hand written routes out of an unknown total", and
// source: none with scale 1 is exactly what that file says.
func ownLoadBlock() *schema.Load {
	return &schema.Load{
		Enabled:  true,
		Source:   schema.LoadNone,
		Scale:    1,
		Duration: "30s",
		SafeRoutes: []string{
			"GET /environments",
			"GET /runs",
			"GET /audit",
			"GET /network",
		},
		UnsafeRoutes: []string{"POST /auth/email", "POST /trpc/*"},
	}
}

// TestRecordTheTrafficObservationForThisRepository writes what the fidelity
// report was told about this repository's own traffic.
//
// It exists to be run against an OLDER checkout with -traffic-out, which is
// how testdata/traffic-before-bfa35d94.txt was made. It uses nothing this lane
// added, so it compiles and runs on both trees. On this branch it asserts
// nothing about the wording and writes nothing without the flag, so a run with
// no flag is still an exercise of the path rather than a no op.
func TestRecordTheTrafficObservationForThisRepository(t *testing.T) {
	o := loadOrchestrator(t, t.TempDir(), ownLoadBlock())
	obs := fidelity.Observation{Manifest: o.opts.Manifest}
	o.observeTraffic(&obs)
	line := obs.Traffic
	if line == "" {
		line = "unmeasured: " + obs.TrafficReason
	}
	t.Log(trafficBeforeFile + "\n\n" + line)
	if *trafficOut != "" {
		require.NoError(t, os.WriteFile(
			filepath.Join(*trafficOut, trafficBeforeFile), []byte(line+"\n"), 0o600))
	}
}

// The recorded sentence is the defect in one line: four routes somebody wrote
// by hand, reported as read from a source whose name is missing because there
// was no source.
func TestTheTrafficSentenceNamedNoSourceAtAll(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", trafficBeforeFile))
	require.NoErrorf(t, err, "%s is missing, so there is no before sentence to compare against",
		trafficBeforeFile)
	before := string(body)

	require.Contains(t, before, "4 routes read from , at 5 requests a second",
		"the recorded sentence does not carry the empty source name this lane exists to fix")
	require.NotContains(t, before, "written by hand",
		"%s carries this build's wording, so it was written here rather than at %s",
		trafficBeforeFile, trafficBeforeCommit)

	// And what this build says for the same manifest. The routes are still
	// four and still hand written; the sentence now says so rather than
	// naming a source that does not exist.
	o := loadOrchestrator(t, t.TempDir(), ownLoadBlock())
	obs := fidelity.Observation{Manifest: o.opts.Manifest}
	o.observeTraffic(&obs)
	require.Equal(t,
		"4 routes read from the routes safe_routes names, which is a list written by hand, "+
			"at 5 requests a second", obs.Traffic)
}

// The routes the report measures are the ones that would actually be sent,
// which is the shape after the safe list has refused what it refuses.
func TestSendableRoutesAreTheShapeAfterTheSafeList(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "access.log",
		`1.2.3.4 - - [01/Jun/2026:12:00:00 +0000] "GET /events HTTP/1.1" 200 12`+"\n"+
			`1.2.3.4 - - [01/Jun/2026:12:00:01 +0000] "POST /events HTTP/1.1" 200 12`+"\n")
	o := loadOrchestrator(t, root, &schema.Load{
		Enabled: true, Source: schema.LoadAccessLog,
		SourceConfig: map[string]string{"path": "access.log"},
		SafeRoutes:   []string{"GET /**"},
	})
	sent, err := o.SendableRoutes()
	require.NoError(t, err)
	require.Equal(t, []traffic.Endpoint{{Method: "GET", Path: "/events"}}, sent,
		"the write the safe list refuses was reported as a route this run sends")
}

// A profile the manifest never declared is a reason rather than an empty
// profile, because an empty one would be a denominator of zero.
func TestTrafficProfileIsAReasonWhenTheManifestDeclaresNone(t *testing.T) {
	o := loadOrchestrator(t, t.TempDir(), &schema.Load{Enabled: true})
	p, why := o.TrafficProfile()
	require.Nil(t, p)
	require.Contains(t, why, "Declare one under load.traffic")
}

// A stale profile is refused rather than quoted, the way a stale golden is.
func TestTrafficProfileRefusesAStaleOne(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	writeProfile(t, root, "traffic.json", traffic.Profile{
		CollectedAt: now.Add(-40 * 24 * time.Hour),
		From:        now.Add(-41 * 24 * time.Hour),
		To:          now.Add(-40 * 24 * time.Hour),
		Requests:    10,
		Routes:      []traffic.Route{{Method: "GET", Path: "/events", Requests: 10}},
	})
	o := trafficOrchestrator(t, root, now, &schema.Traffic{Profile: "traffic.json", MaxAge: "14d"})
	p, why := o.TrafficProfile()
	require.Nil(t, p, "a profile forty days old was quoted")
	require.Contains(t, why, "was collected 40 days ago, past the 14 days it is allowed to be")

	// And the same profile inside its age is read.
	fresh := trafficOrchestrator(t, root, now.Add(-39*24*time.Hour),
		&schema.Traffic{Profile: "traffic.json", MaxAge: "14d"})
	got, why := fresh.TrafficProfile()
	require.Empty(t, why)
	require.NotNil(t, got)
}

// Recording reads the file the manifest already names, so a project with
// source: otel does not configure the same path twice.
func TestRecordTrafficReadsTheFileTheManifestNames(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "traces.json", oneServerSpan)
	o := loadOrchestrator(t, root, &schema.Load{
		Enabled: true, Source: schema.LoadOTel,
		SourceConfig: map[string]string{"path": "traces.json"},
	})
	p, err := o.RecordTraffic("")
	require.NoError(t, err)
	require.Equal(t, "otel export traces.json", p.Source,
		"the source names a path inside the repository, never an absolute one")
	require.Len(t, p.Routes, 1)
	require.Equal(t, "GET /cart", p.Routes[0].String())
}

// A file named on the command line decides its own format, and a name that
// says nothing is refused rather than guessed at.
func TestRecordTrafficRefusesAFileWhoseFormatNothingNames(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "traffic.bin", oneServerSpan)
	o := loadOrchestrator(t, root, &schema.Load{Enabled: true})
	_, err := o.RecordTraffic("traffic.bin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "nothing in the name of traffic.bin says whether it is an")
}

// A project with no source and no file is told which of the two to give,
// rather than handed an empty profile.
func TestRecordTrafficRefusesWhenNothingNamesASource(t *testing.T) {
	o := loadOrchestrator(t, t.TempDir(), &schema.Load{Enabled: true})
	_, err := o.RecordTraffic("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no file was given and the manifest names none")
}

// The baseline a threshold compares against comes from the profile only for
// the routes the shape could not carry one for.
func TestBaselinesFillOnlyWhatTheShapeLacks(t *testing.T) {
	shape := load.Shape{Routes: []load.Route{
		{Method: "GET", Path: "/events", P95Ms: 12},
		{Method: "GET", Path: "/runs", Weight: 1},
		{Method: "GET", Path: "/unknown", Weight: 1},
	}}
	p := &traffic.Profile{
		CollectedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Routes: []traffic.Route{
			{Method: "GET", Path: "/events", Requests: 100, P95Ms: 900},
			{Method: "GET", Path: "/runs", Requests: 100, P95Ms: 41},
		},
	}
	got, filled := withProfileBaselines(shape, p)
	require.Equal(t, 1, filled)
	require.Equal(t, 12.0, got.Routes[0].P95Ms,
		"a baseline measured in the same file the traffic came from was overwritten by an older one")
	require.Equal(t, 41.0, got.Routes[1].P95Ms)
	require.Zero(t, got.Routes[2].P95Ms,
		"a route the profile does not name was given a baseline from somewhere")
	require.Contains(t, baselineNote(got, p, filled, ""),
		"1 of 3 routes take their p95 baseline from the traffic profile collected on 2026-09-01")
}

// A run with no baseline anywhere says so, because a threshold evaluated
// against nothing prints no breach and reads exactly like one that passed.
func TestBaselineNoteSaysWhenAThresholdCannotFire(t *testing.T) {
	shape := load.Shape{Routes: []load.Route{{Method: "GET", Path: "/runs", Weight: 1}}}
	got, filled := withProfileBaselines(shape, nil)
	require.Zero(t, filled)
	require.Equal(t,
		"no route has a p95 baseline, so p95_increase cannot fire: no profile is declared",
		baselineNote(got, nil, filled, "no profile is declared"))

	// And a shape that carries its own says nothing, because the source line
	// already does.
	withOwn := load.Shape{Routes: []load.Route{{Method: "GET", Path: "/runs", P95Ms: 41}}}
	require.Empty(t, baselineNote(withOwn, nil, 0, "no profile is declared"))
}

// oneServerSpan is the smallest export a reader accepts.
const oneServerSpan = `{"resourceSpans":[{"scopeSpans":[{"spans":[` +
	`{"name":"GET /cart","kind":"SPAN_KIND_SERVER","startTimeUnixNano":"1756684800000000000",` +
	`"endTimeUnixNano":"1756684800004000000"}]}]}]}`

// writeProfile writes a traffic profile into a repository root.
func writeProfile(t *testing.T, root, name string, p traffic.Profile) {
	t.Helper()
	require.NoError(t, traffic.Write(filepath.Join(root, name), p))
}

// trafficOrchestrator is loadOrchestrator with a fixed clock and a declared
// profile, which is what a staleness test needs.
func trafficOrchestrator(
	t *testing.T, root string, now time.Time, cfg *schema.Traffic,
) *Orchestrator {
	t.Helper()
	o, err := New(Options{
		Root:     root,
		Manifest: &schema.Manifest{Name: "app", Load: &schema.Load{Enabled: true, Traffic: cfg}},
		Branch:   "main",
		Clock:    clock.NewFake(now),
	})
	require.NoError(t, err)
	return o
}
