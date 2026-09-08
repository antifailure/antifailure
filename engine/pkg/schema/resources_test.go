package schema_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The two quantity parsers, which are the one place a manifest's "512Mi"
// becomes a number.
//
// One function per dimension rather than a parse in each runtime, because two
// parsers are two chances to disagree about what a manifest means, and the
// disagreement would surface as one runtime enforcing a cap the other did not:
// an environment that passes locally and is killed on the cluster reads as a
// flaky cluster rather than as a manifest nobody agreed about.

func TestParseMilliCPU_ReadsTheSpellingsAManifestUses(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int64{
		"2":     2000,
		"1":     1000,
		"0.5":   500,
		"500m":  500,
		"1.5":   1500,
		"1500m": 1500,
		"0.001": 1,
		"1m":    1,
	} {
		got, err := schema.ParseMilliCPU(in)
		require.NoError(t, err, "%q is a share somebody writes", in)
		require.Equal(t, want, got, "%q", in)
	}
}

func TestParseMilliCPU_ReadsTheTwoSpellingsOfOneShareTheSame(t *testing.T) {
	t.Parallel()
	// 0.5 and 500m are the same share, and a parser that disagreed with
	// itself about that would give two manifests that say the same thing two
	// different containers.
	fraction, err := schema.ParseMilliCPU("0.5")
	require.NoError(t, err)
	suffixed, err := schema.ParseMilliCPU("500m")
	require.NoError(t, err)
	require.Equal(t, fraction, suffixed)
}

func TestParseMilliCPU_RefusesAShareFinerThanItCanHold(t *testing.T) {
	t.Parallel()
	// The failure mode this guards is silence, not a wrong number. Truncating
	// 0.0001 gives zero, zero is Docker's own word for unconstrained, and the
	// author of a cap would have got a container with none.
	for _, in := range []string{"0.0001", "0.5m", "0.0005"} {
		_, err := schema.ParseMilliCPU(in)
		require.Error(t, err, "%q rounds to no cap at all and must not be accepted", in)
		require.True(t, errors.Is(err, schema.ErrTooFine),
			"%q is a quantity that is too fine, not a value that is not a quantity, "+
				"and the caller tells its author two different things", in)
	}
}

func TestParseMilliCPU_RefusesWhatIsNotAQuantity(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "  ", "half", "-1", "-500m", "2 cores", "m", "1e3"} {
		_, err := schema.ParseMilliCPU(in)
		require.Error(t, err, "%q is not a CPU quantity", in)
		require.False(t, errors.Is(err, schema.ErrTooFine),
			"%q is not a quantity at all, so calling it too fine would send its author "+
				"looking for a rounding problem that is not there", in)
	}
}

func TestParseMemoryBytes_ReadsBothFamiliesOfUnit(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int64{
		"512Mi": 512 * 1024 * 1024,
		"2Gi":   2 * 1024 * 1024 * 1024,
		"512M":  512 * 1000 * 1000,
		"2G":    2 * 1000 * 1000 * 1000,
		"6Mi":   6 * 1024 * 1024,
	} {
		got, err := schema.ParseMemoryBytes(in)
		require.NoError(t, err, "%q is a size somebody writes", in)
		require.Equal(t, want, got, "%q", in)
	}
}

func TestParseMemoryBytes_KeepsTheTwoUnitFamiliesApart(t *testing.T) {
	t.Parallel()
	// Mi is a power of two and M a power of ten, which is what those suffixes
	// mean everywhere else and what somebody copying a value out of a
	// Deployment expects. A parser that treated them alike would quietly give
	// a service five percent less memory than the number it was written with.
	binary, err := schema.ParseMemoryBytes("512Mi")
	require.NoError(t, err)
	decimal, err := schema.ParseMemoryBytes("512M")
	require.NoError(t, err)
	require.Greater(t, binary, decimal)
}

func TestParseMemoryBytes_RefusesANumberWithNoUnit(t *testing.T) {
	t.Parallel()
	// The one place this deliberately refuses something Kubernetes accepts.
	// "memory: 512" there is 512 bytes, and nobody who writes it means 512
	// bytes. Accepting it silently gives a container the daemon then refuses
	// for being under its floor, several seconds later, with a message about
	// a daemon constant rather than about the line somebody wrote.
	_, err := schema.ParseMemoryBytes("512")
	require.Error(t, err)
	require.Contains(t, err.Error(), "names no unit")
}

func TestParseMemoryBytes_RefusesWhatIsNotAQuantity(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "  ", "heaps", "-512Mi", "512mb", "512 Mi", "Mi", "1.5Gi"} {
		_, err := schema.ParseMemoryBytes(in)
		require.Error(t, err, "%q is not a memory quantity", in)
	}
}

func TestParseMemoryBytes_RefusesASizeThatWouldOverflow(t *testing.T) {
	t.Parallel()
	// Multiplying before checking is how a very large number becomes a small
	// negative one, and a negative cap is a cap nothing applies. Refused with
	// a sentence rather than wrapped into a number no machine has.
	_, err := schema.ParseMemoryBytes("9000000000Gi")
	require.Error(t, err)
	require.Contains(t, err.Error(), "larger than any machine")
}

func TestFormat_WritesTheQuantityBackTheWayAManifestWouldHaveIt(t *testing.T) {
	t.Parallel()
	// For a message rather than for a manifest. A shortfall reported in
	// thousandths and bytes is a number the reader has to do arithmetic on
	// before it means anything, and the point of naming a shortfall is that
	// they can find the key it came from.
	require.Equal(t, "2", schema.FormatMilliCPU(2000))
	require.Equal(t, "500m", schema.FormatMilliCPU(500))
	require.Equal(t, "1m", schema.FormatMilliCPU(schema.MinMilliCPU))
	require.Equal(t, "2Gi", schema.FormatMemoryBytes(2*1024*1024*1024))
	require.Equal(t, "512Mi", schema.FormatMemoryBytes(512*1024*1024))
	require.Equal(t, "6Mi", schema.FormatMemoryBytes(schema.MinMemoryBytes))
	// Not a whole number of either unit. Rounded UP to the megabyte, because
	// a shortfall reported as 402653185 bytes tells the reader nothing.
	require.Equal(t, "385Mi", schema.FormatMemoryBytes(384*1024*1024+1))
}

func TestFormat_RoundTripsEveryQuantityItPrints(t *testing.T) {
	t.Parallel()
	// The property that makes the messages actionable: what a shortfall names
	// is a value the author can paste back into the manifest and have this
	// same parser accept.
	for _, milli := range []int64{1, 250, 500, 1000, 1500, 2000, 64000} {
		back, err := schema.ParseMilliCPU(schema.FormatMilliCPU(milli))
		require.NoError(t, err, "%dm printed as %q, which does not parse",
			milli, schema.FormatMilliCPU(milli))
		require.Equal(t, milli, back)
	}
	for _, bytes := range []int64{
		schema.MinMemoryBytes, 64 * 1024 * 1024, 512 * 1024 * 1024, 2 * 1024 * 1024 * 1024,
	} {
		back, err := schema.ParseMemoryBytes(schema.FormatMemoryBytes(bytes))
		require.NoError(t, err)
		require.Equal(t, bytes, back)
	}
}
