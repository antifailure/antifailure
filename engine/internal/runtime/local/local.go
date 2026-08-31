// Package local runs an environment as a set of Docker containers.
//
// The containment story here is worth stating plainly, because it is the half
// of the product people are trusting. Every environment gets its own network,
// and until the policy sidecar exists that network has no route out at all:
// IP masquerading is off, so a packet leaving a container carries an address
// nothing upstream will route a reply to. That is blunt. It is not the
// per host policy the manifest describes, and it is not presented as one. What
// it is, is real: a service in this environment cannot email a customer,
// cannot charge a card, and cannot write to a production analytics stream,
// which are the three things that make people afraid of preview environments.
//
// Everything is labelled before it is created. The journal callback runs
// first, so an interrupt between the record and the create leaves a record
// pointing at nothing, which teardown handles, rather than a container nothing
// remembers, which it cannot.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/envcert"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// DatabaseAlias is the hostname the database answers to inside an environment.
//
// A fixed name rather than a generated one, because it appears in the
// connection string every service receives and in every log line that mentions
// the database, and a name that changes per environment makes two runs
// impossible to compare.
const DatabaseAlias = "db"

// Runtime places containers on the local Docker daemon.
type Runtime struct {
	cli      *client.Client
	clock    clock.Clock
	ports    *dockerutil.PortAllocator
	redactor *redact.Redactor
	// readyTimeout bounds how long a service may take to answer.
	readyTimeout time.Duration
	// ttl is how long an environment this runtime creates may live.
	ttl time.Duration
}

// Options configure the runtime.
type Options struct {
	Clock    clock.Clock
	Redactor *redact.Redactor
	// PortFrom is where published ports are allocated.
	PortFrom int
	// ReadyTimeout bounds a readiness wait. Zero uses the default.
	ReadyTimeout time.Duration
	// TTL is runtime.ttl from the manifest: how long an environment created
	// by this runtime may live before the reaper removes it.
	//
	// Held on the runtime rather than passed on EnvSpec because the network is
	// created before the spec is assembled, and a network with no expiry
	// outlives every container that was on it. Zero stamps no expiry, which is
	// what a caller inventorying a machine gets: nothing it creates has a
	// lifetime, because it creates nothing.
	TTL time.Duration
}

// DefaultReadyTimeout is long enough for a framework that compiles on first
// request and short enough that a service which will never answer is reported
// rather than waited on.
const DefaultReadyTimeout = 3 * time.Minute

// New returns a runtime talking to the local Docker daemon.
func New(opts Options) (*Runtime, error) {
	cli, err := dockerutil.Client()
	if err != nil {
		return nil, err
	}
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	if opts.Redactor == nil {
		opts.Redactor = redact.New()
	}
	if opts.ReadyTimeout <= 0 {
		opts.ReadyTimeout = DefaultReadyTimeout
	}
	if opts.PortFrom <= 0 {
		// Above the database provider's range, so the two cannot argue over a
		// port and produce a failure that looks like a race because it is one.
		opts.PortFrom = dockerutil.DefaultPortFrom + 3000
	}
	return &Runtime{
		cli: cli, clock: opts.Clock, redactor: opts.Redactor,
		ports:        dockerutil.NewPortAllocator(opts.PortFrom),
		readyTimeout: opts.ReadyTimeout,
		ttl:          opts.TTL,
	}, nil
}

// managed is the label set for one resource this runtime is about to create.
//
// Every create site goes through it, so that the expiry cannot be stamped on
// three kinds of resource and forgotten on the fourth. A network without one
// is the case that matters: it is the last thing left when the containers are
// gone, and nothing would ever collect it.
//
// An empty environment id means the resource belongs to the machine rather
// than to any environment, which is the sidecar and forwarder images. Those
// are shared by every environment on the daemon and are not something one
// environment's lifetime may destroy.
func (r *Runtime) managed(kind, envID string) map[string]string {
	now := r.clock.Now()
	if envID == "" || r.ttl <= 0 {
		return dockerutil.Managed(kind, envID, now)
	}
	return dockerutil.ManagedUntil(kind, envID, now, now.Add(r.ttl))
}

// Name identifies the runtime.
func (r *Runtime) Name() string { return "local" }

// Capabilities declares what this runtime can do.
//
// All three are true, which is what makes it the reference: it is the runtime
// the other one is compared against, so a behavior it skipped would be a
// behavior nothing had ever run.
func (r *Runtime) Capabilities() provider.RuntimeCaps {
	return provider.RuntimeCaps{
		// Every web service gets a forwarder publishing a port on the host,
		// so the URL Status reports is reachable from the machine that ran af.
		Ingress: true,
		Logs:    true,
		// The database provider's branches are containers on this same
		// daemon, so they can be joined to the environment's network.
		AttachesLocalDatabase: true,
	}
}

// Close releases the daemon connection.
func (r *Runtime) Close() error { return r.cli.Close() }

// Attachable is implemented by a database provider whose branches are local
// containers, and which therefore have to join the environment's network to be
// reachable from a service.
//
// A cloud provider does not implement it: its connection string already works
// from anywhere, and there is nothing to attach.
type Attachable interface {
	// AttachToNetwork connects a branch's container to a network under an
	// alias, and reports the port it listens on inside that network.
	AttachToNetwork(ctx context.Context, ref, networkID, alias string) (int, error)
}

// Up brings an environment up.
func (r *Runtime) Up(ctx context.Context, spec provider.EnvSpec) (provider.Env, error) {
	if spec.EnvID == "" {
		return provider.Env{}, aferrors.Coded(aferrors.AFRUN040, "detail", "the environment has no id")
	}
	journal := spec.Journal
	if journal == nil {
		journal = func(string, string) error { return nil }
	}
	progress := spec.Progress
	if progress == nil {
		progress = func(string) {}
	}

	nets, err := r.ensureNetworks(ctx, spec.EnvID, journal)
	if err != nil {
		return provider.Env{}, err
	}
	env := provider.Env{
		EnvID: spec.EnvID, NetworkID: nets.inner, CreatedAt: r.clock.Now().UTC(),
	}

	order, err := startOrder(spec.Services)
	if err != nil {
		return env, err
	}
	names := make([]string, 0, len(order))
	for _, s := range order {
		names = append(names, s.Name)
	}
	// The sidecar starts before any service, for two reasons. A service that
	// makes an outbound call the instant it starts would otherwise get a
	// connection refused that looks exactly like a blocked host and is not
	// one. And every service is pointed at the sidecar for DNS, so its
	// address has to exist before any of them are created.
	var ca *envcert.Authority
	if spec.CACertPEM != "" {
		ca = &envcert.Authority{CertPEM: spec.CACertPEM, KeyPEM: spec.CAKeyPEM}
	}
	proxyIP, err := r.startProxy(ctx, spec.EnvID, spec.Egress, names, ca,
		spec.SandboxCredentials, spec.MockPacks, spec.ModelEnv, nets, journal, progress)
	if err != nil {
		return env, err
	}
	env.ProxyReady = true
	if needsIngress(order) {
		if err := r.ensureIngressImage(ctx); err != nil {
			return env, err
		}
	}
	// Reserved before anything starts, because a service has to be told its
	// own public address before it runs, and the ingress that publishes it is
	// created after the service it forwards to.
	if spec.PublicPorts == nil {
		spec.PublicPorts = map[string]int{}
	}
	for _, s := range order {
		if s.Kind != "web" || s.Port <= 0 {
			continue
		}
		if _, taken := spec.PublicPorts[s.Name]; taken {
			continue
		}
		// An ingress left by an earlier run already holds a port, and reusing
		// it is what makes a second Up address the same environment rather
		// than a new one.
		if existing, err := r.cli.ContainerInspect(ctx, ingressName(spec.EnvID, s.Name)); err == nil {
			if p, ok := publishedPort(existing.NetworkSettings.Ports); ok {
				spec.PublicPorts[s.Name] = p
				continue
			}
		}
		p, err := r.ports.Free()
		if err != nil {
			return env, err
		}
		spec.PublicPorts[s.Name] = p
	}
	for _, s := range order {
		running, err := r.startService(ctx, spec, s, nets, proxyIP, journal, progress)
		env.Services = append(env.Services, running)
		if err != nil {
			// Returned with what came up so far, not with nothing. The
			// containers that started are the evidence, and teardown finds
			// them by label whether or not this function reports them.
			return env, err
		}
	}
	// Back into the order the manifest declares, not the order they started
	// in.
	//
	// `order` is a dependency sort, so a web application that depends on an
	// API starts second and would be listed second. Env.URL returns the first
	// web service, which then meant `af up` printed the API's address and the
	// agents drove the API with a browser: six workflows blocked on a login
	// form that was never going to be there, and the message was a Playwright
	// timeout about a label.
	//
	// Start order is an implementation detail of bringing things up. The
	// manifest is where the author says which service the product is, and
	// every reader of this list wants that order: the status table, the
	// pull request comment, and the address a person opens.
	sortByManifestOrder(env.Services, spec.Services)
	return env, nil
}

// sortByManifestOrder puts running services back into the order they were
// declared in.
func sortByManifestOrder(running []provider.RunningService, declared []provider.ServiceSpec) {
	position := make(map[string]int, len(declared))
	for i, s := range declared {
		position[s.Name] = i
	}
	sort.SliceStable(running, func(i, j int) bool {
		pi, oki := position[running[i].Name]
		pj, okj := position[running[j].Name]
		switch {
		case oki && okj:
			return pi < pj
		case oki:
			return true
		case okj:
			return false
		}
		// Neither is declared, which cannot happen from Up and can from a
		// Status of an environment whose manifest has changed. Leaving them
		// in the order they were found is better than inventing one.
		return false
	})
}

// needsIngress reports whether any service has to be reachable from the host.
func needsIngress(services []provider.ServiceSpec) bool {
	for _, s := range services {
		if s.Kind == "web" && s.Port > 0 {
			return true
		}
	}
	return false
}

// AttachDatabase connects a branch container to the environment network and
// returns the connection string a service should use.
//
// The provider's own connection string points at the host's loopback address,
// which is not where the database is from inside a container. Rewriting the
// host and port rather than asking the provider for a second form keeps the
// provider interface honest: it has no idea this environment exists.
func (r *Runtime) AttachDatabase(ctx context.Context, db Attachable, ref, networkID string, hostURL secrets.Value) (secrets.Value, error) {
	port, err := db.AttachToNetwork(ctx, ref, networkID, DatabaseAlias)
	if err != nil {
		return secrets.Value{}, err
	}
	rewritten, err := rewriteHost(hostURL.Reveal(), DatabaseAlias, port)
	if err != nil {
		return secrets.Value{}, err
	}
	return secrets.New(rewritten), nil
}

// rewriteHost replaces the host and port of a connection URL.
func rewriteHost(raw, host string, port int) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", "the database connection string is not a URL")
	}
	u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	return u.String(), nil
}

// startOrder sorts services so that a dependency starts before what depends on
// it, and reports a cycle rather than deadlocking on one.
func startOrder(services []provider.ServiceSpec) ([]provider.ServiceSpec, error) {
	byName := make(map[string]provider.ServiceSpec, len(services))
	for _, s := range services {
		byName[s.Name] = s
	}
	var out []provider.ServiceSpec
	state := make(map[string]int, len(services)) // 0 unseen, 1 visiting, 2 done

	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch state[name] {
		case 2:
			return nil
		case 1:
			return aferrors.Coded(aferrors.AFRUN041,
				"cycle", strings.Join(append(path, name), " -> "))
		}
		s, ok := byName[name]
		if !ok {
			return aferrors.Coded(aferrors.AFRUN042,
				"service", strings.Join(path, " -> "), "missing", name)
		}
		state[name] = 1
		deps := append([]string(nil), s.DependsOn...)
		// Sorted, so the order does not depend on how the manifest happened to
		// list them and two runs place containers the same way.
		sort.Strings(deps)
		for _, d := range deps {
			if err := visit(d, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = 2
		out = append(out, s)
		return nil
	}

	// Iterated in declaration order rather than over a map, so the result is
	// the same on every run.
	for _, s := range services {
		if err := visit(s.Name, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Down removes everything belonging to an environment.
func (r *Runtime) Down(ctx context.Context, envID string) (provider.Teardown, error) {
	var td provider.Teardown
	if envID == "" {
		return td, aferrors.Coded(aferrors.AFRUN040, "detail", "the environment has no id")
	}

	containers, err := r.cli.ContainerList(ctx, container.ListOptions{
		All: true, Filters: dockerutil.EnvFilter(envID),
	})
	if err != nil {
		return td, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", dockerutil.Host())
	}
	for _, c := range containers {
		switch c.Labels[dockerutil.LabelKind] {
		case dockerutil.KindService, dockerutil.KindSidecar:
		default:
			// The database branch carries this environment's label because it
			// belongs to this environment, but the database provider owns it.
			// Removing it here would destroy a database behind the back of the
			// component responsible for it.
			continue
		}
		// Never stops at the first failure. A container that will not die must
		// not strand the network behind it.
		if err := dockerutil.RemoveContainer(ctx, r.cli, c.ID); err != nil {
			td.Pending = append(td.Pending, provider.PendingResource{
				Kind: "container", ID: dockerutil.ShortID(c.ID), Reason: err.Error(),
			})
			continue
		}
		td.Removed++
	}

	nets, err := r.cli.NetworkList(ctx, network.ListOptions{Filters: dockerutil.EnvFilter(envID)})
	if err != nil {
		return td, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", dockerutil.Host())
	}
	for _, n := range nets {
		// Anything still attached that this teardown did not remove, such as
		// the database branch, has to be detached or Docker refuses to remove
		// the network and it is reported pending forever.
		r.disconnectForeign(ctx, n.ID)
		if err := r.cli.NetworkRemove(ctx, n.ID); err != nil {
			if cerrdefs.IsNotFound(err) {
				continue
			}
			td.Pending = append(td.Pending, provider.PendingResource{
				Kind: "network", ID: n.Name, Reason: err.Error(),
			})
			continue
		}
		td.Removed++
	}
	return td, nil
}

// Status reports what is running for an environment.
func (r *Runtime) Status(ctx context.Context, envID string) (provider.Env, error) {
	env := provider.Env{EnvID: envID}
	containers, err := r.cli.ContainerList(ctx, container.ListOptions{
		All: true, Filters: dockerutil.EnvFilter(envID),
	})
	if err != nil {
		return env, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", dockerutil.Host())
	}
	// The service does not publish its own port; its forwarder does. Reading
	// the port off the service container would report every web service as
	// having no address, which is the one thing status exists to tell you.
	published := map[string]int{}
	for _, c := range containers {
		if c.Labels[dockerutil.LabelKind] != dockerutil.KindSidecar {
			continue
		}
		for _, p := range c.Ports {
			if p.PublicPort != 0 {
				published[c.Labels[dockerutil.LabelService]] = int(p.PublicPort)
				break
			}
		}
	}
	for _, c := range containers {
		if c.Labels[dockerutil.LabelKind] != dockerutil.KindService {
			continue
		}
		rs := provider.RunningService{
			Name:        c.Labels[dockerutil.LabelService],
			ContainerID: c.ID,
			Kind:        c.Labels[dockerutil.LabelServiceKind],
			State:       c.State,
			Ready:       c.State == "running",
		}
		if c.Status != "" {
			rs.Detail = c.Status
		}
		// The list reports the exit code only inside a human sentence, so the
		// number comes from an inspect. Anything that wanted it before parsed
		// "Exited (9) 3 seconds ago" with a regular expression, which is a
		// format Docker has never promised to keep.
		if c.State != "running" {
			if insp, err := r.cli.ContainerInspect(ctx, c.ID); err == nil &&
				insp.State != nil && !insp.State.Running && insp.State.FinishedAt != "" {
				code := insp.State.ExitCode
				rs.ExitCode = &code
			}
		}
		if port, ok := published[rs.Name]; ok {
			rs.URL = fmt.Sprintf("http://127.0.0.1:%d", port)
		}
		env.Services = append(env.Services, rs)
	}
	sort.Slice(env.Services, func(i, j int) bool { return env.Services[i].Name < env.Services[j].Name })

	nets, err := r.cli.NetworkList(ctx, network.ListOptions{Filters: dockerutil.EnvFilter(envID)})
	if err == nil && len(nets) > 0 {
		env.NetworkID = nets[0].ID
	}
	return env, nil
}

// Inventory lists everything this runtime holds.
func (r *Runtime) Inventory(ctx context.Context) ([]provider.Resource, error) {
	var out []provider.Resource

	containers, err := r.cli.ContainerList(ctx, container.ListOptions{
		All: true, Filters: dockerutil.Filter(),
	})
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", dockerutil.Host())
	}
	for _, c := range containers {
		kind := c.Labels[dockerutil.LabelKind]
		if kind != dockerutil.KindService && kind != dockerutil.KindSidecar {
			continue
		}
		out = append(out, provider.Resource{
			Kind: "container/" + kind, ID: c.ID, EnvID: c.Labels[dockerutil.LabelEnv],
			CreatedAt: time.Unix(c.Created, 0).UTC(),
			Labels: map[string]string{
				"name":    dockerutil.FirstName(c.Names),
				"service": c.Labels[dockerutil.LabelService],
				"state":   c.State,
				// Carried through so the reaper reads one shape of resource
				// rather than talking to each runtime's own client. Empty when
				// the resource states no lifetime, which the reaper reads as
				// "leave it alone".
				"expires": c.Labels[dockerutil.LabelExpires],
			},
		})
	}

	nets, err := r.cli.NetworkList(ctx, network.ListOptions{Filters: dockerutil.Filter()})
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", dockerutil.Host())
	}
	for _, n := range nets {
		out = append(out, provider.Resource{
			Kind: "network", ID: n.ID, EnvID: n.Labels[dockerutil.LabelEnv],
			CreatedAt: n.Created.UTC(),
			Labels: map[string]string{
				"name":    n.Name,
				"expires": n.Labels[dockerutil.LabelExpires],
			},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// LogLine is one line of a service's output.
//
// An alias rather than its own type, so that this runtime satisfies
// provider.LogReader without every caller of af logs having to be rewritten
// to name the shared type.
type LogLine = provider.LogLine

// Logs returns recent output from an environment's services.
//
// Redacted on the way out rather than at the call site. A service's own log is
// the second likeliest place for a secret to surface after a build log, and
// redacting at the writer means a missed call site cannot leak.
func (r *Runtime) Logs(ctx context.Context, envID, service string, tail int) ([]LogLine, error) {
	if tail <= 0 {
		tail = 200
	}
	containers, err := r.cli.ContainerList(ctx, container.ListOptions{
		All: true, Filters: dockerutil.EnvFilter(envID),
	})
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", dockerutil.Host())
	}

	var out []LogLine
	for _, c := range containers {
		if c.Labels[dockerutil.LabelKind] != dockerutil.KindService {
			continue
		}
		name := c.Labels[dockerutil.LabelService]
		if service != "" && name != service {
			continue
		}
		rc, logErr := r.cli.ContainerLogs(ctx, c.ID, container.LogsOptions{
			ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(tail),
		})
		if logErr != nil {
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(rc, 8<<20))
		_ = rc.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			continue
		}
		for _, line := range strings.Split(stripDockerLogFraming(string(body)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			out = append(out, LogLine{Service: name, Text: r.redactor.String(line)})
		}
	}
	return out, nil
}

// The kinds this runtime journals.
//
// These are the stable strings from journal.Kind. A runtime is handed a
// journal callback taking a plain string and does not import the journal,
// which keeps the provider interface free of the engine's storage; the cost
// is that nothing in the compiler notices a runtime that invents a kind, and
// kinds are not validated anywhere either, so any string is stored happily.
//
// These two were right by luck rather than by anything checked, which stopped
// being good enough when the cluster runtime got them wrong. Naming them and
// asserting them in this package's tests is what makes the next one a
// compile-and-test problem rather than a row nothing can look up.
const (
	kindContainer = "container"
	kindNetwork   = "network"
)
