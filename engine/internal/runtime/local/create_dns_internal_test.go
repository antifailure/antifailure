package local

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// HostConfig.DNS became a slice of netip.Addr in the moby client, and the
// sidecar's address reaches create as a string. Dropping an address that does
// not parse would leave the service on Docker's own resolver, which answers for
// the whole internet and so resolves past the sidecar and out of the
// environment. A create given one therefore fails, and it fails before the
// daemon is asked for anything: this Runtime has no client, so a create that
// carried on would panic rather than pass.
func TestCreate_ASidecarAddressThatIsNotAnIPFailsTheCreate(t *testing.T) {
	t.Parallel()
	r := &Runtime{clock: clock.New()}
	_, err := r.create(context.Background(),
		provider.EnvSpec{EnvID: "env-dns"},
		provider.ServiceSpec{Name: "web", Image: "example/web:1"},
		networks{}, "sidecar.internal", "af-svc-env-dns-web", "", nil)
	require.ErrorIs(t, err, aferrors.Coded(aferrors.AFRUN040),
		"an address that is not an IP must fail the create, not be dropped from it")
	require.ErrorContains(t, err, "sidecar.internal", "and it must name the address it refused")
}
