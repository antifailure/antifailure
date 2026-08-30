// Package env brings an environment up and takes it down.
//
// It is the only place that knows the order of operations, and the order is
// the product: acquire the branch lock, open the state database, record an
// intent for every resource before it exists, branch the database, build the
// services, place them on a sealed network, and report where they are. Each
// of those steps lives in a package that does not know about the others, which
// is what keeps them testable, and this is where they are put in a line.
//
// The rule that shapes it: nothing is created before it is recorded. A
// resource made before its journal entry is a resource teardown cannot find,
// and an environment nobody can tear down is worse than one that failed to
// come up.
package env

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/build"
	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	neondb "github.com/antifailure/antifailure/engine/internal/db/neon"
	"github.com/antifailure/antifailure/engine/internal/envcert"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/lock"
	"github.com/antifailure/antifailure/engine/internal/mockpack"
	"github.com/antifailure/antifailure/engine/internal/policy"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/state"
	"github.com/antifailure/antifailure/engine/internal/webhook"
	"github.com/antifailure/antifailure/engine/pkg/extension"
	"github.com/antifailure/antifailure/engine/pkg/provider"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// StateDir is where local state lives, relative to the repository root.
const StateDir = ".antifailure"

// Options configure an orchestrator.
type Options struct {
	// Root is the repository root, where the manifest and .antifailure live.
	Root string
	// Manifest is the loaded, validated manifest.
	Manifest *schema.Manifest
	// Branch is the source control branch this environment is for.
	Branch string
	// Clock is the time source.
	Clock clock.Clock
	// Progress receives human readable lines, already redacted.
	Progress func(string)
	// Rebuild forces images to be built even when an identical one exists.
	Rebuild bool
	// Verbose streams the full build output.
	//
	// Off by default. A Docker build prints a line per instruction and a line
	// per layer, and seventeen of those between "building web" and "web is
	// ready" buries the two lines somebody actually wanted. The output is kept
	// either way and printed in full when the build fails, which is the only
	// time it explains anything.
	Verbose bool
	// Redactor is applied to everything that reaches a log or an artifact.
	Redactor *redact.Redactor
	// Getenv reads the environment this command is running in, for sandbox
	// credentials. Nil uses the process environment.
	Getenv func(string) string
	// Secrets is where declared variables are looked up. Nil builds the
	// default chain: this process's environment, then a .env beside the
	// manifest, then the encrypted local store.
	Secrets *secrets.Chain
	// Extensions is consulted before an environment is created. Nil uses the
	// process-wide registry, which in the community build is empty, so the
	// check costs one function call that returns nil.
	//
	// It can only refuse. There is no way for a hook to permit something the
	// manifest does not, because a hook that could widen an egress rule would
	// be a way to change what an environment reaches without changing the
	// repository, and that is the thing this system exists to prevent.
	Extensions *extension.Registry
}

// Orchestrator runs the lifecycle for one environment.
type Orchestrator struct {
	opts     Options
	envID    string
	progress func(string)
	sinks    []events.Sink
}

// New returns an orchestrator for a branch.
func New(opts Options) (*Orchestrator, error) {
	if opts.Manifest == nil {
		return nil, aferrors.Coded(aferrors.AFMAN001, "path", opts.Root)
	}
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	if opts.Redactor == nil {
		opts.Redactor = redact.New()
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}
	return &Orchestrator{opts: opts, envID: EnvID(opts.Manifest.Name, opts.Branch), progress: progress}, nil
}

// EnvID returns the identifier for a project and branch.
//
// Deterministic, so running af up twice on one branch addresses the same
// environment rather than making a second one. Short and lowercase, because it
// becomes part of a container name, a network name, and a hostname, and the
// strictest of those is the hostname. A hash tail is appended because branch
// names collide once they are cut to fit: feature/add-billing-to-the-checkout
// and feature/add-billing-to-the-cart share their first twenty characters.
func EnvID(project, branch string) string {
	if branch == "" {
		branch = "default"
	}
	sum := sha256.Sum256([]byte(project + "\x00" + branch))
	return trimForName(project, 12) + "-" + trimForName(branch, 16) + "-" + hex.EncodeToString(sum[:])[:6]
}

var unsafeInName = regexp.MustCompile(`[^a-z0-9]+`)

func trimForName(s string, max int) string {
	s = unsafeInName.ReplaceAllString(strings.ToLower(s), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "x"
	}
	if len(s) > max {
		s = strings.Trim(s[:max], "-")
	}
	if s == "" {
		s = "x"
	}
	return s
}

// EnvID reports the identifier this orchestrator works on.
func (o *Orchestrator) EnvID() string { return o.envID }

// AddSink subscribes something to the event stream for every lifecycle this
// orchestrator runs from now on.
//
// Call it before Up, Down, or Status. The bus does not exist between commands,
// so a sink is not attached to a bus here; it is remembered and attached to
// each session as that session opens, and closed with it. That is what lets a
// caller hand over a dashboard without knowing when the bus is created or
// having to close it.
//
// This is the whole reason the engine emits anything. Before af up --hud there
// was a bus with no subscribers, and every event it published was delivered to
// nobody.
func (o *Orchestrator) AddSink(s events.Sink) {
	if s == nil {
		return
	}
	o.sinks = append(o.sinks, s)
}

// emit publishes one event to whatever sinks the caller attached.
//
// It tolerates a nil session and a session with no bus. Those are real states:
// a failure inside open returns before there is a bus, and the alternative to
// tolerating them here is a nil check repeated at every call site plus a panic
// the first time one is forgotten.
func (o *Orchestrator) emit(s *session, level events.Level, t events.Type, msg string, f ...events.Field) {
	if s == nil || s.bus == nil {
		return
	}
	s.bus.Emit(o.envID, t, level, msg, f...)
}

// event publishes at info level, which is what almost every lifecycle step is.
func (o *Orchestrator) event(s *session, t events.Type, msg string, f ...events.Field) {
	o.emit(s, events.LevelInfo, t, msg, f...)
}

// eventErr publishes at error level, which is what puts a line in the
// dashboard's error pane rather than only in the scrolling tail.
func (o *Orchestrator) eventErr(s *session, t events.Type, msg string, f ...events.Field) {
	o.emit(s, events.LevelError, t, msg, f...)
}

// Result is what Up produced.
type Result struct {
	EnvID string
	// URL is where the first web service can be reached, if there is one.
	URL string
	// Services is what came up.
	Services []provider.RunningService
	// Golden is the database version the branch came from.
	Golden string
	// Built counts images built, and Cached counts those already present.
	Built, Cached int
	// Duration is how long the whole thing took.
	Duration time.Duration
	// Proxied reports whether the egress sidecar is deciding outbound traffic.
	// When it is not, the environment has no route out at all.
	Proxied bool
	// Rules is how many egress rules the sidecar is enforcing.
	Rules int
}

// session holds everything Up opens and must close.
type session struct {
	lock    *lock.Lock
	db      *state.DB
	bus     *events.Bus
	journal *journal.Journal
	dbProv  provider.Database
	runtime *local.Runtime
	builder *build.DockerBuilder
}

func (s *session) close() {
	// Closed in reverse order of opening, and every one attempted, so a
	// failure in the middle does not strand the rest. The lock goes last
	// because releasing it lets another process in.
	if s.builder != nil {
		_ = s.builder.Close()
	}
	if s.runtime != nil {
		_ = s.runtime.Close()
	}
	if s.dbProv != nil {
		_ = s.dbProv.Close()
	}
	if s.bus != nil {
		_ = s.bus.Close()
	}
	if s.db != nil {
		_ = s.db.Close()
	}
	if s.lock != nil {
		_ = s.lock.Release()
	}
}

// open acquires the lock and every provider, in the order that lets a failure
// leave the least behind.
func (o *Orchestrator) open(ctx context.Context, command string) (*session, error) {
	s := &session{}
	stateDir := filepath.Join(o.opts.Root, StateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFRUN040, "detail", err.Error())
	}

	// The lock comes first. Two af up runs on one branch would otherwise race
	// on the same container names and both fail in ways neither explains.
	l, err := lock.Acquire(filepath.Join(stateDir, o.envID+".lock"), o.opts.Clock, command)
	if err != nil {
		return nil, err
	}
	s.lock = l

	if s.db, err = state.Open(ctx, stateDir); err != nil {
		s.close()
		return nil, err
	}
	// Continued from where the last command left it, so that Seq means what
	// it says: monotonic per environment, and a gap in it is a gap.
	s.bus = events.NewBus(o.opts.Clock, events.ResumeSequence(o.envID,
		events.LastSequence(filepath.Join(o.opts.Root, StateDir, "events"), o.envID)))
	// Before the journal, so that a sink attached by the caller sees the very
	// first resource intent rather than joining part way through a run.
	for _, sink := range o.sinks {
		if sink != nil {
			s.bus.AddSink(sink)
		}
	}
	s.journal = journal.New(s.db, o.opts.Clock, s.bus)

	if s.dbProv, err = o.newDatabaseProvider(ctx); err != nil {
		s.close()
		return nil, err
	}
	if s.runtime, err = o.newRuntime(); err != nil {
		s.close()
		return nil, err
	}
	if s.builder, err = build.NewDockerBuilder(build.DockerOptions{
		Clock: o.opts.Clock, Redactor: o.opts.Redactor, NoCache: o.opts.Rebuild,
		Getenv: o.opts.Getenv,
	}); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// secretChain is where declared variables are looked up.
//
// The order is most specific first, and each step is where somebody would
// reasonably have put the value. An explicit export beats a file, because
// somebody who typed it meant it and is usually debugging. A file beats the
// local store, because a repository's .env is checked out with the branch. The
// store is last because it is the long-lived default.
func (o *Orchestrator) secretChain() *secrets.Chain {
	if o.opts.Secrets != nil {
		return o.opts.Secrets
	}
	getenv := o.opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return secrets.NewChain(
		&secrets.EnvSource{
			Label: "this shell's environment",
			Getenv: func(name string) (string, bool) {
				v := getenv(name)
				return v, v != ""
			},
		},
		secrets.NewDotEnvSource(filepath.Join(o.opts.Root, ".env")),
		secrets.NewFileStore(
			filepath.Join(o.opts.Root, ".antifailure", "secrets.enc"),
			secrets.StorePassphrase(getenv),
		),
		// Last, and only where the platform has one. A keyring entry is the
		// long lived default on a workstation; everything above it is a way to
		// override that for one run.
		secrets.NewKeyringSource(secrets.NewSystemKeyring(), secrets.DefaultKeyringService),
	)
}

// resolveSecrets looks up everything the manifest declares.
//
// Two things happen here that decide what a service actually receives.
//
// A service gets what the manifest declares and nothing else. The engine's own
// environment is not passed through, because a preview environment that
// inherited the shell it was started from would inherit AWS credentials, a
// production database URL, and whatever else is exported on a laptop.
//
// A sandbox credential goes to the sidecar and never to a service. The whole
// point of substituting it at the boundary is that the application never holds
// one, so the service receives an obvious marker instead. A service handed
// nothing at all usually crashes on startup with a message about
// configuration, which reads as a bug in the tool.
func (o *Orchestrator) resolveSecrets(ctx context.Context) (*secrets.Resolved, error) {
	chain := o.secretChain()
	resolved, err := secrets.Resolve(ctx, chain, secrets.Request{
		Declared: secrets.DeclaredVars(o.opts.Manifest),
		Sandbox:  secrets.SandboxNames(o.opts.Manifest),
		EnvID:    o.envID,
	})
	if err != nil {
		var live *secrets.LiveCredentialError
		if errors.As(err, &live) {
			// The one check that has to happen before the environment exists. A
			// live key handed to the sidecar is substituted into every request
			// to that provider, which is the opposite of what sandbox mode is
			// for and charges real cards.
			return nil, aferrors.Coded(aferrors.AFSEC003, "name", live.Name)
		}
		return nil, err
	}

	// What was found, before what was not. Somebody whose run has just failed
	// wants to see that four of five variables resolved and which one did not,
	// rather than only the failure: the four that worked are how they work out
	// where the fifth should go. Names and sources, never values, because this
	// is what a support bundle carries.
	for _, r := range resolved.Resolutions {
		o.progress(fmt.Sprintf("  %s from %s", r.Name, r.Source))
	}
	for _, m := range resolved.Optional {
		o.progress(fmt.Sprintf("  %s was not found and is not required", m.Name))
	}

	if len(resolved.Missing) > 0 {
		names := make([]string, 0, len(resolved.Missing))
		for _, m := range resolved.Missing {
			names = append(names, m.Name)
		}
		sort.Strings(names)
		// Every source that was considered, including the ones that are not
		// there. The place to put a value is very often the .env file that does
		// not exist yet, and a list of only the usable sources would never
		// mention it.
		return nil, aferrors.Coded(aferrors.AFSEC001,
			"names", strings.Join(names, ", "),
			"sources", strings.Join(chain.Considered(ctx), ", "))
	}

	// Registered before anything is built, so a value cannot reach a log line
	// between being read and being registered.
	for _, value := range resolved.Service {
		o.opts.Redactor.Register(value.Reveal())
	}
	for _, value := range resolved.Sidecar {
		o.opts.Redactor.Register(value.Reveal())
	}

	return resolved, nil
}

// WebhookSecrets returns the signing secrets this environment uses, by the
// variable name the application reads.
//
// A value already set in this shell wins, because somebody who set one has a
// reason and is probably matching a fixture recorded elsewhere. Otherwise the
// secret is derived from the environment identifier, so a preview environment
// needs no configuration at all to have working signature verification.
func (o *Orchestrator) WebhookSecrets() map[string]string {
	if o.opts.Manifest.Egress == nil {
		return nil
	}
	getenv := o.opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	out := map[string]string{}
	for _, r := range o.opts.Manifest.Egress.Rules {
		if r.WebhookPath == "" {
			continue
		}
		provider := webhook.ForHost(r.Host)
		if provider == "" {
			continue
		}
		name := webhook.SecretEnvFor(provider)
		if _, done := out[name]; done {
			continue
		}
		if value := getenv(name); value != "" {
			out[name] = value
			continue
		}
		out[name] = webhook.SecretFor(o.envID, provider)
	}
	return out
}

// WebhookSecretFor returns the secret used for one provider.
func (o *Orchestrator) WebhookSecretFor(provider string) string {
	name := webhook.SecretEnvFor(provider)
	if name == "" {
		return ""
	}
	if v, ok := o.WebhookSecrets()[name]; ok {
		return v
	}
	getenv := o.opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if v := getenv(name); v != "" {
		return v
	}
	return webhook.SecretFor(o.envID, provider)
}

// mockPacks reads the fixture packs the manifest points at.
//
// The packs that ship with the engine are compiled into the sidecar and are
// always available, so a manifest names one only to add a provider or to
// override a built in route.
//
// A pack that will not parse is refused here rather than in the sidecar. A
// broken file that reached the sidecar would leave its host answering nothing,
// and the failure would look like a missing route rather than a bad file.
func (o *Orchestrator) mockPacks() ([]string, error) {
	if o.opts.Manifest.Egress == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range o.opts.Manifest.Egress.Rules {
		if r.Fixtures == "" || seen[r.Fixtures] {
			continue
		}
		seen[r.Fixtures] = true

		rel := filepath.FromSlash(strings.TrimPrefix(r.Fixtures, "./"))
		body, err := os.ReadFile(filepath.Join(o.opts.Root, rel))
		if err != nil {
			return nil, aferrors.Wrap(err, aferrors.AFNET010,
				"method", "any", "path", "any", "host", r.Host,
				"suggestion", r.Fixtures)
		}
		if _, err := mockpack.Parse(body); err != nil {
			return nil, aferrors.Wrap(err, aferrors.AFMAN002,
				"path", r.Fixtures, "detail", err.Error())
		}
		out = append(out, string(body))
	}
	return out, nil
}

// modelEnv carries a model key to the sidecar, for a rule in synth mode.
//
// Only when a rule actually asks for one. An environment with no synth rule
// gets no key, because handing a credential to a container that has no use for
// it is a credential in one more place for no reason.
func (o *Orchestrator) modelEnv() []string {
	if o.opts.Manifest.Egress == nil {
		return nil
	}
	wanted := false
	for _, r := range o.opts.Manifest.Egress.Rules {
		if r.Mode == schema.ModeSynth {
			wanted = true
			break
		}
	}
	if !wanted && o.opts.Manifest.Egress.Default != schema.ModeSynth {
		return nil
	}

	getenv := o.opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var out []string
	for _, name := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "AF_MODEL",
		"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL",
	} {
		if v := getenv(name); v != "" {
			out = append(out, name+"="+v)
			if strings.HasSuffix(name, "_API_KEY") {
				o.opts.Redactor.Register(v)
			}
		}
	}
	return out
}

// needsInspection reports whether any rule requires reading inside TLS.
//
// Asked here rather than in the runtime because it decides whether to issue a
// certificate at all, and issuing one that is never used still means every
// service in the environment trusts a key that exists.
func needsInspection(e *schema.Egress) bool {
	eng, err := policy.New(e)
	if err != nil {
		// A policy that does not compile fails later with a better message.
		// Answering yes here is the conservative direction: a certificate
		// nobody uses costs nothing, and refusing to issue one that was needed
		// silently downgrades the policy to host rules.
		return true
	}
	for _, r := range eng.Rules() {
		if eng.InspectsHost(strings.TrimPrefix(r.Host, "*."), 443) {
			return true
		}
	}
	return eng.InspectsHost("probe.invalid", 443)
}

// newRuntime builds the runtime the manifest asked for.
//
// The same reason newDatabaseProvider exists. A manifest could say kubernetes,
// pass validation, and get containers on the laptop that ran af, which is a
// difference nobody would notice until they went looking for their environment
// in a cluster.
//
// Only the local runtime ships today, so this returns a concrete type rather
// than an interface. An interface with one implementation is scaffolding that
// has to be maintained before anything needs it; the day a second runtime
// exists is the day to introduce one, and this function is where it goes.
func (o *Orchestrator) newRuntime() (*local.Runtime, error) {
	kind := schema.RuntimeLocal
	if m := o.opts.Manifest; m != nil && m.Runtime != nil && m.Runtime.Provider != "" {
		kind = m.Runtime.Provider
	}
	if kind != schema.RuntimeLocal {
		return nil, aferrors.Coded(aferrors.AFMAN002,
			"path", filepath.Join(o.opts.Root, "antifailure.yaml"),
			"detail", fmt.Sprintf(
				"runtime.provider is %q, and this build has only the local runtime. "+
					"Remove the field or set it to local", kind))
	}
	return local.New(local.Options{Clock: o.opts.Clock, Redactor: o.opts.Redactor})
}

// newDatabaseProvider builds the provider the manifest asked for.
//
// The manifest names a provider and the engine builds it here. Without this
// the constants in the schema are a list of intentions: a manifest could say
// neon, pass validation, and silently get Docker, which is the kind of gap
// that only shows up when somebody wonders why their preview has no production
// data in it.
func (o *Orchestrator) newDatabaseProvider(ctx context.Context) (provider.Database, error) {
	m := o.opts.Manifest
	kind := schema.DBDocker
	if m != nil && m.Database != nil && m.Database.Provider != "" {
		kind = m.Database.Provider
	}

	switch kind {
	case schema.DBDocker:
		return dockerdb.New(dockerdb.Options{
			Version: databaseVersion(m), Clock: o.opts.Clock,
		})

	case schema.DBNeon:
		db := m.Database
		if db.Project == "" {
			return nil, aferrors.Coded(aferrors.AFMAN002,
				"path", filepath.Join(o.opts.Root, "antifailure.yaml"),
				"detail", "database.provider is neon and database.project is empty; "+
					"the provider needs the Neon project to create branches in")
		}
		name := db.APIKeyEnv
		if name == "" {
			name = "NEON_API_KEY"
		}
		key, _, found, err := o.secretChain().Lookup(ctx, name)
		if err != nil {
			return nil, err
		}
		if !found || key.IsZero() {
			return nil, aferrors.Coded(aferrors.AFSEC001,
				"names", name,
				"sources", strings.Join(o.secretChain().Considered(ctx), ", "))
		}
		return neondb.New(neondb.Options{
			APIKey:      key,
			ProjectID:   db.Project,
			Clock:       o.opts.Clock,
			MaxBranches: db.MaxBranches,
		})

	default:
		// Named in the schema and not built here. Saying so is better than
		// quietly falling back to Docker, which would hand somebody an empty
		// preview and no reason for it.
		return nil, aferrors.Coded(aferrors.AFMAN002,
			"path", filepath.Join(o.opts.Root, "antifailure.yaml"),
			"detail", fmt.Sprintf("database.provider is %q, which this build does not have", kind))
	}
}

// pickGolden chooses the version a branch is made from.
//
// Verified, and made under the rules this manifest declares. It returns how
// many verified versions were refused for the second reason so that the
// caller can say something better than "no golden": a store with six goldens
// in it and none of them usable here is a different situation from an empty
// one, and telling them apart is the difference between "run af golden
// refresh" and "something is wrong".
//
// A version that records no rules at all is refused too, and that is the part
// worth arguing about. The first version of this accepted one, reasoning that
// a missing record should not break a machine that had goldens on it already.
// That reasoning is wrong: a golden whose provenance is unknown cannot be shown
// to match this manifest, and branching it is exactly the cross contamination
// this exists to stop. It is not hypothetical. With the lenient rule in place,
// bringing the control plane up branched an empty golden the masking test suite
// had published minutes earlier, and the environment came up with a schema and
// no data in it.
//
// Refusing costs a refresh, and the error says to run one. Accepting costs
// somebody a preview built on another project's data, and says nothing.
func pickGolden(goldens []provider.GoldenVersion, wantRules string) (string, int) {
	refused := 0
	for _, g := range goldens {
		if !g.Verified {
			continue
		}
		if g.RulesHash != wantRules {
			refused++
			continue
		}
		return g.ID, refused
	}
	return "", refused
}

func databaseVersion(m *schema.Manifest) int {
	if m.Database != nil && m.Database.Version > 0 {
		return m.Database.Version
	}
	return 17
}

// checkPolicy asks the registered hooks whether this environment may exist.
//
// The community build registers nothing, so this returns nil after one call
// over an empty slice. It is here rather than in the enterprise edition because
// the socket has to be in the thing being extended, and because a hook that
// only exists in a build nobody runs is a hook nobody has tested.
func (o *Orchestrator) checkPolicy(ctx context.Context) error {
	registry := o.opts.Extensions
	if registry == nil {
		registry = extension.Default
	}
	if registry.Empty() {
		return nil
	}

	m := o.opts.Manifest
	req := extension.EnvironmentRequest{
		Repository: m.Name,
		Branch:     o.opts.Branch,
		EnvID:      o.envID,
		Provider:   "docker",
	}
	if m.Database != nil && m.Database.Provider != "" {
		req.Provider = string(m.Database.Provider)
	}
	if m.Egress != nil {
		req.EgressModes = make(map[string]string, len(m.Egress.Rules))
		for _, rule := range m.Egress.Rules {
			req.EgressHosts = append(req.EgressHosts, rule.Host)
			req.EgressModes[rule.Host] = string(rule.Mode)
		}
	}

	if err := registry.CheckPolicy(ctx, req); err != nil {
		// Returned as it came. A policy hook's message names the policy and
		// what would satisfy it, and wrapping it in "environment creation
		// failed" would bury the only sentence that helps.
		return err
	}
	return nil
}

// Up brings the environment up.
func (o *Orchestrator) Up(ctx context.Context) (result *Result, rerr error) {
	started := o.opts.Clock.Now()
	s, err := o.open(ctx, "af up")
	if err != nil {
		return nil, err
	}
	defer s.close()

	res := &Result{EnvID: o.envID}

	o.event(s, events.EnvCreating, "creating "+o.envID, events.F("branch", o.opts.Branch))
	// Registered after s.close, so it runs before it: defers unwind last in
	// first out and the bus has to still be open for the last event to reach a
	// sink. Reading the named results is what makes this cover every return in
	// the function, including the ones added after this was written.
	defer func() {
		if rerr != nil {
			o.eventErr(s, events.EnvFailed, rerr.Error())
			return
		}
		o.event(s, events.EnvReady, o.envID+" is ready",
			events.F("url", result.URL), events.F("proxied", result.Proxied))
	}()

	// Before anything is created, so that a refusal costs nothing. Checking
	// after the database branch exists would leave a branch behind every time a
	// policy refused, which is how a policy control becomes a resource leak.
	if err := o.checkPolicy(ctx); err != nil {
		return res, err
	}
	defer func() {
		// Reported whether the environment came up or not, because a failed
		// creation still consumed capacity and a meter that only counts
		// successes undercounts exactly the runs that cost the most.
		o.observe(ctx, extension.LifecycleEvent{
			Repository: o.opts.Manifest.Name,
			EnvID:      o.envID,
			Kind:       "environment.created",
			Seconds:    o.opts.Clock.Since(started).Seconds(),
		})
	}()

	// Before the database and before anything is built. A variable that is
	// missing produces a failure ten seconds later inside a container, in a log
	// nobody is watching, and it looks like the application is broken rather
	// than the configuration.
	resolved, err := o.resolveSecrets(ctx)
	if err != nil {
		return res, err
	}

	golden, branch, dbURL, migrateURL, err := o.database(ctx, s)
	if err != nil {
		return res, err
	}
	res.Golden = golden

	specs, built, cached, err := o.buildServices(ctx, s)
	if err != nil {
		return res, err
	}
	res.Built, res.Cached = built, cached

	recordIntent := func(kind, id string) error {
		_, jerr := s.journal.Intent(ctx, o.envID, "local", journal.Kind(kind), id, nil)
		return jerr
	}

	// The network comes before the database, and the database before the
	// services, and the order is not arbitrary. A service reads DATABASE_URL
	// out of its environment at creation time, and that string names the
	// database by its alias on this network. Attaching after the services
	// started would hand every one of them an address that did not resolve
	// when it read it, which is what the first run of this actually did.
	networkID, err := s.runtime.EnsureNetworks(ctx, o.envID, recordIntent)
	if err != nil {
		return res, err
	}
	// Only a provider whose branches are local containers has anything to
	// attach. A cloud provider's connection string already works from inside a
	// container, so the URL handed to the services is the one it gave us. The
	// interface says as much; this is where that becomes true rather than a
	// comment.
	insideURL, insideMigrateURL := dbURL, migrateURL
	if attachable, ok := s.dbProv.(local.Attachable); ok {
		insideURL, err = s.runtime.AttachDatabase(ctx, attachable, branch.ProviderRef, networkID, dbURL)
		if err != nil {
			return res, err
		}
		insideMigrateURL = insideURL
	}
	o.opts.Redactor.Register(insideURL.Reveal())
	o.opts.Redactor.Register(insideMigrateURL.Reveal())

	// An authority is generated only when something in the policy needs the
	// sidecar to read inside TLS. An environment whose rules are all plain
	// allow or block never terminates a connection, and asking its services to
	// trust a certificate they will never see is a change to their trust store
	// for no reason.
	// The runtime's progress lines go to the event stream as well as to the
	// terminal. They are the only thing said during the readiness wait, which
	// is the longest part of a run, and in dashboard mode the terminal
	// reporter is silenced, so without this the display goes quiet for
	// minutes at exactly the point somebody is watching it.
	names := make(map[string]bool, len(specs))
	for _, sv := range specs {
		names[sv.Name] = true
	}
	spec := provider.EnvSpec{
		EnvID: o.envID, Branch: o.opts.Branch, Services: specs,
		Egress:               o.opts.Manifest.Egress,
		DatabaseURL:          insideURL,
		MigrationDatabaseURL: insideMigrateURL,
		Journal:              recordIntent,
		Progress: func(line string) {
			// engine.progress, not service.log. A real run showed why: the
			// non-TTY fallback folds service.log away as noise, correctly,
			// because a service writes thousands of lines and a build log is
			// not the place for them. These are lifecycle steps rather than
			// service output, they arrive perhaps four times in a run, and
			// folding them away restored exactly the silence this exists to
			// end.
			o.event(s, events.Progress, line, serviceField(line, names)...)
			o.progress(line)
		},
	}
	spec.SandboxCredentials = resolved.Sidecar
	spec.ModelEnv = o.modelEnv()

	// The resolved values reach the services here rather than in the spec
	// builder, because the lookup is per environment and the builder runs per
	// service. Each service still receives only the names it declared, so one
	// service's variable does not travel to another's.
	for i := range spec.Services {
		for name := range spec.Services[i].Env {
			if value, ok := resolved.Service[name]; ok {
				spec.Services[i].Env[name] = value
			}
		}
	}

	packs, err := o.mockPacks()
	if err != nil {
		return res, err
	}
	spec.MockPacks = packs

	// Every service receives the signing secrets for the providers whose
	// callbacks this environment will send, so that af webhook trigger and the
	// application's own verification agree without anybody configuring the
	// same value twice.
	for name, value := range o.WebhookSecrets() {
		for i := range spec.Services {
			if spec.Services[i].Env == nil {
				spec.Services[i].Env = map[string]secrets.Value{}
			}
			if _, set := spec.Services[i].Env[name]; !set {
				spec.Services[i].Env[name] = secrets.New(value)
			}
		}
		o.opts.Redactor.Register(value)
	}

	if needsInspection(o.opts.Manifest.Egress) {
		ca, caErr := envcert.Generate(o.envID, o.opts.Clock.Now())
		if caErr != nil {
			return res, caErr
		}
		spec.CACertPEM, spec.CAKeyPEM = ca.CertPEM, ca.KeyPEM
		o.opts.Redactor.Register(ca.KeyPEM.Reveal())
		o.progress("issued an environment certificate so the proxy can read inside TLS where the policy needs it")
	}

	for _, svc := range spec.Services {
		o.event(s, events.ServiceStarting, svc.Name+" is starting",
			events.F("service", svc.Name), events.F("kind", svc.Kind),
			events.F("state", "starting"))
	}

	env, err := s.runtime.Up(ctx, spec)
	res.Services = env.Services
	// Emitted before the error is returned, not after. A run that failed with
	// three of five services up is exactly when somebody needs to see which
	// three, and returning first would leave the dashboard showing all five
	// starting forever.
	for _, svc := range env.Services {
		t, level := events.ServiceReady, events.LevelInfo
		if !svc.Ready {
			t, level = events.ServiceExited, events.LevelError
		}
		// Empty fields are left off rather than sent empty. A real run showed
		// "service.ready web is running detail= " on every line, because a
		// service that started cleanly has no detail to give, and a key with
		// nothing after it is noise in the one place a reader is scanning.
		o.emit(s, level, t, svc.Name+" is "+orDefault(svc.State, "unknown"),
			nonEmpty("service", svc.Name, "kind", svc.Kind,
				"url", svc.URL, "state", svc.State, "detail", svc.Detail)...)
	}
	if err != nil {
		return res, err
	}

	res.URL = env.URL()
	res.Proxied = env.ProxyReady
	res.Duration = o.opts.Clock.Since(started)
	return res, nil
}

// nonEmpty builds fields from key and value pairs, leaving out the values that
// are empty.
//
// Variadic strings rather than a map so the order is the caller's: the display
// sorts by key, but the NDJSON sink and anything reading the file get them as
// written, and "service" first is what a person scanning a log wants.
func nonEmpty(kv ...string) []events.Field {
	out := make([]events.Field, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] == "" {
			continue
		}
		out = append(out, events.F(kv[i], kv[i+1]))
	}
	return out
}

// serviceField attaches a service name to a runtime progress line when the
// line names one.
//
// The runtime writes "web: running migrations", so the name is the prefix. It
// is matched against the services this environment actually has rather than
// split on the first colon, because "error: something" is not a service called
// error and a row named for one would be a row nobody can explain.
func serviceField(line string, names map[string]bool) []events.Field {
	name, _, ok := strings.Cut(line, ": ")
	if !ok || !names[name] {
		return nil
	}
	return []events.Field{events.F("service", name)}
}

// database makes sure a verified golden exists and branches it.
// database branches the golden and returns the two connection strings that
// come out of it.
//
// Two, because they are for different things. An application should use the
// pooled endpoint where the provider has one; a migration must not, because a
// transaction pooler does not support the session level features migrations
// use. A provider with no pool returns the same string twice, which is the
// honest answer for it.
func (o *Orchestrator) database(ctx context.Context, s *session) (string, provider.Branch, secrets.Value, secrets.Value, error) {
	var zero provider.Branch

	goldens, err := s.dbProv.ListGoldens(ctx)
	if err != nil {
		return "", zero, secrets.Value{}, secrets.Value{}, err
	}

	// Which rules produced it, not merely that something produced it.
	//
	// The store is per repository and a repository holds more than one
	// manifest. Selecting the newest verified golden and nothing else means a
	// preview of one project branches a masked copy of another project's
	// database, which is what actually happened here: bringing the control
	// plane up branched a golden an example had refreshed thirty seconds
	// earlier, and the environment came up with the wrong schema entirely.
	// Copying one project's data into another project's preview is the failure
	// this product is sold to prevent.
	//
	// The rules digest is the right key and it was already recorded for this,
	// unread: GoldenVersion.RulesHash says it "identifies the masking rules
	// that produced it, so that a rules change can be detected without
	// re-reading the data". Matching on it is stronger than matching on a
	// name, because it also refuses a golden of the right database masked
	// under rules somebody has since changed, which is a golden whose
	// attestation is about a different question than the one being asked.
	_, wantRules, rulesErr := o.rules()
	if rulesErr != nil {
		return "", zero, secrets.Value{}, secrets.Value{}, rulesErr
	}
	version, refused := pickGolden(goldens, wantRules)
	// A configured source and nothing to branch is a refusal rather than an
	// empty database.
	//
	// The fallback below makes an empty golden and says so in its comment: "No
	// source database is configured, so the golden is an empty one". That
	// condition was never checked. A manifest naming a source and reaching
	// this line got an empty database, the migrations ran against it, and the
	// environment came up looking correct with none of production's shape or
	// volume in it, which is the one thing a preview is for.
	if version == "" && o.opts.Manifest.Database != nil && o.opts.Manifest.Database.SourceURLEnv != "" {
		return "", zero, secrets.Value{}, secrets.Value{},
			aferrors.Coded(aferrors.AFDB008, "count", fmt.Sprint(refused))
	}
	if version == "" {
		o.progress("no golden yet, creating one")
		o.event(s, events.GoldenRefreshing, "no golden yet, creating one",
			events.F("phase", "refreshing"))
		seed := ""
		if o.opts.Manifest.Database != nil {
			seed = o.opts.Manifest.Database.Seed
		}
		gv, refreshErr := s.dbProv.RefreshGolden(ctx, provider.GoldenSpec{
			Version:   databaseVersion(o.opts.Manifest),
			RulesHash: seedRulesHash(seed),
			// No source database is configured, so the golden is built from
			// nothing. Where the manifest names a seed command, that command
			// is what puts data in it; otherwise the schema arrives with the
			// migrations and the golden is empty. Masking and verification
			// still run either way, because a seeded database is still a
			// database somebody could have put an address into.
			Mask: func(ctx context.Context, candidate secrets.Value) error {
				return o.runSeed(ctx, s, seed, candidate)
			},
			Verify: func(context.Context, secrets.Value) (string, error) { return `{"rows":0}`, nil },
		})
		if refreshErr != nil {
			return "", zero, secrets.Value{}, secrets.Value{}, refreshErr
		}
		version = gv.ID
		o.event(s, events.GoldenReady, "golden "+version+" is ready",
			events.F("phase", "golden"), events.F("version", version),
			events.F("verified", gv.Verified))
	} else {
		// Reported even though nothing was done, because the display cannot
		// tell a verified golden from an unverified one by its absence, and
		// calling a verified golden unverified on every ordinary run is worse
		// than one extra event.
		o.event(s, events.GoldenReady, "golden "+version+" is verified",
			events.F("phase", "golden"), events.F("version", version),
			events.F("verified", true))
	}
	o.progress("branching the database from " + version)
	o.event(s, events.DBBranching, "branching from "+version,
		events.F("phase", "branching"), events.F("version", version))

	if _, err := s.journal.Intent(ctx, o.envID, "docker", journal.Kind("database"), o.envID, nil); err != nil {
		return "", zero, secrets.Value{}, secrets.Value{}, err
	}
	branch, err := s.dbProv.Branch(ctx, version, o.envID)
	if err != nil {
		return "", zero, secrets.Value{}, secrets.Value{}, err
	}
	direct, err := s.dbProv.ConnString(ctx, branch, provider.ConnDirect)
	if err != nil {
		return "", zero, secrets.Value{}, secrets.Value{}, err
	}
	// Every value that reaches a log goes through the redactor, and the
	// connection string is registered so that it is redacted wherever it
	// appears rather than wherever somebody remembered to.
	o.opts.Redactor.Register(direct.Reveal())

	o.event(s, events.DBBranched, "branched from "+version,
		events.F("phase", "branched"), events.F("version", version))

	pooled := direct
	if s.dbProv.Capabilities().PooledEndpoints {
		p, err := s.dbProv.ConnString(ctx, branch, provider.ConnPooled)
		if err != nil {
			// Declared and not delivered. Falling back would hand every
			// service a direct connection and hide the fact, so it is an
			// error: the provider said it had a pool.
			return "", zero, secrets.Value{}, secrets.Value{}, err
		}
		pooled = p
		o.opts.Redactor.Register(pooled.Reveal())
	}
	return version, branch, pooled, direct, nil
}

// buildServices builds an image for every service in the manifest.
func (o *Orchestrator) buildServices(
	ctx context.Context, s *session,
) ([]provider.ServiceSpec, int, int, error) {
	ig, err := readIgnore(o.opts.Root)
	if err != nil {
		return nil, 0, 0, err
	}

	var specs []provider.ServiceSpec
	built, cached := 0, 0
	for _, svc := range o.opts.Manifest.Services {
		image, wasCached, err := o.buildOne(ctx, s, svc, ig)
		if err != nil {
			return nil, built, cached, err
		}
		if wasCached {
			cached++
		} else {
			built++
		}
		specs = append(specs, provider.ServiceSpec{
			Name:       svc.Name,
			Image:      image,
			Kind:       string(orDefault(string(svc.Kind), "worker")),
			Command:    svc.Command,
			Port:       svc.Port,
			HealthPath: svc.HealthPath,
			Migrate:    svc.Migrate,
			DependsOn:  svc.DependsOn,
			Env:        serviceEnv(svc),
		})
	}
	return specs, built, cached, nil
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func serviceEnv(svc schema.Service) map[string]secrets.Value {
	out := map[string]secrets.Value{}
	for _, e := range svc.Env {
		out[e.Name] = secrets.New(e.Value)
	}
	return out
}

func readIgnore(root string) (*build.Ignore, error) {
	f, err := os.Open(filepath.Join(root, ".dockerignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return build.ParseIgnore(nil)
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	ig, err := build.ParseIgnore(f)
	if err != nil {
		return nil, aferrors.Wrap(err, aferrors.AFBLD002,
			"service", "every service", "line", "see below", "detail", err.Error())
	}
	return ig, nil
}

// buildOne resolves how a service is built and builds it.
func (o *Orchestrator) buildOne(
	ctx context.Context, s *session, svc schema.Service, ig *build.Ignore,
) (string, bool, error) {
	root := o.opts.Root
	dir := strings.Trim(filepath.ToSlash(svc.Path), "/")
	o.event(s, events.BuildStarted, svc.Name+": build starting", events.F("service", svc.Name))

	// build.context, which until now was validated, printed by af explain, and
	// read by nothing: every service got the whole repository whatever the
	// manifest said. That is the worst of the three possible behaviours,
	// because a user who set it believed something was happening.
	//
	// The directory is relative to the repository root, as the schema says.
	// Everything else in the manifest stays relative to the root as well, so
	// the paths a person writes do not change meaning depending on a field
	// somewhere else in the file; they are translated into the context below.
	contextRoot := root
	if svc.Build != nil && svc.Build.Context != "" {
		contextRoot = filepath.Join(root, filepath.FromSlash(
			strings.TrimPrefix(filepath.ToSlash(svc.Build.Context), "./")))
	}

	if svc.Build != nil && svc.Build.Strategy == schema.BuildImage {
		if svc.Build.Image == "" {
			err := aferrors.Coded(aferrors.AFBLD010, "service", svc.Name)
			o.eventErr(s, events.BuildFailed, err.Error(), events.F("service", svc.Name))
			return "", false, err
		}
		// Nothing to build. A prebuilt image is a promise the user made and
		// the runtime keeps.
		o.event(s, events.BuildFinished, svc.Name+": image was supplied, nothing to build",
			events.F("service", svc.Name), events.F("cached", true))
		return svc.Build.Image, true, nil
	}

	bctx, err := build.NewContext(build.ContextOptions{Root: contextRoot, Ignore: ig, Service: svc.Name})
	if err != nil {
		return "", false, err
	}
	// Paths in the manifest are relative to the repository; paths in a build
	// are relative to its context. Where those differ, translate once here
	// rather than at each of the three places below that needs it.
	inContext := func(repoRelative string) (string, bool) {
		if contextRoot == root {
			return repoRelative, true
		}
		abs := filepath.Join(root, filepath.FromSlash(repoRelative))
		rel, relErr := filepath.Rel(contextRoot, abs)
		if relErr != nil {
			return "", false
		}
		rel = filepath.ToSlash(rel)
		// A path outside the context is not a path the daemon can be asked
		// for. Docker refuses the same thing, and the reason is the same: a
		// build that reads outside its context is not reproducible anywhere
		// else.
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return "", false
		}
		return rel, true
	}

	// A context nobody meant to send. Said out loud, because the whole cost of
	// it is invisible: it is walked, hashed, held in memory and streamed to
	// the daemon on every build, and the only symptom is that a build which
	// takes forty seconds by hand takes half an hour here. The refusal at two
	// gigabytes does not catch this. Six hundred megabytes builds, slowly,
	// every time, and says nothing.
	//
	// A warning rather than a failure: a repository with a legitimate corpus
	// in it is somebody's real situation, and refusing their build over a
	// number would be worse than the number. It names the directory, because
	// the fix is one line in .dockerignore and that line needs a name.
	if bctx.Oversized() {
		o.progress(fmt.Sprintf("%s: the build context is %s", svc.Name, bctx.Explain()))
		o.progress(fmt.Sprintf(
			"%s: that is sent to the builder on every build. Exclude what the image does not read in .dockerignore, which currently excludes %d paths",
			svc.Name, bctx.Excluded))
		o.event(s, events.Progress, svc.Name+": the build context is "+bctx.Explain(),
			events.F("service", svc.Name),
			events.F("contextBytes", bctx.Bytes),
			events.F("contextFiles", len(bctx.Files)),
			events.F("contextLargest", bctx.Largest))
	}

	explained := false
	req := build.Request{Service: svc.Name, Context: bctx, EnvID: o.envID}
	// Every build line reaches the event stream, and only reaches the terminal
	// when verbose was asked for. A dashboard has a pane for seventeen lines
	// per layer; a terminal running two other services does not, which is why
	// the quiet default stayed quiet and the HUD still gets the whole log.
	req.Progress = func(line string) {
		o.event(s, events.BuildLog, line, events.F("service", svc.Name))
		if o.opts.Verbose {
			o.progress(line)
		}
	}
	if svc.Build != nil {
		req.Target = svc.Build.Target
		req.Args = svc.Build.Args
	}

	switch {
	case svc.Build != nil && svc.Build.Dockerfile != "":
		named := strings.TrimPrefix(filepath.ToSlash(svc.Build.Dockerfile), "./")
		translated, inside := inContext(named)
		if !inside {
			err := aferrors.Coded(aferrors.AFBLD012,
				"service", svc.Name, "dockerfile", named, "context", svc.Build.Context)
			o.eventErr(s, events.BuildFailed, err.Error(), events.F("service", svc.Name))
			return "", false, err
		}
		req.DockerfilePath = translated
		// Named, on disk, and not in the context. The manifest validator
		// checks the file exists and it does; the build sends a filtered copy
		// of the tree to the daemon and names the Dockerfile inside it, so a
		// path the ignore file excludes is simply absent. The daemon then says
		// "cannot locate specified Dockerfile" about a file anybody can see,
		// and the next hour is spent looking at the wrong thing.
		//
		// `docker build -f` does not have this problem, which is what makes it
		// so confusing: the same Dockerfile builds by hand and not here,
		// because -f reads from the host and never consults .dockerignore.
		if !bctx.Has(req.DockerfilePath) {
			err := aferrors.Coded(aferrors.AFBLD011,
				"service", svc.Name, "dockerfile", req.DockerfilePath)
			o.eventErr(s, events.BuildFailed, err.Error(), events.F("service", svc.Name))
			return "", false, err
		}
	case bctx.Has(serviceDir(inContext, dir, "Dockerfile")):
		req.DockerfilePath = serviceDir(inContext, dir, "Dockerfile")
	case dir == "" && bctx.Has("Dockerfile"):
		req.DockerfilePath = "Dockerfile"
	default:
		detectIn, inside := inContext(dir)
		if !inside {
			detectIn = dir
		}
		bp, ok := build.DetectBuildpack(bctx, detectIn, svc.Command, svc.Port)
		if !ok {
			err := aferrors.Coded(aferrors.AFBLD010, "service", svc.Name)
			o.eventErr(s, events.BuildFailed, err.Error(), events.F("service", svc.Name))
			return "", false, err
		}
		o.progress(fmt.Sprintf("%s: no Dockerfile, so %s", svc.Name, lowerFirst(bp.Why)))
		req.Dockerfile = bp.Dockerfile
		explained = true
	}

	if !explained {
		o.progress(svc.Name + ": building")
	}
	// Which builder, said out loud, once per service.
	//
	// The legacy builder costs roughly twenty five seconds a step on a modern
	// daemon, so a thirty four step Dockerfile spends a quarter of an hour
	// before it does any work. Falling back to it silently would be a
	// regression that looks exactly like a slow machine, and Docker is
	// removing it, so the day it disappears the message is already there.
	if s.builder != nil && !s.builder.UsesBuildKit() {
		o.progress(svc.Name + ": the daemon has no BuildKit, so this build uses the legacy builder and will be slow")
		o.event(s, events.Warning, svc.Name+": building with the legacy builder",
			events.F("service", svc.Name), events.F("builder", "legacy"))
	}
	res, err := s.builder.Build(ctx, req)
	if err != nil {
		// The build output is the only thing that explains a failed build, so
		// it is printed here whether or not verbose was asked for. Withholding
		// it would leave somebody with a code and no reason.
		if !o.opts.Verbose {
			for _, line := range res.Log {
				o.progress(line)
			}
		}
		o.eventErr(s, events.BuildFailed, err.Error(), events.F("service", svc.Name))
		return "", false, err
	}
	if res.Cached {
		o.progress(svc.Name + ": image is unchanged, nothing to build")
	} else {
		o.progress(fmt.Sprintf("%s: built in %s", svc.Name, res.Duration.Round(time.Second)))
	}
	o.event(s, events.BuildFinished, svc.Name+": built in "+res.Duration.Round(time.Second).String(),
		events.F("service", svc.Name), events.F("cached", res.Cached),
		events.F("seconds", res.Duration.Seconds()))

	return res.ImageRef, res.Cached, nil
}

// lowerFirst makes a sentence read as a clause after "so".
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	// Only when the first word is not a name. "package.json" stays as it is,
	// and so does "Gemfile", because lowercasing a filename makes it a
	// different filename.
	first := strings.Fields(s)[0]
	if strings.ContainsAny(first, ".") || strings.ToUpper(first[:1]) != first[:1] {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// Teardown is what Down removed and what it could not.
type Teardown struct {
	EnvID   string
	Removed int
	Pending []provider.PendingResource
}

// Down removes the environment and everything it created.
//
// It never stops at the first failure. A provider that is unreachable must not
// strand the other resources, so each is attempted and what could not be
// removed is returned, which is what makes the difference between exit 0 and
// exit 10 meaningful.
func (o *Orchestrator) Down(ctx context.Context) (*Teardown, error) {
	s, err := o.open(ctx, "af down")
	if err != nil {
		return nil, err
	}
	defer s.close()

	td := &Teardown{EnvID: o.envID}
	o.event(s, events.EnvDestroying, "removing "+o.envID)

	rt, err := s.runtime.Down(ctx, o.envID)
	td.Removed += rt.Removed
	td.Pending = append(td.Pending, rt.Pending...)
	if err != nil {
		td.Pending = append(td.Pending, provider.PendingResource{
			Kind: "runtime", ID: o.envID, Reason: err.Error(),
		})
	}
	o.progress(fmt.Sprintf("removed %d runtime resources", rt.Removed))

	// The database goes last, because a service still running against a
	// database that has been taken away produces a page of connection errors
	// in the logs that has nothing to do with why the environment went away.
	branch := provider.Branch{EnvID: o.envID}
	if err := s.dbProv.Destroy(ctx, branch); err != nil {
		td.Pending = append(td.Pending, provider.PendingResource{
			Kind: "database", ID: o.envID, Reason: err.Error(),
		})
	} else {
		td.Removed++
		o.progress("removed the database branch")
	}

	// Emitted before observe and before the return, so it is still inside the
	// session and the bus is still open. A teardown that left resources behind
	// still says destroyed, with the count, because the environment is gone as
	// far as anybody looking at it is concerned and the pending list is what
	// says otherwise.
	o.event(s, events.EnvDestroyed, "removed "+o.envID,
		events.F("removed", td.Removed), events.F("pending", len(td.Pending)))

	o.observe(ctx, extension.LifecycleEvent{
		Repository: o.opts.Manifest.Name,
		EnvID:      o.envID,
		Kind:       "environment.torn_down",
	})
	return td, nil
}

// observe reports a lifecycle event to whatever is registered.
//
// Failures are reported through progress and never returned. A metering
// pipeline that is down must not prevent a teardown, or a billing outage
// becomes a resource leak, which is a strictly worse problem than a missing
// meter reading.
func (o *Orchestrator) observe(ctx context.Context, event extension.LifecycleEvent) {
	registry := o.opts.Extensions
	if registry == nil {
		registry = extension.Default
	}
	if registry.Empty() {
		return
	}
	for _, problem := range registry.Observe(ctx, event) {
		o.progress(fmt.Sprintf("lifecycle hook: %v", problem))
	}
}

// Status reports what is currently running.
func (o *Orchestrator) Status(ctx context.Context) (*Result, error) {
	rt, err := local.New(local.Options{Clock: o.opts.Clock, Redactor: o.opts.Redactor})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close() }()

	// Deliberately no lock. Asking what is running must not block behind an af
	// up that is halfway through, because that is exactly the moment somebody
	// wants to know.
	env, err := rt.Status(ctx, o.envID)
	if err != nil {
		return nil, err
	}
	// Status sorts by name, which is a reasonable default for a runtime that
	// has no manifest and the wrong one for a caller that does. `api` sorts
	// before `app`, so the environment's URL became the API's, and `af test`
	// drove the API with a browser: six workflows blocked on a login form that
	// was never going to be there. The manifest is where the author says which
	// service the product is.
	o.orderByManifest(env.Services)
	return &Result{
		EnvID: o.envID, URL: env.URL(), Services: env.Services,
		Proxied: env.ProxyReady,
	}, nil
}

// orderByManifest puts running services into the order they were declared in.
//
// Every reader of this list wants that order: the status table, the pull
// request comment, and the address a person is told to open. Neither the
// dependency sort that starts them nor an alphabetical one says anything about
// which service somebody came to look at.
func (o *Orchestrator) orderByManifest(running []provider.RunningService) {
	position := make(map[string]int, len(o.opts.Manifest.Services))
	for i, s := range o.opts.Manifest.Services {
		position[s.Name] = i
	}
	sort.SliceStable(running, func(i, j int) bool {
		pi, oki := position[running[i].Name]
		pj, okj := position[running[j].Name]
		switch {
		case oki && okj:
			return pi < pj
		case oki:
			return true
		case okj:
			return false
		}
		// A service the manifest no longer declares, which happens when a
		// branch is checked out over a running environment. Left where it was
		// found rather than given an invented position.
		return false
	})
}

// Decisions returns what the environment's egress proxy has decided.
func (o *Orchestrator) Decisions(ctx context.Context, limit int) ([]local.Decision, error) {
	rt, err := local.New(local.Options{Clock: o.opts.Clock, Redactor: o.opts.Redactor})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close() }()
	// No lock, for the same reason Status takes none: the moment somebody most
	// wants to know what the environment reached is while it is doing it.
	return rt.Decisions(ctx, o.envID, limit)
}

// Messages returns what the environment captured instead of sending.
func (o *Orchestrator) Messages(ctx context.Context, limit int) ([]local.Message, error) {
	rt, err := local.New(local.Options{Clock: o.opts.Clock, Redactor: o.opts.Redactor})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close() }()
	return rt.Messages(ctx, o.envID, limit)
}

// WaitForMessage blocks until a matching message arrives.
func (o *Orchestrator) WaitForMessage(
	ctx context.Context, to, subject string, timeout time.Duration,
) (local.Message, error) {
	rt, err := local.New(local.Options{Clock: o.opts.Clock, Redactor: o.opts.Redactor})
	if err != nil {
		return local.Message{}, err
	}
	defer func() { _ = rt.Close() }()

	return rt.WaitForMessage(ctx, o.envID, func(m local.Message) bool {
		if to != "" {
			matched := false
			for _, r := range m.To {
				if strings.EqualFold(r, to) {
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		if subject != "" && !strings.Contains(strings.ToLower(m.Subject), strings.ToLower(subject)) {
			return false
		}
		return true
	}, timeout)
}

// DeliverWebhook sends a signed event into the environment.
func (o *Orchestrator) DeliverWebhook(
	ctx context.Context, service, path string, body []byte, headers map[string]string,
) (local.Delivery, error) {
	rt, err := local.New(local.Options{Clock: o.opts.Clock, Redactor: o.opts.Redactor})
	if err != nil {
		return local.Delivery{}, err
	}
	defer func() { _ = rt.Close() }()
	return rt.Deliver(ctx, o.envID, service, path, body, headers)
}

// Logs returns recent output from the environment's services.
func (o *Orchestrator) Logs(ctx context.Context, service string, tail int) ([]local.LogLine, error) {
	rt, err := local.New(local.Options{Clock: o.opts.Clock, Redactor: o.opts.Redactor})
	if err != nil {
		return nil, err
	}
	defer func() { _ = rt.Close() }()
	return rt.Logs(ctx, o.envID, service, tail)
}

// serviceDir resolves a file inside a service's directory, as the build
// context sees it.
func serviceDir(inContext func(string) (string, bool), dir, name string) string {
	rel, ok := inContext(joinPath(dir, name))
	if !ok {
		return joinPath(dir, name)
	}
	return rel
}

// seedRulesHash identifies a golden built from a seed command.
//
// A golden is chosen by the rules that produced it, and a seeded one is
// produced by a command rather than by a rule set. Hashing the command gives
// the same property: change the seed, and the next `af up` refuses the golden
// the old one made instead of quietly branching stale data.
func seedRulesHash(seed string) string {
	if seed == "" {
		return "empty"
	}
	sum := sha256.Sum256([]byte("seed\x00" + seed))
	return "seed-" + hex.EncodeToString(sum[:5])
}

// runSeed executes the manifest's database.seed command against a candidate.
//
// The field was validated, refused alongside source_url_env, printed by
// `af explain`, and executed by nothing, so a project with no production
// database got an empty environment and no indication that the command it
// configured had not run. Silently ignoring configuration is worse than not
// offering it.
//
// It runs against the candidate rather than against each branch, which is what
// makes it the counterpart of source_url_env: the seed pays once, at refresh,
// and every branch after that is a copy. The command sees DATABASE_URL, the
// same name a migration sees, so one script can serve both.
func (o *Orchestrator) runSeed(
	ctx context.Context, s *session, seed string, candidate secrets.Value,
) error {
	if strings.TrimSpace(seed) == "" {
		return nil
	}
	o.progress("seeding the golden with the manifest's command")
	o.event(s, events.MaskProgress, "seeding the golden", events.F("phase", "seed"))

	cmd := exec.CommandContext(ctx, "sh", "-c", seed)
	cmd.Dir = o.opts.Root
	cmd.Env = append(os.Environ(), "DATABASE_URL="+candidate.Reveal())
	out, err := cmd.CombinedOutput()
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Through the redactor, because a seed script prints connection
		// strings and the golden's own URL is in its environment.
		o.progress("  " + o.opts.Redactor.String(line))
	}
	if err != nil {
		return aferrors.Coded(aferrors.AFDB009,
			"command", seed, "detail", o.opts.Redactor.String(strings.TrimSpace(lastLine(string(out)))))
	}
	return nil
}

// lastLine returns the final non-empty line, which is where a script that
// failed says why.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return "the command produced no output"
}
