// Package iac reads a description of production out of infrastructure as code,
// by parsing it and by nothing else.
//
// WHAT IT IS FOR. Everything this engine does to rehearse a change against a
// twin of production depends on knowing what production actually is: which
// services run which images, how much CPU and memory they are given, how many
// replicas there are, which Postgres version and which server parameters, which
// buckets and queues and tables exist, what the health probes check, and what
// the network will and will not let out. A manifest can be told all of that by
// hand, and then it is a second copy of the truth that goes stale the first
// time somebody edits the infrastructure and not the manifest. The
// infrastructure as code IS the description of production, already written,
// already reviewed, already the thing that made production what it is. This
// package reads it.
//
// THREE ABSOLUTE REFUSALS. These are not conventions, they are enforced, and
// each one has a test aimed at the exact case it exists for.
//
//  1. IT NEVER EXECUTES ANYTHING. No `terraform apply`, no `terraform plan`,
//     no `helm install`, no `helm template`, no shelling out to a provider or
//     to any binary at all. The package imports neither os/exec nor any
//     package that does, and TestTheReaderCannotExecuteOrReachANetwork asserts
//     that over the real import graph rather than over this comment.
//  2. IT NEVER REACHES A CLOUD OR A NETWORK. No provider API, no registry
//     fetch, no remote module, no `lookup` in a Helm template. Same test, same
//     import graph: net/http and friends are absent. A module whose source is
//     a registry is reported as unread, with the reason, rather than fetched.
//  3. IT NEVER READS A STATE FILE. This is the sharpest of the three, because
//     a state file is the richest description of production there is and it is
//     also a plain text file full of secrets: every password a random_password
//     resource generated, every value a provider returned, every attribute a
//     provider marked sensitive is in there in the clear. A reader pointed at
//     one refuses it by name with a reason, and the refusal is in Reading.
//     Refused where a caller sees it.
//
// HOW THE STATE REFUSAL IS MADE IMPOSSIBLE TO SKIP, rather than documented.
// There is exactly one entry point, Read, and it takes a DIRECTORY. No
// exported function in this package takes a file path, an io.Reader or a byte
// slice, so a caller holding a state file has no way to hand it to a reader
// even deliberately. Inside the walk, classification happens in one place,
// before any reader is chosen, and a state file is recognised BY CONTENT as
// well as by name, because a state file copied to plan.json is still a state
// file and a reader that trusted the extension would read it. See classify in
// walk.go, and TestAStateFileIsRefusedHoweverItIsNamed.
//
// WHAT "UNMEASURED" MEANS HERE, and it is the package's most important
// property. Parsing without executing means meeting expressions that cannot be
// resolved: a variable with no default, a value that only exists after an
// apply, a module from a registry. Those are NOT zero and NOT a guess. Every
// field that can be unresolvable is a Value[T] carrying one of three states,
// and every file the walk saw appears in Reading.Sources whether it was read or
// not. Reading.Unmeasured collects both levels, so a caller can say what it
// could not look at without walking these structs and without forgetting to.
// See value.go.
//
// WHAT IT NEVER CARRIES. Environment variable NAMES, never values. Secret
// REFERENCES, never secrets. Three independent things drop a value: an
// attribute whose name says it holds a credential, a variable the configuration
// itself marks sensitive, and a plan whose JSON marks the attribute sensitive.
// Whatever survives all three still goes through the engine's redactor on its
// way into a Reading, so a credential written in line under an innocuous name
// is caught by shape after it was missed by name. See sensitive.go.
package iac

import (
	"context"
	"fmt"
	"sort"
)

// Reading is everything one read of an infrastructure as code tree could say
// about production.
type Reading struct {
	// Root is the directory that was read, as the caller gave it.
	Root string `json:"root"`
	// Sources is every file the walk saw, read or not. An unread one carries
	// the reason, which is what makes this slice the ledger of what was not
	// looked at rather than a list of successes.
	Sources []Source `json:"sources,omitempty"`
	// Components is every service, store and cloud resource declared.
	Components []Component `json:"components,omitempty"`
	// Network is the egress relevant rules: security group rules and
	// Kubernetes network policies.
	Network []NetworkRule `json:"network,omitempty"`
	// Refused is what this reader refused to open, by name and with the
	// reason. A refusal is stronger than an unread source: it is not "I could
	// not", it is "I will not".
	Refused []Refusal `json:"refused,omitempty"`
}

// Dialect is a language the reader recognises, whether or not it reads it.
type Dialect string

const (
	// DialectTerraform is HCL native syntax: .tf and .tfvars.
	DialectTerraform Dialect = "terraform"
	// DialectTerraformJSON is Terraform's JSON syntax: .tf.json.
	DialectTerraformJSON Dialect = "terraform-json"
	// DialectTerraformPlan is the output of `terraform show -json <planfile>`.
	DialectTerraformPlan Dialect = "terraform-plan"
	// DialectKubernetes is a raw Kubernetes manifest.
	DialectKubernetes Dialect = "kubernetes"
	// DialectKustomize is a kustomization.yaml and the overlay it describes.
	DialectKustomize Dialect = "kustomize"
	// DialectHelm is a Helm chart. Recognised, named, and not rendered: see
	// the reason on the Source.
	DialectHelm Dialect = "helm"
	// DialectTerraformState is a Terraform state file. It is a dialect so that
	// it can be RECOGNISED, which is the whole point: a dialect the classifier
	// cannot name is a dialect it cannot refuse.
	DialectTerraformState Dialect = "terraform-state"
)

// Source is one file the walk saw.
type Source struct {
	// Path is relative to Reading.Root.
	Path string `json:"path"`
	// Dialect is what the classifier decided it is, empty when it could not
	// tell.
	Dialect Dialect `json:"dialect,omitempty"`
	// Read is whether anything in this file reached the Reading.
	Read bool `json:"read"`
	// Why is why it was not read, when Read is false. A sentence, lower case,
	// naming the file's own property rather than the parser.
	Why string `json:"why,omitempty"`
}

// Refusal is a file this reader refused to open.
type Refusal struct {
	// Path is relative to Reading.Root.
	Path string `json:"path"`
	// Dialect is what the classifier recognised it as before refusing it,
	// which for now is always a state file. It is carried because a consumer
	// grouping refusals needs to know WHAT was refused, not only that
	// something was, and because a dialect nothing ever sets is a constant
	// whose doc comment stops anybody asking why it is there.
	Dialect Dialect `json:"dialect,omitempty"`
	// Reason is why, in a sentence a person reads once and agrees with.
	Reason string `json:"reason"`
}

// Error renders a refusal as the sentence a caller can return.
func (r Refusal) Error() string { return r.Path + ": " + r.Reason }

// Kind is what a component is.
//
// The vocabulary is engine/internal/detect's infraImages vocabulary, verbatim,
// because a fidelity report compares what this reader says production holds
// against what detect says a compose file holds, and two vocabularies for the
// same nine things is how one of them silently stops matching. The members
// below that detect has no word for are the ones a container image cannot
// express and a cloud resource can.
type Kind string

const (
	// The nine shared with engine/internal/detect. Do not rename one of these
	// without renaming it there: TestTheKindVocabularyMatchesDetect fails if
	// they drift.
	KindPostgres      Kind = "postgres"
	KindRedis         Kind = "redis"
	KindMySQL         Kind = "mysql"
	KindMongoDB       Kind = "mongodb"
	KindRabbitMQ      Kind = "rabbitmq"
	KindElasticsearch Kind = "elasticsearch"
	KindClickHouse    Kind = "clickhouse"
	KindKafka         Kind = "kafka"
	// KindObjectStore is detect's word for minio, and it is the word an S3
	// bucket arrives under too: to a fidelity report a bucket and a minio are
	// the same fact.
	KindObjectStore Kind = "objectstore"

	// The rest are things infrastructure declares and a container image does
	// not.

	// KindService is something that runs an image and serves traffic.
	KindService Kind = "service"
	// KindQueue is a point to point queue: SQS, a Service Bus queue.
	KindQueue Kind = "queue"
	// KindTopic is a publish and subscribe topic: SNS, Event Grid.
	KindTopic Kind = "topic"
	// KindTable is a managed key value or document table: DynamoDB, Cosmos.
	KindTable Kind = "table"
	// KindSecret is a stored secret: a Key Vault secret, a Secrets Manager
	// secret. The component records that it EXISTS and what it is called. It
	// never records what is in it.
	KindSecret Kind = "secret"
	// KindParameter is a configuration parameter store entry: an SSM
	// parameter, an App Configuration key.
	KindParameter Kind = "parameter"

	// KindUnknown is a declared resource this reader recognised as a resource
	// and not as one of the kinds above.
	//
	// It is deliberately a value rather than an empty string, and the
	// component still arrives with its name, its address, its position and
	// every attribute it declares. A reader of the report then sees a thing
	// nobody taught this package to classify, which is a question worth
	// asking, rather than seeing nothing at all, which is the answer that
	// stops anybody asking.
	KindUnknown Kind = "unknown"
)

// Component is one thing the infrastructure declares.
//
// One type with a Kind discriminator rather than one type per kind: every
// consumer of this package iterates by kind, and a bucket having an Absent CPU
// is exactly as true as a bucket having no CPU field would be, while costing
// the callers nothing to skip.
type Component struct {
	// Name is the name the configuration gives it: the bucket's name, the
	// container app's name, the deployment's metadata.name.
	Name string `json:"name"`
	// Kind is what it is.
	Kind Kind `json:"kind"`
	// Address is the configuration's own address for it, which is the string
	// somebody greps for: azurerm_postgresql_flexible_server.main, or
	// apps/v1 Deployment default/api.
	Address string `json:"address"`
	// At is where it is declared.
	At Position `json:"at"`
	// Type is the provider or api type as written, kept because Kind
	// deliberately collapses: aws_s3_bucket and azurerm_storage_container are
	// both objectstore, and a reader sometimes needs to know which.
	Type string `json:"type,omitempty"`
	// From is which reader produced this component, and it is a CONFIDENCE
	// rather than a curiosity.
	//
	// A component read from a plan has had every variable, local, function,
	// for_each and module resolved by Terraform before this package saw it, so
	// its fields are literals and nothing is Unreadable by construction. The
	// same component read from HCL may carry a dozen holes for the same
	// production. Those two results must not look alike to a consumer: one is
	// "this IS production" and the other is "this is what the configuration
	// says, minus what only a plan can decide". A fidelity report that treated
	// them the same would present a guess with the authority of a measurement.
	From Dialect `json:"from,omitempty"`
	// Present is whether this component is definitely in production.
	//
	// Known(true) is the ordinary case. Known(false) is a resource whose
	// `count` resolves to zero, which is declared and deliberately not
	// deployed. Unreadable is the one that matters: a resource whose `count`
	// or `for_each` depends on something only a plan resolves, so this reader
	// can see that it is DECLARED and cannot say whether production has it.
	// Reporting that as present would invent a resource; reporting it as
	// absent would hide one.
	Present Value[bool] `json:"present,omitempty"`

	Image    Value[string] `json:"image,omitempty"`
	CPU      Value[string] `json:"cpu,omitempty"`
	Memory   Value[string] `json:"memory,omitempty"`
	Replicas Value[int]    `json:"replicas,omitempty"`
	// Engine and Version are the datastore's engine family and version as the
	// configuration declares them: "postgres" and "16".
	Engine  Value[string] `json:"engine,omitempty"`
	Version Value[string] `json:"version,omitempty"`

	// Env is the environment variables this component is given, BY NAME.
	// Never the values.
	Env []EnvVar `json:"env,omitempty"`
	// Secrets is every secret this component references, by reference.
	Secrets []SecretRef `json:"secrets,omitempty"`
	// Params is the server parameters declared for a datastore.
	Params []Param `json:"params,omitempty"`
	// Probes is the health probes declared for a service.
	Probes []Probe `json:"probes,omitempty"`
	// Attrs is every other attribute the configuration declares, sorted by
	// name, with credential shaped values dropped.
	Attrs []Attr `json:"attrs,omitempty"`
}

// EnvVar is one environment variable a component is given, by name.
//
// THE VALUE IS NOT HERE AND THERE IS NO FIELD FOR IT. A reader that carried
// environment variable values would carry every database URL and every API key
// production runs on, because that is what environment variables are for. What
// a rehearsal needs is the SHAPE of the environment: which names exist, and
// which of them are fed from a secret rather than written in line.
type EnvVar struct {
	Name string `json:"name"`
	// Present is whether the variable is definitely set.
	//
	// Known(true) is an ordinary declaration. Unreadable is one inside a
	// `dynamic` block whose for_each this reader cannot resolve, which is
	// extremely common in real configurations: the variable is declared, and
	// whether production has it is decided when Terraform runs.
	Present Value[bool] `json:"present,omitempty"`
	// From names the secret this variable is fed from, when it is fed from
	// one. It is a name, which is a reference, not a secret.
	From Value[string] `json:"from,omitempty"`
	At   Position      `json:"at"`
}

// SecretRef is a reference to a secret, and never a secret.
type SecretRef struct {
	// Name is what the component calls it: the environment variable name, or
	// the secret ref's name in a container app.
	Name string `json:"name"`
	// From is where it is fetched from, as the configuration writes it: a Key
	// Vault secret id, a Secrets Manager arn, a Kubernetes secret's name and
	// key. It is a locator, so it is carried. What is behind it is not read,
	// which would require reaching a cloud, which this package does not do.
	From Value[string] `json:"from,omitempty"`
	At   Position      `json:"at"`
}

// Param is one declared server parameter, the Postgres and MySQL knobs that
// change behaviour a rehearsal has to reproduce.
type Param struct {
	Name  string        `json:"name"`
	Value Value[string] `json:"value,omitempty"`
	At    Position      `json:"at"`
}

// Probe is a declared health probe.
type Probe struct {
	// Type is what the platform calls it: liveness, readiness, startup.
	Type string `json:"type"`
	// Path and Port are the HTTP probe's target. A probe that is not HTTP
	// leaves Path Absent and says so in Scheme.
	Path   Value[string] `json:"path,omitempty"`
	Port   Value[string] `json:"port,omitempty"`
	Scheme Value[string] `json:"scheme,omitempty"`
	// The timings, in seconds as declared.
	TimeoutSeconds      Value[int] `json:"timeout_seconds,omitempty"`
	PeriodSeconds       Value[int] `json:"period_seconds,omitempty"`
	InitialDelaySeconds Value[int] `json:"initial_delay_seconds,omitempty"`
	FailureThreshold    Value[int] `json:"failure_threshold,omitempty"`
	At                  Position   `json:"at"`
}

// Attr is one declared attribute of a component that this reader has no
// dedicated field for.
type Attr struct {
	// Name is the attribute path as written, dotted through nested blocks:
	// "sku.tier", "storage.size_gb".
	Name  string        `json:"name"`
	Value Value[string] `json:"value,omitempty"`
	At    Position      `json:"at"`
}

// Direction is which way a network rule points.
type Direction string

const (
	// Egress is traffic leaving. It is the direction that matters most to a
	// rehearsal, because a twin that can reach the internet when production
	// cannot is a twin that passes tests production would fail.
	Egress Direction = "egress"
	// Ingress is traffic arriving.
	Ingress Direction = "ingress"
)

// NetworkRule is one declared rule about what may talk to what.
type NetworkRule struct {
	// Address is the configuration's address for the rule, or for the policy
	// that carries it.
	Address   string    `json:"address"`
	Direction Direction `json:"direction"`
	// Allow is whether the rule permits or denies. A Kubernetes network policy
	// has no deny rules, so every rule it yields is an allow, and the denial
	// is the absence of a rule.
	Allow bool `json:"allow"`
	// Protocol, Ports and To are as declared. To is a CIDR, a security group
	// id, a namespace selector, whatever the rule names.
	Protocol Value[string] `json:"protocol,omitempty"`
	Ports    Value[string] `json:"ports,omitempty"`
	To       Value[string] `json:"to,omitempty"`
	At       Position      `json:"at"`
}

// Unmeasured is one thing this reader could not look at, at either level: a
// field it could not resolve, or a whole file it did not read.
type Unmeasured struct {
	// What names the thing in the report's words: "the image of the container
	// app api", or "the file".
	What string `json:"what"`
	// Why is the reason, a sentence.
	Why string   `json:"why"`
	At  Position `json:"at"`
	// Withheld says this is a value the reader RESOLVED and will not carry
	// because it is a credential, rather than one it could not work out.
	//
	// Both belong in this list, because a consumer rendering "what I cannot
	// tell you about production" wants both. They must not be COUNTED
	// together: a withheld credential is the system working and needs no
	// action, and an unresolved value is a hole in the description that
	// somebody should go and look at. A report saying "47 values could not be
	// measured" when 12 of them were passwords it deliberately refused would
	// send somebody hunting for a defect that is not there, and bury the 35
	// that are.
	Withheld bool `json:"withheld,omitempty"`
}

// String renders an unmeasured item as the one line a report prints.
func (u Unmeasured) String() string {
	if at := u.At.String(); at != "" {
		return fmt.Sprintf("%s at %s: %s", u.What, at, u.Why)
	}
	return u.What + ": " + u.Why
}

// Read reads the infrastructure as code tree rooted at dir.
//
// It takes a DIRECTORY and not a file, deliberately: see the header of this
// file, under how the state refusal is made impossible to skip.
//
// An error is returned only when the root itself cannot be walked. A file that
// could not be read is not an error, it is a Source with Read false and a
// reason, because one unreadable file must never blank the description of
// everything beside it.
func Read(ctx context.Context, dir string, opts ...Option) (*Reading, error) {
	cfg := newConfig(opts...)
	r := &Reading{Root: dir}
	if err := walk(ctx, dir, cfg, r); err != nil {
		return nil, err
	}
	sort.SliceStable(r.Sources, func(i, j int) bool { return r.Sources[i].Path < r.Sources[j].Path })
	sort.SliceStable(r.Components, func(i, j int) bool {
		if r.Components[i].Address != r.Components[j].Address {
			return r.Components[i].Address < r.Components[j].Address
		}
		return r.Components[i].At.File < r.Components[j].At.File
	})
	sort.SliceStable(r.Network, func(i, j int) bool { return r.Network[i].Address < r.Network[j].Address })
	sort.SliceStable(r.Refused, func(i, j int) bool { return r.Refused[i].Path < r.Refused[j].Path })
	return r, nil
}

// Unresolved is the subset of Unmeasured that is a genuine hole: a value this
// reader could not work out, as opposed to one it refused to carry.
//
// It exists so that a consumer counting holes does not have to remember to
// filter, which is the same reasoning as Unmeasured itself: the caller who
// forgets is the one who reports a password as a defect.
func (r *Reading) Unresolved() []Unmeasured {
	var out []Unmeasured
	for _, u := range r.Unmeasured() {
		if !u.Withheld {
			out = append(out, u)
		}
	}
	return out
}

// FromPlan says whether every component in this reading came from a plan, and
// is therefore fully resolved.
//
// It is the one call a consumer needs to decide how far to trust what follows,
// and it is deliberately ALL or nothing: a reading mixing a plan with loose
// HCL is not "from a plan", it is a mixture, and rounding it up would give the
// HCL half the plan half's authority. An empty reading answers false, because
// "nothing was read" is not a statement about how well it was read.
func (r *Reading) FromPlan() bool {
	if len(r.Components) == 0 {
		return false
	}
	for _, c := range r.Components {
		if c.From != DialectTerraformPlan {
			return false
		}
	}
	return true
}

// Unmeasured collects everything this reading cannot tell a caller about, at
// both levels: every file that was seen and not read, and every field of every
// component that is Unreadable or Withheld, each marked with which it is.
//
// It exists so that "what could not be measured" is ONE call rather than a
// walk every consumer writes for itself, because the consumer that forgets to
// walk it is the one that reports a hole as a zero.
func (r *Reading) Unmeasured() []Unmeasured {
	var out []Unmeasured
	for _, s := range r.Sources {
		if !s.Read {
			out = append(out, Unmeasured{What: "the file " + s.Path, Why: s.Why, At: Position{File: s.Path}})
		}
	}
	for _, c := range r.Components {
		what := func(field string) string {
			return fmt.Sprintf("the %s of %s %s", field, c.Kind, c.Name)
		}
		add := func(field string, st State, why string, at Position) {
			if st == Unreadable || st == Withheld {
				out = append(out, Unmeasured{
					What: what(field), Why: why, At: at, Withheld: st == Withheld,
				})
			}
		}
		add("presence", c.Present.State(), c.Present.Why(), c.At)
		add("image", c.Image.State(), c.Image.Why(), c.Image.At())
		add("cpu", c.CPU.State(), c.CPU.Why(), c.CPU.At())
		add("memory", c.Memory.State(), c.Memory.Why(), c.Memory.At())
		add("replica count", c.Replicas.State(), c.Replicas.Why(), c.Replicas.At())
		add("engine", c.Engine.State(), c.Engine.Why(), c.Engine.At())
		add("version", c.Version.State(), c.Version.Why(), c.Version.At())
		for _, p := range c.Params {
			add("server parameter "+p.Name, p.Value.State(), p.Value.Why(), p.At)
		}
		for _, s := range c.Secrets {
			add("secret reference "+s.Name, s.From.State(), s.From.Why(), s.At)
		}
		for _, v := range c.Env {
			add("presence of the environment variable "+v.Name, v.Present.State(), v.Present.Why(), v.At)
		}
		for _, a := range c.Attrs {
			add("attribute "+a.Name, a.Value.State(), a.Value.Why(), a.At)
		}
	}
	return out
}
