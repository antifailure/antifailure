package provider

import (
	"context"
	"time"

	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/schema"
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
	DatabaseURL secrets.Value
	// Egress is the policy the sidecar enforces.
	//
	// Nil means block everything. The runtime never decides what a rule means;
	// it hands the policy to the sidecar, which shares the decision code with
	// af net explain, so the two cannot disagree.
	Egress *schema.Egress
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
	Env map[string]secrets.Value
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
