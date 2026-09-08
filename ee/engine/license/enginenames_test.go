package license_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/license"
	"github.com/antifailure/antifailure/engine/pkg/edition"
)

// The gate the engine applies is keyed on a string, and this is what keeps that
// string attached to the licence.
//
// engine/pkg/edition cannot import this package: the community build has no way
// to resolve the enterprise module, which is the whole reason edition carries
// rendered strings rather than licence types. So the two spellings of
// multi_runtime are written out twice, and two spellings that can drift apart
// silently is how a licensed feature becomes free. A rename on either side
// fails here.
//
// It is a comparison rather than a claim about behaviour, and it is not a
// substitute for the placement tests that turn the entitlement off and watch
// the refusal. What it catches is the one thing those cannot: a rename that
// leaves both sides internally consistent and no longer talking to each other.
func TestTheEngineGatesOnTheNameThisLicenceSells(t *testing.T) {
	t.Parallel()
	require.Equal(t, string(license.FeatureMultiRuntime), edition.FeatureMultiRuntime,
		"the engine gates placement on edition.FeatureMultiRuntime and the licence "+
			"grants license.FeatureMultiRuntime; if these differ the gate reads a "+
			"feature no licence can carry, and placement is refused for everyone")
}

// And the other direction: a feature the engine names has to be one a licence
// can actually grant, or the gate is closed against every customer.
func TestTheNameTheEngineGatesOnIsOneALicenceCanCarry(t *testing.T) {
	t.Parallel()
	found := false
	for _, f := range license.AllFeatures() {
		if string(f) == edition.FeatureMultiRuntime {
			found = true
		}
	}
	require.True(t, found,
		"edition.FeatureMultiRuntime is not in license.AllFeatures(), so no licence "+
			"issued by this product could ever turn placement on")
}
