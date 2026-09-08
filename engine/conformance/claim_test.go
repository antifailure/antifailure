package conformance_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/conformance"
)

// The two sweeps that make the ledger's completeness a property of a check
// rather than of the afternoon somebody grepped.
//
// The ruling that produced the third verdict has a second condition: an
// unproven CopyOnWrite: true is not publishable as a proved claim anywhere a
// customer reads it. A condition of that shape decays the moment somebody adds
// a provider or a row, so it is kept the way claimcheck keeps its own rule.
// Do not sweep by hand, because a sweep is only true on the day it is run.
//
// The two directions are different questions and neither implies the other.
//
// DECLARATIONS. Every provider.Caps literal in the repository that sets
// CopyOnWrite has a ledger entry. This is the direction that catches a provider
// arriving with a declaration nobody recorded a verdict for, which is the state
// all five of the existing ones were in before the ledger existed.
//
// PUBLICATIONS. Every cell in the comparison table's copy on write column says
// what the ledger says. This is the direction that catches the claim reaching a
// buyer, and it is the one that was already broken: three rows of that table
// published yes for providers that do not exist yet, beside the words "not
// measured yet" in the next cell.

// repoRoot is where the sweeps look, and it is asserted rather than assumed.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	// Named files rather than a directory, because a wrong root that happens
	// to be a directory would make both sweeps find nothing and pass.
	for _, marker := range []string{"go.work", "CONTRIBUTING.md"} {
		if _, err := os.Stat(filepath.Join(root, marker)); err != nil {
			t.Fatalf("%s does not look like the repository root: %s is not there (%v). "+
				"Both sweeps below walk the tree from here, and a root that resolved to "+
				"the wrong place would find nothing and report that as clean.",
				root, marker, err)
		}
	}
	return root
}

// TestEveryCopyOnWriteDeclarationHasARecordedVerdict is the declaration sweep.
func TestEveryCopyOnWriteDeclarationHasARecordedVerdict(t *testing.T) {
	root := repoRoot(t)
	found := declaringDirs(t, root)
	if len(found) == 0 {
		t.Fatal("the sweep found no provider.Caps literal setting CopyOnWrite anywhere in " +
			"the repository. There are several, so the sweep is broken rather than the " +
			"tree being clean, and a sweep that cannot find what it is looking for reports " +
			"every future provider as recorded too.")
	}

	// Each ledger entry claims a directory, through its Evidence path. That
	// path is checked to exist for the reason claimcheck checks its own: an
	// entry pointing at a file nobody wrote reads as a recorded verdict and is
	// a dead link.
	byDir := map[string]string{}
	for name, e := range conformance.CopyOnWriteLedger {
		if e.Evidence == "" {
			t.Errorf("the ledger entry for %q names no evidence. The entry's whole job is "+
				"to point at the instrument that would refuse the declaration, and one "+
				"that points nowhere is the recorded verdict equivalent of a function "+
				"with no callers.", name)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Evidence)); err != nil {
			t.Errorf("the ledger entry for %q names %s as its evidence and that file is "+
				"not there: %v", name, e.Evidence, err)
			continue
		}
		dir := filepath.Dir(e.Evidence)
		if other, dup := byDir[dir]; dup {
			t.Errorf("%q and %q both name evidence in %s, so the sweep cannot tell which "+
				"entry records the verdict for that provider", name, other, dir)
			continue
		}
		byDir[dir] = name
		if e.Verdict != conformance.Proved && e.Verdict != conformance.Unproven {
			t.Errorf("the ledger entry for %q records the verdict %q. A ledger records what "+
				"a run reached, and a run that refuted a declaration leaves a provider "+
				"nobody should ship rather than a row in this table.", name, e.Verdict)
		}
		if len(strings.TrimSpace(e.Because)) < 40 {
			t.Errorf("the ledger entry for %q gives %q as its reason. The reason is the only "+
				"thing that tells a harness which cannot exhibit the property apart from "+
				"an instrument nobody has fired, and both of those publish as unproven.",
				name, e.Because)
		}
	}

	for _, dir := range found {
		if _, ok := byDir[dir]; !ok {
			t.Errorf("%s declares a copy on write value in a provider.Caps literal and no "+
				"ledger entry records a verdict for it. Add one to CopyOnWriteLedger in "+
				"engine/conformance/ledger.go, with the verdict a run actually reached: "+
				"unproven is the honest entry for an instrument nobody has fired, and it "+
				"is not a defect to record, it is the state every provider starts in.", dir)
		}
	}
	for dir, name := range byDir {
		if !contains(found, dir) {
			t.Errorf("the ledger records a verdict for %q with evidence in %s, and no "+
				"provider.Caps literal there sets CopyOnWrite. A stale entry describes a "+
				"decision about something that is no longer here and would silently cover "+
				"a future declaration that landed in the same directory.", name, dir)
		}
	}

	names := make([]string, 0, len(byDir))
	for _, n := range byDir {
		names = append(names, n)
	}
	sort.Strings(names)
	t.Logf("%d directories declare a copy on write value, and the ledger records a verdict "+
		"for each: %s", len(found), strings.Join(names, ", "))
}

// declaringDirs walks the tree and returns every directory holding a
// provider.Caps composite literal that sets CopyOnWrite.
//
// The literal's TYPE is what is matched, not the field name, and that is the
// whole reason this parses rather than greps. provider.DatastoreCaps carries a
// field of the same name, checked by a different suite under different rules,
// and a grep for "CopyOnWrite:" cannot tell the two apart. Test files are
// skipped: a fake declaring a value to be caught by is the instrument, not a
// claim about a product.
func declaringDirs(t *testing.T, root string) []string {
	t.Helper()
	seen := map[string]bool{}
	fset := token.NewFileSet()

	for _, tree := range []string{"engine", "ee"} {
		base := filepath.Join(root, tree)
		if _, err := os.Stat(base); err != nil {
			t.Fatalf("the sweep cannot read %s: %v. A tree it cannot walk is a tree it "+
				"reports as clean.", base, err)
		}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				// Refused rather than skipped. A file the sweep could not read
				// is a file it could not check, and counting it as clean is
				// the shape of gap this whole package is against.
				t.Errorf("the sweep could not parse %s: %v", path, perr)
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				sel, ok := lit.Type.(*ast.SelectorExpr)
				if !ok || sel.Sel == nil || sel.Sel.Name != "Caps" {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "provider" {
					return true
				}
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok || key.Name != "CopyOnWrite" {
						continue
					}
					rel, rerr := filepath.Rel(root, filepath.Dir(path))
					if rerr == nil {
						seen[filepath.ToSlash(rel)] = true
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}

	out := make([]string, 0, len(seen))
	for d := range seen {
		// The suite's own fakes declare values on purpose, one of them wrong
		// on purpose, because that is how the behaviour is proved able to
		// fail. They are the instrument rather than a product claim, and a
		// ledger entry for a deliberately broken provider would be a verdict
		// about a thing nobody ships.
		if strings.Contains(d, "testutil/fakes") {
			continue
		}
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// benchmarkTable is the comparison table the wave publishes into, and the sweep
// below reads the real file rather than a copy of it.
const benchmarkTable = "benchmarks/README.md"

// copyOnWriteColumn is the column heading that makes a table one of these.
const copyOnWriteColumn = "Copy on write"

// TestThePublishedCopyOnWriteColumnSaysWhatTheLedgerSays is the publication
// sweep.
func TestThePublishedCopyOnWriteColumnSaysWhatTheLedgerSays(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, benchmarkTable)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the publication sweep cannot read %s: %v. This is the table the wave "+
			"publishes its copy on write claim into, so a sweep that cannot find it has "+
			"stopped checking the surface a buyer reads, and returning early here would "+
			"look exactly like a clean tree.", benchmarkTable, err)
	}

	problems, rows := checkPublishedColumn(string(raw))
	if rows == 0 {
		t.Fatalf("%s has no %q column. Either the table moved and this sweep now checks "+
			"nothing, or the column was removed; both need a person, because the sweep "+
			"passing while checking nothing is the failure it exists to prevent.",
			benchmarkTable, copyOnWriteColumn)
	}
	for _, p := range problems {
		t.Error(p)
	}
	t.Logf("%d rows of the %q column in %s agree with the ledger",
		rows, copyOnWriteColumn, benchmarkTable)
}

// checkPublishedColumn reads a Markdown document, finds the copy on write
// column, and reports every cell that says something the ledger does not.
//
// Separated from the test so the negative control below can point it at a
// document that is wrong on purpose. A gate whose refusing branch has never
// been reached is a gate nobody has watched say no.
func checkPublishedColumn(doc string) (problems []string, rows int) {
	lines := strings.Split(doc, "\n")
	col := -1
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			col = -1
			continue
		}
		cells := splitRow(line)
		if col < 0 {
			for i, c := range cells {
				if strings.EqualFold(c, copyOnWriteColumn) {
					col = i
				}
			}
			continue
		}
		if col >= len(cells) {
			continue
		}
		// The separator row under a Markdown heading.
		if strings.Trim(cells[0], "-: ") == "" {
			continue
		}
		name := strings.Trim(cells[0], "`* ")
		got := strings.ToLower(strings.TrimSpace(cells[col]))
		rows++
		want := publishedWord(conformance.PublishableClaim(name))
		if got != want {
			problems = append(problems, fmt.Sprintf(
				"the %q column publishes %q for %q and the ledger says %q. The table is "+
					"where a buyer chooses a vendor, so the cell is the claim rather than a "+
					"summary of it, and a value typed into it is exactly the unfalsifiable "+
					"declaration this whole behaviour was built to refuse. Either record a "+
					"verdict for %q in engine/conformance/ledger.go or publish %q.",
				copyOnWriteColumn, got, name, want, name, want))
		}
	}
	return problems, rows
}

// publishedWord maps the ledger's vocabulary onto the table's.
//
// The table answers a yes or no question for a reader, and the verdict type
// answers a three valued one for a machine. The mapping is one place, here, so
// that the third value keeps its own word in both: unproven does not become no,
// which would be a claim, and it does not become blank, which would be an
// omission a reader fills in themselves.
func publishedWord(claim string) string {
	switch claim {
	case "true":
		return "yes"
	case "false":
		return "no"
	default:
		return claim
	}
}

func splitRow(line string) []string {
	trimmed := strings.Trim(strings.TrimSpace(line), "|")
	parts := strings.Split(trimmed, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// TestThePublicationSweepCanSayNo is the negative control.
//
// A check that has only ever been watched pass is a check nobody has seen work.
// This points the sweep at the exact document shape that produced the ruling:
// a comparison table publishing yes for a provider whose declaration nothing
// has proved.
func TestThePublicationSweepCanSayNo(t *testing.T) {
	// The table as it stood before this lane, reduced to the rows that matter.
	// aurora has no ledger entry, so the ledger says unproven, and the cell
	// says yes beside the words "not measured yet" in the next column.
	const wrong = `
| Provider | Copy on write | First golden, per GB |
| --- | --- | --- |
| ` + "`pgurl`" + ` | no | 55 s to 169 s |
| ` + "`aurora`" + ` | yes | not measured yet, L2.2 |
`
	problems, rows := checkPublishedColumn(wrong)
	if rows != 2 {
		t.Fatalf("the control table has two provider rows and the sweep read %d, so what "+
			"follows says nothing about whether it can refuse a cell", rows)
	}
	if len(problems) != 1 {
		t.Fatalf("the sweep found %d problems in a table publishing an unproved yes, and "+
			"there is exactly one. It cannot refuse the case that produced the ruling, so "+
			"a green from it means nothing.\n%s", len(problems), strings.Join(problems, "\n"))
	}
	if !strings.Contains(problems[0], "aurora") || !strings.Contains(problems[0], "unproven") {
		t.Fatalf("the sweep refused a cell and its complaint names neither the provider nor "+
			"the word it should have published: %s", problems[0])
	}

	// The other half of the control. The same sweep over the same shape with
	// the cell corrected must find nothing, or the red above is attributable
	// to the fixture rather than to the cell.
	fixed := strings.Replace(wrong, "| yes |", "| unproven |", 1)
	problems, rows = checkPublishedColumn(fixed)
	if rows != 2 || len(problems) != 0 {
		t.Fatalf("the same table with the cell corrected still has %d problems over %d "+
			"rows, so the refusal above was about the fixture and not the claim.\n%s",
			len(problems), rows, strings.Join(problems, "\n"))
	}
}

// TestAnUnprovenDeclarationIsNotPublishableAsTheDeclaredValue is the rendering
// rule itself, checked directly.
func TestAnUnprovenDeclarationIsNotPublishableAsTheDeclaredValue(t *testing.T) {
	for _, tc := range []struct {
		declared bool
		answer   conformance.Answer
		want     string
	}{
		{true, conformance.Proved, "true"},
		{false, conformance.Proved, "false"},
		{true, conformance.Unproven, "unproven"},
		{false, conformance.Unproven, "unproven"},
		{true, conformance.Refuted, "refuted"},
		// The zero value, which is what a Finding somebody forgot to fill in
		// carries. It publishes as unproven rather than as the declaration.
		{true, conformance.Answer(""), "unproven"},
		{true, conformance.Answer("PASSED"), "unproven"},
	} {
		got := conformance.CopyOnWriteClaim(tc.declared, tc.answer)
		if got != tc.want {
			t.Errorf("CopyOnWriteClaim(%v, %q) = %q, want %q. There must be no argument "+
				"pairing that gets the declared value out of an answer that did not prove "+
				"it, because every customer facing surface renders through this.",
				tc.declared, tc.answer, got, tc.want)
		}
	}
	if conformance.Unproven.Publishable() {
		t.Error("Unproven reports itself publishable, which makes the third verdict a pass " +
			"with a longer name")
	}
	if conformance.Refuted.Publishable() {
		t.Error("Refuted reports itself publishable")
	}
	if !conformance.Proved.Publishable() {
		t.Error("Proved reports itself not publishable, which would leave nothing sayable")
	}
}
