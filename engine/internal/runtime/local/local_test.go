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
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
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
	require.False(t, env.EgressAllowed)

	// Every resource was recorded before it was made, or teardown after a
	// crash has nothing to find.
	require.Contains(t, journaled, "network af-net-"+id)
	require.Contains(t, journaled, "network af-edge-"+id)
	require.Contains(t, journaled, "container af-ing-"+id+"-web")
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

// probeCommand exits 0 if the internet is reachable and 9 if it is not, so the
// result is an exit code rather than a judgement about how long to wait.
const probeCommand = "wget -T 12 -q -O - http://1.1.1.1/ >/dev/null 2>&1 && exit 0 || exit 9"

// runProbe brings up a one service environment whose service exits as soon as
// it knows the answer, and returns the code it exited with.
//
// Up reports an immediate exit as a startup failure, which is the right
// behaviour for a real service and is also exactly the signal wanted here, so
// the error is read rather than avoided.
func runProbe(t *testing.T, r *local.Runtime, id string, allowEgress bool) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id, AllowEgress: allowEgress,
		Services: []provider.ServiceSpec{{
			Name: "prober", Image: "alpine:3.20", Kind: "worker", Command: probeCommand,
		}},
	})
	// Two shapes, both legitimate. A sealed environment fails the connection
	// instantly, so the probe has already exited by the time Up checks and Up
	// reports a startup failure. An open one is still waiting on the request,
	// so Up succeeds and the answer arrives a moment later.
	if err != nil {
		var coded *aferrors.Error
		require.True(t, aferrors.As(err, &coded))
		require.Equal(t, aferrors.AFRUN005, coded.Code())
		return parseInt(t, codeInMessage, coded.Message())
	}

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := r.Status(context.Background(), id)
		require.NoError(t, statusErr)
		require.Len(t, status.Services, 1)
		if m := codeInStatus.FindStringSubmatch(status.Services[0].Detail); m != nil {
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

func TestUp_SealedNetworkHasNoRouteOut(t *testing.T) {
	r := requireRuntime(t)
	// The containment claim, measured rather than asserted. Turning off IP
	// masquerading looks like it should do this and does not: on Docker
	// Desktop the traffic is translated again by the virtual machine's own
	// gateway, and an environment built that way still reaches 1.1.1.1. This
	// test is what caught that.
	require.Equal(t, 9, runProbe(t, r, envID(t, r, "sealed1"), false),
		"a sealed environment reached the internet")
}

func TestUp_UnsealedNetworkDoesReachOut(t *testing.T) {
	r := requireRuntime(t)
	// The negative control. Without it the sealed test could pass for the
	// wrong reason: because the probe never worked at all, rather than because
	// the seal held.
	require.Equal(t, 0, runProbe(t, r, envID(t, r, "open1"), true),
		"an environment with egress allowed could not reach out, so the sealed test proves nothing")
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
	require.GreaterOrEqual(t, td.Removed, 4,
		"the service, its forwarder, and both networks")

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
