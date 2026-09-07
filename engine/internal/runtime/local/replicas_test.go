package local_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// The reason instance counts exist, demonstrated rather than described.
//
// `replicas` is not a scale knob. An environment is a copy of production on
// one laptop and nobody needs three copies of a worker for throughput there.
// What more than one instance buys is a class of bug that CANNOT be reproduced
// at one, and every test in this file is one member of that class: it passes
// at one instance and fails at three, on the same image, running the same
// command, with nothing changed but the number.
//
// That is also the honest way to read them. These tests assert that a
// deliberately wrong application is WRONG, which is a strange looking
// assertion until you notice what it is protecting: while the runtime started
// one container whatever the manifest said, every one of these applications
// passed, and passed in exactly the words a correct one would have used. A
// developer who wrote replicas: 3 precisely because they suspected one of
// these bugs got a green run and the conclusion that they were wrong about
// their own system.
//
// Two named classes here, and they are the two most expensive ones:
//
//  1. An unguarded side effect. A job that assumes it is the only copy of
//     itself does its work once per instance: three invoices, three charges,
//     three nightly emails. Leader election, a claim before the work, or an
//     idempotency key is what a correct application has and this one does not.
//
//  2. In process state that is assumed to be shared. A value cached in one
//     instance's memory is invisible to the other two, so the second request
//     from one user lands somewhere that has never heard of them. Sessions in
//     process memory, a warmed cache, a rate limiter counting in a local map,
//     and a websocket registry are all this bug.

// busyboxRef is the fixture. A public image with a shell rather than one built
// here, because nothing about these tests is about how an image is built and a
// build is the slowest thing this package does.
const busyboxRef = "busybox:1.36"

// requireBusybox makes the fixture image available, or skips.
//
// The local runtime never pulls: it runs images the engine built. This one is
// public, so it has to be on the daemon before a container can be made from
// it, and a create that fails with "No such image" says nothing about
// instances.
func requireBusybox(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := pullIfMissing(ctx, busyboxRef); err != nil {
		t.Skipf("skipped: %s could not be made available: %v", busyboxRef, err)
	}
}

// TestReplicas_AnUnguardedSideEffectRunsOncePerInstance is bug class one.
func TestReplicas_AnUnguardedSideEffectRunsOncePerInstance(t *testing.T) {
	r := requireRuntime(t)
	requireBusybox(t)

	// The nightly job, with no leader election and no claim before the work.
	// It charges the customer and then stays up, which is what a worker
	// process does between pieces of work.
	const charge = "charged customer 4310"
	job := provider.ServiceSpec{
		Name: "biller", Image: busyboxRef, Kind: "worker",
		Command: "echo '" + charge + "'; sleep 120",
	}

	t.Run("at one instance the customer is charged once", func(t *testing.T) {
		charges := chargesUnder(t, r, "repl1a", job, 0)
		// The green run. Nothing about this application is correct; it is
		// only unobserved, and this is the result its author would have taken
		// as proof that the bug they suspected is not there.
		require.Equal(t, 1, charges,
			"one instance of an unguarded job charges once, which is why the bug is invisible here")
	})

	t.Run("at three instances the customer is charged three times", func(t *testing.T) {
		charges := chargesUnder(t, r, "repl1b", job, 3)
		require.Equal(t, 3, charges,
			"three instances of a job with no leader election each did the work. If this "+
				"reports one, the runtime is running one container for a manifest that "+
				"asked for three and the bug is invisible again")
	})
}

// chargesUnder brings the job up with the given instance count and counts how
// many times it did its work.
func chargesUnder(t *testing.T, r *local.Runtime, name string, job provider.ServiceSpec, replicas int) int {
	t.Helper()
	id := envID(t, r, name)
	job.Replicas = replicas

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env, err := r.Up(ctx, provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{job}})
	require.NoError(t, err)
	require.Len(t, env.Services, 1)
	require.Equal(t, want(replicas), env.Services[0].Instances,
		"the runtime reports a different number of instances than it was asked for")

	// The count is read out of the logs the processes themselves wrote, not
	// out of anything the runtime says about itself. A runtime that reported
	// three and started one would pass every assertion above this line.
	//
	// Polled, because a container that has been created and started has not
	// necessarily reached its first line yet, and a count taken too early is
	// a smaller number for a reason that has nothing to do with instances.
	deadline := time.Now().Add(90 * time.Second)
	for {
		lines, err := r.Logs(ctx, id, "biller", 200)
		require.NoError(t, err)
		seen := 0
		for _, l := range lines {
			if strings.Contains(l.Text, "charged customer 4310") {
				seen++
			}
		}
		if seen >= want(replicas) || time.Now().After(deadline) {
			return seen
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestReplicas_InProcessStateIsNotSharedBetweenInstances is bug class two.
func TestReplicas_InProcessStateIsNotSharedBetweenInstances(t *testing.T) {
	r := requireRuntime(t)
	requireBusybox(t)

	// A web service holding a value in its own process and serving it. The
	// value is the container's hostname, which is the one thing that differs
	// between instances without the test having to inject anything: an
	// identity the application would have written into a session or a cache.
	web := provider.ServiceSpec{
		Name: "shop", Image: busyboxRef, Kind: "web", Port: 8080,
		Command: "mkdir -p /w && hostname > /w/index.html && exec httpd -f -p 8080 -h /w",
	}

	t.Run("at one instance every request sees the same state", func(t *testing.T) {
		bodies := bodiesUnder(t, r, "repl2a", web, 0)
		require.Len(t, bodies, 1,
			"one instance answers every request from one process, so the value it "+
				"cached is the value the next request finds. This is the environment "+
				"in which an application that keeps sessions in memory works perfectly")
	})

	t.Run("at three instances a request lands somewhere that never saw the write", func(t *testing.T) {
		bodies := bodiesUnder(t, r, "repl2b", web, 3)
		require.Greater(t, len(bodies), 1,
			"twenty requests through the environment's own address reached %d distinct "+
				"instance. Three instances behind one name means the second request "+
				"from a user does not have to reach the process that served the "+
				"first, and an application keeping their session in memory loses it. "+
				"If this reports one, either the runtime is running one container or "+
				"the forwarder is pinned to a single instance, and in both cases the "+
				"bug is invisible", len(bodies))
	})
}

// bodiesUnder brings the web service up with the given instance count and
// returns the distinct answers twenty requests got.
func bodiesUnder(t *testing.T, r *local.Runtime, name string, web provider.ServiceSpec, replicas int) map[string]bool {
	t.Helper()
	id := envID(t, r, name)
	web.Replicas = replicas

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	env, err := r.Up(ctx, provider.EnvSpec{EnvID: id, Services: []provider.ServiceSpec{web}})
	require.NoError(t, err)
	require.Len(t, env.Services, 1)
	require.Equal(t, want(replicas), env.Services[0].Instances)
	url := env.Services[0].URL
	require.NotEmpty(t, url, "the service has no address, so nothing can be asked of it")

	// Through the address a person would open, not into a container by id.
	// The question is what a user's second request sees, and a user's requests
	// go through the forwarder.
	seen := map[string]bool{}
	hc := &http.Client{Timeout: 10 * time.Second}
	for i := 0; i < 20; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)
		// Connection reuse would send every request down the one connection
		// the first one opened, which reaches one instance however many there
		// are. A browser does that too, and it is exactly why this bug
		// survives a developer clicking around: it appears on the second
		// visit, on a different tab, or after a timeout, rather than on the
		// second click.
		req.Close = true
		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if text := strings.TrimSpace(string(body)); text != "" {
			seen[text] = true
		}
	}
	require.NotEmpty(t, seen, "no request was answered at all, so nothing was measured")
	return seen
}

// want is the instance count a spec asking for replicas actually gets.
func want(replicas int) int {
	if replicas < 1 {
		return 1
	}
	return replicas
}

// TestReplicas_StatusCountsEveryInstanceItCanSee is the other half of the
// count, and the half that can rot quietly.
//
// Up reports what the runtime INTENDED: it returns the number it was asked
// for, having started that many. Status reports what the runtime can still
// FIND, by counting containers on the daemon. A service whose second instance
// died is the case where those two answers differ, and Status is the only one
// of the pair that notices.
func TestReplicas_StatusCountsEveryInstanceItCanSee(t *testing.T) {
	r := requireRuntime(t)
	requireBusybox(t)
	id := envID(t, r, "repl3")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	_, err := r.Up(ctx, provider.EnvSpec{
		EnvID: id,
		Services: []provider.ServiceSpec{
			{Name: "many", Image: busyboxRef, Kind: "worker", Command: "sleep 120", Replicas: 3},
			// Declared alongside and asking for nothing, so that a runtime
			// counting three of everything would fail here. Without it,
			// "three instances" is not a choice the runtime made.
			{Name: "one", Image: busyboxRef, Kind: "worker", Command: "sleep 120"},
		},
	})
	require.NoError(t, err)

	env, err := r.Status(ctx, id)
	require.NoError(t, err)
	counts := map[string]int{}
	for _, s := range env.Services {
		counts[s.Name] = s.Instances
	}
	require.Equal(t, map[string]int{"many": 3, "one": 1}, counts,
		"Status reports one entry per service with the number of containers behind it. "+
			"A service reported once for each container would make af status disagree "+
			"with the manifest, and a service reported without a count would make three "+
			"asked for and one running read exactly like three running")
	require.Len(t, env.Services, 2,
		"one entry per service, not one per container")
}
