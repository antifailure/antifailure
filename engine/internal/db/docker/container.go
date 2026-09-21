package docker

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/antifailure/antifailure/engine/internal/db/pgcopy"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
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
//
// A manifest may name its own image instead, and then the major comes from the
// image rather than from the tag this would have built. That is the whole
// point of naming one: the stock image carries the contrib modules and nothing
// else, so a schema using PostGIS, pgvector, TimescaleDB or a table access
// method out of an extension could not be copied into a golden at all. The
// declared version is not ignored when an image is named; it is CHECKED
// against what the image's server reports, in versionMatches, because a golden
// silently built on the wrong major is an environment running a Postgres the
// application does not.
func (p *Provider) imageFor(version int) string {
	if p.image != "" {
		return p.image
	}
	if version == 0 {
		version = p.version
	}
	return fmt.Sprintf("postgres:%d-alpine", version)
}

// start creates and runs a container, pulling the image if it is absent.
//
// preload is the libraries this container's postmaster loads beyond the
// statistics module, and it is a parameter rather than a field because the two
// callers get it from different places: a candidate takes what the manifest
// declares, and a branch takes what the golden image records it was built
// with, which is the only one of the two that cannot be wrong.
func (p *Provider) start(
	ctx context.Context, name, img string, labels map[string]string, preload []string,
) (started, error) {
	return p.startWithRetry(ctx, name, img, labels, preload, 0)
}

func (p *Provider) startWithRetry(
	ctx context.Context, name, img string, labels map[string]string, preload []string, attempt int,
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
	if err := p.checkDataDirectory(ctx, img); err != nil {
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

	hostPort := network.MustParsePort("5432/tcp")
	resp, err := p.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  img,
			Labels: all,
			// The image's own entrypoint, told to preload the statistics
			// module and anything else this chain needs loaded at start.
			// It has to be preloaded at start: created without the preload,
			// pg_stat_statements exists and records nothing, so the insights
			// read a permanently empty table and reported that statement
			// timing was unavailable on every environment this provider made.
			// The same two flags the repository's own CI Postgres is started
			// with, so a local branch and a CI branch measure the same thing.
			// A committed golden image keeps the entrypoint, so this holds
			// for a candidate, a golden and a branch alike.
			Cmd: serverCmd(preload),
			Env: []string{
				"POSTGRES_PASSWORD=" + managedPassword,
				"POSTGRES_USER=antifailure",
				"POSTGRES_DB=antifailure",
				"PGDATA=" + dataDir,
			},
			ExposedPorts: network.PortSet{hostPort: struct{}{}},
			Healthcheck: &container.HealthConfig{
				Test:     []string{"CMD-SHELL", "pg_isready -U antifailure -d antifailure"},
				Interval: time.Second,
				Timeout:  2 * time.Second,
				Retries:  30,
			},
		},
		HostConfig: &container.HostConfig{
			PortBindings: network.PortMap{hostPort: []network.PortBinding{{
				// Loopback only. This is the security boundary that makes the
				// fixed password acceptable: the database is unreachable from
				// anywhere but this machine.
				HostIP:   netip.MustParseAddr("127.0.0.1"),
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
		Name: name,
	})
	if err != nil {
		if isNoSpace(err) {
			return started{}, aferrors.Wrap(err, aferrors.AFRUN020, "detail", err.Error())
		}
		return started{}, fmt.Errorf("db.docker: create the container %s: %w", name, err)
	}

	if _, err := p.cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
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
		if dockerutil.IsPortTaken(err) && attempt < dockerutil.PortRetries {
			return p.startWithRetry(ctx, name, img, labels, preload, attempt+1)
		}
		return started{}, fmt.Errorf("db.docker: start the container %s: %w", name, err)
	}
	return started{id: resp.ID, name: name, port: port}, nil
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
	if err := airgap.CheckImage(airgap.SiteImagePull, ref); err != nil {
		return fmt.Errorf("db.docker: %s is not present locally and cannot be pulled: %w", ref, err)
	}
	rc, err := p.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
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

// checkDataDirectory refuses an image that would swallow the data directory.
//
// This exists because naming your own image re-opens the failure the dataDir
// constant above was written to close, and re-opens it in a form nothing
// downstream can see. A golden is `docker commit` of a container, commit
// captures the writable layer, and anything written under a path the image
// declares as a VOLUME goes to an anonymous volume instead of that layer. The
// stock image declares /var/lib/postgresql/data, which is exactly why PGDATA
// is moved somewhere else here; an image somebody built themselves is free to
// declare anything, including the somewhere else.
//
// The result of getting this wrong is not an error. The candidate starts, the
// copy succeeds, the masking succeeds, the verification succeeds, the commit
// succeeds, and every branch of the published golden is an immaculate empty
// Postgres. That is the one outcome worth a check of its own, because a wrong
// golden that reports success is how unmasked or absent data reaches a preview
// nobody inspects.
//
// Only the declared image is inspected. A volume the operator adds at run time
// is not this provider's to see, and the daemon is the one that would report
// it.
func (p *Provider) checkDataDirectory(ctx context.Context, img string) error {
	info, err := p.cli.ImageInspect(ctx, img)
	if err != nil {
		// The image was pulled or found a moment ago, so a failure here is the
		// daemon rather than the image. It is not this check's business to
		// turn that into its own refusal.
		return nil
	}
	if info.Config == nil {
		return nil
	}
	for declared := range info.Config.Volumes {
		if declared != dataDir && !strings.HasPrefix(dataDir, strings.TrimSuffix(declared, "/")+"/") {
			continue
		}
		return aferrors.Coded(aferrors.AFDB038,
			"image", img, "volume", declared, "datadir", dataDir)
	}
	return nil
}

// versionMatches refuses a candidate whose server is not the declared major.
//
// A named image decides its own Postgres version and a manifest declares one,
// and the two disagreeing is neither rare nor visible: pgvector/pgvector:pg16
// beside `version: 17` is a plausible thing to write and produces a golden
// every branch of which runs a Postgres the application does not. Nothing
// later catches it. The copy works, because pg_dump matches the SOURCE major
// rather than the target's; the branch works; the application starts.
//
// Checked against the server rather than against the tag, because a tag is a
// string somebody chose and server_version_num is what is actually running.
func (p *Provider) versionMatches(ctx context.Context, conn secrets.Value, want int, img string) error {
	if want == 0 {
		want = p.version
	}
	if want == 0 || p.image == "" {
		// Nothing was declared, or this provider built the tag itself from the
		// declared major, in which case the two cannot disagree.
		return nil
	}
	found := pgcopy.ServerMajor(ctx, conn)
	if found == 0 {
		// Zero is "could not ask". The server answered every readiness probe a
		// moment ago, so a failure here is a transient read rather than
		// evidence of a mismatch, and refusing on it would refuse to build a
		// golden at all for a reason that is not about the version.
		return nil
	}
	if found == want {
		return nil
	}
	return aferrors.Coded(aferrors.AFDB039,
		"image", img, "found", strconv.Itoa(found), "declared", strconv.Itoa(want))
}

// createExtensions creates what the manifest declared, in the order given.
//
// Before the source is copied in, which is the only order that works: a dump
// of a schema using an extension's types carries `CREATE EXTENSION` of its
// own, and a restore of one the image does not have stops on it with a message
// about the extension being unavailable. Creating them first does not make
// that dump succeed, but it does mean an extension the image HAS and the
// source's dump does not mention is present, which is what a table created
// `USING` an access method out of an extension needs before its CREATE TABLE
// arrives.
//
// IF NOT EXISTS, because an image such as citus creates its own extensions in
// the template database and a manifest naming one of those is right rather
// than wrong.
//
// One statement per extension rather than one script, so that the extension
// that failed is named. A script fails as a whole and the transcript names a
// line number in something nobody wrote.
func (p *Provider) createExtensions(ctx context.Context, conn secrets.Value, img string) error {
	for _, name := range p.extensions {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// Quoted, and the quoting is the validation. The manifest schema
		// already refuses a name that is not an identifier, and this is the
		// second of the two: a name reaching here is interpolated into DDL,
		// and an extension is server side code.
		stmt := `CREATE EXTENSION IF NOT EXISTS "` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if err := p.execSQL(ctx, conn, stmt); err != nil {
			return aferrors.Wrap(err, aferrors.AFDB040, "extension", name, "image", img)
		}
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
	if _, err := p.cli.ContainerStop(ctx, ref, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
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
	_, err := p.cli.ContainerRemove(ctx, ref, client.ContainerRemoveOptions{
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
	out, err := p.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: args})
	if err != nil {
		return nil, fmt.Errorf("db.docker: list containers: %w", err)
	}
	return out.Items, nil
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

// statisticsLibrary is the module every container this provider starts
// preloads, whatever else it preloads.
//
// It is not negotiable and it is not a default. Created without the preload,
// pg_stat_statements exists and records nothing, so the insights read a
// permanently empty table and reported that statement timing was unavailable
// on every environment this provider made. A manifest asking for timescaledb
// is asking for one more library, never for a different list, which is why
// serverCmd appends and cannot replace.
const statisticsLibrary = "pg_stat_statements"

// serverCmd is the command a container this provider starts runs: the image's
// postgres, with the statistics module preloaded, plus whatever the manifest
// asked to have loaded at server start.
//
// Preloading is the half of extension support that CREATE EXTENSION cannot do.
// timescaledb, citus and pg_cron are loaded by the postmaster before any
// database exists, and a server carrying such an extension's catalog entries
// without its library refuses to start rather than starting degraded, so the
// list has to reach the command line of every container in the chain: the
// candidate the golden is built in, and every branch started from the image it
// was committed to.
//
// THE DECLARED LIBRARIES COME FIRST AND THE STATISTICS MODULE LAST, and that
// order is measured rather than chosen. citus refuses to load from anywhere
// but the front: started as
// `shared_preload_libraries=pg_stat_statements,citus` the postmaster exits
// during initdb's own first start with "FATAL: Citus has to be loaded first"
// and the hint "Place citus at the beginning of shared_preload_libraries", so
// the container never accepts a connection and the refresh fails five minutes
// later at the readiness deadline with nothing but a refused dial. Putting the
// statistics module first cost nothing anywhere else and made one real
// extension impossible to use.
//
// Nothing has the opposite requirement. pg_stat_statements installs executor
// hooks and chains with whatever else installed them, in either order, which
// is why it is the one that moves. A manifest naming it explicitly keeps its
// own position and is not repeated.
//
// The result is deterministic in the order given and de-duplicated. Exported
// to the test that proves a branch was started with it, because the proof has
// to read what the server says rather than what this file says.
func serverCmd(preload []string) []string {
	var libraries []string
	seen := map[string]bool{}
	for _, lib := range preload {
		lib = strings.TrimSpace(lib)
		if lib == "" || seen[lib] {
			continue
		}
		seen[lib] = true
		libraries = append(libraries, lib)
	}
	if !seen[statisticsLibrary] {
		libraries = append(libraries, statisticsLibrary)
	}
	return []string{
		"postgres",
		"-c", "shared_preload_libraries=" + strings.Join(libraries, ","),
		"-c", "pg_stat_statements.track=all",
	}
}
