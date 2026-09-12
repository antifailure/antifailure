package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/antifailure/antifailure/engine/internal/clock"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The cluster runtime's half of mounts and readiness, decided before anything
// talks to a cluster, so these run on a laptop with none.

func TestUnsupportedMounts_NamesEveryServiceAndBothShapes(t *testing.T) {
	got := unsupportedMounts([]provider.ServiceSpec{
		{Name: "web"},
		{Name: "db", Mounts: []provider.MountSpec{
			{At: "/var/lib/postgresql/data", Volume: "pgdata"},
			{At: "/docker-entrypoint-initdb.d", Files: []provider.MountFile{{Rel: "roles.sql"}}},
		}},
	})
	require.Contains(t, got, "db keeps the named volume pgdata")
	require.Contains(t, got, "db mounts a repository file at /docker-entrypoint-initdb.d")
	require.NotContains(t, got, "web", "a service with no mounts was named in the refusal")
	require.Empty(t, unsupportedMounts([]provider.ServiceSpec{{Name: "web"}}))
}

// Refused BEFORE any cluster call. The runtime here has no client at all, so a
// refusal that came after the first API call would panic rather than return,
// and this test is what says the order is right.
func TestUp_RefusesMountsBeforeTouchingTheCluster(t *testing.T) {
	// A clock, because Up stamps the environment's creation time before it
	// refuses anything, and no client, because the refusal has to come before
	// the first call to a cluster: a refusal that came later would reach a nil
	// client and panic rather than return, which is what this asserts.
	r := &Runtime{prefix: DefaultNamespacePrefix, clock: clock.New()}
	_, err := r.Up(context.Background(), provider.EnvSpec{
		EnvID: "e1",
		Services: []provider.ServiceSpec{{Name: "db", Mounts: []provider.MountSpec{
			{At: "/data", Volume: "pgdata"},
		}}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-RUN-049")
	require.Contains(t, err.Error(), "pgdata")
}

func TestContainerFor_AHealthCommandIsAnExecProbe(t *testing.T) {
	c := containerFor(provider.EnvSpec{}, provider.ServiceSpec{
		Name: "db", Port: 5432, HealthCommand: "pg_isready -U postgres",
	}, false)
	require.NotNil(t, c.ReadinessProbe)
	require.NotNil(t, c.ReadinessProbe.Exec, "a declared command was probed some other way")
	require.Equal(t, []string{"/bin/sh", "-c", "pg_isready -U postgres"}, c.ReadinessProbe.Exec.Command)
	require.Nil(t, c.ReadinessProbe.TCPSocket, "the command and the port were both probed")
}

// A portless service with a command gets a probe, which is the only way the
// cluster can say it is not ready yet.
func TestContainerFor_APortlessServiceWithACommandIsProbed(t *testing.T) {
	c := containerFor(provider.EnvSpec{}, provider.ServiceSpec{
		Name: "consumer", Kind: "worker", HealthCommand: "test -f /tmp/ready",
	}, false)
	require.NotNil(t, c.ReadinessProbe)
	require.Empty(t, c.Ports)
}

func TestContainerFor_APortlessServiceWithNoCommandHasNoProbe(t *testing.T) {
	c := containerFor(provider.EnvSpec{}, provider.ServiceSpec{Name: "consumer", Kind: "worker"}, false)
	require.Nil(t, c.ReadinessProbe)
}

func runningPod(probe *corev1.Probe) corev1.Pod {
	return corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", ReadinessProbe: probe}}},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "app", Ready: true}},
		},
	}
}

// The cluster marks a container with no probe Ready the moment it runs, which is
// the same false pass the local runtime used to report.
func TestPodReadiness_ARunningPodWithNoProbeIsUnproved(t *testing.T) {
	require.Equal(t, provider.ReadinessUnproved, podReadiness(runningPod(nil)))
}

func TestPodReadiness_ARunningPodWhoseProbePassedIsProved(t *testing.T) {
	require.Equal(t, provider.ReadinessProved, podReadiness(runningPod(&corev1.Probe{})))
}

func TestPodReadiness_APodThatIsNotRunningFailed(t *testing.T) {
	p := runningPod(&corev1.Probe{})
	p.Status.Phase = corev1.PodFailed
	require.Equal(t, provider.ReadinessFailed, podReadiness(p))
}
