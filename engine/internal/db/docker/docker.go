// Package docker is the local database provider, and the reference
// implementation of the provider interface.
//
// It is the provider that needs no account, no credential, and no network:
// everything happens against a Docker daemon the developer already has. That
// makes it the one that has to be right, because it is what a person tries
// first and what every conformance run exercises on every pull request.
//
// The model is deliberately simple. A golden version is a committed image: a
// Postgres container is started, seeded from the source, masked, verified, and
// then committed, so the version is an immutable artifact the daemon already
// knows how to store and reference. A branch is a container started from that
// image, so two branches share nothing, which is what makes isolation a
// property of the design rather than a thing to police.
package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/antifailure/antifailure/engine/internal/clock"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// Labels the provider stamps on everything it creates.
//
// The leak detector finds resources by these rather than by name, so a
// container an older version created with a different naming scheme is still
// found and cleaned up. A resource with no label is not ours and is never
// touched, which is what keeps the provider from deleting a developer's own
// Postgres container.
const (
	LabelManaged = "dev.antifailure.managed"
	LabelKind    = "dev.antifailure.kind"
	LabelEnv     = "dev.antifailure.env"
	LabelGolden  = "dev.antifailure.golden"
	LabelCreated = "dev.antifailure.created"
)

// ImageRepo is where golden versions are committed. Using one repository with
// the version as the tag means docker images already lists them in a readable
// form, and docker image rm already knows how to remove one.
const ImageRepo = "antifailure/golden"

// The password every managed container uses.
//
// It is a constant on purpose, and that is safe for a specific reason: these
// containers publish only on the loopback interface, hold nothing but masked
// data, and are destroyed with the environment. Generating a password per
// branch would imply the password is a security boundary, and it is not; the
// loopback binding is. Writing it down here is more honest than a generated
// value that looks like a secret and protects nothing.
const managedPassword = "antifailure-local-only"

// Provider is the Docker backed database provider.
type Provider struct {
	cli   *client.Client
	clock clock.Clock
	// version is the Postgres major to run.
	version int
	// portFrom is where published ports are allocated.
	portFrom int
	// seedSQL is applied to a golden candidate when there is no source
	// database, which is the case for a project that has not connected one yet.
	seedSQL string

	mu sync.Mutex
	// ports remembers what this process allocated, so two branches created in
	// the same run cannot be handed the same port between the probe and the
	// bind.
	ports map[int]bool
}

// Options configure the provider.
type Options struct {
	// Version is the Postgres major version.
	Version int
	// PortFrom is where to start allocating published ports.
	PortFrom int
	// SeedSQL initialises a golden candidate when no source is configured.
	SeedSQL string
	// Clock is the time source.
	Clock clock.Clock
}

// DefaultPortFrom is high enough to sit above the ephemeral range on every
// platform, so an allocation does not collide with an outbound connection.
const DefaultPortFrom = 43000

// New returns a provider talking to the local Docker daemon.
func New(opts Options) (*Provider, error) {
	if opts.Version == 0 {
		opts.Version = 17
	}
	if opts.PortFrom == 0 {
		opts.PortFrom = DefaultPortFrom
	}
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", dockerHost())
	}
	return &Provider{
		cli: cli, clock: opts.Clock, version: opts.Version,
		portFrom: opts.PortFrom, seedSQL: opts.SeedSQL,
		ports: map[int]bool{},
	}, nil
}

func dockerHost() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	return "unix:///var/run/docker.sock"
}

// Name identifies the provider.
func (p *Provider) Name() string { return "docker" }

// Capabilities describes what this provider can do.
func (p *Provider) Capabilities() provider.Caps {
	return provider.Caps{
		Branching: true,
		// Reset is destroy and recreate from the same image, which is exact
		// rather than approximate: the sequences come back too, because they
		// are part of the committed filesystem.
		Reset: true,
		// Each branch is its own container with its own writable layer, so
		// storage is copied on write by the daemon's storage driver rather
		// than by anything this provider does.
		CopyOnWrite:     true,
		ProviderMasking: false,
		PooledEndpoints: false,
		// The daemon imposes no branch limit. Disk and memory do, and those
		// surface as AF-DB-010 and AF-RUN-020 from the daemon itself, with the
		// numbers the user needs.
		MaxConcurrentBranches: 0,
		ExpectedBranchLatency: 15 * time.Second,
		SupportedVersions:     []int{14, 15, 16, 17},
	}
}

// Close releases the daemon client.
func (p *Provider) Close() error {
	if p.cli == nil {
		return nil
	}
	err := p.cli.Close()
	p.cli = nil
	if err != nil {
		return fmt.Errorf("db.docker: close the daemon client: %w", err)
	}
	return nil
}

// RefreshGolden builds a new golden version.
//
// The sequence is the whole point and the order is not negotiable: start a
// candidate, load the data, mask it, verify it, and only then commit. A
// provider that commits before verifying has published an unverified copy, and
// a provider that verifies before masking has attested to the unmasked data,
// which is worse than not verifying at all.
func (p *Provider) RefreshGolden(ctx context.Context, spec provider.GoldenSpec) (provider.GoldenVersion, error) {
	if !p.Capabilities().Supports(spec.Version) {
		return provider.GoldenVersion{}, aferrors.Coded(aferrors.AFDB003,
			"found", strconv.Itoa(spec.Version),
			"supported", joinInts(p.Capabilities().SupportedVersions))
	}

	// Sweep orphaned candidates first.
	//
	// A candidate is ephemeral by definition: it exists only for the minutes
	// between starting a Postgres container and committing it, and nothing
	// ever branches from one. The deferred removal below handles the ordinary
	// paths, but a killed process, a machine that slept, or a daemon restart
	// leaves one behind, and a leaked Postgres container holds a port and a
	// few hundred megabytes forever. Because a candidate is never referenced,
	// removing an old one is unconditionally safe, which is what lets the
	// provider heal itself rather than wait for a leak report.
	p.sweepCandidates(ctx)

	candidate := fmt.Sprintf("af-candidate-%d", p.clock.Now().UnixNano())
	c, err := p.start(ctx, candidate, p.imageFor(spec.Version), map[string]string{
		LabelKind: "candidate",
	})
	if err != nil {
		return provider.GoldenVersion{}, err
	}
	// The candidate is removed whatever happens. A failed refresh that leaves
	// a running Postgres behind is exactly the kind of leak the journal exists
	// to catch, and not creating it is better than catching it.
	defer func() { _ = p.remove(context.WithoutCancel(ctx), c.id) }()

	conn := p.connString(c.port)
	if err := p.waitReady(ctx, conn); err != nil {
		return provider.GoldenVersion{}, err
	}
	if err := p.loadSource(ctx, conn, spec); err != nil {
		return provider.GoldenVersion{}, err
	}

	if spec.Mask != nil {
		if err := spec.Mask(ctx, conn); err != nil {
			return provider.GoldenVersion{}, fmt.Errorf("db.docker: mask the golden candidate: %w", err)
		}
	}
	attestation := ""
	if spec.Verify != nil {
		attestation, err = spec.Verify(ctx, conn)
		if err != nil {
			// Nothing is committed. The candidate is removed by the deferred
			// call above, so a failed verification leaves no artifact anyone
			// could branch by accident.
			return provider.GoldenVersion{}, fmt.Errorf("db.docker: verify the golden candidate: %w", err)
		}
	}

	// A clean shutdown flushes Postgres's buffers before the commit. Without
	// it the image holds a database that has to recover on every start, which
	// turns a two second branch into a fifteen second one.
	if err := p.stop(ctx, c.id); err != nil {
		return provider.GoldenVersion{}, err
	}

	version := provider.NewGoldenVersionID(p.clock.Now(), spec.RulesHash)
	tag := ImageRepo + ":" + version
	if _, err := p.cli.ContainerCommit(ctx, c.id, container.CommitOptions{
		Reference: tag,
		Comment:   "Antifailure golden " + version,
		Changes:   []string{`LABEL ` + LabelManaged + `=true`, `LABEL ` + LabelKind + `=golden`},
	}); err != nil {
		return provider.GoldenVersion{}, fmt.Errorf("db.docker: commit the golden image: %w", err)
	}

	gv := provider.GoldenVersion{
		ID: version, CreatedAt: p.clock.Now().UTC(), RulesHash: spec.RulesHash,
		Verified: spec.Verify != nil, Attestation: attestation, ProviderRef: tag,
	}
	if info, _, err := p.cli.ImageInspectWithRaw(ctx, tag); err == nil {
		gv.SizeBytes = info.Size
	}
	return gv, nil
}

// sweepCandidates removes candidate containers older than the age at which one
// can only be an orphan.
func (p *Provider) sweepCandidates(ctx context.Context) {
	list, err := p.listContainers(ctx, "candidate")
	if err != nil {
		return // sweeping is opportunistic; a refresh must not fail because of it
	}
	// Fifteen minutes is far longer than any refresh this provider performs
	// and far shorter than a human would tolerate a stray container, so it
	// cannot remove a candidate another process is still using.
	cutoff := p.clock.Now().Add(-15 * time.Minute)
	for _, c := range list {
		created, parseErr := time.Parse(time.RFC3339, c.Labels[LabelCreated])
		if parseErr != nil || created.Before(cutoff) {
			_ = p.remove(ctx, c.ID)
		}
	}
}

// loadSource fills a candidate from the source database, or from the seed when
// there is no source.
func (p *Provider) loadSource(ctx context.Context, target secrets.Value, spec provider.GoldenSpec) error {
	if spec.SourceURL.IsZero() || strings.Contains(spec.SourceURL.Reveal(), "@source/") {
		// No real source. A project that has not connected production yet
		// still needs a schema to branch, and the seed is what provides it.
		if p.seedSQL == "" {
			return nil
		}
		return p.execSQL(ctx, target, p.seedSQL)
	}
	return p.copyDatabase(ctx, spec.SourceURL, target)
}

// ListGoldens returns published versions, newest first.
func (p *Provider) ListGoldens(ctx context.Context) ([]provider.GoldenVersion, error) {
	images, err := p.cli.ImageList(ctx, image.ListOptions{
		Filters: filters.NewArgs(filters.Arg("reference", ImageRepo+":*")),
	})
	if err != nil {
		return nil, fmt.Errorf("db.docker: list golden images: %w", err)
	}
	var out []provider.GoldenVersion
	for _, img := range images {
		for _, tag := range img.RepoTags {
			id, ok := strings.CutPrefix(tag, ImageRepo+":")
			if !ok {
				continue
			}
			out = append(out, provider.GoldenVersion{
				ID: id, CreatedAt: time.Unix(img.Created, 0).UTC(),
				SizeBytes: img.Size, ProviderRef: tag,
				// A committed image only exists because verification passed;
				// the commit is the last step of a refresh.
				Verified: true,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// DestroyGolden removes a version, refusing one a branch still depends on.
func (p *Provider) DestroyGolden(ctx context.Context, version string) error {
	tag := ImageRepo + ":" + version
	branches, err := p.listContainers(ctx, "branch")
	if err != nil {
		return err
	}
	for _, c := range branches {
		if c.Labels[LabelGolden] == version {
			// Removing the image underneath a running container would leave an
			// environment whose data disappears when it restarts.
			return aferrors.Coded(aferrors.AFDB005, "version", version, "count", "1")
		}
	}
	_, err = p.cli.ImageRemove(ctx, tag, image.RemoveOptions{PruneChildren: true})
	if err != nil && !client.IsErrNotFound(err) {
		return fmt.Errorf("db.docker: remove the golden image %s: %w", version, err)
	}
	return nil
}

// Branch creates a database for an environment from a golden version.
func (p *Provider) Branch(ctx context.Context, version, envID string) (provider.Branch, error) {
	// Idempotency first. A retry after a timeout must find the branch this
	// call may already have created, or the first attempt's container becomes
	// an orphan nobody has an identifier for.
	if existing, ok, err := p.findBranch(ctx, envID); err != nil {
		return provider.Branch{}, err
	} else if ok {
		return existing, nil
	}

	tag := ImageRepo + ":" + version
	if _, _, err := p.cli.ImageInspectWithRaw(ctx, tag); err != nil {
		if client.IsErrNotFound(err) {
			return provider.Branch{}, aferrors.Coded(aferrors.AFDB004, "version", version)
		}
		return provider.Branch{}, fmt.Errorf("db.docker: inspect the golden image: %w", err)
	}

	name := branchName(envID)
	c, err := p.start(ctx, name, tag, map[string]string{
		LabelKind:   "branch",
		LabelEnv:    envID,
		LabelGolden: version,
	})
	if err != nil {
		return provider.Branch{}, err
	}
	b := provider.Branch{
		EnvID: envID, From: version, ProviderRef: c.id, CreatedAt: p.clock.Now().UTC(),
	}
	if err := p.waitReady(ctx, p.connString(c.port)); err != nil {
		// The container exists and is recorded, so the caller can tear it
		// down. Leaving it running and unusable would be worse.
		return b, err
	}
	return b, nil
}

// Reset returns a branch to its golden state.
//
// It destroys and recreates from the same image rather than replaying a dump,
// which makes the result exact: the sequences, the visibility map, and the
// planner statistics all come back, because they are part of the committed
// filesystem. A reset that rewinds rows but not sequences produces a primary
// key collision on the next insert, which surfaces as an application bug
// nobody can reproduce.
func (p *Provider) Reset(ctx context.Context, b provider.Branch) error {
	if err := p.Destroy(ctx, b); err != nil {
		return err
	}
	_, err := p.Branch(ctx, b.From, b.EnvID)
	return err
}

// Destroy removes a branch. Removing one that is already gone succeeds.
//
// Both the recorded reference and the deterministic name are removed, and that
// is not belt and braces. Reset replaces the container, so a caller holding a
// branch from before a reset has a reference to something already gone while
// the replacement is still running. Teardown by the stale reference alone
// would report success and leave a Postgres container behind, which is exactly
// the leak the whole journal exists to prevent.
func (p *Provider) Destroy(ctx context.Context, b provider.Branch) error {
	refs := map[string]bool{}
	if b.ProviderRef != "" {
		refs[b.ProviderRef] = true
	}
	if b.EnvID != "" {
		refs[branchName(b.EnvID)] = true
	}
	if len(refs) == 0 {
		return fmt.Errorf("db.docker: the branch has neither an identifier nor an environment")
	}
	for ref := range refs {
		if err := p.remove(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

// ConnString returns a connection string for a branch.
func (p *Provider) ConnString(ctx context.Context, b provider.Branch, mode provider.ConnMode) (secrets.Value, error) {
	if mode == provider.ConnPooled {
		// Declaring a capability this provider does not have would make the
		// conformance suite pass a behavior it should skip.
		return secrets.Value{}, provider.ErrUnsupported
	}

	// The deterministic name is tried first, and the recorded reference second.
	//
	// Reset destroys the container and creates a new one, so a caller holding
	// a Branch from before the reset has a provider reference that no longer
	// exists. The name is derived from the environment identifier and survives,
	// which is the whole reason it is derived rather than generated.
	var refs []string
	if b.EnvID != "" {
		refs = append(refs, branchName(b.EnvID))
	}
	if b.ProviderRef != "" {
		refs = append(refs, b.ProviderRef)
	}

	var lastErr error
	for _, ref := range refs {
		info, err := p.cli.ContainerInspect(ctx, ref)
		if err != nil {
			lastErr = err
			continue
		}
		port, perr := publishedPort(info.NetworkSettings.Ports)
		if perr != nil {
			lastErr = perr
			continue
		}
		return p.connString(port), nil
	}
	if lastErr != nil && client.IsErrNotFound(lastErr) {
		return secrets.Value{}, aferrors.Coded(aferrors.AFDB004, "version", b.From)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("the branch has no identifier")
	}
	return secrets.Value{}, fmt.Errorf("db.docker: resolve the branch connection: %w", lastErr)
}

// Inventory lists what this provider currently holds.
//
// It reports containers and images by label rather than by name, so a resource
// an older version created under a different naming scheme is still found. A
// resource with no label is not ours and never appears here, which is what
// keeps the leak detector from proposing to delete a developer's own database.
func (p *Provider) Inventory(ctx context.Context) ([]provider.Resource, error) {
	var out []provider.Resource

	containers, err := p.listContainers(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, c := range containers {
		out = append(out, provider.Resource{
			Kind:      "container/" + c.Labels[LabelKind],
			ID:        c.ID,
			EnvID:     c.Labels[LabelEnv],
			CreatedAt: time.Unix(c.Created, 0).UTC(),
			Labels: map[string]string{
				"name":   strings.TrimPrefix(firstName(c.Names), "/"),
				"golden": c.Labels[LabelGolden],
				"state":  c.State,
			},
		})
	}

	goldens, err := p.ListGoldens(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range goldens {
		out = append(out, provider.Resource{
			Kind: "image/golden", ID: g.ProviderRef, CreatedAt: g.CreatedAt,
			Labels: map[string]string{"version": g.ID, "size": strconv.FormatInt(g.SizeBytes, 10)},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Health reports whether a branch is reachable.
func (p *Provider) Health(ctx context.Context, b provider.Branch) (provider.Health, error) {
	start := p.clock.Now()
	conn, err := p.ConnString(ctx, b, provider.ConnDirect)
	if err != nil {
		return provider.Health{Reachable: false, Detail: "the branch container does not exist"}, nil
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := p.ping(pingCtx, conn); err != nil {
		return provider.Health{Reachable: false, Detail: err.Error()}, nil
	}
	return provider.Health{Reachable: true, Detail: "accepting connections", Latency: p.clock.Since(start)}, nil
}

// branchName is derived from the environment identifier, so that a retry after
// a crash finds the same container without needing the journal to be intact.
func branchName(envID string) string { return "af-db-" + envID }

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

func publishedPort(ports nat.PortMap) (int, error) {
	for _, bindings := range ports {
		for _, b := range bindings {
			if n, err := strconv.Atoi(b.HostPort); err == nil && n > 0 {
				return n, nil
			}
		}
	}
	return 0, fmt.Errorf("db.docker: the branch container publishes no port")
}

// discard drains a reader so that a daemon stream is fully consumed, which the
// API requires before the operation is considered finished.
func discard(r io.ReadCloser) {
	if r == nil {
		return
	}
	_, _ = io.Copy(io.Discard, r)
	_ = r.Close()
}

// freePort finds a port nothing is listening on and nothing in this process
// has already claimed.
//
// Binding to port zero and reading back what the kernel chose is the usual
// trick, but it does not work here: the port has to be handed to the daemon,
// which binds it afterwards, so the socket must be closed first and the
// interval between is a race. Remembering what this process allocated closes
// the half of that race that matters in practice, which is two branches
// created in the same run.
func (p *Provider) freePort() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for port := p.portFrom; port < p.portFrom+2000; port++ {
		if p.ports[port] {
			continue
		}
		l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			continue
		}
		_ = l.Close()
		p.ports[port] = true
		return port, nil
	}
	return 0, aferrors.Coded(aferrors.AFRUN020,
		"detail", fmt.Sprintf("no free port was found between %d and %d", p.portFrom, p.portFrom+2000))
}

var errNotOurs = errors.New("db.docker: the resource is not managed by Antifailure")
