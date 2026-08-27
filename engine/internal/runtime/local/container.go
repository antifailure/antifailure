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
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// containerName is deterministic, so a second run finds the container the
// first one made rather than starting a duplicate that fights it for the port.
func containerName(envID, service string) string {
	return "af-svc-" + envID + "-" + service
}

// startService creates, starts, and waits for one service.
func (r *Runtime) startService(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	nets networks,
	proxyIP string,
	journal func(string, string) error,
	progress func(string),
) (provider.RunningService, error) {
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

	name := containerName(spec.EnvID, s.Name)
	if err := journal("container", name); err != nil {
		return running, err
	}
	// A container left by an interrupted run holds the name. Reusing a running
	// one keeps Up idempotent; replacing a stopped one is what makes a second
	// Up after a crash work rather than fail on a name conflict.
	if existing, err := r.cli.ContainerInspect(ctx, name); err == nil {
		if existing.State != nil && existing.State.Running {
			running.ContainerID = existing.ID
			running.State = "running"
			running.Ready = true
			return running, nil
		}
		if rmErr := dockerutil.RemoveContainer(ctx, r.cli, existing.ID); rmErr != nil {
			return running, rmErr
		}
	}

	id, err := r.create(ctx, spec, s, nets, proxyIP, name, "")
	if err != nil {
		return running, err
	}
	running.ContainerID = id

	if err := r.installCA(ctx, id, spec); err != nil {
		return running, err
	}

	if err := r.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		running.State = "failed to start"
		return running, aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("starting %s: %v", s.Name, err))
	}
	running.State = "running"

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
	if err := r.waitReady(ctx, s, id, hostPort, timeout, progress); err != nil {
		running.Detail = r.lastLogLines(ctx, id)
		return running, err
	}
	running.Ready = true
	if running.URL != "" {
		progress(fmt.Sprintf("%s: ready at %s", s.Name, running.URL))
	} else {
		progress(fmt.Sprintf("%s: ready", s.Name))
	}
	return running, nil
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
) (string, error) {
	labels := dockerutil.Managed(dockerutil.KindService, spec.EnvID, r.clock.Now())
	labels[dockerutil.LabelService] = s.Name
	labels[dockerutil.LabelServiceKind] = s.Kind

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
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			nets.inner: {
				// The service name resolves inside the environment, so a
				// manifest can say http://worker:8080 and mean it.
				Aliases: []string{s.Name},
			},
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
// The order is load bearing: the container is created on the edge network with
// the port binding, then attached to the inner network, and only then started.
// Started first, the forwarder cannot resolve the service's name yet and exits
// immediately, which looks exactly like a service that never came up.
func (r *Runtime) startIngress(
	ctx context.Context,
	spec provider.EnvSpec,
	s provider.ServiceSpec,
	nets networks,
	journal func(string, string) error,
) (int, error) {
	name := ingressName(spec.EnvID, s.Name)
	if err := journal("container", name); err != nil {
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

	hostPort, err := r.ports.Free()
	if err != nil {
		return 0, err
	}
	release := func() { r.ports.Release(hostPort) }

	port, err := nat.NewPort("tcp", strconv.Itoa(s.Port))
	if err != nil {
		release()
		return 0, aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	labels := dockerutil.Managed(dockerutil.KindSidecar, spec.EnvID, r.clock.Now())
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
		release()
		return 0, aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("creating the forwarder for %s: %v", s.Name, err))
	}
	if err := r.cli.NetworkConnect(ctx, nets.inner, resp.ID, &network.EndpointSettings{}); err != nil {
		release()
		return 0, aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("attaching the forwarder for %s: %v", s.Name, err))
	}
	if err := r.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		release()
		return 0, aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", fmt.Sprintf("starting the forwarder for %s: %v", s.Name, err))
	}
	return hostPort, nil
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
	if spec.CACertPEM == "" {
		return nil
	}
	// World readable, because a certificate authority's certificate is public
	// by construction and the service does not run as root. Copying it in at
	// 0600 produced a container whose runtime could see the file, could not
	// open it, and reported a self-signed certificate error that pointed
	// nowhere near the permissions.
	return r.copyInto(ctx, id, envcert.BundlePath, 0o644, []byte(spec.CACertPEM))
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
	name := containerName(spec.EnvID, s.Name+"-migrate")
	if err := journal("container", name); err != nil {
		return err
	}
	id, err := r.create(ctx, spec, s, nets, proxyIP, name, command)
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
			"detail", fmt.Sprintf("starting the %s migration: %v", s.Name, err))
	}
	statusCh, errCh := r.cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return aferrors.Wrap(err, aferrors.AFRUN040,
				"detail", fmt.Sprintf("waiting for the %s migration: %v", s.Name, err))
		}
	case status := <-statusCh:
		if status.StatusCode != 0 {
			output = r.lastLogLines(ctx, id)
			return aferrors.Coded(aferrors.AFRUN005,
				"service", s.Name+" migration",
				"code", strconv.FormatInt(status.StatusCode, 10)+"\n"+output)
		}
	case <-ctx.Done():
		return ctx.Err()
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
	hc := &http.Client{Timeout: 5 * time.Second}
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
		conn, dialErr := net.DialTimeout("tcp",
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
