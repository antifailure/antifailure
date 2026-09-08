package local

// Proving the refusal is WIRED, not merely present.
//
// ensureProxyImage returns early when the daemon already has the sidecar
// image, which is the ordinary case on any machine that has run this suite, so
// a sealed call to it on a warm machine returns nil and a test asserting a
// refusal would pass for a reason that has nothing to do with the refusal
// existing. That is the shape of a check that cannot say no.
//
// The tag is content addressed over proxyimage.Sources, so changing one entry
// produces a reference the daemon has never seen and the early return is gone.
// Nothing is built and nothing shared is removed: the refusal fires before the
// build, which is the whole point of putting it there.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/proxyimage"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

func TestAirGapped_TheSidecarImageIsRequiredRatherThanBuiltOnDemand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped: -short")
	}
	r, err := New(Options{Clock: clock.New()})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// A source the generator would never emit, restored immediately, so the
	// tag names an image nothing has ever built.
	const key = "airgap_test_marker.go"
	proxyimage.Sources[key] = "// only for this test\n"
	t.Cleanup(func() { delete(proxyimage.Sources, key) })
	absent := proxyimage.Tag()

	if _, err := r.cli.ImageInspect(context.Background(), absent); err == nil {
		t.Fatalf("%s already exists, so this test would have proved nothing", absent)
	}

	airgap.Reset()
	t.Cleanup(airgap.Reset)
	airgap.Seal("this test is measuring the refusal")

	err = r.ensureProxyImage(context.Background(), func(string) {})
	require.Error(t, err,
		"the sidecar's Dockerfile begins FROM golang:1.25-alpine, so building it on "+
			"demand is a pull from Docker Hub on the path of every af up")
	require.ErrorIs(t, err, airgap.ErrSealed)
	require.True(t, strings.Contains(err.Error(), "sidecar image"),
		"the refusal must name what it refused: %s", err)

	require.Len(t, airgap.Refusals(), 1)
	require.Equal(t, airgap.SiteImageBuild, airgap.Refusals()[0].Site)
}

func TestAirGapped_TheIngressForwarderImageIsRequiredToo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped: -short")
	}
	r, err := New(Options{Clock: clock.New()})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Unlike the sidecar this tag is a constant, so the early return cannot be
	// dodged and the live half of this is only reachable on a machine that has
	// never built it. When it is present the assertion is that the refusal
	// exists on the path rather than that it fired, and the test says so
	// rather than reporting a pass it did not earn.
	if _, err := r.cli.ImageInspect(context.Background(), ingressImage); err == nil {
		t.Skipf("skipped: %s is already on this daemon, so the build is not reached", ingressImage)
	}

	airgap.Reset()
	t.Cleanup(airgap.Reset)
	airgap.Seal("this test is measuring the refusal")

	err = r.ensureIngressImage(context.Background())
	require.ErrorIs(t, err, airgap.ErrSealed)
	require.Len(t, airgap.Refusals(), 1)
}
