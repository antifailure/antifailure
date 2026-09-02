// The negative controls for the gate.
//
// Every assertion here is that surfacecheck says NO to something. A check that
// has only ever been watched passing is a check nobody has evidence about, and
// this repository has shipped several of those: a scan whose regular expression
// could not match, a test whose skip was indistinguishable from a pass, a
// document comparison that compared a file with itself. So each of the three
// things this tool exists to refuse is built here as a real tree on disk and
// the refusal is asserted, along with the compatible change that must NOT be
// refused, which is the half that stops the gate from being satisfied by
// failing at everything.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree writes a repository shaped like the real one: an engine module with the
// packages named, and a classification file.
func tree(t *testing.T, packages map[string]string, classes string) string {
	t.Helper()
	root := t.TempDir()
	for dir, source := range packages {
		full := filepath.Join(root, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(dir)
		if err := os.WriteFile(filepath.Join(full, name+".go"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Both shipped modules have to exist or the walk errors on the missing
	// one, which would be a different failure from the one under test.
	for _, module := range shippedModules {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(module.Dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "engine", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, packagesFile), []byte(classes), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func load(t *testing.T, root string) ([]*Package, map[string]Classification) {
	t.Helper()
	packages, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	classes, err := ReadClasses(filepath.Join(root, packagesFile))
	if err != nil {
		t.Fatal(err)
	}
	return packages, classes
}

func messages(problems []Problem) string {
	var b strings.Builder
	for _, p := range problems {
		b.WriteString(p.Kind)
		b.WriteString(" ")
		b.WriteString(p.Message)
		b.WriteString("\n")
	}
	return b.String()
}

const stableClass = "stable github.com/antifailure/antifailure/engine/pkg/provider " +
	"The interfaces a provider implements, meant to be written outside this repository.\n"

func TestInternalPackagesAreNotEnumerated(t *testing.T) {
	// The compiler's half of the promise, as an assertion about this tool
	// rather than about the compiler: an internal package is not importable, so
	// it must not appear in the inventory and must not need classifying.
	root := tree(t, map[string]string{
		"engine/pkg/provider":     "package provider\n\ntype Database interface{ Name() string }\n",
		"engine/internal/secrets": "package secrets\n\ntype Value struct{ v string }\n",
	}, stableClass)

	packages, classes := load(t, root)
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/internal/") {
			t.Fatalf("the walk enumerated %s, which nothing outside the module can import", pkg.ImportPath)
		}
	}
	if problems := CheckInventory(packages, classes); len(problems) != 0 {
		t.Fatalf("an internal package was asked to be classified:\n%s", messages(problems))
	}
}

func TestCommandsAreNotEnumerated(t *testing.T) {
	root := tree(t, map[string]string{
		"engine/pkg/provider": "package provider\n\ntype Database interface{ Name() string }\n",
		"engine/cmd/af":       "package main\n\nfunc main() {}\n",
	}, stableClass)

	packages, classes := load(t, root)
	if problems := CheckInventory(packages, classes); len(problems) != 0 {
		t.Fatalf("a command was asked to be classified:\n%s", messages(problems))
	}
}

func TestAnUnclassifiedPackageFails(t *testing.T) {
	root := tree(t, map[string]string{
		"engine/pkg/provider": "package provider\n\ntype Database interface{ Name() string }\n",
		"engine/pkg/hastily":  "package hastily\n\n// Thing is public the moment it lands.\ntype Thing struct{ Name string }\n",
	}, stableClass)

	packages, classes := load(t, root)
	problems := CheckInventory(packages, classes)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "pkg/hastily") {
		t.Fatalf("a new importable package was not reported:\n%s", messages(problems))
	}
}

func TestAClassificationForAGonePackageFails(t *testing.T) {
	root := tree(t, map[string]string{
		"engine/pkg/provider": "package provider\n\ntype Database interface{ Name() string }\n",
	}, stableClass+"unstable github.com/antifailure/antifailure/engine/pkg/departed "+
		"A package that used to exist and no longer does.\n")

	packages, classes := load(t, root)
	problems := CheckInventory(packages, classes)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "pkg/departed") {
		t.Fatalf("a stale classification was not reported:\n%s", messages(problems))
	}
}

// The defect the tool was written for: an internal type in a stable signature.
func TestAnInternalTypeInAStableSignatureFails(t *testing.T) {
	root := tree(t, map[string]string{
		"engine/pkg/provider": "package provider\n\n" +
			"import \"github.com/antifailure/antifailure/engine/internal/secrets\"\n\n" +
			"type Database interface {\n\tConnString() (secrets.Value, error)\n}\n",
		"engine/internal/secrets": "package secrets\n\ntype Value struct{ v string }\n",
	}, stableClass)

	packages, classes := load(t, root)
	problems := CheckLeaks(packages, classes)
	if len(problems) != 1 {
		t.Fatalf("expected one leak, got:\n%s", messages(problems))
	}
	if !strings.Contains(problems[0].Message, "Database.ConnString") ||
		!strings.Contains(problems[0].Message, "internal/secrets.Value") {
		t.Fatalf("the leak was reported without naming the thing:\n%s", messages(problems))
	}
}

func TestAnUnstableTypeInAStableSignatureFails(t *testing.T) {
	// Not only internal. A package that is importable and classified unstable
	// can be changed in a minor release, so naming one in a stable signature
	// makes the stable signature change with it.
	root := tree(t, map[string]string{
		"engine/pkg/provider": "package provider\n\n" +
			"import \"github.com/antifailure/antifailure/engine/pkg/edition\"\n\n" +
			"type Database interface {\n\tEdition() edition.Kind\n}\n",
		"engine/pkg/edition": "package edition\n\ntype Kind string\n",
	}, stableClass+"unstable github.com/antifailure/antifailure/engine/pkg/edition "+
		"How a binary reports which edition it is, which is ours to change.\n")

	packages, classes := load(t, root)
	problems := CheckLeaks(packages, classes)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "is not classified stable") {
		t.Fatalf("an unstable type in a stable signature was not reported:\n%s", messages(problems))
	}
}

func TestAStableTypeInAStableSignaturePasses(t *testing.T) {
	// The half that stops the leak check from being satisfied by refusing
	// everything. A stable package naming another stable package is the
	// arrangement the promise is FOR.
	root := tree(t, map[string]string{
		"engine/pkg/provider": "package provider\n\n" +
			"import \"github.com/antifailure/antifailure/engine/pkg/secret\"\n\n" +
			"type Database interface {\n\tConnString() (secret.Value, error)\n}\n",
		"engine/pkg/secret": "package secret\n\ntype Value struct{ v string }\n",
	}, stableClass+"stable github.com/antifailure/antifailure/engine/pkg/secret "+
		"The type provider signatures name, public so an outside caller can name it too.\n")

	packages, classes := load(t, root)
	if problems := CheckLeaks(packages, classes); len(problems) != 0 {
		t.Fatalf("a stable type in a stable signature was reported:\n%s", messages(problems))
	}
}

func TestAStandardLibraryTypeInAStableSignaturePasses(t *testing.T) {
	root := tree(t, map[string]string{
		"engine/pkg/provider": "package provider\n\n" +
			"import (\n\t\"context\"\n\t\"time\"\n)\n\n" +
			"type Database interface {\n\tUp(ctx context.Context, d time.Duration) error\n}\n",
	}, stableClass)

	packages, classes := load(t, root)
	if problems := CheckLeaks(packages, classes); len(problems) != 0 {
		t.Fatalf("a standard library type was reported as a leak:\n%s", messages(problems))
	}
}

// The compatibility half, exercised through a baseline written from one tree
// and compared against another.
func compatibility(t *testing.T, before, after string) ([]Problem, int) {
	t.Helper()
	beforeRoot := tree(t, map[string]string{"engine/pkg/provider": before}, stableClass)
	packages, classes := load(t, beforeRoot)
	baselinePath := filepath.Join(beforeRoot, baselineFile)
	if err := WriteBaseline(baselinePath, packages, classes); err != nil {
		t.Fatal(err)
	}
	baseline, err := ReadBaseline(baselinePath)
	if err != nil {
		t.Fatal(err)
	}

	afterRoot := tree(t, map[string]string{"engine/pkg/provider": after}, stableClass)
	afterPackages, afterClasses := load(t, afterRoot)
	return CheckCompatibility(afterPackages, afterClasses, baseline)
}

func TestAddingAnExportPasses(t *testing.T) {
	problems, additions := compatibility(t,
		"package provider\n\nfunc Up() error { return nil }\n",
		"package provider\n\nfunc Up() error { return nil }\n\nfunc Down() error { return nil }\n",
	)
	if len(problems) != 0 {
		t.Fatalf("adding an export was reported as incompatible:\n%s", messages(problems))
	}
	if additions != 1 {
		t.Fatalf("expected one addition, got %d", additions)
	}
}

func TestRemovingAnExportFails(t *testing.T) {
	problems, _ := compatibility(t,
		"package provider\n\nfunc Up() error { return nil }\n\nfunc Down() error { return nil }\n",
		"package provider\n\nfunc Up() error { return nil }\n",
	)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "Down is gone") {
		t.Fatalf("removing an export was not reported:\n%s", messages(problems))
	}
}

func TestChangingASignatureFails(t *testing.T) {
	problems, _ := compatibility(t,
		"package provider\n\nfunc Up() error { return nil }\n",
		"package provider\n\nfunc Up(force bool) error { return nil }\n",
	)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "changed shape") {
		t.Fatalf("a changed signature was not reported:\n%s", messages(problems))
	}
}

func TestRemovingAStructFieldFails(t *testing.T) {
	// The reason entries are one per field rather than one per declaration: a
	// struct printed whole is one line that changes whenever anything in it
	// does and names nothing.
	problems, _ := compatibility(t,
		"package provider\n\ntype Branch struct {\n\tID string\n\tName string\n}\n",
		"package provider\n\ntype Branch struct {\n\tID string\n}\n",
	)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "Branch.Name is gone") {
		t.Fatalf("a removed field was not reported:\n%s", messages(problems))
	}
}

func TestAddingAnInterfaceMethodFails(t *testing.T) {
	// The case that reads as an addition in the diff and lands as a removal in
	// somebody else's build. A caller of provider.Database keeps compiling; an
	// implementation of it, which is what the interface is FOR, does not.
	problems, _ := compatibility(t,
		"package provider\n\ntype Database interface {\n\tUp() error\n}\n",
		"package provider\n\ntype Database interface {\n\tUp() error\n\tDown() error\n}\n",
	)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "Database.Down is new on an interface") {
		t.Fatalf("a method added to a published interface was not reported:\n%s", messages(problems))
	}
}

func TestAddingAStructFieldPasses(t *testing.T) {
	// The other side of the same rule, so it does not become "any addition
	// fails". A field on an options struct is the ordinary way this surface
	// grows and nothing outside breaks on it.
	problems, additions := compatibility(t,
		"package provider\n\ntype Options struct {\n\tTimeout int\n}\n",
		"package provider\n\ntype Options struct {\n\tTimeout int\n\tRetries int\n}\n",
	)
	if len(problems) != 0 || additions != 1 {
		t.Fatalf("adding a struct field was reported: %d additions\n%s", additions, messages(problems))
	}
}

func TestChangingAConstantValueFails(t *testing.T) {
	problems, _ := compatibility(t,
		"package provider\n\nconst DefaultImage = \"busybox:1.36\"\n",
		"package provider\n\nconst DefaultImage = \"busybox:1.37\"\n",
	)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "DefaultImage") {
		t.Fatalf("a changed constant was not reported:\n%s", messages(problems))
	}
}

func TestUnexportedThingsAreNotSurface(t *testing.T) {
	problems, additions := compatibility(t,
		"package provider\n\nfunc Up() error { return nil }\n",
		"package provider\n\nfunc Up() error { return nil }\n\nfunc down() error { return nil }\n"+
			"\ntype hidden struct{ Name string }\n",
	)
	if len(problems) != 0 || additions != 0 {
		t.Fatalf("something unexported reached the surface: %d additions\n%s", additions, messages(problems))
	}
}

func TestAMethodOnAnUnexportedTypeIsNotSurface(t *testing.T) {
	problems, additions := compatibility(t,
		"package provider\n\nfunc Up() error { return nil }\n",
		"package provider\n\nfunc Up() error { return nil }\n\ntype hidden struct{}\n"+
			"\nfunc (hidden) Reachable() error { return nil }\n",
	)
	if len(problems) != 0 || additions != 0 {
		t.Fatalf("a method on an unexported type reached the surface: %d additions\n%s", additions, messages(problems))
	}
}

func TestAReasonlessClassificationIsRefused(t *testing.T) {
	root := tree(t, map[string]string{
		"engine/pkg/provider": "package provider\n\ntype Database interface{ Name() string }\n",
	}, "stable github.com/antifailure/antifailure/engine/pkg/provider yes\n")

	if _, err := ReadClasses(filepath.Join(root, packagesFile)); err == nil {
		t.Fatal("a classification with no reason was accepted")
	}
}

func TestAnEmptyBaselineIsRefused(t *testing.T) {
	// A baseline that records nothing passes against any surface at all, which
	// is the shape of check this repository has been bitten by before.
	root := t.TempDir()
	path := filepath.Join(root, "empty.txt")
	if err := os.WriteFile(path, []byte("# nothing but a comment\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBaseline(path); err == nil {
		t.Fatal("an empty baseline was accepted, so the comparison would pass against anything")
	}
}

// The real tree, which is the only assertion here that is not a negative
// control: the checked-in baseline and classification have to describe the
// repository as it stands, or the gate is red on main.
func TestTheRepositoryPasses(t *testing.T) {
	root := "../.."
	if _, err := os.Stat(filepath.Join(root, packagesFile)); err != nil {
		t.Skipf("not running from the repository: %v", err)
	}
	packages, classes := load(t, root)
	problems := CheckInventory(packages, classes)
	problems = append(problems, CheckLeaks(packages, classes)...)
	baseline, err := ReadBaseline(filepath.Join(root, baselineFile))
	if err != nil {
		t.Fatal(err)
	}
	compat, _ := CheckCompatibility(packages, classes, baseline)
	problems = append(problems, compat...)
	if len(problems) != 0 {
		t.Fatalf("the tree does not match what it promises:\n%s", messages(problems))
	}
	// The negative control on the walk itself. A walk that found nothing would
	// pass every check above.
	if len(packages) < 10 {
		t.Fatalf("the walk found only %d importable packages", len(packages))
	}
}
