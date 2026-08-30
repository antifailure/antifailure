package golden_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/golden"
)

func mustParse(t *testing.T, expr string) golden.Schedule {
	t.Helper()
	s, err := golden.ParseSchedule(expr)
	require.NoError(t, err, expr)
	return s
}

func at(t *testing.T, zone, stamp string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	require.NoError(t, err)
	got, err := time.ParseInLocation("2006-01-02 15:04", stamp, loc)
	require.NoError(t, err)
	return got
}

func TestParseSchedule_ReadsEveryFormTheValidatorAccepts(t *testing.T) {
	t.Parallel()
	// The manifest already validates these five forms, so a schedule that
	// validates and then fails to parse would be a build that accepts a
	// manifest it cannot run.
	for _, expr := range []string{
		"* * * * *",
		"0 3 * * *",
		"*/15 * * * *",
		"0 0 1,15 * *",
		"30 9-17 * * 1-5",
		"0 0 * * 0",
		"0 0 * * 7",
		"CRON_TZ=Europe/London 0 3 * * *",
	} {
		_, err := golden.ParseSchedule(expr)
		require.NoError(t, err, expr)
	}
}

func TestParseSchedule_SaysWhatIsWrongWithTheOnesItRefuses(t *testing.T) {
	t.Parallel()
	for expr, want := range map[string]string{
		"0 3 * *":                        "has 4 fields",
		"60 3 * * *":                     "within 0 to 59",
		"0 24 * * *":                     "within 0 to 23",
		"0 3 * * 8":                      "within 0 to 7",
		"*/0 * * * *":                    "positive number",
		"5-1 * * * *":                    "run upward",
		"0 x * * *":                      "a number, a range, or *",
		"CRON_TZ=Mars/Olympus 0 3 * * *": "not a time zone",
		"CRON_TZ=Europe/London":          "not followed by an expression",
	} {
		_, err := golden.ParseSchedule(expr)
		require.Error(t, err, expr)
		require.Contains(t, err.Error(), want, expr)
	}
}

func TestNext_IsStrictlyAfterSoALoopTerminates(t *testing.T) {
	t.Parallel()
	s := mustParse(t, "0 3 * * *")
	start := at(t, "UTC", "2026-05-01 03:00")

	// Feeding a firing back in has to return the following one. If it returned
	// the same one, every caller that walks the schedule would spin.
	next := s.Next(start)
	require.True(t, next.After(start))
	require.Equal(t, at(t, "UTC", "2026-05-02 03:00"), next)

	seen := map[time.Time]bool{}
	cur := start
	for i := 0; i < 100; i++ {
		cur = s.Next(cur)
		require.False(t, seen[cur], "the same firing came back twice")
		seen[cur] = true
	}
}

func TestNext_HandlesRangesStepsAndLists(t *testing.T) {
	t.Parallel()
	quarterly := mustParse(t, "*/15 * * * *")
	require.Equal(t, at(t, "UTC", "2026-05-01 09:15"),
		quarterly.Next(at(t, "UTC", "2026-05-01 09:07")))
	require.Equal(t, at(t, "UTC", "2026-05-01 10:00"),
		quarterly.Next(at(t, "UTC", "2026-05-01 09:45")))

	workdays := mustParse(t, "30 9 * * 1-5")
	// Saturday the second of May 2026 is a Saturday, so the next firing is
	// Monday.
	require.Equal(t, at(t, "UTC", "2026-05-04 09:30"),
		workdays.Next(at(t, "UTC", "2026-05-02 00:00")))

	firstAndFifteenth := mustParse(t, "0 0 1,15 * *")
	require.Equal(t, at(t, "UTC", "2026-05-15 00:00"),
		firstAndFifteenth.Next(at(t, "UTC", "2026-05-02 00:00")))
}

func TestNext_TakesEitherDayFieldWhenBothAreNarrowed(t *testing.T) {
	t.Parallel()
	// Cron's rule and the surprising one. "0 0 13 * 5" is the thirteenth of
	// every month AND every Friday, not Friday the thirteenth. Getting this
	// wrong makes a weekly job run monthly, which nobody notices for a month.
	s := mustParse(t, "0 0 13 * 5")

	// The first of May 2026 is a Friday, so the very next firing is that day
	// rather than the thirteenth.
	require.Equal(t, at(t, "UTC", "2026-05-01 00:00"),
		s.Next(at(t, "UTC", "2026-04-30 12:00")))
	// And the thirteenth is taken even though it is a Wednesday.
	require.Equal(t, at(t, "UTC", "2026-05-13 00:00"),
		s.Next(at(t, "UTC", "2026-05-09 00:00")))
}

func TestNext_ReadsTheExpressionInItsOwnZone(t *testing.T) {
	t.Parallel()
	// Three in the morning means three in the morning where the team is. A
	// schedule kept in UTC drifts an hour twice a year against the one thing
	// it was chosen to avoid, which is being awake for it.
	s := mustParse(t, "CRON_TZ=Europe/London 0 3 * * *")

	// In January London is on UTC, so the firing is 03:00 UTC.
	winter := s.Next(at(t, "UTC", "2026-01-10 00:00"))
	require.Equal(t, "2026-01-10 03:00", winter.In(s.Location()).Format("2006-01-02 15:04"))
	require.Equal(t, "03:00", winter.UTC().Format("15:04"))

	// In July it is on BST, so the same expression fires at 02:00 UTC and
	// still at three in the morning locally.
	summer := s.Next(at(t, "UTC", "2026-07-10 00:00"))
	require.Equal(t, "2026-07-10 03:00", summer.In(s.Location()).Format("2006-01-02 15:04"))
	require.Equal(t, "02:00", summer.UTC().Format("15:04"))
}

func TestNext_FiresOnceOnTheDayTheClocksGoForward(t *testing.T) {
	t.Parallel()
	// New York jumps from 02:00 to 03:00 on 8 March 2026, so 02:30 does not
	// exist that day. Go's own resolution of that wall time is 01:30, an hour
	// BEFORE the gap, which would run a nightly refresh early once a year. The
	// deliberate answer is the first moment the clock reaches: 03:00.
	s := mustParse(t, "CRON_TZ=America/New_York 30 2 * * *")

	fired := s.Next(at(t, "America/New_York", "2026-03-07 12:00"))
	require.Equal(t, "2026-03-08 03:00:00 EDT",
		fired.In(s.Location()).Format("2006-01-02 15:04:05 MST"),
		"the refresh happens at the first instant it could have, not an hour early")
	require.False(t, fired.Before(at(t, "America/New_York", "2026-03-08 01:59")),
		"and never before the gap")

	// The following day is back to normal, and the schedule fired exactly once
	// on the day of the change.
	require.Equal(t, "2026-03-09 02:30:00 EDT",
		s.Next(fired).In(s.Location()).Format("2006-01-02 15:04:05 MST"))

	// The same day in London, which jumps 01:00 to 02:00 three weeks later and
	// which Go resolves in the opposite direction. One rule, both zones.
	ldn := mustParse(t, "CRON_TZ=Europe/London 30 1 * * *")
	fired = ldn.Next(at(t, "Europe/London", "2026-03-28 12:00"))
	require.Equal(t, "2026-03-29 02:00:00 BST",
		fired.In(ldn.Location()).Format("2006-01-02 15:04:05 MST"))
}

func TestNext_FiresOnceOnTheDayTheClocksGoBack(t *testing.T) {
	t.Parallel()
	// New York goes 02:00 back to 01:00 on 1 November 2026, so 01:30 happens
	// twice: once on daylight time and once on standard. A refresh scheduled
	// for half past one runs once, because the second is not strictly after
	// the first.
	s := mustParse(t, "CRON_TZ=America/New_York 30 1 * * *")

	first := s.Next(at(t, "America/New_York", "2026-10-31 12:00"))
	require.Equal(t, "2026-11-01 01:30:00 EDT",
		first.In(s.Location()).Format("2006-01-02 15:04:05 MST"))
	require.Equal(t, "05:30", first.UTC().Format("15:04"))

	second := s.Next(first)
	require.Equal(t, "2026-11-02 01:30:00 EST",
		second.In(s.Location()).Format("2006-01-02 15:04:05 MST"),
		"the repeated hour does not fire a second time")
	require.Greater(t, second.Sub(first), 24*time.Hour,
		"the gap across the repeated hour is 25 hours, not 24")
}

func TestNext_ReturnsNothingForAnExpressionThatCanNeverFire(t *testing.T) {
	t.Parallel()
	// The thirtieth of February. It terminates rather than searching forever,
	// which is what the four year bound is for.
	s := mustParse(t, "0 0 30 2 *")
	require.True(t, s.Next(at(t, "UTC", "2026-01-01 00:00")).IsZero())

	// The twenty-ninth does fire, in a leap year, which is the case the bound
	// has to be wide enough for.
	leap := mustParse(t, "0 0 29 2 *")
	require.Equal(t, at(t, "UTC", "2028-02-29 00:00"),
		leap.Next(at(t, "UTC", "2026-01-01 00:00")))
}

func TestDue_IsTrueWithNoGoldenAndFalseWithNoSchedule(t *testing.T) {
	t.Parallel()
	s := mustParse(t, "0 3 * * *")
	now := at(t, "UTC", "2026-05-10 04:00")

	require.True(t, s.Due(time.Time{}, now),
		"a project with a schedule and no golden wants one now, not at three tomorrow")
	require.True(t, s.Due(at(t, "UTC", "2026-05-09 03:00"), now),
		"yesterday's refresh means today's is due")
	require.False(t, s.Due(at(t, "UTC", "2026-05-10 03:00"), now),
		"today's already happened")

	var none golden.Schedule
	require.True(t, none.Zero())
	require.False(t, none.Due(time.Time{}, now),
		"no schedule means on demand, and never an interruption")
}
