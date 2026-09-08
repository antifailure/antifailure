package fidelity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The article in front of an engine name.
//
// An internal test rather than one through the report, because the report only
// ever reaches this with the handful of engines a fixture declares, and the
// engine key is free text. A report that says "a elasticsearch" is one
// somebody stops reading, and the two vowel names in the recognition table
// beside this file are exactly the ones a fixture is least likely to carry.
func TestTheArticleMatchesTheEngineName(t *testing.T) {
	t.Parallel()
	require.Equal(t, "a ", article("clickhouse"))
	require.Equal(t, "a ", article("redis"))
	require.Equal(t, "a ", article("kafka"))
	require.Equal(t, "an ", article("elasticsearch"))
	require.Equal(t, "an ", article("influxdb"))
	require.Equal(t, "an ", article("Opensearch"))
	// Empty never reaches a reader through the report, since a datastore with
	// no engine is refused, and answering "an " for it would be a sentence
	// with a hole in it either way.
	require.Equal(t, "a ", article(""))
}
