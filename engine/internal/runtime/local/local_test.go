package local_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/antifailure/antifailure/engine/internal/build"
	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/envcert"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func requireRuntime(t *testing.T) *local.Runtime {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	r, err := local.New(local.Options{Clock: clock.New(), ReadyTimeout: 90 * time.Second})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.Inventory(ctx); err != nil {
		_ = r.Close()
		t.Skipf("skipped: the Docker daemon did not respond: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// envID gives each test its own environment, and guarantees teardown even when
// the assertion under test is what failed.
func envID(t *testing.T, r *local.Runtime, name string) string {
	t.Helper()
	id := "test" + name
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_, _ = r.Down(ctx, id)
	})
	return id
}

// tinyWebImage builds an image that serves one line over HTTP, using only
// busybox, so the test does not depend on a language runtime being pullable.
func tinyWebImage(t *testing.T, port int, body string) string {
	t.Helper()
	b, err := build.NewDockerBuilder(build.DockerOptions{Clock: clock.New()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
while true; do
  printf 'HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s' | nc -l -p %d -s 0.0.0.0
done
`, len(body), body, port)
	require.NoError(t, os.WriteFile(dir+"/serve.sh", []byte(script), 0o755))
	require.NoError(t, os.WriteFile(dir+"/Dockerfile", []byte(
		"FROM alpine:3.20\nCOPY serve.sh /serve.sh\nCMD [\"/bin/sh\", \"/serve.sh\"]\n"), 0o644))

	c, err := build.NewContext(build.ContextOptions{Root: dir, Service: "web"})
	require.NoError(t, err)
	req := build.Request{Service: "web", Context: c, EnvID: "test-fixture"}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	res, err := b.Build(ctx, req)
	require.NoError(t, err, "the fixture image must build")
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		cli, err := dockerutil.Client()
		if err != nil {
			return
		}
		defer func() { _ = cli.Close() }()
		_, _ = cli.ImageRemove(c, res.ImageRef, image.RemoveOptions{Force: true, PruneChildren: true})
	})
	return res.ImageRef
}

func TestUp_BringsAWebServiceUpAndReachable(t *testing.T) {
	r := requireRuntime(t)
	img := tinyWebImage(t, 8080, "hello from the environment")
	id := envID(t, r, "web1")

	var journaled []string
	var progress []string
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id, Branch: "feature/x",
		Services: []provider.ServiceSpec{{
			Name: "web", Image: img, Kind: "web", Port: 8080,
		}},
		Journal:  func(kind, resID string) error { journaled = append(journaled, kind+" "+resID); return nil },
		Progress: func(l string) { progress = append(progress, l) },
	})
	require.NoError(t, err)
	require.Len(t, env.Services, 1)
	require.True(t, env.Services[0].Ready)
	require.NotEmpty(t, env.URL())
	require.True(t, env.ProxyReady)

	// Every resource was recorded before it was made, or teardown after a
	// crash has nothing to find.
	require.Contains(t, journaled, "network af-net-"+id)
	require.Contains(t, journaled, "network af-edge-"+id)
	require.Contains(t, journaled, "container af-ing-"+id+"-web")
	require.Contains(t, journaled, "container af-proxy-"+id)
	require.Contains(t, journaled, "container af-svc-"+id+"-web")
	require.NotEmpty(t, progress)

	body := get(t, env.URL())
	require.Equal(t, "hello from the environment", body)
}

func TestUp_IsIdempotentForTheSameEnvironment(t *testing.T) {
	r := requireRuntime(t)
	img := tinyWebImage(t, 8080, "idempotent")
	id := envID(t, r, "idem1")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	spec := provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{{
		Name: "web", Image: img, Kind: "web", Port: 8080,
	}}}
	first, err := r.Up(ctx, spec)
	require.NoError(t, err)

	// The second Up finds the network the first made rather than creating a
	// duplicate that quietly divides the services between two of them.
	second, err := r.Up(ctx, spec)
	if err != nil {
		require.Contains(t, err.Error(), "already", "a repeat Up must not create a second network")
	}
	require.Equal(t, first.NetworkID, second.NetworkID)
}

func TestUp_ReportsAServiceThatExitsImmediately(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "crash1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Without the running check, the readiness loop would poll a dead
	// container for its whole timeout and then report a timeout, which sends
	// somebody looking at the network instead of at their own crash.
	env, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		Services: []provider.ServiceSpec{{
			Name: "web", Image: "alpine:3.20", Kind: "web", Port: 8080,
			Command: "echo the-reason-it-died >&2; exit 7",
		}},
	})
	require.Error(t, err)

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Equal(t, aferrors.AFRUN005, coded.Code())
	require.Contains(t, coded.Message(), "7", "the exit code is named")
	require.Len(t, env.Services, 1)
	require.Contains(t, env.Services[0].Detail, "the-reason-it-died",
		"the log is the only thing that explains the failure, so it comes back with it")
}

func TestUp_RunsMigrationsBeforeTheService(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "migrate1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var progress []string
	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		Services: []provider.ServiceSpec{{
			Name: "worker", Image: "alpine:3.20", Kind: "worker",
			Migrate: "echo migrating; exit 0",
			Command: "sleep 60",
		}},
		Progress: func(l string) { progress = append(progress, l) },
	})
	require.NoError(t, err)
	require.Contains(t, strings.Join(progress, "\n"), "running migrations")
}

func TestUp_StopsWhenAMigrationFails(t *testing.T) {
	r := requireRuntime(t)
	id := envID(t, r, "migrate2")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// A service started against a half migrated database produces failures
	// that look like application bugs, so the migration failing has to stop
	// the environment rather than be logged and stepped over.
	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		Services: []provider.ServiceSpec{{
			Name: "web", Image: "alpine:3.20", Kind: "web", Port: 8080,
			Migrate: "echo the-migration-failed >&2; exit 4",
			Command: "sleep 60",
		}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "migration")

	// And nothing was left running.
	status, err := r.Status(context.Background(), id)
	require.NoError(t, err)
	for _, s := range status.Services {
		require.NotEqual(t, "running", s.State, "%s was left running after a failed migration", s.Name)
	}
}

// The egress tests use a host that is reachable, a host that is not in any
// policy, and a direct connection that bypasses the proxy entirely. Together
// they answer the only question that matters about an egress control: does it
// decide, and can it be walked around.
const (
	allowedHost = "example.com"
	refusedHost = "www.iana.org"
)

// curlImage is alpine with curl, built once.
//
// busybox wget cannot be used for the HTTPS tests: it reads https_proxy but
// does not issue a CONNECT, so it fails against any proxy rather than against
// this one. Every real client does issue one, which the manual check against
// this proxy confirms, so the test uses a client that behaves like they do.
func curlImage(t *testing.T) string {
	t.Helper()
	b, err := build.NewDockerBuilder(build.DockerOptions{Clock: clock.New()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/Dockerfile",
		[]byte("FROM alpine:3.20\nRUN apk add --no-cache curl\n"), 0o644))

	c, err := build.NewContext(build.ContextOptions{Root: dir, Service: "curl"})
	require.NoError(t, err)
	req := build.Request{Service: "curl", Context: c, EnvID: "test-fixture"}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	res, err := b.Build(ctx, req)
	require.NoError(t, err)
	return res.ImageRef
}

// probe runs one outbound attempt and returns its exit code.
//
// Exit 0 means the request completed. Anything else means it did not, and the
// command is written so that the two are distinguishable without parsing
// output, which busybox wget writes to stderr in a format that varies.
func probe(t *testing.T, r *local.Runtime, id string, egress *schema.Egress, command string) int {
	t.Helper()
	return probeWith(t, r, id, egress, "alpine:3.20", command)
}

func probeWith(t *testing.T, r *local.Runtime, id string, egress *schema.Egress, image, command string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id, Egress: egress,
		Services: []provider.ServiceSpec{{
			Name: "prober", Image: image, Kind: "worker", Command: command,
		}},
	})
	if err != nil {
		var coded *aferrors.Error
		require.True(t, aferrors.As(err, &coded), "unexpected failure: %v", err)
		require.Equal(t, aferrors.AFRUN005, coded.Code(), coded.Message())
		return parseInt(t, codeInMessage, coded.Message())
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := r.Status(context.Background(), id)
		require.NoError(t, statusErr)
		require.Len(t, status.Services, 1)
		if codeInStatus.MatchString(status.Services[0].Detail) {
			return parseInt(t, codeInStatus, status.Services[0].Detail)
		}
		time.Sleep(time.Second)
	}
	t.Fatal("the probe never finished")
	return -1
}

var (
	codeInMessage = regexp.MustCompile(`code (\d+)`)
	codeInStatus  = regexp.MustCompile(`Exited \((\d+)\)`)
)

func parseInt(t *testing.T, re *regexp.Regexp, s string) int {
	t.Helper()
	m := re.FindStringSubmatch(s)
	require.NotNil(t, m, "no exit code in %q", s)
	n, err := strconv.Atoi(m[1])
	require.NoError(t, err)
	return n
}

// requireInternet skips when the machine cannot reach the hosts these tests
// decide about, so a laptop on a plane reports a skip rather than a failure
// that looks like a broken egress control.
func requireInternet(t *testing.T) {
	t.Helper()
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get("http://" + allowedHost + "/")
	if err != nil {
		t.Skipf("skipped: %s is not reachable from this machine: %v", allowedHost, err)
	}
	_ = resp.Body.Close()
}

func TestEgress_AllowedHostIsReached(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	code := probe(t, r, envID(t, r, "egallow"), &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: allowedHost, Mode: schema.ModeAllow}},
	}, "wget -T 20 -q -O - http://"+allowedHost+"/ >/dev/null 2>&1 && exit 0 || exit 9")
	require.Equal(t, 0, code, "a host the policy allows was not reached through the proxy")
}

func TestEgress_HostWithNoRuleIsRefused(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	// The default is block, and the point of this test is that the refusal
	// comes from the policy rather than from the host being unreachable, which
	// is why the allowed host in the test above is a real one too.
	code := probe(t, r, envID(t, r, "egblock"), &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: allowedHost, Mode: schema.ModeAllow}},
	}, "wget -T 20 -q -O - http://"+refusedHost+"/ >/dev/null 2>&1 && exit 0 || exit 9")
	require.Equal(t, 9, code, "a host with no rule was reached")
}

// noProxyVars strips the proxy variables, so the request that follows is what
// a client with no proxy support makes.
const noProxyVars = "env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY "

func TestEgress_AppliesToAClientThatIgnoresProxyVariables(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	// The property the whole design rests on. Proxy variables are a request,
	// and a great many clients ignore them: Node has no proxy support at all,
	// and plenty of SDKs bundle a client that does the same. An egress control
	// that only works for clients that agreed to it is not a control.
	//
	// So the decision does not depend on them. Every external name resolves to
	// the sidecar, and the sidecar decides the connection that follows.
	rules := &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: allowedHost, Mode: schema.ModeAllow}},
	}
	allowed := probe(t, r, envID(t, r, "egnovars1"), rules,
		noProxyVars+"wget -T 20 -q -O - http://"+allowedHost+"/ >/dev/null 2>&1 && exit 0 || exit 9")
	require.Equal(t, 0, allowed,
		"a client with no proxy support could not reach a host the policy allows")

	refused := probe(t, r, envID(t, r, "egnovars2"), rules,
		noProxyVars+"wget -T 20 -q -O - http://"+refusedHost+"/ >/dev/null 2>&1 && exit 0 || exit 9")
	require.Equal(t, 9, refused,
		"a client with no proxy support reached a host the policy refuses")
}

func TestEgress_CannotBeBypassedByConnectingToAnAddress(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	// Interception happens through DNS, so the obvious way around it is to
	// skip DNS. That has to fail, and it does for a reason that has nothing to
	// do with DNS: the service's network has no route out, so a packet
	// addressed straight at the internet has nowhere to go.
	code := probe(t, r, envID(t, r, "egraw"), &schema.Egress{
		Default: schema.ModeAllow,
	}, noProxyVars+"wget -T 15 -q -O - http://1.1.1.1/ >/dev/null 2>&1 && exit 0 || exit 9")
	require.Equal(t, 9, code,
		"a service reached the internet by address, going around the sidecar entirely")
}

func TestEgress_HTTPSIsDecidedByHost(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	img := curlImage(t)
	cmd := "curl -sS -o /dev/null --max-time 20 https://" + allowedHost + "/ && exit 0 || exit 9"

	// An HTTPS request arrives as a CONNECT, so only the host and port are
	// visible. That is enough for the decision that matters most, which is
	// whether the connection happens at all. A rule that names paths or
	// methods cannot be enforced on HTTPS until the environment certificate
	// lands, and that limit is stated in the docs rather than papered over.
	allowed := probeWith(t, r, envID(t, r, "eghttps1"), &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: allowedHost, Mode: schema.ModeAllow}},
	}, img, cmd)
	require.Equal(t, 0, allowed, "an allowed host was not reachable over HTTPS")

	refused := probeWith(t, r, envID(t, r, "eghttps2"), &schema.Egress{
		Default: schema.ModeBlock,
	}, img, cmd)
	require.Equal(t, 9, refused, "a host with no rule was reachable over HTTPS")
}

func TestEgress_NoPolicyMeansBlockEverything(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	// A manifest with no egress section is valid, and it must not mean the
	// sidecar fails to parse its policy and the environment fails to start.
	code := probe(t, r, envID(t, r, "egnone"), nil,
		"wget -T 20 -q -O - http://"+allowedHost+"/ >/dev/null 2>&1 && exit 0 || exit 9")
	require.Equal(t, 9, code)
}

func TestDown_RemovesEverythingAndSaysWhatItRemoved(t *testing.T) {
	r := requireRuntime(t)
	img := tinyWebImage(t, 8080, "down")
	id := envID(t, r, "down1")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	_, err := r.Up(ctx, provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{{
		Name: "web", Image: img, Kind: "web", Port: 8080,
	}}})
	require.NoError(t, err)

	td, err := r.Down(ctx, id)
	require.NoError(t, err)
	require.Empty(t, td.Pending, "nothing may be left pending")
	require.GreaterOrEqual(t, td.Removed, 5,
		"the service, its forwarder, the egress proxy, and both networks")

	status, err := r.Status(ctx, id)
	require.NoError(t, err)
	require.Empty(t, status.Services)
	require.Empty(t, status.NetworkID)
}

func TestDown_OfSomethingThatWasNeverUpSucceeds(t *testing.T) {
	r := requireRuntime(t)
	// Teardown runs after crashes, so "there was nothing there" is the normal
	// case rather than an error.
	td, err := r.Down(context.Background(), "testneverexisted")
	require.NoError(t, err)
	require.Zero(t, td.Removed)
	require.Empty(t, td.Pending)
}

func TestDown_TouchesOnlyItsOwnEnvironment(t *testing.T) {
	r := requireRuntime(t)
	img := tinyWebImage(t, 8080, "isolated")
	keep := envID(t, r, "keep1")
	remove := envID(t, r, "remove1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for _, id := range []string{keep, remove} {
		_, err := r.Up(ctx, provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{{
			Name: "web", Image: img, Kind: "web", Port: 8080,
		}}})
		require.NoError(t, err)
	}

	_, err := r.Down(ctx, remove)
	require.NoError(t, err)

	survived, err := r.Status(ctx, keep)
	require.NoError(t, err)
	require.Len(t, survived.Services, 1, "another environment must be untouched")
	require.Equal(t, "running", survived.Services[0].State)
}

func TestUp_RefusesAnEnvironmentWithNoID(t *testing.T) {
	r := requireRuntime(t)
	_, err := r.Up(context.Background(), provider.EnvSpec{})
	require.Error(t, err)
	_, err = r.Down(context.Background(), "")
	require.Error(t, err)
}

func TestInventory_ListsWhatTheRuntimeHolds(t *testing.T) {
	r := requireRuntime(t)
	img := tinyWebImage(t, 8080, "inventory")
	id := envID(t, r, "inv1")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	_, err := r.Up(ctx, provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{{
		Name: "web", Image: img, Kind: "web", Port: 8080,
	}}})
	require.NoError(t, err)

	items, err := r.Inventory(ctx)
	require.NoError(t, err)

	var kinds []string
	for _, it := range items {
		if it.EnvID == id {
			kinds = append(kinds, it.Kind)
		}
	}
	require.Contains(t, kinds, "container/service")
	require.Contains(t, kinds, "network",
		"a resource the leak detector cannot see is a resource that leaks")
}

func get(t *testing.T, url string) string {
	t.Helper()
	var last error
	for i := 0; i < 20; i++ {
		resp, err := http.Get(url)
		if err != nil {
			last = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		require.NoError(t, readErr)
		return strings.TrimSpace(string(body))
	}
	t.Fatalf("GET %s never answered: %v", url, last)
	return ""
}

func TestEgress_HTTPSPathRulesAreEnforcedWhenTheCertificateIsIssued(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	img := curlImage(t)

	// The thing a host level tunnel cannot do. A rule naming a path needs the
	// path, and the path is inside TLS, so the connection has to be terminated
	// with a certificate the environment trusts.
	ca, err := envcert.Generate("test-inspect", time.Now())
	require.NoError(t, err)

	rules := &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{
			{Host: allowedHost, Mode: schema.ModeAllow, Paths: []string{"/"}},
		},
	}
	// The rule allows every path under root, so this is the positive case and
	// proves the certificate is trusted and the request is re-originated.
	code := probeInspected(t, r, envID(t, r, "insp1"), rules, ca, img,
		"curl -sS -o /dev/null --max-time 25 https://"+allowedHost+"/ && exit 0 || exit 9")
	require.Equal(t, 0, code, "an inspected HTTPS request to an allowed path did not complete")
}

func TestEgress_HTTPSPathOutsideTheRuleIsRefused(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	img := curlImage(t)

	ca, err := envcert.Generate("test-inspect2", time.Now())
	require.NoError(t, err)

	// A path the rule does not cover. Over a tunnel this would have been
	// allowed, because a tunnel only ever sees the host.
	rules := &schema.Egress{
		Default: schema.ModeBlock,
		Rules: []schema.EgressRule{
			{Host: allowedHost, Mode: schema.ModeAllow, Paths: []string{"/allowed-prefix"}},
		},
	}
	code := probeInspected(t, r, envID(t, r, "insp2"), rules, ca, img,
		"curl -sS -o /dev/null --max-time 25 --fail https://"+allowedHost+"/ && exit 0 || exit 9")
	require.Equal(t, 9, code,
		"an HTTPS request to a path outside the rule was allowed, which is what a tunnel does")
}

func probeInspected(
	t *testing.T, r *local.Runtime, id string, egress *schema.Egress,
	ca *envcert.Authority, image, command string,
) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id, Egress: egress,
		CACertPEM: ca.CertPEM, CAKeyPEM: ca.KeyPEM,
		Services: []provider.ServiceSpec{{
			Name: "prober", Image: image, Kind: "worker", Command: command,
		}},
	})
	if err != nil {
		var coded *aferrors.Error
		require.True(t, aferrors.As(err, &coded), "unexpected failure: %v", err)
		require.Equal(t, aferrors.AFRUN005, coded.Code(), coded.Message())
		return parseInt(t, codeInMessage, coded.Message())
	}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := r.Status(context.Background(), id)
		require.NoError(t, statusErr)
		require.Len(t, status.Services, 1)
		if codeInStatus.MatchString(status.Services[0].Detail) {
			return parseInt(t, codeInStatus, status.Services[0].Detail)
		}
		time.Sleep(time.Second)
	}
	t.Fatal("the probe never finished")
	return -1
}

func TestCapture_RecordsAMessageAndAnswersAsTheProviderWould(t *testing.T) {
	r := requireRuntime(t)
	img := curlImage(t)
	id := envID(t, r, "capture1")

	ca, err := envcert.Generate("test-capture", time.Now())
	require.NoError(t, err)

	// The mode that lets an agent finish a sign up. Nobody receives the mail,
	// the application's own error handling never fires because the response is
	// the shape its client expects, and the message is readable afterwards.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	payload := `{"from":"hello@shop.test","to":["someone@example.test"],` +
		`"subject":"Confirm your email",` +
		`"html":"<a href=\"https://shop.test/verify?token=Zq81LmPPqrs82kdlxx\">Confirm</a> code 481920"}`

	_, err = r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		Egress: &schema.Egress{
			Default: schema.ModeBlock,
			Rules:   []schema.EgressRule{{Host: "api.resend.com", Mode: schema.ModeCapture}},
		},
		CACertPEM: ca.CertPEM, CAKeyPEM: ca.KeyPEM,
		Services: []provider.ServiceSpec{{
			Name: "sender", Image: img, Kind: "worker",
			Command: `curl -sS --max-time 25 -X POST https://api.resend.com/emails ` +
				`-H 'content-type: application/json' -d '` + payload + `' > /tmp/out; sleep 45`,
		}},
	})
	require.NoError(t, err)

	msg, err := r.WaitForMessage(ctx, id, func(m local.Message) bool {
		return m.Subject == "Confirm your email"
	}, 60*time.Second)
	require.NoError(t, err)

	require.Equal(t, "resend", msg.Provider)
	require.Equal(t, "email", msg.Kind)
	require.Equal(t, "someone@example.test", msg.Recipient())
	require.Equal(t, "481920", msg.Code, "the code an agent has to type is extracted")
	require.Equal(t, "https://shop.test/verify?token=Zq81LmPPqrs82kdlxx", msg.Link())
}

func TestCapture_SendsNothingToTheRealProvider(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	img := curlImage(t)
	id := envID(t, r, "capture2")

	ca, err := envcert.Generate("test-capture2", time.Now())
	require.NoError(t, err)

	// The property that matters most about capture, checked rather than
	// assumed: the request never leaves. A rule that recorded the message and
	// forwarded it anyway would look identical from inside the environment and
	// would email a real customer.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	_, err = r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		Egress: &schema.Egress{
			Default: schema.ModeBlock,
			Rules:   []schema.EgressRule{{Host: allowedHost, Mode: schema.ModeCapture}},
		},
		CACertPEM: ca.CertPEM, CAKeyPEM: ca.KeyPEM,
		Services: []provider.ServiceSpec{{
			Name: "sender", Image: img, Kind: "worker",
			Command: `curl -sS --max-time 25 -X POST https://` + allowedHost + `/anything ` +
				`-d 'subject=captured' -o /tmp/body; sleep 45`,
		}},
	})
	require.NoError(t, err)

	_, err = r.WaitForMessage(ctx, id, func(m local.Message) bool { return true }, 60*time.Second)
	require.NoError(t, err, "the request was not captured")

	decisions, err := r.Decisions(ctx, id, 100)
	require.NoError(t, err)
	var sawCapture bool
	for _, d := range decisions {
		if d.Host == allowedHost && d.Mode == "capture" {
			sawCapture = true
			require.False(t, d.Allowed,
				"a captured request is answered inside the environment and must not count as reaching out")
		}
	}
	require.True(t, sawCapture, "the decision log does not show the capture")
}

// fakeLiveKey builds a credential that looks live without any string in this
// repository looking like one.
func fakeLiveKey() string {
	return "sk" + "_" + "live" + "_" + strings.Repeat("A1b2C3d4", 3)
}

func TestTripwire_RefusesARequestCarryingALiveCredential(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	img := curlImage(t)
	id := envID(t, r, "tripwire1")

	ca, err := envcert.Generate("test-tripwire", time.Now())
	require.NoError(t, err)

	// The host is allowed. The request is still refused, because an
	// environment that holds a copy of production data and runs unreviewed
	// code must never carry a credential that can act on production. A live
	// Stripe key here is a real charge on a real card.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	_, err = r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		Egress: &schema.Egress{
			Default: schema.ModeBlock,
			Rules:   []schema.EgressRule{{Host: allowedHost, Mode: schema.ModeAllow, Paths: []string{"/"}}},
		},
		CACertPEM: ca.CertPEM, CAKeyPEM: ca.KeyPEM,
		Services: []provider.ServiceSpec{{
			Name: "caller", Image: img, Kind: "worker",
			Command: "curl -sS --max-time 25 -o /tmp/body -w '%{http_code}' " +
				"-H 'authorization: Bearer " + fakeLiveKey() + "' " +
				"https://" + allowedHost + "/ > /tmp/code; sleep 60",
		}},
	})
	require.NoError(t, err)

	deadline := time.Now().Add(60 * time.Second)
	var refused bool
	for time.Now().Before(deadline) && !refused {
		decisions, decErr := r.Decisions(ctx, id, 100)
		require.NoError(t, decErr)
		for _, d := range decisions {
			if strings.Contains(d.Reason, "live credential") {
				refused = true
				require.False(t, d.Allowed)
				require.Equal(t, 403, d.Status)
				// The message says what kind and where, and never what.
				require.Contains(t, d.Reason, "Stripe secret key")
				require.NotContains(t, d.Reason, "A1b2C3d4")
			}
		}
		if !refused {
			time.Sleep(time.Second)
		}
	}
	require.True(t, refused, "a request carrying a live credential was not refused")
}

func TestSandbox_SubstitutesTheCredentialOnTheWayOut(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t)
	img := curlImage(t)
	id := envID(t, r, "sandbox1")

	ca, err := envcert.Generate("test-sandbox", time.Now())
	require.NoError(t, err)

	// An application configured with a sandbox key is an application somebody
	// can misconfigure. Replacing it at the boundary is a mistake nobody can
	// make, because whatever the application sent is discarded before the
	// request leaves.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	_, err = r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		Egress: &schema.Egress{
			Default: schema.ModeBlock,
			Rules: []schema.EgressRule{{
				Host: allowedHost, Mode: schema.ModeSandbox, Credential: "TEST_KEY",
			}},
		},
		SandboxCredentials: map[string]secrets.Value{
			"TEST_KEY": secrets.New("sk" + "_" + "test" + "_" + "substituted00000000"),
		},
		CACertPEM: ca.CertPEM, CAKeyPEM: ca.KeyPEM,
		Services: []provider.ServiceSpec{{
			Name: "caller", Image: img, Kind: "worker",
			Command: "curl -sS --max-time 25 -o /dev/null " +
				"-H 'authorization: Bearer whatever-the-app-had' " +
				"https://" + allowedHost + "/; sleep 60",
		}},
	})
	require.NoError(t, err)

	deadline := time.Now().Add(60 * time.Second)
	var seen bool
	for time.Now().Before(deadline) && !seen {
		decisions, decErr := r.Decisions(ctx, id, 100)
		require.NoError(t, decErr)
		for _, d := range decisions {
			// The inspected record, not the CONNECT that opened the tunnel.
			// The tunnel decision is host level and carries no request, so it
			// cannot have substituted anything.
			if d.Mode == "sandbox" && d.Host == allowedHost && d.Via == "inspect" {
				seen = true
				require.True(t, d.Allowed, "sandbox reaches the provider's sandbox")
				if !d.Substituted {
					t.Logf("decision without substitution: %+v", d)
				}
				require.True(t, d.Substituted,
					"the credential was not replaced, so the application's own would have gone out")
			}
		}
		if !seen {
			time.Sleep(time.Second)
		}
	}
	if !seen {
		decisions, _ := r.Decisions(ctx, id, 100)
		for _, d := range decisions {
			t.Logf("decision: mode=%s host=%s status=%d allowed=%v substituted=%v via=%s reason=%s",
				d.Mode, d.Host, d.Status, d.Allowed, d.Substituted, d.Via, d.Reason)
		}
	}
	require.True(t, seen, "no sandbox decision was recorded")
}
