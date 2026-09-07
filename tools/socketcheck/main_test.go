package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A gate that cannot say no is worse than no gate, so most of this file is
// about making it say no. Each case writes a small tree with the defect in it
// and requires the reported problem to name that defect, and then removes the
// defect and requires the tree to pass.

const goodExtension = `package extension

import (
	"context"
	"sync"

	"github.com/antifailure/antifailure/engine/pkg/provider"
)

type PolicyHook interface {
	Name() string
	Check(ctx context.Context) error
}

type DatabaseProvider interface {
	Name() string
	Open(ctx context.Context, cfg Config) (provider.Database, error)
}

type Config struct {
	Root string
}

type Registry struct {
	mu       sync.RWMutex
	policy   []PolicyHook
	database []DatabaseProvider
}

func (r *Registry) AddPolicy(h PolicyHook) { r.policy = append(r.policy, h) }

func (r *Registry) CheckPolicy(ctx context.Context) error {
	for _, h := range r.policy {
		if err := h.Check(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) AddDatabaseProvider(p DatabaseProvider) {
	r.database = append(r.database, p)
}

func (r *Registry) DatabaseProviderNamed(name string) (DatabaseProvider, bool) {
	for _, p := range r.database {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

func (r *Registry) Registered() []string {
	var out []string
	for _, h := range r.policy {
		out = append(out, h.Name())
	}
	for _, p := range r.database {
		out = append(out, p.Name())
	}
	return out
}
`

const goodEngine = `package env

import "github.com/antifailure/antifailure/engine/pkg/extension"

func consult(r *extension.Registry) {
	_ = r.CheckPolicy(nil)
	_, _ = r.DatabaseProviderNamed("docker")
}
`

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, extension, engine string) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "engine/pkg/extension/extension.go", extension)
	write(t, root, "engine/internal/env/env.go", engine)
	return root
}

func problems(t *testing.T, root string, unconsulted map[string]string) string {
	t.Helper()
	report, err := check(root, unconsulted)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(report.Problems, "\n")
}

func TestATreeWhereEverySocketIsConsultedPasses(t *testing.T) {
	t.Parallel()
	if got := problems(t, tree(t, goodExtension, goodEngine), nil); got != "" {
		t.Fatalf("a correct tree was reported: %s", got)
	}
}

func TestASocketNothingConsultsIsReported(t *testing.T) {
	t.Parallel()
	// The AuditSink case, which is what this gate was written for: the
	// interface, the registry and the Add are all there, and no line of the
	// engine ever asks.
	engine := strings.Replace(goodEngine, `	_, _ = r.DatabaseProviderNamed("docker")`, "", 1)
	got := problems(t, tree(t, goodExtension, engine), nil)
	if !strings.Contains(got, "DatabaseProvider") ||
		!strings.Contains(got, "nothing in the engine calls") {
		t.Fatalf("an unconsulted socket was not reported: %q", got)
	}
}

func TestASocketListedAsUnconsultedAndActuallyConsultedIsReported(t *testing.T) {
	t.Parallel()
	// The other direction, which is what stops the list becoming exemptions
	// nobody rereads: when the consultation lands, the entry has to go.
	stale := map[string]string{"DatabaseProvider": "a reason that has stopped being true"}

	got := problems(t, tree(t, goodExtension, goodEngine), stale)
	if !strings.Contains(got, "Delete the entry") {
		t.Fatalf("a stale entry was not reported: %q", got)
	}
}

func TestASocketNamingAnInternalTypeIsReported(t *testing.T) {
	t.Parallel()
	// The defect surfacecheck found in provider.Database, in the one package
	// whose whole purpose is being implemented from outside the module. It
	// compiles, and no implementation outside this module can name the type.
	leaky := strings.Replace(goodExtension,
		`	"github.com/antifailure/antifailure/engine/pkg/provider"`,
		"\t\"github.com/antifailure/antifailure/engine/internal/secrets\"\n"+
			"\t\"github.com/antifailure/antifailure/engine/pkg/provider\"", 1)
	leaky = strings.Replace(leaky,
		"	Open(ctx context.Context, cfg Config) (provider.Database, error)",
		"	Open(ctx context.Context, cfg Config) (provider.Database, secrets.Value, error)", 1)

	got := problems(t, tree(t, leaky, goodEngine), nil)
	if !strings.Contains(got, "secrets.Value") || !strings.Contains(got, "engine/internal") {
		t.Fatalf("a socket naming an internal type was not reported: %q", got)
	}
}

func TestATypeCarriedInsideAConfigStructIsReportedToo(t *testing.T) {
	t.Parallel()
	// One level in. The signature names only Config, and Config carries the
	// type an outside implementation cannot name, which is the same defect
	// one step further from the interface.
	leaky := strings.Replace(goodExtension,
		`	"github.com/antifailure/antifailure/engine/pkg/provider"`,
		"\t\"github.com/antifailure/antifailure/engine/internal/secrets\"\n"+
			"\t\"github.com/antifailure/antifailure/engine/pkg/provider\"", 1)
	leaky = strings.Replace(leaky, "type Config struct {\n\tRoot string\n}",
		"type Config struct {\n\tRoot string\n\tKey secrets.Value\n}", 1)

	got := problems(t, tree(t, leaky, goodEngine), nil)
	if !strings.Contains(got, "secrets.Value") {
		t.Fatalf("an internal type inside a config struct was not reported: %q", got)
	}
}

func TestARegistrationNothingCanEvenReadIsReported(t *testing.T) {
	t.Parallel()
	// A registry with an Add and no reader at all. Registered() lists it, so
	// without the inventory exclusion this would look consulted.
	blind := strings.Replace(goodExtension, `func (r *Registry) DatabaseProviderNamed(name string) (DatabaseProvider, bool) {
	for _, p := range r.database {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}
`, "", 1)
	engine := strings.Replace(goodEngine, `	_, _ = r.DatabaseProviderNamed("docker")`, "", 1)

	got := problems(t, tree(t, blind, engine), nil)
	if !strings.Contains(got, "registered and never read") {
		t.Fatalf("a socket with no reader was not reported: %q", got)
	}
}

// TestThisRepository is the gate itself.
func TestThisRepository(t *testing.T) {
	report, err := Check("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + report.String())
	if len(report.Problems) > 0 {
		t.Fatalf("%s", strings.Join(report.Problems, "\n"))
	}
}
