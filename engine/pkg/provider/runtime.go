package provider

import (
	"context"
	"time"

	"github.com/antifailure/antifailure/engine/pkg/schema"
	"github.com/antifailure/antifailure/engine/pkg/secret"
)

// Runtime creates and destroys environments.
//
// It is deliberately small. Everything that decides what an environment should
// look like happens before a runtime is called: the manifest is resolved, the
// images are built, the database branch exists and its connection string is in
// hand. A runtime places containers, wires them to a network, waits for them
// to answer, and takes them away again. Keeping the decisions out of it is
// what makes a second runtime a package rather than a fork, because the
// Kubernetes one and the Docker one disagree about almost everything except
// this shape.
type Runtime interface {
	// Name identifies the runtime in output and in errors.
	Name() string

	// Capabilities declares what this runtime can do.
	//
	// It exists so that the conformance suite can skip a behavior by name
	// rather than silently pass one it never ran. Note what is deliberately
	// absent: there is no capability for containment. A runtime that cannot
	// keep an environment off the network is not a runtime this product has,
	// so the egress behaviors are not skippable and no field here can turn
	// them off.
	Capabilities() RuntimeCaps

	// Up creates the environment and returns it once every service that
	// declares readiness has answered.
	//
	// It is not atomic and does not pretend to be. Each resource is reported
	// through the spec's Journal callback before it is created, so that an
	// interrupt at any instant leaves something Down can remove. A runtime
	// that created three containers and then failed must leave those three
	// findable, not roll them back silently and lose the logs that explain
	// the failure.
	Up(ctx context.Context, spec EnvSpec) (Env, error)

	// Down removes everything belonging to an environment.
	//
	// It never stops at the first failure. A provider that is unreachable
	// must not strand the other resources, so each is attempted and what
	// could not be removed is returned.
	Down(ctx context.Context, envID string) (Teardown, error)

	// Status reports what is currently running for an environment.
	Status(ctx context.Context, envID string) (Env, error)

	// Inventory lists everything this runtime holds, for the leak detector.
	Inventory(ctx context.Context) ([]Resource, error)

	// Close releases the runtime's own resources.
	Close() error
}

// RuntimeCaps declares what a runtime supports.
//
// Every field gates something: a conformance behavior that is skipped by name,
// or a decision the engine makes about which path to take. Nothing else
// belongs here. A capability that gates nothing is a claim no code reads,
// which is how a provider ends up advertising something it does not do.
type RuntimeCaps struct {
	// Ingress reports that a web service is reachable from the machine that
	// called Up, at the URL Status returns. A runtime in a cluster the caller
	// cannot route to answers false, and says so rather than reporting a URL
	// that does not resolve.
	Ingress bool
	// Logs reports that the runtime implements LogReader.
	Logs bool
	// AttachesLocalDatabase reports that the runtime can put a database
	// container from the local daemon onto the environment's network. It is
	// false for anything running off this machine, where the only usable
	// database is one with an address the environment can already reach.
	AttachesLocalDatabase bool
}

// EnvSpec is everything needed to bring one environment up.
type EnvSpec struct {
	// EnvID identifies the environment. Every resource is labelled with it,
	// and teardown of one environment must never touch another's.
	EnvID string
	// Branch is the source control branch, for display.
	Branch string
	// Services are the containers to run, in the order they should start.
	Services []ServiceSpec
	// DatabaseURL is the connection string services receive. It is a secret
	// because it is one, and because a runtime that took a plain string would
	// eventually log it.
	DatabaseURL secret.Value
	// MigrationDatabaseURL is the connection string a service's migrate command
	// receives, where it differs from the one the service itself gets.
	//
	// It differs whenever the provider offers a pooled endpoint. An
	// application should use the pool; a migration must not, because a
	// transaction pooler does not support the session level features
	// migrations use, and the failure is a migration that half applies rather
	// than one that refuses. Zero means use DatabaseURL for both, which is
	// correct for a provider with no pool.
	MigrationDatabaseURL secret.Value
	// PublicPorts maps a service name to the host port it will be reachable
	// on, reserved before anything starts.
	//
	// Reserved up front, and not merely reported afterwards, because a service
	// has to be told its own address before it runs. An application that emails
	// a sign in link, redirects through OAuth, or hands a webhook a callback
	// builds an absolute URL, and inside a preview the only address that works
	// is one the runtime allocates. Without this, every such application sends
	// a link to its own container port: the agent that received one navigated
	// to http://localhost:3100 and got a connection refused, four minutes into
	// a workflow.
	PublicPorts map[string]int
	// Egress is the policy the sidecar enforces.
	//
	// Nil means block everything. The runtime never decides what a rule means;
	// it hands the policy to the sidecar, which shares the decision code with
	// af net explain, so the two cannot disagree.
	Egress *schema.Egress
	// MockPacks are fixture packs from the repository, as raw JSON. The packs
	// that ship with the engine are always available and are not listed here.
	MockPacks []string
	// ModelEnv carries a model key to the sidecar, for a rule in synth mode.
	// It is passed as an environment variable rather than written into a
	// file, so a key never lands on disk.
	ModelEnv []string
	// SandboxCredentials are the values the sidecar substitutes for a rule in
	// sandbox mode, keyed by the name the rule refers to.
	SandboxCredentials map[string]secret.Value
	// CACertPEM is the environment certificate every service is told to trust.
	//
	// Empty when nothing in the policy needs to read inside TLS, in which case
	// no connection is ever terminated and no service needs to trust anything
	// it would not otherwise.
	CACertPEM string
	// CAKeyPEM is the matching private key, which goes to the sidecar alone.
	CAKeyPEM secret.Value
	// Journal records a resource before it is created. A runtime must call it
	// and must respect an error from it, because a resource created before it
	// was recorded is a resource teardown cannot find.
	Journal func(kind, id string) error
	// Progress receives human readable progress, already redacted.
	Progress func(line string)
}

// ServiceSpec is one container to run.
type ServiceSpec struct {
	// Name is the manifest service name.
	Name string
	// Image is the built image reference.
	Image string
	// Kind is web, worker, or cron.
	Kind string
	// Command overrides the image's command. Empty uses the image's own.
	Command string
	// Port is the port the service listens on inside the container. Zero
	// means it does not listen.
	Port int
	// HealthPath is the path to poll for readiness. Empty means the runtime
	// waits for the port to accept a connection instead.
	HealthPath string
	// HealthTimeout bounds the wait. Zero uses the runtime's default.
	HealthTimeout time.Duration
	// Env are additional variables. Values are secrets because some of them
	// are, and separating the two at this layer means guessing.
	Env map[string]secret.Value
	// DependsOn names services that must be ready first.
	DependsOn []string
	// Migrate is a command to run to completion before the service starts.
	Migrate string
}

// Env is a running environment.
type Env struct {
	// EnvID identifies it.
	EnvID string
	// Services is what is running, with the URL each can be reached at.
	Services []RunningService
	// NetworkID is the runtime's network identifier.
	NetworkID string
	// CreatedAt is when it came up.
	CreatedAt time.Time
	// ProxyReady reports whether the egress sidecar is running. When it is
	// not, the environment has no route out at all.
	ProxyReady bool
}

// URL returns the address of the first web service, which is what af up
// prints and what a pull request comment links to.
func (e Env) URL() string {
	for _, s := range e.Services {
		if s.Kind == "web" && s.URL != "" {
			return s.URL
		}
	}
	return ""
}

// RunningService is one container in a running environment.
type RunningService struct {
	// Name is the manifest service name.
	Name string
	// Kind is web, worker, or cron.
	Kind string
	// ContainerID is the runtime's identifier.
	ContainerID string
	// URL is where it can be reached from the host, when it listens.
	URL string
	// Ready reports whether it answered its readiness check.
	Ready bool
	// State is the runtime's own word for what it is doing.
	State string
	// Detail explains a state that is not running, such as an exit code.
	Detail string
	// ExitCode is the code a finished service exited with, and nil while it
	// is still running or where the runtime cannot say.
	//
	// Detail carries the same fact in the runtime's own words, and that is
	// the problem: "Exited (9) 3 seconds ago" and a terminated container
	// status are the same event in two formats, so anything that wanted the
	// number parsed one of them with a regular expression. The conformance
	// suite needs it for every behavior that asks whether a service could
	// reach a host, because the answer is the probe's exit code, so the
	// number is part of the contract rather than a detail of Docker's
	// phrasing.
	ExitCode *int
}

// Teardown is what Down managed to remove, and what it could not.
type Teardown struct {
	// Removed counts resources that are gone.
	Removed int
	// Pending lists what could not be removed, with the reason. A non empty
	// list is why af down exits 10 rather than 0: something is still there,
	// and reporting success would be a lie somebody pays for later.
	Pending []PendingResource
}

// PendingResource is one thing teardown could not remove.
type PendingResource struct {
	Kind   string
	ID     string
	Reason string
}

// LogReader is implemented by a runtime that can return a service's output.
//
// Optional rather than part of Runtime, because it is the one capability a
// runtime can honestly lack: reading logs out of a cluster the caller cannot
// reach is not something to fake. A runtime that does not implement it
// declares Logs false, and the command that wanted them says which runtime
// could not supply them instead of printing nothing.
type LogReader interface {
	// Logs returns recent output from an environment's services, already
	// redacted. An empty service name means every service.
	Logs(ctx context.Context, envID, service string, tail int) ([]LogLine, error)
}

// LogLine is one line of a service's output.
type LogLine struct {
	// Service is which service wrote it.
	Service string
	// Stream is "stdout" or "stderr".
	Stream string
	// Text is the line, already redacted.
	Text string
}
