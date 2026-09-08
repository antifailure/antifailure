package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/antifailure/antifailure/engine/internal/capacity"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// checkCapacity refuses an environment this cluster cannot place.
//
// It runs before anything is created, because the alternative is what
// happened without it: the API server accepts a pod asking for more than any
// node has, the scheduler leaves it Pending with an event nobody is watching,
// and af up waits out the readiness timeout and reports a service that did not
// start. Minutes of waiting for a fact that was knowable in one list call.
//
// It is a NECESSARY condition and not a sufficient one, and capacity.Fits says
// which. The scheduler is still the authority: this refuses only the sets for
// which no placement exists, and lets the ones that merely might not pack
// through to the thing that actually packs them.
//
// WHEN IT CANNOT LOOK IT SAYS SO, rather than passing. A cluster that refuses
// to list its nodes or its pods is one this cannot check, and reporting
// nothing there would be a check that answers "fine" to a question it never
// asked. It says the size was not checked, on the progress channel, and lets
// the environment through: refusing to start because a permission is narrow
// would break every cluster where af has namespace scoped access and nothing
// more, which is a reasonable way to run it.
func (r *Runtime) checkCapacity(
	ctx context.Context, spec provider.EnvSpec, progress func(string),
) error {
	asks := asksFor(spec.Services)
	if len(asks) == 0 {
		return nil
	}
	nodes, err := r.freeCapacity(ctx)
	if err != nil {
		progress(fmt.Sprintf(
			"this cluster's free capacity could not be read (%v), so the sizes the manifest "+
				"asks for were NOT checked against it; a request larger than the cluster "+
				"will appear as a pod that stays Pending", err))
		return nil
	}
	if err := capacity.Fits(nodes, asks); err != nil {
		return aferrors.Coded(aferrors.AFRUN047, "detail", err.Error())
	}
	return nil
}

// asksFor is the sizes an environment declared, one entry per service that
// named one.
//
// A service that named neither is left out rather than entered as a zero. A
// zero row would make the environment look like it asked for something and got
// nothing, and it would put a service with no request into a shortfall message
// that has nothing to say about it.
func asksFor(services []provider.ServiceSpec) []capacity.Ask {
	var out []capacity.Ask
	for _, s := range services {
		if s.CPUMillis <= 0 && s.MemoryBytes <= 0 {
			continue
		}
		out = append(out, capacity.Ask{
			Service:     s.Name,
			Instances:   s.Instances(),
			MilliCPU:    s.CPUMillis,
			MemoryBytes: s.MemoryBytes,
		})
	}
	return out
}

// freeCapacity is what each schedulable node has left.
//
// Allocatable minus the requests of the pods already on it, which is the
// quantity the scheduler places against. Capacity alone would accept an
// environment onto a full cluster, which is the same "accepted and scheduled
// anyway" this check exists to stop, and it is the easy mistake to make here
// because capacity is the field with the obvious name.
//
// Unschedulable and not ready nodes are left out. A node that is cordoned or
// gone still reports its allocatable, so counting it makes the cluster look
// larger than anything can be placed on.
func (r *Runtime) freeCapacity(ctx context.Context) ([]capacity.Node, error) {
	nodes, err := r.cli.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	pods, err := r.cli.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	usedCPU := map[string]int64{}
	usedMem := map[string]int64{}
	for _, pod := range pods.Items {
		// A pod that has finished holds nothing. Succeeded and Failed pods
		// linger for their logs, and counting their requests would shrink a
		// cluster by every job it has ever run.
		if pod.Spec.NodeName == "" ||
			pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		cpu, mem := podRequests(pod)
		usedCPU[pod.Spec.NodeName] += cpu
		usedMem[pod.Spec.NodeName] += mem
	}
	var out []capacity.Node
	for _, n := range nodes.Items {
		if n.Spec.Unschedulable || !nodeReady(n) {
			continue
		}
		freeCPU := n.Status.Allocatable.Cpu().MilliValue() - usedCPU[n.Name]
		freeMem := n.Status.Allocatable.Memory().Value() - usedMem[n.Name]
		if freeCPU < 0 {
			freeCPU = 0
		}
		if freeMem < 0 {
			freeMem = 0
		}
		out = append(out, capacity.Node{Name: n.Name, MilliCPU: freeCPU, MemoryBytes: freeMem})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no schedulable node reported its allocatable capacity")
	}
	return out, nil
}

// podRequests sums what one pod reserves.
//
// Init containers are taken as the MAXIMUM rather than added, because they run
// one at a time and before the others, so a pod's requirement is the larger of
// what its init containers need and what its containers need together. Adding
// them is the mistake that makes a pod with a big init container look twice as
// expensive as the scheduler thinks it is.
func podRequests(pod corev1.Pod) (milliCPU, memoryBytes int64) {
	for _, c := range pod.Spec.Containers {
		milliCPU += c.Resources.Requests.Cpu().MilliValue()
		memoryBytes += c.Resources.Requests.Memory().Value()
	}
	for _, c := range pod.Spec.InitContainers {
		if v := c.Resources.Requests.Cpu().MilliValue(); v > milliCPU {
			milliCPU = v
		}
		if v := c.Resources.Requests.Memory().Value(); v > memoryBytes {
			memoryBytes = v
		}
	}
	return milliCPU, memoryBytes
}

func nodeReady(n corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// appliedResources is the size the cluster is actually holding for a service,
// read off the object rather than echoed from the spec.
//
// The whole point of reading it back. A runtime that accepts a memory cap and
// emits no requirement reports exactly what a correct one reports, and asking
// the stored object is the only thing that tells the two apart.
func appliedResources(pod corev1.PodSpec) (milliCPU, memoryBytes int64) {
	for _, c := range pod.Containers {
		if c.Name != "app" {
			continue
		}
		return c.Resources.Requests.Cpu().MilliValue(), c.Resources.Requests.Memory().Value()
	}
	return 0, 0
}
