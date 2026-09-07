package volume_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/volume"
)

// The arithmetic that decides whether a copy is production, and the sentence
// it produces.
//
// Every case here is written from the failure in the plan: a golden built from
// a staging database holding two hundred rows in events reported as
// reproducing a production holding four billion. The numbers below are that
// case and the ones either side of it.

func profile(tables ...volume.Table) volume.Profile {
	return volume.Profile{
		CollectedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Source:      "the database named by PRODUCTION_DATABASE_URL",
		Tables:      tables,
	}
}

func table(name string, rows int64) volume.Table {
	return volume.Table{Name: name, Rows: rows, Analyzed: true}
}

func TestCompare_TwoHundredRowsAgainstFourBillionIsNotAReproduction(t *testing.T) {
	t.Parallel()
	c := volume.Compare(
		[]volume.TableRows{{Name: "public.events", Rows: 200}},
		profile(table("public.events", 4_200_000_000)))

	require.False(t, c.Reproduces(),
		"a branch holding two hundred of four billion rows was called a reproduction")
	share, ok := c.Share()
	require.True(t, ok)
	require.InDelta(t, 200.0/4_200_000_000.0, share, 1e-12)
	require.Equal(t, "0.0000047 percent", volume.Percent(share),
		"the share rounded away to zero, which is the figure this exists to print")
}

func TestCompare_AFullCopyIsAReproduction(t *testing.T) {
	t.Parallel()
	// Not equal on purpose. Both sides are pg_class.reltuples, so a byte for
	// byte copy reports a slightly different figure from its source as soon as
	// either is vacuumed, and an equality test would call every faithful copy
	// a substitution.
	c := volume.Compare(
		[]volume.TableRows{
			{Name: "public.events", Rows: 4_198_000_000},
			{Name: "public.users", Rows: 1_000_000},
		},
		profile(table("public.events", 4_200_000_000), table("public.users", 1_000_000)))
	require.True(t, c.Reproduces())
}

func TestCompare_OneEmptyTableFailsWhateverTheHeadlineSays(t *testing.T) {
	t.Parallel()
	// The headline share is 99.9 percent, because events is enormous and users
	// is not. A verdict taken from the headline alone would pass a copy whose
	// users table is empty, and users is the table somebody's feature reads.
	c := volume.Compare(
		[]volume.TableRows{
			{Name: "public.events", Rows: 4_200_000_000},
			{Name: "public.users", Rows: 0},
		},
		profile(table("public.events", 4_200_000_000), table("public.users", 1_000_000)))

	share, ok := c.Share()
	require.True(t, ok)
	require.Greater(t, share, 0.999)
	require.False(t, c.Reproduces(),
		"a copy with an empty users table passed because events carried the average")

	worst, found := c.Smallest()
	require.True(t, found)
	require.Equal(t, "public.users", worst.Name)
}

// The share alone is unusable at the small end, and that is quantisation
// rather than tolerance: reltuples on a table of twenty rows moves by five
// percent every time one row is inserted or vacuumed away.
func TestCompare_ASmallTableShortByAFewRowsIsStillACopy(t *testing.T) {
	t.Parallel()
	c := volume.Compare(
		[]volume.TableRows{
			{Name: "public.events", Rows: 4_200_000_000},
			{Name: "public.plans", Rows: 14},
		},
		profile(table("public.events", 4_200_000_000), table("public.plans", 20)))

	share, ok := c.Tables[1].Share()
	require.True(t, ok)
	require.Less(t, share, volume.ReproducesAt,
		"the case is only interesting while the share alone would fail it")
	require.True(t, c.Reproduces(),
		"a lookup table six rows short of production's twenty was called a substitution")
}

// And the small table the copy does not have at all still fails, which is the
// case the slack must not swallow.
func TestCompare_ASmallTableTheCopyIsMissingStillFails(t *testing.T) {
	t.Parallel()
	c := volume.Compare(
		[]volume.TableRows{
			{Name: "public.events", Rows: 4_200_000_000},
			{Name: "public.plans", Rows: 0},
		},
		profile(table("public.events", 4_200_000_000), table("public.plans", 20)))
	require.False(t, c.Reproduces(),
		"an empty lookup table passed because the slack swallowed it")
}

func TestCompare_ATableProductionHasAndTheBranchDoesNotIsNamed(t *testing.T) {
	t.Parallel()
	c := volume.Compare(
		[]volume.TableRows{{Name: "public.events", Rows: 4_200_000_000}},
		profile(table("public.events", 4_200_000_000), table("public.invoices", 12_000)))

	require.Equal(t, []string{"public.invoices"}, c.Absent)
	require.False(t, c.Reproduces(),
		"a branch missing a table production has was called a reproduction of it")
	require.Contains(t, c.Describe(), "Production has public.invoices and this branch does not")
}

func TestCompare_ATableTheProfileNeverSawIsAGapNotAFault(t *testing.T) {
	t.Parallel()
	// A migration added public.audit after the profile was taken. Folding it
	// into either total would let a migration move the headline share, which
	// would be a number about the migration rather than about the copy.
	c := volume.Compare(
		[]volume.TableRows{
			{Name: "public.events", Rows: 4_200_000_000},
			{Name: "public.audit", Rows: 40},
		},
		profile(table("public.events", 4_200_000_000)))

	require.Equal(t, []string{"public.audit"}, c.Unprofiled)
	require.EqualValues(t, 4_200_000_000, c.Branch, "an unprofiled table moved the branch total")
	require.True(t, c.Reproduces())
}

func TestCompare_NothingInCommonIsNotAScore(t *testing.T) {
	t.Parallel()
	c := volume.Compare(
		[]volume.TableRows{{Name: "public.events", Rows: 10}},
		profile(table("analytics.events", 4_200_000_000)))

	_, ok := c.Share()
	require.False(t, ok, "a comparison against nothing produced a percentage")
	require.False(t, c.Reproduces())
}

func TestCompare_ATableProductionNeverAnalyzedIsNotADenominator(t *testing.T) {
	t.Parallel()
	c := volume.Compare(
		[]volume.TableRows{{Name: "public.events", Rows: 900}},
		profile(volume.Table{Name: "public.events", Rows: 0, Analyzed: false}))

	require.Empty(t, c.Tables, "a table with no production row count was compared against zero")
	require.Equal(t, []string{"public.events"}, c.Unprofiled)
	_, ok := c.Share()
	require.False(t, ok)
}

func TestPercent_StaysANumberAtTheSmallEnd(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		share float64
		want  string
	}{
		{1, "100 percent"},
		{0.999, "99 percent"},
		{0.5, "50 percent"},
		{0.0432, "4.3 percent"},
		{0.0000003309, "0.000033 percent"},
		{1e-15, "less than 0.000000000001 percent"},
		{0, "0 percent"},
	} {
		require.Equal(t, c.want, volume.Percent(c.share))
	}
}

// A copy holding 99.9 percent of production must never print as 100.
//
// Rounding to nearest overstates it exactly at the boundary somebody would
// quote, and overstating the copy is the one direction this number is not
// allowed to err in.
func TestPercent_NeverRoundsUpToAHundred(t *testing.T) {
	t.Parallel()
	require.Equal(t, "99 percent", volume.Percent(0.9999))
	require.Equal(t, "100 percent", volume.Percent(1.0))
}

func TestRows_ReadsWithoutBeingRounded(t *testing.T) {
	t.Parallel()
	require.Equal(t, "4,200,000,000 rows", volume.Rows(4_200_000_000))
	require.Equal(t, "1 row", volume.Rows(1))
	require.Equal(t, "0 rows", volume.Rows(0))
}

func TestSkew_OnePartitionHoldingEverythingIsVisible(t *testing.T) {
	t.Parallel()
	share, ok := volume.Table{
		Rows: 1000, Partitions: 10, LargestPartitionRows: 900,
	}.Skew()
	require.True(t, ok)
	require.InDelta(t, 0.9, share, 1e-9)

	_, ok = volume.Table{Rows: 1000}.Skew()
	require.False(t, ok, "an unpartitioned table reported a skew")
}

// The lock sampler reports pg_class.relname, which carries no schema.
func TestFindRelname_RefusesTwoTablesOfTheSameName(t *testing.T) {
	t.Parallel()
	p := profile(table("public.orders", 300), table("billing.orders", 11))

	_, why := p.FindRelname("orders")
	require.Contains(t, why, "more than one table is called orders")
	require.Contains(t, why, "billing.orders")
	require.Contains(t, why, "public.orders")

	found, why := p.FindRelname("events")
	require.Contains(t, why, "does not name a table called events")
	require.Zero(t, found.Rows)

	one, why := profile(table("public.orders", 300)).FindRelname("orders")
	require.Empty(t, why)
	require.EqualValues(t, 300, one.Rows)
}

func TestFindRelname_ATableProductionNeverAnalyzedHasNoRowCount(t *testing.T) {
	t.Parallel()
	p := profile(volume.Table{Name: "public.orders", Analyzed: false})
	_, why := p.FindRelname("orders")
	require.Contains(t, why, "has never been analyzed on production")
}

// The renderers, at the ends a report actually reaches.
//
// Bytes and Age are read by af volume show and nothing else calls them from
// inside this package, so a break in either is invisible to every test above
// while the numbers they wrap stay correct. That is the shape of the defect
// this repository keeps finding: the pieces are there and the sentence a
// person reads is wrong.
func TestBytes_ReadsInTheUnitSomebodyWouldSayItIn(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{4096, "4.0 KiB"},
		{966367641600, "900.0 GiB"},
	} {
		require.Equal(t, c.want, volume.Bytes(c.bytes))
	}
}

func TestAge_IsMeasuredFromWhenItWasCollected(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	p := volume.Profile{CollectedAt: now.Add(-72 * time.Hour)}
	require.Equal(t, 72*time.Hour, p.Age(now))

	// A profile that does not say when it was taken has no age, and zero is
	// the only honest answer: reporting "since the epoch" would be a figure
	// somebody screenshots. Load refuses that profile outright.
	require.Zero(t, volume.Profile{}.Age(now))
}

// A profile naming a table nothing else does answers no question, and Find
// says so rather than returning a zero table somebody divides by.
func TestFind_SaysWhenItDoesNotHaveTheTable(t *testing.T) {
	t.Parallel()
	p := profile(table("public.events", 12))
	_, ok := p.Find("public.orders")
	require.False(t, ok)
	got, ok := p.Find("public.events")
	require.True(t, ok)
	require.EqualValues(t, 12, got.Rows)
}

// Describe is the sentence the fidelity report carries, and the branch where
// there is nothing to compare has to say that rather than print a zero.
func TestDescribe_SaysWhenThereIsNothingToCompare(t *testing.T) {
	t.Parallel()
	c := volume.Compare(
		[]volume.TableRows{{Name: "public.events", Rows: 10}},
		profile(table("analytics.events", 4_200_000_000)))
	require.Contains(t, c.Describe(), "names no table this branch also has")
}

// list is what puts more than one missing table into one line of prose, and
// the three shapes it has to get right are none, one, and several.
func TestDescribe_NamesEveryTableTheBranchIsMissing(t *testing.T) {
	t.Parallel()
	c := volume.Compare(
		[]volume.TableRows{{Name: "public.events", Rows: 4_200_000_000}},
		profile(
			table("public.events", 4_200_000_000),
			table("public.invoices", 12_000),
			table("public.receipts", 900)))
	require.Contains(t, c.Describe(),
		"Production has public.invoices and public.receipts and this branch does not")
}

// A qualified name resolves directly, and a qualified name the profile does
// not have says which one it looked for.
//
// The lock sampler reports an unqualified relname today, so this arm is the
// one a second caller would reach first, and an arm nothing exercises is an
// arm that is wrong the day somebody uses it.
func TestFindRelname_AQualifiedNameIsResolvedDirectly(t *testing.T) {
	t.Parallel()
	p := profile(table("public.orders", 300), table("billing.orders", 11))

	got, why := p.FindRelname("billing.orders")
	require.Empty(t, why)
	require.EqualValues(t, 11, got.Rows, "the qualified name resolved to the other schema's table")

	_, why = p.FindRelname("public.events")
	require.Equal(t, "the volume profile does not name public.events", why)
}
