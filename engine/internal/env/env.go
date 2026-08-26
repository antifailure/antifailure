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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/build"
	"github.com/antifailure/antifailure/engine/internal/clock"
	dockerdb "github.com/antifailure/antifailure/engine/internal/db/docker"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/journal"
	"github.com/antifailure/antifailure/engine/internal/lock"
	"github.com/antifailure/antifailure/engine/internal/redact"
	"github.com/antifailure/antifailure/engine/internal/runtime/local"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/internal/state"
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
}

// Orchestrator runs the lifecycle for one environment.
type Orchestrator struct {
	opts     Options
	envID    string
	progress func(string)
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
	// Sealed reports whether the environment has no route to the internet.
	Sealed bool
}

// session holds everything Up opens and must close.
type session struct {
	lock    *lock.Lock
	db      *state.DB
	bus     *events.Bus
	journal *journal.Journal
	dbProv  *dockerdb.Provider
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
	s.bus = events.NewBus(o.opts.Clock)
	s.journal = journal.New(s.db, o.opts.Clock, s.bus)

	if s.dbProv, err = dockerdb.New(dockerdb.Options{
		Version: databaseVersion(o.opts.Manifest), Clock: o.opts.Clock,
	}); err != nil {
		s.close()
		return nil, err
	}
	if s.runtime, err = local.New(local.Options{
		Clock: o.opts.Clock, Redactor: o.opts.Redactor,
	}); err != nil {
		s.close()
		return nil, err
	}
	if s.builder, err = build.NewDockerBuilder(build.DockerOptions{
		Clock: o.opts.Clock, Redactor: o.opts.Redactor, NoCache: o.opts.Rebuild,
	}); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func databaseVersion(m *schema.Manifest) int {
	if m.Database != nil && m.Database.Version > 0 {
		return m.Database.Version
	}
	return 17
}

// Up brings the environment up.
func (o *Orchestrator) Up(ctx context.Context) (*Result, error) {
	started := o.opts.Clock.Now()
	s, err := o.open(ctx, "af up")
	if err != nil {
		return nil, err
	}
	defer s.close()

	res := &Result{EnvID: o.envID, Sealed: true}

	golden, branch, dbURL, err := o.database(ctx, s)
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
	insideURL, err := s.runtime.AttachDatabase(ctx, s.dbProv, branch.ProviderRef, networkID, dbURL)
	if err != nil {
		return res, err
	}
	o.opts.Redactor.Register(insideURL.Reveal())

	env, err := s.runtime.Up(ctx, provider.EnvSpec{
		EnvID: o.envID, Branch: o.opts.Branch, Services: specs,
		AllowEgress: false,
		DatabaseURL: insideURL,
		Journal:     recordIntent,
		Progress:    o.progress,
	})
	res.Services = env.Services
	if err != nil {
		return res, err
	}

	res.URL = env.URL()
	res.Duration = o.opts.Clock.Since(started)
	return res, nil
}

// database makes sure a verified golden exists and branches it.
func (o *Orchestrator) database(ctx context.Context, s *session) (string, provider.Branch, secrets.Value, error) {
	var zero provider.Branch

	goldens, err := s.dbProv.ListGoldens(ctx)
	if err != nil {
		return "", zero, secrets.Value{}, err
	}
	var version string
	for _, g := range goldens {
		if g.Verified {
			version = g.ID
			break
		}
	}
	if version == "" {
		o.progress("no golden yet, creating one")
		gv, refreshErr := s.dbProv.RefreshGolden(ctx, provider.GoldenSpec{
			Version:   databaseVersion(o.opts.Manifest),
			RulesHash: "empty",
			// No source database is configured, so the golden is an empty one
			// and the schema arrives with the migrations. Masking and
			// verification still run, and both trivially pass on no rows,
			// which is the honest answer rather than a skipped step.
			Mask:   func(context.Context, secrets.Value) error { return nil },
			Verify: func(context.Context, secrets.Value) (string, error) { return `{"rows":0}`, nil },
		})
		if refreshErr != nil {
			return "", zero, secrets.Value{}, refreshErr
		}
		version = gv.ID
	}
	o.progress("branching the database from " + version)

	if _, err := s.journal.Intent(ctx, o.envID, "docker", journal.Kind("database"), o.envID, nil); err != nil {
		return "", zero, secrets.Value{}, err
	}
	branch, err := s.dbProv.Branch(ctx, version, o.envID)
	if err != nil {
		return "", zero, secrets.Value{}, err
	}
	url, err := s.dbProv.ConnString(ctx, branch, provider.ConnDirect)
	if err != nil {
		return "", zero, secrets.Value{}, err
	}
	// Every value that reaches a log goes through the redactor, and the
	// connection string is registered so that it is redacted wherever it
	// appears rather than wherever somebody remembered to.
	o.opts.Redactor.Register(url.Reveal())
	return version, branch, url, nil
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

	if svc.Build != nil && svc.Build.Strategy == schema.BuildImage {
		if svc.Build.Image == "" {
			return "", false, aferrors.Coded(aferrors.AFBLD010, "service", svc.Name)
		}
		// Nothing to build. A prebuilt image is a promise the user made and
		// the runtime keeps.
		return svc.Build.Image, true, nil
	}

	bctx, err := build.NewContext(build.ContextOptions{Root: root, Ignore: ig, Service: svc.Name})
	if err != nil {
		return "", false, err
	}

	explained := false
	req := build.Request{Service: svc.Name, Context: bctx, EnvID: o.envID}
	if o.opts.Verbose {
		req.Progress = o.progress
	}
	if svc.Build != nil {
		req.Target = svc.Build.Target
		req.Args = svc.Build.Args
	}

	switch {
	case svc.Build != nil && svc.Build.Dockerfile != "":
		req.DockerfilePath = strings.TrimPrefix(filepath.ToSlash(svc.Build.Dockerfile), "./")
	case bctx.Has(joinPath(dir, "Dockerfile")):
		req.DockerfilePath = joinPath(dir, "Dockerfile")
	case dir == "" && bctx.Has("Dockerfile"):
		req.DockerfilePath = "Dockerfile"
	default:
		bp, ok := build.DetectBuildpack(bctx, dir, svc.Command, svc.Port)
		if !ok {
			return "", false, aferrors.Coded(aferrors.AFBLD010, "service", svc.Name)
		}
		o.progress(fmt.Sprintf("%s: no Dockerfile, so %s", svc.Name, lowerFirst(bp.Why)))
		req.Dockerfile = bp.Dockerfile
		explained = true
	}

	if !explained {
		o.progress(svc.Name + ": building")
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
		return "", false, err
	}
	if res.Cached {
		o.progress(svc.Name + ": image is unchanged, nothing to build")
	} else {
		o.progress(fmt.Sprintf("%s: built in %s", svc.Name, res.Duration.Round(time.Second)))
	}

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
	return td, nil
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
	return &Result{
		EnvID: o.envID, URL: env.URL(), Services: env.Services,
		Sealed: !env.EgressAllowed,
	}, nil
}
