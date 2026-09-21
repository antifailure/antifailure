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
		// A real Docker daemon, and the branch time this suite measures is the
		// daemon's storage driver doing the work rather than a stand in for it.
		// The other credential free suite, and the one on the true side of the
		// assertion.
		RealService: "a real Docker daemon on this machine",
		Timeout:     4 * time.Minute,
		// The behaviors that create several branches are the slowest, and they
		// are also the ones that catch a provider whose branches share
		// storage, so they run unless the environment asks otherwise.
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	})
}

func requireDocker(t *testing.T) {
	t.Helper()
	asked.Add(1)
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		skipped.Add(1)
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	p, err := dockerdb.New(dockerdb.Options{Clock: clock.New()})
	if err != nil {
		skipped.Add(1)
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	defer func() { _ = p.Close() }()

	// Ninety seconds, not ten. This is the probe that decides whether the
	// whole database conformance suite runs, and ten seconds is shorter than a
	// loaded daemon takes to answer. Measured on 2026-09-21 on a machine
	// shared by several lanes: with 118 containers on the daemon this probe
	// timed out at ten seconds six times in one session, while the daemon was
	// still serving other work and answered normally within the minute either
	// side. Every one of those runs skipped the entire suite and exited 0.
	//
	// A daemon that is genuinely absent still fails fast, because a refused
	// connection is immediate rather than a timeout, so the larger budget
	// costs nothing in the case it is meant to detect and buys the case that
	// was being misread as that one. A guard whose budget is shorter than the
	// thing it measures does not detect a missing daemon, it manufactures one.
	//
	// The sibling package engine/internal/runtime/local carries the same
	// number for the same reason, measured separately at 250 containers.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := p.Inventory(ctx); err != nil {
		skipped.Add(1)
		t.Skipf("skipped: the Docker daemon did not respond: %v", err)
	}
}
