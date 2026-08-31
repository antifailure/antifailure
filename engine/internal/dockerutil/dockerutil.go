// Package dockerutil holds what every Docker backed component shares.
//
// There are three of them: the database provider, the build engine, and the
// local runtime. The thing they must agree on is not the API client, which is
// trivial, but the labels. The leak detector finds resources by label rather
// than by name, so a container an older version created under a different
// naming scheme is still found and removed, and a container with no label is
// never touched at all. That second half is what keeps Antifailure from
// deleting somebody's own Postgres because it happened to be called postgres.
//
// If the three components stamped their own labels, the leak detector would
// have to know all three schemes, and the one it did not know about would be
// the one that leaked.
package dockerutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Labels stamped on everything Antifailure creates.
const (
	// LabelManaged is present on every resource and on nothing else. Its
	// value is the schema version of the label set, so a future change can be
	// recognised rather than guessed at.
	LabelManaged = "dev.antifailure.managed"
	// LabelKind says what the resource is: golden, branch, service, sidecar,
	// network, or volume.
	LabelKind = "dev.antifailure.kind"
	// LabelEnv is the environment id the resource belongs to. Teardown of one
	// environment must never touch another's.
	LabelEnv = "dev.antifailure.env"
	// LabelGolden is the golden version a branch came from.
	LabelGolden = "dev.antifailure.golden"
	// LabelRules is the masking rules digest a golden was produced under, and
	// LabelAttestation is the signed statement that it verified.
	//
	// On the image, because the image is the only thing that survives. A
	// golden's metadata used to live in the struct a refresh returned and
	// nowhere else: `ListGoldens` rebuilt every version from its tag, so the
	// rules digest came back empty and the attestation came back missing. Two
	// separate checks read those fields, and both were silently inert.
	LabelRules       = "dev.antifailure.rules"
	LabelAttestation = "dev.antifailure.attestation"
	// LabelService is the manifest service name a container runs.
	LabelService = "dev.antifailure.service"
	// LabelServiceKind is web, worker, or cron.
	//
	// Recorded rather than inferred. Status used to decide a service was web
	// because a published port had been found for it, which meant a worker
	// came back with no kind at all and a web service came back with no kind
	// until its forwarder existed. af up reads the kind back to choose the
	// service whose URL it prints, so the inference was one race away from an
	// environment that is running and reports no address.
	LabelServiceKind = "dev.antifailure.service-kind"
	// LabelCreated is RFC 3339, used to age out orphans whose creating
	// process died before it could clean up.
	LabelCreated = "dev.antifailure.created"
)

// ManagedValue is what LabelManaged is stamped with.
//
// Nothing reads it. Ownership is decided by the label being present, not by
// its value, so that a future release which stamps something else here still
// finds and cleans up what this one created. A leak detector that matched on
// the value would go blind the day the value changed, which is the one day it
// most needs to work.
const ManagedValue = "true"

// Kinds of resource, as they appear in LabelKind.
const (
	KindGolden    = "golden"
	KindCandidate = "candidate"
	KindBranch    = "branch"
	KindService   = "service"
	KindSidecar   = "sidecar"
	KindNetwork   = "network"
	KindVolume    = "volume"
)

// Managed returns the label set every resource carries.
func Managed(kind, envID string, now time.Time) map[string]string {
	l := map[string]string{
		LabelManaged: ManagedValue,
		LabelKind:    kind,
		LabelCreated: now.UTC().Format(time.RFC3339),
	}
	if envID != "" {
		l[LabelEnv] = envID
	}
	return l
}

// IsOurs reports whether a label set belongs to Antifailure.
//
// Every destructive operation goes through this. A resource that does not
// carry the managed label is somebody else's, and the only correct thing to do
// with it is nothing.
func IsOurs(labels map[string]string) bool {
	return labels[LabelManaged] != ""
}

// Filter builds a label filter for a listing call.
func Filter(pairs ...string) filters.Args {
	// Presence, not equality. Docker treats a bare key as an existence test,
	// which is what makes this find a resource an older release labelled
	// differently.
	f := filters.NewArgs(filters.Arg("label", LabelManaged))
	for i := 0; i+1 < len(pairs); i += 2 {
		f.Add("label", pairs[i]+"="+pairs[i+1])
	}
	return f
}

// EnvFilter matches everything belonging to one environment.
func EnvFilter(envID string) filters.Args { return Filter(LabelEnv, envID) }

// Client opens a connection to the daemon.
//
// The error is coded rather than wrapped raw, because "cannot connect to the
// Docker daemon" with no next step is the single most common first failure a
// new user hits, and the next step depends on which endpoint was tried.
func Client() (*client.Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", Host())
	}
	return cli, nil
}

// Host is the daemon endpoint in use, for error messages.
func Host() string {
	if h := os.Getenv("DOCKER_HOST"); h != "" {
		return h
	}
	return "unix:///var/run/docker.sock"
}

// Discard drains and closes a stream.
//
// Docker's attach and pull endpoints require the body to be read to completion
// before the operation is considered finished. Closing without reading leaves
// the daemon holding the operation open, which shows up much later as a
// mysterious hang in an unrelated call.
func Discard(r io.ReadCloser) {
	if r == nil {
		return
	}
	_, _ = io.Copy(io.Discard, r)
	_ = r.Close()
}

// AwaitExit blocks until a container exits and returns its exit code.
//
// It exists so that no caller has to know the sharp edge in ContainerWait, and
// so that none of them can abandon a wait.
//
// ContainerWait hands back two channels and a goroutine that sends exactly one
// value on one of them. The result channel is UNBUFFERED, and the goroutine
// closes the response body in a defer that runs only after that send. A caller
// that also selects on ctx.Done() can therefore walk away at the moment the
// result is being handed over -- select picks at random between two ready
// cases -- and park the goroutine on the send for good. The body is never
// closed, so the connection never goes back into the transport's idle pool,
// and Client.Close cannot reclaim it, because CloseIdleConnections only
// touches connections that are idle. readLoop and writeLoop then outlive the
// process, which is what a leak detector reports, in a stack with no frame
// from this repository.
//
// Both call sites in this repository were written that way. The fix is not to
// drain the abandoned channels afterwards but to never abandon them: the wait
// request is made with this same context, so cancelling it fails the request
// the goroutine is reading, and the goroutine reports that on the error
// channel. Receiving from both channels and nothing else therefore returns
// promptly when the deadline passes AND leaves nothing parked, which is why
// there is no ctx.Done() case here and must not be one.
func AwaitExit(ctx context.Context, cli *client.Client, id string) (int64, error) {
	statusCh, errCh := cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		return 0, err
	case status := <-statusCh:
		return status.StatusCode, nil
	}
}

// PortAllocator hands out free localhost ports.
//
// Probing for a free port and then binding it is a race with everything else
// on the machine, and the window is small enough that it almost never fires in
// testing and fires regularly in a build that starts six containers at once.
// Remembering what this process handed out closes the half of the race we can
// close; the other half is the caller retrying on a bind failure.
type PortAllocator struct {
	mu    sync.Mutex
	from  int
	taken map[int]bool
}

// DefaultPortFrom is above the ephemeral range on every platform, so an
// allocation does not collide with an outbound connection somebody else made.
const DefaultPortFrom = 43000

// maxPort is the last number that can be a TCP port.
const maxPort = 65535

// NewPortAllocator returns an allocator starting at from, or at the default.
func NewPortAllocator(from int) *PortAllocator {
	if from <= 0 {
		from = DefaultPortFrom
	}
	return &PortAllocator{from: from, taken: map[int]bool{}}
}

// Free returns a port nothing is listening on, or an error naming the range it
// searched.
func (a *PortAllocator) Free() (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	const span = 2000
	// Clamped to the port space. Without this, an allocator started near the
	// top spends its whole search on numbers that cannot be a port, and
	// reports exhaustion of a range that was never searchable.
	end := a.from + span
	if end > maxPort+1 {
		end = maxPort + 1
	}
	if a.from > maxPort {
		return 0, aferrors.Coded(aferrors.AFRUN009,
			"range", fmt.Sprintf("%d-%d", a.from, a.from+span))
	}
	for p := a.from; p < end; p++ {
		if a.taken[p] {
			continue
		}
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			continue
		}
		_ = ln.Close()
		a.taken[p] = true
		return p, nil
	}
	return 0, aferrors.Coded(aferrors.AFRUN009,
		"range", fmt.Sprintf("%d-%d", a.from, end-1))
}

// Release returns a port to the pool, for a container that failed to start.
func (a *PortAllocator) Release(p int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.taken, p)
}

// ErrNotOurs is returned when a caller names a resource Antifailure did not
// create. It is a distinct error because the right response is to refuse
// loudly rather than to fall back to doing nothing.
var ErrNotOurs = errors.New("dockerutil: the resource is not managed by Antifailure")

// RemoveContainer removes a container if it is ours, and reports nothing to do
// if it is already gone.
//
// Teardown runs after crashes, so "already gone" is the normal case, not an
// exceptional one. Treating it as an error would make a clean teardown look
// like a failure every second time.
func RemoveContainer(ctx context.Context, cli *client.Client, id string) error {
	insp, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	if insp.Config == nil || !IsOurs(insp.Config.Labels) {
		return fmt.Errorf("%w: container %s", ErrNotOurs, ShortID(id))
	}
	err = cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true, RemoveVolumes: true})
	if err != nil && cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// RemoveNetwork removes a network Antifailure created, and refuses one it did
// not.
//
// It exists because RemoveContainer had no counterpart and callers therefore
// reached for cli.NetworkRemove directly, which removes whatever the name
// resolves to. The engine's journal replay was one of those callers: it
// addresses a resource by the name recorded before creation, which is the right
// handle for compensating a crash and is not, on its own, evidence of
// ownership. A name is a request; a label is a fact.
//
// A network that is not there is not an error. That is the ordinary state a
// compensating delete finds, and the caller's intent is satisfied.
func RemoveNetwork(ctx context.Context, cli *client.Client, id string) error {
	insp, err := cli.NetworkInspect(ctx, id, network.InspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !IsOurs(insp.Labels) {
		return fmt.Errorf("%w: network %s", ErrNotOurs, id)
	}
	if err := cli.NetworkRemove(ctx, id); err != nil && !cerrdefs.IsNotFound(err) {
		return err
	}
	return nil
}

// RemoveVolume removes a volume Antifailure created, and refuses one it did not.
//
// The same reasoning as RemoveNetwork, and it matters more here: a container can
// be recreated and a network can be recreated, and the data in somebody else's
// volume cannot.
//
// force, because a volume whose container is already gone is exactly the state a
// compensating delete finds. It forces past the container check, never past the
// ownership one.
func RemoveVolume(ctx context.Context, cli *client.Client, id string) error {
	insp, err := cli.VolumeInspect(ctx, id)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !IsOurs(insp.Labels) {
		return fmt.Errorf("%w: volume %s", ErrNotOurs, id)
	}
	if err := cli.VolumeRemove(ctx, id, true); err != nil && !cerrdefs.IsNotFound(err) {
		return err
	}
	return nil
}

// ShortID trims a Docker id to the twelve characters the CLI shows, which is
// what somebody comparing our output to docker ps will be looking at.
func ShortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// FirstName returns the readable name from Docker's slash prefixed list.
func FirstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}
