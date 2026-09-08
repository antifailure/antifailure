package local_test

// The number this lane owes, measured on a real environment rather than
// reasoned about from the source.
//
// Air gapped is a claim about ABSENCE, which is the hardest kind to prove and
// the easiest to fake. A test that asserts a configuration flag is set proves
// nothing at all, and a test that asserts a list of call sites were converted
// proves that a list was converted. What has to be true is that a full
// lifecycle, from nothing to a running environment and back to nothing, makes
// no connection outside the operator's own network. So this runs one, with the
// guard sealed, and reads the ledger afterwards.
//
// THE ASSERTION THAT ZERO REFUSALS IS NOT ENOUGH ON ITS OWN. A ledger that
// recorded nothing would report zero refusals too, and it would report zero
// refusals on a build where the guard was never on the path, where the seal
// never took, and where the lifecycle never ran. Every one of those reads
// exactly like a clean result. So the test also requires that the guard was
// REACHED: the readiness probe is a guarded site on the up path, and its
// permitted attempt has to be in the ledger. Zero refusals out of zero
// observations is not a measurement.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestAirGapped_AFullLifecycleMakesNoConnectionOutsideTheOperatorsNetwork(t *testing.T) {
	r := requireRuntime(t)

	// Built BEFORE the seal, and that is the mode rather than a convenience.
	// An air gapped installation loads its images from a tarball or an
	// internal registry; what the mode refuses is the silent reach for Docker
	// Hub on a machine somebody believed had no route out.
	img := tinyWebImage(t, 8080, "air gapped")
	id := envID(t, r, "airgap1")

	warm, cancelWarm := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelWarm()
	spec := provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{{
		Name: "web", Image: img, Kind: "web", Port: 8080,
	}}}
	// One unsealed lifecycle first, so the sidecar and forwarder images exist
	// the way they would on an installation that pre-published them. Without
	// this the sealed run would be refused at the image build, which is
	// correct behaviour and a different test.
	_, err := r.Up(warm, spec)
	require.NoError(t, err, "the warm up lifecycle must succeed, or the sealed one proves nothing")
	_, err = r.Down(warm, id)
	require.NoError(t, err)

	airgap.Reset()
	t.Cleanup(airgap.Reset)
	airgap.Seal("this test is measuring a sealed lifecycle")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	env, err := r.Up(ctx, spec)
	require.NoError(t, err, "a sealed installation must still be able to bring an environment up")
	require.True(t, env.Services[0].Ready)
	require.Equal(t, "air gapped", get(t, env.URL()))

	td, err := r.Down(ctx, id)
	require.NoError(t, err)
	require.Empty(t, td.Pending)

	attempts := airgap.Attempts()
	for _, a := range attempts {
		t.Log(a.String())
	}
	t.Logf("outbound connection attempts during a full sealed lifecycle: %d observed, %d refused",
		len(attempts), len(airgap.Refusals()))

	require.Empty(t, airgap.Refusals(),
		"a sealed lifecycle reached outside the operator's network, and every entry here "+
			"is a path this mode was sold as closing")

	var probes int
	for _, a := range attempts {
		if a.Site == airgap.SiteServiceProbe {
			probes++
			require.Falsef(t, a.Refused,
				"the readiness probe reaches a container on loopback and must never be refused: %s", a)
		}
	}
	require.Greaterf(t, probes, 0,
		"the ledger recorded no readiness probe, so the guard was not on the lifecycle's path "+
			"and the zero above is zero observations rather than zero escapes")
}
