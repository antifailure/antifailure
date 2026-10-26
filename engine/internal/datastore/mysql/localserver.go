package mysql

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
)

// The MySQL a golden lives on.
//
// The same arrangement the ClickHouse provider has and for the same reasons: one
// labelled container per machine, started the first time a manifest declares a
// MySQL datastore, holding every golden and every branch as databases, reached
// on loopback only and attached to an environment's network under the store's
// name when the environment comes up.

// LocalServerImage is the MySQL this provider runs. The 8.4 line is MySQL's
// long term support release.
const LocalServerImage = "mysql:8.4"

// LocalServerName is the container's name, which is how a second `af up`
// finds the first one's server.
const LocalServerName = "af-mysql"

// localServerPassword is root's password on a server that listens on loopback
// only and holds masked copies. It is a constant for the reason the ClickHouse
// one is: a password generated per machine would have to be stored somewhere,
// and the container's own environment is that somewhere already.
const localServerPassword = "antifailure-local-only"

// localServerPort is the port MySQL listens on inside the container.
const localServerPort = 3306

// LocalServer is a running local MySQL.
type LocalServer struct {
	// URL is root's address on this machine, with no database.
	URL secrets.Value
	// ContainerRef is the container, for attaching it to a network.
	ContainerRef string
	// Started reports that this call started it.
	Started bool
}

// LocalServerOptions configure EnsureLocalServer.
type LocalServerOptions struct {
	// PortFrom is the first host port tried. Zero reads AF_PORT_RANGE.
	PortFrom int
	// Getenv reads the environment, and may be nil.
	Getenv func(string) string
	// Ready bounds the wait for the server to answer. Zero uses
	// DefaultReadyTimeout.
	Ready time.Duration
	// Progress receives a line per step, and may be nil.
	Progress func(string)
	// OnlyIfPresent refuses to create a server, which is what a teardown
	// wants: there is nothing to drop on a server that does not exist.
	OnlyIfPresent bool
}

// ErrNoLocalServer is OnlyIfPresent finding nothing.
var ErrNoLocalServer = errors.New("datastore.mysql: there is no managed MySQL on this machine")

// DefaultReadyTimeout is how long a server is given to answer. The first start
// initialises a data directory, which takes most of a minute on a laptop.
const DefaultReadyTimeout = 3 * time.Minute

// EnsureLocalServer returns the machine's MySQL, starting it when it is not
// running and creating it when it does not exist.
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
	insp, err := cli.ContainerInspect(ctx, LocalServerName, dockerclient.ContainerInspectOptions{})
	switch {
	case err == nil:
		// A container of that name that this did not make is somebody's own
		// MySQL, and dropping databases on it is not this provider's to do.
		if insp.Container.Config == nil || !dockerutil.IsOurs(insp.Container.Config.Labels) {
			return LocalServer{}, fmt.Errorf(
				"datastore.mysql: a container called %s exists and Antifailure did not create "+
					"it, so this will not use it; rename it or remove it", LocalServerName)
		}
		if insp.Container.State == nil || !insp.Container.State.Running {
			if _, err := cli.ContainerStart(ctx, insp.Container.ID, dockerclient.ContainerStartOptions{}); err != nil {
				return LocalServer{}, fmt.Errorf(
					"datastore.mysql: starting the existing %s container: %w", LocalServerName, err)
			}
			started = true
		}
	case cerrdefs.IsNotFound(err):
		if opts.OnlyIfPresent {
			return LocalServer{}, ErrNoLocalServer
		}
		progress("starting a local MySQL for the datastores that hold one")
		if err := createLocalServer(ctx, cli, opts); err != nil {
			return LocalServer{}, err
		}
		started = true
	default:
		return LocalServer{}, fmt.Errorf(
			"datastore.mysql: inspecting the %s container: %w", LocalServerName, err)
	}

	fresh, err := cli.ContainerInspect(ctx, LocalServerName, dockerclient.ContainerInspectOptions{})
	if err != nil {
		return LocalServer{}, fmt.Errorf(
			"datastore.mysql: inspecting the %s container: %w", LocalServerName, err)
	}
	var ports network.PortMap
	if fresh.Container.NetworkSettings != nil {
		ports = fresh.Container.NetworkSettings.Ports
	}
	port, err := publishedPort(ports)
	if err != nil {
		return LocalServer{}, err
	}
	url := secrets.New(fmt.Sprintf("mysql://root:%s@127.0.0.1:%d/", localServerPassword, port))

	if started {
		progress("waiting for the local MySQL to accept connections")
	}
	if err := waitReady(ctx, url, opts.Ready); err != nil {
		return LocalServer{}, err
	}
	return LocalServer{URL: url, ContainerRef: fresh.Container.ID, Started: started}, nil
}

func createLocalServer(ctx context.Context, cli *dockerclient.Client, opts LocalServerOptions) error {
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
	ports := dockerutil.NewPortAllocator(from)
	for attempt := 0; ; attempt++ {
		port, err := ports.Free()
		if err != nil {
			return err
		}
		err = startLocalServerOn(ctx, cli, port)
		if err == nil {
			return nil
		}
		if !dockerutil.IsPortTaken(err) || attempt >= dockerutil.PortRetries {
			return err
		}
	}
}

func startLocalServerOn(ctx context.Context, cli *dockerclient.Client, port int) error {
	sqlPort := network.MustParsePort(strconv.Itoa(localServerPort) + "/tcp")
	resp, err := cli.ContainerCreate(ctx, dockerclient.ContainerCreateOptions{
		Config: &container.Config{
			Image:  LocalServerImage,
			Labels: dockerutil.Managed("mysql-server", "", time.Now()),
			Env:    []string{"MYSQL_ROOT_PASSWORD=" + localServerPassword},
			// The server's own defaults, written down so that a later image
			// whose defaults moved does not change what a golden is: utf8mb4
			// for a database that does not name a character set, and table
			// names compared as they are spelled, which is what production on
			// Linux does.
			Cmd: []string{
				"--character-set-server=utf8mb4",
				"--collation-server=utf8mb4_0900_ai_ci",
				"--lower-case-table-names=0",
				"--skip-name-resolve",
			},
			ExposedPorts: network.PortSet{sqlPort: struct{}{}},
		},
		HostConfig: &container.HostConfig{
			// Loopback only. The server holds masked copies and a known root
			// password, and neither is anything to offer the network.
			PortBindings: network.PortMap{sqlPort: []network.PortBinding{{
				HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(port),
			}}},
			RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		},
		Name: LocalServerName,
	})
	if err != nil {
		return fmt.Errorf("datastore.mysql: creating the %s container: %w", LocalServerName, err)
	}
	if _, err := cli.ContainerStart(ctx, resp.ID, dockerclient.ContainerStartOptions{}); err != nil {
		startErr := fmt.Errorf("datastore.mysql: starting the %s container: %w", LocalServerName, err)
		_, rmErr := cli.ContainerRemove(context.WithoutCancel(ctx), resp.ID,
			dockerclient.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
		if rmErr != nil && !cerrdefs.IsNotFound(rmErr) {
			return errors.Join(startErr, fmt.Errorf(
				"datastore.mysql: removing the %s container that did not start, so its name is "+
					"still taken: %w", LocalServerName, rmErr))
		}
		return startErr
	}
	return nil
}

func ensureImage(ctx context.Context, cli *dockerclient.Client, ref string) error {
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	if err := airgap.CheckImage(airgap.SiteImagePull, ref); err != nil {
		return fmt.Errorf("datastore.mysql: %s is not present locally and cannot be pulled: %w", ref, err)
	}
	rc, err := cli.ImagePull(ctx, ref, dockerclient.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("datastore.mysql: pull %s: %w", ref, err)
	}
	dockerutil.Discard(rc)
	if _, err := cli.ImageInspect(ctx, ref); err != nil {
		return fmt.Errorf("datastore.mysql: %s is not present after pulling it: %w", ref, err)
	}
	return nil
}

// waitReady polls until the server completes a query.
//
// A fresh MySQL container starts a temporary server to initialise its data
// directory, stops it, and starts the real one, and the temporary server
// listens only on a socket. So a connection that succeeds is the real server,
// and one refused in between is waited through rather than reported.
func waitReady(ctx context.Context, url secrets.Value, within time.Duration) error {
	s, err := parseURL(url)
	if err != nil {
		return err
	}
	db, err := s.open(sessionPlain)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	deadline := time.Now().Add(within)
	var last error
	for {
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		last = ping(probe, db)
		cancel()
		if last == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("datastore.mysql: the local MySQL did not answer within %s: %w",
				within, last)
		}
		time.Sleep(time.Second)
	}
}

func publishedPort(ports network.PortMap) (int, error) {
	for _, bindings := range ports {
		for _, b := range bindings {
			if n, err := strconv.Atoi(b.HostPort); err == nil && n > 0 {
				return n, nil
			}
		}
	}
	return 0, fmt.Errorf("datastore.mysql: the %s container publishes no port", LocalServerName)
}

// AttachToNetwork connects the server to an environment's network under the
// store's name, and reports the port it listens on there. It is what lets a
// service reach the branch as name:3306.
func (s *LocalServer) AttachToNetwork(ctx context.Context, ref, networkID, alias string) (int, error) {
	if ref == "" {
		ref = s.ContainerRef
	}
	cli, err := dockerutil.Client()
	if err != nil {
		return 0, err
	}
	defer func() { _ = cli.Close() }()

	insp, err := cli.ContainerInspect(ctx, ref, dockerclient.ContainerInspectOptions{})
	if err != nil {
		return 0, fmt.Errorf("datastore.mysql: inspecting %s: %w", ref, err)
	}
	if insp.Container.Config == nil || !dockerutil.IsOurs(insp.Container.Config.Labels) {
		return 0, fmt.Errorf("%w: container %s", dockerutil.ErrNotOurs, dockerutil.ShortID(ref))
	}
	if insp.Container.NetworkSettings != nil {
		for id, ep := range insp.Container.NetworkSettings.Networks {
			if id == networkID || (ep != nil && ep.NetworkID == networkID) {
				return localServerPort, nil
			}
		}
	}
	_, err = cli.NetworkConnect(ctx, networkID, dockerclient.NetworkConnectOptions{
		Container:      ref,
		EndpointConfig: &network.EndpointSettings{Aliases: []string{alias}},
	})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return 0, fmt.Errorf("datastore.mysql: attaching %s to the environment network: %w",
			dockerutil.ShortID(ref), err)
	}
	return localServerPort, nil
}
