package insights_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/internal/volume"
)

// A lock held for half a second over a thousand rows is a real measurement and
// a worthless prediction, and until this lane nothing said which of the two it
// was. These are the cases where saying it wrong would be worse than saying
// nothing.

func prodProfile(tables ...volume.Table) *volume.Profile {
	return &volume.Profile{
		CollectedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Tables:      tables,
	}
}

func prodTable(name string, rows int64) volume.Table {
	return volume.Table{Name: name, Rows: rows, Analyzed: true}
}

func rehearsalHolding(table, mode string, heldMS float64, branchRows int64) *insights.Rehearsal {
	return &insights.Rehearsal{
		Locks:      []insights.LockHold{{Table: table, Mode: mode, HeldMS: heldMS}},
		BranchRows: map[string]int64{table: branchRows},
	}
}

// The headline. The projection is stated, and it is labelled.
func TestExtrapolate_StatesTheProjectionAndCallsItAnExtrapolation(t *testing.T) {
	t.Parallel()
	r := rehearsalHolding("events", "AccessExclusiveLock", 512, 1390)
	insights.Extrapolate(r, prodProfile(prodTable("public.events", 4_200_000_000)), "")

	require.Len(t, r.Extrapolations, 1)
	e := r.Extrapolations[0]
	require.EqualValues(t, 1390, e.BranchRows)
	require.EqualValues(t, 4_200_000_000, e.ProductionRows)
	require.InDelta(t, 4_200_000_000.0/1390.0, e.Factor, 1)
	require.InDelta(t, 512*4_200_000_000.0/1390.0, e.AtThisRateMS, 1000)

	require.Contains(t, e.Sentence, "held AccessExclusiveLock on events for 512ms over 1,390 rows")
	require.Contains(t, e.Sentence, "Production holds 4,200,000,000 rows in that table")
	require.Contains(t, e.Sentence, "3,021,582 times as many")
	require.Contains(t, e.Sentence, "the lock would be held for roughly 17.9 days")
	require.Contains(t, e.Sentence, "extrapolation from one measurement rather than a second measurement",
		"the projection was printed as though it were a timing")
}

// Every sentence this produces carries the word, whatever branch it came from.
// A projection presented as a timing would make this engine the thing it was
// built to replace.
func TestExtrapolate_EverySentenceSaysWhatKindOfNumberItIs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		r      *insights.Rehearsal
		p      *volume.Profile
		expect string
	}{
		{
			name:   "production is bigger",
			r:      rehearsalHolding("events", "AccessExclusiveLock", 500, 1000),
			p:      prodProfile(prodTable("public.events", 1_000_000)),
			expect: "extrapolation",
		},
		{
			name:   "the branch holds nothing",
			r:      rehearsalHolding("events", "AccessExclusiveLock", 500, 0),
			p:      prodProfile(prodTable("public.events", 1_000_000)),
			expect: "no rate to extrapolate from",
		},
		{
			name:   "production is no bigger",
			r:      rehearsalHolding("events", "AccessExclusiveLock", 500, 1_000_000),
			p:      prodProfile(prodTable("public.events", 900_000)),
			expect: "measurement rather than a lower bound",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			insights.Extrapolate(c.r, c.p, "")
			require.Len(t, c.r.Extrapolations, 1)
			require.Contains(t, c.r.Extrapolations[0].Sentence, c.expect)
		})
	}
}

// Without a profile the timings are still lower bounds, and the report has to
// say so. Silence here is the defect this lane exists for.
func TestExtrapolate_WithNoProfileTheTimingsAreDeclaredLowerBounds(t *testing.T) {
	t.Parallel()
	r := rehearsalHolding("events", "AccessExclusiveLock", 512, 1390)
	insights.Extrapolate(r, nil, "no volume profile has been recorded at .antifailure/volume.json")

	require.Empty(t, r.Extrapolations)
	joined := strings.Join(r.Missing, "\n")
	require.Contains(t, joined, "measured against this branch's own row counts")
	require.Contains(t, joined, "no volume profile has been recorded at .antifailure/volume.json")
	require.Contains(t, joined, "lower bound and not a prediction")
}

// The lock sampler reports pg_class.relname, which carries no schema. Two
// tables of that name is refused by name rather than resolved by taking the
// first, because the whole value of a number attached to a table is that it
// belongs to the table somebody is looking at.
func TestExtrapolate_RefusesAnAmbiguousTableRatherThanPickingOne(t *testing.T) {
	t.Parallel()
	r := rehearsalHolding("orders", "AccessExclusiveLock", 512, 300)
	insights.Extrapolate(r, prodProfile(
		prodTable("public.orders", 4_000_000), prodTable("billing.orders", 11)), "")

	require.Empty(t, r.Extrapolations, "an ambiguous table was resolved by picking one")
	joined := strings.Join(r.Missing, "\n")
	require.Contains(t, joined, "more than one table is called orders")
	require.Contains(t, joined, "lower bound and not a prediction")
}

// A table the profile never saw gets no projection and says which one.
func TestExtrapolate_ATableTheProfileNeverSawIsNamed(t *testing.T) {
	t.Parallel()
	r := rehearsalHolding("audit", "AccessExclusiveLock", 400, 12)
	insights.Extrapolate(r, prodProfile(prodTable("public.events", 4_000_000)), "")

	require.Empty(t, r.Extrapolations)
	require.Contains(t, strings.Join(r.Missing, "\n"),
		"the volume profile does not name a table called audit")
}

// A lock nothing saw held is not a lock to extrapolate from.
func TestExtrapolate_ALockWithNoMeasuredHoldIsSkipped(t *testing.T) {
	t.Parallel()
	r := &insights.Rehearsal{
		Locks:      []insights.LockHold{{Table: "events", Mode: "AccessShareLock"}},
		BranchRows: map[string]int64{"events": 1000},
	}
	insights.Extrapolate(r, prodProfile(prodTable("public.events", 4_000_000)), "")
	require.Empty(t, r.Extrapolations)
	require.Empty(t, r.Missing)
}

// The profile's date travels with the projection, so a report quoting it can
// say how old the denominator was.
func TestExtrapolate_TheProfilesDateIsCarriedIntoTheRehearsal(t *testing.T) {
	t.Parallel()
	r := rehearsalHolding("events", "AccessExclusiveLock", 512, 1390)
	p := prodProfile(prodTable("public.events", 4_200_000_000))
	insights.Extrapolate(r, p, "")
	require.Equal(t, p.CollectedAt, r.ProfileCollectedAt)
}

// The projection has to reach the page, or it is a field nothing renders.
//
// A number computed and never printed is the dead code this repository keeps
// finding in its own instruments: the pieces are all there and the observable
// behaviour never happens.
func TestExplain_PrintsTheExtrapolationUnderTheLocksItCameFrom(t *testing.T) {
	t.Parallel()
	r := rehearsalHolding("events", "AccessExclusiveLock", 512, 1390)
	insights.Extrapolate(r, prodProfile(prodTable("public.events", 4_200_000_000)), "")

	text := r.Explain()
	require.Contains(t, text, "Locks held while the migrations ran:")
	require.Contains(t, text, "At production's row counts, from the volume profile collected on 2026-09-01:")
	require.Contains(t, text, "3,021,582 times as many")
	require.Contains(t, text, "extrapolation from one measurement")
}

// With no profile the sentence still reaches the page, through Missing, which
// is the list Full.Explain already prints.
func TestExplain_TheLowerBoundWarningReachesTheReport(t *testing.T) {
	t.Parallel()
	r := rehearsalHolding("events", "AccessExclusiveLock", 512, 1390)
	insights.Extrapolate(r, nil, "no volume profile has been recorded")

	full := insights.Full{Rehearsal: r, Missing: r.Missing}
	require.Contains(t, full.Explain(),
		"Not measured: every lock timing here was measured against this branch's own row counts")
}

// The wiring, against a real database, because a projection computed and never
// passed through is the dead code this repository keeps finding: the pieces
// are all there and the observable behaviour never happens.
//
// insights.Run is what af insights calls, and it is the only place that hands
// the profile to Extrapolate. Everything above tests Extrapolate directly,
// which proves the arithmetic and proves nothing about whether anybody calls
// it.
func TestRun_TheProfileReachesTheRehearsalsExtrapolation(t *testing.T) {
	db, done := requireDatabase(t, "insightsvolume")
	defer done()
	ctx := context.Background()

	// The sleep is what makes this test say the same thing on every machine,
	// and it is worth explaining because it looks like padding. The lock
	// sampler wakes every LockSampleInterval, 250ms, and Extrapolate only has
	// something to project from once a lock has been SEEN. This migration
	// rewrites fifty thousand rows, which takes long enough to be sampled on
	// a loaded workstation and finished inside one interval on a CI runner,
	// so the first version of this test passed locally and failed there
	// having proved nothing about the wiring it exists to check. Holding the
	// transaction open for a second puts the lock across four samples, which
	// is a property of the test rather than of the machine.
	set := insights.Discover(migrationsFS(map[string]string{
		"001_widen.sql": "ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint;\n" +
			"SELECT pg_sleep(1);",
	}))
	target := &insights.Target{
		Conn: db.conn, Watch: db.watch, URL: db.url, Set: set,
		Applier: &insights.SQLApplier{},
	}
	// The template holds about fifty thousand orders. Production, in this
	// profile, holds four billion.
	profile := prodProfile(prodTable("public.orders", 4_200_000_000))

	full, err := insights.Run(ctx, insights.Options{
		Config: insights.Configure(nil), Branch: db.conn, Limit: 5,
		Rehearsal: target, Volume: profile,
	})
	require.NoError(t, err)
	require.NotNil(t, full.Rehearsal)
	require.False(t, full.Rehearsal.Failed, full.Rehearsal.Error)

	require.NotEmpty(t, full.Rehearsal.BranchRows,
		"the rehearsal recorded no branch row counts, so there is nothing to extrapolate from")
	require.NotEmpty(t, full.Rehearsal.Extrapolations,
		"the profile was passed to Run and no projection came back, so nothing calls Extrapolate")
	require.Equal(t, profile.CollectedAt, full.Rehearsal.ProfileCollectedAt)

	e := full.Rehearsal.Extrapolations[0]
	require.Equal(t, "orders", e.Table)
	require.EqualValues(t, 4_200_000_000, e.ProductionRows)
	require.Greater(t, e.Factor, 1.0)
	require.Contains(t, e.Sentence, "extrapolation from one measurement")
	require.Contains(t, full.Explain(), "At production's row counts, from the volume profile")
}

// And without one, the sentence that says the timings are lower bounds reaches
// the same report through the same call.
func TestRun_WithNoProfileTheReportSaysTheTimingsAreLowerBounds(t *testing.T) {
	db, done := requireDatabase(t, "insightsnovolume")
	defer done()
	ctx := context.Background()

	set := insights.Discover(migrationsFS(map[string]string{
		"001_widen.sql": "ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint;",
	}))
	full, err := insights.Run(ctx, insights.Options{
		Config: insights.Configure(nil), Branch: db.conn, Limit: 5,
		Rehearsal: &insights.Target{
			Conn: db.conn, Watch: db.watch, URL: db.url, Set: set,
			Applier: &insights.SQLApplier{},
		},
		VolumeReason: "no volume profile has been recorded at .antifailure/volume.json",
	})
	require.NoError(t, err)
	require.NotNil(t, full.Rehearsal)
	require.Empty(t, full.Rehearsal.Extrapolations)
	require.Contains(t, strings.Join(full.Missing, "\n"),
		"no volume profile has been recorded at .antifailure/volume.json")
	require.Contains(t, full.Explain(), "lower bound and not a prediction")
}

// The projection has to be readable at every scale it can come out at.
//
// duration, which the rest of the rehearsal prints with, stops being readable
// past an hour: it was written for a statement timing, and a statement nobody
// has ever timed at a day would render as 5760000m00s. A projection is exactly
// the number that runs to days, so it gets its own renderer, and this is the
// check that the renderer covers the range rather than one point in it.
func TestExtrapolate_TheProjectionIsReadableAtEveryScale(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name       string
		branchRows int64
		prodRows   int64
		heldMS     float64
		want       string
	}{
		{"under a minute stays in the units a statement is timed in", 1000, 4000, 500, "2.0s"},
		{"minutes", 1000, 600_000, 500, "5.0 minutes"},
		{"hours", 1000, 30_000_000, 500, "4.2 hours"},
		{"days", 1390, 4_200_000_000, 512, "17.9 days"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := rehearsalHolding("events", "AccessExclusiveLock", c.heldMS, c.branchRows)
			insights.Extrapolate(r, prodProfile(prodTable("public.events", c.prodRows)), "")
			require.Len(t, r.Extrapolations, 1)
			require.Contains(t, r.Extrapolations[0].Sentence,
				"held for roughly "+c.want)
		})
	}
}
