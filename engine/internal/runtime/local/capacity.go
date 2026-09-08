package local

import (
	"context"

	"github.com/docker/docker/api/types/container"

	"github.com/antifailure/antifailure/engine/internal/capacity"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// checkCapacity refuses an environment this daemon cannot hold.
//
// The daemon is one node and it is the only one, so the check is a division
// against what it reported: a manifest asking for 12Gi on a machine whose
// Docker VM has 8 fails, and it fails here with the shortfall named rather
// than several seconds later with a message from the daemon about a cgroup.
//
// WHAT IT DOES NOT ACCOUNT FOR, and the difference from the cluster is real.
// Docker RESERVES NOTHING. A container with no memory limit, which is most of
// them and every container this machine was already running, is not holding
// anything the daemon can subtract, so there is no free figure to compute and
// the comparison is against the whole machine. So this refuses an environment
// that could never fit and does not refuse the eleventh environment on a
// machine that holds ten. The cluster check does better because a cluster
// scheduler has the fact this does not: what every pod asked for.
//
// Said here rather than left to be found, because a check whose limits are not
// written down is one somebody will read as a guarantee.
func (r *Runtime) checkCapacity(ctx context.Context, spec provider.EnvSpec) error {
	asks := capacity.AsksFor(spec.Services)
	if len(asks) == 0 {
		return nil
	}
	info, err := r.cli.Info(ctx)
	if err != nil {
		// Not a refusal. A daemon that will not describe itself is one this
		// cannot check, and every other call in Up is about to report the
		// same unreachable daemon in words that name it.
		return nil
	}
	node := capacity.Node{
		Name:        daemonName(info.Name),
		MilliCPU:    int64(info.NCPU) * 1000,
		MemoryBytes: info.MemTotal,
	}
	if node.MilliCPU <= 0 || node.MemoryBytes <= 0 {
		return nil
	}
	if err := capacity.Fits([]capacity.Node{node}, asks); err != nil {
		return aferrors.Coded(aferrors.AFRUN047, "detail", err.Error())
	}
	return nil
}

func daemonName(name string) string {
	if name == "" {
		return "this Docker daemon"
	}
	return name
}

// appliedResources is the size the daemon is actually holding a container to.
//
// From HostConfig on an inspect, which is the daemon's own record of what the
// container was created with, rather than the spec that asked for it. A
// runtime that accepted a cap and set none would otherwise report what a
// correct one reports.
//
// NanoCPUs comes back in billionths and the manifest is written in
// thousandths, so it is divided by a million on the way out. Anything the
// daemon rounded to less than one thousandth reads as zero, which is the same
// answer validation gives to a value that fine.
func (r *Runtime) appliedResources(ctx context.Context, id string) (milliCPU, memoryBytes int64) {
	insp, err := r.cli.ContainerInspect(ctx, id)
	if err != nil || insp.HostConfig == nil {
		return 0, 0
	}
	return hostConfigResources(insp.HostConfig.Resources)
}

func hostConfigResources(res container.Resources) (milliCPU, memoryBytes int64) {
	return res.NanoCPUs / 1_000_000, res.Memory
}
