package local

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/proxyimage"
	"github.com/antifailure/antifailure/engine/pkg/airgap"
	"github.com/antifailure/antifailure/engine/pkg/emulator"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Putting production's declared cloud resources into the emulators, before any
// service starts.
//
// WHY THE REQUESTS GO THROUGH THE SIDECAR, which is the decision this file
// stands or falls on. An emulator joins the inner network and nothing else, so
// the engine cannot reach one: there is no published port, the network is
// created with Docker's internal flag, and the host has no route to it. Two
// ways out of that were available and only one of them proves anything.
//
// The one not taken: forward straight to the emulator's own address. It is one
// line shorter and it creates the bucket. It also bypasses every part of this
// product. The claim an environment makes is not "there is a bucket in
// LocalStack", it is "the application, unmodified, addressing
// s3.amazonaws.com, reaches a bucket". A seeder that dialled the container
// would create a bucket the application might still be refused at, and it
// would have reported success.
//
// The one taken: a forwarder container, the sidecar's own image in the
// -forward-listen mode the published ingress already uses, published on the
// host's loopback and relaying into the environment's sidecar. The engine then
// speaks to the sidecar as an ORDINARY HTTP PROXY, which is exactly what every
// service in the environment is told to do through HTTP_PROXY, and the request
// carries the provider's own hostname. So the route under test is the route
// the application has: the policy decides it, the sidecar matches the host,
// and the emulator answers. A rule that does not route the host refuses the
// seeding for the same reason it would refuse the application, and that
// refusal is reported rather than worked around.
//
// The forwarder is journalled before it is created, like every other resource,
// and removed when seeding finishes. Journalled even though it is short lived,
// because "short lived" is a description of the happy path: a process killed
// between the create and the remove leaves a container, and a container
// created before it was recorded is a container teardown cannot find.

// seedForwarderName is the forwarder's container name.
func seedForwarderName(envID string) string { return "af-seed-" + envID }

// seedRequestTimeout bounds one request to an emulator.
//
// Generous, because the first request to a freshly started emulator arrives
// while it is still loading providers, and the readiness wait before this
// proves the port is open rather than that the service behind it is fast.
const seedRequestTimeout = 60 * time.Second

// seedForwarderReadyTimeout bounds the wait for the forwarder to accept a
// connection on the host.
//
// It is a container this build wrote, from an image already on the daemon,
// running one accept loop, so thirty seconds is many times what it takes. The
// wait exists at all because a started container is not a listening one, which
// is the same distinction AF-RUN-049 exists for.
const seedForwarderReadyTimeout = 30 * time.Second

// seedCloudResources creates the declared resources inside the emulators.
//
// It never fails the environment, and that is deliberate. A twin with the
// bucket in it and the lifecycle rule reported missing is more useful than no
// twin at all, and a resource that could not be created is REPORTED, at the
// same volume, in the same place. What it must never do is stay quiet: every
// declaration produces a line when it is anything other than fully reproduced.
func (r *Runtime) seedCloudResources(
	ctx context.Context,
	spec provider.EnvSpec,
	nets networks,
	journal func(string, string) error,
	progress func(string),
) {
	if len(spec.CloudResources) == 0 {
		return
	}
	steps := emulator.PlanSeeding(spec.CloudResources)
	seeder := emulator.Seeder{Routes: emulateRoutes(spec.Egress)}

	// A plan with nothing to send still produces results, and they are the
	// ones that matter most: a resource type nothing here creates, and an
	// Azure container this build cannot sign a request for. Starting a
	// forwarder for them would be a container nobody needs.
	if plannedRequests(steps) > 0 {
		send, done, err := r.seedTransport(ctx, spec.EnvID, nets, journal)
		if err != nil {
			// Reported through the results rather than returned, so that every
			// declaration carries the same reason and the reason is the one
			// thing a reader needs. An `af up` that failed here would take the
			// environment down over the seeding of it.
			send = func(context.Context, string, emulator.Request) (emulator.Response, error) {
				return emulator.Response{}, fmt.Errorf(
					"the environment could not be reached to seed it: %w", err)
			}
		}
		if done != nil {
			defer done()
		}
		seeder.Send = send
	}

	progress(fmt.Sprintf("creating %d declared cloud %s inside the emulators",
		len(spec.CloudResources), plural(len(spec.CloudResources), "resource", "resources")))
	var reproduced int
	for _, result := range seeder.Run(ctx, steps) {
		if result.Worst() == emulator.Reproduced {
			reproduced++
		}
		for _, line := range result.Lines() {
			progress(line)
		}
	}
	progress(fmt.Sprintf("%d of %d declared cloud resources are reproduced in the twin",
		reproduced, len(spec.CloudResources)))
}

// plannedRequests counts what the plan will actually send.
func plannedRequests(steps []emulator.Step) int {
	var n int
	for _, s := range steps {
		n += len(s.Actions)
	}
	return n
}

// emulateRoutes answers whether the environment's policy sends a hostname to a
// particular emulator.
//
// It asks the policy's own matcher rather than a second one written here, for
// the reason emulator.HostMatches gives: two host matchers in one repository
// drift, and the drift shows up as a seeding request the plan believed was
// routed and the sidecar refused, reported as a missing bucket.
func emulateRoutes(egress *schema.Egress) func(emulatorName, host string) bool {
	return func(emulatorName, host string) bool {
		if egress == nil {
			return false
		}
		for _, rule := range egress.Rules {
			if rule.Mode != schema.ModeEmulate || rule.Emulator != emulatorName {
				continue
			}
			if emulator.HostMatches(rule.Host, host) {
				return true
			}
		}
		return false
	}
}

// seedTransport starts the forwarder and returns a Send that goes through it.
//
// The returned close function removes the forwarder. It is returned rather
// than deferred here because the caller holds the requests, and a forwarder
// removed while a request is in flight is a connection reset reported as an
// emulator that could not be asked.
func (r *Runtime) seedTransport(
	ctx context.Context,
	envID string,
	nets networks,
	journal func(string, string) error,
) (emulator.Send, func(), error) {
	name := seedForwarderName(envID)
	if err := journal(kindContainer, name); err != nil {
		return nil, nil, err
	}
	// A forwarder left by an interrupted run holds the name, and its published
	// port is not one this process knows. Removed rather than reused.
	if existing, err := r.cli.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); err == nil {
		if rmErr := dockerutil.RemoveContainer(ctx, r.cli, existing.Container.ID); rmErr != nil {
			return nil, nil, rmErr
		}
	}
	hostPort, err := r.ports.Free()
	if err != nil {
		return nil, nil, err
	}
	id, err := r.createSeedForwarder(ctx, envID, nets, name, hostPort)
	if err != nil {
		r.ports.Release(hostPort)
		return nil, nil, err
	}
	remove := func() {
		_ = dockerutil.RemoveContainer(context.WithoutCancel(ctx), r.cli, id)
		r.ports.Release(hostPort)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(hostPort))
	if err := r.waitSeedForwarder(ctx, address); err != nil {
		remove()
		return nil, nil, err
	}
	proxyURL, err := url.Parse("http://" + address)
	if err != nil {
		remove()
		return nil, nil, err
	}
	// Through the air gap guard, like every other outbound client in this
	// product. The addresses are all loopback, which the guard permits
	// unconditionally, so a sealed installation seeds its twin exactly as an
	// open one does. What the guard buys here is that a future edit pointing
	// this somewhere real cannot make it out of a sealed process silently, and
	// that the attempt is in the ledger either way.
	transport := airgap.Transport(airgap.SiteEmulatorSeeding)
	transport.Proxy = http.ProxyURL(proxyURL)
	// Off, because every request here is to a different provider hostname
	// through one proxy and the connections are few. Keeping them alive would
	// hold the forwarder open past the point this wants to remove it.
	transport.DisableKeepAlives = true
	httpClient := &http.Client{Transport: transport, Timeout: seedRequestTimeout}
	return seedSend(httpClient), remove, nil
}

// seedSend turns a client into the transport the plan uses.
//
// Plain HTTP at the provider's own hostname. Not HTTPS, and the reason is that
// this is the one caller in the environment that gains nothing from TLS: the
// sidecar terminates it with the environment's own authority for the benefit
// of an APPLICATION that must not know an emulator exists, and this is not an
// application. The hostname, the routing and the emulator's answer are the
// same either way, and the sidecar's own package proves the TLS half with a
// verifying client.
func seedSend(httpClient *http.Client) emulator.Send {
	return func(ctx context.Context, host string, r emulator.Request) (emulator.Response, error) {
		var resp emulator.Response
		var err error
		for attempt := 0; attempt < seedAttempts; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return resp, ctx.Err()
				case <-time.After(seedRetryPause):
				}
			}
			resp, err = seedOnce(ctx, httpClient, host, r)
			// Retried on a transport failure and on the emulator's own 5xx,
			// and on NOTHING ELSE. An emulator whose port is open while its
			// providers are still loading answers 502 and 503 for a second or
			// two after the readiness probe has passed, and a plan that read
			// the first of those as the answer would report an absent bucket
			// for a twin that was merely still starting. A 4xx is a decision
			// the emulator has made and retrying it would only make the same
			// wrong request again, more slowly.
			if err == nil && resp.Status < 500 {
				return resp, nil
			}
		}
		return resp, err
	}
}

// The retry's two numbers. Five attempts a second apart, which is four
// seconds of tolerance for an emulator that is listening and not yet serving,
// and is far short of the readiness budget an emulator that never comes up
// has already spent.
const (
	seedAttempts   = 5
	seedRetryPause = time.Second
)

func seedOnce(
	ctx context.Context, httpClient *http.Client, host string, r emulator.Request,
) (emulator.Response, error) {
	target := "http://" + host + r.Path
	if r.Query != "" {
		target += "?" + r.Query
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, strings.NewReader(r.Body))
	if err != nil {
		return emulator.Response{}, err
	}
	req.Host = host
	for k, v := range r.Header {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return emulator.Response{}, err
	}
	defer resp.Body.Close()
	// Bounded, because the body of an emulator's answer is read into a
	// progress line and a listing of ten thousand objects is not one.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return emulator.Response{Status: resp.StatusCode}, err
	}
	return emulator.Response{Status: resp.StatusCode, Body: string(body)}, nil
}

// createSeedForwarder publishes a relay into the environment's sidecar.
//
// The same order the published ingress uses and for the same reason: created
// on the edge network with the port binding, attached to the inner network,
// and only then started. Started first, it would accept a connection it has no
// route to the sidecar to deliver.
func (r *Runtime) createSeedForwarder(
	ctx context.Context, envID string, nets networks, name string, hostPort int,
) (string, error) {
	port, err := network.ParsePort(strconv.Itoa(ProxyPort) + "/tcp")
	if err != nil {
		return "", err
	}
	labels := r.managed(dockerutil.KindSidecar, envID)
	labels[dockerutil.LabelService] = "seed"

	resp, err := r.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			Image:  proxyimage.Tag(),
			Labels: labels,
			Cmd: []string{
				"-forward-listen", ":" + strconv.Itoa(ProxyPort),
				"-forward-to", net.JoinHostPort(ProxyAlias, strconv.Itoa(ProxyPort)),
			},
			ExposedPorts: network.PortSet{port: struct{}{}},
		},
		HostConfig: &container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			PortBindings: network.PortMap{port: []network.PortBinding{{
				// Loopback only, for the reason the published ingress gives:
				// publishing on every interface would put a door into an
				// environment holding a copy of production data onto whatever
				// network the laptop happens to be joined to.
				HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: strconv.Itoa(hostPort),
			}}},
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{nets.edge: {}},
		},
		Name: name,
	})
	if err != nil {
		return "", fmt.Errorf("creating the seeding forwarder: %w", err)
	}
	if _, err := r.cli.NetworkConnect(ctx, nets.inner, client.NetworkConnectOptions{
		Container:      resp.ID,
		EndpointConfig: &network.EndpointSettings{},
	}); err != nil {
		_ = dockerutil.RemoveContainer(context.WithoutCancel(ctx), r.cli, resp.ID)
		return "", fmt.Errorf("attaching the seeding forwarder: %w", err)
	}
	if _, err := r.cli.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{}); err != nil {
		_ = dockerutil.RemoveContainer(context.WithoutCancel(ctx), r.cli, resp.ID)
		return "", fmt.Errorf("starting the seeding forwarder: %w", err)
	}
	return resp.ID, nil
}

// waitSeedForwarder blocks until the published port accepts a connection.
func (r *Runtime) waitSeedForwarder(ctx context.Context, address string) error {
	deadline := r.clock.Now().Add(seedForwarderReadyTimeout)
	pause := emulatorProbeFirstPause
	for {
		conn, err := airgap.Dial(
			airgap.SiteEmulatorSeeding, "tcp", address, emulatorDialTimeout)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if !r.clock.Now().Before(deadline) {
			return fmt.Errorf(
				"the forwarder that carries the seeding into the environment never accepted a "+
					"connection at %s within %s: %w", address, seedForwarderReadyTimeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.clock.After(pause):
		}
		if pause < emulatorProbeMaxPause {
			pause *= 2
		}
	}
}
