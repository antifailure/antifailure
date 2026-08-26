// Package schema holds the types that cross a language boundary.
//
// The JSON Schema documents under schemas/ are the source of truth. These Go
// types mirror them, and a test validates real manifests against both so the
// two cannot drift: a field added to the schema and not here, or here and not
// there, fails the build.
//
// Field order follows the schema, and every field carries both a JSON and a
// YAML tag, because the manifest is written as YAML and transmitted as JSON.
package schema

// ManifestVersion is the schema version this build understands.
const ManifestVersion = 1

// Manifest is antifailure.yaml.
type Manifest struct {
	Version    int         `json:"version" yaml:"version"`
	Name       string      `json:"name,omitempty" yaml:"name,omitempty"`
	Services   []Service   `json:"services,omitempty" yaml:"services,omitempty"`
	Database   *Database   `json:"database,omitempty" yaml:"database,omitempty"`
	Egress     *Egress     `json:"egress,omitempty" yaml:"egress,omitempty"`
	Personas   []Persona   `json:"personas,omitempty" yaml:"personas,omitempty"`
	Workflows  []Workflow  `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	Invariants []Invariant `json:"invariants,omitempty" yaml:"invariants,omitempty"`
	Insights   *Insights   `json:"insights,omitempty" yaml:"insights,omitempty"`
	Load       *Load       `json:"load,omitempty" yaml:"load,omitempty"`
	Runtime    *Runtime    `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	GitHub     *GitHub     `json:"github,omitempty" yaml:"github,omitempty"`
}

// ServiceKind is what a service is.
type ServiceKind string

const (
	// ServiceWeb gets a hostname and a readiness check.
	ServiceWeb ServiceKind = "web"
	// ServiceWorker runs continuously with neither.
	ServiceWorker ServiceKind = "worker"
	// ServiceCron is invoked on a schedule rather than run continuously.
	ServiceCron ServiceKind = "cron"
)

// Service is one process the environment runs.
type Service struct {
	Name          string      `json:"name" yaml:"name"`
	Path          string      `json:"path,omitempty" yaml:"path,omitempty"`
	Kind          ServiceKind `json:"kind,omitempty" yaml:"kind,omitempty"`
	Build         *Build      `json:"build,omitempty" yaml:"build,omitempty"`
	Command       string      `json:"command,omitempty" yaml:"command,omitempty"`
	Port          int         `json:"port,omitempty" yaml:"port,omitempty"`
	HealthPath    string      `json:"health_path,omitempty" yaml:"health_path,omitempty"`
	HealthTimeout string      `json:"health_timeout,omitempty" yaml:"health_timeout,omitempty"`
	Env           []EnvVar    `json:"env,omitempty" yaml:"env,omitempty"`
	Replicas      int         `json:"replicas,omitempty" yaml:"replicas,omitempty"`
	Resources     *Resources  `json:"resources,omitempty" yaml:"resources,omitempty"`
	Schedule      string      `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	Migrate       string      `json:"migrate,omitempty" yaml:"migrate,omitempty"`
	DependsOn     []string    `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
}

// BuildStrategy is how a service becomes an image.
type BuildStrategy string

const (
	// BuildAuto picks a Dockerfile if there is one and a buildpack otherwise.
	BuildAuto BuildStrategy = "auto"
	// BuildDockerfile uses the service's Dockerfile.
	BuildDockerfile BuildStrategy = "dockerfile"
	// BuildBuildpack infers the build from the language and lockfile.
	BuildBuildpack BuildStrategy = "buildpack"
	// BuildImage uses a prebuilt image and does not build at all.
	BuildImage BuildStrategy = "image"
)

// Build describes how to produce a service's image.
type Build struct {
	Strategy   BuildStrategy     `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	Dockerfile string            `json:"dockerfile,omitempty" yaml:"dockerfile,omitempty"`
	Target     string            `json:"target,omitempty" yaml:"target,omitempty"`
	Context    string            `json:"context,omitempty" yaml:"context,omitempty"`
	Image      string            `json:"image,omitempty" yaml:"image,omitempty"`
	Args       map[string]string `json:"args,omitempty" yaml:"args,omitempty"`
	AllowHosts []string          `json:"allow_hosts,omitempty" yaml:"allow_hosts,omitempty"`
}

// EnvVar names a variable a service needs. It holds a name, never a secret.
type EnvVar struct {
	Name string `json:"name" yaml:"name"`
	// Required defaults to true. The pointer distinguishes "not set, so use
	// the default" from "explicitly set to false", which a bare bool cannot.
	Required *bool  `json:"required,omitempty" yaml:"required,omitempty"`
	Sandbox  bool   `json:"sandbox,omitempty" yaml:"sandbox,omitempty"`
	Value    string `json:"value,omitempty" yaml:"value,omitempty"`
	From     string `json:"from,omitempty" yaml:"from,omitempty"`
}

// IsRequired reports the effective value of Required.
func (e EnvVar) IsRequired() bool { return e.Required == nil || *e.Required }

// Resources caps a service's CPU and memory.
type Resources struct {
	CPU    string `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	Memory string `json:"memory,omitempty" yaml:"memory,omitempty"`
}

// DBProvider names the database provider.
type DBProvider string

const (
	DBDocker   DBProvider = "docker"
	DBNeon     DBProvider = "neon"
	DBSupabase DBProvider = "supabase"
	DBDBLab    DBProvider = "dblab"
)

// Database says where the environment's Postgres comes from.
type Database struct {
	Provider     DBProvider `json:"provider,omitempty" yaml:"provider,omitempty"`
	Version      int        `json:"version,omitempty" yaml:"version,omitempty"`
	SourceURLEnv string     `json:"source_url_env,omitempty" yaml:"source_url_env,omitempty"`
	URLEnv       string     `json:"url_env,omitempty" yaml:"url_env,omitempty"`
	MaskingRules string     `json:"masking_rules,omitempty" yaml:"masking_rules,omitempty"`
	// Project identifies the account-side project for a hosted provider, such
	// as a Neon project. It is not a secret and belongs in the manifest; the
	// credential that reaches it does not, which is what APIKeyEnv is for.
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	// APIKeyEnv names the variable holding the provider's API key. Named
	// rather than carried, for the same reason source_url_env is: a manifest is
	// committed and a key is not.
	APIKeyEnv string `json:"api_key_env,omitempty" yaml:"api_key_env,omitempty"`
	// MaxBranches is the plan's concurrent branch limit, where the provider
	// has one it cannot read from its own API.
	MaxBranches int     `json:"max_branches,omitempty" yaml:"max_branches,omitempty"`
	Golden      *Golden `json:"golden,omitempty" yaml:"golden,omitempty"`
	Subset      *Subset `json:"subset,omitempty" yaml:"subset,omitempty"`
	Seed        string  `json:"seed,omitempty" yaml:"seed,omitempty"`
}

// GoldenStorage names where dumps and attestations live.
type GoldenStorage string

const (
	StorageLocal     GoldenStorage = "local"
	StorageAzureBlob GoldenStorage = "azure_blob"
	StorageS3        GoldenStorage = "s3"
)

// Golden configures the masked, verified copy environments branch from.
type Golden struct {
	Schedule   string        `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	MaxAge     string        `json:"max_age,omitempty" yaml:"max_age,omitempty"`
	Retain     int           `json:"retain,omitempty" yaml:"retain,omitempty"`
	Storage    GoldenStorage `json:"storage,omitempty" yaml:"storage,omitempty"`
	StorageURL string        `json:"storage_url,omitempty" yaml:"storage_url,omitempty"`
}

// Subset configures taking a production shaped slice.
type Subset struct {
	Enabled              bool                  `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	SeedTable            string                `json:"seed_table,omitempty" yaml:"seed_table,omitempty"`
	SeedWhere            string                `json:"seed_where,omitempty" yaml:"seed_where,omitempty"`
	MaxRows              int                   `json:"max_rows,omitempty" yaml:"max_rows,omitempty"`
	FollowDependents     *int                  `json:"follow_dependents,omitempty" yaml:"follow_dependents,omitempty"`
	VirtualRelationships []VirtualRelationship `json:"virtual_relationships,omitempty" yaml:"virtual_relationships,omitempty"`
}

// VirtualRelationship declares a link the schema does not.
type VirtualRelationship struct {
	From string `json:"from" yaml:"from"`
	To   string `json:"to" yaml:"to"`
}

// Mode is what happens to an outbound request.
type Mode string

const (
	// ModeBlock refuses with a readable decision.
	ModeBlock Mode = "block"
	// ModeAllow passes through, with an optional rate limit.
	ModeAllow Mode = "allow"
	// ModeCapture records the message into the inbox and returns the
	// provider's documented success shape.
	ModeCapture Mode = "capture"
	// ModeMock answers from a fixture pack or an offline provider pack.
	ModeMock Mode = "mock"
	// ModeSandbox substitutes test credentials and forwards to the provider's
	// sandbox.
	ModeSandbox Mode = "sandbox"
	// ModeSynth asks a model to invent a response, and marks every result that
	// touched it as unverified rather than passed.
	ModeSynth Mode = "synth"
)

// AllModes returns every mode, in the order they appear in the documentation.
func AllModes() []Mode {
	return []Mode{ModeBlock, ModeAllow, ModeCapture, ModeMock, ModeSandbox, ModeSynth}
}

// Egress says what the environment may reach.
type Egress struct {
	Default   Mode         `json:"default,omitempty" yaml:"default,omitempty"`
	AllowIPv6 bool         `json:"allow_ipv6,omitempty" yaml:"allow_ipv6,omitempty"`
	Rules     []EgressRule `json:"rules,omitempty" yaml:"rules,omitempty"`
}

// EgressRule matches a host and says what to do with it.
type EgressRule struct {
	Host        string   `json:"host" yaml:"host"`
	Mode        Mode     `json:"mode" yaml:"mode"`
	Paths       []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	Methods     []string `json:"methods,omitempty" yaml:"methods,omitempty"`
	RateLimit   string   `json:"rate_limit,omitempty" yaml:"rate_limit,omitempty"`
	Credential  string   `json:"credential,omitempty" yaml:"credential,omitempty"`
	Fixtures    string   `json:"fixtures,omitempty" yaml:"fixtures,omitempty"`
	WebhookPath string   `json:"webhook_path,omitempty" yaml:"webhook_path,omitempty"`
	Note        string   `json:"note,omitempty" yaml:"note,omitempty"`
}

// LoginStrategy is how a persona signs in.
type LoginStrategy string

const (
	LoginPassword  LoginStrategy = "password"
	LoginMagicLink LoginStrategy = "magic_link"
	LoginEmailCode LoginStrategy = "email_code"
	LoginSMSCode   LoginStrategy = "sms_code"
	LoginTOTP      LoginStrategy = "totp"
	LoginSession   LoginStrategy = "session"
)

// Persona is an account an agent logs in as.
type Persona struct {
	Name       string            `json:"name" yaml:"name"`
	Email      string            `json:"email,omitempty" yaml:"email,omitempty"`
	Role       string            `json:"role,omitempty" yaml:"role,omitempty"`
	Login      LoginStrategy     `json:"login,omitempty" yaml:"login,omitempty"`
	MFA        bool              `json:"mfa,omitempty" yaml:"mfa,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty" yaml:"attributes,omitempty"`
}

// Workflow is something an agent does.
type Workflow struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	Persona     string   `json:"persona,omitempty" yaml:"persona,omitempty"`
	StartPath   string   `json:"start_path,omitempty" yaml:"start_path,omitempty"`
	Independent bool     `json:"independent,omitempty" yaml:"independent,omitempty"`
	Budget      *Budget  `json:"budget,omitempty" yaml:"budget,omitempty"`
	Expect      []string `json:"expect,omitempty" yaml:"expect,omitempty"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// Budget caps what one workflow may consume.
type Budget struct {
	Steps    int     `json:"steps,omitempty" yaml:"steps,omitempty"`
	USD      float64 `json:"usd,omitempty" yaml:"usd,omitempty"`
	Duration string  `json:"duration,omitempty" yaml:"duration,omitempty"`
}

// Invariant is a read only statement that must return no rows.
type Invariant struct {
	Name        string `json:"name" yaml:"name"`
	SQL         string `json:"sql" yaml:"sql"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Insights configures the Postgres native checks.
type Insights struct {
	Enabled            *bool   `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	MigrationRehearsal *bool   `json:"migration_rehearsal,omitempty" yaml:"migration_rehearsal,omitempty"`
	QueryRegression    *bool   `json:"query_regression,omitempty" yaml:"query_regression,omitempty"`
	PlanDiff           *bool   `json:"plan_diff,omitempty" yaml:"plan_diff,omitempty"`
	RegressionFactor   float64 `json:"regression_factor,omitempty" yaml:"regression_factor,omitempty"`
	RegressionMinMS    float64 `json:"regression_min_ms,omitempty" yaml:"regression_min_ms,omitempty"`
	LargeTableRows     int     `json:"large_table_rows,omitempty" yaml:"large_table_rows,omitempty"`
}

// LoadSource names where the endpoint mix comes from.
type LoadSource string

const (
	LoadNone      LoadSource = "none"
	LoadDatadog   LoadSource = "datadog"
	LoadNewRelic  LoadSource = "newrelic"
	LoadOTel      LoadSource = "otel"
	LoadAccessLog LoadSource = "access_log"
)

// Load configures production shaped traffic.
type Load struct {
	Enabled      bool              `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Source       LoadSource        `json:"source,omitempty" yaml:"source,omitempty"`
	SourceConfig map[string]string `json:"source_config,omitempty" yaml:"source_config,omitempty"`
	Scale        float64           `json:"scale,omitempty" yaml:"scale,omitempty"`
	Duration     string            `json:"duration,omitempty" yaml:"duration,omitempty"`
	SafeRoutes   []string          `json:"safe_routes,omitempty" yaml:"safe_routes,omitempty"`
	UnsafeRoutes []string          `json:"unsafe_routes,omitempty" yaml:"unsafe_routes,omitempty"`
	Thresholds   *LoadThresholds   `json:"thresholds,omitempty" yaml:"thresholds,omitempty"`
}

// LoadThresholds are the deltas that fail a run.
type LoadThresholds struct {
	P95Increase        float64 `json:"p95_increase,omitempty" yaml:"p95_increase,omitempty"`
	ErrorRate          float64 `json:"error_rate,omitempty" yaml:"error_rate,omitempty"`
	QueryCountIncrease float64 `json:"query_count_increase,omitempty" yaml:"query_count_increase,omitempty"`
}

// RuntimeProvider names where environments run.
type RuntimeProvider string

const (
	RuntimeLocal      RuntimeProvider = "local"
	RuntimeKubernetes RuntimeProvider = "kubernetes"
)

// Runtime configures where and how long environments run.
type Runtime struct {
	Provider          RuntimeProvider `json:"provider,omitempty" yaml:"provider,omitempty"`
	TTL               string          `json:"ttl,omitempty" yaml:"ttl,omitempty"`
	IdleSleep         string          `json:"idle_sleep,omitempty" yaml:"idle_sleep,omitempty"`
	Domain            string          `json:"domain,omitempty" yaml:"domain,omitempty"`
	NamespacePrefix   string          `json:"namespace_prefix,omitempty" yaml:"namespace_prefix,omitempty"`
	KubeconfigContext string          `json:"kubeconfig_context,omitempty" yaml:"kubeconfig_context,omitempty"`
}

// GitHubMode is how the GitHub integration runs.
type GitHubMode string

const (
	// GitHubActions runs everything inside a workflow, with no server.
	GitHubActions GitHubMode = "actions"
	// GitHubApp uses the GitHub App and the control plane.
	GitHubApp GitHubMode = "app"
	// GitHubOff disables the integration.
	GitHubOff GitHubMode = "off"
)

// ForkPolicy is what to do with a pull request from a fork.
type ForkPolicy string

const (
	ForkNever  ForkPolicy = "never"
	ForkLabel  ForkPolicy = "label"
	ForkAlways ForkPolicy = "always"
)

// GitHub configures the pull request integration.
type GitHub struct {
	Mode       GitHubMode `json:"mode,omitempty" yaml:"mode,omitempty"`
	Comment    *bool      `json:"comment,omitempty" yaml:"comment,omitempty"`
	ForkPolicy ForkPolicy `json:"fork_policy,omitempty" yaml:"fork_policy,omitempty"`
	TeardownOn []string   `json:"teardown_on,omitempty" yaml:"teardown_on,omitempty"`
}
