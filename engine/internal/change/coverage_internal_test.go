package change

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The coverage contract, in four parts that do not substitute for each other.
//
// Structure: every surface constant is decided in exactly one table, and no
// covered surface selects an empty list.
// Behaviour: a real diff touching each covered surface really does select the
// checks the table promises, and a real diff touching each inert surface really
// does select nothing.
// Isolation: the infrastructure content facts only ever appear on a file the
// path rules already called infrastructure.
// Agreement: the published table in the documentation says exactly what these
// tables say.
//
// The structural part alone would pass a coverage table nothing reads. The
// behavioural part alone would pass a surface constant nobody added to either
// table, because an unclassified path fires the fail safe and selects every
// check, which looks from the outside exactly like coverage working.

// coverageManifest is a manifest that declares one of everything the surfaces
// need, so that no assertion below is silently measuring "the manifest has no
// services" instead of "the diff selects nothing".
func coverageManifest() *schema.Manifest {
	return &schema.Manifest{
		Version: 1, Name: "coverage",
		Services: []schema.Service{
			{Name: "api", Path: "api", Kind: schema.ServiceWeb, Port: 3000},
		},
		Database: &schema.Database{MaskingRules: "masking.yaml", Version: 16},
		Egress: &schema.Egress{Default: schema.ModeBlock, Rules: []schema.EgressRule{
			{Host: "api.stripe.com", Mode: schema.ModeSandbox},
		}},
		Workflows:  []schema.Workflow{{Name: "checkout", Description: "buy something"}},
		Invariants: []schema.Invariant{{Name: "no-orphans", SQL: "select 1 from t where false"}},
		Load:       &schema.Load{Enabled: true},
	}
}

// surfaceFixture is one changed file written to reach exactly one surface.
func surfaceFixture(surface Surface) File {
	line := func(path string, added ...string) File {
		f := File{Path: path, Status: StatusModified}
		for i, text := range added {
			f.AddedLines = append(f.AddedLines, AddedLine{N: i + 1, Text: text})
		}
		return f
	}
	switch surface {
	case SurfaceSchema:
		return line("migrations/0001_add_status.sql")
	case SurfaceService:
		return line("api/handler.ts")
	case SurfaceCode:
		return line("lib/total.ts")
	case SurfaceAsset:
		return line("web/styles/app.css")
	case SurfaceBuild:
		return line("Dockerfile")
	case SurfaceDependency:
		return line("package.json")
	case SurfaceConfig:
		return line("config/app.yaml")
	case SurfaceManifest:
		return line("antifailure.yaml")
	case SurfaceMasking:
		return line("masking.yaml")
	case SurfaceAuth:
		return line("internal/auth/session.go")
	case SurfaceEgress:
		return line("lib/notify.ts", `await fetch("https://hooks.slack.com/services/T0/B0");`)
	case SurfaceInfrastructure:
		return line("infra/network.tf", `  name = "private"`)
	case SurfaceDatabaseConfig:
		return line("infra/rds.tf", `  engine_version = "16"`)
	case SurfaceCapacity:
		return line("infra/ecs.tf", `  desired_count = 4`)
	case SurfaceNetworkRule:
		return line("infra/sg.tf", `  cidr_blocks = ["10.0.0.0/8"]`)
	case SurfacePipeline:
		return line(".github/workflows/ci.yml")
	case SurfaceTest:
		return line("api/handler.test.ts")
	case SurfaceDocs:
		return line("README.md")
	}
	return File{}
}

// analyzeSurface classifies one fixture and proves the fixture reached the
// surface it was written for before the caller asserts anything about the plan.
//
// That proof is the whole reason this helper exists. A fixture with a typo in
// its path classifies to unknown, unknown fires the fail safe, the fail safe
// selects every check, and a test asserting "this surface selects something"
// passes having measured a path no rule recognised.
func analyzeSurface(t *testing.T, surface Surface) *Profile {
	t.Helper()
	f := surfaceFixture(surface)
	if f.Path == "" {
		t.Fatalf("the surface %q has no fixture, so nothing behavioural can be asserted about it", surface)
	}
	p := Analyze(Options{Manifest: coverageManifest(), Files: []File{f}})
	if p.Everything {
		t.Fatalf("the fixture %s for %q was not classified, so the fail safe fired and this measures nothing",
			f.Path, surface)
	}
	for _, fact := range p.Facts {
		if fact.Surface == surface {
			return p
		}
	}
	t.Fatalf("the fixture %s was written to reach the surface %q and reached %v instead",
		f.Path, surface, factSurfaces(p))
	return nil
}

func factSurfaces(p *Profile) []string {
	var out []string
	seen := map[Surface]bool{}
	for _, f := range p.Facts {
		if !seen[f.Surface] {
			seen[f.Surface] = true
			out = append(out, string(f.Surface))
		}
	}
	sort.Strings(out)
	return out
}

// Every Surface constant declared in this package is decided in exactly one of
// the two tables.
//
// Read from the SOURCE rather than from a list written here, because a list
// written here is a second copy of the constants and the whole failure this
// test guards against is somebody adding a constant and not the table entry. A
// second copy that also has to be updated by hand catches nothing the first one
// would not have caught.
func TestCoverage_EverySurfaceConstantIsDecidedOneWayOrTheOther(t *testing.T) {
	declared := declaredSurfaces(t)
	if len(declared) < 15 {
		t.Fatalf("only %d surface constants were read out of the source, which means the reader is "+
			"broken rather than that the package shrank: %v", len(declared), declared)
	}

	for _, s := range declared {
		_, covered := coverage[s]
		_, stated := exercisesNothing[s]
		switch {
		case s == SurfaceUnknown:
			if covered || stated {
				t.Errorf("unknown is the absence of a classification, and an entry for it in either " +
					"table would give the fail safe a coverage answer")
			}
		case covered && stated:
			t.Errorf("the surface %q is in both tables, so what it exercises has two answers", s)
		case !covered && !stated:
			t.Errorf("the surface %q is in neither table. Put it in coverage with the checks that "+
				"exercise it, or in exercisesNothing with the reason nothing does. A surface in "+
				"neither selects nothing and says nothing, which reads in a report exactly like a "+
				"surface somebody decided about", s)
		}
	}
}

// NEITHER table may be satisfied by an empty value.
//
// Splitting one map into two makes "this surface exercises nothing" a thing
// somebody writes down. It does not by itself make them write anything IN it,
// and an empty value in either table rebuilds the exact state the split was
// meant to remove, one type further along. An empty check list is a surface
// that says it is exercised while plan walks nothing. An empty reason is a
// surface that says it is inert while the report counts its files and explains
// neither, which is the quiet nothing again wearing the other table's clothes.
//
// Each field is its own check with its own message rather than one condition
// joined by ors, so that a mutation aimed at any one of them is caught by an
// assertion that names it. A single or'd condition would go red for all four
// and prove only that one of them was read.
func TestCoverage_NeitherTableCanBeSatisfiedByAnEmptyValue(t *testing.T) {
	for surface, checks := range coverage {
		if len(checks) == 0 {
			t.Errorf("the surface %q is in the coverage table with no checks. Either name the checks "+
				"that exercise it, or move it to exercisesNothing with the reason", surface)
		}
	}
	for surface, spec := range exercisesNothing {
		if spec.why == "" {
			t.Errorf("the surface %q exercises nothing and does not say why, so the report would "+
				"count its files and explain neither", surface)
		}
		if spec.one == "" {
			t.Errorf("the surface %q has no singular noun, so the blind spot counting one of its "+
				"files would print a bare number", surface)
		}
		if spec.many == "" {
			t.Errorf("the surface %q has no plural noun, so the blind spot counting several of its "+
				"files would print a bare number", surface)
		}
	}
}

// The same rule, reached through the report rather than through the field.
//
// The test above reads the struct. This one reads what a person is shown, which
// is the thing that actually has to be true: a diff touching an inert surface
// produces a blind spot sentence that counts the files AND says why. A reason
// that went empty would still leave a sentence here, just a shorter and emptier
// one, so this asserts the reason's own words are in it.
func TestCoverage_AnInertSurfaceExplainsItselfInTheReport(t *testing.T) {
	for surface, spec := range exercisesNothing {
		t.Run(string(surface), func(t *testing.T) {
			p := analyzeSurface(t, surface)
			blind := strings.Join(p.Blind, "\n")
			if !strings.Contains(blind, spec.why) {
				t.Errorf("a diff touching %q does not carry its own reason into the report.\nwant: %s\ngot:\n%s",
					surface, spec.why, blind)
			}
			if !strings.Contains(blind, "1 "+spec.one+" changed") {
				t.Errorf("a diff touching one %q file does not count it in the report's own words: %s",
					surface, blind)
			}
		})
	}
}

// Every check the coverage table names has to be a check the plan renders,
// or a surface would select something no report ever reports.
func TestCoverage_NamesOnlyRealChecks(t *testing.T) {
	known := map[Check]bool{}
	for _, c := range Checks() {
		known[c] = true
	}
	for surface, checks := range coverage {
		for _, c := range checks {
			if !known[c] {
				t.Errorf("the surface %q selects %q, which Checks does not list", surface, c)
			}
		}
	}
}

// The behavioural half. A real diff touching each covered surface selects the
// checks its table row promises, measured through Analyze rather than by
// reading the map back.
func TestCoverage_ACoveredSurfaceReallySelectsItsChecks(t *testing.T) {
	for surface, checks := range coverage {
		t.Run(string(surface), func(t *testing.T) {
			p := analyzeSurface(t, surface)
			for _, c := range checks {
				if !p.Selects(c) {
					t.Errorf("the coverage table says %q is exercised by %q and a diff touching it "+
						"selects %v", surface, c, p.Selected())
				}
			}
			if len(p.Selected()) == 0 {
				t.Errorf("the surface %q is covered and a diff touching it selects no check at all", surface)
			}
		})
	}
}

// The converse, which is the half that keeps the first one honest. A surface
// this product says it does not exercise has to actually select nothing, or the
// documentation and the report are telling a customer the opposite of what the
// engine does.
func TestCoverage_AnInertSurfaceReallySelectsNothing(t *testing.T) {
	for surface := range exercisesNothing {
		t.Run(string(surface), func(t *testing.T) {
			p := analyzeSurface(t, surface)
			if selected := p.Selected(); len(selected) > 0 {
				t.Errorf("the surface %q is listed as exercising nothing and a diff touching it "+
					"selects %v", surface, selected)
			}
		})
	}
}

// The infrastructure content facts only ever sit on a file the path rules
// already called infrastructure.
//
// classify gates them on that and nothing enforces the gate, so this is the
// enforcement. Without it, moving the call one line up in classify would let
// `replicas:` in a Kubernetes-shaped test fixture, `cpu` in a benchmark and
// `memory` in a Go comment select the load check.
func TestCoverage_TheInfrastructureContentFactsStayOnInfrastructureFiles(t *testing.T) {
	iacRules := map[string]bool{
		"content.iac_database_version": true,
		"content.iac_server_parameter": true,
		"content.iac_capacity":         true,
		"content.iac_network_rule":     true,
	}

	// Lines that would match every one of the content rules, put into files of
	// every other surface. Only the infrastructure file may produce a fact.
	lines := []AddedLine{
		{N: 1, Text: `  engine_version = "16"`},
		{N: 2, Text: `  name = "lock_timeout"`},
		{N: 3, Text: `  desired_count = 4`},
		{N: 4, Text: `  cidr_blocks = ["0.0.0.0/0"]`},
		{N: 5, Text: `resource "aws_security_group_rule" "db" {`},
	}
	for _, path := range []string{
		"api/handler.ts", "config/app.yaml", "README.md", "package.json",
		"migrations/0001_x.sql", "api/handler.test.ts", ".github/workflows/ci.yml",
		"Dockerfile", "web/styles/app.css", "internal/auth/session.go",
	} {
		p := Analyze(Options{Manifest: coverageManifest(), Files: []File{
			{Path: path, Status: StatusModified, AddedLines: lines},
		}})
		for _, f := range p.Facts {
			if iacRules[f.Rule] {
				t.Errorf("%s is not an infrastructure file and the rule %s fired on it, subject %q",
					path, f.Rule, f.Subject)
			}
		}
	}

	// And the control, so that a gate which refuses everything cannot pass this
	// test by refusing the real case too.
	p := Analyze(Options{Manifest: coverageManifest(), Files: []File{
		{Path: "infra/db.tf", Status: StatusModified, AddedLines: lines},
	}})
	fired := map[string]bool{}
	for _, f := range p.Facts {
		if iacRules[f.Rule] {
			fired[f.Rule] = true
		}
	}
	for rule := range iacRules {
		if !fired[rule] {
			t.Errorf("the rule %s did not fire on an infrastructure file carrying a line written for "+
				"it, so the isolation above is measuring a rule that never fires", rule)
		}
	}
}

// The published table and these two tables are one fact, so they are compared
// rather than kept in step by hand.
//
// A hand written copy of a table in prose is a second opinion about the same
// fact, and the second opinion is always the one that is wrong and never the
// one anybody reads. The documentation is where a customer decides whether to
// trust the plan, so this is the copy that matters most.
func TestCoverage_TheDocumentedTableSaysWhatTheseTablesSay(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("could not find the repository root: %v", err)
	}
	page := filepath.Join(root, "docs", "src", "content", "docs", "concepts", "change-analysis.md")
	body, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("the page that publishes the coverage table could not be read, so this test "+
			"cannot say whether it agrees: %v", err)
	}

	documented := parseCoverageTable(string(body))
	if len(documented) == 0 {
		t.Fatalf("no coverage table was found in %s. The table is what a customer reads, and a test "+
			"that quietly finds none would report agreement having compared nothing", page)
	}

	want := map[Surface]string{}
	for surface, checks := range coverage {
		names := make([]string, 0, len(checks))
		for _, c := range checks {
			names = append(names, string(c))
		}
		want[surface] = strings.Join(names, ", ")
	}
	for surface := range exercisesNothing {
		want[surface] = "nothing"
	}

	for surface, selects := range want {
		got, ok := documented[surface]
		switch {
		case !ok:
			t.Errorf("the surface %q selects %s and the published table does not list it at all",
				surface, selects)
		case got != selects:
			t.Errorf("the published table says %q selects %q and it selects %q",
				surface, got, selects)
		}
	}
	for surface := range documented {
		if _, ok := want[surface]; !ok {
			t.Errorf("the published table lists a surface %q that this package does not have", surface)
		}
	}
}

// parseCoverageTable reads the rows of the one markdown table whose first
// column is a surface in backticks and whose header ends in "Selects".
func parseCoverageTable(body string) map[Surface]string {
	out := map[Surface]string{}
	inTable := false
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "| Surface |") && strings.HasSuffix(line, "Selects |") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			break
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) != 3 {
			continue
		}
		name := strings.Trim(strings.TrimSpace(cells[0]), "`")
		if name == "" || strings.HasPrefix(name, "-") {
			continue
		}
		out[Surface(name)] = strings.TrimSpace(cells[2])
	}
	return out
}

// declaredSurfaces reads every Surface typed constant out of this package's own
// source and returns the values they are declared with.
func declaredSurfaces(t *testing.T) []Surface {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("could not read this package's own source: %v", err)
	}

	var out []Surface
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					ident, ok := value.Type.(*ast.Ident)
					if !ok || ident.Name != "Surface" || len(value.Values) != 1 {
						continue
					}
					lit, ok := value.Values[0].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					unquoted, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("a Surface constant has a value this reader could not unquote: %v", err)
					}
					out = append(out, Surface(unquoted))
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
