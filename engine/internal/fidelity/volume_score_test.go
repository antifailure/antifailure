package fidelity_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/fidelity"
	"github.com/antifailure/antifailure/engine/internal/volume"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The number this lane owes: your twin holds X percent of production's rows,
// per table, stated.
//
// Before this lane that percentage was unknown and the report said
// "reproduced". The evidence for that sentence is a file rather than a claim:
// testdata/score-before-volume-d02fc3de.txt is the report the instrument on
// main printed for this exact observation, written by running the recorder in
// score_test.go in a worktree at d02fc3de with -score-out. Nothing on this
// branch can produce it: this build's data component always names the volume
// profile, and beforeVolume refuses a file that does, so a rerun of the
// recorder here fails rather than quietly restating the after number as the
// before.
//
// The rule from the plan that shapes it: no number is quotable unless the
// harness is in this repository, the methodology is published beside it, and a
// customer can run it against their own stack and get their own number. So the
// production side is a committed artifact anybody can read, the branch side is
// written out per table, and the report is dated.

// beforeVolume names the recorded report, and the commit it was recorded at.
const (
	beforeVolume          = "score-before-volume-d02fc3de.txt"
	beforeVolumeCommit    = "d02fc3de"
	productionProfileFile = "production-volume.json"
)

// twinTables is the branch's side of the comparison, per table.
//
// Written out rather than generated, so that a reader can add the column up
// and get 184,000, which is the figure analyticsTwin already reported as its
// total. Two numbers for one thing is how a report ends up quoting a total no
// row in its own table adds up to.
var twinTables = []volume.TableRows{
	{Name: "public.api_keys", Rows: 95},
	{Name: "public.audit_log", Rows: 50},
	{Name: "public.dashboards", Rows: 700},
	{Name: "public.event_props", Rows: 40_000},
	{Name: "public.events", Rows: 120_000},
	{Name: "public.experiments", Rows: 140},
	{Name: "public.feature_flags", Rows: 95},
	{Name: "public.insights", Rows: 420},
	{Name: "public.memberships", Rows: 2_400},
	{Name: "public.orgs", Rows: 1_900},
	{Name: "public.sessions", Rows: 12_000},
	{Name: "public.users", Rows: 6_200},
}

// productionVolume is the committed profile the twin is measured against.
func productionVolume(t *testing.T) volume.Profile {
	t.Helper()
	p, err := volume.Read(filepath.Join("testdata", productionProfileFile))
	require.NoError(t, err, "the committed production profile is missing, so there is no denominator")
	return p
}

// analyticsTwinWithProfile is the same environment as analyticsTwin, with the
// one thing this lane adds: something that says what production holds.
func analyticsTwinWithProfile(t *testing.T) fidelity.Observation {
	t.Helper()
	obs := analyticsTwin(t)
	p := productionVolume(t)
	obs.Branch = twinTables
	obs.Volume = &p
	return obs
}

// The branch's own numbers have to agree with themselves before any share
// computed from them means anything.
func TestTheTwinsTablesAddUpToTheTotalTheObservationReports(t *testing.T) {
	t.Parallel()
	var total int64
	for _, tbl := range twinTables {
		total += tbl.Rows
	}
	obs := analyticsTwin(t)
	require.Equal(t, obs.Rows, total,
		"the per table list and the total disagree, so any share computed from them is arithmetic on two different environments")
	require.Equal(t, obs.Tables, len(twinTables))
}

// recordedByOlderInstrument decides whether a report came from the build this
// branch replaced, and says why not when it did not.
//
// A predicate rather than a chain of assertions, because the guard is the only
// thing standing between a measured before number and a simulated one, and a
// guard that cannot be asked to say no about a specific input has never been
// shown to work. TestTheGuardRefusesAReportThisBuildWrote asks it exactly that.
//
// Two conditions, and each closes a different way of being wrong. This build's
// data component ALWAYS names the volume profile, in every arm that could read
// the branch, so a file that names it was written here. And the report has to
// carry the topology dimension, or it predates L4.7 and is the wrong before:
// it would carry a drop this branch had nothing to do with.
func recordedByOlderInstrument(file, text string) (bool, string) {
	if strings.Contains(text, "volume profile") {
		return false, file + " names the volume profile, so it was written by this build " +
			"rather than by the one it claims to record"
	}
	if !strings.Contains(text, string(schema.FidelityTopology)) {
		return false, file + " has no topology dimension, so it predates " + beforeVolumeCommit +
			" rather than being the report at it"
	}
	return true, ""
}

// beforeVolumeReport reads what the instrument at d02fc3de printed, and
// refuses a file this branch could have written.
func beforeVolumeReport(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", beforeVolume))
	require.NoErrorf(t, err, "%s is missing, so there is no before number to compare against", beforeVolume)
	ok, why := recordedByOlderInstrument(beforeVolume, string(body))
	require.True(t, ok, why)
	return string(body)
}

// The guard, pointed at the thing that would fool it.
//
// A rerun of the recorder on this branch overwrites the fixture with this
// build's own report, which is a file of exactly the same shape carrying the
// after number under the before heading. Nothing in the bytes says which build
// wrote it, so if the guard cannot refuse this it protects nothing.
func TestTheGuardRefusesAReportThisBuildWrote(t *testing.T) {
	t.Parallel()
	mine := fidelity.Build(analyticsTwinWithProfile(t)).Explain()
	ok, why := recordedByOlderInstrument("a rerun", mine)
	require.False(t, ok, "the guard accepted a report this build wrote")
	require.Contains(t, why, "written by this build")

	// And with no profile, which is the other report this build can produce
	// for the same environment.
	ok, why = recordedByOlderInstrument("a rerun", fidelity.Build(analyticsTwin(t)).Explain())
	require.False(t, ok, "the guard accepted this build's report for a twin with no profile")
	require.Contains(t, why, "written by this build")

	// A report from before the topology dimension is refused too, and for the
	// other reason. score-before-c2233c50.txt is exactly that file.
	old, err := os.ReadFile(filepath.Join("testdata", beforeHealthy))
	require.NoError(t, err)
	ok, why = recordedByOlderInstrument(beforeHealthy, string(old))
	require.False(t, ok, "a report predating the topology dimension passed as the before for this lane")
	require.Contains(t, why, "predates "+beforeVolumeCommit)

	// And the recorded fixture itself passes, so the guard is not simply
	// refusing everything.
	recorded, err := os.ReadFile(filepath.Join("testdata", beforeVolume))
	require.NoError(t, err)
	ok, why = recordedByOlderInstrument(beforeVolume, string(recorded))
	require.True(t, ok, why)
}

// The before instrument called a twin holding a fraction of a percent of
// production a reproduction of it, in those words.
func TestTheInstrumentBeforeThisLaneCalledAFractionOfAPercentAReproduction(t *testing.T) {
	t.Parallel()
	text := beforeVolumeReport(t)
	require.Contains(t, text,
		"data         reproduced   12 tables over 184000 rows, branched from gv_20260830120000_abcd1234",
		"the recorded before report does not carry the line this lane exists to change")
	require.Contains(t, text, "9 of 10 measured components are production's own, which is 90 percent.")
}

// TestBenchmarkTheShareOfProductionInTheTwin is this lane's half of
// `just benchmark`.
func TestBenchmarkTheShareOfProductionInTheTwin(t *testing.T) {
	t.Parallel()
	profile := productionVolume(t)
	cmp := volume.Compare(twinTables, profile)

	share, ok := cmp.Share()
	require.True(t, ok, "nothing was compared, so there is no share to quote")
	require.EqualValues(t, 184_000, cmp.Branch)
	require.EqualValues(t, 6_445_324_600, cmp.Production)
	require.Equal(t, "0.0028 percent", volume.Percent(share))
	require.False(t, cmp.Reproduces())

	worst, found := cmp.Smallest()
	require.True(t, found)
	require.Equal(t, "public.audit_log", worst.Name)
	worstShare, ok := worst.Share()
	require.True(t, ok)
	require.Equal(t, "0.000039 percent", volume.Percent(worstShare))

	// The verdict on the same environment, three ways. The before is read from
	// the recorded file; the two afters are this build, on the same manifest,
	// differing only in whether anything says what production holds.
	before := beforeVolumeReport(t)
	withoutProfile := measureScore(t, fidelity.Build(analyticsTwin(t)))
	withProfile := measureScore(t, fidelity.Build(analyticsTwinWithProfile(t)))

	require.Equal(t, scoreOf{reproduced: 8, counted: 9, percent: 89}, withoutProfile)
	require.Equal(t, scoreOf{reproduced: 8, counted: 10, percent: 80}, withProfile)

	// The component itself, because a score that drops without saying which
	// component dropped it is a worse report than the one it replaced.
	data := componentState(t, fidelity.Build(analyticsTwinWithProfile(t)), schema.FidelityDatabase, "data")
	require.Equal(t, fidelity.Substituted, data.State)
	require.Contains(t, data.Detail, "184,000 rows against production's 6,445,324,600 rows")
	require.Contains(t, data.Detail, "0.0028 percent")
	require.Contains(t, data.Detail, "Its smallest table is public.audit_log, at 0.000039 percent")
	require.Contains(t, data.Detail, "lower bound and not a prediction")

	report := renderVolumeBenchmark(t, cmp, before, withoutProfile, withProfile, data.Detail)
	t.Log("\n" + report)
	if out := os.Getenv(volumeBenchmarkOutEnv); out != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o750))
		require.NoError(t, os.WriteFile(out, []byte(report), 0o600))
	}
}

// volumeBenchmarkOutEnv names the file the report is written to. Unset, the
// test still runs and still asserts; it just does not write.
const volumeBenchmarkOutEnv = "AF_VOLUME_BENCHMARK_OUT"

// renderVolumeBenchmark writes the report, dated, because a number older than
// the code that produced it is withdrawn rather than rounded.
func renderVolumeBenchmark(
	t *testing.T, cmp volume.Comparison, before string,
	withoutProfile, withProfile scoreOf, detail string,
) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "# The share of production a twin holds, per table\n\nMeasured %s by\n",
		time.Now().UTC().Format("2006-01-02"))
	b.WriteString("`go test ./internal/fidelity -run TestBenchmarkTheShareOfProductionInTheTwin`,\n")
	b.WriteString("from engine/internal/fidelity/volume_score_test.go, on the manifest in\n")
	b.WriteString("engine/internal/fidelity/testdata/product-analytics.yaml against the volume\n")
	b.WriteString("profile in engine/internal/fidelity/testdata/production-volume.json.\n\n")
	b.WriteString("The profile is the committed record of what production holds, collected by\n")
	b.WriteString("`af volume record` over a read only connection. It reads no row: every\n")
	b.WriteString("figure comes from pg_class, pg_stats and the partition catalogs. Run it\n")
	b.WriteString("against your own database and the table below is yours.\n\n")

	fmt.Fprintf(&b, "Collected %s.\n\n", cmp.CollectedAt.UTC().Format("2006-01-02"))
	b.WriteString("| Table | This twin | Production | Share |\n| --- | --- | --- | --- |\n")
	rows := append([]volume.TableShare(nil), cmp.Tables...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Production > rows[j].Production })
	for _, r := range rows {
		s, ok := r.Share()
		pct := "no production rows"
		if ok {
			pct = volume.Percent(s)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
			r.Name, volume.Count(r.Branch), volume.Count(r.Production), pct)
	}
	share, _ := cmp.Share()
	fmt.Fprintf(&b, "| **all twelve** | **%s** | **%s** | **%s** |\n\n",
		volume.Count(cmp.Branch), volume.Count(cmp.Production), volume.Percent(share))

	b.WriteString("What the fidelity report says about that database, on the same\n")
	b.WriteString("environment, three ways:\n\n")
	b.WriteString("| Instrument | The database's data component | Score |\n| --- | --- | --- |\n")
	fmt.Fprintf(&b, "| At %s, before this lane | `reproduced`, 12 tables over 184000 rows | %s |\n",
		beforeVolumeCommit, quoteScore(headlineOf(t, before)))
	fmt.Fprintf(&b, "| This build, no volume profile | `unmeasured`, and it says nothing compared it | %s |\n",
		renderScore(withoutProfile))
	fmt.Fprintf(&b, "| This build, with the profile | `substituted`, with the fraction | %s |\n\n",
		renderScore(withProfile))

	b.WriteString("The sentence the last row prints, in full:\n\n> ")
	b.WriteString(detail)
	b.WriteString("\n\nThe middle row is the one worth arguing about. An environment nobody has\n")
	b.WriteString("given a profile scores HIGHER than one that has, because an unmeasured\n")
	b.WriteString("component leaves the denominator while a substituted one stays in it. That\n")
	b.WriteString("is deliberate: an unknown is not a smaller pass, and the report names the\n")
	b.WriteString("component and how to measure it rather than scoring a guess. The number\n")
	b.WriteString("that goes down is the one on the row where somebody actually looked.\n\n")
	b.WriteString("The lock timings a migration rehearsal prints against a branch this size\n")
	b.WriteString("are lower bounds. `af insights` now says so, and states the extrapolation\n")
	b.WriteString("to production's row counts as an extrapolation.\n")
	return b.String()
}

// The committed profile has to be the shape the collector writes, or the
// benchmark is measured against a file nothing produces.
func TestTheCommittedProfileIsWhatTheCollectorWrites(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile(filepath.Join("testdata", productionProfileFile))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(body, &raw))
	require.Contains(t, raw, "collected_at")
	require.Contains(t, raw, "tables")

	p := productionVolume(t)
	require.False(t, p.CollectedAt.IsZero())
	require.NotContains(t, p.Source, "://",
		"the profile carries a connection string, which is a credential in the repository")
	for _, tbl := range p.Tables {
		require.True(t, tbl.Analyzed, "%s carries no production row count", tbl.Name)
		require.Positive(t, tbl.Rows)
	}
}
