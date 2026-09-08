package k8s

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The requirement a container carries, which was the empty struct on every
// container this runtime ever created.
//
// With no request the scheduler has nothing to place against, so a ClickHouse
// and an application land wherever they fall and one environment starves
// another. The symptom is a workflow that reads as flaky, which is the failure
// this product exists to tell apart from the change under test.

const gib = 1024 * 1024 * 1024

func TestAContainerCarriesTheSizeTheManifestAsked(t *testing.T) {
	got := resourcesFor(provider.ServiceSpec{
		Name: "clickhouse", CPUMillis: 2000, MemoryBytes: 4 * gib,
	})
	require.Equal(t, "2", got.Requests.Cpu().String())
	require.Equal(t, "4Gi", got.Requests.Memory().String())
}

func TestTheRequestAndTheLimitAreTheSameFigure(t *testing.T) {
	got := resourcesFor(provider.ServiceSpec{Name: "web", CPUMillis: 500, MemoryBytes: gib})

	// Guaranteed quality of service, on purpose. The gap between a request and
	// a larger limit is where a node is oversubscribed: every pod is placed
	// against its request and may then grow into its limit, so a node that
	// fits ten environments on paper runs eleven and the eleventh takes memory
	// from the others. A twin whose failures belong to the machine rather than
	// to the change under test is worth less than no twin.
	require.Equal(t, got.Requests.Cpu().MilliValue(), got.Limits.Cpu().MilliValue())
	require.Equal(t, got.Requests.Memory().Value(), got.Limits.Memory().Value())
}

func TestTheRequestsAndTheLimitsAreNotTheSameMap(t *testing.T) {
	got := resourcesFor(provider.ServiceSpec{Name: "web", CPUMillis: 500, MemoryBytes: gib})

	// A ResourceList is a map, so one shared between both fields would mean
	// anything editing the limits silently edited the requests. The pod would
	// then leave the Guaranteed class without anything saying so.
	delete(got.Limits, corev1.ResourceMemory)
	require.Contains(t, got.Requests, corev1.ResourceMemory,
		"editing the limits changed the requests, so the two fields alias one map")
}

func TestADimensionTheManifestDidNotNameIsOmittedFromBothMaps(t *testing.T) {
	got := resourcesFor(provider.ServiceSpec{Name: "web", MemoryBytes: gib})

	// Omitted, not zeroed. A zero request is not "no request", it is a request
	// for nothing, and a zero limit is a limit of nothing: the API server
	// accepts the first and the kubelet refuses to run the second. Omitting is
	// what "the manifest said nothing" has to mean.
	require.NotContains(t, got.Requests, corev1.ResourceCPU)
	require.NotContains(t, got.Limits, corev1.ResourceCPU)
	require.Contains(t, got.Requests, corev1.ResourceMemory)
}

func TestAServiceThatNamedNoSizeGetsTheRequirementItAlwaysGot(t *testing.T) {
	got := resourcesFor(provider.ServiceSpec{Name: "web", Port: 8080})

	// The compatibility claim, stated as a test. Every manifest in every
	// existing repository arrives here with zeroes, and each has to produce
	// byte for byte the Deployment it produced before this key was honoured.
	require.Empty(t, got.Requests)
	require.Empty(t, got.Limits)
	require.Equal(t, corev1.ResourceRequirements{}, got)
}

func TestTheDeploymentCarriesTheRequirementRatherThanTheSpec(t *testing.T) {
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec := provider.EnvSpec{EnvID: "e1", Services: []provider.ServiceSpec{
		{Name: "clickhouse", Kind: "worker", CPUMillis: 2000, MemoryBytes: 4 * gib},
	}}
	d := r.deploymentFor(spec, spec.Services[0], "af-env-e1", "10.43.0.9")

	// resourcesFor being correct proves nothing on its own if the Deployment
	// never calls it. This is the wiring, and it is the step the old comment
	// in objects.go said was deliberately not taken.
	var app *corev1.Container
	for i := range d.Spec.Template.Spec.Containers {
		if d.Spec.Template.Spec.Containers[i].Name == "app" {
			app = &d.Spec.Template.Spec.Containers[i]
		}
	}
	require.NotNil(t, app, "the deployment has no app container")
	require.Equal(t, int64(2000), app.Resources.Requests.Cpu().MilliValue())
	require.Equal(t, int64(4*gib), app.Resources.Limits.Memory().Value())
}

func TestTheAppliedSizeIsReadOffTheObject(t *testing.T) {
	// Off the stored object rather than echoed from the spec, which is the
	// whole point of reading it back. A runtime that accepts a memory cap and
	// emits no requirement reports exactly what a correct one reports.
	r := &Runtime{prefix: DefaultNamespacePrefix}
	spec := provider.EnvSpec{EnvID: "e1", Services: []provider.ServiceSpec{
		{Name: "clickhouse", Kind: "worker", CPUMillis: 2000, MemoryBytes: 4 * gib},
	}}
	d := r.deploymentFor(spec, spec.Services[0], "af-env-e1", "10.43.0.9")
	cpu, mem := appliedResources(d.Spec.Template.Spec)
	require.Equal(t, int64(2000), cpu)
	require.Equal(t, int64(4*gib), mem)

	// And an object carrying nothing reports nothing, rather than reporting
	// the sidecar's or the migration container's numbers as the service's.
	bare, memBare := appliedResources(corev1.PodSpec{Containers: []corev1.Container{
		{Name: "af-proxy"}, {Name: "app"},
	}})
	require.Zero(t, bare)
	require.Zero(t, memBare)
}

func TestPodRequestsTakesInitContainersAsTheLargestRatherThanTheSum(t *testing.T) {
	// Init containers run one at a time and BEFORE the others, so a pod's
	// requirement is the larger of what its init containers need and what its
	// containers need together. Adding them makes a pod with a big init
	// container look twice as expensive as the scheduler thinks it is, which
	// would shrink the free figure and refuse environments that fit.
	pod := corev1.Pod{Spec: corev1.PodSpec{
		Containers:     []corev1.Container{{Name: "app", Resources: requirement(500, gib)}},
		InitContainers: []corev1.Container{{Name: "wait", Resources: requirement(2000, 4*gib)}},
	}}
	cpu, mem := podRequests(pod)
	require.Equal(t, int64(2000), cpu, "the init container needs more than the app does")
	require.Equal(t, int64(4*gib), mem)
}

func TestPodRequestsSumsTheContainersThatRunTogether(t *testing.T) {
	// The app and the sidecar are up at the same time, so their requests add.
	// Taking the maximum here would make a full node look free.
	pod := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
		{Name: "app", Resources: requirement(500, gib)},
		{Name: "af-proxy", Resources: requirement(250, gib/2)},
	}}}
	cpu, mem := podRequests(pod)
	require.Equal(t, int64(750), cpu)
	require.Equal(t, int64(gib+gib/2), mem)
}

func requirement(milliCPU, memoryBytes int64) corev1.ResourceRequirements {
	return resourcesFor(provider.ServiceSpec{CPUMillis: milliCPU, MemoryBytes: memoryBytes})
}
