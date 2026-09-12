package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/envcert"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// containerName is deterministic, so a second run finds the container the
// first one made rather than starting a duplicate that fights it for the port.
//
// The first instance keeps the name it has always had and only the second and
// later carry an ordinal. That asymmetry is deliberate. Reuse is keyed on the
// name: an environment created before instance counts were honoured holds a
// container called af-svc-<env>-<service>, and renaming the first instance
// would leave that container running, unrecognised by the next Up, holding the
// same network alias as the container that replaced it. Two processes behind
// one name is the exact failure this whole lane exists to make visible, and
// shipping it as an upgrade artefact would be absurd.
func containerName(envID, service string, ordinal int) string {
	name := "af-svc-" + envID + "-" + service
	if ordinal <= 1 {
		return name
	}
	return name + "-" + strconv.Itoa(ordinal)
}

// startService creates, starts, and waits for every instance of one service.
//
// One migration, one ingress, N containers. Each of those counts is a decision
// rather than an accident of the loop:
//
// The migration runs ONCE. It is the schema change the service needs, not
// something each copy of the service does for itself, and three containers
// racing the same migration is a failure people have shipped to production
// often enough that it is one of the bugs this field exists to reproduce, not
// one to reproduce inside the runtime.
//
// The ingress is ONE forwarder for the service, not one per instance. It
// forwards to the service's name, which every instance answers to, and socat
// resolves that name per connection, so requests spread across the instances
// the way a load balancer would. That is what makes a sticky session
// assumption visible: the second request lands somewhere else.
//
// Readiness waits for EVERY instance. Reporting a service ready when one of
// three has answered is the same lie as reporting three when one is running,
// and a dependant that starts on it races the two that are still coming up.
//
// The limit of that, stated rather than left to be discovered: what is checked
// per instance is that the container is still running, and what is checked
// once is that the service answers on its published port. The only address the
// host can reach is the one forwarder, and a request to it lands on whichever
// instance the resolver picked, so an instance that is running and not
// listening is not distinguished here. The cluster runtime has no such
// limit, because a readiness probe is evaluated per pod. Closing it here needs
// a probe that runs inside the environment and addresses one container, which
// is a bigger change than this and belongs with whoever wants it.
func (r *Runtime) startService(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	nets networks,
	proxyIP string,
	journal func(string, string) error,
	progress func(string),
) (provider.RunningService, error) {
	want := s.Instances()
	running := provider.RunningService{Name: s.Name, Kind: s.Kind}

	if s.Migrate != "" {
		progress(fmt.Sprintf("%s: running migrations", s.Name))
		// The migration gets its own connection string when the provider
		// offers a pooled one, because a migration must not go through a
		// transaction pooler. Everything else about the container is the same,
		// so this is the spec with one field swapped rather than a second path.
		migrateSpec := spec
		if !spec.MigrationDatabaseURL.IsZero() {
			migrateSpec.DatabaseURL = spec.MigrationDatabaseURL
		}
		if err := r.runOnce(ctx, migrateSpec, s, nets, proxyIP, s.Migrate, journal); err != nil {
			running.State = "migration failed"
			running.Detail = err.Error()
			return running, err
		}
	}

	ids := make([]string, 0, want)
	for ordinal := 1; ordinal <= want; ordinal++ {
		id, err := r.startInstance(ctx, spec, s, nets, proxyIP, ordinal, want, journal, progress)
		if id != "" {
			// Kept even when this instance failed. startInstance returns the
			// container it created alongside the error that stopped it from
			// starting, and that container holds the only explanation of why:
			// dropping the id would leave the failure findable only by label
			// and its logs unread.
			ids = append(ids, id)
		}
		if err != nil {
			// Reported with the instances that did come up rather than with
			// none. Teardown finds them by label either way, and the ones
			// that started are the evidence for why the one that did not
			// failed.
			running.Instances = len(ids)
			if len(ids) > 0 {
				running.ContainerID = ids[0]
			}
			return running, err
		}
	}
	// The first instance, because RunningService names one container and this
	// is the one whose name has no ordinal on it. Logs read every instance by
	// label rather than through this field.
	running.ContainerID = ids[0]
	running.Instances = want
	running.State = "running"
	running.CPUMillis, running.MemoryBytes = r.appliedResources(ctx, ids[0])

	// The service itself is on a network with no route out, which is also a
	// network the host cannot publish a port from. A forwarder on both sides
	// is what makes it reachable without giving the service a way out.
	hostPort := 0
	if s.Kind == "web" && s.Port > 0 {
		p, ingressErr := r.startIngress(ctx, spec, s, nets, journal)
		if ingressErr != nil {
			return running, ingressErr
		}
		hostPort = p
		running.URL = fmt.Sprintf("http://127.0.0.1:%d", hostPort)
	}

	// A cron service is invoked on a schedule rather than run continuously, so
	// waiting for it to answer would wait forever.
	if s.Kind == "cron" {
		running.Ready = true
		progress(fmt.Sprintf("%s: ready (invoked on a schedule)", s.Name))
		return running, nil
	}

	timeout := s.HealthTimeout
	if timeout <= 0 {
		timeout = r.readyTimeout
	}
	for _, id := range ids {
		if err := r.waitReady(ctx, s, id, hostPort, timeout, progress); err != nil {
			running.Detail = r.lastLogLines(ctx, id)
			return running, err
		}
	}
	running.Ready = true
	switch {
	case running.URL != "" && want > 1:
		progress(fmt.Sprintf("%s: ready at %s, %d instances", s.Name, running.URL, want))
	case running.URL != "":
		progress(fmt.Sprintf("%s: ready at %s", s.Name, running.URL))
	case want > 1:
		progress(fmt.Sprintf("%s: ready, %d instances", s.Name, want))
	default:
		progress(fmt.Sprintf("%s: ready", s.Name))
	}
	return running, nil
}

// startInstance creates and starts one instance of a service and returns its
// container id.
func (r *Runtime) startInstance(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	nets networks,
	proxyIP string,
	ordinal, want int,
	journal func(string, string) error,
	progress func(string),
) (string, error) {
	name := containerName(spec.EnvID, s.Name, ordinal)
	if err := journal(kindContainer, name); err != nil {
		return "", err
	}
	// A container left by an interrupted run holds the name. Reusing a running
	// one keeps Up idempotent; replacing a stopped one is what makes a second
	// Up after a crash work rather than fail on a name conflict.
	//
	// Reused only when it runs the image this Up would start. The image
	// reference carries a digest of the build context, so a tree edited since
	// the container started names a different image, and a container from
	// the old one kept answering every probe with the old code while `af up`
	// printed ready. A fix was rehearsed that way against the build it was
	// fixing, and the rehearsal passed. The comparison is on image IDs rather
	// than tags, because --rebuild produces a new image under the same tag.
	//
	// The reused container also used to return here, ready, with no URL and
	// without the ingress being looked up, so a repeat Up printed a service
	// with no address. It now takes the same path as a fresh one from the
	// ingress onwards.
	var id string
	fingerprint := r.serviceFingerprint(spec, s, proxyIP, "")
	if existing, err := r.cli.ContainerInspect(ctx, name); err == nil {
		switch {
		case existing.State != nil && existing.State.Running && r.runsImage(ctx, existing, s.Image) &&
			existing.Config != nil && existing.Config.Labels[configurationLabel] == fingerprint:
			id = existing.ID
		case existing.State != nil && existing.State.Running:
			progress(fmt.Sprintf("%s: replacing the running container because its image or configuration changed", instanceLabel(s.Name, ordinal, want)))
			fallthrough
		default:
			if rmErr := dockerutil.RemoveContainer(ctx, r.cli, existing.ID); rmErr != nil {
				return "", rmErr
			}
		}
	}
	if id != "" {
		return id, nil
	}

	created, err := r.create(ctx, spec, s, nets, proxyIP, name, "", []string{s.Name})
	if err != nil {
		return "", err
	}
	if err := r.installCA(ctx, created, spec); err != nil {
		return created, err
	}
	if err := r.cli.ContainerStart(ctx, created, container.StartOptions{}); err != nil {
		return created, aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("starting %s: %v", instanceLabel(s.Name, ordinal, want), err))
	}
	return created, nil
}

// instanceLabel names a service in a message, and names the instance too when
// there is more than one of it.
//
// A message reading "worker: failed to start" is a whole service down; the
// same message when two of the three came up is a different situation, and a
// reader who cannot tell them apart looks in the wrong place first.
func instanceLabel(service string, ordinal, want int) string {
	if want <= 1 {
		return service
	}
	return fmt.Sprintf("%s instance %d of %d", service, ordinal, want)
}

// runsImage reports whether a container runs the image ref names now.
//
// By image ID, resolved through the daemon, so that a rebuilt image under an
// unchanged tag reads as a change. When the reference cannot be resolved the
// answer is false: the container is then replaced and the create that follows
// says why the image is missing, which is a better message than a stale
// container reported ready.
func (r *Runtime) runsImage(ctx context.Context, existing container.InspectResponse, ref string) bool {
	if ref == "" {
		return false
	}
	img, err := r.cli.ImageInspect(ctx, ref)
	if err != nil {
		return false
	}
	return img.ID != "" && img.ID == existing.Image
}

// create makes a container without starting it.
func (r *Runtime) create(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	nets networks,
	proxyIP string,
	name string,
	overrideCmd string,
	aliases []string,
) (string, error) {
	labels := r.managed(dockerutil.KindService, spec.EnvID)
	labels[dockerutil.LabelService] = s.Name
	labels[dockerutil.LabelServiceKind] = s.Kind
	labels[configurationLabel] = r.serviceFingerprint(spec, s, proxyIP, overrideCmd)

	cfg := &container.Config{
		Image:  s.Image,
		Labels: labels,
		Env:    r.envList(spec, s),
	}
	switch {
	case overrideCmd != "":
		cfg.Cmd = []string{"/bin/sh", "-c", overrideCmd}
		// A migration must not inherit an entrypoint that starts the server,
		// or it runs the application instead of the migration and reports
		// success when it is killed.
		cfg.Entrypoint = []string{}
	case s.Command != "":
		cfg.Cmd = []string{"/bin/sh", "-c", s.Command}
		cfg.Entrypoint = []string{}
	}

	host := &container.HostConfig{
		// Every name this service looks up that is not inside the environment
		// resolves to the sidecar, which then decides whether the connection
		// that follows happens. This is what makes the policy apply to every
		// client rather than to the ones that read their proxy variables:
		// Node ignores them entirely, and a great many SDKs bundle a client
		// that does the same.
		DNS: []string{proxyIP},
		// Restart is deliberately off. A service that crash loops must be
		// visible as a crash loop, not hidden behind a runtime that keeps
		// starting it until the readiness wait times out with no explanation.
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
		// The size the manifest asked for, as the daemon's own cpu and memory
		// constraint.
		//
		// There is no scheduler here to reserve anything, so the single value
		// schema.Resources carries is applied as the cap alone: a container
		// with NanoCPUs set gets that share of the machine under contention
		// and no more, and one with Memory set is killed rather than allowed
		// to take the machine down with it. That is the half of the promise
		// this runtime can keep, and it is the half that matters on a laptop,
		// where the failure being reproduced is one environment starving
		// another.
		//
		// Zero is Docker's own word for unconstrained, so a manifest that
		// named no size produces exactly the container it produced before this
		// key was honoured.
		Resources: container.Resources{
			NanoCPUs: s.CPUMillis * 1_000_000,
			Memory:   s.MemoryBytes,
		},
	}

	// The service name resolves inside the environment, so a manifest can say
	// http://worker:8080 and mean it.
	//
	// A one shot job passes NONE, and that is not tidiness. A stance job runs
	// in the store's own image, so a broker's topic creation would otherwise
	// carry the alias the broker itself answers to, and for the seconds it
	// lives the name would resolve to two containers: the resolver would hand
	// the job's own connection to whichever it picked, and a topic created
	// against a container that is exiting is a topic nobody has. Two
	// containers behind one name is the failure this runtime already refuses
	// everywhere else.
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			nets.inner: {Aliases: aliases},
		},
	}

	resp, err := r.cli.ContainerCreate(ctx, cfg, host, netCfg, nil, name)
	if err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("creating %s: %v", s.Name, err))
	}
	// A service is never attached to the outer network. The sidecar is the
	// only container in the environment with a route out, which is what makes
	// the policy an enforcement rather than a request: a service that ignores
	// its proxy variables has nowhere to send the packet.
	return resp.ID, nil
}

// ingressName is deterministic for the same reason every other name here is.
func ingressName(envID, service string) string {
	return "af-ing-" + envID + "-" + service
}

// startIngress publishes a service on the host's loopback.
//
// The port was reserved before any service started, so that the service could
// be told its own address, and the daemon binds it here, after that service has
// been built and started. Anything else on the machine can take it in between,
// which is the half of the race the allocator cannot close and this loop is the
// other half of. A conflict is retried on a fresh port rather than reported,
// because the situation is transient by construction and reporting it turns a
// port bump into a failed `af up`. Anything that is not a conflict, a
// permission refusal or an unreachable daemon, surfaces on the first attempt
// rather than being retried into a longer wait for the same answer.
//
// A retry moves the address, and the containers already created were told the
// old one in AF_PUBLIC_URL and AF_ENV_URL, so those two go stale. That is worth
// it against the whole environment failing, and it is as far as the staleness
// reaches: the address `af up` prints comes from this function's answer and the
// one `af status` prints is read back off the forwarder itself, so only a
// variable baked into a container at creation can name the port that was lost.
func (r *Runtime) startIngress(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	nets networks,
	journal func(string, string) error,
) (int, error) {
	name := ingressName(spec.EnvID, s.Name)
	if err := journal(kindContainer, name); err != nil {
		return 0, err
	}
	if existing, err := r.cli.ContainerInspect(ctx, name); err == nil {
		if p, ok := publishedPort(existing.NetworkSettings.Ports); ok {
			return p, nil
		}
		if rmErr := dockerutil.RemoveContainer(ctx, r.cli, existing.ID); rmErr != nil {
			return 0, rmErr
		}
	}

	// Falling back to allocating one here keeps a caller that did not reserve
	// working.
	hostPort, reserved := spec.PublicPorts[s.Name]
	for attempt := 0; ; attempt++ {
		if !reserved {
			var err error
			hostPort, err = r.ports.Free()
			if err != nil {
				return 0, err
			}
		}
		err := r.createIngress(ctx, spec, s, nets, name, hostPort)
		if err == nil {
			// Written back so that the caller's own record of the address, and
			// every service created after this one, name the port the daemon
			// actually bound rather than the one that was asked for.
			if spec.PublicPorts != nil {
				spec.PublicPorts[s.Name] = hostPort
			}
			return hostPort, nil
		}
		if !dockerutil.IsPortTaken(err) {
			r.ports.Release(hostPort)
			return 0, err
		}
		// Not released: something else on the machine holds it, so handing it
		// out again is the one thing that cannot work. The next attempt asks
		// the allocator for a port it has not handed out and the kernel says
		// nothing is listening on.
		if attempt >= dockerutil.PortRetries {
			return 0, err
		}
		reserved = false
	}
}

// createIngress makes one attempt to publish a service on hostPort.
//
// The order is load bearing: the container is created on the edge network with
// the port binding, then attached to the inner network, and only then started.
// Started first, the forwarder cannot resolve the service's name yet and exits
// immediately, which looks exactly like a service that never came up.
//
// A failed attempt leaves nothing behind, because the name is deterministic and
// the next attempt's create would collide with the container this one made.
func (r *Runtime) createIngress(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	nets networks,
	name string,
	hostPort int,
) error {
	port, err := nat.NewPort("tcp", strconv.Itoa(s.Port))
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	labels := r.managed(dockerutil.KindSidecar, spec.EnvID)
	labels[dockerutil.LabelService] = s.Name

	resp, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image:  ingressImage,
			Labels: labels,
			Cmd: []string{"socat",
				fmt.Sprintf("TCP-LISTEN:%d,fork,reuseaddr", s.Port),
				fmt.Sprintf("TCP:%s:%d", s.Name, s.Port)},
			ExposedPorts: nat.PortSet{port: struct{}{}},
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			PortBindings: nat.PortMap{port: []nat.PortBinding{{
				// Loopback only. Publishing on every interface would put an
				// environment holding a copy of production data on whatever
				// network the laptop happens to be joined to, which for a
				// laptop is a coffee shop.
				HostIP: "127.0.0.1", HostPort: strconv.Itoa(hostPort),
			}}},
		},
		&network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{nets.edge: {}},
		}, nil, name)
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("creating the forwarder for %s: %v", s.Name, err))
	}
	remove := func() {
		// Without cancellation, because the context that failed the attempt is
		// often the one that was cancelled, and a container left created holds
		// the name against every retry after it.
		_ = dockerutil.RemoveContainer(context.WithoutCancel(ctx), r.cli, resp.ID)
	}
	if err := r.cli.NetworkConnect(ctx, nets.inner, resp.ID, &network.EndpointSettings{}); err != nil {
		remove()
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("attaching the forwarder for %s: %v", s.Name, err))
	}
	if err := r.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		remove()
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("starting the forwarder for %s: %v", s.Name, err))
	}
	return nil
}

// publishedPort reads the host port out of a port map.
func publishedPort(ports nat.PortMap) (int, bool) {
	for _, bindings := range ports {
		for _, b := range bindings {
			if n, err := strconv.Atoi(b.HostPort); err == nil && n > 0 {
				return n, true
			}
		}
	}
	return 0, false
}

// envList builds the environment a service receives.
//
// The order is fixed by sorting, so that two runs produce identical container
// configurations and a diff between them means something.
func (r *Runtime) envList(spec provider.EnvSpec, s provider.ServiceSpec) []string {
	vars := map[string]string{}
	if raw := spec.DatabaseURL.Reveal(); raw != "" {
		vars["DATABASE_URL"] = raw
	}
	// The address this service answers on from outside the environment.
	//
	// An application that emails a link, redirects through OAuth, or registers
	// a webhook callback has to build an absolute URL, and the only address
	// that works from a browser is the one the runtime published. Nothing told
	// it before this, so every such application sent a link to its own
	// container port.
	if p, ok := spec.PublicPorts[s.Name]; ok && p > 0 {
		public := fmt.Sprintf("http://127.0.0.1:%d", p)
		vars["AF_PUBLIC_URL"] = public
		// The conventional names too, because most frameworks already read
		// one of them and an application that does needs no change at all.
		vars["PUBLIC_URL"] = public
		vars["BASE_URL"] = public
	}
	// The environment's own address, which is a different question from this
	// service's.
	//
	// A link in an email has to land on the application a person opens, and
	// the service that sends it is usually not that application: here the API
	// mails the sign in link and the web application serves the page it lands
	// on. Every container gets the same answer, which is the address `af up`
	// prints and a pull request comment links to.
	if url := environmentURL(spec); url != "" {
		vars["AF_ENV_URL"] = url
	}
	if s.Port > 0 {
		vars["PORT"] = strconv.Itoa(s.Port)
		// Binding to loopback inside a container makes the service
		// unreachable from anywhere, including the readiness check, and it is
		// the single most common reason a container looks healthy and answers
		// nothing.
		vars["HOST"] = "0.0.0.0"
	}
	// Every outbound request goes through the sidecar. The variables are what
	// a well behaved library reads; the network underneath is what makes it
	// true for the others, since the inner network has no route out and a
	// service that ignores these has nowhere to send the packet.
	proxyURL := fmt.Sprintf("http://%s:%d", ProxyAlias, ProxyPort)
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		vars[k] = proxyURL
	}
	// Traffic inside the environment must not be sent to the proxy: a service
	// calling another service, or the database, is not egress, and routing it
	// through the sidecar would make every internal call a policy decision.
	//
	// That is what this list is for, and for a long time it did not contain
	// the services. It named localhost, the database and the sidecar, so a
	// manifest that said http://api:3000 from one service to another had that
	// request sent to the proxy and decided against the egress policy, which
	// on any environment with the usual default refused it. The decision log
	// then showed a blocked request to a host called "api", which reads as
	// the environment being broken rather than as a variable being short.
	// Nothing caught it because nothing had two services talking to each
	// other until the runtime conformance suite did.
	internal := []string{"localhost", "127.0.0.1", "::1", DatabaseAlias, ProxyAlias}
	for _, other := range spec.Services {
		if other.Name != "" {
			internal = append(internal, other.Name)
		}
	}
	// And the stores the environment provides, for exactly the reason the
	// services are here. A datastore is not a service and is not the database,
	// so it was in neither list, and the one address a manifest declaring a
	// masked ClickHouse most wants to reach was the one the policy refused.
	internal = append(internal, spec.Datastores...)
	noProxy := strings.Join(internal, ",")
	vars["NO_PROXY"] = noProxy
	vars["no_proxy"] = noProxy

	if spec.CACertPEM != "" {
		// There is no single way to point a runtime at a certificate, which is
		// the whole problem: Node reads one variable, Python's requests reads
		// another, Go reads a third, and each ignores the rest. Setting only
		// one is invisible until a request fails for a reason that looks
		// nothing like a certificate.
		for k, v := range envcert.TrustEnv() {
			vars[k] = v
		}
	}

	vars["AF_ENV_ID"] = spec.EnvID
	vars["AF_SERVICE"] = s.Name
	// An application that behaves differently in a preview environment can
	// read this. It is deliberately not NODE_ENV or anything a framework
	// already means something by.
	vars["ANTIFAILURE"] = "1"
	// A service's own name resolves inside the environment, so a call from web
	// to worker is internal and must not be proxied either.
	for _, peer := range spec.Services {
		vars["NO_PROXY"] += "," + peer.Name
		vars["no_proxy"] += "," + peer.Name
	}

	// Last, so that a manifest can override anything above it. Somebody who
	// sets HTTP_PROXY themselves has a reason, and silently winning over them
	// would be the kind of surprise that costs an afternoon.
	for k, v := range s.Env {
		vars[k] = v.Reveal()
	}

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+vars[k])
	}
	return out
}

// installCA writes the environment certificate into a container before it
// starts, so that the runtime finds it at the path the variables name.
func (r *Runtime) installCA(ctx context.Context, id string, spec provider.EnvSpec) error {
	// World readable, because a certificate authority's certificate is public
	// by construction and the service does not run as root. Copying it in at
	// 0600 produced a container whose runtime could see the file, could not
	// open it, and reported a self-signed certificate error that pointed
	// nowhere near the permissions.
	if spec.CACertPEM != "" {
		if err := r.copyInto(ctx, id, envcert.BundlePath, 0o644, []byte(spec.CACertPEM)); err != nil {
			return err
		}
	}
	if spec.DatabaseCACertPEM != "" {
		return r.copyInto(ctx, id, provider.DatabaseTrustBundlePath, 0o644, []byte(spec.DatabaseCACertPEM))
	}
	return nil
}

// runOnce runs a command to completion in a throwaway container.
//
// Migrations use it. The container is removed whether the command succeeded or
// not, but its output is read first, because the output is the only thing that
// explains a failed migration.
func (r *Runtime) runOnce(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	nets networks,
	proxyIP string,
	command string,
	journal func(string, string) error,
) error {
	return r.runOnceAs(ctx, spec, s, nets, proxyIP, command,
		s.Name+"-migrate", s.Name+" migration", []string{s.Name}, journal)
}

// runOnceAs is runOnce with the name and the words it fails in.
//
// The suffix is part of the container's name and the role is what a failure
// calls the thing that failed. They are arguments rather than constants
// because a migration is no longer the only command an environment runs to
// completion: a datastore's stance can be a rebuild or a topic creation, and
// naming either of those "migration" in the container list and in the error
// sends somebody to look at their schema.
func (r *Runtime) runOnceAs(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	nets networks,
	proxyIP string,
	command string,
	suffix string,
	role string,
	aliases []string,
	journal func(string, string) error,
) error {
	name := containerName(spec.EnvID, suffix, 1)
	if err := journal(kindContainer, name); err != nil {
		return err
	}
	id, err := r.create(ctx, spec, s, nets, proxyIP, name, command, aliases)
	if err != nil {
		return err
	}
	if err := r.installCA(ctx, id, spec); err != nil {
		return err
	}
	var output string
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		_ = dockerutil.RemoveContainer(c, r.cli, id)
	}()

	if err := r.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("starting the %s: %v", role, err))
	}
	code, waitErr := dockerutil.AwaitExit(ctx, r.cli, id)
	if waitErr != nil {
		return aferrors.Wrap(waitErr, aferrors.AFRUN040,
			"detail", fmt.Sprintf("waiting for the %s: %v", role, waitErr))
	}
	if code != 0 {
		output = r.lastLogLines(ctx, id)
		return aferrors.Coded(aferrors.AFRUN005,
			"service", role,
			"code", strconv.FormatInt(code, 10)+"\n"+output)
	}
	return nil
}

// waitReady blocks until the service answers, or reports why it did not.
func (r *Runtime) waitReady(
	ctx context.Context,
	s provider.ServiceSpec,
	id string,
	hostPort int,
	timeout time.Duration,
	progress func(string),
) error {
	if s.Port <= 0 || hostPort == 0 {
		// Nothing to poll from here. A worker is ready when it is running, and
		// asking for more would mean inventing a protocol the application does
		// not speak.
		return r.confirmStillRunning(ctx, s, id)
	}

	deadline := r.clock.Now().Add(timeout)
	hc := airgap.Client(airgap.SiteServiceProbe, 5*time.Second)
	attempt := 0
	for {
		if err := r.confirmStillRunning(ctx, s, id); err != nil {
			return err
		}
		if r.probe(ctx, hc, s, hostPort) {
			return nil
		}
		attempt++
		if attempt%20 == 0 {
			progress(fmt.Sprintf("%s: still waiting", s.Name))
		}
		if !r.clock.Now().Before(deadline) {
			health := s.HealthPath
			if health == "" {
				health = "/"
			}
			return aferrors.Coded(aferrors.AFRUN004,
				"service", s.Name, "timeout", timeout.Round(time.Second).String(),
				"health", health)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.clock.After(500 * time.Millisecond):
		}
	}
}

// probe reports whether the service answered.
//
// Any HTTP status counts, including a 500. Readiness here means the process is
// listening and routing, not that the application is healthy: a service that
// answers 500 has started, and reporting it as never having started would send
// somebody looking at the runtime instead of at their own handler.
func (r *Runtime) probe(ctx context.Context, hc *http.Client, s provider.ServiceSpec, hostPort int) bool {
	path := s.HealthPath
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	target := fmt.Sprintf("http://127.0.0.1:%d%s", hostPort, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	resp, err := hc.Do(req)
	if err != nil {
		// A service that speaks something other than HTTP still counts as
		// ready once it accepts a connection, so the port is tried directly
		// before giving up on this round.
		conn, dialErr := airgap.Dial(airgap.SiteServiceProbe, "tcp",
			net.JoinHostPort("127.0.0.1", strconv.Itoa(hostPort)), 2*time.Second)
		if dialErr != nil {
			return false
		}
		_ = conn.Close()
		return s.HealthPath == ""
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	return true
}

// confirmStillRunning turns a container that has already exited into an error
// naming the exit code, rather than letting the readiness loop wait out its
// whole timeout on something that will never answer.
func (r *Runtime) confirmStillRunning(ctx context.Context, s provider.ServiceSpec, id string) error {
	insp, err := r.cli.ContainerInspect(ctx, id)
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("inspecting %s: %v", s.Name, err))
	}
	if insp.State == nil || insp.State.Running {
		return nil
	}
	return aferrors.Coded(aferrors.AFRUN005,
		"service", s.Name, "code", strconv.Itoa(insp.State.ExitCode))
}

// maxLogLines is what is kept from a failed service.
//
// The tail, because a stack trace ends with the line that matters and a
// framework's startup banner does not.
const maxLogLines = 40

// lastLogLines reads the end of a container's output, redacted.
func (r *Runtime) lastLogLines(ctx context.Context, id string) string {
	rc, err := r.cli.ContainerLogs(context.WithoutCancel(ctx), id, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(maxLogLines),
	})
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(io.LimitReader(rc, 256<<10))
	if err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	return r.redactor.String(stripDockerLogFraming(string(body)))
}

// stripDockerLogFraming removes the eight byte header Docker prefixes each log
// frame with when the container has no TTY.
//
// Without this the output is readable but carries a control character every
// few lines, which is enough to make somebody think the log itself is corrupt
// and stop reading it.
func stripDockerLogFraming(s string) string {
	var b strings.Builder
	for len(s) >= 8 {
		if s[0] > 2 {
			// Not a frame header, so the stream was not multiplexed.
			return s
		}
		n := int(s[4])<<24 | int(s[5])<<16 | int(s[6])<<8 | int(s[7])
		s = s[8:]
		if n > len(s) {
			n = len(s)
		}
		b.WriteString(s[:n])
		s = s[n:]
	}
	b.WriteString(s)
	return b.String()
}

// environmentURL is the address of the first web service the manifest declares.
//
// The same rule Env.URL uses, applied before anything is running, because the
// containers have to be told it at creation time and the running environment
// does not exist yet.
func environmentURL(spec provider.EnvSpec) string {
	for _, s := range spec.Services {
		if s.Kind != "web" || s.Port <= 0 {
			continue
		}
		if p, ok := spec.PublicPorts[s.Name]; ok && p > 0 {
			return fmt.Sprintf("http://127.0.0.1:%d", p)
		}
	}
	return ""
}
