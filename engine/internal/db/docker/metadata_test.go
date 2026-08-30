package docker_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// A golden's metadata survives being read back.
//
// The regression, found by running the product: ListGoldens rebuilt every
// version from its image tag alone, so RulesHash came back empty and
// Attestation came back missing however carefully a refresh had filled them
// in. Two separate checks read those fields and both were silently inert: the
// rule that a branch may only be made from a golden produced under this
// manifest's masking rules, and the section of the pull request comment that
// tells a reviewer the data was proved masked.
//
// A field that does not survive a round trip is a field nothing can be built
// on, and neither of those checks would have failed a test that only exercised
// a refresh.
func TestGoldenMetadata_SurvivesARoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in short mode: this needs a Docker daemon")
	}
	p, err := dockerdb.New(dockerdb.Options{Version: 17, Clock: clock.New(), PortFrom: freePort(t)})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	const rules = "abc123def456"
	const attestation = `{"report":{"columns":84},"golden":"x","signature":"sig"}`

	gv, err := p.RefreshGolden(ctx, provider.GoldenSpec{
		Version: 17, RulesHash: rules,
		Mask: func(context.Context, secrets.Value) error { return nil },
		Verify: func(context.Context, secrets.Value) (string, error) {
			return attestation, nil
		},
	})
	require.NoError(t, err)
	defer func() {
		c, cancel2 := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel2()
		_ = p.DestroyGolden(c, gv.ID)
	}()

	require.Equal(t, rules, gv.RulesHash, "the refresh itself has to report it")

	// The part that was broken: a different process, asking the provider what
	// exists, rather than the struct the refresh happened to return.
	listed, err := p.ListGoldens(ctx)
	require.NoError(t, err)

	var found *provider.GoldenVersion
	for i := range listed {
		if listed[i].ID == gv.ID {
			found = &listed[i]
		}
	}
	require.NotNil(t, found, "the golden that was just published is not listed")
	require.Equal(t, rules, found.RulesHash,
		"the rules digest did not survive, so golden selection cannot use it")
	require.Equal(t, attestation, found.Attestation,
		"the attestation did not survive, so the comment cannot report the masking")
	require.True(t, found.Verified)
}

// freePort asks the kernel for a port nothing is using, so two suites running
// at once do not collide on a constant.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("skipped: no free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("closing the probe listener: %v", err)
	}
	return port
}
