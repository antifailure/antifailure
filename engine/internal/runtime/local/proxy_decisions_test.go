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

// A sidecar that was read and logged only its own startup holds no decision.
// That is an observation, an application that reached for nothing, and nil is
// what every reader takes to mean the log could not be read at all.
func TestParseDecisionsReadButEmptyIsNotAbsent(t *testing.T) {
	log := "{\"event\":\"ready\",\"rules\":0}\nlistening on :3128\n"
	got, err := parseDecisions([]byte(log), -1)
	require.NoError(t, err)
	require.NotNil(t, got, "a log that was read must not answer nil")
	require.Empty(t, got)

	got, err = parseDecisions(nil, 200)
	require.NoError(t, err)
	require.NotNil(t, got, "an empty log that was read must not answer nil")
}

func TestSidecarLinesFromAnEmptyLogIsNotAbsent(t *testing.T) {
	require.NotNil(t, sidecarLinesFrom(nil))
	require.NotNil(t, sidecarLinesFrom([]byte("listening on :3128\n")))
	require.Equal(t, []string{"{\"event\":\"x\"}"}, sidecarLinesFrom([]byte("noise\n {\"event\":\"x\"} \n")))
}

func TestParseMessagesKeepsAbsentAndEmptyApart(t *testing.T) {
	require.Nil(t, parseMessages(nil, 100), "no sidecar to read stays absent")

	got := parseMessages([]string{}, 100)
	require.NotNil(t, got, "a sidecar read with nothing in it is an empty list")
	require.Empty(t, got)

	got = parseMessages([]string{"{\"event\":\"decision\",\"host\":\"a\"}"}, 100)
	require.NotNil(t, got, "a log of decisions holds no message, which is still a reading")
	require.Empty(t, got)

	got = parseMessages([]string{
		"{\"event\":\"message\",\"subject\":\"one\"}",
		"{\"event\":\"message\",\"subject\":\"two\"}",
	}, 1)
	require.Len(t, got, 1)
	require.Equal(t, "two", got[0].Subject)
}
