package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
)

// These drive createLocalServer against a daemon that is not Docker, because
// the defect they hold is a RACE, and a race against a real daemon is a test
// that passes on every machine except the one where it matters. The fake loses
// the race on purpose, every time, which is the only way to prove what happens
// after it.
//
// The live suite beside this still runs against a real ClickHouse in CI. These
// do not replace it. They cover the one thing it cannot reach on demand: the
// moment a port the allocator called free turns out to be held by somebody
// else, which on main was 2 of 25 runs and poisoned all ten live tests each
// time.

// fakeDaemon answers the four calls createLocalServer makes, and nothing else.
type fakeDaemon struct {
	t *testing.T

	mu sync.Mutex
	// lose is how many distinct host ports to treat as held by another
	// process. The first ones asked for become taken for good, like a real
	// port somebody else bound: a retry that asks for the same number again
	// loses again.
	lose int
	held map[string]bool
	// failStart, when set, is a start failure that has nothing to do with a
	// port, so it must not be retried.
	failStart string

	next    int
	ports   []string          // host port of every create, in order
	live    map[string]string // id to host port, containers that exist now
	removed []string          // ids removed, in order
	started []string          // ids started
}

func newFakeDaemon(t *testing.T) (*fakeDaemon, *dockerclient.Client) {
	t.Helper()
	d := &fakeDaemon{t: t, held: map[string]bool{}, live: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(srv.Close)
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()),
		dockerclient.WithHTTPClient(srv.Client()),
		dockerclient.WithVersion("1.47"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	return d, cli
}

func (d *fakeDaemon) refuse(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

func (d *fakeDaemon) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// The client asks for /v1.47/containers/create and the rest; the version
	// is not what is being faked, so it is stripped off the front.
	path := r.URL.Path
	if strings.HasPrefix(path, "/v") {
		if i := strings.IndexByte(path[1:], '/'); i >= 0 {
			path = path[1+i:]
		}
	}
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": "sha256:fake"})

	case r.Method == http.MethodPost && path == "/containers/create":
		// The name is unique, as it is on a real daemon. This is what turns a
		// container left behind by a failed attempt into the failure CI saw.
		if len(d.live) > 0 {
			d.refuse(w, http.StatusConflict, fmt.Sprintf(
				`Conflict. The container name "/%s" is already in use`, r.URL.Query().Get("name")))
			return
		}
		var body struct {
			HostConfig struct {
				PortBindings map[string][]struct{ HostPort string }
			}
		}
		require.NoError(d.t, json.NewDecoder(r.Body).Decode(&body))
		port := ""
		for _, b := range body.HostConfig.PortBindings {
			port = b[0].HostPort
		}
		d.next++
		id := fmt.Sprintf("c%d", d.next)
		d.ports = append(d.ports, port)
		d.live[id] = port
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": id, "Warnings": []string{}})

	case r.Method == http.MethodPost && strings.HasSuffix(path, "/start"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/start")
		if d.failStart != "" {
			d.refuse(w, http.StatusInternalServerError, d.failStart)
			return
		}
		port := d.live[id]
		if !d.held[port] && len(d.held) < d.lose {
			d.held[port] = true
		}
		if d.held[port] {
			d.refuse(w, http.StatusInternalServerError, fmt.Sprintf(
				"driver failed programming external connectivity on endpoint %s (%s): "+
					"Bind for 127.0.0.1:%s failed: port is already allocated",
				LocalServerName, id, port))
			return
		}
		d.started = append(d.started, id)
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/containers/"):
		id := strings.TrimPrefix(path, "/containers/")
		delete(d.live, id)
		d.removed = append(d.removed, id)
		w.WriteHeader(http.StatusNoContent)

	default:
		d.t.Errorf("the fake daemon was asked for %s %s, which createLocalServer never calls", r.Method, r.URL.Path)
		d.refuse(w, http.StatusNotFound, "not faked")
	}
}

// freeRange is a start for the allocator where the kernel has ports free, so
// the only refusal in play is the one the fake makes.
const freeRange = 52000

func TestCreateLocalServerTakesAnotherPortWhenItLosesTheRace(t *testing.T) {
	d, cli := newFakeDaemon(t)
	d.lose = 1

	// One assertion, because the fake makes the others redundant: a port it
	// refused stays refused, so a retry that asked for the same number again
	// would fail here too, and so would a start that was not retried at all.
	require.NoError(t, createLocalServer(context.Background(), cli, LocalServerOptions{PortFrom: freeRange}),
		"one lost port race failed the whole start, which is what failed ten live tests on main")
}

func TestCreateLocalServerRemovesAContainerThatLostTheRace(t *testing.T) {
	d, cli := newFakeDaemon(t)
	d.lose = 1

	_ = createLocalServer(context.Background(), cli, LocalServerOptions{PortFrom: freeRange})
	require.Contains(t, d.removed, "c1",
		"the container that could not start was left behind holding the name and a dead port, "+
			"which is the state every later test in the package failed on")
	require.Equal(t, map[string]string{"c2": d.ports[1]}, d.live,
		"exactly one container should exist afterwards, the one that started")
}

func TestCreateLocalServerDoesNotRetryAFailureThatIsNotAPort(t *testing.T) {
	d, cli := newFakeDaemon(t)
	d.failStart = "OCI runtime create failed: no such file or directory"

	err := createLocalServer(context.Background(), cli, LocalServerOptions{PortFrom: freeRange})
	require.ErrorContains(t, err, "OCI runtime create failed")
	require.Len(t, d.ports, 1,
		"a start failure that has nothing to do with a port was retried, which spends three "+
			"attempts before reporting the real problem")
}

func TestCreateLocalServerLeavesNothingBehindWhenStartFailsForAnotherReason(t *testing.T) {
	d, cli := newFakeDaemon(t)
	d.failStart = "OCI runtime create failed: no such file or directory"

	_ = createLocalServer(context.Background(), cli, LocalServerOptions{PortFrom: freeRange})
	require.Empty(t, d.live,
		"a container that failed to start for any reason poisons the fixed name for the next caller")
}

func TestCreateLocalServerStopsAfterItsRetriesAndSaysWhy(t *testing.T) {
	d, cli := newFakeDaemon(t)
	d.lose = 1000

	err := createLocalServer(context.Background(), cli, LocalServerOptions{PortFrom: freeRange})
	require.True(t, dockerutil.IsPortTaken(err),
		"after the retries run out the error should still say a port was taken, got: %v", err)
	require.Len(t, d.ports, dockerutil.PortRetries+1,
		"a machine with no free ports should report that after a bounded number of attempts")
}
