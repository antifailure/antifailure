// Package detect derives a manifest draft from a repository by reading files.
//
// The hard constraint, and the reason this package exists rather than a script
// that runs the project's own tooling: the customer's repository is untrusted
// input. Detection never executes anything from it. No npm install, no go run,
// no evaluating a config file that happens to be JavaScript. Everything here
// is parsing, and every parser is bounded.
//
// The second constraint is determinism. Two runs over the same tree produce
// byte identical output, and so does a run over the same tree with the files
// enumerated in a different order, because af init writes a manifest that gets
// committed and a manifest that shuffles on every run is unusable in review.
package detect

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// Limits on what detection will read. A repository is arbitrary input, so
// every walk is bounded in file size, total bytes, file count, and wall time.
const (
	// MaxFileSize is the largest single file that is read. Anything above it
	// is a build artifact, a media asset, or a checked in database, none of
	// which describe how to run the application.
	MaxFileSize = 5 << 20 // 5 MiB
	// MaxTotalBytes bounds the whole walk.
	MaxTotalBytes = 200 << 20 // 200 MiB
	// MaxFiles bounds how many entries are visited.
	MaxFiles = 200000
	// DefaultBudget is how long detection runs before returning what it has.
	// Partial results with an explicit event beat an af init that appears to
	// hang on a large monorepo.
	DefaultBudget = 30 * time.Second
)

// Confidence is how sure an analyzer is about a finding.
type Confidence int

const (
	// Low means the finding is a guess worth asking the user about.
	Low Confidence = 1
	// Medium means the evidence is indirect but consistent.
	Medium Confidence = 2
	// High means the evidence is explicit, for example a declared port in a
	// compose file or a framework's own configuration.
	High Confidence = 3
)

func (c Confidence) String() string {
	switch c {
	case High:
		return "high"
	case Medium:
		return "medium"
	default:
		return "low"
	}
}

// Kind classifies a finding.
type Kind string

const (
	KindService    Kind = "service"
	KindPort       Kind = "port"
	KindCommand    Kind = "command"
	KindBuild      Kind = "build"
	KindDatabase   Kind = "database"
	KindMigration  Kind = "migration"
	KindEnvVar     Kind = "env"
	KindThirdParty Kind = "third_party"
	KindWorker     Kind = "worker"
	KindCron       Kind = "cron"
	KindFramework  Kind = "framework"
	KindPackageMgr Kind = "package_manager"
	KindWorkspace  Kind = "workspace"
	KindNote       Kind = "note"
)

// Finding is one observation about the repository.
type Finding struct {
	Kind Kind
	// Subject is what the finding is about: a service name, a variable name, a
	// host. It is the key findings are merged on.
	Subject string
	// Value carries the finding's payload, for example a port number as text.
	Value string
	// Confidence is how sure the analyzer is.
	Confidence Confidence
	// Evidence is the file that produced the finding, relative to the root.
	// Every finding names one, so a user can check the reasoning.
	Evidence string
	// Detail is a short explanation shown when the user is asked to confirm.
	Detail string
	// Extra carries analyzer specific fields the merger understands.
	Extra map[string]string
	// Analyzer is the name of the analyzer that produced it.
	Analyzer string
}

// Analyzer inspects a repository and reports findings.
//
// An analyzer reads through fs.FS and never touches the operating system
// directly, which is what makes the whole package testable against an in
// memory tree and unable to escape the repository root.
type Analyzer interface {
	// Name identifies the analyzer in findings and events.
	Name() string
	// Analyze reads the tree and reports what it found. An analyzer that finds
	// nothing returns no findings and no error; an error means the analyzer
	// itself failed, which never fails the whole detection.
	Analyze(ctx context.Context, r *Repo) ([]Finding, error)
}

// Repo is a bounded, read only view of a repository.
//
// It caches file contents so that ten analyzers reading package.json read the
// disk once, and it enforces the size and count limits in one place rather
// than in each analyzer.
type Repo struct {
	fsys fs.FS
	root string

	files     []string
	cache     map[string][]byte
	skipped   []string
	totalRead int64
	truncated bool
}

// NewRepo indexes a repository, applying the walk limits.
//
// The index is sorted, which is what makes detection independent of the order
// the filesystem happens to return entries in.
func NewRepo(fsys fs.FS, root string) (*Repo, error) {
	r := &Repo{fsys: fsys, root: root, cache: map[string][]byte{}}
	count := 0
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory is skipped rather than fatal. A
			// repository with one permission denied subdirectory should still
			// produce a draft for the rest.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if count > MaxFiles {
			r.truncated = true
			return fs.SkipAll
		}
		count++
		name := path.Base(p)
		if d.IsDir() {
			if skipDir(name) && p != "." {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			// Symlinks are not followed. A link pointing outside the
			// repository is exactly how a detector gets tricked into reading
			// something it should not.
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		if info.Size() > MaxFileSize {
			r.skipped = append(r.skipped, p)
			return nil
		}
		r.files = append(r.files, p)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("detect: walk %s: %w", root, err)
	}
	sort.Strings(r.files)
	return r, nil
}

// skipDir reports whether a directory is never worth walking. These hold
// dependencies, build output, and version control metadata, which describe
// nothing about how to run the application and are usually the bulk of a
// repository's file count.
func skipDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", ".venv", "venv",
		"__pycache__", ".next", ".nuxt", ".svelte-kit", "dist", "build",
		"target", ".turbo", ".cache", "coverage", ".pytest_cache", ".mypy_cache",
		".terraform", ".gradle", ".idea", ".vscode", ".antifailure", "bin", "obj",
		".tox", ".eggs", "site-packages", ".yarn", ".pnpm-store", ".astro":
		return true
	}
	return false
}

// Files returns every indexed path, sorted.
func (r *Repo) Files() []string { return r.files }

// Skipped returns paths that were too large to read.
func (r *Repo) Skipped() []string { return r.skipped }

// Truncated reports whether the walk hit the file count limit.
func (r *Repo) Truncated() bool { return r.truncated }

// Root returns the repository root as given.
func (r *Repo) Root() string { return r.root }

// Read returns a file's contents, cached, or false when it is absent, too
// large, or the total read budget is exhausted.
func (r *Repo) Read(p string) ([]byte, bool) {
	p = path.Clean(p)
	if b, ok := r.cache[p]; ok {
		return b, b != nil
	}
	if r.totalRead > MaxTotalBytes {
		return nil, false
	}
	b, err := fs.ReadFile(r.fsys, p)
	if err != nil {
		r.cache[p] = nil
		return nil, false
	}
	if len(b) > MaxFileSize {
		r.cache[p] = nil
		r.skipped = append(r.skipped, p)
		return nil, false
	}
	r.totalRead += int64(len(b))
	r.cache[p] = b
	return b, true
}

// ReadString is Read as text.
func (r *Repo) ReadString(p string) (string, bool) {
	b, ok := r.Read(p)
	return string(b), ok
}

// Exists reports whether a path is in the index.
func (r *Repo) Exists(p string) bool {
	p = path.Clean(p)
	i := sort.SearchStrings(r.files, p)
	return i < len(r.files) && r.files[i] == p
}

// Glob returns indexed paths whose base name matches one of the names given,
// sorted. It is the primary way analyzers find their inputs.
func (r *Repo) Glob(names ...string) []string {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []string
	for _, f := range r.files {
		if want[path.Base(f)] {
			out = append(out, f)
		}
	}
	return out
}

// WithExtension returns indexed paths ending in ext, sorted.
func (r *Repo) WithExtension(ext string) []string {
	var out []string
	for _, f := range r.files {
		if strings.HasSuffix(f, ext) {
			out = append(out, f)
		}
	}
	return out
}

// InDir returns indexed paths directly inside dir.
func (r *Repo) InDir(dir string) []string {
	dir = path.Clean(dir)
	var out []string
	for _, f := range r.files {
		if path.Dir(f) == dir {
			out = append(out, f)
		}
	}
	return out
}

// Result is everything detection found.
type Result struct {
	// Findings are sorted so that two runs produce identical output.
	Findings []Finding
	// Draft is the manifest the findings merge into.
	Draft *schema.Manifest
	// Questions are the things af init has to ask, because a finding was below
	// the confidence threshold or two analyzers disagreed.
	Questions []Question
	// Partial reports that the time or size budget ran out before the walk
	// finished, so the draft may be incomplete.
	Partial bool
	// Skipped lists files that were too large to read.
	Skipped []string
	// Duration is how long detection took.
	Duration time.Duration
}

// Question is something detection could not decide.
type Question struct {
	// ID is stable, so that a non interactive run can answer it by flag.
	ID string
	// Prompt is the question, in the second person.
	Prompt string
	// Options are the choices, best first. An empty list means free text.
	Options []string
	// Default is the answer used when the user accepts, or when a non
	// interactive run does not override it.
	Default string
	// Why explains what detection saw, so the user can judge the guess.
	Why string
}

// Options configures a run.
type Options struct {
	// Budget bounds wall time. Zero uses DefaultBudget.
	Budget time.Duration
	// Clock is the time source. Zero uses the real clock.
	Clock clock.Clock
	// Analyzers overrides the default set. Tests use it to run one analyzer.
	Analyzers []Analyzer
}

// DefaultAnalyzers returns the analyzers that run when none are named. Order
// does not affect the result, since findings are sorted before merging, but it
// is kept stable so that event streams read the same way every time.
func DefaultAnalyzers() []Analyzer {
	return []Analyzer{
		&WorkspaceAnalyzer{},
		&NodeAnalyzer{},
		&PythonAnalyzer{},
		&GoAnalyzer{},
		&RubyAnalyzer{},
		&DockerAnalyzer{},
		&ComposeAnalyzer{},
		&ProcfileAnalyzer{},
		&MigrationAnalyzer{},
		&EnvAnalyzer{},
		&ThirdPartyAnalyzer{},
		&AuthAnalyzer{},
		&ScheduleAnalyzer{},
	}
}

// Run analyzes a repository and returns a manifest draft.
//
// Detection never fails the caller: an analyzer that errors contributes a note
// and the rest continue, because a partial draft the user can edit beats no
// draft at all.
func Run(ctx context.Context, fsys fs.FS, root string, opts Options) (*Result, error) {
	if opts.Budget <= 0 {
		opts.Budget = DefaultBudget
	}
	if opts.Clock == nil {
		opts.Clock = clock.New()
	}
	analyzers := opts.Analyzers
	if analyzers == nil {
		analyzers = DefaultAnalyzers()
	}

	start := opts.Clock.Now()
	repo, err := NewRepo(fsys, root)
	if err != nil {
		return nil, err
	}

	res := &Result{Skipped: repo.Skipped(), Partial: repo.Truncated()}

	deadline := start.Add(opts.Budget)
	for _, a := range analyzers {
		if ctx.Err() != nil {
			res.Partial = true
			break
		}
		if !opts.Clock.Now().Before(deadline) {
			res.Partial = true
			res.Findings = append(res.Findings, Finding{
				Kind:       KindNote,
				Subject:    "budget",
				Value:      opts.Budget.String(),
				Confidence: High,
				Analyzer:   "detect",
				Detail: fmt.Sprintf(
					"Detection stopped after %s with partial results. Analyzers after %s did not run.",
					opts.Budget, a.Name()),
			})
			break
		}
		found, aErr := a.Analyze(ctx, repo)
		if aErr != nil {
			// One analyzer failing must not lose the other eleven.
			res.Findings = append(res.Findings, Finding{
				Kind:       KindNote,
				Subject:    a.Name(),
				Confidence: High,
				Analyzer:   a.Name(),
				Detail:     fmt.Sprintf("The %s analyzer failed: %v", a.Name(), aErr),
			})
			continue
		}
		for i := range found {
			if found[i].Analyzer == "" {
				found[i].Analyzer = a.Name()
			}
		}
		res.Findings = append(res.Findings, found...)
	}

	sortFindings(res.Findings)
	res.Draft, res.Questions = Merge(res.Findings, root)
	res.Duration = opts.Clock.Since(start)
	return res, nil
}

// sortFindings orders findings so that output is identical across runs. The
// key is chosen so that related findings sit together, which makes the event
// stream and the draft readable as well as stable.
func sortFindings(f []Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Kind != f[j].Kind {
			return f[i].Kind < f[j].Kind
		}
		if f[i].Subject != f[j].Subject {
			return f[i].Subject < f[j].Subject
		}
		if f[i].Confidence != f[j].Confidence {
			return f[i].Confidence > f[j].Confidence // strongest evidence first
		}
		if f[i].Evidence != f[j].Evidence {
			return f[i].Evidence < f[j].Evidence
		}
		return f[i].Value < f[j].Value
	})
}

// OfKind returns the findings of one kind, preserving order.
func OfKind(fs []Finding, k Kind) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Kind == k {
			out = append(out, f)
		}
	}
	return out
}
