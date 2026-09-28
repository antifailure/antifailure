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

func TestReplayEvidenceRequiresIndependentFinalWatermark(t *testing.T) {
	body := "{\"event\":\"ready\",\"seq\":1}\n"
	clean := replayWatermark{Version: 1, Env: "test-env", Instance: strings.Repeat("a", 32), Sequence: 1}
	decisions, err := verifiedReplayDecisions(body, clean, clean)
	require.NoError(t, err)
	require.Empty(t, decisions)
	tests := []struct {
		name          string
		before, after replayWatermark
	}{
		{"final failed record", replayWatermark{Version: 1, Env: clean.Env, Instance: clean.Instance, Sequence: 2, Failed: true}, replayWatermark{Version: 1, Env: clean.Env, Instance: clean.Instance, Sequence: 2, Failed: true}},
		{"final record lost by log collector", replayWatermark{Version: 1, Env: clean.Env, Instance: clean.Instance, Sequence: 2}, replayWatermark{Version: 1, Env: clean.Env, Instance: clean.Instance, Sequence: 2}},
		{"writer changes during read", clean, replayWatermark{Version: 1, Env: clean.Env, Instance: clean.Instance, Sequence: 2}},
		{"proxy restarts during read", clean, replayWatermark{Version: 1, Env: clean.Env, Instance: strings.Repeat("b", 32), Sequence: 1}},
		{"absent control", replayWatermark{}, replayWatermark{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := verifiedReplayDecisions(body, test.before, test.after)
			require.Error(t, err)
		})
	}
	_, err = verifiedReplayDecisions("", clean, clean)
	require.Error(t, err, "an empty or rotated log cannot claim the known watermark")
}

func TestReplayEvidenceRefusesAcceptedButUnloggedWork(t *testing.T) {
	body := "{\"event\":\"ready\",\"seq\":1}\n"
	clean := replayWatermark{Version: 1, Env: "env", Instance: strings.Repeat("a", 32), Sequence: 1}
	active := clean
	active.Active = 1
	active.Work = 1
	_, err := verifiedReplayDecisions(body, active, active)
	require.Error(t, err)
	finished := clean
	finished.Work = 1
	_, err = verifiedReplayDecisions(body, clean, finished)
	require.Error(t, err, "work admitted during the log read must invalidate the read even without a new log record")
}
