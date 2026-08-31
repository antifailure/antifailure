package local

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	dockerbuild "github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/network"

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

// ingressImage is the forwarder image.
//
// Built here rather than pulled, from a base already in use, so that bringing
// an environment up does not depend on a third party image nobody in this
// repository has read. The tag is fixed because the content is fixed; a change
// to the Dockerfile below must change it.
const (
	ingressImage      = "antifailure/ingress:socat-1"
	ingressDockerfile = "FROM alpine:3.20\nRUN apk add --no-cache socat\n"
)

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
	if existing, err := r.cli.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		if !dockerutil.IsOurs(existing.Labels) {
			return "", aferrors.Coded(aferrors.AFRUN040,
				"detail", fmt.Sprintf("a network called %s exists and is not managed by Antifailure", name))
		}
		return existing.ID, nil
	}
	if err := journal(kindNetwork, name); err != nil {
		return "", err
	}
	res, err := r.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver:   "bridge",
		Internal: internal,
		Labels:   dockerutil.Managed(dockerutil.KindNetwork, envID, r.clock.Now()),
	})
	if err != nil {
		// Two Up calls racing produce this, and the loser should use the
		// network the winner made rather than fail. The winner still has to
		// be us: the inspect above checked that, and a network created in the
		// window between it and this create has not been checked by anything.
		// One branch of a function testing an invariant and the other not is
		// how the invariant stops being one.
		if strings.Contains(err.Error(), "already exists") {
			if existing, insErr := r.cli.NetworkInspect(ctx, name, network.InspectOptions{}); insErr == nil {
				if !dockerutil.IsOurs(existing.Labels) {
					return "", aferrors.Coded(aferrors.AFRUN040,
						"detail", fmt.Sprintf("a network called %s exists and is not managed by Antifailure", name))
				}
				return existing.ID, nil
			}
		}
		return "", aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	return res.ID, nil
}

// ensureIngressImage builds the forwarder image if it is not already present.
//
// The context is assembled in memory rather than on disk, because writing a
// two line Dockerfile into a temporary directory to hand it back to the daemon
// is a filesystem round trip for nothing, and one more thing to clean up.
func (r *Runtime) ensureIngressImage(ctx context.Context) error {
	if _, err := r.cli.ImageInspect(ctx, ingressImage); err == nil {
		return nil
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile", Mode: 0o644, Size: int64(len(ingressDockerfile)),
		ModTime: time.Unix(946684800, 0).UTC(), Format: tar.FormatPAX, Typeflag: tar.TypeReg,
	}); err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	if _, err := io.WriteString(tw, ingressDockerfile); err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	if err := tw.Close(); err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}

	resp, err := r.cli.ImageBuild(ctx, bytes.NewReader(buf.Bytes()), dockerbuild.ImageBuildOptions{
		Tags:   []string{ingressImage},
		Remove: true,
		Labels: dockerutil.Managed(dockerutil.KindSidecar, "", r.clock.Now()),
	})
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", "building the ingress forwarder: "+err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	// The stream has to be drained for the daemon to consider the build
	// finished, and the error only appears inside it.
	dec := json.NewDecoder(resp.Body)
	var buildErr string
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if decErr := dec.Decode(&msg); decErr != nil {
			break
		}
		if msg.Error != "" {
			buildErr = msg.Error
		}
	}
	if buildErr != "" {
		return aferrors.Coded(aferrors.AFRUN040,
			"detail", "building the ingress forwarder: "+r.redactor.String(buildErr))
	}
	return nil
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
	insp, err := r.cli.NetworkInspect(ctx, networkID, network.InspectOptions{})
	if err != nil {
		return
	}
	for id := range insp.Containers {
		if err := r.cli.NetworkDisconnect(ctx, networkID, id, true); err != nil {
			if cerrdefs.IsNotFound(err) {
				continue
			}
		}
	}
}
