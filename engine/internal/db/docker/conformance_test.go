package docker_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// TestConformance runs the shared suite against a real Docker daemon.
//
// It is the reference implementation's proof, and it runs against the real
// daemon rather than a fake on purpose: a fake would test that the provider
// agrees with our idea of Docker, and what matters is that it agrees with
// Docker.
func TestConformance(t *testing.T) {
	requireDocker(t)
	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := dockerdb.New(dockerdb.Options{
			Version:  17,
			Clock:    clock.New(),
			SeedSQL:  conformance.DefaultSeedSQL,
			PortFrom: 44000,
		})
		require.NoError(t, err)
		return p
	}, conformance.Options{
		Timeout: 4 * time.Minute,
		// The behaviors that create several branches are the slowest, and they
		// are also the ones that catch a provider whose branches share
		// storage, so they run unless the environment asks otherwise.
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	})
}

func requireDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	p, err := dockerdb.New(dockerdb.Options{Clock: clock.New()})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Inventory(ctx); err != nil {
		t.Skipf("skipped: the Docker daemon did not respond: %v", err)
	}
}
