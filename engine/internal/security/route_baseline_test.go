package security_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	"github.com/antifailure/antifailure/engine/internal/security"
)

// baselineReaderFamily is a fakeFamily that also reads a base twin: it satisfies
// security.BaselineReader on top of the Family interface, the shape side_effect
// has. It is what lets the collector's worth-it gate be tested against both a
// family that wants a baseline and one that does not.
type baselineReaderFamily struct{ fakeFamily }

func (baselineReaderFamily) ReadsBaseline() {}

func TestSelectionsWantBaseline_TrueOnlyWhenABaselineReaderIsSelected(t *testing.T) {
	t.Parallel()

	// A selected family that reads a baseline makes a base twin worth building.
	reader := baselineReaderFamily{fakeFamily{
		name: "side_effect", surfaces: []change.Surface{change.SurfaceCode},
	}}
	regReader := security.NewRegistry()
	regReader.Register(reader)
	require.True(t,
		security.SelectionsWantBaseline(security.Select(regReader, codeAndAuthProfile())),
		"a selected baseline reader makes the second environment worth building")

	// A selected family that does NOT read a baseline does not: the whole point
	// of the gate is that a docs or plain-code change pays for no base twin.
	regPlain := security.NewRegistry()
	regPlain.Register(headersFamily()) // code, not a baseline reader
	require.False(t,
		security.SelectionsWantBaseline(security.Select(regPlain, codeAndAuthProfile())),
		"a change that routes no baseline reader must not build a base twin")

	// No selections at all wants nothing.
	require.False(t, security.SelectionsWantBaseline(nil), "no selections, no base twin")
}
