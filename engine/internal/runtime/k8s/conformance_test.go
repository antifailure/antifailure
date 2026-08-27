package k8s_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/runtime/k8s"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// kubeContextEnv names the cluster these tests run against.
//
// Required rather than defaulted to the current context, and that is the whole
// point of it. These tests create namespaces, delete namespaces, and run pods
// that try to reach the internet. Defaulting to whatever kubectl happens to be
// pointing at would eventually run them against somebody's real cluster, and
// the first sign would be a namespace disappearing.
const kubeContextEnv = "AF_KUBE_CONTEXT"

// TestConformance runs the shared runtime suite against a real cluster.
func TestConformance(t *testing.T) {
	kubeContext := requireCluster(t)
	loader, ok := k8s.LocalClusterLoaderFor(kubeContext)
	if !ok {
		// A real cluster pulls the sidecar from a registry, and this suite has
		// no way to put it in one. Saying so beats failing every behavior
		// with an image pull error.
		t.Skipf("skipped: %s is not a k3d or kind cluster, so the locally built "+
			"sidecar image cannot be copied into it", kubeContext)
	}

	conformance.RunRuntime(t, func(t *testing.T) provider.Runtime {
		r, err := k8s.New(k8s.Options{
			Context: kubeContext,
			// Resolved on demand, exactly as the engine does it, rather than
			// built once at the top of the run. Built once is what this did
			// first, and on a shared machine the image was pruned by another
			// process between the build and the first Up, which arrived as
			// k3d refusing to import an image that was not there any more.
			// Resolving per environment rebuilds it if it has gone.
			ResolveProxyImage: proxyImage,
			Images:            loader,
			ReadyTimeout:      3 * time.Minute,
			Clock:             clock.New(),
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = r.Close() })
		return r
	}, conformance.RuntimeOptions{
		Timeout: 6 * time.Minute,
		// Every image the suite runs has to be inside the cluster's nodes
		// before a pod can be made from it, and on a development cluster that
		// is a copy from the local daemon rather than a pull.
		PrepareImage: func(ctx context.Context, ref string) error {
			if err := pullLocally(ctx, ref); err != nil {
				return err
			}
			_, err := loader.Ensure(ctx, ref)
			return err
		},
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
	})
}

// requireCluster skips unless a cluster has been named for these tests.
func requireCluster(t *testing.T) string {
	t.Helper()
	kubeContext := os.Getenv(kubeContextEnv)
	if kubeContext == "" {
		t.Skipf("skipped: set %s to the kubeconfig context of a throwaway cluster. "+
			"These tests create and delete namespaces and run pods that try to reach "+
			"the internet, so they are never run against a cluster by accident",
			kubeContextEnv)
	}
	// Retried rather than asked once. A cluster under load answers slowly
	// before it answers at all, and a guard that skips on the first timeout
	// turns "this machine is busy" into "these tests did not run", which is
	// the failure mode where a suite quietly stops covering anything.
	var out []byte
	var err error
	deadline := time.Now().Add(3 * time.Minute)
	for {
		out, err = exec.Command("kubectl", "--context", kubeContext,
			"--request-timeout=30s", "get", "nodes").CombinedOutput()
		if err == nil {
			return kubeContext
		}
		if time.Now().After(deadline) {
			t.Skipf("skipped: %s did not answer within three minutes: %v: %s",
				kubeContext, err, strings.TrimSpace(string(out)))
		}
		t.Logf("waiting for %s to answer: %s", kubeContext, strings.TrimSpace(string(out)))
		time.Sleep(10 * time.Second)
	}
}

// proxyImage builds the sidecar on the local daemon, which is the only thing
// that can build it, and is called whenever a runtime needs it.
func proxyImage(ctx context.Context) (string, error) {
	r, err := local.New(local.Options{Clock: clock.New()})
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	return r.EnsureProxyImage(ctx, func(string) {})
}

// pullLocally makes sure an image is on the local daemon, so that it can be
// copied into the cluster from there.
func pullLocally(ctx context.Context, ref string) error {
	if out, err := exec.CommandContext(ctx, "docker", "image", "inspect", ref).CombinedOutput(); err == nil {
		_ = out
		return nil
	}
	out, err := exec.CommandContext(ctx, "docker", "pull", ref).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pulling %s: %v: %s", ref, err, strings.TrimSpace(string(out)))
	}
	return nil
}
