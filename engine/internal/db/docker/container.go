package docker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/go-connections/nat"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// started describes a container this provider created.
type started struct {
	id   string
	name string
	port int
}

// dataDir is where Postgres keeps its data directory, and it is deliberately
// not the image's default.
//
// The official Postgres image declares /var/lib/postgresql/data as a VOLUME.
// Anything written under a declared volume goes to an anonymous volume rather
// than to the container's writable layer, and docker commit captures only the
// writable layer. Leaving PGDATA at the default therefore produces a golden
// image that is a perfectly good Postgres installation containing no data at
// all, and every branch from it starts empty.
//
// The failure is silent, which is what makes it dangerous: the container
// starts, accepts connections, and answers queries. Only the rows are missing.
// The conformance suite caught it because it reads a known row back rather
// than trusting that the branch exists.
const dataDir = "/var/lib/antifailure/pgdata"

// imageFor returns the Postgres image for a major version.
//
// The tag is pinned to the major and the base is alpine, which is what keeps a
// golden image small enough that committing one is a few seconds rather than a
// minute. The digest is not pinned here because the image is a local
// convenience rather than a shipped artifact; the images the project publishes
// itself are digest pinned in their Dockerfiles.
func (p *Provider) imageFor(version int) string {
	if version == 0 {
		version = p.version
	}
	return fmt.Sprintf("postgres:%d-alpine", version)
}

// start creates and runs a container, pulling the image if it is absent.
func (p *Provider) start(ctx context.Context, name, img string, labels map[string]string) (started, error) {
	return p.startWithRetry(ctx, name, img, labels, 0)
}

func (p *Provider) startWithRetry(
	ctx context.Context, name, img string, labels map[string]string, attempt int,
) (started, error) {
	// A container left by a previous run under the same deterministic name is
	// removed rather than adopted. Adopting one would mean starting an
	// environment on data whose provenance nothing recorded.
	if err := p.remove(ctx, name); err != nil {
		return started{}, err
	}
	if err := p.ensureImage(ctx, img); err != nil {
		return started{}, err
	}

	port, err := p.freePort()
	if err != nil {
		return started{}, err
	}

	all := map[string]string{
		LabelManaged: dockerutil.ManagedValue,
		LabelCreated: p.clock.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range labels {
		all[k] = v
	}

	hostPort := nat.Port("5432/tcp")
	resp, err := p.cli.ContainerCreate(ctx,
		&container.Config{
			Image:  img,
			Labels: all,
			Env: []string{
				"POSTGRES_PASSWORD=" + managedPassword,
				"POSTGRES_USER=antifailure",
				"POSTGRES_DB=antifailure",
				"PGDATA=" + dataDir,
			},
			ExposedPorts: nat.PortSet{hostPort: struct{}{}},
			Healthcheck: &container.HealthConfig{
				Test:     []string{"CMD-SHELL", "pg_isready -U antifailure -d antifailure"},
				Interval: time.Second,
				Timeout:  2 * time.Second,
				Retries:  30,
			},
		},
		&container.HostConfig{
			PortBindings: nat.PortMap{hostPort: []nat.PortBinding{{
				// Loopback only. This is the security boundary that makes the
				// fixed password acceptable: the database is unreachable from
				// anywhere but this machine.
				HostIP:   "127.0.0.1",
				HostPort: strconv.Itoa(port),
			}}},
			// The container is removed when it stops, so a crashed run leaves
			// no stopped container to accumulate.
			AutoRemove:    false,
			RestartPolicy: container.RestartPolicy{Name: "no"},
			// Postgres wants more shared memory than the daemon's default,
			// and the failure without it is a confusing crash under load
			// rather than a clear message.
			ShmSize: 256 << 20,
		},
		nil, nil, name)
	if err != nil {
		if isNoSpace(err) {
			return started{}, aferrors.Wrap(err, aferrors.AFRUN020, "detail", err.Error())
		}
		return started{}, fmt.Errorf("db.docker: create the container %s: %w", name, err)
	}

	if err := p.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = p.remove(context.WithoutCancel(ctx), resp.ID)
		// A port that was free when it was probed and taken when it was bound.
		//
		// The allocator asks the kernel whether it can listen on a port before
		// handing it out, which is the best a separate process can do and is
		// not a lock: anything else on the machine can take it in the gap
		// between the probe and the daemon's bind. On a laptop running two of
		// these at once that gap is hit regularly, and the failure was fatal
		// to the whole command with a message about a port number.
		//
		// Retried rather than surfaced, because the situation is transient by
		// construction and the next port is very likely free. The attempts are
		// bounded so that a machine with no free ports at all still reports
		// that rather than looping.
		if isPortTaken(err) && attempt < portRetries {
			return p.startWithRetry(ctx, name, img, labels, attempt+1)
		}
		return started{}, fmt.Errorf("db.docker: start the container %s: %w", name, err)
	}
	return started{id: resp.ID, name: name, port: port}, nil
}

// portRetries bounds how many times a lost race is retried.
//
// Three, because each attempt takes a different port and the chance of losing
// three races in a row is negligible unless something is wrong in a way more
// attempts would not fix.
const portRetries = 3

// isPortTaken reports whether the daemon refused because the host port was
// already bound.
//
// Matched on the message because the daemon does not give this a distinct
// error type. The match is deliberately narrow: a broader one would swallow a
// real networking failure and retry it three times before reporting it.
func isPortTaken(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "port is already allocated") ||
		strings.Contains(msg, "address already in use")
}

func isNoSpace(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no space left") || strings.Contains(msg, "disk quota")
}

// ensureImage pulls an image if the daemon does not already have it.
func (p *Provider) ensureImage(ctx context.Context, ref string) error {
	if _, err := p.cli.ImageInspect(ctx, ref); err == nil {
		return nil
	}
	rc, err := p.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("db.docker: pull %s: %w", ref, err)
	}
	// The stream has to be drained before the pull is finished, or the image
	// is only partly present when the next call inspects it.
	dockerutil.Discard(rc)
	if _, err := p.cli.ImageInspect(ctx, ref); err != nil {
		return fmt.Errorf("db.docker: %s is not present after pulling it: %w", ref, err)
	}
	return nil
}

// stop shuts a container down cleanly.
//
// The timeout is generous on purpose: Postgres flushes its buffers on
// shutdown, and killing it instead leaves a database that has to recover on
// every start, which turns a two second branch into a fifteen second one.
func (p *Provider) stop(ctx context.Context, ref string) error {
	timeout := 30
	if err := p.cli.ContainerStop(ctx, ref, container.StopOptions{Timeout: &timeout}); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("db.docker: stop %s: %w", ref, err)
	}
	return nil
}

// remove deletes a container. Removing one that is already gone succeeds,
// because teardown retries and a crash leaves a partial state.
func (p *Provider) remove(ctx context.Context, ref string) error {
	err := p.cli.ContainerRemove(ctx, ref, container.RemoveOptions{
		Force: true,
		// The anonymous volume Postgres creates for its data directory goes
		// with the container. Leaving it is the most common way a Docker based
		// tool fills a laptop's disk.
		RemoveVolumes: true,
	})
	if err == nil || cerrdefs.IsNotFound(err) {
		return nil
	}
	// A removal already in progress is the same outcome as a removal, and
	// treating it as an error makes a concurrent teardown fail for no reason.
	if strings.Contains(strings.ToLower(err.Error()), "already in progress") {
		return nil
	}
	return fmt.Errorf("db.docker: remove %s: %w", ref, err)
}

// listContainers returns the containers this provider manages, optionally of
// one kind.
func (p *Provider) listContainers(ctx context.Context, kind string) ([]container.Summary, error) {
	args := dockerutil.Filter()
	if kind != "" {
		args.Add("label", LabelKind+"="+kind)
	}
	out, err := p.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, fmt.Errorf("db.docker: list containers: %w", err)
	}
	return out, nil
}

// findBranch returns the existing branch for an environment, if there is one.
//
// This is what makes Branch idempotent, and idempotence is what stops a retry
// after a timeout from creating a second container the caller has no
// identifier for.
func (p *Provider) findBranch(ctx context.Context, envID string) (provider.Branch, bool, error) {
	list, err := p.listContainers(ctx, "branch")
	if err != nil {
		return provider.Branch{}, false, err
	}
	for _, c := range list {
		if c.Labels[LabelEnv] != envID {
			continue
		}
		if c.State != "running" {
			// A stopped branch is not a usable one. Removing it lets the
			// caller create a fresh container rather than hand an environment
			// a database that is not accepting connections.
			if err := p.remove(ctx, c.ID); err != nil {
				return provider.Branch{}, false, err
			}
			return provider.Branch{}, false, nil
		}
		created, _ := time.Parse(time.RFC3339, c.Labels[LabelCreated])
		return provider.Branch{
			EnvID: envID, From: c.Labels[LabelGolden],
			ProviderRef: c.ID, CreatedAt: created,
		}, true, nil
	}
	return provider.Branch{}, false, nil
}

// connString builds the connection string for a published port.
func (p *Provider) connString(port int) secrets.Value {
	return secrets.NewFrom(fmt.Sprintf(
		"postgres://antifailure:%s@127.0.0.1:%d/antifailure?sslmode=disable",
		managedPassword, port), "docker")
}
