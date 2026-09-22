package local

import (
	"context"
	"fmt"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// An environment gets two networks, and the reason is the whole containment
// story, so it is worth stating exactly.
//
// The inner network is created with internal set, which is the only setting
// Docker offers that actually removes a container's route to the internet.
// Turning off IP masquerading looks like it should work and does not: on
// Docker Desktop the traffic is translated again by the virtual machine's own
// gateway, so a container with masquerading disabled still reaches 1.1.1.1.
// That was measured, not assumed, and the test that measures it is in this
// package.
//
// The cost of internal is that a published port stops working, because the
// port forwarder has no bridge to forward from. So each web service gets a
// small forwarder of its own on both networks, publishing on the host's
// loopback and connecting inward. The forwarder is the only thing in the
// environment with a route out, it accepts connections and forwards them, and
// it is where the egress proxy will live when the policy sidecar lands.
const (
	innerNetworkPrefix = "af-net-"
	edgeNetworkPrefix  = "af-edge-"
)

func innerNetworkName(envID string) string { return innerNetworkPrefix + envID }
func edgeNetworkName(envID string) string  { return edgeNetworkPrefix + envID }

// networks holds an environment's two network identifiers.
type networks struct {
	inner string
	edge  string
}

// EnsureNetworks creates an environment's networks and returns the inner one.
//
// It is exported because the database has to join the network before any
// service starts. A service receives its connection string in its environment
// at creation time, and that string names the database by its alias on this
// network, so the alias has to exist first. Doing it after Up would hand every
// service an address that did not resolve when it read it.
func (r *Runtime) EnsureNetworks(ctx context.Context, envID string, journal func(string, string) error) (string, error) {
	if journal == nil {
		journal = func(string, string) error { return nil }
	}
	n, err := r.ensureNetworks(ctx, envID, journal)
	return n.inner, err
}

func (r *Runtime) ensureNetworks(ctx context.Context, envID string, journal func(string, string) error) (networks, error) {
	var n networks
	var err error
	if n.inner, err = r.ensureOneNetwork(ctx, envID, innerNetworkName(envID), true, journal); err != nil {
		return n, err
	}
	if n.edge, err = r.ensureOneNetwork(ctx, envID, edgeNetworkName(envID), false, journal); err != nil {
		return n, err
	}
	return n, nil
}

func (r *Runtime) ensureOneNetwork(
	ctx context.Context, envID, name string, internal bool, journal func(string, string) error,
) (string, error) {
	if existing, err := r.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{}); err == nil {
		if !dockerutil.IsOurs(existing.Network.Labels) {
			return "", aferrors.Coded(aferrors.AFRUN040,
				"detail", fmt.Sprintf("a network called %s exists and is not managed by Antifailure", name))
		}
		return existing.Network.ID, nil
	}
	if err := journal(kindNetwork, name); err != nil {
		return "", err
	}
	res, err := r.cli.NetworkCreate(ctx, name, client.NetworkCreateOptions{
		Driver:   "bridge",
		Internal: internal,
		Labels:   r.managed(dockerutil.KindNetwork, envID),
	})
	if err != nil {
		// Two Up calls racing produce this, and the loser should use the
		// network the winner made rather than fail. The winner still has to
		// be us: the inspect above checked that, and a network created in the
		// window between it and this create has not been checked by anything.
		// One branch of a function testing an invariant and the other not is
		// how the invariant stops being one.
		if strings.Contains(err.Error(), "already exists") {
			if existing, insErr := r.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{}); insErr == nil {
				if !dockerutil.IsOurs(existing.Network.Labels) {
					return "", aferrors.Coded(aferrors.AFRUN040,
						"detail", fmt.Sprintf("a network called %s exists and is not managed by Antifailure", name))
				}
				return existing.Network.ID, nil
			}
		}
		if addressPoolsExhausted(err) {
			return "", r.addressPoolsError(ctx, err)
		}
		return "", aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	return res.ID, nil
}

// addressPoolsExhausted reports the daemon refusing a network because every
// subnet it is allowed to hand out is already taken.
//
// Two spellings, because Docker has used both. Current releases say the
// predefined pools have been fully subnetted; older ones said they could not
// find an available, non-overlapping address pool. Either way it is not the
// environment that is wrong, it is the daemon that is full, and the remedy for
// that is nothing AF-RUN-040 names: af doctor reports the runtime healthy and
// af down removes only the checked out branch's environment.
func addressPoolsExhausted(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "fully subnetted") ||
		strings.Contains(msg, "non-overlapping IPv4 address pool")
}

// addressPoolsError says how full the daemon is and how much of that is ours
// to give back.
//
// Measured on 2026-09-22: the default pools hold about thirty one bridge
// networks, the daemon held thirty, and fourteen were Antifailure networks
// that nothing was attached to, left by test runs that were killed before
// their teardown ran. The count is read at the moment of failure because it is
// the number that decides what to do next: a daemon full of our orphans wants
// af env prune --orphaned, one full of somebody else's networks wants a wider
// default-address-pools setting, and a message that cannot tell the two apart
// sends half its readers to the wrong one.
func (r *Runtime) addressPoolsError(ctx context.Context, cause error) error {
	detail := "Docker has handed out every address range it is allowed to"
	if total, orphaned, uncounted, err := r.countNetworks(ctx); err == nil {
		detail = fmt.Sprintf("%s. The daemon holds %d %s, and %d of them %s Antifailure %s with no container attached",
			detail, total, plural(total, "network", "networks"),
			orphaned, plural(orphaned, "is an", "are"), plural(orphaned, "network", "networks"))
		if uncounted > 0 {
			detail += fmt.Sprintf("; %d more Antifailure %s could not be inspected, so %s not counted",
				uncounted, plural(uncounted, "network", "networks"), plural(uncounted, "it was", "they were"))
		}
	}
	return aferrors.Wrap(cause, aferrors.AFRUN052, "detail", detail)
}

// countNetworks counts every network on the daemon, and the Antifailure ones
// that no container is attached to.
//
// Attached means an endpoint, which only a container that is running, paused
// or restarting has. A container that was created and never started holds no
// endpoint, so a network it names still reads as unattached here, which is the
// truth about the address range: that range is held by the network, not by the
// container, and removing the network is what frees it.
func (r *Runtime) countNetworks(ctx context.Context) (total, orphaned, uncounted int, err error) {
	all, err := r.cli.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return 0, 0, 0, err
	}
	total = len(all.Items)
	for _, n := range all.Items {
		if !dockerutil.IsOurs(n.Labels) {
			continue
		}
		attached, ok := r.networkEndpoints(ctx, n.ID)
		switch {
		case !ok:
			uncounted++
		case attached == 0:
			orphaned++
		}
	}
	return total, orphaned, uncounted, nil
}

// networkEndpoints is how many containers are attached to a network, and
// whether that could be read at all.
//
// The list endpoint never fills in Containers, only inspect does, so a count
// taken off the list would report every network as empty. That is the one
// wrong answer this must never give, because an empty network is one a sweep
// is allowed to remove.
func (r *Runtime) networkEndpoints(ctx context.Context, id string) (int, bool) {
	insp, err := r.cli.NetworkInspect(ctx, id, client.NetworkInspectOptions{})
	if err != nil {
		return 0, false
	}
	return len(insp.Network.Containers), true
}

// disconnectForeign detaches anything still on a network that this teardown is
// not going to remove.
//
// The database branch container is the case that matters: it belongs to the
// database provider, it joined this network so services could reach it, and
// removing it here would destroy a database the runtime does not own. Docker
// refuses to remove a network with endpoints still attached, so it has to be
// disconnected first or teardown reports a pending network forever.
func (r *Runtime) disconnectForeign(ctx context.Context, networkID string) {
	insp, err := r.cli.NetworkInspect(ctx, networkID, client.NetworkInspectOptions{})
	if err != nil {
		return
	}
	for id := range insp.Network.Containers {
		if _, err := r.cli.NetworkDisconnect(ctx, networkID, client.NetworkDisconnectOptions{
			Container: id,
			Force:     true,
		}); err != nil {
			if cerrdefs.IsNotFound(err) {
				continue
			}
		}
	}
}
