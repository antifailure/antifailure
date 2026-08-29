package local_test

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// TestConformance runs the shared runtime suite against a real Docker daemon.
//
// This is the reference runtime's proof, and it runs against the real daemon
// rather than a fake on purpose: a fake would test that the runtime agrees
// with our idea of Docker, and what matters is that it agrees with Docker. The
// suite itself is proved able to fail elsewhere, against a fake built to be
// broken one behavior at a time, which is the other half of the same argument.
func TestConformance(t *testing.T) {
	requireRuntime(t)

	conformance.RunRuntime(t, func(t *testing.T) provider.Runtime {
		r, err := local.New(local.Options{
			Clock: clock.New(), ReadyTimeout: 90 * time.Second,
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = r.Close() })
		return r
	}, conformance.RuntimeOptions{
		Timeout: 4 * time.Minute,
		// The local runtime never pulls: it runs images the engine built. The
		// suite's fixture is a public one, so it has to be here before a
		// container can be made from it, and pulling it once here is cheaper
		// and far clearer than a create that fails with "No such image".
		PrepareImage: pullIfMissing,
		SkipSlow:     os.Getenv("AF_SKIP_SLOW") != "",
	})
}

// pullIfMissing makes sure an image is on the daemon.
func pullIfMissing(ctx context.Context, ref string) error {
	cli, err := dockerutil.Client()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()

	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	rc, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	// The body has to be drained or the pull is cancelled when the reader is
	// closed, and the image is then still missing.
	_, err = io.Copy(io.Discard, rc)
	return err
}
