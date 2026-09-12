package k8s

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// unsupportedMounts names every service whose mounts this runtime cannot place,
// and returns "" when there are none.
//
// BOTH shapes are refused here, for two different reasons, and neither is a
// judgement that the shape is wrong.
//
// A named volume has to survive a restart of the service. On a cluster that is
// a PersistentVolumeClaim, and a claim is bound against a storage class: which
// one exists, what it costs and whether it can be written from two nodes are
// facts about somebody else's cluster that this runtime does not get to choose.
// An emptyDir would be the one-line version, and it is exactly wrong, because it
// is deleted with the pod and the whole promise of the key is that it is not.
//
// A repository file would be a ConfigMap, which is the right object and a small
// amount of code. It is not written because it could not be RUN here: no
// cluster exists for this repository's tests to start, and a mount path that has
// never placed a file is the defined-but-unproven state this repository keeps
// finding in its own instruments. Refusing by name is the honest answer until
// something can run it.
//
// Pure, so the refusal is tested without a cluster.
func unsupportedMounts(services []provider.ServiceSpec) string {
	var parts []string
	for _, s := range services {
		var vols, files []string
		for _, m := range s.Mounts {
			if m.Volume != "" {
				vols = append(vols, m.Volume)
			} else {
				files = append(files, m.At)
			}
		}
		sort.Strings(vols)
		sort.Strings(files)
		if len(vols) > 0 {
			parts = append(parts, fmt.Sprintf("%s keeps the named %s %s",
				s.Name, plural(len(vols), "volume", "volumes"), strings.Join(vols, ", ")))
		}
		if len(files) > 0 {
			parts = append(parts, fmt.Sprintf("%s mounts a repository file at %s",
				s.Name, strings.Join(files, ", ")))
		}
	}
	return strings.Join(parts, "; ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// podReadiness is what a pod's own readiness can promise.
//
// The cluster marks a container Ready the moment it runs when it has no
// readiness probe, so a Ready with no probe behind it is the same false pass the
// local runtime used to report for a service with no port.
func podReadiness(pod corev1.Pod) provider.Readiness {
	if !podReady(pod) {
		return provider.ReadinessFailed
	}
	for _, c := range pod.Spec.Containers {
		if c.ReadinessProbe == nil {
			return provider.ReadinessUnproved
		}
	}
	return provider.ReadinessProved
}
