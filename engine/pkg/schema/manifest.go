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
	Datastores []Datastore `json:"datastores,omitempty" yaml:"datastores,omitempty"`
	Egress     *Egress     `json:"egress,omitempty" yaml:"egress,omitempty"`
	Personas   []Persona   `json:"personas,omitempty" yaml:"personas,omitempty"`
	Auth       *Auth       `json:"auth,omitempty" yaml:"auth,omitempty"`
	Workflows  []Workflow  `json:"workflows,omitempty" yaml:"workflows,omitempty"`
	// TerminalWorkflows are the workflows driven at a command line rather
	// than in a browser. Their own list rather than a surface key on
	// Workflow: the two share the sentence and nothing else, and see the
	// schema's terminal_workflow description for why that is a list and not
	// a conditional.
	TerminalWorkflows []TerminalWorkflow `json:"terminal_workflows,omitempty" yaml:"terminal_workflows,omitempty"`
	// Desktop is which application the workflows driving SurfaceDesktop are
	// driven in. Declared once rather than per workflow, because a manifest
	// describes one product, and required by a manifest that names the
	// surface at all: without it the run reaches the runner and is refused
	// there for want of something to open.
	Desktop        *DesktopApplication `json:"desktop,omitempty" yaml:"desktop,omitempty"`
	Invariants     []Invariant         `json:"invariants,omitempty" yaml:"invariants,omitempty"`
	Insights       *Insights           `json:"insights,omitempty" yaml:"insights,omitempty"`
	Change         *Change             `json:"change,omitempty" yaml:"change,omitempty"`
	Oracle         *Oracle             `json:"oracle,omitempty" yaml:"oracle,omitempty"`
	Explore        *Explore            `json:"explore,omitempty" yaml:"explore,omitempty"`
	Diversity      *Diversity          `json:"diversity,omitempty" yaml:"diversity,omitempty"`
	Fidelity       *Fidelity           `json:"fidelity,omitempty" yaml:"fidelity,omitempty"`
	Load           *Load               `json:"load,omitempty" yaml:"load,omitempty"`
	Policy         *Policy             `json:"policy,omitempty" yaml:"policy,omitempty"`
	Runtime        *Runtime            `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	Infrastructure *Infrastructure     `json:"infrastructure,omitempty" yaml:"infrastructure,omitempty"`
	GitHub         *GitHub             `json:"github,omitempty" yaml:"github,omitempty"`
	Security       *Security           `json:"security,omitempty" yaml:"security,omitempty"`
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

// MaxReplicas bounds the instance count a service may ask for.
//
// Ten, matching schemas/manifest.v1.json. The bound exists because an
// environment is a copy of production on one developer's machine rather than
// production itself, and a manifest asking for forty instances of a worker
// does not reproduce anything: it exhausts the machine and reports a runtime
// failure that has nothing to do with the change under test. Three is enough
// to find every bug in the class this field exists for.
const MaxReplicas = 10

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
	// Scope says whose value this is. Empty is the environment's: every
	// service that declares the name and reads it from the same place gets
	// the same value. ScopeService is this service's own, stored under
	// ScopedName and never readable by another service.
	Scope EnvScope `json:"scope,omitempty" yaml:"scope,omitempty"`
}

// EnvScope names whose value a variable is.
type EnvScope string

// ScopeService is a value that belongs to one service. It exists because a
// published stack can give two services one variable name with two different
// credentials in it, Supabase's storage and supavisor both reading
// DATABASE_URL, and a lookup keyed by the name alone could only ever hand both
// of them the same one.
const ScopeService EnvScope = "service"

// IsRequired reports the effective value of Required.
func (e EnvVar) IsRequired() bool { return e.Required == nil || *e.Required }

// Resources is the size a service is given, and it is a single number per
// dimension on purpose.
//
// Each value becomes BOTH the request and the limit. On Kubernetes that is the
// Guaranteed quality of service class: the scheduler reserves exactly what the
// manifest asked for and the kubelet caps the container at the same figure. On
// the local runtime, where there is no scheduler to reserve anything, it is the
// daemon's own cpu and memory constraint.
//
// A request that is smaller than the limit is the shape most people know, and
// it is deliberately not what this is. The gap between the two is where a node
// is oversubscribed: every container is placed against its request and may then
// grow into its limit, so a machine that fits ten environments on paper runs
// eleven and the eleventh takes memory from the others. The symptom is a
// workflow that reads as flaky, and a twin whose failures are the machine's
// rather than the change's is worth less than no twin. One number means the
// environment gets what it asked for and takes no more, and it means
// environments per node is a division rather than a guess.
//
// Both are optional and independent: a service may cap CPU alone, memory
// alone, or neither.
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
	// DBPgURL is any reachable Postgres, named by a connection string rather
	// than by an account. It is the provider for every server nobody wrote a
	// provider for.
	DBPgURL DBProvider = "pgurl"
	// DBXata is Xata, whose branches are copy on write snapshots at the
	// storage layer. database.project holds "<organization>/<project>",
	// because both identifiers are path segments of every call the provider
	// makes and neither can be discovered from the other.
	DBXata DBProvider = "xata"
)

// Database says where the environment's Postgres comes from.
type Database struct {
	Provider DBProvider `json:"provider,omitempty" yaml:"provider,omitempty"`
	Version  int        `json:"version,omitempty" yaml:"version,omitempty"`
	// Image is the container image the docker provider runs Postgres from,
	// instead of the stock one built from Version. It is how a schema that
	// needs PostGIS, pgvector, TimescaleDB or a custom table access method
	// gets a golden at all.
	Image string `json:"image,omitempty" yaml:"image,omitempty"`
	// Extensions are created in the golden before the source is copied into
	// it. An extension present in the image and never created carries none of
	// its types, operators or table access methods, so declaring the image is
	// only half of the answer.
	Extensions []string `json:"extensions,omitempty" yaml:"extensions,omitempty"`
	// PreloadLibraries are ADDED to shared_preload_libraries, never a
	// replacement for it: pg_stat_statements has to stay, or the insights read
	// a permanently empty table. They lead the list, because citus refuses to
	// load from anywhere but the front and the statistics module does not care
	// where it sits.
	PreloadLibraries []string `json:"preload_libraries,omitempty" yaml:"preload_libraries,omitempty"`
	SourceURLEnv     string   `json:"source_url_env,omitempty" yaml:"source_url_env,omitempty"`
	URLEnv           string   `json:"url_env,omitempty" yaml:"url_env,omitempty"`
	MaskingRules     string   `json:"masking_rules,omitempty" yaml:"masking_rules,omitempty"`
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
	// Migrations says where the SQL migrations live, for a project whose
	// migrate command is its own script rather than a tool the rehearsal
	// recognises from the tree. Declared, nothing is inferred.
	Migrations *Migrations `json:"migrations,omitempty" yaml:"migrations,omitempty"`
	// Volume names the committed profile of what production holds, which is
	// the denominator every row count in a report is measured against.
	Volume *Volume `json:"volume,omitempty" yaml:"volume,omitempty"`
}

// Volume names the committed record of production's own size.
//
// Without one, a fidelity report says a branch holds twelve tables over a
// hundred thousand rows and has nothing to compare that against, so a golden
// built from a staging database with two hundred rows in it reports as
// reproducing a production holding four billion. The profile is what turns
// that sentence into a fraction.
//
// It is a path rather than a connection, deliberately. The profile is
// collected once from a read only connection to production or a replica, and
// committed; every machine that reads it afterwards, including a pull request
// check that can reach nothing, reads a file.
type Volume struct {
	// Profile is the artifact, relative to the repository root. Written by
	// af volume record and read by everything that needs a denominator.
	Profile string `json:"profile" yaml:"profile"`
	// MaxAge is how old the profile may be before it is refused. A stale
	// profile is not a smaller number, it is an unknown one, so it is refused
	// the way a stale golden is rather than quoted.
	MaxAge string `json:"max_age,omitempty" yaml:"max_age,omitempty"`
}

// Migrations declares a project's own migration files.
//
// The rehearsal recognises Prisma, Supabase, Drizzle, Flyway and a handful of
// others from their marker files. A project that applies a directory of
// numbered SQL files with a script of its own has no marker to recognise, and
// the rehearsal used to answer INCONCLUSIVE for it while the manifest declared
// lock thresholds that could never fire. This names the directory instead.
type Migrations struct {
	// Dir is the directory of .sql files, relative to the repository root.
	Dir string `json:"dir" yaml:"dir"`
	// Format is how the files are read. Only sql exists, and it is the
	// default; it is here so a second format can be added without a second
	// key.
	Format string `json:"format,omitempty" yaml:"format,omitempty"`
	// Table is the ledger the project's own runner records applied files in,
	// so the rehearsal can tell what is pending against a branch the way the
	// runner itself would. Empty probes the usual names.
	Table string `json:"table,omitempty" yaml:"table,omitempty"`
}

// GoldenStorage names where dumps and attestations live.
type GoldenStorage string

const (
	StorageLocal     GoldenStorage = "local"
	StorageAzureBlob GoldenStorage = "azure_blob"
	StorageS3        GoldenStorage = "s3"
	StorageGCS       GoldenStorage = "gcs"
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

// DatastoreStance is what the environment does about one datastore's contents.
//
// A stance is DECLARED, never defaulted, and a datastore that names none is
// refused at validation. That refusal is the whole reason this type is a
// closed set rather than a string with a sensible fallback. An analytics
// product's twin held a masked Postgres and zero events, because the events
// live in ClickHouse and ClickHouse came up empty; nobody chose that, and
// nothing said it out loud. A default here would have chosen it again, once
// per manifest, silently.
//
// Not every store should be cloned, and pretending otherwise is its own
// failure. A cache is CORRECT to start empty and a copy of one would be noise.
// So empty is a legitimate answer. An invisible empty is not.
type DatastoreStance string

const (
	// StanceGolden is a masked, verified copy that environments branch from,
	// which is what the primary database has always had.
	StanceGolden DatastoreStance = "golden"
	// StanceEmpty starts the store with nothing in it, on purpose, and the
	// manifest says why. A cache rebuilt from the primary is the case this
	// exists for.
	StanceEmpty DatastoreStance = "empty"
	// StanceDerived rebuilds the store from another one after that one is
	// ready, which is how a search index is built from the Postgres branch
	// rather than cloned separately and left stale against it.
	StanceDerived DatastoreStance = "derived"
	// StanceTopicsOnly creates topics and consumer groups with no messages,
	// which is what a broker usually wants and a replay of production traffic
	// is not.
	StanceTopicsOnly DatastoreStance = "topics_only"
)

// AllDatastoreStances returns every stance, in the order the documentation
// introduces them.
func AllDatastoreStances() []DatastoreStance {
	return []DatastoreStance{StanceGolden, StanceEmpty, StanceDerived, StanceTopicsOnly}
}

// PrimaryDatastore is the name the primary database normalizes into.
//
// database: stays exactly as it was and becomes the datastores entry called
// this, so every manifest written before the list existed keeps working and
// there is one code path afterwards rather than two. It is reserved: a
// manifest cannot declare a second datastore under this name.
const PrimaryDatastore = "primary"

// Datastore is one store the environment holds, and what is done about its
// contents.
//
// The list exists because Database is a single struct and it is Postgres.
// There was one golden, one masking pass, one verification scan and one
// branch, and everything else a manifest declared was an empty container that
// no part of the report mentioned. A stack of an application, six workers,
// ClickHouse, Redis and Kafka STARTED, which is genuinely a lot, and held
// production's data in exactly one of those five places.
type Datastore struct {
	// Name identifies the store in the environment and in the report. Unique
	// within the manifest, and "primary" is reserved for the entry database:
	// normalizes into.
	Name string `json:"name" yaml:"name"`
	// Engine is what the store runs, such as postgres, clickhouse or redis. It
	// is open rather than a closed set on purpose: the closed set here would
	// be a list of engines this build happens to have a provider for, and a
	// manifest that names one it does not have is refused by the provider
	// lookup, by name, which is a better message than "unknown engine".
	Engine string `json:"engine" yaml:"engine"`
	// Provider names the implementation, for an engine that more than one
	// thing can provide. Empty means the engine's own default.
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	// Stance is what happens to this store's contents. Required; a datastore
	// that declares none is refused.
	Stance DatastoreStance `json:"stance,omitempty" yaml:"stance,omitempty"`
	// Because is the declared reason for the stance, in the words of whoever
	// chose it, and it is carried into the fidelity report as written. An
	// empty store nobody explained and an empty store somebody decided on look
	// identical in a running environment; this is the only thing that tells
	// them apart afterwards.
	Because string `json:"because,omitempty" yaml:"because,omitempty"`
	// From names the datastore a derived store is rebuilt from. Required for
	// the derived stance and refused for the others.
	From string `json:"from,omitempty" yaml:"from,omitempty"`
	// SourceURLEnv names the variable holding this store's production
	// connection string, which is what a golden of it is copied from.
	//
	// The same key database.source_url_env is, arriving for the second store,
	// and it is a variable name rather than a URL for the same reason: the
	// value is a credential for production and a manifest is checked in.
	//
	// Empty is allowed and produces an EMPTY golden, which is said out loud
	// on every refresh rather than left to be discovered. That is the same
	// answer the primary database gives a project that has not connected
	// production yet, and it is deliberately not a refusal: a store whose
	// tables are created by migrations is still worth branching, and refusing
	// would make the first `af up` on a new project impossible.
	SourceURLEnv string `json:"source_url_env,omitempty" yaml:"source_url_env,omitempty"`
	// Topics are the topics a topics_only broker is created with, and the
	// consumer groups created against them. Required for that stance and
	// refused for the others.
	//
	// Declared rather than discovered, because there is nothing to discover:
	// a broker's topics live in production and copying the messages in them
	// is the thing this stance exists to refuse. What a twin needs is the
	// SHAPE, so that a consumer subscribing to a topic finds it and a
	// producer writing to one is not creating it by accident, and the shape
	// is something only the person writing the manifest knows.
	Topics []DatastoreTopic `json:"topics,omitempty" yaml:"topics,omitempty"`
	// Rebuild is the command that builds a derived store from the one named
	// in From. Required for that stance and refused for the others.
	Rebuild *DatastoreRebuild `json:"rebuild,omitempty" yaml:"rebuild,omitempty"`
}

// DatastoreTopic is one topic a topics_only broker is created with.
type DatastoreTopic struct {
	// Name is the topic.
	Name string `json:"name" yaml:"name"`
	// Partitions is how many the topic is created with. Zero means one,
	// which is what a broker does with an unspecified count.
	//
	// It is here because a partition count is not cosmetic: a consumer group
	// with more members than partitions leaves members idle, and ordering is
	// per partition, so a twin whose topic has one partition where production
	// has twelve cannot reproduce a reordering bug at all.
	Partitions int `json:"partitions,omitempty" yaml:"partitions,omitempty"`
	// ConsumerGroups are the groups created against this topic, with their
	// offsets committed and no messages behind them.
	//
	// A group is created rather than left to appear on its own because a
	// consumer that joins a group nobody created reads from the end by
	// default, so the twin's first run of a consumer silently skips whatever
	// the twin's own producers wrote before it started.
	ConsumerGroups []string `json:"consumer_groups,omitempty" yaml:"consumer_groups,omitempty"`
}

// DatastoreRebuild is how a derived store is built from the one it reads.
//
// A command rather than a copy, and that is the whole argument for the stance.
// A search index cloned from production is stale against the branch the moment
// the branch is masked: the documents in it name people who do not exist in
// the twin's Postgres, so a search returns a row a join cannot resolve. An
// index BUILT from the branch cannot be stale against it, because the branch
// is what it read.
type DatastoreRebuild struct {
	// Service names the service whose image the command runs in. It is the
	// application's own image in almost every case, because the code that
	// knows how to index this product's rows is the product's code.
	Service string `json:"service" yaml:"service"`
	// Command is what rebuilds the store. It runs once, to completion, inside
	// the environment, after every service is up, and a non-zero exit fails
	// the environment rather than leaving an index nobody built.
	Command string `json:"command" yaml:"command"`
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
	// ModeEmulate answers from an emulator running inside the environment,
	// which the application reaches with no endpoint override and no client
	// construction that only exists in tests.
	//
	// It is the one mode that is pure routing. Antifailure writes no
	// emulators: LocalStack, Azurite and the vendors' own emulators exist and
	// carry years of fidelity work a hand written replacement would not have.
	// What is missing from all of them is that using one normally means
	// changing the application, and an application changed for the test is not
	// the application that ships. So the sidecar terminates TLS with the
	// certificate the environment already trusts, answers for the provider's
	// own hostname, and forwards to the emulator's address on the
	// environment's own network. The unmodified production code path runs.
	//
	// The emulator is named by the rule and supplied by a registration, which
	// is what keeps this mode from being a second implementation of mock: mock
	// answers from a fixture the sidecar holds, and emulate hands the request
	// to a real service that holds state.
	ModeEmulate Mode = "emulate"
	// ModeSynth asks a model to invent a response, and marks every result that
	// touched it as unverified rather than passed.
	ModeSynth Mode = "synth"
)

// AllModes returns every mode, in the order they appear in the documentation.
func AllModes() []Mode {
	return []Mode{
		ModeBlock, ModeAllow, ModeCapture, ModeMock, ModeEmulate, ModeSandbox, ModeSynth,
	}
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
	// Emulator names the registered emulator that answers this host, for a
	// rule in emulate mode. Required there and refused on every other mode.
	//
	// A name rather than an image, for the same reason database.provider is a
	// name: the image, its digest, the port and what it is started with belong
	// to whoever registered the emulator and are checked when they register
	// it, and a manifest that could name an image could name any image.
	//
	// A name this build has not registered is refused at validation rather
	// than falling through to block, because a rule that silently does
	// nothing is how somebody comes to believe an environment was tested
	// against S3.
	Emulator string `json:"emulator,omitempty" yaml:"emulator,omitempty"`
}

// LoginStrategy is how a persona signs in.
type LoginStrategy string

const (
	// LoginNone is a persona that never signs in, for an application with no
	// sign in at all or a workflow about a signed out visitor. The agent goes
	// straight to the workflow's start path.
	LoginNone      LoginStrategy = "none"
	LoginPassword  LoginStrategy = "password"
	LoginMagicLink LoginStrategy = "magic_link"
	LoginEmailCode LoginStrategy = "email_code"
	LoginSMSCode   LoginStrategy = "sms_code"
	LoginTOTP      LoginStrategy = "totp"
	LoginSession   LoginStrategy = "session"
)

// Persona is an account an agent logs in as.
type Persona struct {
	Name  string `json:"name" yaml:"name"`
	Email string `json:"email,omitempty" yaml:"email,omitempty"`
	Role  string `json:"role,omitempty" yaml:"role,omitempty"`
	// Tenant is the account boundary this persona belongs to, an identity label
	// and nothing more: it is never a credential and it does not change how the
	// persona is provisioned or signs in. It exists so the security suite's
	// access-probe pass can decide a cross-tenant reach, where an actor in one
	// tenant reaches an object owned by another. Absent when the application has
	// no tenant boundary, which is most of them, and then only the same-tenant
	// idor and privilege-escalation classes can fire.
	Tenant     string            `json:"tenant,omitempty" yaml:"tenant,omitempty"`
	Login      LoginStrategy     `json:"login,omitempty" yaml:"login,omitempty"`
	Phone      string            `json:"phone,omitempty" yaml:"phone,omitempty"`
	MFA        bool              `json:"mfa,omitempty" yaml:"mfa,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty" yaml:"attributes,omitempty"`
	// SignInPath is where this persona's sign-in form lives, when it is not
	// where the workflow starts. An application with two sign-in surfaces on
	// one origin, a customer console and an operator portal say, has personas
	// whose forms are on different paths, and the runner's search for a form
	// begins at the workflow's start path, which is the wrong one for the
	// persona that signs in somewhere else.
	SignInPath string `json:"sign_in_path,omitempty" yaml:"sign_in_path,omitempty"`
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
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Persona     string `json:"persona,omitempty" yaml:"persona,omitempty"`
	// Personas is the list of personas this workflow signs in as, in order,
	// in ONE browser, for a workflow about a person who holds more than one
	// session at once. The last one named is the identity the workflow acts
	// as; the ones before it are signed in first and their sessions kept.
	// Mutually exclusive with Persona.
	Personas []string `json:"personas,omitempty" yaml:"personas,omitempty"`
	// Personality pins one built in personality (HOW the agent behaves) to this
	// workflow instead of drawing one from the diversity mix. Independent of
	// Persona, the account the workflow signs in as (WHO). Read only when
	// diversity is enabled; empty means the mix assigns one from the seed.
	Personality string `json:"personality,omitempty" yaml:"personality,omitempty"`
	// Surface is what this workflow drives. Empty is filled in as SurfaceWeb
	// by normalisation, so everything downstream reads a value rather than
	// deciding what an absence means.
	Surface     Surface  `json:"surface,omitempty" yaml:"surface,omitempty"`
	StartPath   string   `json:"start_path,omitempty" yaml:"start_path,omitempty"`
	Independent bool     `json:"independent,omitempty" yaml:"independent,omitempty"`
	Budget      *Budget  `json:"budget,omitempty" yaml:"budget,omitempty"`
	Expect      []string `json:"expect,omitempty" yaml:"expect,omitempty"`
	Tags        []string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// Surface is what a workflow drives.
//
// NOT ChangeRule.Surface, which says what a changed FILE is. This says what a
// workflow DRIVES, and the two words landed in one schema from opposite ends.
// The note is here because the collision is the kind a reader resolves wrongly
// once and then carries.
type Surface string

// The five surfaces the product knows. Every one of them may be written in a
// manifest, including the ones a given build has no driver for, because a
// refusal that names the surface is worth more than a schema saying the value
// is unknown. Which of them a build can actually drive is DriveableSurfaces.
const (
	// SurfaceWeb is a browser, driven through its accessibility tree.
	SurfaceWeb Surface = "web"
	// SurfaceTerminal is a command line program. Written in
	// TerminalWorkflows rather than in Workflows, because it needs a program
	// to run where a browser workflow needs a persona to sign in as.
	SurfaceTerminal Surface = "terminal"
	// SurfaceDesktop is a native or Electron application.
	SurfaceDesktop Surface = "desktop"
	// SurfaceIOS is an iOS application.
	SurfaceIOS Surface = "ios"
	// SurfaceAndroid is an Android application.
	SurfaceAndroid Surface = "android"
)

// Surfaces is every surface a manifest may name, in the order the reference
// lists them.
var Surfaces = []Surface{SurfaceWeb, SurfaceTerminal, SurfaceDesktop, SurfaceIOS, SurfaceAndroid}

// DriveableSurfaces is the subset THIS BUILD carries a driver for.
//
// It is deliberately a different list from Surfaces, and the gap between them
// is the whole point: a manifest may name a surface this build cannot drive,
// and the engine refuses it by name against this list rather than against the
// schema. A lane that finishes a driver adds its surface here and flips its
// entry in the runner's registry, and TestTheDriveableSurfacesMatchTheRunnersRegistry
// fails if it does only one of the two.
var DriveableSurfaces = []Surface{SurfaceWeb, SurfaceTerminal, SurfaceDesktop, SurfaceIOS}

// CanDrive reports whether this build has a driver for a surface.
func CanDrive(s Surface) bool {
	for _, d := range DriveableSurfaces {
		if d == s {
			return true
		}
	}
	return false
}

// IsSurface reports whether a value names a surface the product knows at all,
// driveable or not.
func IsSurface(s Surface) bool {
	for _, known := range Surfaces {
		if known == s {
			return true
		}
	}
	return false
}

// SurfaceNames renders a list of surfaces for a message a person reads.
func SurfaceNames(surfaces []Surface) []string {
	out := make([]string, 0, len(surfaces))
	for _, s := range surfaces {
		out = append(out, string(s))
	}
	return out
}

// TerminalWorkflow is one thing the agents do at a command line.
//
// It carries the half of Workflow that is about the goal, the name, the
// description and the expectations, and replaces the half that is about a
// browser with the half that is about a program: what to run, what to type at
// it, and whether it draws a screen.
type TerminalWorkflow struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	Command     string   `json:"command" yaml:"command"`
	Args        []string `json:"args,omitempty" yaml:"args,omitempty"`
	// Input is what a person types, in order. Lines on standard input when the
	// workflow declares no screen, and keystrokes when it does; the angle
	// bracket key names are listed in the schema and decoded by the runner.
	Input  []string `json:"input,omitempty" yaml:"input,omitempty"`
	Expect []string `json:"expect,omitempty" yaml:"expect,omitempty"`
	// Screen is the size of the screen the program draws, and its presence is
	// what says the program draws one. A pointer rather than a value because
	// absence is the decision: it selects the pipe rather than the pseudo
	// terminal, and a zero sized screen and no screen at all are different
	// things.
	Screen *TerminalScreen `json:"screen,omitempty" yaml:"screen,omitempty"`
	// Cwd is where to run it, relative to the directory holding the manifest
	// unless it is absolute.
	Cwd    string          `json:"cwd,omitempty" yaml:"cwd,omitempty"`
	Budget *TerminalBudget `json:"budget,omitempty" yaml:"budget,omitempty"`
}

// TerminalScreen is how big the terminal the program is given is.
type TerminalScreen struct {
	Rows int `json:"rows,omitempty" yaml:"rows,omitempty"`
	Cols int `json:"cols,omitempty" yaml:"cols,omitempty"`
}

// TerminalBudget caps what one terminal workflow may take.
//
// Only a duration. A terminal workflow spends no steps, because its keys are
// written down rather than decided, and no money, because no model is asked
// anything; a budget with two fields that mean nothing would be three promises
// where one is kept.
type TerminalBudget struct {
	Duration string `json:"duration,omitempty" yaml:"duration,omitempty"`
}

// DefaultTerminalRows and DefaultTerminalCols are the screen a terminal
// workflow is given when it declares one without a size. Twenty four by eighty
// is what a terminal has been since the VT100, and it is what a program laying
// out a screen is most likely to have been written against.
const (
	DefaultTerminalRows = 24
	DefaultTerminalCols = 80
)

// DefaultTerminalDuration is how long a terminal workflow may take when it
// names no budget. It matches the runner's own default, so a workflow with no
// budget and one that writes this duration down behave identically.
const DefaultTerminalDuration = "30s"

// DesktopApplication is which application the desktop workflows drive.
//
// It is the piece without which SurfaceDesktop is a name and nothing else. A
// workflow says it drives the desktop; this says what the desktop is, and the
// runner cannot open a window without it. Declared once per manifest rather
// than per workflow, because a manifest describes one product: a list whose
// entries each named their own application would be unrelated runs sharing one
// report, with nothing in it saying which of them the change under review was
// about.
type DesktopApplication struct {
	// Kind is DesktopElectron or DesktopMacOS, and it is stated rather than
	// inferred from the path. Inferring it would mean an application driven
	// the wrong way reports as an application that does not work.
	Kind string `json:"kind" yaml:"kind"`
	// Application is the Electron binary, or the .app bundle for a native
	// application. Relative paths are resolved against the directory holding
	// the manifest before the runner is told, because the runner is started
	// from somewhere the manifest never mentions.
	Application string `json:"application" yaml:"application"`
	// Args are passed as written and never through a shell.
	Args []string `json:"args,omitempty" yaml:"args,omitempty"`
	// Process is what macOS calls the running application when that is not
	// the bundle's own name: Visual Studio Code.app runs as Code. Only for
	// DesktopMacOS, and refused on DesktopElectron, which is launched
	// directly and never looked up. Empty is filled in by normalisation with
	// the bundle's name without .app.
	Process string `json:"process,omitempty" yaml:"process,omitempty"`
}

// The kinds of desktop application, which is which accessibility tree the
// runner reads: Chromium's over the DevTools protocol, or the platform's own.
const (
	DesktopElectron = "electron"
	DesktopMacOS    = "macos"
)

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

// Security configures the dynamic security suite's fixtures: the facts the
// engine cannot infer from a diff and only the application's author knows.
//
// Off by default in the strongest sense: absent, or present with no access
// block, means the suite runs exactly as it did before, and no access probing
// happens and nothing is paid for it. The suite's families still run against a
// diff's targets on their own; this block only feeds the authenticated
// authorization differential the ownership facts it cannot guess, so it stays
// silent rather than probe a boundary it was never told about.
type Security struct {
	// Access declares the ownership-scoped objects the authz access-probe pass
	// reaches: a concrete object at a route, who owns it, and the canary the
	// application's own seed planted into it. Absent means no access probing.
	Access *SecurityAccess `json:"access,omitempty" yaml:"access,omitempty"`
}

// SecurityAccess is the list of ownership-scoped objects the access-probe pass
// reaches as each persona.
type SecurityAccess struct {
	// Objects are the declared reachable objects. Empty means no access probing,
	// the same as an absent access block.
	Objects []AccessObject `json:"objects,omitempty" yaml:"objects,omitempty"`
}

// AccessObject is one ownership-scoped object the access-probe pass reaches. The
// engine populates the golden's canary view from it and drives the runner to
// reach it as every persona, so a persona that reached another owner's object
// and got the object's canary back is a proven authorization break.
//
// The application's own seed is what plants the canary into the object; this
// block only DECLARES the ownership and the planted value, which keeps the
// engine app-agnostic. The canary value lives here and in the golden, against
// the twin; a finding that recognises it reports the location and the class,
// never the value.
type AccessObject struct {
	// Route is the object's location template, for example /api/orders/{id}. The
	// id below is substituted into its dynamic segment to form the concrete
	// reach, and the template, never the concrete url, is what a finding
	// reports.
	Route string `json:"route" yaml:"route"`
	// ID is the concrete object id substituted into the route's dynamic segment.
	// It names one real seeded object, so a refusal proves a boundary dropped a
	// real row rather than that the id was invented.
	ID string `json:"id" yaml:"id"`
	// Owner is the identity that owns the object. A cross-owner reach that
	// returns the canary is the break; a reach by the owner itself is the
	// self-access liveness arm and never a violation.
	Owner AccessOwner `json:"owner" yaml:"owner"`
	// ObjectClass is a category label for the object, for example "another
	// customer's order". It is what a finding says was reached, so it is a label
	// and NEVER an id or a value.
	ObjectClass string `json:"object_class" yaml:"object_class"`
	// Canary is the token the application's seed planted into this object so it
	// appears in the object's response body. Its presence in a response a
	// persona should not have been able to read is what proves the leak. The
	// value stays inside the engine; a finding reports only the location.
	Canary string `json:"canary" yaml:"canary"`
	// CanaryKind routes a leaked canary to the canary_leak family's secret or
	// pii key when the same value surfaces in a response it must not. Defaults
	// to "pii", because another owner's object content is another person's data;
	// set it to "secret" for a planted credential.
	CanaryKind CanaryKind `json:"canary_kind,omitempty" yaml:"canary_kind,omitempty"`
}

// CanaryKind is what a planted canary is, which decides the finding key when it
// leaks.
type CanaryKind string

const (
	// CanaryPII is another party's data, the default for an access object's
	// canary.
	CanaryPII CanaryKind = "pii"
	// CanarySecret is a planted credential.
	CanarySecret CanaryKind = "secret"
)

// AccessOwner is who owns an access object, given either as a declared persona
// by name or as an explicit identity. Naming a persona is the ordinary form:
// the object is owned by an account the manifest already declares, so the owner
// identity is resolved from that persona and stays one source of truth. An
// explicit identity is for an owner that is not a driven persona, a background
// account that seeds data but never signs in.
type AccessOwner struct {
	// Persona names a declared persona whose identity owns the object. When set,
	// User and Role are resolved from that persona; Tenant is resolved from the
	// persona too, and Tenant here may still supply one the persona does not
	// carry so a cross-tenant reach can be expressed without a tenant on every
	// persona.
	Persona string `json:"persona,omitempty" yaml:"persona,omitempty"`
	// Tenant, User and Role are the explicit owning identity, for an owner that
	// is not a declared persona. At least one must be set when Persona is empty.
	Tenant string `json:"tenant,omitempty" yaml:"tenant,omitempty"`
	User   string `json:"user,omitempty" yaml:"user,omitempty"`
	Role   string `json:"role,omitempty" yaml:"role,omitempty"`
}

// Diversity configures the behavioral variance of the agents that drive the
// workflows.
//
// A SEPARATE axis from the identity persona, and deliberately not a field on
// it. A persona is WHO an agent signs in as: an account provisioned in the
// golden, a few of them, each a write. A personality is HOW an agent behaves
// while pursuing a workflow's goal: a behavioral lens assigned per agent per
// run, drawn from a weighted population, that reaches no provisioning and
// changes only which of the controls already on the page the agent prefers.
// Conflating the two would drag a reasoning lens into the auth adapter and tie
// behavioral variance to which accounts are signed in, so they are kept apart.
//
// Off by default: an absent block, or Enabled false, is exactly today's
// behavior of one neutral agent per workflow. Reproducible from Seed, so a
// personality driven finding replays step for step the way an exploration
// does.
type Diversity struct {
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// Seed decides which personality drives which workflow and the behavioral
	// profile layered on top. Defaults to the run id, and is echoed into the
	// report so a run can be replayed. Deliberately separate from the identity
	// seed, which stays wall-clock fresh because a signup retry needs a
	// genuinely new email; personality assignment is a replay need, not a
	// freshness one.
	Seed string `json:"seed,omitempty" yaml:"seed,omitempty"`
	// Mix is how the population is drawn: balanced, realistic_population, or
	// aggressive_diversity. Empty means balanced.
	Mix PersonaMixMode `json:"mix,omitempty" yaml:"mix,omitempty"`
	// Variance is how far each agent's profile may drift from the neutral
	// centre: low, medium, or high. Empty means medium.
	Variance BehaviorVariance `json:"variance,omitempty" yaml:"variance,omitempty"`
	// AgentsPerWorkflow is how many personality varied agents drive each
	// workflow. Zero and one both mean one. More produces one result per
	// agent, each labelled with its personality.
	AgentsPerWorkflow int `json:"agents_per_workflow,omitempty" yaml:"agents_per_workflow,omitempty"`
	// Personalities selects which built in personalities may be drawn and
	// reweights them. Absent means all ten built ins at their default weights.
	Personalities []Personality `json:"personalities,omitempty" yaml:"personalities,omitempty"`
}

// Personality selects one built in personality by id and optionally reweights
// it. The catalogue of built ins lives in engine/internal/personality; this is
// only the manifest's way of choosing among them.
type Personality struct {
	ID string `json:"id" yaml:"id"`
	// Weight is how likely this personality is to be drawn relative to the
	// others listed. Weights are renormalized to sum to one hundred. Zero
	// falls back to the personality's built in population weight.
	Weight float64 `json:"weight,omitempty" yaml:"weight,omitempty"`
}

// PersonaMixMode is how a personality population is drawn.
type PersonaMixMode string

const (
	// MixBalanced spreads a few common strategies evenly. The default.
	MixBalanced PersonaMixMode = "balanced"
	// MixRealistic follows the built in population weights.
	MixRealistic PersonaMixMode = "realistic_population"
	// MixAggressive spreads strategies as widely as the agent count allows.
	MixAggressive PersonaMixMode = "aggressive_diversity"
)

// BehaviorVariance is how far an agent's profile may drift from neutral.
type BehaviorVariance string

const (
	// VarianceLow keeps agents close to regular behavior.
	VarianceLow BehaviorVariance = "low"
	// VarianceMedium is the default.
	VarianceMedium BehaviorVariance = "medium"
	// VarianceHigh lets agents diverge.
	VarianceHigh BehaviorVariance = "high"
)

// BuiltInPersonalityIDs are the ten built in personality ids, in the order the
// catalogue and the schema describe them. It is the shared vocabulary that the
// manifest validator checks a `personality:` pin against and that the
// engine/internal/personality catalogue must cover exactly, so the two cannot
// disagree about which ids exist.
//
// The list lives here rather than in the catalogue package so the manifest
// validator can reference it without importing the catalogue, and the
// catalogue asserts it covers this list exactly in its own test.
var BuiltInPersonalityIDs = []string{
	"explorer",
	"fast_actor",
	"cautious_analyst",
	"goal_oriented",
	"distracted",
	"skeptic",
	"text_oriented",
	"visual_follower",
	"keyboard_user",
	"edge_case",
}

// IsBuiltInPersonality reports whether id names one of the built in
// personalities.
func IsBuiltInPersonality(id string) bool {
	for _, b := range BuiltInPersonalityIDs {
		if b == id {
			return true
		}
	}
	return false
}

// FidelityDimension names one part of the environment the inventory measures.
//
// A closed vocabulary rather than free text, because the manifest names these
// in fidelity.require and a typo there would silently require nothing.
type FidelityDimension string

const (
	// FidelityServices is the services the manifest declares, against the
	// containers that are running.
	FidelityServices FidelityDimension = "services"
	// FidelityDatabase is the branch: which golden it came from, whether that
	// golden was verified, and whether its attestation still checks out.
	FidelityDatabase FidelityDimension = "database"
	// FidelityThirdParty is the hosts the egress policy covers, and what
	// answers for each of them.
	FidelityThirdParty FidelityDimension = "third_party"
	// FidelityAuth is whether each persona can be created and signed in as.
	FidelityAuth FidelityDimension = "auth"
	// FidelityRuntime is where the environment runs.
	FidelityRuntime FidelityDimension = "runtime"
	// FidelityTraffic is where the endpoint mix comes from.
	FidelityTraffic FidelityDimension = "traffic"
	// FidelityDatastores is every datastore in the environment other than the
	// primary database: what each one is, and whether anything here reproduced
	// its contents.
	//
	// It exists because the absence was invisible. Database is a single struct
	// and it is Postgres, so a ClickHouse or a Redis declared as a service is
	// started empty and no dimension above says a word about it. An analytics
	// product's twin held a masked Postgres and zero events, and the one
	// instrument whose job is to say "this is not production" reported it as
	// faithful because it was not looking.
	FidelityDatastores FidelityDimension = "datastores"
	// FidelityTopology is how many instances of each service are running,
	// against how many the manifest asked for.
	//
	// The other half of the same finding the datastores dimension closed.
	// Replicas was a dead manifest field until both runtimes honoured it, and
	// a manifest asking for three instances silently ran one, so nothing that
	// only breaks above one instance could appear in a twin: leader election,
	// double processing of a queue, a cache coherent with one instance and not
	// two, a sticky session assumption. The runtimes run the count now, and
	// this is the dimension that says whether they did.
	//
	// A service that names no count has said nothing about how many instances
	// production runs, and a twin running one of it is neither shown to
	// reproduce that nor shown not to. So it is reported unmeasured rather
	// than reproduced, which is the same refusal the runtime dimension makes
	// for the same reason.
	FidelityTopology FidelityDimension = "topology"
)

// AllFidelityDimensions returns every dimension, in the order the inventory
// reports them.
func AllFidelityDimensions() []FidelityDimension {
	return []FidelityDimension{
		FidelityServices, FidelityDatabase, FidelityThirdParty,
		FidelityAuth, FidelityRuntime, FidelityTraffic, FidelityDatastores,
		FidelityTopology,
	}
}

// Fidelity configures the component inventory.
//
// There is no threshold here on purpose. A single percentage hides the one
// dimension that matters to a particular change, so what can be required is a
// dimension, by name, and the requirement is that every component of it was
// reproduced.
type Fidelity struct {
	Enabled *bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	// Require names the dimensions that must be fully reproduced. A dimension
	// that could not be measured does not satisfy a requirement and does not
	// break one either; it fails the command with a different code, because
	// "not measured" counted as either answer is the mistake this whole
	// feature exists to avoid.
	Require []FidelityDimension `json:"require,omitempty" yaml:"require,omitempty"`
}

// Change configures how a pull request's diff is classified.
//
// Absent from most manifests, because the built in rules cover the layouts
// most projects use. Present when a repository puts something somewhere the
// conventions do not predict, which in a monorepo is normal rather than
// exotic. It cannot turn a check off: a rule only says what a path IS, and
// what that implies is decided by the engine.
type Change struct {
	Rules []ChangeRule `json:"rules,omitempty" yaml:"rules,omitempty"`
}

// ChangeRule assigns a surface to the paths a pattern matches.
type ChangeRule struct {
	// Path is a glob. A single star does not cross a slash and a double star
	// does, so "packages/*/src/**" is a legal and useful thing to write.
	Path string `json:"path" yaml:"path"`
	// Surface is what the matched paths are. The engine refuses a surface it
	// does not know rather than treating it as unclassified, because a typo
	// that silently means "unknown" would look like the rule working.
	Surface string `json:"surface" yaml:"surface"`
	// Note replaces the sentence the report prints for this rule, for a
	// project that wants to say why rather than restate the pattern.
	Note string `json:"note,omitempty" yaml:"note,omitempty"`
}

// LoadSource names where the endpoint mix comes from.
type LoadSource string

// Datadog and New Relic were here and are gone. Both were accepted by the
// schema and refused by the runtime, which is worse than not offering them: a
// key a person can set that cannot work reads as a broken product rather than
// as an unfinished one. OpenTelemetry stayed because it is a published wire
// format that can be read from a file, so it could be finished.
const (
	LoadNone      LoadSource = "none"
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
	Scenarios    []LoadScenario    `json:"scenarios,omitempty" yaml:"scenarios,omitempty"`
	Thresholds   *LoadThresholds   `json:"thresholds,omitempty" yaml:"thresholds,omitempty"`
	// Comparison runs the same workload on the base branch as well and
	// differences the two. It is the only part of Load that measures a base
	// branch delta: Thresholds above judges one run against production.
	Comparison *LoadComparison `json:"comparison,omitempty" yaml:"comparison,omitempty"`
	// Traffic names the committed profile of what production serves, which is
	// the denominator every route in a load run is measured against.
	Traffic *Traffic `json:"traffic,omitempty" yaml:"traffic,omitempty"`
	// SQL is a concurrent workload run against the database directly rather
	// than through the application.
	//
	// Inside load rather than beside it, because it is the same question asked
	// one layer down: what does this change do under concurrency. Everything
	// above sends requests and measures the application, with the database
	// somewhere inside the number. This sends statements and measures the
	// database. A person changing an index wants the second and can only reach
	// it through the first, which is why it is here.
	SQL *LoadSQL `json:"sql,omitempty" yaml:"sql,omitempty"`
}

// SQLWorkloadSource is where a SQL workload's statements come from.
type SQLWorkloadSource string

// The two sources, spelled as the schema spells them.
const (
	// SQLDeclared reads a workload document in the repository.
	SQLDeclared SQLWorkloadSource = "declared"
	// SQLStatementStatistics reads pg_stat_statements on the branch, so the
	// mix is the traffic that really ran, weighted by how often it ran.
	SQLStatementStatistics SQLWorkloadSource = "statement_statistics"
)

// LoadSQL configures a concurrent SQL workload.
// No Enabled field, and the absence is a decision rather than an oversight.
// load.enabled exists because af ci reads it to decide whether to send the
// mix. Nothing runs a SQL workload except af load sql and the hosted kind that
// reproduces through it, so an enabled flag here would be a knob the schema
// publishes, the reference renders, and no code reads: the exact shape
// tools/fieldsweep exists to catch. Declaring the block is what turns it on.
type LoadSQL struct {
	Source SQLWorkloadSource `json:"source,omitempty" yaml:"source,omitempty"`
	// Script is the workload document, relative to the repository root.
	Script string `json:"script,omitempty" yaml:"script,omitempty"`
	// Clients is how many run at once, each on its own connection.
	Clients int `json:"clients,omitempty" yaml:"clients,omitempty"`
	// Duration and Transactions are the two ways to say how much work. A
	// duration is what a check usually wants and a transaction count is what a
	// comparison between two builds wants, because the same count on both
	// sides is the same amount of work whatever the machines did.
	Duration     string `json:"duration,omitempty" yaml:"duration,omitempty"`
	Transactions int    `json:"transactions,omitempty" yaml:"transactions,omitempty"`
	// ThinkTime is how long a client waits between transactions.
	ThinkTime string `json:"think_time,omitempty" yaml:"think_time,omitempty"`
	// Writes allows a derived mix to include statements that change data.
	Writes bool `json:"writes,omitempty" yaml:"writes,omitempty"`
	// MaxStatements caps how many statements a derived mix holds.
	MaxStatements int                `json:"max_statements,omitempty" yaml:"max_statements,omitempty"`
	Thresholds    *LoadSQLThresholds `json:"thresholds,omitempty" yaml:"thresholds,omitempty"`
}

// LoadSQLThresholds are what fail a SQL workload.
type LoadSQLThresholds struct {
	// MeanIncrease is how much slower a transaction may get than the mean the
	// statistics recorded for it. It needs a baseline, so it applies under
	// statement_statistics only.
	MeanIncrease float64 `json:"mean_increase,omitempty" yaml:"mean_increase,omitempty"`
	// ErrorRate is the share of attempts that may fail.
	ErrorRate float64 `json:"error_rate,omitempty" yaml:"error_rate,omitempty"`
}

// LoadComparison configures running the workload twice, once per build.
//
// Separate from LoadThresholds rather than three more keys inside it, because
// the two answer different questions against different baselines and putting
// them in one object is what let this manifest describe itself wrongly for as
// long as it did. A key under Thresholds is measured against production or
// against the run's own responses; a key under Comparison is measured against
// a second run of the same workload on another commit. A reader who has to
// know which is which per key has been handed the ambiguity rather than
// spared it.
type LoadComparison struct {
	// Enabled is a pointer so that present-and-false is distinguishable from
	// absent, which is how a project keeps its thresholds and turns the check
	// off for a while. The oracle's own Enabled is a pointer for this reason.
	Enabled  *bool          `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	Baseline BaselineSource `json:"baseline,omitempty" yaml:"baseline,omitempty"`
	BaseRef  string         `json:"base_ref,omitempty" yaml:"base_ref,omitempty"`
	// Thresholds are the deltas against the base branch that fail the run.
	Thresholds *LoadComparisonThresholds `json:"thresholds,omitempty" yaml:"thresholds,omitempty"`
}

// LoadComparisonThresholds are the base branch deltas that fail a run.
//
// Every field is a ratio between two measurements of the same workload, one
// per build. A field is evaluated only when BOTH sides carried the number;
// otherwise the verdict is unverified, never pass. A threshold that quietly
// evaluated nothing and reported green is the defect this repository keeps
// finding in its own instruments, and it is the reason p95_increase is
// refused under a source that carries no baseline.
type LoadComparisonThresholds struct {
	// P95Increase is how much slower than the base branch a route may get, as
	// a ratio of the base branch's own p95 for that route. Distinct from
	// LoadThresholds.P95Increase, which compares against production.
	P95Increase float64 `json:"p95_increase,omitempty" yaml:"p95_increase,omitempty"`
	// ThroughputDrop is how much of the base branch's achieved request rate
	// this branch may lose, as a ratio. Read from the rate each run actually
	// achieved rather than the rate it aimed at, because a run that fell
	// behind its target reports the target as fine while the queue grows.
	ThroughputDrop float64 `json:"throughput_drop,omitempty" yaml:"throughput_drop,omitempty"`
	// ErrorRateIncrease is how much the share of failing requests may rise,
	// in absolute points expressed as a fraction. Absolute rather than a
	// ratio because a base branch error rate of zero has no ratio, and a
	// build introducing errors where there were none is the case this most
	// needs to catch.
	ErrorRateIncrease float64 `json:"error_rate_increase,omitempty" yaml:"error_rate_increase,omitempty"`
}

// Traffic names the committed record of what production actually serves.
//
// Without one, safe_routes is a list somebody wrote from memory and nothing
// can say how much of production it misses. Measured on this repository on
// 2026-09-06: a migration held AccessExclusiveLock on nine relations for
// thirty seconds and the load run over four hand written routes reported 0.0
// percent failed, because none of the four read the locked table. The profile
// is what turns that list into a fraction of production's requests.
//
// It is a path rather than a collector endpoint, deliberately, and for the
// same reason database.volume is. The profile is recorded once from telemetry
// a team already has, and committed; every machine that reads it afterwards,
// including a pull request check that can reach nothing, reads a file. Nothing
// here reports from inside a running application.
type Traffic struct {
	// Profile is the artifact, relative to the repository root. Written by
	// af traffic record and read by everything that needs a denominator.
	Profile string `json:"profile" yaml:"profile"`
	// MaxAge is how old the profile may be before it is refused. A stale
	// profile is not a smaller number, it is an unknown one, so it is refused
	// the way a stale golden is rather than quoted.
	MaxAge string `json:"max_age,omitempty" yaml:"max_age,omitempty"`
}

// LoadScenario points at a journey document and says how hard to run it.
//
// The document lives in the repository rather than in the manifest because a
// journey with parallel blocks and assertions in it is a page long, and a
// manifest with three of them inline is a manifest nobody can read.
type LoadScenario struct {
	// Path is the scenario document, relative to the repository root.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
	// Sessions is how many run the journey at once.
	Sessions int `json:"sessions,omitempty" yaml:"sessions,omitempty"`
	// Iterations is how many times each session walks it.
	Iterations int `json:"iterations,omitempty" yaml:"iterations,omitempty"`
	// StartAfter delays this scenario, so one journey can burst while another
	// is already running.
	StartAfter string `json:"start_after,omitempty" yaml:"start_after,omitempty"`
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
	// MigrationLint governs all seventeen lint rules together. They are one setting
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
	// WorkflowsUnverified is a run in which no workflow reached a verdict
	// about the application, because every one was blocked or unverified or
	// because none was declared.
	WorkflowsUnverified PolicyLevel `json:"workflows_unverified,omitempty" yaml:"workflows_unverified,omitempty"`
	// Review is a finding from the static code reviewer, the LLM-backed lane
	// that reads the change's added lines for correctness defects. It defaults
	// to warn rather than fail, because the reviewer is probabilistic and must
	// not block a merge on a model's say-so; raise it to fail once the project
	// trusts it.
	Review PolicyLevel `json:"review,omitempty" yaml:"review,omitempty"`
	// Security maps each dynamic security check key, "security.<family>.<rule>"
	// such as security.authz.idor, to what a finding on it does to the check.
	//
	// A map rather than a field per key, because the security check families
	// land in parallel and each owns a slice of this namespace; a field per key
	// would have three lanes editing one struct. The values are the same three
	// levels every other key uses, and an unrecognised level is refused the way
	// every other policy value is. The KEY is not constrained by the schema:
	// the legal keys are the ones the registered families declare, which the
	// engine knows and this file does not, so a manifest may set a key ahead of
	// the family that reads it.
	Security map[string]PolicyLevel `json:"security,omitempty" yaml:"security,omitempty"`
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
	Provider RuntimeProvider `json:"provider,omitempty" yaml:"provider,omitempty"`
	TTL      string          `json:"ttl,omitempty" yaml:"ttl,omitempty"`
	// MaxTTL is the furthest af env extend may push an environment's expiry,
	// measured from when the environment was created. It is the answer to
	// "a lifetime that can be extended forever is not a lifetime": TTL is what
	// an environment gets without asking, MaxTTL is the most it can be given.
	MaxTTL            string `json:"max_ttl,omitempty" yaml:"max_ttl,omitempty"`
	IdleSleep         string `json:"idle_sleep,omitempty" yaml:"idle_sleep,omitempty"`
	Domain            string `json:"domain,omitempty" yaml:"domain,omitempty"`
	NamespacePrefix   string `json:"namespace_prefix,omitempty" yaml:"namespace_prefix,omitempty"`
	KubeconfigContext string `json:"kubeconfig_context,omitempty" yaml:"kubeconfig_context,omitempty"`
	// Requires is what a target must offer for this repository to be placed on
	// it, as attribute equals value matched against a target's Tags.
	//
	// Empty means anywhere, which is what every manifest that declares no
	// placement says. A requirement no declared target satisfies is refused at
	// validation rather than at dispatch, because the targets are in the same
	// file and an author who wrote region=eu-west-2 for a fleet that only has
	// eu-west-1 should be told while they are looking at both lines.
	Requires map[string]string `json:"requires,omitempty" yaml:"requires,omitempty"`
	// Targets are the places an environment may be placed, in preference
	// order. Empty is the ordinary case and means the single runtime Provider
	// names, which is every manifest that existed before placement did.
	//
	// A target is a runtime plus the facts about WHERE it is, and the second
	// half is the part no runtime can supply for itself: a kubeconfig context
	// is a name on somebody's laptop and it does not say which region the
	// cluster is in. So the tags are declared here, in the repository, under
	// review, rather than discovered from a cluster that could be relabelled
	// by anyone with access to it.
	Targets []RuntimeTarget `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// RuntimeTarget is one place an environment may be placed.
//
// It carries the settings that differ between two pools of the same kind, and
// nothing else. Lifetime is not here: how long an environment lives is a
// property of the repository and it does not change because the environment
// landed in Frankfurt rather than Virginia.
type RuntimeTarget struct {
	// Name identifies the target in a placement decision and in the refusal
	// when none will do. Unique within the manifest.
	Name string `json:"name" yaml:"name"`
	// Provider is the runtime this target uses. Empty inherits the runtime
	// block's own provider, which is what lets a fleet of clusters be written
	// as one provider line and a list of contexts.
	Provider RuntimeProvider `json:"provider,omitempty" yaml:"provider,omitempty"`
	// TargetTags are what this target offers, matched against Requires.
	//
	// Named TargetTags rather than Tags because tools/fieldsweep resolves a
	// reader by field NAME rather than by type, and workflows[].tags is exempt
	// there as a label nothing reads. A second Tags with real readers would
	// make that exemption unable to fail, which is a check that has stopped
	// being able to say no. The manifest key is still tags.
	TargetTags map[string]string `json:"tags,omitempty" yaml:"tags,omitempty"`
	// Domain is the wildcard domain for environments placed here. Empty
	// inherits the runtime block's.
	Domain string `json:"domain,omitempty" yaml:"domain,omitempty"`
	// NamespacePrefix is the Kubernetes namespace prefix for this target.
	// Empty inherits the runtime block's.
	NamespacePrefix string `json:"namespace_prefix,omitempty" yaml:"namespace_prefix,omitempty"`
	// KubeconfigContext is which cluster this target is. Empty inherits the
	// runtime block's, which for a list of clusters is almost never what the
	// author meant, so validation says so when two targets would resolve to
	// the same cluster.
	KubeconfigContext string `json:"kubeconfig_context,omitempty" yaml:"kubeconfig_context,omitempty"`
}

// RegionTag is the tag a target uses to say where it is.
//
// Named rather than spelled at each use because the organization policy hook's
// residency rule reads it: a target tagged with this key is what fills
// EnvironmentRequest.Region, and before placement existed nothing filled that
// field at all, so the rule could not fire. A rename that touched one of the
// two sites and not the other would put it back.
const RegionTag = "region"

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

// InfraSource names the tool that declares an application's infrastructure.
//
// A closed vocabulary with one member, on the same reasoning LoadSource states
// a few hundred lines up: a value the schema accepts and nothing can read is
// worse than a value that is not offered, because the first looks like a
// configured feature and behaves like a missing one. The list grows when a
// reader for the next tool exists, and not before.
//
// OpenTofu is deliberately not a second member. It writes the same language
// and is read by the same reader, so a second spelling would be two names for
// one behaviour and a manifest could then disagree with itself about which
// one it meant.
type InfraSource string

// InfraTerraform is Terraform, and OpenTofu, which is the same language.
const InfraTerraform InfraSource = "terraform"

// Infrastructure says where this application's infrastructure as code lives.
//
// It is the only section of the manifest that describes PRODUCTION rather than
// the copy. Everything else here says what to build; this says what the thing
// being copied is declared to be, which is what makes a comparison possible at
// all. Nothing in it changes what an environment builds or runs.
//
// The section is absent from most manifests, and its absence is reported
// rather than assumed: a copy nobody compared against its own infrastructure
// has not been shown to reproduce it.
type Infrastructure struct {
	// Source is the tool that declares the infrastructure.
	Source InfraSource `json:"source" yaml:"source"`
	// Paths are the root module directories, relative to the repository root.
	//
	// A list because a root module is the unit that is planned and applied on
	// its own, and an application whose infrastructure is split by concern has
	// several. Every entry is checked to exist and to be a directory.
	Paths []string `json:"paths" yaml:"paths"`
	// Workspace is which workspace holds production.
	//
	// Only meaningful alongside a single root module, because a workspace is
	// selected inside one, and validation refuses it beside several rather
	// than picking one of them.
	Workspace string `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	// VarFiles are the variable files that describe production, in the order
	// they would be passed. Refused beside several root modules for the same
	// reason Workspace is: a variable file is an argument to one.
	VarFiles []string `json:"var_files,omitempty" yaml:"var_files,omitempty"`
}

// GitHub configures the pull request integration.
type GitHub struct {
	Mode       GitHubMode `json:"mode,omitempty" yaml:"mode,omitempty"`
	Comment    *bool      `json:"comment,omitempty" yaml:"comment,omitempty"`
	ForkPolicy ForkPolicy `json:"fork_policy,omitempty" yaml:"fork_policy,omitempty"`
	TeardownOn []string   `json:"teardown_on,omitempty" yaml:"teardown_on,omitempty"`
}
