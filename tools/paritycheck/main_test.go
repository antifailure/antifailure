package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is this repository, which is what the last test in this file puts
// the whole gate to.
const repoRoot = "../.."

// tree writes a throwaway repository holding only import statements, which is
// all checkSurfacesAreTheWholeStory reads.
func tree(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	var tracked []string
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		tracked = append(tracked, rel)
	}
	return root, tracked
}

// importing is a Go file that imports one path and nothing else.
func importing(paths ...string) string {
	var b strings.Builder
	b.WriteString("package p\n\nimport (\n")
	for _, p := range paths {
		b.WriteString("\t\"" + p + "\"\n")
	}
	b.WriteString(")\n")
	return b.String()
}

// wholeTree is a tree in which every surface this gate knows imports the
// capability API, which is what the repository itself looks like.
func wholeTree() map[string]string {
	return map[string]string{
		"engine/internal/cli/root.go": importing(
			capabilityImport, modulePrefix+workloadPkg),
		"engine/internal/mcp/serve.go":     importing(capabilityImport),
		"engine/internal/workload/run.go":  importing(capabilityImport),
		"engine/internal/env/env.go":       "package env\n",
		"engine/internal/report/report.go": "package report\n",
		"engine/internal/cli/root_test.go": importing("testing"),
	}
}

func TestTheSurfaceListHasToDescribeTheTree(t *testing.T) {
	t.Parallel()
	// The gate's whole inventory is read through the surface list, so a list
	// that no longer describes the tree is the difference between "I looked
	// and it was fine" and "I could not look". Both directions, because a
	// check that refuses everything and a check that refuses nothing look the
	// same from one side.
	root, files := tree(t, wholeTree())
	if err := checkSurfacesAreTheWholeStory(root, files); err != nil {
		t.Fatalf("the tree this gate was written against must be accepted: %v", err)
	}

	t.Run("a fourth importer is a surface nobody is checking", func(t *testing.T) {
		files := wholeTree()
		files["engine/internal/hosted/serve.go"] = importing(capabilityImport)
		root, tracked := tree(t, files)
		err := checkSurfacesAreTheWholeStory(root, tracked)
		if err == nil {
			t.Fatal("a new importer of the capability API must not be ignored")
		}
		if !strings.Contains(err.Error(), "engine/internal/hosted") {
			t.Fatalf("the refusal has to name it: %v", err)
		}
	})

	t.Run("a listed surface that imports nothing means the tree moved", func(t *testing.T) {
		files := wholeTree()
		delete(files, "engine/internal/mcp/serve.go")
		root, tracked := tree(t, files)
		err := checkSurfacesAreTheWholeStory(root, tracked)
		if err == nil {
			t.Fatal("a surface measured against an empty set must not pass")
		}
		if !strings.Contains(err.Error(), "engine/internal/mcp") {
			t.Fatalf("the refusal has to name it: %v", err)
		}
	})

	t.Run("a test file is not a surface", func(t *testing.T) {
		// A _test.go file reaches nothing a customer can reach, so one
		// importing the capability API must not register as a fourth surface.
		files := wholeTree()
		files["engine/internal/hosted/serve_test.go"] = importing(capabilityImport)
		root, tracked := tree(t, files)
		if err := checkSurfacesAreTheWholeStory(root, tracked); err != nil {
			t.Fatalf("a test file must not be read as a surface: %v", err)
		}
	})

	t.Run("the workload runner may only be reached from the command line", func(t *testing.T) {
		// It is counted as part of the command line's surface because only the
		// command line reaches it. A second importer makes that classification
		// false, and a capability reached only through it would then be
		// reported as reachable from a surface that cannot reach it.
		files := wholeTree()
		files["engine/internal/mcp/serve.go"] = importing(
			capabilityImport, modulePrefix+workloadPkg)
		root, tracked := tree(t, files)
		err := checkSurfacesAreTheWholeStory(root, tracked)
		if err == nil {
			t.Fatal("a second importer of the workload runner must be refused")
		}
		if !strings.Contains(err.Error(), workloadPkg) {
			t.Fatalf("the refusal has to name it: %v", err)
		}
	})
}

func TestARowThisGateCannotActOnIsRefused(t *testing.T) {
	t.Parallel()
	const good = "SQLLoad\tcli-only\ta reason long enough to be one, saying what would close it\n"
	for _, tc := range []struct {
		name, body, want string
	}{
		{"two fields", "SQLLoad\tcli-only\n", "three"},
		{"four fields", "SQLLoad\tcli-only\twhy\tand something else entirely\n", "three"},
		{"a kind nobody checks", "SQLLoad\tbecause-i-said-so\ta reason long enough to be one\n", "kind"},
		{"a reason that is not one", "SQLLoad\tcli-only\tbecause\n", "too short"},
		{"the same method twice", good + good, "a second time"},
		{"no rows at all", "# only a comment\n", "no rows at all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := tree(t, map[string]string{exemptionsPath: tc.body})
			_, err := exemptions(root)
			if err == nil {
				t.Fatalf("%s must be refused", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal has to say why, got: %v", err)
			}
		})
	}

	// And the other direction. A parser that refuses everything passes every
	// test above and is useless.
	root, _ := tree(t, map[string]string{
		exemptionsPath: "# a comment\n\n" + good + "Branch\taccessor\treports what its caller already handed it\n",
	})
	rows, err := exemptions(root)
	if err != nil {
		t.Fatalf("an honest file must be accepted: %v", err)
	}
	if len(rows) != 2 || rows["SQLLoad"].kind != kindCLIOnly || rows["Branch"].line != 4 {
		t.Fatalf("the rows were not read as written: %#v", rows)
	}
}

func TestAKindHasToHoldForTheCapabilityItExcuses(t *testing.T) {
	t.Parallel()
	// The exemption file is only worth having if a row cannot be pasted onto
	// an inconvenient capability. Each kind carries a structural precondition,
	// and each case here is checked in both directions.
	reaches := func(keys ...string) map[string]map[string][]site {
		out := map[string]map[string][]site{"cli": {}, "mcp": {}}
		for _, k := range keys {
			out[k]["X"] = []site{{where: "engine/internal/" + k + "/x.go:1"}}
		}
		return out
	}
	working := capability{name: "X", takesContext: true}
	reporting := capability{name: "X", takesContext: false}

	for _, tc := range []struct {
		name    string
		c       capability
		kind    string
		reach   map[string]map[string][]site
		refused bool
		want    string
	}{
		{"a capability filed as an accessor", working, kindAccessor, reaches("cli"), true, "context.Context"},
		{"a reporter filed as an accessor", reporting, kindAccessor, reaches("cli"), false, ""},
		{"cli-only when the cli does not reach it", working, kindCLIOnly, reaches("mcp"), true, "does not reach it either"},
		{"cli-only when it does", working, kindCLIOnly, reaches("cli"), false, ""},
		{"mcp-only when the server does not reach it", working, kindMCPOnly, reaches("cli"), true, "does not reach it either"},
		{"mcp-only when it does", working, kindMCPOnly, reaches("mcp"), false, ""},
		{"unreached when something reaches it", working, kindUnreached, reaches("cli"), true, "reaches it at"},
		{"unreached when nothing does", working, kindUnreached, reaches(), false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why := kindHolds(tc.c, row{name: "X", kind: tc.kind, line: 7}, tc.reach)
			if tc.refused && why == "" {
				t.Fatal("this row must be refused and was accepted")
			}
			if !tc.refused && why != "" {
				t.Fatalf("this row is honest and was refused: %s", why)
			}
			if tc.refused && !strings.Contains(why, tc.want) {
				t.Fatalf("the refusal has to say why, got: %s", why)
			}
		})
	}
}

func TestAGapWithNoRowFailsAndARowThatIsNoLongerNeededFails(t *testing.T) {
	t.Parallel()
	caps := []capability{
		{name: "Reachable", where: "engine/internal/env/a.go:1", takesContext: true},
		{name: "OneSurfaceOnly", where: "engine/internal/env/b.go:1", takesContext: true},
		{name: "Internal", where: "engine/internal/env/c.go:1", internalOnly: true, internalWhy: "session"},
	}
	reach := map[string]map[string][]site{
		"cli": {"Reachable": {{where: "a:1"}}, "OneSurfaceOnly": {{where: "b:1"}}},
		"mcp": {"Reachable": {{where: "a:2"}}},
	}

	uncovered, wrongKind, stale, covered, exempt, internal := gaps(caps, reach, map[string]row{})
	if len(uncovered) != 1 || !strings.Contains(uncovered[0], "OneSurfaceOnly") {
		t.Fatalf("the capability on one surface has to be named: %v", uncovered)
	}
	if !strings.Contains(uncovered[0], "and from nothing else") {
		t.Fatalf("the message has to say which surfaces it IS on: %v", uncovered)
	}
	if covered != 1 || exempt != 0 || internal != 1 {
		t.Fatalf("counted %d covered, %d exempt, %d internal", covered, exempt, internal)
	}
	if len(wrongKind) != 0 || len(stale) != 0 {
		t.Fatalf("nothing else should have been reported: %v %v", wrongKind, stale)
	}

	// With the row, it passes. Without this arm a gate that fails everything
	// would look identical.
	rows := map[string]row{"OneSurfaceOnly": {name: "OneSurfaceOnly", kind: kindCLIOnly, line: 3}}
	uncovered, wrongKind, stale, _, exempt, _ = gaps(caps, reach, rows)
	if len(uncovered) != 0 || len(wrongKind) != 0 || len(stale) != 0 || exempt != 1 {
		t.Fatalf("an excused gap must pass: %v %v %v", uncovered, wrongKind, stale)
	}

	// A row for a capability that is now on both surfaces, and a row for one
	// that no longer exists. Staleness that is not reported is an allowance
	// that outlives its argument.
	rows = map[string]row{
		"Reachable": {name: "Reachable", kind: kindCLIOnly, line: 4},
		"Gone":      {name: "Gone", kind: kindCLIOnly, line: 5},
	}
	_, _, stale, _, _, _ = gaps(caps, reach, rows)
	if len(stale) != 2 {
		t.Fatalf("both stale rows have to be reported, got: %v", stale)
	}
}

func TestTheThinEvidenceIsNamedRatherThanCountedAsTheRest(t *testing.T) {
	t.Parallel()
	// The one place this gate says yes on weaker evidence than usual. An
	// interface call is counted because the capability type implements the
	// interface, which is a full signature match and is NOT proof that an
	// orchestrator is what gets passed. If that answer is not named it
	// quietly becomes the standard.
	caps := []capability{
		{name: "Concrete"}, {name: "Mixed"}, {name: "OnlyThroughAnInterface"},
	}
	reach := map[string]map[string][]site{
		"cli": {
			"Concrete":               {{where: "a:1"}},
			"Mixed":                  {{where: "b:1"}, {where: "b:2", through: "changeReader"}},
			"OnlyThroughAnInterface": {{where: "c:1", through: "accessProber"}},
		},
		"mcp": {"Concrete": {{where: "a:2"}}},
	}

	thin := thinlyCovered(caps, reach)
	require(t, len(thin) == 1, "exactly one capability is reached thinly, got %v", thin)
	require(t, strings.Contains(thin[0], "OnlyThroughAnInterface"), "the wrong one: %v", thin)
	require(t, strings.Contains(thin[0], "cli"), "the surface has to be named: %v", thin)

	// A capability with one concrete call is not thin, whatever else it has.
	// Without this arm, a function returning every capability would pass.
	require(t, !onlyThroughInterfaces(reach["cli"]["Mixed"]), "one concrete call is not thin")
	require(t, !onlyThroughInterfaces(nil), "no call site at all is a gap, not thin evidence")
}

// require is a local assertion so this file needs no dependency the rest of
// tools/ does not already carry.
func require(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}

func TestTheFloorsRefuseAnImplausiblyQuietRead(t *testing.T) {
	t.Parallel()
	if err := floorCheck("things", 0, 1, "nothing was read"); err == nil {
		t.Fatal("a scan that found nothing must not report a clean tree")
	}
	if err := floorCheck("things", 50, 50, "nothing was read"); err != nil {
		t.Fatalf("a scan at the floor is a scan: %v", err)
	}
}

// fakeEngine writes a throwaway module shaped like the engine: a go.mod under
// engine/ and one package per directory this gate loads.
//
// It depends on nothing, so it type checks without a network, and it is the
// only way to put load() to a tree that does NOT compile.
func fakeEngine(t *testing.T, mcpBody string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"engine/go.mod":              "module github.com/antifailure/antifailure/engine\n\ngo 1.26.0\n",
		"engine/internal/env/env.go": envSource,
		"engine/internal/cli/root.go": "package cli\n\n" + importEnv +
			"func Run(o *env.Orchestrator) error { return o.Up() }\n",
		"engine/internal/workload/run.go": "package workload\n\n" + importEnv +
			"func Run(o *env.Orchestrator) error { return o.Up() }\n",
		"engine/internal/mcp/serve.go": mcpBody,
	}
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const (
	importEnv = "import \"github.com/antifailure/antifailure/engine/internal/env\"\n\n"
	envSource = "package env\n\ntype Orchestrator struct{}\n\n" +
		"func (o *Orchestrator) Up() error { return nil }\n"
	goodMCP = "package mcp\n\n" + importEnv +
		"func Serve(o *env.Orchestrator) error { return o.Up() }\n"
	brokenMCP = "package mcp\n\n" + importEnv +
		"func Serve(o *env.Orchestrator) error { var x int = \"not an int\"; _ = x; return o.Up() }\n"
)

func TestATreeThatDoesNotTypeCheckIsRefusedRatherThanReadPartially(t *testing.T) {
	t.Parallel()
	// A gate that read a broken tree and found no gaps has not found that
	// there are none: every call site in the package it could not resolve is
	// missing, so the answer is "I could not look" wearing the costume of a
	// clean tree. Both directions, because a loader that refused every tree
	// would pass the first half of this on its own.
	loaded, err := load(fakeEngine(t, goodMCP))
	if err != nil {
		t.Fatalf("a tree that compiles must be read: %v", err)
	}
	caps, recv, err := capabilities(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 || caps[0].name != "Up" {
		t.Fatalf("the method set was not read: %#v", caps)
	}
	if n := len(callSites(loaded, []string{"engine/internal/mcp"}, recv)["Up"]); n != 1 {
		t.Fatalf("the call site was not resolved, got %d", n)
	}

	_, err = load(fakeEngine(t, brokenMCP))
	if err == nil {
		t.Fatal("a package that does not type check must be refused, not read partially")
	}
	if !strings.Contains(err.Error(), "did not type check") {
		t.Fatalf("the refusal has to say what went wrong: %v", err)
	}

	// And a surface that is not there at all. A directory this gate asks for
	// and does not get is the same fact as a package it could not read: every
	// call site in it is missing, and the capabilities it alone reaches would
	// be reported as unreachable.
	gone := fakeEngine(t, goodMCP)
	if err := os.RemoveAll(filepath.Join(gone, "engine", "internal", "mcp")); err != nil {
		t.Fatal(err)
	}
	_, err = load(gone)
	if err == nil {
		t.Fatal("a surface that did not load must be refused")
	}
	if !strings.Contains(err.Error(), "engine/internal/mcp") {
		t.Fatalf("the refusal has to name it: %v", err)
	}
}

// TestTheRepositoryItselfHasNoUnexcusedGap is the gate put to the real tree.
//
// The unit tests above run over fixtures, which proves the rules and proves
// nothing about this repository. This one loads the real packages, resolves the
// real call sites and reads the real exemption file, and it is what would have
// failed on the day the SQL workload was reachable from the command line alone.
func TestTheRepositoryItselfHasNoUnexcusedGap(t *testing.T) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	files, err := tracked(repoRoot)
	if err != nil {
		t.Fatalf("the index could not be read, so nothing was checked: %v", err)
	}
	if err := checkSurfacesAreTheWholeStory(repoRoot, files); err != nil {
		t.Fatal(err)
	}
	loaded, err := load(root)
	if err != nil {
		t.Fatalf("the engine could not be loaded, so nothing was checked: %v", err)
	}
	caps, recv, err := capabilities(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if err := floorCheck("capabilities", len(caps), minCapabilities, "the load is broken"); err != nil {
		t.Fatal(err)
	}
	reach := map[string]map[string][]site{}
	for _, s := range surfaces {
		found := callSites(loaded, s.dirs, recv)
		total := 0
		for _, sites := range found {
			total += len(sites)
		}
		if err := floorCheck("call sites from "+s.key, total, minCallSites, "it was not read"); err != nil {
			t.Fatal(err)
		}
		reach[s.key] = found
	}
	rows, err := exemptions(repoRoot)
	if err != nil {
		t.Fatal(err)
	}

	uncovered, wrongKind, stale, covered, exempt, internal := gaps(caps, reach, rows)
	for _, f := range append(append(uncovered, wrongKind...), stale...) {
		t.Error(f)
	}
	t.Logf("%d capabilities: %d on every surface, %d exempt, %d uncallable from outside",
		len(caps), covered, exempt, internal)

	// The interface hop is real in this tree and the label that says so has to
	// be populated. A mutation proved why: stopping the labelling leaves every
	// site looking concrete, so the summary reports nothing reached thinly,
	// which is the claim "every call site is a concrete call" made by
	// accident. Both surfaces call the orchestrator through small local
	// interfaces today. If that ever stops being true this fails, and the
	// right response is to read the summary rather than to delete this.
	labelled := 0
	for _, sites := range reach["cli"] {
		for _, st := range sites {
			if st.through != "" {
				labelled++
			}
		}
	}
	if labelled == 0 {
		t.Error("no call site was recorded as reached through an interface, so the " +
			"summary would report that every one of them is a concrete call")
	}
	if len(thinlyCovered(caps, reach)) == 0 {
		t.Error("no capability was reported as reached only through an interface, and " +
			"five were when this was written")
	}

	// The one this gate was written for, asserted by name. A gate whose
	// motivating case quietly stopped being covered would still pass
	// everything above.
	if len(reach["mcp"]["SQLLoad"]) == 0 {
		t.Error("SQLLoad is reachable from no MCP tool, which is the gap this gate exists for")
	}
	if len(reach["cli"]["SQLLoad"]) == 0 {
		t.Error("SQLLoad is reachable from no command, so this gate is measuring the wrong thing")
	}
}
