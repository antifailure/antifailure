package local_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/security"
	"github.com/antifailure/antifailure/engine/internal/security/sideeffect"
	"github.com/antifailure/antifailure/engine/internal/security/ssrf"
)

// The defect, end to end through the readers that consume the parsers. A run
// whose application called nothing produced a sidecar log with no decision and
// no message in it; both parsers answered nil; and the two families that read
// them took nil as "could not read" and reached no verdict. af ci then said the
// ssrf and side_effect families could not complete, about an application that
// had done exactly what its egress policy asked.
func TestAReadButEmptySidecarLogIsACleanLookForTheFamilies(t *testing.T) {
	log := []byte("{\"event\":\"ready\",\"rules\":0}\n")
	decisions, err := local.ParseDecisions(log, -1)
	require.NoError(t, err)
	messages := local.ParseMessages(local.SidecarLinesFrom(log), 100)

	in := security.Input{}.WithRunArtifacts(security.RunArtifacts{
		Decisions: decisions,
		Messages:  messages,
	})

	findings, err := ssrf.New().Probe(context.Background(), in)
	require.NoError(t, err, "ssrf must look at an empty log, not refuse it")
	require.Empty(t, findings)

	findings, err = sideeffect.New().Probe(context.Background(), in)
	require.NoError(t, err, "side_effect must look at an empty log, not refuse it")
	require.Empty(t, findings)
}

// The other half, so the fix cannot be the families giving up on nil: a run
// whose logs were never read must still be refused by both.
func TestAnUnreadSidecarLogIsStillRefused(t *testing.T) {
	in := security.Input{}.WithRunArtifacts(security.RunArtifacts{
		Decisions: nil,
		Messages:  local.ParseMessages(nil, 100),
	})
	_, err := ssrf.New().Probe(context.Background(), in)
	require.Error(t, err)
	_, err = sideeffect.New().Probe(context.Background(), in)
	require.Error(t, err)
}
