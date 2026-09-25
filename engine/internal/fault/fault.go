// Package fault injects real failures into the containers one environment
// owns, so that a claim about recovery is measured against a system that
// actually broke.
//
// Everything here is destructive on purpose, which is why the whole package is
// organised around one question: what may this touch. The answer is one
// environment's own containers and nothing else, and it is enforced rather
// than intended. A target is resolved by the Docker labels the runtime stamps
// at create time, never by a name a caller supplied, and every mutating call
// re-reads the container's labels immediately before it acts. A name is not
// evidence of ownership: the reconciler says so about teardown, and a fault
// that kills the wrong Postgres is the same mistake with a worse ending,
// because this machine runs other people's databases beside ours.
//
// Three refusals are absolute and have no override:
//
//   - a container carrying no dev.antifailure.managed label is not ours
//   - a container whose dev.antifailure.env is a different environment
//   - the egress sidecar, whatever environment it belongs to
//
// The sidecar is protected because it is the control that decides what the
// environment may reach on the network. A fault that stops it would switch
// the default deny policy off, and a chaos feature that can disable a safety
// control is not a chaos feature, it is a bypass with a rehearsal in front of
// it.
//
// Every fault returns an Injection that carries its own undo. The undo is
// idempotent and is written so that it still means something after the thing
// it undoes has died: unpausing a container that exited is not an error here,
// because the caller's deferred undo must not turn one failure into two.
package fault

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// Kind is what a fault does to its target.
//
// The set is closed, and it is closed at the manifest too. A kind is added
// here only when it is implemented against real containers: a declared kind
// that silently does nothing is the failure this package exists to catch in
// somebody else's system, and it would be indefensible to ship it in ours.
type Kind string

const (
	// KindProcessKill sends SIGKILL to one process inside the container,
	// chosen by a substring of its command line. The container keeps running.
	//
	// This is the Postgres crash. Killing a backend or an auxiliary process
	// makes the postmaster discard shared memory, terminate every other
	// server process and reinitialise, which is the real crash recovery path
	// and not a clean shutdown wearing its clothes.
	KindProcessKill Kind = "process_kill"
	// KindContainerKill sends SIGKILL to the container's main process. The
	// container stops, and the undo starts it again. This is the node that
	// went away rather than the process that crashed.
	KindContainerKill Kind = "container_kill"
	// KindContainerStop sends SIGTERM, waits, then SIGKILL. The contrast with
	// KindContainerKill is the point: a database that shuts down cleanly does
	// no recovery on the way back, so a recovery check that passes on this
	// kind is a recovery check that is not looking.
	KindContainerStop Kind = "container_stop"
	// KindContainerPause freezes every process in the container with the
	// cgroup freezer. Nothing is killed and no connection is closed, so the
	// clients hang exactly as they do when a node stalls, and the undo thaws
	// it with the processes in the state they were frozen in.
	KindContainerPause Kind = "container_pause"
	// KindNetworkPartition detaches the container from the environment's
	// internal network. It keeps running and keeps its memory, and is
	// unreachable by name and by address from everything else in the
	// environment. The undo attaches it again under the same aliases.
	KindNetworkPartition Kind = "network_partition"
	// KindReadOnlyData removes write permission from the database's data
	// directory, so a write that reaches the filesystem fails with a real
	// errno rather than with a simulated one. The undo restores the modes it
	// recorded before it changed them.
	KindReadOnlyData Kind = "read_only_data"
	// KindDiskFill writes a file into the data directory until the filesystem
	// holding it has less than a stated headroom free, so the database meets a
	// real ENOSPC. It is refused unless that filesystem is a mount of its own,
	// because on a shared filesystem the blast radius is every container on
	// the machine and this package's whole claim is that it has none.
	KindDiskFill Kind = "disk_fill"
)

// Kinds is every kind, in the order they are documented.
func Kinds() []Kind {
	return []Kind{
		KindProcessKill, KindContainerKill, KindContainerStop,
		KindContainerPause, KindNetworkPartition, KindReadOnlyData, KindDiskFill,
	}
}

// ExpectsCrash reports whether this kind, aimed at a database, is expected to
// stop it uncleanly so that it has to replay its write ahead log on the way
// back.
//
// Written down here rather than inferred from what the log turns out to say,
// because inferring it is precisely how a recovery check comes to pass on a
// run where nothing crashed. A kind that is not on this list and is run with a
// recovery proof reports that it could not establish a recovery, which is the
// honest answer: a container stopped with SIGTERM shuts Postgres down cleanly
// and replays nothing, and a check that called that a successful recovery
// would report the same thing whether or not the fault had landed.
func (k Kind) ExpectsCrash() bool {
	return k == KindProcessKill || k == KindContainerKill
}

// Valid reports whether k is a kind this package implements.
func (k Kind) Valid() bool {
	for _, known := range Kinds() {
		if k == known {
			return true
		}
	}
	return false
}

// Role is which container in the environment a fault is aimed at.
type Role string

const (
	// RoleDatabase is the environment's database branch.
	RoleDatabase Role = "database"
	// RoleService is one of the manifest's services, named by Target.Service.
	RoleService Role = "service"
)

// Roles is every role.
func Roles() []Role { return []Role{RoleDatabase, RoleService} }

// Valid reports whether r is a role this package resolves.
func (r Role) Valid() bool {
	for _, known := range Roles() {
		if r == known {
			return true
		}
	}
	return false
}

// kindForRole is the container kind label a role resolves to.
//
// The mapping is here rather than at the call site so that the set of label
// kinds a fault may reach is one list somebody can read. "sidecar" and
// "emulator" are deliberately absent, and protectedKinds says so out loud.
var kindForRole = map[Role]string{
	RoleDatabase: "branch",
	RoleService:  "service",
}

// protectedKinds are container kinds no fault may ever touch, in any
// environment, whatever the caller asks for.
//
// The sidecar carries the egress policy. The emulator answers for a real
// third party the environment is forbidden to reach, so stopping it does not
// produce an outage, it produces a request that goes looking for the real
// host. Both are safety controls, and a fault that can switch a safety control
// off is a hole with a feature name.
var protectedKinds = map[string]string{
	"sidecar":  "it carries the egress policy, and stopping it would let the environment reach hosts the manifest refused",
	"emulator": "it stands in for a third party the environment must not reach, and stopping it would send the request looking for the real one",
	"storage":  "it holds the database's data directory mounted, and stopping it would delete the data directory rather than crash the database, so every durability check after it would be reading an empty database and calling it a loss",
}

// Target says which container a fault is aimed at.
type Target struct {
	// Role is database or service.
	Role Role
	// Service is the manifest service name, required when Role is service and
	// refused otherwise.
	Service string
}

// String renders a target the way a finding and an error message name it.
func (t Target) String() string {
	if t.Role == RoleService {
		return "service " + t.Service
	}
	return string(t.Role)
}

// Fault is one injection: what to do, to which container, with the parameters
// that kind needs.
type Fault struct {
	// Name is the author's label for this fault. It is what a report calls it.
	Name string
	// Kind is what is done.
	Kind Kind
	// Target is what it is done to.
	Target Target
	// Process is the substring of a command line KindProcessKill matches. It
	// is required for that kind and refused for every other.
	Process string
	// Signal is the signal KindProcessKill and KindContainerKill send.
	// Empty means KILL, which is the only signal that is a crash rather than
	// a request.
	Signal string
	// Path is the directory KindReadOnlyData and KindDiskFill act on. Empty
	// means the database's data directory, which is what the manifest's
	// default resolves to.
	Path string
	// HeadroomBytes is how little space KindDiskFill leaves free. It must be
	// set, because filling a filesystem to exactly zero leaves no room to
	// write the file that undoes it.
	HeadroomBytes int64
	// MaxFillBytes caps how much KindDiskFill will write, whatever the
	// filesystem reports free. A cap that is never reached costs nothing; a
	// missing cap costs the machine.
	MaxFillBytes int64
}

// Container is a resolved target: a container this environment owns, with the
// labels that proved it.
type Container struct {
	ID      string
	Name    string
	Kind    string
	Service string
	EnvID   string
	State   string
}

// Short is the id in the form every other part of the engine prints.
func (c Container) Short() string { return dockerutil.ShortID(c.ID) }

// Docker is the part of the daemon API this package uses.
//
// Narrow on purpose. A fake that satisfies this can drive the refusals, and a
// fake that cannot pause a container also cannot pretend it did.
type Docker interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(ctx context.Context, id string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerKill(ctx context.Context, id string, options client.ContainerKillOptions) (client.ContainerKillResult, error)
	ContainerStop(ctx context.Context, id string, options client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerStart(ctx context.Context, id string, options client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerPause(ctx context.Context, id string, options client.ContainerPauseOptions) (client.ContainerPauseResult, error)
	ContainerUnpause(ctx context.Context, id string, options client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error)
	NetworkConnect(ctx context.Context, networkID string, options client.NetworkConnectOptions) (client.NetworkConnectResult, error)
	NetworkDisconnect(ctx context.Context, networkID string, options client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error)
	ExecCreate(ctx context.Context, id string, options client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(ctx context.Context, execID string, options client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(ctx context.Context, execID string, options client.ExecInspectOptions) (client.ExecInspectResult, error)
	ContainerLogs(ctx context.Context, id string, options client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	VolumeInspect(ctx context.Context, volumeID string, options client.VolumeInspectOptions) (client.VolumeInspectResult, error)
}

// Errors the guard raises. They are sentinels so a caller can tell a refusal
// from a daemon failure: the first is the feature working and the second is
// the feature broken, and a test that cannot tell them apart proves neither.
var (
	// ErrNotOurs is a container with no Antifailure label.
	ErrNotOurs = errors.New("fault: the container is not one Antifailure created")
	// ErrOtherEnvironment is a container belonging to a different environment.
	ErrOtherEnvironment = errors.New("fault: the container belongs to another environment")
	// ErrProtected is a container whose kind is never a target.
	ErrProtected = errors.New("fault: the container carries a safety control")
	// ErrNoSuchTarget is a target the environment does not have.
	ErrNoSuchTarget = errors.New("fault: the environment has no such container")
)

// Injector applies faults inside one environment.
type Injector struct {
	cli   Docker
	envID string
}

// New returns an injector bound to one environment.
//
// The environment id is fixed at construction and is never a parameter
// afterwards, so there is no call in this package that can be handed the wrong
// one by a caller in a hurry.
func New(cli Docker, envID string) (*Injector, error) {
	if cli == nil {
		return nil, errors.New("fault: no Docker client")
	}
	if strings.TrimSpace(envID) == "" {
		return nil, errors.New("fault: no environment id, so there is nothing to scope a fault to")
	}
	return &Injector{cli: cli, envID: envID}, nil
}

// EnvID is the environment this injector is bound to.
func (i *Injector) EnvID() string { return i.envID }

// Containers lists what this environment owns, which is also the whole set a
// fault may reach.
//
// It is exported because the refusal for an unknown target names it: being
// told "no such service" without being told what there is means reading the
// manifest to find out what you already asked about.
func (i *Injector) Containers(ctx context.Context) ([]Container, error) {
	res, err := i.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: dockerutil.EnvFilter(i.envID),
	})
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN002, "endpoint", dockerutil.Host())
	}
	out := make([]Container, 0, len(res.Items))
	for _, c := range res.Items {
		// The daemon filtered by label, and this checks the label again. Not
		// belt and braces: the filter is a string the daemon parsed and this
		// is the predicate the refusal is written against, so a test can drive
		// the predicate with a container the filter would never have returned.
		if !dockerutil.IsOurs(c.Labels) || c.Labels[dockerutil.LabelEnv] != i.envID {
			continue
		}
		out = append(out, Container{
			ID:      c.ID,
			Name:    dockerutil.FirstName(c.Names),
			Kind:    c.Labels[dockerutil.LabelKind],
			Service: c.Labels[dockerutil.LabelService],
			EnvID:   c.Labels[dockerutil.LabelEnv],
			State:   string(c.State),
		})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

// Resolve finds the one container a target names.
//
// Zero matches and more than one match are both errors, and neither is
// resolved by picking. A fault aimed at "the database" in an environment with
// two of them has not been told what to break.
func (i *Injector) Resolve(ctx context.Context, t Target) (Container, error) {
	if !t.Role.Valid() {
		return Container{}, aferrors.Coded(aferrors.AFCHS001,
			"target", t.String(), "detail", "the role is not one this engine resolves")
	}
	if t.Role == RoleService && strings.TrimSpace(t.Service) == "" {
		return Container{}, aferrors.Coded(aferrors.AFCHS001,
			"target", t.String(), "detail", "a service target names no service")
	}
	all, err := i.Containers(ctx)
	if err != nil {
		return Container{}, err
	}
	want := kindForRole[t.Role]
	var matches []Container
	for _, c := range all {
		if c.Kind != want {
			continue
		}
		if t.Role == RoleService && c.Service != t.Service {
			continue
		}
		matches = append(matches, c)
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return Container{}, fmt.Errorf("%w: %s. This environment has %s",
			ErrNoSuchTarget, t, describe(all))
	default:
		return Container{}, fmt.Errorf("fault: %s matches %d containers in this environment, so there is no one thing to break: %s",
			t, len(matches), describe(matches))
	}
}

// describe renders a container list for a refusal message.
func describe(cs []Container) string {
	if len(cs) == 0 {
		return "no containers at all"
	}
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		label := c.Kind
		if c.Service != "" {
			label = c.Kind + " " + c.Service
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

// mustOwn re-reads a container by id and refuses unless this environment owns
// it and its kind is not a protected one.
//
// Every mutating call goes through this, including the ones whose id came
// from Resolve a moment earlier. Resolve proves ownership at the time it ran,
// and the thing that makes this a guarantee rather than a habit is that the
// proof is taken again at the instant of the act, from the daemon, by id.
func (i *Injector) mustOwn(ctx context.Context, id string) (Container, error) {
	res, err := i.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return Container{}, aferrors.Wrap(err, aferrors.AFCHS001,
			"target", dockerutil.ShortID(id), "detail", "the container could not be inspected: "+err.Error())
	}
	c := res.Container
	labels := map[string]string{}
	if c.Config != nil {
		labels = c.Config.Labels
	}
	got := Container{
		ID:      c.ID,
		Name:    strings.TrimPrefix(c.Name, "/"),
		Kind:    labels[dockerutil.LabelKind],
		Service: labels[dockerutil.LabelService],
		EnvID:   labels[dockerutil.LabelEnv],
	}
	if c.State != nil {
		got.State = string(c.State.Status)
	}
	if !dockerutil.IsOurs(labels) {
		return got, fmt.Errorf("%w: %s carries no %s label, so nothing here may touch it",
			ErrNotOurs, name(got), dockerutil.LabelManaged)
	}
	if got.EnvID != i.envID {
		return got, fmt.Errorf("%w: %s belongs to environment %q and this injector is bound to %q",
			ErrOtherEnvironment, name(got), got.EnvID, i.envID)
	}
	if why, protected := protectedKinds[got.Kind]; protected {
		return got, fmt.Errorf("%w: %s is the %s and %s",
			ErrProtected, name(got), got.Kind, why)
	}
	return got, nil
}

// name is what a refusal calls a container: its name when it has one, and its
// short id when it does not.
func name(c Container) string {
	if c.Name != "" {
		return c.Name
	}
	return dockerutil.ShortID(c.ID)
}

// Injection is a fault that has been applied and the undo that reverses it.
type Injection struct {
	// Fault is what was asked for.
	Fault Fault
	// Container is what it landed on.
	Container Container
	// At is when the fault was applied.
	At time.Time
	// Evidence is what the daemon or the container said at the moment of the
	// act: the process that was killed, the exit code, the network that was
	// detached. It is recorded because "the fault was injected" is a claim
	// and this is what backs it.
	Evidence string
	// KilledSignal is the signal the container's MAIN process died on, read
	// from its exit status, or zero when the container is still running or
	// exited normally.
	//
	// It is here because it is the only evidence a node level crash leaves.
	// A database whose postmaster is killed cannot log that its postmaster was
	// killed: the process that writes that line is the process that died. A
	// recovery check that looked only at the log would report "nothing
	// crashed" about the most complete crash it can cause, which is the exact
	// shape of blindness this package exists to remove.
	KilledSignal int

	undo   func(ctx context.Context) error
	undone bool
}

// Undo reverses the fault. It is idempotent, and calling it on a fault whose
// target has since died is not an error.
func (in *Injection) Undo(ctx context.Context) error {
	if in == nil || in.undone {
		return nil
	}
	in.undone = true
	if in.undo == nil {
		return nil
	}
	return in.undo(ctx)
}

// Undone reports whether Undo has run.
func (in *Injection) Undone() bool { return in != nil && in.undone }

// Inject applies a fault to the container its target resolves to.
//
// A refused fault returns an error and no Injection, so there is never an
// undo for something that did not happen.
func (i *Injector) Inject(ctx context.Context, f Fault) (*Injection, error) {
	if err := Validate(f); err != nil {
		return nil, err
	}
	c, err := i.Resolve(ctx, f.Target)
	if err != nil {
		return nil, err
	}
	return i.InjectInto(ctx, c.ID, f)
}

// InjectInto applies a fault to one container by id, after proving the
// environment owns it.
//
// Exported so that the guard can be aimed at a container it must refuse. A
// refusal that has only ever been described is a refusal nobody has seen
// happen, and this is the entry point a test uses to make it happen.
func (i *Injector) InjectInto(ctx context.Context, id string, f Fault) (*Injection, error) {
	if err := Validate(f); err != nil {
		return nil, err
	}
	c, err := i.mustOwn(ctx, id)
	if err != nil {
		return nil, err
	}
	at := time.Now().UTC()
	var apply func(context.Context, Container, Fault) (string, func(context.Context) error, error)
	switch f.Kind {
	case KindProcessKill:
		apply = i.processKill
	case KindContainerKill:
		apply = i.containerKill
	case KindContainerStop:
		apply = i.containerStop
	case KindContainerPause:
		apply = i.containerPause
	case KindNetworkPartition:
		apply = i.networkPartition
	case KindReadOnlyData:
		apply = i.readOnlyData
	case KindDiskFill:
		apply = i.diskFill
	default:
		// Unreachable while Validate and Kind.Valid agree, and present so that
		// adding a kind to the constant block without adding it here is a
		// refusal rather than a silent no-op.
		return nil, aferrors.Coded(aferrors.AFCHS002, "kind", string(f.Kind),
			"detail", "the kind is declared but this engine has no implementation for it")
	}
	evidence, undo, err := apply(ctx, c, f)
	if err != nil {
		return nil, err
	}
	return &Injection{
		Fault: f, Container: c, At: at, Evidence: evidence,
		KilledSignal: i.exitSignal(ctx, c.ID), undo: undo,
	}, nil
}

// Validate refuses a fault whose parameters do not fit its kind.
//
// Separate from the manifest's validator and run again here, because a fault
// can reach this package from the manifest, from a flag, or from a test, and
// the parameter that is wrong is the same parameter in all three.
func Validate(f Fault) error {
	if !f.Kind.Valid() {
		return aferrors.Coded(aferrors.AFCHS002, "kind", string(f.Kind),
			"detail", "it is not one of "+joinKinds())
	}
	if !f.Target.Role.Valid() {
		return aferrors.Coded(aferrors.AFCHS001, "target", f.Target.String(),
			"detail", "the role is not one this engine resolves")
	}
	if f.Target.Role == RoleService && strings.TrimSpace(f.Target.Service) == "" {
		return aferrors.Coded(aferrors.AFCHS001, "target", f.Target.String(),
			"detail", "a service target names no service")
	}
	if f.Target.Role != RoleService && f.Target.Service != "" {
		return aferrors.Coded(aferrors.AFCHS001, "target", f.Target.String(),
			"detail", "a service name was given for a target that is not a service")
	}
	switch f.Kind {
	case KindProcessKill:
		if strings.TrimSpace(f.Process) == "" {
			return aferrors.Coded(aferrors.AFCHS002, "kind", string(f.Kind),
				"detail", "it names no process to kill, and killing an unnamed process is killing whichever one was listed first")
		}
	case KindDiskFill:
		if f.HeadroomBytes <= 0 {
			return aferrors.Coded(aferrors.AFCHS002, "kind", string(f.Kind),
				"detail", "it states no headroom, and a filesystem filled to zero leaves no room to write the file that empties it")
		}
		if f.MaxFillBytes <= 0 {
			return aferrors.Coded(aferrors.AFCHS002, "kind", string(f.Kind),
				"detail", "it states no cap, and a fill with no cap is bounded only by the machine")
		}
	}
	if f.Process != "" && f.Kind != KindProcessKill {
		return aferrors.Coded(aferrors.AFCHS002, "kind", string(f.Kind),
			"detail", "a process was named for a kind that kills no process")
	}
	return nil
}

func joinKinds() string {
	parts := make([]string, 0, len(Kinds()))
	for _, k := range Kinds() {
		parts = append(parts, string(k))
	}
	return strings.Join(parts, ", ")
}

// signalOf is the signal a fault sends, defaulting to the only one that is a
// crash.
func signalOf(f Fault) string {
	if s := strings.TrimSpace(f.Signal); s != "" {
		return s
	}
	return "KILL"
}

// exitSignal is the signal a container's main process died on, read from its
// exit status, or zero when it is running or exited normally.
//
// Docker reports a signal death as 128 plus the signal, which is the shell's
// convention and the only place the number survives. A container that is still
// running has no exit status to read and reports zero, which is correct: a
// fault that left the container running did not kill its main process.
func (i *Injector) exitSignal(ctx context.Context, id string) int {
	res, err := i.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil || res.Container.State == nil || res.Container.State.Running {
		return 0
	}
	if code := res.Container.State.ExitCode; code > 128 && code < 128+64 {
		return code - 128
	}
	return 0
}

// running reports whether a container is up, for an undo that must not turn a
// dead target into a second failure.
func (i *Injector) running(ctx context.Context, id string) bool {
	res, err := i.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil || res.Container.State == nil {
		return false
	}
	return res.Container.State.Running
}

// paused reports whether a container is frozen.
func (i *Injector) paused(ctx context.Context, id string) bool {
	res, err := i.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil || res.Container.State == nil {
		return false
	}
	return res.Container.State.Paused
}

// inspect is the raw container state, for the faults that need more than
// running and paused.
func (i *Injector) inspect(ctx context.Context, id string) (container.InspectResponse, error) {
	res, err := i.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return container.InspectResponse{}, err
	}
	return res.Container, nil
}
