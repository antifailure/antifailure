package local_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The emulator's containment, checked against a real container rather than
// against a fake.
//
// engine/conformance/emulator.go carries a Reach_FindsNoRouteOut behaviour, and its
// self test proves that behaviour can go red. What the self test proves is
// that the SUITE can say no, because the emulator it points at is a struct in
// this process whose Reach returns whatever the flaw says. It says nothing
// about Docker. A conformance behaviour proved only against a fake is a check
// that has never met its subject, and the claim here is about a network the
// daemon creates.
//
// So this file asks the daemon. It reads back the network the emulator
// ACTUALLY attached to and requires that network to be the internal one, and
// it attempts an outbound connection from inside the running container.
//
// The control is the part that makes the attack mean anything. A container
// that cannot reach ANYTHING fails an escape attempt for reasons that have
// nothing to do with containment: a missing tool, a dead container, a network
// it never joined. So the same container, in the same run, must succeed in
// reaching the sidecar by name. One success and one failure from one container
// is the only shape of this test that cannot pass by accident.

// A public address reached WITHOUT DNS, because DNS is a second thing that can
// fail and a failure to resolve reads exactly like a failure to route. 1.1.1.1
// answers on 443 from any machine with a route to the internet.
const emulatorEscapeAddress = "1.1.1.1 443"

func TestEmulator_ContainerHasNoRouteOutAndTheNetworkSaysWhy(t *testing.T) {
	r := requireRuntime(t)

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// A digest, taken from the daemon's own record of the image rather than
	// written down here. The engine refuses an emulator pinned by a tag, so a
	// test that wrote alpine:3.20 would be testing the refusal instead of the
	// containment, and a digest hardcoded in a test file goes stale on a
	// different architecture.
	digest := repoDigest(t, ctx, cli, proberImage)

	id := envID(t, r, "emucontain")
	_, err = r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		// Block everything. The emulator's containment must not depend on
		// what the egress policy says, because the policy governs the
		// sidecar and this container is not behind the sidecar at all.
		Egress: &schema.Egress{Default: schema.ModeBlock},
		Emulators: []provider.EmulatorSpec{{
			Name: "probe", Image: digest, Port: 8080,
			// A command rather than the image's own, because alpine's is a
			// shell that exits the moment it has no terminal. A real emulator
			// runs its own server and needs none of this.
			Command: []string{"/bin/sh", "-c", "sleep 900"},
			Env:     map[string]string{"AF_TEST": "1"},
		}},
	})
	require.NoError(t, err, "the environment did not come up with an emulator in it")

	name := "af-emu-probe-" + id
	insp, err := cli.ContainerInspect(ctx, name)
	require.NoError(t, err, "the emulator container was not created under the name the "+
		"sidecar's route points at")

	// One network and one only. An emulator on a second network is an
	// emulator whose containment depends on which one, and the whole design
	// is that it is on the inner one and nothing else.
	require.Len(t, insp.NetworkSettings.Networks, 1,
		"the emulator is attached to %d networks; the containment argument is that it is "+
			"on the inner network alone", len(insp.NetworkSettings.Networks))

	var attached string
	for _, ep := range insp.NetworkSettings.Networks {
		attached = ep.NetworkID
	}
	netInsp, err := cli.NetworkInspect(ctx, attached, network.InspectOptions{})
	require.NoError(t, err)
	require.True(t, netInsp.Internal,
		"the network the emulator actually attached to, %s, is not internal. Internal is "+
			"the only setting that removes a container's route to the internet: turning "+
			"off IP masquerading looks like it does the same thing and does not, because "+
			"Docker Desktop translates the traffic again at the virtual machine's gateway",
		netInsp.Name)

	// The control first, so that a failure here is read as "this container
	// cannot reach anything" rather than as containment working.
	control := execInContainer(t, ctx, cli, insp.ID,
		"nc -w 5 -z "+local.ProxyAlias+" 3128 && echo AF-CONTROL-OK")
	require.Contains(t, control, "AF-CONTROL-OK",
		"the emulator container could not reach the sidecar by name, so it cannot reach "+
			"anything at all and the escape attempt below proves nothing.\n%s", control)

	escape := execInContainer(t, ctx, cli, insp.ID,
		"nc -w 5 -z "+emulatorEscapeAddress+" && echo AF-ESCAPED")
	require.NotContains(t, escape, "AF-ESCAPED",
		"the emulator container opened a connection to %s. It runs a third party image and "+
			"holds a copy of every request the application made, so a route out is a route "+
			"to anywhere.\n%s", emulatorEscapeAddress, escape)
}

// TestEmulator_TeardownRemovesTheContainer is the other half of the promise
// the conformance suite states and cannot check against a real daemon.
//
// The emulator was invisible to Down when it was first written, because Down
// removes containers by kind and the new kind was not in the list. The
// container survived the environment, stayed attached to the network, and the
// network could then never be removed either. Every other test in this package
// passed.
func TestEmulator_TeardownRemovesTheContainer(t *testing.T) {
	r := requireRuntime(t)

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	digest := repoDigest(t, ctx, cli, proberImage)
	id := envID(t, r, "emuteardown")
	_, err = r.Up(ctx, provider.EnvSpec{
		EnvID:  id,
		Egress: &schema.Egress{Default: schema.ModeBlock},
		Emulators: []provider.EmulatorSpec{{
			Name: "probe", Image: digest, Port: 8080,
			Command: []string{"/bin/sh", "-c", "sleep 900"},
		}},
	})
	require.NoError(t, err)

	name := "af-emu-probe-" + id
	_, err = cli.ContainerInspect(ctx, name)
	require.NoError(t, err, "the emulator container was never created, so its removal "+
		"below would prove nothing")

	// Listed by the inventory as well, because the reaper reads the inventory
	// and a resource it cannot see is a resource nothing will ever collect.
	inv, err := r.Inventory(ctx)
	require.NoError(t, err)
	found := false
	for _, res := range inv {
		if res.EnvID == id && strings.HasSuffix(res.Kind, dockerutil.KindEmulator) {
			found = true
		}
	}
	require.True(t, found,
		"the emulator does not appear in the inventory, so the leak detector cannot see "+
			"it and nothing would ever report it as left behind")

	td, err := r.Down(ctx, id)
	require.NoError(t, err)
	require.Empty(t, td.Pending, "teardown left something behind: %+v", td.Pending)

	_, err = cli.ContainerInspect(ctx, name)
	require.Error(t, err,
		"the emulator container survived Down. An emulator holds state nothing else in "+
			"the environment can see, and state that outlives the environment is state "+
			"that could reach the next one")
}

// repoDigest returns an image reference pinned by digest, from the daemon's
// own record.
func repoDigest(
	t *testing.T, ctx context.Context, cli *client.Client, ref string,
) string {
	t.Helper()
	insp, err := cli.ImageInspect(ctx, ref)
	if err != nil {
		rc, pullErr := cli.ImagePull(ctx, ref, image.PullOptions{})
		if pullErr != nil {
			t.Skipf("skipped: %s could not be pulled, and this test needs a real image "+
				"pinned by digest: %v", ref, pullErr)
		}
		dockerutil.Discard(rc)
		insp, err = cli.ImageInspect(ctx, ref)
		if err != nil {
			t.Skipf("skipped: %s is not present after pulling it: %v", ref, err)
		}
	}
	if len(insp.RepoDigests) == 0 {
		// Named rather than worked around. An image built locally has no
		// registry digest, and substituting the image id would test a
		// reference the engine does not accept.
		t.Skipf("skipped: the local %s carries no repository digest, so there is no "+
			"digest pinned reference for the engine to accept", ref)
	}
	return insp.RepoDigests[0]
}

// execInContainer runs one shell command inside a container and returns
// everything it wrote.
func execInContainer(
	t *testing.T, ctx context.Context, cli *client.Client, id, script string,
) string {
	t.Helper()
	created, err := cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          []string{"/bin/sh", "-c", script},
		AttachStdout: true, AttachStderr: true,
	})
	require.NoError(t, err)

	attached, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	require.NoError(t, err)
	defer attached.Close()

	var b strings.Builder
	// The multiplexed stream carries an eight byte header per frame, and
	// reading it as plain text puts control bytes in the middle of the
	// markers this test looks for.
	_, err = copyDemuxed(&b, attached.Reader)
	require.NoError(t, err)
	return b.String()
}

// copyDemuxed strips Docker's stream framing.
func copyDemuxed(w io.Writer, r *bufio.Reader) (int64, error) {
	var total int64
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return total, nil
			}
			return total, err
		}
		size := int64(header[4])<<24 | int64(header[5])<<16 | int64(header[6])<<8 | int64(header[7])
		n, err := io.CopyN(w, r, size)
		total += n
		if err != nil {
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return total, err
		}
	}
}
