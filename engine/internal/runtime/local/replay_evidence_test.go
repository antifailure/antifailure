package local

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplayEvidenceDoesNotTailAwayAnEarlierRefusal(t *testing.T) {
	var log strings.Builder
	for n := 1; n <= 700; n++ {
		_, err := fmt.Fprintf(&log, "{\"event\":\"decision\",\"seq\":%d,\"at\":\"2026-09-27T00:00:00Z\",\"mode\":\"block\"}\n", n)
		require.NoError(t, err)
	}
	decisions, err := replayDecisions(log.String())
	require.NoError(t, err)
	require.Len(t, decisions, 700)
	require.Equal(t, uint64(1), decisions[0].Seq)
}
func TestReplayEvidenceRefusesMalformedMissingAndDuplicateRecords(t *testing.T) {
	for _, log := range []string{
		`{"event":"decision",`,
		`{"event":"decision","seq":2,"at":"2026-09-27T00:00:00Z"}`,
		"{\"event\":\"message\",\"seq\":1}\n{\"event\":\"message\",\"seq\":1}",
		`{"event":"decision","seq":1,"at":"not-a-time"}`,
	} {
		_, err := replayDecisions(log)
		require.Error(t, err)
	}
}
func TestReplayEvidenceAllowsInterleavedMessageSequences(t *testing.T) {
	log := "{\"event\":\"message\",\"seq\":2}\n{\"event\":\"decision\",\"seq\":1,\"at\":\"2026-09-27T00:00:00Z\"}"
	decisions, err := replayDecisions(log)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
}
