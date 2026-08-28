package local

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	dockerbuild "github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/antifailure/antifailure/engine/pkg/provider"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/envcert"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/proxyimage"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// ProxyAlias is the hostname the sidecar answers to inside an environment, and
// ProxyPort is where it listens.
//
// Fixed names, because they appear in every service's proxy variables and in
// every decision log line, and a name that changes per environment makes two
// runs impossible to compare.
const (
	ProxyAlias = "af-proxy"
	ProxyPort  = 3128
)

// configPath is where the sidecar's configuration is placed inside it.
const configPath = "/etc/antifailure/proxy.json"

// sidecarConfig mirrors the sidecar's own Config. It is declared here rather
// than imported so that the runtime does not depend on a main package, and it
// is one struct in two places on purpose: the sidecar is compiled from source
// carried in this binary, so a mismatch fails its own test rather than a
// build.
type sidecarConfig struct {
	Egress      schema.Egress     `json:"egress"`
	Subnet      string            `json:"subnet"`
	Internal    []string          `json:"internal"`
	EnvID       string            `json:"env_id"`
	MockPacks   []string          `json:"mock_packs,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
	Resolver    string            `json:"resolver,omitempty"`
	CACert      string            `json:"ca_cert,omitempty"`
	CAKey       string            `json:"ca_key,omitempty"`
}

// startProxy builds, places, and starts the egress sidecar.
//
// The sidecar is the only container in the environment on both networks, so it
// is the only thing with a route out. Services are told to use it through the
// standard proxy variables, and what makes that trustworthy is not the
// variables, which any library may ignore, but the network underneath: a
// service that ignores them has nowhere to send the packet. A badly behaved
// SDK fails to connect rather than quietly reaching the internet.
func (r *Runtime) startProxy(
	ctx context.Context,
	envID string,
	egress *schema.Egress,
	serviceNames []string,
	ca *envcert.Authority,
	credentials map[string]secrets.Value,
	mockPacks []string,
	modelEnv []string,
	nets networks,
	journal func(string, string) error,
	progress func(string),
) (string, error) {
	if err := r.ensureProxyImage(ctx, progress); err != nil {
		return "", err
	}

	name := proxyName(envID)
	if err := journal(kindContainer, name); err != nil {
		return "", err
	}
	if existing, err := r.cli.ContainerInspect(ctx, name); err == nil {
		if existing.State != nil && existing.State.Running {
			return runningProxyIP(existing.NetworkSettings, nets.inner)
		}
		if rmErr := dockerutil.RemoveContainer(ctx, r.cli, existing.ID); rmErr != nil {
			return "", rmErr
		}
	}

	labels := dockerutil.Managed(dockerutil.KindSidecar, envID, r.clock.Now())
	labels[dockerutil.LabelService] = ProxyAlias

	resp, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image:  proxyimage.Tag(),
			Labels: labels,
			Cmd:    []string{"-config", configPath},
			// Passed as an environment variable rather than written into the
			// configuration file, so a model key never lands on disk. Only a
			// rule in synth mode uses it, and an environment with none is
			// given none.
			Env: modelEnv,
		},
		&container.HostConfig{
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
		},
		&network.NetworkingConfig{
			// Created on the outer network, because that is the one with a
			// route out. The inner one is attached before it starts.
			EndpointsConfig: map[string]*network.EndpointSettings{nets.edge: {}},
		}, nil, name)
	if err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", "creating the egress proxy: "+err.Error())
	}

	if err := r.cli.NetworkConnect(ctx, nets.inner, resp.ID,
		&network.EndpointSettings{Aliases: []string{ProxyAlias}}); err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", "attaching the egress proxy: "+err.Error())
	}
	subnet, err := r.networkSubnet(ctx, nets.inner)
	if err != nil {
		return "", err
	}

	// Written in rather than baked into the image, so that editing a rule
	// rebuilds nothing and one image serves every environment on the machine.
	//
	// The subnet is passed rather than the address, because Docker does not
	// assign an address until the container starts, which is after this file
	// has to exist. The sidecar finds its own inside it.
	cfg := sidecarConfig{
		Egress:   orEmptyEgress(egress),
		Subnet:   subnet,
		Internal: append([]string{DatabaseAlias, ProxyAlias}, serviceNames...),
		EnvID:    envID,
	}
	if ca != nil {
		cfg.CACert, cfg.CAKey = ca.CertPEM, ca.KeyPEM.Reveal()
	}
	cfg.MockPacks = mockPacks
	if len(credentials) > 0 {
		cfg.Credentials = make(map[string]string, len(credentials))
		for name, value := range credentials {
			cfg.Credentials[name] = value.Reveal()
		}
	}
	compiled, err := json.Marshal(cfg)
	if err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN040, "detail", "compiling the policy: "+err.Error())
	}
	// Readable only by root, and the sidecar is the one container that runs
	// as root, because this file carries the authority's private key.
	if err := r.copyInto(ctx, resp.ID, configPath, 0o600, compiled); err != nil {
		return "", err
	}

	if err := r.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", "starting the egress proxy: "+err.Error())
	}
	if err := r.waitProxyReady(ctx, resp.ID); err != nil {
		return "", err
	}
	selfIP, err := r.startedProxyIP(ctx, resp.ID, nets.inner)
	if err != nil {
		return "", err
	}
	rules := 0
	if egress != nil {
		rules = len(egress.Rules)
	}
	progress(fmt.Sprintf(
		"egress proxy ready with %d %s, everything else takes the default",
		rules, plural(rules, "rule", "rules")))
	return selfIP, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// networkSubnet reads a network's address range.
func (r *Runtime) networkSubnet(ctx context.Context, networkID string) (string, error) {
	insp, err := r.cli.NetworkInspect(ctx, networkID, network.InspectOptions{})
	if err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	for _, cfg := range insp.IPAM.Config {
		if cfg.Subnet != "" {
			return cfg.Subnet, nil
		}
	}
	return "", aferrors.Coded(aferrors.AFRUN040,
		"detail", "the environment network has no address range")
}

// startedProxyIP reads the sidecar's address once it is running.
func (r *Runtime) startedProxyIP(ctx context.Context, id, networkID string) (string, error) {
	insp, err := r.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	return runningProxyIP(insp.NetworkSettings, networkID)
}

// runningProxyIP reads the sidecar's address on the environment's inner
// network.
//
// It is what every service is pointed at for DNS, so a sidecar without one
// means an environment where nothing is intercepted, which would look
// contained and would not be.
func runningProxyIP(settings *dockertypes.NetworkSettings, networkID string) (string, error) {
	if settings != nil {
		for _, ep := range settings.Networks {
			if ep != nil && ep.NetworkID == networkID && ep.IPAddress != "" {
				return ep.IPAddress, nil
			}
		}
	}
	return "", aferrors.Coded(aferrors.AFRUN040,
		"detail", "the egress proxy has no address on the environment network")
}

func proxyName(envID string) string { return "af-proxy-" + envID }

func orEmptyEgress(e *schema.Egress) schema.Egress {
	if e == nil {
		// No egress section means block everything, which is what an empty
		// policy with no default compiles to. Sending null would make the
		// sidecar fail to parse and the environment fail to start, for a
		// manifest that is perfectly valid.
		return schema.Egress{Default: schema.ModeBlock}
	}
	return *e
}

// waitProxyReady blocks until the sidecar says it is listening.
//
// It announces itself on stdout before it binds, so this waits for a line
// rather than for a timeout. A service started before the proxy is listening
// gets a connection refused on its first outbound call, which looks exactly
// like a blocked host and is not one.
func (r *Runtime) waitProxyReady(ctx context.Context, id string) error {
	deadline := r.clock.Now().Add(90 * time.Second)
	for {
		out := r.lastLogLines(ctx, id)
		if strings.Contains(out, `"event":"ready"`) {
			return nil
		}
		if err := r.proxyStillRunning(ctx, id, out); err != nil {
			return err
		}
		if !r.clock.Now().Before(deadline) {
			return aferrors.Coded(aferrors.AFRUN004,
				"service", "the egress proxy", "timeout", "90s", "health", "startup")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.clock.After(200 * time.Millisecond):
		}
	}
}

func (r *Runtime) proxyStillRunning(ctx context.Context, id, out string) error {
	insp, err := r.cli.ContainerInspect(ctx, id)
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	if insp.State == nil || insp.State.Running {
		return nil
	}
	return aferrors.Coded(aferrors.AFRUN005,
		"service", "the egress proxy",
		"code", strconv.Itoa(insp.State.ExitCode)+"\n"+out)
}

// ensureProxyImage builds the sidecar image if it is not already present.
func (r *Runtime) ensureProxyImage(ctx context.Context, progress func(string)) error {
	tag := proxyimage.Tag()
	if _, _, err := r.cli.ImageInspectWithRaw(ctx, tag); err == nil {
		return nil
	}
	progress("building the egress proxy (once per version)")

	resp, err := r.cli.ImageBuild(ctx, proxyimage.BuildContext(), dockerbuild.ImageBuildOptions{
		Tags:   []string{tag},
		Remove: true,
		Labels: dockerutil.Managed(dockerutil.KindSidecar, "", r.clock.Now()),
	})
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", "building the egress proxy: "+err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	dec := json.NewDecoder(resp.Body)
	var buildErr, tail string
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if decErr := dec.Decode(&msg); decErr != nil {
			break
		}
		if s := strings.TrimSpace(msg.Stream); s != "" {
			tail = s
		}
		if msg.Error != "" {
			buildErr = msg.Error
		}
	}
	if buildErr != "" {
		return aferrors.Coded(aferrors.AFRUN040,
			"detail", "building the egress proxy: "+r.redactor.String(buildErr+" "+tail))
	}
	return nil
}

// copyInto writes a file into a container that is not running yet.
//
// Used for the policy, so that one sidecar image serves every environment on
// the machine and editing a rule rebuilds nothing. The container has to exist
// and must not have started, which is why the create and start are separated
// around this call.
func (r *Runtime) copyInto(ctx context.Context, id, path string, mode int64, body []byte) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{
		Name: strings.TrimPrefix(path, "/"), Mode: mode, Size: int64(len(body)),
		ModTime: r.clock.Now().UTC(), Format: tar.FormatPAX, Typeflag: tar.TypeReg,
	}); err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	if _, err := tw.Write(body); err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	if err := tw.Close(); err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	// Copied to the root, with the path carried in the header, because the
	// runtime image is scratch and has no directories to copy into.
	err := r.cli.CopyToContainer(ctx, id, "/", bytes.NewReader(buf.Bytes()),
		container.CopyToContainerOptions{})
	if err != nil {
		return aferrors.Wrap(err, aferrors.AFRUN040,
			"detail", "writing the policy into the proxy: "+err.Error())
	}
	return nil
}

// Decision is one line of the sidecar's decision log.
//
// It mirrors the record the sidecar writes. Every request produces one,
// allowed or not: a log that only records refusals answers "why was this
// blocked" and not "did anything reach Stripe", and the second question is the
// one somebody asks after an incident.
type Decision struct {
	Event    string    `json:"event"`
	At       time.Time `json:"-"`
	AtRaw    string    `json:"at"`
	Method   string    `json:"method"`
	Host     string    `json:"host"`
	Port     int       `json:"port"`
	Path     string    `json:"path"`
	TLS      bool      `json:"tls"`
	Mode     string    `json:"mode"`
	Rule     string    `json:"rule"`
	Reason   string    `json:"reason"`
	Allowed  bool      `json:"allowed"`
	Status   int       `json:"status"`
	Bytes    int64     `json:"bytes"`
	Duration string    `json:"duration"`
	Error    string    `json:"error"`
	Seq      uint64    `json:"seq"`
	Via      string    `json:"via"`
	HostOnly bool      `json:"host_only"`
	// Substituted marks a request whose credential the sidecar replaced on
	// the way out, so a reader can tell a sandbox call from a live one.
	Substituted bool `json:"substituted"`
}

// Decisions reads the sidecar's decision log for an environment.
//
// Read from the container's output rather than a mounted file, because a file
// needs a volume, a volume needs cleaning up, and a volume is one more thing
// that can outlive the environment.
func (r *Runtime) Decisions(ctx context.Context, envID string, limit int) ([]Decision, error) {
	if limit <= 0 {
		limit = 200
	}
	id := proxyName(envID)
	if _, err := r.cli.ContainerInspect(ctx, id); err != nil {
		if client.IsErrNotFound(err) {
			// Nothing running is not an error. Somebody asking what the
			// environment reached before bringing it up should be told that,
			// not handed a Docker error.
			return nil, nil
		}
		return nil, aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}

	rc, err := r.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(limit + 50),
	})
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	defer func() { _ = rc.Close() }()

	body, err := io.ReadAll(io.LimitReader(rc, 8<<20))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}

	var out []Decision
	for _, line := range strings.Split(stripDockerLogFraming(string(body)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var d Decision
		if err := json.Unmarshal([]byte(line), &d); err != nil {
			// A truncated trailing line is normal when the container is still
			// writing. Skipping it is right; failing on it would make the log
			// unreadable exactly while something is happening.
			continue
		}
		if d.Event != "decision" {
			continue
		}
		if t, parseErr := time.Parse(time.RFC3339Nano, d.AtRaw); parseErr == nil {
			d.At = t
		}
		out = append(out, d)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// Message is one message the sidecar captured instead of sending.
type Message struct {
	Event    string   `json:"event"`
	AtRaw    string   `json:"at"`
	Seq      uint64   `json:"seq"`
	Provider string   `json:"provider"`
	Kind     string   `json:"kind"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	Subject  string   `json:"subject"`
	Text     string   `json:"text"`
	HTML     string   `json:"html"`
	Links    []string `json:"links"`
	Code     string   `json:"code"`
	Host     string   `json:"host"`
	Path     string   `json:"path"`
}

// At returns the time the message was captured.
func (m Message) At() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, m.AtRaw)
	return t
}

// Recipient returns the first recipient, which is what a workflow waits on.
func (m Message) Recipient() string {
	if len(m.To) == 0 {
		return ""
	}
	return m.To[0]
}

// Link returns the most likely link in the message, which is what an agent
// following a magic link needs.
func (m Message) Link() string {
	if len(m.Links) == 0 {
		return ""
	}
	return m.Links[0]
}

// Messages reads what the sidecar captured for an environment.
func (r *Runtime) Messages(ctx context.Context, envID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 100
	}
	lines, err := r.sidecarLines(ctx, envID, limit*4)
	if err != nil {
		return nil, err
	}
	var out []Message
	for _, line := range lines {
		var m Message
		if err := json.Unmarshal([]byte(line), &m); err != nil || m.Event != "message" {
			continue
		}
		out = append(out, m)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// sidecarLines reads the tail of the sidecar's output.
func (r *Runtime) sidecarLines(ctx context.Context, envID string, tail int) ([]string, error) {
	id := proxyName(envID)
	if _, err := r.cli.ContainerInspect(ctx, id); err != nil {
		if client.IsErrNotFound(err) {
			// Nothing running is not an error. Somebody asking what arrived
			// before bringing the environment up should be told that, not
			// handed a Docker error.
			return nil, nil
		}
		return nil, aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	rc, err := r.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(tail),
	})
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	defer func() { _ = rc.Close() }()

	body, err := io.ReadAll(io.LimitReader(rc, 32<<20))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}
	var out []string
	for _, line := range strings.Split(stripDockerLogFraming(string(body)), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "{") {
			out = append(out, line)
		}
	}
	return out, nil
}

// WaitForMessage blocks until a message arrives that matches, or the deadline
// passes.
//
// The polling interval is short because a workflow is blocked on this: an
// agent that signed up is standing still until the welcome email arrives, and
// every second of interval is a second added to every test that waits.
func (r *Runtime) WaitForMessage(
	ctx context.Context, envID string, match func(Message) bool, timeout time.Duration,
) (Message, error) {
	deadline := r.clock.Now().Add(timeout)
	seen := uint64(0)
	if existing, err := r.Messages(ctx, envID, 200); err == nil {
		for _, m := range existing {
			if m.Seq > seen {
				seen = m.Seq
			}
			// Checked before waiting, because the message often arrived
			// before anybody started waiting for it. A wait that only ever
			// looks forward is how a test flakes on a fast machine.
			if match(m) {
				return m, nil
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return Message{}, ctx.Err()
		case <-r.clock.After(250 * time.Millisecond):
		}
		msgs, err := r.Messages(ctx, envID, 200)
		if err != nil {
			return Message{}, err
		}
		for _, m := range msgs {
			if m.Seq > seen && match(m) {
				return m, nil
			}
		}
		if !r.clock.Now().Before(deadline) {
			return Message{}, aferrors.Coded(aferrors.AFNET011,
				"match", "the filter", "timeout", timeout.Round(time.Second).String())
		}
	}
}

// Delivery is the result of sending a webhook into the environment.
type Delivery struct {
	// Service is the service that received it.
	Service string
	// URL is where it was delivered.
	URL string
	// Status is what the application answered.
	Status int
	// Body is the start of the response, which is where an application puts
	// the reason it rejected an event.
	Body string
	// Duration is how long the application took.
	Duration time.Duration
}

// Deliver posts a signed event to a service inside the environment.
//
// It goes through the service's published address rather than into the
// network, because that is the same path the provider's own callback would
// take and it exercises whatever sits in front of the handler.
func (r *Runtime) Deliver(
	ctx context.Context, envID, service, path string, body []byte, headers map[string]string,
) (Delivery, error) {
	env, err := r.Status(ctx, envID)
	if err != nil {
		return Delivery{}, err
	}
	var target provider.RunningService
	for _, s := range env.Services {
		if s.URL == "" {
			continue
		}
		if service == "" || s.Name == service {
			target = s
			break
		}
	}
	if target.URL == "" {
		return Delivery{}, aferrors.Coded(aferrors.AFNET012,
			"service", orAny(service), "detail", "no service in this environment is reachable")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	started := r.clock.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL+path, bytes.NewReader(body))
	if err != nil {
		return Delivery{}, aferrors.Wrap(err, aferrors.AFNET012,
			"service", target.Name, "detail", err.Error())
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return Delivery{}, aferrors.Wrap(err, aferrors.AFNET012,
			"service", target.Name, "detail", err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	// Bounded, because the interesting part of a rejection is the first line
	// and an application that answers with a whole HTML page should not fill
	// somebody's terminal with it.
	preview, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	return Delivery{
		Service: target.Name, URL: target.URL + path, Status: resp.StatusCode,
		Body:     strings.TrimSpace(r.redactor.String(string(preview))),
		Duration: r.clock.Since(started),
	}, nil
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}

// EnsureProxyImage builds the egress sidecar's image on the local Docker
// daemon if it is not already there, and returns its reference.
//
// Exported because the Kubernetes runtime needs the same image and cannot
// build it: it talks to an API server, not to a daemon, and the sidecar is
// compiled from source carried in this binary. So the engine builds it here,
// on the machine that ran af, and hands the reference to whichever runtime is
// going to place it. A cluster that has no access to this machine's daemon
// gets the reference from configuration instead, which is the same path an air
// gapped install uses.
func (r *Runtime) EnsureProxyImage(ctx context.Context, progress func(string)) (string, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if err := r.ensureProxyImage(ctx, progress); err != nil {
		return "", err
	}
	return proxyimage.Tag(), nil
}
