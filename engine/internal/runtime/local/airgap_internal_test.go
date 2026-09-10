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

	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
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

	// Asked through ImagePresent rather than inline, because the inline form
	// read EVERY error as absence and this precondition is the only thing
	// standing between a poisoned daemon and a test that proves nothing. It
	// waved exactly that through once: the image was present, a busy daemon
	// answered the inspect with something that was not a not found, the
	// precondition read it as absence, and the assertion below failed nine
	// lines later saying an error was expected.
	present, err := dockerutil.ImagePresent(context.Background(), r.cli, absent)
	require.NoError(t, err,
		"the daemon could not say whether %s exists, so this test was NOT run rather "+
			"than passed", absent)
	if present {
		// Removed rather than refused. A previous mutation run that deleted the
		// refusal would have BUILT this image, and restoring the source does not
		// remove what the broken code made, so a bare failure here leaves every
		// later run failing for a reason that has nothing to do with the code.
		// The tag is content addressed over a marker only this test injects, so
		// nothing else can own it.
		t.Logf("%s exists, which only a previous run of this test can have built; removing it", absent)
		_, rmErr := r.cli.ImageRemove(context.Background(), absent, image.RemoveOptions{Force: true})
		require.NoErrorf(t, rmErr, "%s exists and could not be removed, so this test would have proved nothing", absent)
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
	// The same two valued read, and the same fix. Here the consequence of
	// guessing is a SKIP, which is the quieter of the two failures: a daemon
	// that could not answer would report this test as not applicable rather
	// than as not run, and a skip reads like a pass in every summary view.
	present, err := dockerutil.ImagePresent(context.Background(), r.cli, ingressImage)
	require.NoError(t, err,
		"the daemon could not say whether %s exists, so this test was NOT run rather "+
			"than skipped for a known reason", ingressImage)
	if present {
		t.Skipf("skipped: %s is already on this daemon, so the build is not reached", ingressImage)
	}

	airgap.Reset()
	t.Cleanup(airgap.Reset)
	airgap.Seal("this test is measuring the refusal")

	err = r.ensureIngressImage(context.Background())
	require.ErrorIs(t, err, airgap.ErrSealed)
	require.Len(t, airgap.Refusals(), 1)
}
