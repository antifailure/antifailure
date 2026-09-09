package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// The ClickHouse the engine runs when a manifest names no server, which is the
// case a developer with no ClickHouse account is in.
//
// ONE container per machine, long lived, shared by every environment and every
// golden. That is the opposite of the Postgres provider's model, where a
// branch is a container, and the difference is the store rather than a
// preference: a ClickHouse image is most of a gigabyte and starting one takes
// the better part of a minute under load, while creating a database on a
// running one and attaching a golden's partitions to it takes milliseconds.
// A container per branch would spend a minute of startup to reach a store that
// answers in milliseconds, and a committed image per golden would spend a
// gigabyte to hold a hundred megabytes of events.

// LocalServerImage is the server the engine runs.
//
// Pinned to a minor version rather than to latest, because the type
// vocabulary, the statement grammar and the EXCHANGE TABLES this depends on
// are claims about a version, and "whatever was newest that morning" is not a
// version anybody can reproduce. It is the same image the masking dialect's
// live tests are written against.
const LocalServerImage = "clickhouse/clickhouse-server:25.3-alpine"

// LocalServerName is the container. One name, so a second engine process finds
// the first one's server rather than starting a second.
const LocalServerName = "af-clickhouse"

// localServerPassword is what the managed server uses.
//
// A constant, and that is safe for the same specific reason the Postgres
// provider's is: the container publishes only on the loopback interface, holds
// nothing but masked data, and is reachable from nowhere else. Generating one
// per machine would imply the password is the boundary. It is not; the
// loopback binding is.
const localServerPassword = "antifailure-local-only"

// localServerPort is the HTTP port inside the container.
const localServerPort = 8123

// LocalServer is a managed ClickHouse on this machine.
type LocalServer struct {
	// URL is the admin connection to it.
	URL secrets.Value
	// ContainerRef is the container, which is what a runtime attaches to an
	// environment's network so that a service can reach the store by name.
	ContainerRef string
	// Started reports whether this call created the container, which is what
	// a progress line says out loud: the first refresh on a machine waits for
	// a server to come up and every one after it does not.
	Started bool
}

// LocalServerOptions configure EnsureLocalServer.
type LocalServerOptions struct {
	// PortFrom is where to start allocating the published port. Zero resolves
	// AF_PORT_RANGE_START and the default under it.
	PortFrom int
	// Getenv reads the environment, so a test can move the port range without
	// moving the process's.
	Getenv func(string) string
	// Ready bounds the wait for the server to answer. Zero uses
	// DefaultReadyTimeout.
	Ready time.Duration
	// Progress receives a line while the server starts, and may be nil.
	Progress func(string)
	// OnlyIfPresent asks for the server that is already there and refuses to
	// start one, answering ErrNoLocalServer when there is none.
	//
	// A teardown sets it. Starting a ClickHouse in order to drop databases
	// that went away with the container it was in is a minute of waiting to
	// remove nothing, and `af down` on a machine whose containers were pruned
	// would do it every time.
	OnlyIfPresent bool
}

// ErrNoLocalServer is returned when OnlyIfPresent is set and the machine has
// no managed ClickHouse. Everything it held went with it, so a caller tearing
// down treats it as nothing to remove rather than as a failure.
var ErrNoLocalServer = errors.New("datastore.clickhouse: there is no managed ClickHouse on this machine")

// DefaultReadyTimeout is how long a first start is given.
//
// Three minutes, and that is not padding. A cold ClickHouse on a loaded
// machine took eighty seconds to answer its first query while this was being
// written, on a laptop running four other test suites, and a timeout that
// expires under load produces a skip or a failure that names the timeout
// rather than the load.
const DefaultReadyTimeout = 3 * time.Minute

// EnsureLocalServer returns the machine's ClickHouse, starting it if it is not
// already there.
//
// Idempotent by container name. A container that exists and is running is
// reused, one that exists and is stopped is started, and one that is there
// under a name this did not create is refused rather than adopted: a server
// somebody else's tooling manages is not one to put a copy of production into.
func EnsureLocalServer(ctx context.Context, opts LocalServerOptions) (LocalServer, error) {
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}
	if opts.Ready <= 0 {
		opts.Ready = DefaultReadyTimeout
	}
	cli, err := dockerutil.Client()
	if err != nil {
		return LocalServer{}, err
	}
	defer func() { _ = cli.Close() }()

	started := false
	insp, err := cli.ContainerInspect(ctx, LocalServerName)
	switch {
	case err == nil:
		if insp.Config == nil || !dockerutil.IsOurs(insp.Config.Labels) {
			return LocalServer{}, fmt.Errorf(
				"datastore.clickhouse: a container called %s exists and Antifailure did not "+
					"create it, so this will not use it; rename it or remove it",
				LocalServerName)
		}
		if insp.State == nil || !insp.State.Running {
			if err := cli.ContainerStart(ctx, insp.ID, container.StartOptions{}); err != nil {
				return LocalServer{}, fmt.Errorf(
					"datastore.clickhouse: starting the existing %s container: %w",
					LocalServerName, err)
			}
			started = true
		}
	case cerrdefs.IsNotFound(err):
		if opts.OnlyIfPresent {
			return LocalServer{}, ErrNoLocalServer
		}
		if err := createLocalServer(ctx, cli, opts); err != nil {
			return LocalServer{}, err
		}
		started = true
	default:
		return LocalServer{}, fmt.Errorf(
			"datastore.clickhouse: inspecting the %s container: %w", LocalServerName, err)
	}

	// Read back rather than remembered, because a container that already
	// existed was published on a port this process did not choose.
	fresh, err := cli.ContainerInspect(ctx, LocalServerName)
	if err != nil {
		return LocalServer{}, fmt.Errorf(
			"datastore.clickhouse: inspecting the %s container: %w", LocalServerName, err)
	}
	port, err := publishedPort(fresh.NetworkSettings.Ports)
	if err != nil {
		return LocalServer{}, err
	}
	url := secrets.New(fmt.Sprintf("http://default:%s@127.0.0.1:%d/default",
		localServerPassword, port))

	if started {
		progress("waiting for the local ClickHouse to accept queries")
	}
	if err := waitReady(ctx, url, opts.Ready); err != nil {
		return LocalServer{}, err
	}
	return LocalServer{URL: url, ContainerRef: fresh.ID, Started: started}, nil
}

func createLocalServer(
	ctx context.Context, cli *dockerclient.Client, opts LocalServerOptions,
) error {
	if err := ensureImage(ctx, cli, LocalServerImage); err != nil {
		return err
	}
	from := opts.PortFrom
	if from == 0 {
		resolved, err := dockerutil.PortRangeFrom(opts.Getenv)
		if err != nil {
			return err
		}
		from = resolved
	}
	port, err := dockerutil.NewPortAllocator(from).Free()
	if err != nil {
		return err
	}

	httpPort := nat.Port(strconv.Itoa(localServerPort) + "/tcp")
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image: LocalServerImage,
			// Labelled as ours, and that is not cosmetic: dockerutil refuses
			// to remove a container it cannot see a label on, so an
			// unlabelled one is created, used, and then left running forever.
			Labels: dockerutil.Managed("clickhouse-server", "", time.Now()),
			Env: []string{
				"CLICKHOUSE_PASSWORD=" + localServerPassword,
				"CLICKHOUSE_DB=default",
			},
			ExposedPorts: nat.PortSet{httpPort: struct{}{}},
		},
		&container.HostConfig{
			PortBindings: nat.PortMap{httpPort: []nat.PortBinding{{
				// Loopback only, which is what makes the fixed password
				// acceptable: the server is unreachable from anywhere but
				// this machine.
				HostIP: "127.0.0.1", HostPort: strconv.Itoa(port),
			}}},
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		}, nil, nil, LocalServerName)
	if err != nil {
		return fmt.Errorf(
			"datastore.clickhouse: creating the %s container: %w", LocalServerName, err)
	}
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf(
			"datastore.clickhouse: starting the %s container: %w", LocalServerName, err)
	}
	return nil
}

// ensureImage pulls the server image when it is not already present.
//
// The pull stream is drained before the image is used, because a pull that is
// still streaming is an image that is only partly there when the next call
// inspects it.
func ensureImage(ctx context.Context, cli *dockerclient.Client, ref string) error {
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	if err := airgap.CheckImage(airgap.SiteImagePull, ref); err != nil {
		return fmt.Errorf("datastore.clickhouse: %s is not present locally and cannot be pulled: %w", ref, err)
	}
	rc, err := cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("datastore.clickhouse: pull %s: %w", ref, err)
	}
	dockerutil.Discard(rc)
	if _, err := cli.ImageInspect(ctx, ref); err != nil {
		return fmt.Errorf("datastore.clickhouse: %s is not present after pulling it: %w", ref, err)
	}
	return nil
}

// waitReady polls until the server answers a query.
func waitReady(ctx context.Context, url secrets.Value, within time.Duration) error {
	c, err := parseURL(url)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(within)
	var last error
	for {
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, last = c.value(probe, "SELECT toString(1)", nil)
		cancel()
		if last == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"datastore.clickhouse: the local ClickHouse did not answer within %s: %w",
				within, last)
		}
		time.Sleep(time.Second)
	}
}

// publishedPort finds the host port a container publishes.
func publishedPort(ports nat.PortMap) (int, error) {
	for _, bindings := range ports {
		for _, b := range bindings {
			if n, err := strconv.Atoi(b.HostPort); err == nil && n > 0 {
				return n, nil
			}
		}
	}
	return 0, fmt.Errorf("datastore.clickhouse: the %s container publishes no port",
		LocalServerName)
}

// AttachToNetwork connects the server to an environment's network under an
// alias, and reports the port it listens on inside it.
//
// It is the same method the Postgres provider implements for the same reason:
// the connection string this hands out points at the host's loopback address,
// and a service running in a container is not on the host's loopback. It is on
// LocalServer rather than on Provider because the container belongs to the
// machine and the provider belongs to one store: two datastores on one server
// attach the same container under two names.
func (s *LocalServer) AttachToNetwork(ctx context.Context, ref, networkID, alias string) (int, error) {
	if ref == "" {
		ref = s.ContainerRef
	}
	return attachToNetwork(ctx, ref, networkID, alias)
}

func attachToNetwork(ctx context.Context, ref, networkID, alias string) (int, error) {
	cli, err := dockerutil.Client()
	if err != nil {
		return 0, err
	}
	defer func() { _ = cli.Close() }()

	insp, err := cli.ContainerInspect(ctx, ref)
	if err != nil {
		return 0, fmt.Errorf("datastore.clickhouse: inspecting %s: %w", ref, err)
	}
	if insp.Config == nil || !dockerutil.IsOurs(insp.Config.Labels) {
		return 0, fmt.Errorf("%w: container %s", dockerutil.ErrNotOurs, dockerutil.ShortID(ref))
	}
	already := false
	if insp.NetworkSettings != nil {
		for id, ep := range insp.NetworkSettings.Networks {
			if id == networkID || (ep != nil && ep.NetworkID == networkID) {
				already = true
				break
			}
		}
	}
	if !already {
		// Idempotent, because Up runs again on every push and reconnecting
		// would be an error every time after the first.
		err = cli.NetworkConnect(ctx, networkID, ref, &network.EndpointSettings{
			Aliases: []string{alias},
		})
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			return 0, fmt.Errorf("datastore.clickhouse: attaching %s to the environment "+
				"network: %w", dockerutil.ShortID(ref), err)
		}
	}
	// The port INSIDE the network, which is the server's own rather than the
	// published one.
	return localServerPort, nil
}
