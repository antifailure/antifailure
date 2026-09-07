package provider_test

import (
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The identifier every golden is known by had no test of its own. These are
// about the one property nothing else can supply: two goldens are two things.
//
// The rules hash cannot supply it. It is a digest of the masking rules, so two
// goldens built from the same rules SHOULD carry the same one, and the two
// suites that hit this in CI both had to mint a random hash per call to get
// around it. That is a workaround at the caller, and it leaves every other
// caller holding the same edge.

func TestTwoGoldensRefreshedInsideOneSecondGetTwoIdentifiers(t *testing.T) {
	t.Parallel()

	// The exact shape of the failure. A refresh, then a second refresh of the
	// same golden a moment later, both with the constant rules hash a caller is
	// entitled to pass, both inside one second.
	first := time.Date(2026, 9, 7, 6, 4, 45, 120_000_000, time.UTC)
	second := first.Add(time.Microsecond)

	require.NotEqual(t,
		provider.NewGoldenVersionID(first, "abc"),
		provider.NewGoldenVersionID(second, "abc"),
		"two refreshes a microsecond apart were given one identifier, which is "+
			"the CREATE DATABASE that failed with already exists")
}

func TestAGoldenIdentifierStillNamesTheSecondItWasMadeIn(t *testing.T) {
	t.Parallel()

	// The extra digits are appended, not substituted. A reader of a listing
	// still sees the date and the time of day at the front, which is the
	// property the format exists for.
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	require.Equal(t, "gv_20260101000000000000_abcd1234",
		provider.NewGoldenVersionID(at, "abcd1234"),
		"the second is still spelled out in full before the microseconds")
}

func TestGoldenIdentifiersSortByAgeAsStrings(t *testing.T) {
	t.Parallel()

	// The documented promise: a directory listing, a database index and a human
	// reading a list all agree on the order without parsing anything. A
	// sub-second field breaks that the moment it is not fixed width and zero
	// padded, and a listing would then interleave two goldens made in one second
	// with goldens made hours apart.
	base := time.Date(2026, 9, 7, 6, 4, 45, 0, time.UTC)
	times := []time.Time{
		base.Add(2 * time.Second),
		base.Add(999999 * time.Microsecond),
		base,
		base.Add(time.Microsecond),
		base.Add(1500 * time.Microsecond),
		base.Add(time.Hour),
	}

	ids := make([]string, len(times))
	for i, at := range times {
		ids[i] = provider.NewGoldenVersionID(at, "abcd1234")
	}
	byAge := append([]time.Time(nil), times...)
	sort.Slice(byAge, func(i, j int) bool { return byAge[i].Before(byAge[j]) })
	want := make([]string, len(byAge))
	for i, at := range byAge {
		want[i] = provider.NewGoldenVersionID(at, "abcd1234")
	}
	sort.Strings(ids)

	require.Equal(t, want, ids,
		"sorting the identifiers as strings no longer sorts the goldens by age")
}

func TestAGoldenIdentifierMatchesThePatternTheMCPServerPublishes(t *testing.T) {
	t.Parallel()

	// engine/internal/mcp/tools_golden.go publishes this pattern to every agent
	// that calls the server, as the shape of a value it will accept. Twenty is
	// the ceiling on the timestamp and a microsecond timestamp is exactly
	// twenty, so this is the assertion that says no to a wider one. Nanoseconds
	// would be twenty three and would be refused by the server that describes
	// the identifiers this function mints.
	published := regexp.MustCompile(`^gv_[0-9]{8,20}_[0-9a-f]{4,32}$`)
	at := time.Date(2026, 9, 7, 6, 4, 45, 999_999_999, time.UTC)

	require.Regexp(t, published, provider.NewGoldenVersionID(at, "74234e98"),
		"the identifier no longer matches the pattern the MCP server accepts")
}

func TestAShortHashIsPaddedAndALongOneIsTruncated(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	require.Equal(t, "gv_20260101000000000000_ab000000",
		provider.NewGoldenVersionID(at, "ab"),
		"a hash shorter than eight characters is padded to eight")

	require.Equal(t, "gv_20260101000000000000_abcdef12",
		provider.NewGoldenVersionID(at, "abcdef1234567890"),
		"a hash longer than eight characters is truncated to eight")
}
