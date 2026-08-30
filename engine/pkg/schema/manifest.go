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
	Auth       *Auth       `json:"auth,omitempty" yaml:"auth,omitempty"`
	Workflows  []Workflow  `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	Invariants []Invariant `json:"invariants,omitempty" yaml:"invariants,omitempty"`
	Insights   *Insights   `json:"insights,omitempty" yaml:"insights,omitempty"`
	Oracle     *Oracle     `json:"oracle,omitempty" yaml:"oracle,omitempty"`
	Explore    *Explore    `json:"explore,omitempty" yaml:"explore,omitempty"`
	Load       *Load       `json:"load,omitempty" yaml:"load,omitempty"`
	Policy     *Policy     `json:"policy,omitempty" yaml:"policy,omitempty"`
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
	Phone      string            `json:"phone,omitempty" yaml:"phone,omitempty"`
	MFA        bool              `json:"mfa,omitempty" yaml:"mfa,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty" yaml:"attributes,omitempty"`
}

// AuthAdapter names how personas are created.
type AuthAdapter string

const (
	// AuthAuto picks the adapter from the dependencies and the live schema,
	// which is what a manifest that says nothing gets.
	AuthAuto AuthAdapter = "auto"
	// AuthDirect writes rows into the application's own users table.
	AuthDirect AuthAdapter = "direct"
	// AuthSupabase writes rows into Supabase's auth schema.
	AuthSupabase AuthAdapter = "supabase"
	// AuthSupabaseAPI goes through a Supabase project's auth admin API,
	// which hashes the password the way its own signup does.
	AuthSupabaseAPI AuthAdapter = "supabase_api"
	// AuthNextAuth writes rows into the NextAuth and Auth.js tables.
	AuthNextAuth AuthAdapter = "nextauth"
	// AuthClerk creates personas through Clerk's backend API.
	AuthClerk AuthAdapter = "clerk"
	// AuthAuth0 creates personas through the Auth0 Management API.
	AuthAuth0 AuthAdapter = "auth0"
	// AuthWorkOS creates personas through WorkOS User Management.
	AuthWorkOS AuthAdapter = "workos"
	// AuthSeed runs a command the manifest names, for a scheme nothing else
	// covers.
	AuthSeed AuthAdapter = "seed"
)

// Auth configures how personas come to exist.
//
// Absent from most manifests, because detection answers it. Present when
// detection is wrong, when the users table has names nothing could guess, or
// when the application's users live somewhere only a script can reach.
type Auth struct {
	Adapter AuthAdapter `json:"adapter,omitempty" yaml:"adapter,omitempty"`
	// Seed is the command AuthSeed runs, once per persona.
	Seed string `json:"seed,omitempty" yaml:"seed,omitempty"`
	// Sandbox declares that the configured tenant is not the production one.
	// A hosted adapter refuses to create anybody without it, because the only
	// tenant it could otherwise fall back to is the real one.
	Sandbox bool `json:"sandbox,omitempty" yaml:"sandbox,omitempty"`
	// TokenEnv names the variable holding the provider's admin credential.
	// The variable name, never the credential.
	TokenEnv string `json:"token_env,omitempty" yaml:"token_env,omitempty"`
	// URL is the project's API root, for Supabase.
	URL string `json:"url,omitempty" yaml:"url,omitempty"`
	// Domain is the tenant, for Auth0.
	Domain string `json:"domain,omitempty" yaml:"domain,omitempty"`
	// Connection is the Auth0 database connection users are created in.
	Connection string `json:"connection,omitempty" yaml:"connection,omitempty"`
	// Table describes the application's own users table, for AuthDirect.
	Table *AuthTable `json:"table,omitempty" yaml:"table,omitempty"`
	// Sessions are extra tables holding sessions or tokens, emptied so that
	// no real session survives into a branch.
	Sessions []string `json:"sessions,omitempty" yaml:"sessions,omitempty"`
	// Password shapes the generated password for an application whose rules
	// are stricter than the generator's.
	Password *PasswordRules `json:"password,omitempty" yaml:"password,omitempty"`
}

// AuthTable names the columns of an application's own users table.
type AuthTable struct {
	Schema     string            `json:"schema,omitempty" yaml:"schema,omitempty"`
	Name       string            `json:"name" yaml:"name"`
	ID         string            `json:"id,omitempty" yaml:"id,omitempty"`
	Email      string            `json:"email,omitempty" yaml:"email,omitempty"`
	Password   string            `json:"password,omitempty" yaml:"password,omitempty"`
	Role       string            `json:"role,omitempty" yaml:"role,omitempty"`
	JSON       string            `json:"json,omitempty" yaml:"json,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty" yaml:"attributes,omitempty"`
	Timestamps []string          `json:"timestamps,omitempty" yaml:"timestamps,omitempty"`
}

// PasswordRules describe an application's password policy.
//
// Stated so that the generated password satisfies it. Without this, an
// application stricter than the generator refuses a correct password at sign
// in, and the run reports a login failure that looks like the application's
// fault.
type PasswordRules struct {
	MinLength int    `json:"min_length,omitempty" yaml:"min_length,omitempty"`
	Symbols   string `json:"symbols,omitempty" yaml:"symbols,omitempty"`
	Forbid    string `json:"forbid,omitempty" yaml:"forbid,omitempty"`
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
	// RollingCompatibility is the check that runs the PREVIOUS release against
	// the migrated schema. It is a block rather than a bool because two of its
	// three answers are not on and off: which commit the previous release is,
	// and whether to pay for the check when the migration cannot break
	// anything.
	RollingCompatibility *RollingCompatibility `json:"rolling_compatibility,omitempty" yaml:"rolling_compatibility,omitempty"`
}

// RollingCompatibility configures the rolling deploy check.
type RollingCompatibility struct {
	// When is never, risky or always. Risky is the default and runs the check
	// only when the pending migrations contain a change the previous release
	// could notice.
	When string `json:"when,omitempty" yaml:"when,omitempty"`
	// Against names the previous release: merge-base, previous-commit, or any
	// revision git can resolve, such as a tag.
	Against string `json:"against,omitempty" yaml:"against,omitempty"`
}

// BaselineSource names how the version to compare against is chosen.
//
// The two answer different questions and a project has to say which it wants.
// The merge base answers "what does this branch change", and it does not move
// when somebody else lands a commit on main halfway through a review. An
// explicit ref answers "what changes when this ships", which is what a release
// gate wants and what a tag names. There is no third value for "the revision
// currently deployed", because the engine has no way to know what that is: a
// deployment pipeline does, and it passes the commit as base_ref.
type BaselineSource string

const (
	// BaselineMergeBase is the commit this branch and the base branch share.
	BaselineMergeBase BaselineSource = "merge_base"
	// BaselineRef is a git ref named outright: a branch, a tag, or a commit.
	BaselineRef BaselineSource = "ref"
)

// Oracle configures the differential comparison of two versions.
type Oracle struct {
	Enabled           *bool           `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Baseline          BaselineSource  `json:"baseline,omitempty" yaml:"baseline,omitempty"`
	BaseRef           string          `json:"base_ref,omitempty" yaml:"base_ref,omitempty"`
	FailOn            string          `json:"fail_on,omitempty" yaml:"fail_on,omitempty"`
	Probes            []Probe         `json:"probes,omitempty" yaml:"probes,omitempty"`
	CompareTimestamps bool            `json:"compare_timestamps,omitempty" yaml:"compare_timestamps,omitempty"`
	CompareUUIDs      bool            `json:"compare_uuids,omitempty" yaml:"compare_uuids,omitempty"`
	Ignore            *OracleIgnore   `json:"ignore,omitempty" yaml:"ignore,omitempty"`
	Database          *OracleDatabase `json:"database,omitempty" yaml:"database,omitempty"`
}

// Probe is one request sent to both versions.
//
// Written down rather than discovered, because both sides have to receive the
// same bytes in the same order. The agents that drive a workflow decide their
// next step from what is on the screen, so two runs of one workflow send two
// different request sequences, and a diff of those compares the agent with
// itself.
type Probe struct {
	Name    string            `json:"name" yaml:"name"`
	Method  string            `json:"method,omitempty" yaml:"method,omitempty"`
	Path    string            `json:"path" yaml:"path"`
	Headers map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body    string            `json:"body,omitempty" yaml:"body,omitempty"`
}

// OracleIgnore is what the comparison is told not to look at.
//
// Everything named here is printed in the report along with the defaults, so a
// reader can see what was skipped rather than wondering.
type OracleIgnore struct {
	Headers []string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Fields  []string `json:"fields,omitempty" yaml:"fields,omitempty"`
}

// OracleDatabase configures the comparison of the two branches' contents.
type OracleDatabase struct {
	Enabled *bool    `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Tables  []string `json:"tables,omitempty" yaml:"tables,omitempty"`
	Exclude []string `json:"exclude,omitempty" yaml:"exclude,omitempty"`
	MaxRows int      `json:"max_rows,omitempty" yaml:"max_rows,omitempty"`
}

// Explore configures the exploratory runs.
//
// A separate block from Workflows rather than a flag on one, because the two
// are different things asked of the same browser. A workflow declares an
// outcome and is judged against it; a goal declares an intention and is judged
// against nothing, which is why an exploration cannot fail a build. Overloading
// workflows[] would also collide with its own validation: a workflow needs a
// description of at least four words to plan from and a persona to run as, and
// a goal needs neither.
type Explore struct {
	Enabled bool   `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Goals   []Goal `json:"goals,omitempty" yaml:"goals,omitempty"`
}

// Goal is one thing an exploratory agent tries to achieve.
type Goal struct {
	Name string `json:"name" yaml:"name"`
	// Goal is the sentence, written the way somebody would say it out loud.
	// The agent has no script, so this is the only thing telling it where to
	// go, and it is what the run is judged to have reached or not reached.
	Goal    string `json:"goal" yaml:"goal"`
	Persona string `json:"persona,omitempty" yaml:"persona,omitempty"`
	// Seed decides every choice the agent makes. The same seed against the
	// same application takes the same path, which is what makes a finding
	// something somebody can replay rather than something they have to
	// believe. Defaults to the goal's name.
	Seed      string `json:"seed,omitempty" yaml:"seed,omitempty"`
	StartPath string `json:"start_path,omitempty" yaml:"start_path,omitempty"`
	// SlowMs is how long one step may take before it is reported as friction.
	SlowMs int     `json:"slow_ms,omitempty" yaml:"slow_ms,omitempty"`
	Budget *Budget `json:"budget,omitempty" yaml:"budget,omitempty"`
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

// PolicyLevel is what one class of finding does to the check.
//
// Three levels rather than two, because a real finding that does not stop a
// merge had nowhere to go before this: everything the run noticed either
// failed the build or was printed and forgotten. A rewrite on a table of four
// hundred rows is worth a line in the comment and is not worth blocking on,
// and a policy that can only say fail or nothing teaches people to say
// nothing.
type PolicyLevel string

const (
	// PolicyIgnore drops the finding entirely. It is not printed and it does
	// not reach the verdict.
	PolicyIgnore PolicyLevel = "ignore"
	// PolicyWarn reports the finding and leaves the check passing.
	PolicyWarn PolicyLevel = "warn"
	// PolicyFail reports the finding and fails the check.
	PolicyFail PolicyLevel = "fail"
)

// AllPolicyLevels returns every level, weakest first. Kept so the schema, the
// validator and the documentation cannot drift.
func AllPolicyLevels() []PolicyLevel {
	return []PolicyLevel{PolicyIgnore, PolicyWarn, PolicyFail}
}

// Policy is what each class of finding does to the pull request check.
//
// It exists because "pass, warning, or block" was a sentence on a web page and
// nothing in the engine could produce the middle one. Every key here is read
// by af ci when it builds the report, and each maps one kind of evidence to
// one level, so that the answer to "why did this fail" is always a key in this
// block rather than a rule somebody has to read Go to find.
type Policy struct {
	// MigrationLock is how long a migration may hold a lock on a table.
	MigrationLock *LockPolicy `json:"migration_lock,omitempty" yaml:"migration_lock,omitempty"`
	// MigrationFailed is a migration that did not apply to a branch with
	// production's shape in it.
	MigrationFailed PolicyLevel `json:"migration_failed,omitempty" yaml:"migration_failed,omitempty"`
	// MigrationRewrite is a statement Postgres reported as rewriting a table.
	MigrationRewrite PolicyLevel `json:"migration_rewrite,omitempty" yaml:"migration_rewrite,omitempty"`
	// MigrationLint governs all six lint rules together. They are one setting
	// because a project that wants the lint wants all of it: the rules are
	// already scoped by table size, so the noisy case is handled by
	// insights.large_table_rows rather than by turning a rule off.
	MigrationLint PolicyLevel `json:"migration_lint,omitempty" yaml:"migration_lint,omitempty"`
	// PlanRegression is a query plan that got worse.
	PlanRegression PolicyLevel `json:"plan_regression,omitempty" yaml:"plan_regression,omitempty"`
	// QueryRegression is a statement that runs more often or slower than the
	// baseline did.
	QueryRegression PolicyLevel `json:"query_regression,omitempty" yaml:"query_regression,omitempty"`
	// LoadRegression is a load threshold from the load block being exceeded.
	LoadRegression PolicyLevel `json:"load_regression,omitempty" yaml:"load_regression,omitempty"`
	// EgressSurprise is the environment trying to reach a host the manifest
	// does not mention.
	EgressSurprise PolicyLevel `json:"egress_surprise,omitempty" yaml:"egress_surprise,omitempty"`
	// Masking is the environment's own branch reading back with something in
	// it that still parses as real data.
	Masking PolicyLevel `json:"masking,omitempty" yaml:"masking,omitempty"`
	// Cleanup is teardown leaving a resource behind.
	Cleanup PolicyLevel `json:"cleanup,omitempty" yaml:"cleanup,omitempty"`
}

// LockPolicy is the two thresholds on how long a migration held a lock.
//
// Two numbers rather than one because the interesting range is wide: a lock
// held for a fifth of a second is a pause, one held for five seconds is an
// outage on a busy table, and there is no single figure that is right for
// both. Both are compared against a sampled lower bound, so a run that
// breaches one really did hold the lock at least that long.
type LockPolicy struct {
	// WarnMS reports a lock held at least this long.
	WarnMS float64 `json:"warn_ms,omitempty" yaml:"warn_ms,omitempty"`
	// FailMS fails the check on a lock held at least this long. It must not be
	// below WarnMS, which the validator enforces.
	FailMS float64 `json:"fail_ms,omitempty" yaml:"fail_ms,omitempty"`
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
