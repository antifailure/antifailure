package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every case here drives the real `go` toolchain over a real module, because
// the thing under test is what happens when a directory is taken away and a
// suite is run twice. A table of strings would prove the classifier and not
// the gate, and the defect this replaces was in the gate.

// tree writes a community module with the packages named, plus an ee
// directory to take away. Each package is a name and the body of its test.
func tree(t *testing.T, packages map[string]string) string {
	t.Helper()
	root := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(root, "ee"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ee", "marker.txt"),
		[]byte("the enterprise edition lives here\n"), 0o644))

	engine := filepath.Join(root, moduleDir)
	require.NoError(t, os.MkdirAll(engine, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(engine, "go.mod"),
		[]byte("module example/engine\n\ngo 1.26.0\n"), 0o644))

	for name, body := range packages {
		dir := filepath.Join(engine, "internal", name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg.go"),
			[]byte("package "+name+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "pkg_test.go"),
			[]byte(body), 0o644))
	}
	return root
}

// passing is a package whose test does nothing and succeeds.
func passing(name string) string {
	return fmt.Sprintf("package %s\n\nimport \"testing\"\n\nfunc TestPasses(t *testing.T) {}\n", name)
}

// alwaysFails is the shape of the nine real failures: a stale generated file,
// a schema drift, an emulator suite. It fails with ee and without it.
func alwaysFails(name string) string {
	return fmt.Sprintf("package %s\n\nimport \"testing\"\n\n"+
		"func TestStaleGeneratedFile(t *testing.T) {\n\tt.Fatal(\"pages.gen.go is stale\")\n}\n", name)
}

// readsEE is the violation the gate exists for: no import, so the compiler
// cannot see it, but the package cannot work without the enterprise tree on
// disk.
func readsEE(name string) string {
	return fmt.Sprintf("package %s\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"+
		"func TestReadsTheEnterpriseTree(t *testing.T) {\n"+
		"\tif _, err := os.Stat(\"../../../ee/marker.txt\"); err != nil {\n"+
		"\t\tt.Fatalf(\"the enterprise tree is not there: %%v\", err)\n\t}\n}\n", name)
}

// flaky fails the first time it is run and passes every time after, by
// consuming a marker its own package directory carries.
func flaky(name string) string {
	return fmt.Sprintf("package %s\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"+
		"func TestFlaky(t *testing.T) {\n"+
		"\tif _, err := os.Stat(\"first-run\"); err == nil {\n"+
		"\t\tos.Remove(\"first-run\")\n\t\tt.Fatal(\"failed once and will not fail again\")\n\t}\n}\n", name)
}

func TestACleanTreePassesWithEeTakenAway(t *testing.T) {
	root := tree(t, map[string]string{"clean": passing("clean")})
	rep, err := check(root, shell)
	require.NoError(t, err)
	require.True(t, rep.ok(), "a tree that needs nothing from ee must pass: %+v", rep)
	require.Empty(t, rep.unrelated)
	require.Contains(t, rep.render(), "builds and passes with ee taken away")
}

// The exact case that produced this tool. Thirteen pull requests were told
// their edition boundary was broken by a stale generated file.
func TestAPackageThatFailsWithEePresentIsNotAnEditionViolation(t *testing.T) {
	root := tree(t, map[string]string{"stale": alwaysFails("stale")})
	rep, err := check(root, shell)
	require.NoError(t, err)
	require.True(t, rep.ok(),
		"a failure that reproduces with ee present is not this gate's finding")
	require.Equal(t, []string{"example/engine/internal/stale"}, rep.unrelated)
	require.Empty(t, rep.dependent, "it must not be named as needing ee")
	require.Contains(t, rep.render(), "the engine job's finding")
}

// The proof it can still say no.
func TestAPackageThatReachesIntoEeIsRefused(t *testing.T) {
	root := tree(t, map[string]string{"reaches": readsEE("reaches")})
	rep, err := check(root, shell)
	require.NoError(t, err)
	require.Equal(t, []string{"example/engine/internal/reaches"}, rep.dependent)
	require.False(t, rep.ok(), "a package that needs ee must fail this gate")
	require.Contains(t, rep.render(), "the community\nbuild needs the enterprise edition")
}

// The two must not cancel out. An unrelated failure alongside a real one is
// how a loosened gate goes quietly green.
func TestAnUnrelatedFailureDoesNotHideARealViolation(t *testing.T) {
	root := tree(t, map[string]string{
		"stale":   alwaysFails("stale"),
		"reaches": readsEE("reaches"),
		"clean":   passing("clean"),
	})
	rep, err := check(root, shell)
	require.NoError(t, err)
	require.False(t, rep.ok(), "the real violation must still refuse the tree")
	require.Equal(t, []string{"example/engine/internal/reaches"}, rep.dependent)
	require.Equal(t, []string{"example/engine/internal/stale"}, rep.unrelated)
}

func TestAFlakeIsReportedAsUndecidedRatherThanAsAViolation(t *testing.T) {
	root := tree(t, map[string]string{"unsteady": flaky("unsteady")})
	require.NoError(t, os.WriteFile(
		filepath.Join(root, moduleDir, "internal", "unsteady", "first-run"), []byte("x"), 0o644))

	rep, err := check(root, shell)
	require.NoError(t, err)
	require.Equal(t, []string{"example/engine/internal/unsteady"}, rep.unclear)
	require.Empty(t, rep.dependent, "one failure and one pass is not evidence of a dependency")
	require.False(t, rep.ok(), "an undecided answer is not a pass")
	require.Contains(t, rep.render(), "COULD-NOT-LOOK")
}

// A community tree that does not compile without ee is the same violation
// arriving one stage earlier, and it must be classified the same way.
func TestATreeThatOnlyCompilesWithEeIsRefused(t *testing.T) {
	root := tree(t, map[string]string{"clean": passing("clean")})
	writeEEModule(t, root)
	engine := filepath.Join(root, moduleDir)
	require.NoError(t, os.WriteFile(filepath.Join(engine, "go.mod"),
		[]byte("module example/engine\n\ngo 1.26.0\n\nrequire example/ee v0.0.0\n\nreplace example/ee => ../ee\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(engine, "internal", "clean", "uses.go"),
		[]byte("package clean\n\nimport \"example/ee\"\n\nvar _ = ee.Name\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(engine, "go.sum"), nil, 0o644))

	rep, err := check(root, shell)
	require.NoError(t, err)
	require.Equal(t, []string{buildPseudoPackage}, rep.dependent)
	require.False(t, rep.ok())
}

// And a tree that was already broken must not be blamed on the boundary.
func TestATreeThatDoesNotCompileEitherWayIsNotAnEditionViolation(t *testing.T) {
	root := tree(t, map[string]string{"broken": passing("broken")})
	require.NoError(t, os.WriteFile(
		filepath.Join(root, moduleDir, "internal", "broken", "pkg.go"),
		[]byte("package broken\n\nthis is not go\n"), 0o644))

	rep, err := check(root, shell)
	require.NoError(t, err)
	require.Equal(t, []string{buildPseudoPackage}, rep.unrelated)
	require.Empty(t, rep.dependent)
	require.True(t, rep.ok(), "a tree that never compiled says nothing about ee")
}

// ee must be on disk again whatever happened, because this runs on a
// contributor's checkout as well as on a runner that is thrown away.
func TestEeIsPutBackAfterEveryVerdict(t *testing.T) {
	for name, body := range map[string]string{
		"clean":   passing("clean"),
		"stale":   alwaysFails("stale"),
		"reaches": readsEE("reaches"),
	} {
		root := tree(t, map[string]string{name: body})
		_, err := check(root, shell)
		require.NoError(t, err)
		require.DirExists(t, filepath.Join(root, "ee"), "ee was not put back after %s", name)
		require.NoDirExists(t, filepath.Join(root, hidden))
	}
}

func TestARunWithNoEeDirectoryIsAnErrorRatherThanAPass(t *testing.T) {
	root := tree(t, map[string]string{"clean": passing("clean")})
	require.NoError(t, os.RemoveAll(filepath.Join(root, "ee")))
	_, err := check(root, shell)
	require.Error(t, err, "nothing was taken away, so nothing was proved")
	require.Contains(t, err.Error(), "nothing was proved")
}

func TestFailedPackagesReadsTheSummaryLineAndNotTheFailingTest(t *testing.T) {
	out := strings.Join([]string{
		"--- FAIL: TestSomething (0.00s)",
		"FAIL",
		"FAIL\tgithub.com/a/b/internal/docs\t0.008s",
		"ok  \tgithub.com/a/b/internal/fine\t0.100s",
		"FAIL\tgithub.com/a/b/internal/cli [build failed]",
		"FAIL\tgithub.com/a/b/internal/cli [build failed]",
		"FAIL\tgithub.com/a/b/internal/db [setup failed]",
	}, "\n")
	require.Equal(t, []string{
		"github.com/a/b/internal/cli",
		"github.com/a/b/internal/db",
		"github.com/a/b/internal/docs",
	}, failedPackages(out),
		"a package that could not build or set up fails on this line and on no other")
}

func TestASuiteThatFailsWithoutNamingAPackageIsUndecided(t *testing.T) {
	rep, err := check(tree(t, map[string]string{"clean": passing("clean")}),
		func(dir string, args ...string) (string, bool) {
			if args[1] == "build" {
				return "", true
			}
			return "signal: killed\n", false
		})
	require.NoError(t, err)
	require.False(t, rep.ok(), "a suite that died is not a pass and is not a catch")
	require.Contains(t, rep.render(), "COULD-NOT-LOOK")
}

// The reaper on #394: COULD-NOT-LOOK named a package and printed nothing else,
// so the only way to learn which test had failed was to reproduce a flake. The
// verdict has to carry what the failing run said, and only what that package
// said: a neighbour's failure printed under this name would send somebody to
// the wrong test.
func TestACouldNotLookVerdictCarriesWhatTheFailingRunPrinted(t *testing.T) {
	root := tree(t, map[string]string{
		"unsteady": flaky("unsteady"),
		"stale":    alwaysFails("stale"),
	})
	require.NoError(t, os.WriteFile(
		filepath.Join(root, moduleDir, "internal", "unsteady", "first-run"), []byte("x"), 0o644))

	rep, err := check(root, shell)
	require.NoError(t, err)
	require.Equal(t, []string{"example/engine/internal/unsteady"}, rep.unclear)

	got := rep.evidence["example/engine/internal/unsteady"]
	require.Contains(t, got, "failed once and will not fail again",
		"the verdict did not carry the failing test's own message")
	require.NotContains(t, got, "pages.gen.go is stale",
		"another package's failure was printed as this one's")
	require.Contains(t, rep.render(), "      pkg_test.go:",
		"the failing run's output was not printed under the package")
	require.Contains(t, rep.render(), "failed once and will not fail again")
}

// readsEECounting is readsEE numbering its attempts, so a report can be held to
// printing the reproduction rather than the first run. Without ee it fails on
// attempt 1, passes with ee on attempt 2, and fails again on attempt 3.
func readsEECounting(name string) string {
	return fmt.Sprintf("package %s\n\nimport (\n\t\"os\"\n\t\"strconv\"\n\t\"testing\"\n)\n\n"+
		"func TestReadsTheEnterpriseTree(t *testing.T) {\n"+
		"\tb, _ := os.ReadFile(\"attempts\")\n"+
		"\tn, _ := strconv.Atoi(string(b))\n"+
		"\tn++\n"+
		"\t_ = os.WriteFile(\"attempts\", []byte(strconv.Itoa(n)), 0o644)\n"+
		"\tif _, err := os.Stat(\"../../../ee/marker.txt\"); err != nil {\n"+
		"\t\tt.Fatalf(\"attempt %%d: the enterprise tree is not there: %%v\", n, err)\n\t}\n}\n", name)
}

// The other red verdict needs the same, and its evidence is the reproduction,
// which ran the package alone, so nothing else on the run can have caused what
// it printed.
func TestADependentVerdictCarriesWhatItsReproductionPrinted(t *testing.T) {
	rep, err := check(tree(t, map[string]string{"reaches": readsEECounting("reaches")}), shell)
	require.NoError(t, err)
	require.Equal(t, []string{"example/engine/internal/reaches"}, rep.dependent)
	require.Contains(t, rep.evidence["example/engine/internal/reaches"], "attempt 3:",
		"the reproduction's output was not the evidence")
	require.Contains(t, rep.render(), "attempt 3: the enterprise tree is not there",
		"the dependent verdict did not say what failed")
}

// A suite that died naming nothing is the case where the output is the only
// clue there is.
func TestASuiteThatDiesWithoutNamingAPackagePrintsWhatItSaid(t *testing.T) {
	rep, err := check(tree(t, map[string]string{"clean": passing("clean")}),
		func(dir string, args ...string) (string, bool) {
			if args[1] == "build" {
				return "", true
			}
			return "panic: test timed out after 15m0s\nsignal: killed\n", false
		})
	require.NoError(t, err)
	require.Contains(t, rep.render(), "panic: test timed out after 15m0s",
		"the dead suite's output was not printed")
}

// A green run prints no evidence section at all, so the section only ever
// means something failed.
func TestAPassingTreePrintsNoEvidence(t *testing.T) {
	rep, err := check(tree(t, map[string]string{"clean": passing("clean")}), shell)
	require.NoError(t, err)
	require.True(t, rep.ok())
	require.NotContains(t, rep.render(), "what go printed",
		"a passing run printed an evidence section")
}

func TestPackageOutputTakesOnlyThatPackagesBlock(t *testing.T) {
	out := strings.Join([]string{
		"--- FAIL: TestA (0.00s)",
		"    a_test.go:9: a is broken",
		"FAIL",
		"FAIL\tgithub.com/x/internal/a\t0.010s",
		"?   \tgithub.com/x/internal/empty\t[no test files]",
		"--- FAIL: TestB (0.00s)",
		"    b_test.go:4: b is broken",
		"FAIL",
		"FAIL\tgithub.com/x/internal/b\t0.020s",
		"ok  \tgithub.com/x/internal/c\t0.030s",
	}, "\n")

	b := packageOutput(out, "github.com/x/internal/b")
	require.Contains(t, b, "b is broken")
	require.Contains(t, b, "FAIL\tgithub.com/x/internal/b")
	require.NotContains(t, b, "a is broken", "the block before it leaked in")
	require.NotContains(t, b, "internal/c", "the block after it leaked in")
	require.NotContains(t, b, "internal/empty", "a package with no test files leaked in")
	require.Contains(t, packageOutput(out, "github.com/x/internal/a"), "a is broken")
	require.Empty(t, packageOutput(out, "github.com/x/internal/missing"))
}

// A goroutine dump must not bury the verdict, and a shortened block must say
// that it was shortened.
func TestLongEvidenceIsCutAndSaysHowMuch(t *testing.T) {
	lines := make([]string, maxEvidenceLines+50)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	rep := report{
		unclear:  []string{"example/p"},
		evidence: map[string]string{"example/p": strings.Join(lines, "\n")},
	}
	got := rep.render()
	require.Contains(t, got, fmt.Sprintf("line %d\n", maxEvidenceLines))
	require.NotContains(t, got, fmt.Sprintf("line %d\n", maxEvidenceLines+1), "the cap was not applied")
	require.Contains(t, got, "(50 further lines not shown)")
}

func writeEEModule(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "ee", "go.mod"),
		[]byte("module example/ee\n\ngo 1.26.0\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "ee", "ee.go"),
		[]byte("package ee\n\n// Name is what the community build must not need.\nconst Name = \"enterprise\"\n"), 0o644))
}

func TestATreeLeftAsideByAnInterruptedRunIsPutBack(t *testing.T) {
	root := tree(t, map[string]string{"clean": passing("clean")})
	require.NoError(t, os.Rename(filepath.Join(root, "ee"), filepath.Join(root, hidden)))

	rep, err := check(root, shell)
	require.NoError(t, err, "a tree left aside by a killed run must be recovered, not reported as missing")
	require.True(t, rep.ok())
	require.DirExists(t, filepath.Join(root, "ee"))
	require.NoDirExists(t, filepath.Join(root, hidden))
}

func TestTwoTreesAreARefusalRatherThanAGuess(t *testing.T) {
	root := tree(t, map[string]string{"clean": passing("clean")})
	require.NoError(t, os.MkdirAll(filepath.Join(root, hidden), 0o755))

	_, err := check(root, shell)
	require.Error(t, err, "which of the two is the real ee is not something this may guess")
	require.Contains(t, err.Error(), "cannot tell which tree is the real one")
}
