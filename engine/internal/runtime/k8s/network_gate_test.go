package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestEveryCustomerPodStartsWithTheTrustedNetworkGate(t *testing.T) {
	r := &Runtime{proxyRef: "trusted-sidecar:build"}
	s := provider.ServiceSpec{Name: "app", Image: "customer-image:no-shell", Replicas: 3, CPUMillis: 500, MemoryBytes: 128 << 20}
	spec := provider.EnvSpec{EnvID: "gate", Services: []provider.ServiceSpec{s}}
	d := r.deploymentFor(spec, s, "gate", "10.43.0.9")
	require.EqualValues(t, 3, *d.Spec.Replicas)
	pods := map[string]corev1.PodSpec{
		"service and replicas": d.Spec.Template.Spec,
		"migration":            r.migrationJob(spec, s, "gate", "10.43.0.9").Spec.Template.Spec,
		"stance":               r.stanceJob(spec, s, provider.StanceJob{Store: "search", Command: "rebuild"}, "gate", "10.43.0.9").Spec.Template.Spec,
	}
	for name, pod := range pods {
		t.Run(name, func(t *testing.T) {
			require.Len(t, pod.InitContainers, 1)
			gate := pod.InitContainers[0]
			require.Equal(t, corev1.TerminationMessageFallbackToLogsOnError, gate.TerminationMessagePolicy)
			require.Equal(t, "trusted-sidecar:build", gate.Image)
			require.Equal(t, []string{"/af-proxy"}, gate.Command)
			require.Equal(t, []string{"-network-gate", "-gate-control", "10.43.0.9:3128"}, gate.Args)
			require.Empty(t, gate.Env)
			require.Empty(t, gate.EnvFrom)
			require.Empty(t, gate.VolumeMounts)
			require.False(t, *pod.AutomountServiceAccountToken)
			require.False(t, *gate.SecurityContext.AllowPrivilegeEscalation)
			require.True(t, *gate.SecurityContext.ReadOnlyRootFilesystem)
			require.True(t, *gate.SecurityContext.RunAsNonRoot)
			require.EqualValues(t, 65532, *gate.SecurityContext.RunAsUser)
			require.Equal(t, &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}, gate.SecurityContext.Capabilities)
			require.Equal(t, pod.Containers[0].Resources, gate.Resources)
		})
	}
}

func TestKubernetesContainersUseRuntimeDefaultSeccomp(t *testing.T) {
	r := &Runtime{proxyRef: "trusted-sidecar:build"}
	s := provider.ServiceSpec{Name: "app", Image: "customer-image:existing-user"}
	spec := provider.EnvSpec{EnvID: "gate", Services: []provider.ServiceSpec{s}}
	_, proxy, _, err := r.proxyObjects(context.Background(), "gate", "gate", "10.42.0.0/16", "10.43.0.10", spec)
	require.NoError(t, err)
	containers := map[string]corev1.Container{
		"proxy":              proxy.Spec.Template.Spec.Containers[0],
		"preflight and init": networkGateContainer("trusted-sidecar:build", "10.43.0.9"),
		"app":                r.deploymentFor(spec, s, "gate", "10.43.0.9").Spec.Template.Spec.Containers[0],
		"migration":          r.migrationJob(spec, s, "gate", "10.43.0.9").Spec.Template.Spec.Containers[0],
		"stance":             r.stanceJob(spec, s, provider.StanceJob{Store: "search", Command: "rebuild"}, "gate", "10.43.0.9").Spec.Template.Spec.Containers[0],
	}
	for name, container := range containers {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}, container.SecurityContext.SeccompProfile)
			if container.Image == s.Image {
				require.Nil(t, container.SecurityContext.RunAsUser, "the runtime must preserve the customer image's user")
			}
		})
	}
}

func containmentAPIServer(t *testing.T, phase corev1.PodPhase, logs string) (*Runtime, <-chan corev1.Pod) {
	t.Helper()
	created := make(chan corev1.Pod, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/log") {
			_, _ = fmt.Fprint(w, logs)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost:
			var pod corev1.Pod
			if err := json.NewDecoder(req.Body).Decode(&pod); err != nil {
				http.Error(w, "invalid pod", 400)
				return
			}
			created <- pod
			_ = json.NewEncoder(w).Encode(pod)
		case strings.Contains(req.URL.Path, "/services/"):
			_ = json.NewEncoder(w).Encode(corev1.Service{Spec: corev1.ServiceSpec{ClusterIP: "10.43.0.9"}})
		default:
			_ = json.NewEncoder(w).Encode(corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: probeName}, Status: corev1.PodStatus{Phase: phase}})
		}
	}))
	t.Cleanup(server.Close)
	config := &rest.Config{Host: server.URL, ContentConfig: rest.ContentConfig{ContentType: "application/json", AcceptContentTypes: "application/json"}}
	client, err := kubernetes.NewForConfig(config)
	require.NoError(t, err)
	return &Runtime{cli: client, rest: config, proxyRef: "trusted-sidecar:build", readyWait: time.Second}, created
}

func TestNamespaceContainmentRunsTrustedCodeWithARealSidecarControl(t *testing.T) {
	r, created := containmentAPIServer(t, corev1.PodSucceeded, containmentMarker+" contained\n")
	err := r.verifyContainment(context.Background(), provider.EnvSpec{EnvID: "gate", Services: []provider.ServiceSpec{{Image: "malicious-shell:fake-verdict"}}}, "gate", "10.43.0.10", func(string) {})
	require.NoError(t, err)
	pod := <-created
	require.Equal(t, networkGateContainer("trusted-sidecar:build", "10.43.0.9"), pod.Spec.Containers[0])
}

func TestContainmentRequiresExactSuccessAndSuccessfulExit(t *testing.T) {
	for _, test := range []struct {
		name    string
		phase   corev1.PodPhase
		logs    string
		success bool
	}{
		{"complete", corev1.PodSucceeded, "AF-CONTAINMENT contained\n", true},
		{"failed after marker", corev1.PodFailed, "AF-CONTAINMENT contained\n", false},
		{"unknown verdict", corev1.PodSucceeded, "AF-CONTAINMENT unknown\n", false},
		{"missing utilities", corev1.PodFailed, "nslookup: not found\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, _ := containmentAPIServer(t, test.phase, test.logs)
			verdict, err := r.awaitProbe(context.Background(), "gate")
			if test.success {
				require.NoError(t, err)
				require.Equal(t, "contained", verdict)
			} else {
				require.Error(t, err)
			}
		})
	}
}
