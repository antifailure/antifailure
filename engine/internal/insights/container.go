package insights

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/secrets"
)

// ContainerApplier runs the project's own migrate command inside the service's
// built image.
//
// This is the only honest way to rehearse a migration written in Ruby, Python
// or JavaScript: only ActiveRecord knows what a Rails migration becomes, only
// Django knows what a Django one becomes, and the answer depends on the gems
// and packages in the image rather than on whatever happens to be installed on
// the machine running `af insights`. Running the tool on the workstation would
// rehearse a different thing than the deploy does, which is worse than not
// rehearsing, because it produces a result somebody would believe.
//
// It reports no statements. The server does that instead, through the event
// trigger capture, which is how a tool-driven migration still gets a duration
// per statement.
type ContainerApplier struct {
	// Image is the service's built image.
	Image string
	// Command is the manifest's migrate command for that service.
	Command string
	// URLVar is the variable the application reads its connection string from,
	// which is database.url_env in the manifest. Empty means DATABASE_URL.
	URLVar string
	// Env is anything else the migrate command needs.
	Env map[string]secrets.Value
	// Database is the provider, when its branches are local containers.
	// DatabaseRef names the branch's container. Given both, the migration
	// joins a network holding that container and nothing else. Nil means the
	// database is reachable without help, which is the case for a hosted
	// provider whose connection string already works from anywhere.
	Database    Attachable
	DatabaseRef string
	// EnvID labels everything created, so the leak detector finds it.
	EnvID string
	// Progress receives lines already safe to print.
	Progress func(string)
}

// Attachable is a database provider whose branches are local containers.
//
// It is declared here rather than imported so this package does not depend on
// the runtime, which declares the same shape for the same reason. Go matches
// interfaces structurally, so the Docker provider satisfies both without
// knowing either exists.
type Attachable interface {
	// AttachToNetwork connects a branch's container to a network under an
	// alias, and reports the port it listens on inside that network.
	AttachToNetwork(ctx context.Context, ref, networkID, alias string) (int, error)
}

// Name identifies the applier.
func (*ContainerApplier) Name() string { return "container" }

// databaseAlias is the name the migration reaches the branch by.
//
// A fixed alias rather than the container's own name, because the connection
// string has to be rewritten to point at it and a short stable name keeps that
// rewrite readable in a log.
const databaseAlias = "af-rehearsal-db"

// Apply runs the migrate command to completion and fails on a non-zero exit.
//
// The network it runs on is created internal, so Docker gives it no route off
// the machine. That is not decoration: this container holds a connection to a
// copy of production's data, and the product's whole premise is that an
// environment has nowhere to send a packet. A rehearsal that quietly ran with
// the internet attached would be the one hole in it.
func (a *ContainerApplier) Apply(
	ctx context.Context, url secrets.Value, _ []Migration,
) ([]StatementTiming, error) {
	if a.Image == "" || a.Command == "" {
		return nil, fmt.Errorf("insights: no image or migrate command to run")
	}
	cli, err := dockerutil.Client()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()

	progress := a.Progress
	if progress == nil {
		progress = func(string) {}
	}

	reachable := url
	var netID string
	if a.Database != nil && a.DatabaseRef != "" {
		var port int
		netID, port, err = a.joinDatabase(ctx, cli)
		if err != nil {
			return nil, err
		}
		defer func() {
			c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
			defer cancel()
			// netID is the id NetworkCreate returned to this call, not a name
			// resolved afterwards, so the network being removed is provably
			// the one this function made. That is the difference between
			// naming a resource and proving it is yours, and it is why these
			// two lines do not need the label check dockerutil.RemoveContainer
			// applies: there is no name for a foreign network to collide with.
			//
			// The disconnect is of somebody else's container from our network,
			// which removes nothing and is what lets the network go.
			_ = cli.NetworkDisconnect(c, netID, a.DatabaseRef, true)
			_ = cli.NetworkRemove(c, netID)
		}()
		rewritten, rErr := rewriteHost(url, databaseAlias, port)
		if rErr != nil {
			return nil, rErr
		}
		reachable = rewritten
	}

	variable := a.URLVar
	if variable == "" {
		variable = "DATABASE_URL"
	}
	env := []string{variable + "=" + reachable.Reveal()}
	for k, v := range a.Env {
		env = append(env, k+"="+v.Reveal())
	}

	name := "af-rehearse-" + a.EnvID
	// A container left by an interrupted run holds the name, and adopting one
	// would mean reading the result of a run nobody recorded.
	_ = dockerutil.RemoveContainer(ctx, cli, name)

	hostCfg := &container.HostConfig{RestartPolicy: container.RestartPolicy{Name: "no"}}
	if netID != "" {
		hostCfg.NetworkMode = container.NetworkMode(netID)
	} else {
		// No database container to join, so the migration reaches a hosted
		// provider the ordinary way. There is nothing to seal it off from
		// here: the database it must reach is on the internet.
		hostCfg.NetworkMode = "bridge"
	}

	progress("rehearsing the migrations in " + a.Image)
	created, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:  a.Image,
			Env:    env,
			Cmd:    []string{"sh", "-lc", a.Command},
			Labels: dockerutil.Managed("rehearsal", a.EnvID, time.Now().UTC()),
		}, hostCfg, nil, nil, name)
	if err != nil {
		return nil, fmt.Errorf("insights: create the rehearsal container: %w", err)
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		_ = dockerutil.RemoveContainer(c, cli, created.ID)
	}()

	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("insights: start the rehearsal container: %w", err)
	}

	statusCh, errCh := cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return nil, fmt.Errorf("insights: waiting for the migration: %w", err)
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			// The tool's own output is the finding. A migration tool explains
			// its failure far better than an exit code does, and discarding
			// that in favour of "exit 1" would make the report useless in
			// exactly the case it exists for.
			return nil, fmt.Errorf("the migration command exited %d: %s",
				status.StatusCode, lastLines(ctx, cli, created.ID))
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// The applier does not know what it ran; the server does.
	return nil, nil
}

// joinDatabase makes a network with no route off the machine and asks the
// provider to put the branch on it.
//
// The provider is asked rather than told, because it knows which port its
// container listens on inside a network, which is not the published one the
// connection string carries.
func (a *ContainerApplier) joinDatabase(
	ctx context.Context, cli *client.Client,
) (netID string, port int, err error) {
	created, err := cli.NetworkCreate(ctx, "af-rehearse-"+a.EnvID, network.CreateOptions{
		Driver: "bridge",
		// No route to anywhere but the other container on it.
		Internal: true,
		Labels:   dockerutil.Managed("rehearsal", a.EnvID, time.Now().UTC()),
	})
	if err != nil {
		return "", 0, fmt.Errorf("insights: create the rehearsal network: %w", err)
	}
	port, err = a.Database.AttachToNetwork(ctx, a.DatabaseRef, created.ID, databaseAlias)
	if err != nil {
		// Removed by the id the create above returned, so it is provably the
		// network this call made rather than one that answers to the name.
		_ = cli.NetworkRemove(ctx, created.ID)
		return "", 0, fmt.Errorf("insights: attach the branch to the rehearsal network: %w", err)
	}
	return created.ID, port, nil
}

// rewriteHost points a connection string at a host and port reachable from
// inside a container.
//
// The provider hands out a string for this machine, usually 127.0.0.1 and a
// published port, and inside a container that address is the container itself.
// Everything else about the string, and in particular the credential, is
// carried through untouched.
func rewriteHost(v secrets.Value, host string, port int) (secrets.Value, error) {
	u, err := url.Parse(v.Reveal())
	if err != nil {
		return secrets.Value{}, fmt.Errorf("insights: the connection string could not be read")
	}
	u.Host = net.JoinHostPort(host, fmt.Sprint(port))
	return secrets.New(u.String()), nil
}

// lastLines is the tail of a failed migration's output.
func lastLines(ctx context.Context, cli *client.Client, id string) string {
	logs, err := cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: "20",
	})
	if err != nil {
		return ""
	}
	defer dockerutil.Discard(logs)
	buf := make([]byte, 8192)
	n, _ := logs.Read(buf)
	// Docker multiplexes stdout and stderr with an eight byte header per
	// frame. Stripping the non printing bytes is enough to make it readable
	// without demultiplexing properly, which this does not need to do.
	var b strings.Builder
	for _, c := range string(buf[:n]) {
		if c == '\n' || c == '\t' || (c >= 32 && c < 127) || c > 159 {
			b.WriteRune(c)
		}
	}
	return strings.TrimSpace(b.String())
}
