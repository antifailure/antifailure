package masking

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/verify"
)

// The masking classifier and the verification scanner each carry a list of
// the types whose text form cannot hold a sentence, and they carry it twice
// on purpose: verify must not import masking, because it is the check on
// masking. This is what keeps the two lists the same list. A type masking
// calls structural and verify would read, or the other way round, is a
// column one instrument is silent about while the other speaks.
func TestKnownStructural_AgreesWithTheScanner(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{
		"smallint", "integer", "bigint", "decimal", "numeric", "real",
		"double precision", "money", "smallserial", "serial", "bigserial",
		"boolean", "uuid", "date", "time", "time without time zone",
		"time with time zone", "timestamp", "timestamp without time zone",
		"timestamp with time zone", "interval", "oid", "bit", "bit varying",
	} {
		require.True(t, knownStructural(ColumnInfo{Type: typ}), typ)
		require.Equal(t, "structural", verify.KindOf(typ), typ)
	}
	// And the types the comment on knownStructural says are deliberately
	// not there are ones the scanner reads or lists, never ignores.
	for _, typ := range []string{"bytea", "inet", "cidr", "macaddr", "tsvector", "ARRAY", "USER-DEFINED", "point"} {
		require.False(t, knownStructural(ColumnInfo{Type: typ}), typ)
		require.NotEqual(t, "structural", verify.KindOf(typ), typ)
	}
}
