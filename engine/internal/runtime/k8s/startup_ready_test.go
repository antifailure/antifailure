package k8s

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func startupPod(initDone, appRunning bool) corev1.Pod {
	p := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker"},
		Spec: corev1.PodSpec{InitContainers: []corev1.Container{{Name: "af-network-gate"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning,
			InitContainerStatuses: []corev1.ContainerStatus{{Name: "af-network-gate", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}},
			ContainerStatuses:     []corev1.ContainerStatus{{Name: "app", Ready: true}},
		},
	}
	if initDone {
		p.Status.InitContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}
	}
	if appRunning {
		p.Status.ContainerStatuses[0].State.Running = &corev1.ContainerStateRunning{}
	}
	return p
}

func startupAPI(t *testing.T, response func(int32, http.ResponseWriter, *http.Request)) (*Runtime, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response(calls.Add(1), w, req)
	}))
	t.Cleanup(server.Close)
	config := &rest.Config{Host: server.URL, ContentConfig: rest.ContentConfig{ContentType: "application/json", AcceptContentTypes: "application/json"}}
	client, err := kubernetes.NewForConfig(config)
	require.NoError(t, err)
	return &Runtime{cli: client, rest: config, readyWait: time.Second}, &calls
}

func sendStartupPods(w http.ResponseWriter, pods ...corev1.Pod) {
	_ = json.NewEncoder(w).Encode(corev1.PodList{Items: pods})
}

func TestWorkerReadinessWaitsForBothEventOrderings(t *testing.T) {
	for _, ordering := range []string{"init first", "app status first"} {
		t.Run(ordering, func(t *testing.T) {
			r, calls := startupAPI(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
				if n == 1 {
					sendStartupPods(w, startupPod(ordering == "init first", ordering == "app status first"))
					return
				}
				sendStartupPods(w, startupPod(true, true))
			})
			require.NoError(t, r.waitForInstances(context.Background(), "ns", provider.ServiceSpec{Name: "worker"}, 1, time.Second))
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestWorkerReadinessDoesNotPassWithAMissingEvent(t *testing.T) {
	for _, absent := range []string{"init", "app", "pods"} {
		t.Run(absent, func(t *testing.T) {
			r, _ := startupAPI(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
				if absent == "pods" {
					sendStartupPods(w)
					return
				}
				sendStartupPods(w, startupPod(absent != "init", absent != "app"))
			})
			err := r.confirmStarted(context.Background(), "ns", provider.ServiceSpec{Name: "worker", HealthTimeout: 30 * time.Millisecond})
			require.Error(t, err)
			require.Contains(t, err.Error(), "AF-RUN-004")
		})
	}
}

func TestWorkerReadinessRefusesAPIErrorAndBoundsTheRequest(t *testing.T) {
	t.Run("API error", func(t *testing.T) {
		r, _ := startupAPI(t, func(_ int32, w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) })
		err := r.confirmStarted(context.Background(), "ns", provider.ServiceSpec{Name: "worker"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "AF-RUN-002")
	})
	t.Run("blocked response", func(t *testing.T) {
		finished := make(chan struct{})
		r, _ := startupAPI(t, func(_ int32, _ http.ResponseWriter, req *http.Request) { <-req.Context().Done(); close(finished) })
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
		defer cancel()
		err := r.confirmStarted(ctx, "ns", provider.ServiceSpec{Name: "worker", HealthTimeout: 35 * time.Millisecond})
		require.Error(t, err)
		require.Less(t, time.Since(started), 500*time.Millisecond)
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("the API request was left in flight")
		}
	})
}

func TestWorkerReadinessRequiresEveryReplicaAndPreservesObservation(t *testing.T) {
	t.Run("replicas", func(t *testing.T) {
		r, calls := startupAPI(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
			a, b := startupPod(true, true), startupPod(n > 1, true)
			b.Name = "second-worker"
			sendStartupPods(w, a, b)
		})
		require.NoError(t, r.waitForInstances(context.Background(), "ns", provider.ServiceSpec{Name: "worker"}, 2, time.Second))
		require.EqualValues(t, 2, calls.Load())
	})
	t.Run("observation", func(t *testing.T) {
		r, _ := startupAPI(t, func(_ int32, w http.ResponseWriter, _ *http.Request) { sendStartupPods(w, startupPod(true, true)) })
		started := time.Now()
		require.NoError(t, r.confirmStarted(context.Background(), "ns", provider.ServiceSpec{Name: "worker", HealthTimeout: 4 * time.Second}))
		require.GreaterOrEqual(t, time.Since(started), 2*time.Second)
	})
}

func TestWorkerReadinessHonorsCallerCancellation(t *testing.T) {
	r, calls := startupAPI(t, func(_ int32, w http.ResponseWriter, _ *http.Request) { sendStartupPods(w, startupPod(true, true)) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.confirmStarted(ctx, "ns", provider.ServiceSpec{Name: "worker"})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls.Load())
}

func TestWorkerReadinessAcceptsCompletedWorkAndRefusesEarlyCrash(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		r, calls := startupAPI(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
			p := startupPod(true, false)
			p.Status.Phase = corev1.PodSucceeded
			p.Status.ContainerStatuses[0].State.Terminated = &corev1.ContainerStateTerminated{ExitCode: 0}
			sendStartupPods(w, p)
		})
		require.NoError(t, r.confirmStarted(context.Background(), "ns", provider.ServiceSpec{Name: "worker", HealthTimeout: 200 * time.Millisecond}))
		require.EqualValues(t, 1, calls.Load())
	})
	t.Run("crash after running", func(t *testing.T) {
		r, calls := startupAPI(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
			p := startupPod(true, true)
			if n > 1 {
				p.Status.ContainerStatuses[0].LastTerminationState.Terminated = &corev1.ContainerStateTerminated{ExitCode: 42}
			}
			sendStartupPods(w, p)
		})
		err := r.confirmStarted(context.Background(), "ns", provider.ServiceSpec{Name: "worker"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "AF-RUN-005")
		require.EqualValues(t, 2, calls.Load())
	})
}

func TestInitReadinessUsesDeclaredContainersAndReportsRefusal(t *testing.T) {
	p := startupPod(true, true)
	p.Status.InitContainerStatuses[0].Name = "unrelated"
	require.False(t, podReady(p))
	require.Contains(t, podTrouble(p), "af-network-gate has not started")
	p = startupPod(false, true)
	p.Status.InitContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
	p.Status.InitContainerStatuses[0].LastTerminationState.Terminated = &corev1.ContainerStateTerminated{ExitCode: 1, Message: "AF-CONTAINMENT refused: cluster API reachable"}
	require.False(t, podReady(p))
	require.Contains(t, podTrouble(p), "AF-CONTAINMENT refused: cluster API reachable")
	p.Status.InitContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, Message: "current refusal"}}
	require.False(t, podReady(p))
	require.Contains(t, podTrouble(p), "current refusal")
}

func TestRestartableInitSidecarMustBeStartedAndReadyWhenProbed(t *testing.T) {
	p := startupPod(false, true)
	always := corev1.ContainerRestartPolicyAlways
	p.Spec.InitContainers[0].RestartPolicy = &always
	started := true
	p.Status.InitContainerStatuses[0].Started = &started
	require.True(t, podReady(p), "restartable sidecars do not terminate before the app starts")
	p.Status.InitContainerStatuses[0].Started = nil
	require.False(t, podReady(p))
	p.Status.InitContainerStatuses[0].Started = &started
	p.Spec.InitContainers[0].ReadinessProbe = &corev1.Probe{}
	require.False(t, podReady(p))
	p.Status.InitContainerStatuses[0].Ready = true
	require.True(t, podReady(p))
	p.Status.InitContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "restarting"}}
	require.False(t, podReady(p))
	require.Contains(t, podTrouble(p), "restarting")
	p.Status.Phase = corev1.PodSucceeded
	p.Status.InitContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 143}}
	p.Status.ContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}
	running, completed := applicationStarted(p)
	require.False(t, running)
	require.True(t, completed, "successful jobs stop restartable sidecars after the app exits")
}
