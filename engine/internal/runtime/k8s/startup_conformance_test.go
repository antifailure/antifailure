package k8s_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/runtime/k8s"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Unlike the shared suite's governed wrapper, the customer's first command
// attempts direct UDP immediately. The only preceding gate is production's
// trusted init container, in this exact pod's network namespace.
func TestImmediateStartupCannotBypassContainment(t *testing.T) {
	kubeContext := requireCluster(t)
	loader, ok := k8s.LocalClusterLoaderFor(kubeContext)
	require.True(t, ok, "this destructive proof requires a disposable local cluster")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	ref, err := proxyImage(ctx)
	require.NoError(t, err)
	_, err = loader.Ensure(ctx, ref)
	require.NoError(t, err)
	require.NoError(t, pullLocally(ctx, conformance.DefaultShellImage))
	_, err = loader.Ensure(ctx, conformance.DefaultShellImage)
	require.NoError(t, err)
	kubectl := func(args ...string) []byte {
		t.Helper()
		args = append([]string{"--context", kubeContext, "--request-timeout=30s"}, args...)
		out, err := exec.CommandContext(ctx, "kubectl", args...).CombinedOutput()
		require.NoError(t, err, "%s", out)
		return out
	}
	envID := fmt.Sprintf("startup-%x", time.Now().UnixNano())
	controlNS := "af-control-" + envID
	kubectl("create", "namespace", controlNS)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		out, err := exec.CommandContext(cleanup, "kubectl", "--context", kubeContext, "delete", "namespace", controlNS, "--wait=true", "--timeout=90s").CombinedOutput()
		if err != nil {
			t.Errorf("removing owned startup control: %v: %s", err, out)
		}
	})
	// This must answer from the same cluster before denied traffic means
	// anything. A blocked public resolver cannot create a passing proof.
	kubectl("run", "direct-control", "-n", controlNS, "--image="+conformance.DefaultShellImage,
		"--override-type=strategic", `--overrides={"spec":{"automountServiceAccountToken":false,"containers":[{"name":"direct-control","securityContext":{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true,"runAsNonRoot":true,"runAsUser":65532,"capabilities":{"drop":["ALL"]}}}]}}`,
		"--restart=Never", "--command", "--", "sh", "-c", "nslookup example.com 1.1.1.1 && echo AF-PUBLIC-ANSWERED")
	kubectl("wait", "-n", controlNS, "pod/direct-control", "--for=jsonpath={.status.phase}=Succeeded", "--timeout=90s")
	require.Contains(t, string(kubectl("logs", "-n", controlNS, "direct-control")), "AF-PUBLIC-ANSWERED")

	r, err := k8s.New(k8s.Options{Context: kubeContext, ProxyImage: ref, Images: loader, ReadyTimeout: 3 * time.Minute, Clock: clock.New()})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, err := r.Down(cleanup, envID)
		if err != nil {
			t.Errorf("removing owned startup environment: %v", err)
		}
		_ = r.Close()
	})
	probe := "if nslookup example.com 1.1.1.1 >/dev/null 2>&1; then echo AF-STARTUP-ESCAPED; exit 42; fi; echo AF-STARTUP-DENIED"
	_, err = r.Up(ctx, provider.EnvSpec{EnvID: envID, Egress: &schema.Egress{Default: schema.ModeAllow}, Services: []provider.ServiceSpec{{
		Name: "prober", Kind: "worker", Image: conformance.DefaultShellImage, Replicas: 3,
		Command: probe + "; sleep 600", Migrate: probe,
	}}})
	require.NoError(t, err)
	namespace := k8s.DefaultNamespacePrefix + envID
	inspect := func() []string {
		t.Helper()
		var pods corev1.PodList
		require.NoError(t, json.Unmarshal(kubectl("get", "pods", "-n", namespace, "-l", k8s.LabelService+"=prober", "-o", "json"), &pods))
		require.Len(t, pods.Items, 3)
		var names []string
		for _, pod := range pods.Items {
			names = append(names, pod.Name)
			require.Len(t, pod.Status.InitContainerStatuses, 1)
			require.NotNil(t, pod.Status.InitContainerStatuses[0].State.Terminated)
			require.Zero(t, pod.Status.InitContainerStatuses[0].State.Terminated.ExitCode)
			logs := string(kubectl("logs", "-n", namespace, pod.Name, "-c", "app"))
			require.NotContains(t, logs, "AF-STARTUP-ESCAPED")
			require.Contains(t, logs, "AF-STARTUP-DENIED")
		}
		return names
	}
	// Running is earlier than the immediate DNS probe completing. Wait on
	// the application's marker, never inject a delay before its first packet.
	kubectl("wait", "-n", namespace, "deployment/prober", "--for=condition=Available", "--timeout=90s")
	waitLogs := func() {
		t.Helper()
		require.Eventually(t, func() bool {
			lines, err := r.Logs(ctx, envID, "prober", 100)
			if err != nil {
				return false
			}
			count := 0
			for _, line := range lines {
				if strings.Contains(line.Text, "AF-STARTUP-ESCAPED") {
					t.Error("a customer's first command escaped")
				}
				if strings.Contains(line.Text, "AF-STARTUP-DENIED") {
					count++
				}
			}
			return count >= 3
		}, 90*time.Second, time.Second)
	}
	waitLogs()
	names := inspect()
	// Replacements also need their own gate. A preflight performed by Up
	// cannot establish network readiness for a pod scheduled afterwards.
	kubectl("delete", "pod", "-n", namespace, names[0], "--wait=true", "--timeout=90s")
	kubectl("rollout", "status", "-n", namespace, "deployment/prober", "--timeout=120s")
	waitLogs()
	require.NotContains(t, inspect(), names[0])
	logs := string(kubectl("logs", "-n", namespace, "job/prober-migrate", "-c", "migrate"))
	require.Contains(t, logs, "AF-STARTUP-DENIED")
	require.NotContains(t, logs, "AF-STARTUP-ESCAPED")
}
