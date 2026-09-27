package local

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDecisionsCompleteStreamKeepsOlderEvents(t *testing.T) {
	var log strings.Builder
	for i := 0; i < 601; i++ {
		if i == 0 {
			log.WriteString("{\"event\":\"decision\",\"host\":\"early.example\"}\n")
		} else {
			log.WriteString("{\"event\":\"decision\",\"host\":\"later.example\"}\n")
		}
	}
	all, err := parseDecisions([]byte(log.String()), -1)
	require.NoError(t, err)
	require.Len(t, all, 601)
	require.Equal(t, "early.example", all[0].Host)
	bounded, err := parseDecisions([]byte(log.String()), 500)
	require.NoError(t, err)
	require.Len(t, bounded, 500)
	require.Equal(t, "later.example", bounded[0].Host)
}

func TestParseDecisionsRejectsIncompleteEvidence(t *testing.T) {
	_, err := parseDecisions([]byte("{\"event\":\"decision\"}\n{\"event\":"), -1)
	require.ErrorContains(t, err, "incomplete JSON")
}
