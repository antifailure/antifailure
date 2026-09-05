package local_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
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

// asked and skipped count how many tests wanted the daemon and how many did
// not get it. TestMain reads them.
var asked, skipped atomic.Int64

// silentFullSkip reports whether a run proved nothing and said so quietly.
//
// A pure function so it can be tested, which a TestMain cannot be. The
// condition is narrow on purpose: some tests skipping is a machine being slow
// on one operation, and no tests asking is a package with nothing to skip.
// Every test asking and every one skipping is the case where ok is a lie.
func silentFullSkip(asked, skipped int64, optedOut bool) bool {
	return !optedOut && asked > 0 && skipped == asked
}

// TestMain refuses a run in which every test that needed the daemon skipped.
//
// This package holds the containment suite, and containment is the property
// the product is sold on. A green check that never ran is worse than a red
// one, because the next person builds on it. Set AF_SKIP_DOCKER to say out
// loud that this machine has no daemon.
func TestMain(m *testing.M) {
	code := m.Run()

	// Leaks first, because a goroutine left behind only shows up once
	// everything has finished. A package gets one TestMain, so both checks
	// live here.
	if code == 0 {
		if err := goleak.Find(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}

	a, s := asked.Load(), skipped.Load()
	if a > 0 {
		fmt.Fprintf(os.Stderr, "local: %d of %d daemon tests ran\n", a-s, a)
	}
	if code == 0 && silentFullSkip(a, s, os.Getenv("AF_SKIP_DOCKER") != "") {
		fmt.Fprintf(os.Stderr,
			"local: every one of the %d daemon tests skipped, so this package proved "+
				"nothing about containment. Start Docker, or set AF_SKIP_DOCKER=1 to "+
				"accept that.\n", a)
		code = 1
	}
	os.Exit(code)
}

// The guard's own decision, checked directly. A TestMain cannot be tested, so
// the condition it acts on is a function that can be.
func TestSilentFullSkipOnlyFiresWhenNothingRan(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		asked, skipped int64
		optedOut       bool
		want           bool
	}{
		{"everything skipped", 7, 7, false, true},
		{"everything ran", 7, 0, false, false},
		{"some skipped", 7, 2, false, false},
		{"nothing asked", 0, 0, false, false},
		{"everything skipped, said out loud", 7, 7, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, silentFullSkip(c.asked, c.skipped, c.optedOut))
		})
	}
}

func requireRuntime(t *testing.T) *local.Runtime {
	t.Helper()
	return requireRuntimeWith(t, local.Options{})
}

// requireRuntimeWith is requireRuntime for a test that needs the runtime built
// differently, which is the port range and nothing else so far.
func requireRuntimeWith(t *testing.T, opts local.Options) *local.Runtime {
	t.Helper()
	asked.Add(1)
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		skipped.Add(1)
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	opts.Clock = clock.New()
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 90 * time.Second
	}
	r, err := local.New(opts)
	if err != nil {
		skipped.Add(1)
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := r.Inventory(ctx); err != nil {
		_ = r.Close()
		skipped.Add(1)
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

func TestUp_GivesTheMigrationItsOwnConnectionString(t *testing.T) {
	// An application should use a pooled endpoint; a migration must not,
	// because a transaction pooler does not support the session level features
	// migrations use, and the failure is a migration that half applies rather
	// than one that refuses.
	//
	// The interface documented that split from the start and nothing
	// implemented it: every service, and every migration, got the direct
	// string. This is where that becomes true rather than a comment.
	//
	// The migration exits non-zero on purpose. A migration container is
	// removed as soon as it finishes, so the only place its output survives is
	// the error a failure produces, and that is what this reads.
	r := requireRuntime(t)
	id := envID(t, r, "migrate-url")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Not real connection strings. What matters is which one arrives where.
	const forServices = "postgres://u:p@pooled.example/db"
	const forMigrations = "postgres://u:p@direct.example/db"

	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID:                id,
		DatabaseURL:          secrets.New(forServices),
		MigrationDatabaseURL: secrets.New(forMigrations),
		Services: []provider.ServiceSpec{{
			Name: "worker", Image: "alpine:3.20", Kind: "worker",
			Migrate: `echo "migrate saw $DATABASE_URL" >&2; exit 7`,
			Command: "sleep 60",
		}},
	})
	require.Error(t, err)
	// The password is gone by the time this reaches an error, because the
	// redactor runs on the way out. The host is the part under test.
	require.Contains(t, err.Error(), "migrate saw postgres://u:")
	require.Contains(t, err.Error(), "@direct.example/db")
	require.NotContains(t, err.Error(), "pooled.example",
		"the migration was given the pooled connection string")
	require.NotContains(t, err.Error(), ":p@", "the password reached an error message")
}

func TestUp_UsesOneConnectionStringWhenTheProviderHasNoPool(t *testing.T) {
	// The negative control for the split. A provider with no pooled endpoint
	// leaves MigrationDatabaseURL zero, and the migration must then get
	// DatabaseURL. Handing it an empty DATABASE_URL is the failure this
	// prevents, and an empty one would make the assertion below match a
	// prefix of nothing, so the value is checked and not just its presence.
	r := requireRuntime(t)
	id := envID(t, r, "migrate-url-one")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const only = "postgres://u:p@only.example/db"
	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID:       id,
		DatabaseURL: secrets.New(only),
		Services: []provider.ServiceSpec{{
			Name: "worker", Image: "alpine:3.20", Kind: "worker",
			Migrate: `echo "migrate saw [$DATABASE_URL]" >&2; exit 7`,
			Command: "sleep 60",
		}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "@only.example/db]",
		"the migration was given an empty DATABASE_URL")
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
//
// BOTH HOSTS ARE ONE ORGANISATION'S, AND THAT IS A DEPENDENCY WORTH SAYING OUT
// LOUD. example.com is a reserved domain ICANN and IANA operate, and
// www.iana.org is IANA's own site. So `engine`, a required context on every
// pull request in this repository, cannot go green without one organisation
// being up, and the two fixtures do not fail independently: an IANA outage
// moves the allowed host and the refused host together.
//
// Kept rather than changed, and the reasons are worth having written down.
// Both are chosen precisely because they are boring, stable and nobody's
// production service, and a refused host has to be one nothing in any policy
// here ever names. What the choice costs is measured elsewhere in this file:
// a guard that skips when either is unreachable, and, for the reachability
// assertions, a failure that reads the sidecar's own decision log rather than
// blaming containment for the network.
//
// If the coupling is ever worth removing, the change is to move refusedHost to
// a second organisation's domain, so the two fixtures fail independently. That
// is a decision about which third parties this repository's merge gate depends
// on, not a cleanup, which is why it is written here rather than done.
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

// requireInternet skips when the machine cannot reach what these tests depend
// on, so a laptop on a plane reports a skip rather than a failure that looks
// like a broken egress control.
//
// IT TAKES THE ORIGINS RATHER THAN ASSUMING THEM, and that is the whole point
// of the parameter. It used to probe `http://example.com/` unconditionally,
// while TestEgress_HTTPSIsDecidedByHost and five others need HTTPS. On
// 2026-09-05 `engine`, a required context, went red on a pull request touching
// no file under engine/: HTTP answered, so nothing skipped, and the HTTPS
// request then failed. A guard that checks a protocol the test does not use
// answers a question nobody asked.
//
// Pass every origin the test actually depends on, spelled the way the probe
// spells it, so a copy of the string is what keeps the two in step. A test that
// needs a host to be UP in order for its refusal to mean anything names that
// host too: an assertion that a request failed is satisfied by a host that is
// merely down, which is a pass for the wrong reason and nothing would say so.
//
// WHAT IT DELIBERATELY STILL DOES NOT DO. The probe is made from the test
// process; the test's request is made from inside a container, through the
// sidecar. Making the guard identical would mean building the environment to
// decide whether to run the test, and worse, it would make the guard
// indistinguishable from the assertion, so a real egress regression would
// report as a skip. The conformance harness states the same reasoning on its
// own copy. Closing the scheme gap does not close that one, and a probe that
// fails past the sidecar is told apart by unreachableIsOurs instead.
//
// The first origin is a plain parameter and not part of the variadic, so a call
// that guards on nothing does not compile. A runtime check would have been a
// check; this is the compiler.
func requireInternet(t *testing.T, origin string, more ...string) {
	t.Helper()
	for _, origin := range append([]string{origin}, more...) {
		if err := reachErr(origin); err != nil {
			// Counted as a skip for the same reason the daemon ones are. A
			// laptop with no network would otherwise run every containment
			// test, reach nothing, skip everything and print ok.
			skipped.Add(1)
			t.Skipf("skipped: %s is not reachable from this machine: %v", origin, err)
		}
	}
}

// reachErr is one GET, and nil when the origin answered at all.
//
// Its own transport, closed on the way out. The default one keeps the
// connection idle for a minute and a half, and its reader and writer goroutines
// are what goleak finds at the end of a run where this was the only outbound
// call anything made.
//
// The status is not read. This asks whether the machine can reach the host over
// this scheme, and a 500 from a host that answered is a reachable host.
func reachErr(origin string) error {
	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	c := &http.Client{Timeout: 10 * time.Second, Transport: tr}
	resp, err := c.Get(origin + "/")
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// requireGotOut asserts a probe reached a host the policy allows, and when it
// did not, says which of two very different things happened.
//
// `curl ... && exit 0 || exit 9` collapses "the sidecar refused a host the
// policy allows" and "the host did not answer" into one number, and the message
// beside it named the first. The sidecar has always known which: it writes one
// decision per request, allowed or not, and Runtime.Decisions reads them.
// TestCapture_SendsNothingToTheRealProvider in this file has asserted on that
// log since it was written. The reachability tests never asked.
//
// The rule is in unreachableIsOurs and it is proved there, on fabricated
// records, without a daemon. Only one of its six rows is the weather, and that
// row needs the sidecar's own log to say it allowed the connection AND this
// machine to be unable to reach the host at that moment. A sidecar that logs an
// allow and then drops the bytes still fails, because the machine outside it
// can still reach the host.
func requireGotOut(t *testing.T, r *local.Runtime, id, host, origin string, code int) {
	t.Helper()
	if code == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	decisions, err := r.Decisions(ctx, id, 200)
	require.NoError(t, err, "the probe exited %d and the sidecar's log could not be read", code)

	verdict, d := readVerdict(decisions, host)
	ours, message := unreachableIsOurs(verdict, d, host, origin, reachErr(origin) == nil)
	if ours {
		t.Fatalf("the probe exited %d and %s", code, message)
	}
	skipped.Add(1)
	t.Skipf("skipped: %s", message)
}

func TestEgress_AllowedHostIsReached(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t, "http://"+allowedHost)
	id := envID(t, r, "egallow")
	code := probe(t, r, id, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: allowedHost, Mode: schema.ModeAllow}},
	}, "wget -T 20 -q -O - http://"+allowedHost+"/ >/dev/null 2>&1 && exit 0 || exit 9")
	requireGotOut(t, r, id, allowedHost, "http://"+allowedHost, code)
	require.Equal(t, 0, code, "a host the policy allows was not reached through the proxy")
}

func TestEgress_HostWithNoRuleIsRefused(t *testing.T) {
	r := requireRuntime(t)
	// The refused host is named here as well as the allowed one, and that is
	// not belt and braces. The assertion below is that a request FAILED, and a
	// host that is merely down satisfies it: the test would pass, for entirely
	// the wrong reason, and nothing would say so. Guarding on it turns that
	// into a skip.
	requireInternet(t, "http://"+allowedHost, "http://"+refusedHost)
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
	requireInternet(t, "http://"+allowedHost, "http://"+refusedHost)
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
	allowedID := envID(t, r, "egnovars1")
	allowed := probe(t, r, allowedID, rules,
		noProxyVars+"wget -T 20 -q -O - http://"+allowedHost+"/ >/dev/null 2>&1 && exit 0 || exit 9")
	requireGotOut(t, r, allowedID, allowedHost, "http://"+allowedHost, allowed)
	require.Equal(t, 0, allowed,
		"a client with no proxy support could not reach a host the policy allows")

	refused := probe(t, r, envID(t, r, "egnovars2"), rules,
		noProxyVars+"wget -T 20 -q -O - http://"+refusedHost+"/ >/dev/null 2>&1 && exit 0 || exit 9")
	require.Equal(t, 9, refused,
		"a client with no proxy support reached a host the policy refuses")
}

func TestEgress_CannotBeBypassedByConnectingToAnAddress(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t, "http://"+allowedHost)
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
	// HTTPS, because that is what the probe below asks for. This guard read
	// http://example.com/ until 2026-09-05, when HTTP answered, nothing
	// skipped, and the HTTPS request failed into a required context.
	requireInternet(t, "https://"+allowedHost)
	img := curlImage(t)
	cmd := "curl -sS -o /dev/null --max-time 20 https://" + allowedHost + "/ && exit 0 || exit 9"

	// An HTTPS request arrives as a CONNECT, so only the host and port are
	// visible. That is enough for the decision that matters most, which is
	// whether the connection happens at all. A rule that names paths or
	// methods cannot be enforced on HTTPS until the environment certificate
	// lands, and that limit is stated in the docs rather than papered over.
	allowedID := envID(t, r, "eghttps1")
	allowed := probeWith(t, r, allowedID, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: allowedHost, Mode: schema.ModeAllow}},
	}, img, cmd)
	requireGotOut(t, r, allowedID, allowedHost, "https://"+allowedHost, allowed)
	require.Equal(t, 0, allowed, "an allowed host was not reachable over HTTPS")

	refused := probeWith(t, r, envID(t, r, "eghttps2"), &schema.Egress{
		Default: schema.ModeBlock,
	}, img, cmd)
	require.Equal(t, 9, refused, "a host with no rule was reachable over HTTPS")
}

func TestEgress_NoPolicyMeansBlockEverything(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t, "http://"+allowedHost)
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
	requireInternet(t, "https://"+allowedHost)
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
	id := envID(t, r, "insp1")
	code := probeInspected(t, r, id, rules, ca, img,
		"curl -sS -o /dev/null --max-time 25 https://"+allowedHost+"/ && exit 0 || exit 9")
	requireGotOut(t, r, id, allowedHost, "https://"+allowedHost, code)
	require.Equal(t, 0, code, "an inspected HTTPS request to an allowed path did not complete")
}

func TestEgress_HTTPSPathOutsideTheRuleIsRefused(t *testing.T) {
	r := requireRuntime(t)
	requireInternet(t, "https://"+allowedHost)
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
	requireInternet(t, "https://"+allowedHost)
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
	requireInternet(t, "https://"+allowedHost)
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
	requireInternet(t, "https://"+allowedHost)
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

func TestMock_RunsAStripeBillingFlowWithNoNetwork(t *testing.T) {
	r := requireRuntime(t)
	img := curlImage(t)
	id := envID(t, r, "mock1")

	ca, err := envcert.Generate("test-mock", time.Now())
	require.NoError(t, err)

	// No requireInternet: the point of mock mode is that this works with the
	// network unplugged. api.stripe.com is never contacted.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create a customer, read it back, subscribe, cancel. Each step writes its
	// status so the whole flow can be asserted from one exit code.
	script := strings.Join([]string{
		`set -e`,
		// Whitespace is stripped first, because the response is pretty printed
		// and sed works a line at a time.
		//
		// Match the id by its Stripe prefix rather than by the "id" key. A
		// subscription nests two more ids, on the subscription item and on its
		// price, and sed is greedy, so `.*"id":"` walks past the top level one
		// and captures the last. The price id is empty when the request names
		// no price, so the naive form silently extracted "" and the flow failed
		// three steps later at the subscription read. A prefix is also stable
		// against the pack nesting more objects later.
		`CUS=$(curl -sS --max-time 20 -X POST https://api.stripe.com/v1/customers ` +
			`-d 'email=buyer@example.test' | tr -d ' \n' | sed 's/.*"\(cus_[A-Za-z0-9]*\)".*/\1/')`,
		`echo "customer=$CUS"`,
		`case "$CUS" in cus_*) ;; *) echo "no customer id"; exit 21;; esac`,
		`curl -sS --max-time 20 https://api.stripe.com/v1/customers/$CUS | grep -q "$CUS" || exit 22`,
		`SUB=$(curl -sS --max-time 20 -X POST https://api.stripe.com/v1/subscriptions ` +
			`-d "customer=$CUS" | tr -d ' \n' | sed 's/.*"\(sub_[A-Za-z0-9]*\)".*/\1/')`,
		`case "$SUB" in sub_*) ;; *) echo "no subscription id"; exit 23;; esac`,
		`curl -sS --max-time 20 https://api.stripe.com/v1/subscriptions/$SUB | grep -q '"active"' || exit 24`,
		`curl -sS --max-time 20 -X DELETE https://api.stripe.com/v1/subscriptions/$SUB | grep -q '"canceled"' || exit 25`,
		`curl -sS --max-time 20 https://api.stripe.com/v1/subscriptions/$SUB | grep -q '"canceled"' || exit 26`,
		`exit 0`,
	}, "; ")

	code := probeInspected(t, r, id, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "api.stripe.com", Mode: schema.ModeMock}},
	}, ca, img, script)
	require.Equal(t, 0, code, "the billing flow did not complete against the mock pack")

	decisions, err := r.Decisions(ctx, id, 100)
	require.NoError(t, err)
	var mocked int
	for _, d := range decisions {
		if d.Mode == "mock" && d.Via == "inspect" {
			mocked++
			require.False(t, d.Allowed, "a mocked request is answered here and must not count as reaching out")
		}
	}
	require.GreaterOrEqual(t, mocked, 5, "every step of the flow is recorded")
}

func TestMock_AMissSaysWhatToAddRatherThanAnsweringEmptily(t *testing.T) {
	r := requireRuntime(t)
	img := curlImage(t)
	id := envID(t, r, "mock2")

	ca, err := envcert.Generate("test-mock2", time.Now())
	require.NoError(t, err)

	// An application that receives an empty 200 usually carries on and fails
	// somewhere unrelated, which is the failure this mode exists to avoid
	// rather than cause.
	code := probeInspected(t, r, id, &schema.Egress{
		Default: schema.ModeBlock,
		Rules:   []schema.EgressRule{{Host: "api.stripe.com", Mode: schema.ModeMock}},
	}, ca, img,
		`curl -sS --max-time 20 -o /tmp/b -w '%{http_code}' https://api.stripe.com/v1/nothing_like_this > /tmp/c; `+
			`grep -q 404 /tmp/c || exit 31; grep -q "no fixture" /tmp/b || exit 32; exit 0`)
	require.Equal(t, 0, code, "a mock miss must be a 404 that says what to add")
}

// TestUp_SurvivesAPortTakenBetweenTheReservationAndTheBind forces the race the
// port allocator documents.
//
// The allocator probes a port and hands it out, and the daemon binds it much
// later, after the service it forwards to has been built and started. Anything
// else on the machine can take it in that gap. Holding a listener on the
// reserved port for the whole of Up is that gap forced open: the reservation
// succeeded, and the bind cannot.
func TestUp_SurvivesAPortTakenBetweenTheReservationAndTheBind(t *testing.T) {
	r := requireRuntime(t)
	img := tinyWebImage(t, 8080, "survived the race")
	id := envID(t, r, "portrace")

	// An ephemeral port, so the number cannot collide with the range the
	// allocator hands out and the retry has somewhere to go.
	held, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = held.Close() }()
	taken := held.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	reserved := map[string]int{"web": taken}
	env, err := r.Up(ctx, provider.EnvSpec{
		EnvID:       id,
		PublicPorts: reserved,
		Services: []provider.ServiceSpec{{
			Name: "web", Image: img, Kind: "web", Port: 8080,
		}},
	})
	require.NoError(t, err, "a port taken before the bind must be retried, not reported")
	require.NotEmpty(t, env.URL())
	require.NotContains(t, env.URL(), strconv.Itoa(taken),
		"the environment must have moved off the port something else holds")
	require.Equal(t, "survived the race", get(t, env.URL()))

	// The reservation is corrected in place, which is how a service created
	// after this one is told the address that was bound rather than the one
	// that was asked for.
	require.Equal(t, portOf(t, env.URL()), reserved["web"])
}

// TestUp_PublishesInsideTheRangeAFPortRangeStartNames traces the variable from
// set to observably effective.
//
// It is not enough that the runtime reads it: `af doctor` names this variable
// in its remediation, and for a long time nothing anywhere read it, so a user
// who followed that advice moved nothing. The proof is the port the daemon
// actually published on.
func TestUp_PublishesInsideTheRangeAFPortRangeStartNames(t *testing.T) {
	const base = 44500
	r := requireRuntimeWith(t, local.Options{
		Getenv: func(k string) string {
			if k == "AF_PORT_RANGE_START" {
				return strconv.Itoa(base)
			}
			return ""
		},
	})
	img := tinyWebImage(t, 8080, "moved range")
	id := envID(t, r, "portrange")

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		Services: []provider.ServiceSpec{{
			Name: "web", Image: img, Kind: "web", Port: 8080,
		}},
	})
	require.NoError(t, err)
	require.Len(t, env.Services, 1)

	published := portOf(t, env.URL())
	// The allocator searches a span of 2000 from where it starts, and the
	// runtime publishes above the database provider's range rather than in it.
	from := base + local.PublishedPortOffset
	require.GreaterOrEqual(t, published, from)
	require.Less(t, published, from+2000)
	require.Equal(t, "moved range", get(t, env.URL()))
}

// portOf reads the port out of an environment URL.
func portOf(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	p, err := strconv.Atoi(u.Port())
	require.NoError(t, err, "the environment URL must carry a port: %s", raw)
	return p
}
