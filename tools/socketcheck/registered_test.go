package main

import (
	"strings"
	"testing"
)

// The registration direction, made to say no.
//
// Every case below changes one thing about the fixture tree in main_test.go,
// whose one shipped binary registers both of its sockets, and requires the
// problem to name what changed. The two controls come first, in the shape
// engine/pkg/airgap/guarded_test.go set: the instrument has to be able to say
// no to the exact defect it was written for, and it has to not say it about
// the same tree with the defect fixed, because an instrument that reports
// everything is as blind as one that reports nothing.

// builtinPackage is the emulator package's shape: a function that registers
// every built in into whatever registry it is handed, in a package of its own.
const builtinPackage = `package builtin

import "github.com/antifailure/antifailure/engine/pkg/extension"

func RegisterBuiltin(r *extension.Registry) {
	r.AddDatabaseProvider(nil)
}
`

// cliWithout is the fixture binary's registrations with the database provider
// taken out, so the socket is empty unless the case puts something back.
const cliWithout = `package cli

import "github.com/antifailure/antifailure/engine/pkg/extension"

func Run(r *extension.Registry) {
	register(r)
}

func register(r *extension.Registry) {
	r.AddPolicy(nil)
}
`

// TestTheInstrumentCanSayNo is the emulator on 2026-09-11, reduced to its
// shape. The registration function exists, it is correct, and it lives in a
// package no shipped binary imports, so the socket the engine consults is
// empty in every build a customer runs. The consultation direction passes this
// tree, which is exactly what it did on main.
func TestTheInstrumentCanSayNo(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/cli.go", cliWithout)
	write(t, root, "engine/pkg/builtin/builtin.go", builtinPackage)

	got := problems(t, root, nil)
	if !strings.Contains(got, "DatabaseProvider can be registered with AddDatabaseProvider "+
		"and no binary a customer runs does") {
		t.Fatalf("a socket nothing shipped registers was not reported: %q", got)
	}
}

// TestTheUnreachedRegistrationIsNamed is the second half of the same finding.
// Somebody reading it needs to know the registration EXISTS and is simply
// not reached, because "write the emulators" and "call the function that
// registers them" are very different amounts of work.
func TestTheUnreachedRegistrationIsNamed(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/cli.go", cliWithout)
	write(t, root, "engine/pkg/builtin/builtin.go", builtinPackage)

	got := problems(t, root, nil)
	if !strings.Contains(got, "The call that exists is at engine/pkg/builtin/builtin.go:6") {
		t.Fatalf("the unreached registration was not located: %q", got)
	}
}

// TestARegistrationAShippedBinaryReachesIsNotAFinding is the same tree plus the
// one line PR 320 adds, emulator.RegisterBuiltin(extension.Default), in its
// reduced form. It has to pass, or the instrument is only reporting that a
// package exists.
func TestARegistrationAShippedBinaryReachesIsNotAFinding(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/cli.go", strings.Replace(
		strings.Replace(cliWithout,
			`import "github.com/antifailure/antifailure/engine/pkg/extension"`,
			"import (\n\t\"github.com/antifailure/antifailure/engine/pkg/builtin\"\n"+
				"\t\"github.com/antifailure/antifailure/engine/pkg/extension\"\n)", 1),
		"\tr.AddPolicy(nil)\n", "\tr.AddPolicy(nil)\n\tbuiltin.RegisterBuiltin(r)\n", 1))
	write(t, root, "engine/pkg/builtin/builtin.go", builtinPackage)

	if got := problems(t, root, nil); got != "" {
		t.Fatalf("a registration a shipped binary reaches was reported: %s", got)
	}
}

// TestARegistrationInAFunctionNothingCallsIsReported is the same defect one
// package closer. Importing the package is not enough; the function holding
// the registration has to be reachable from main, or it is RegisterBuiltin
// again with a different name.
func TestARegistrationInAFunctionNothingCallsIsReported(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/cli.go", cliWithout+`
func registerAll(r *extension.Registry) {
	r.AddDatabaseProvider(nil)
}
`)
	got := problems(t, root, nil)
	if !strings.Contains(got, "DatabaseProvider can be registered with AddDatabaseProvider "+
		"and no binary a customer runs does") {
		t.Fatalf("a registration in a function nothing calls was counted: %q", got)
	}
}

// TestARegistrationOnlyATestMakesIsNotARegistration is the reason tests are
// not read: a test registering into the socket proves the registry works and
// says nothing about any binary.
func TestARegistrationOnlyATestMakesIsNotARegistration(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/cli.go", cliWithout)
	write(t, root, "engine/internal/cli/cli_test.go", `package cli

import "github.com/antifailure/antifailure/engine/pkg/extension"

func init() { extension.Default.AddDatabaseProvider(nil) }
`)
	got := problems(t, root, nil)
	if !strings.Contains(got, "DatabaseProvider can be registered") {
		t.Fatalf("a registration only a test makes was counted: %q", got)
	}
}

// TestAFileNamedForAnUnreleasedPlatformIsNotRead: the fixture's release
// matrix builds darwin and linux, so a registration in a _windows.go file is
// one no customer receives.
func TestAFileNamedForAnUnreleasedPlatformIsNotRead(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/cli.go", strings.Replace(cliWithout,
		"\tr.AddPolicy(nil)\n", "\tr.AddPolicy(nil)\n\tregisterPlatform(r)\n", 1))
	write(t, root, "engine/internal/cli/register_windows.go", `package cli

import "github.com/antifailure/antifailure/engine/pkg/extension"

func registerPlatform(r *extension.Registry) { r.AddDatabaseProvider(nil) }
`)
	got := problems(t, root, nil)
	if !strings.Contains(got, "DatabaseProvider can be registered") {
		t.Fatalf("a registration only a Windows build makes was counted: %q", got)
	}

	// And the same file for a platform the release does build is read.
	write(t, root, "engine/internal/cli/register_windows.go", "package cli\n")
	write(t, root, "engine/internal/cli/register_linux.go", `package cli

import "github.com/antifailure/antifailure/engine/pkg/extension"

func registerPlatform(r *extension.Registry) { r.AddDatabaseProvider(nil) }
`)
	if got := problems(t, root, nil); got != "" {
		t.Fatalf("a registration a Linux release makes was not counted: %s", got)
	}
}

// TestAFileABuildLineExcludesIsNotRead: the same question asked through a
// build constraint rather than through a file name.
func TestAFileABuildLineExcludesIsNotRead(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/cli.go", strings.Replace(cliWithout,
		"\tr.AddPolicy(nil)\n", "\tr.AddPolicy(nil)\n\tregisterPlatform(r)\n", 1))
	write(t, root, "engine/internal/cli/register.go", `//go:build ignore

package cli

import "github.com/antifailure/antifailure/engine/pkg/extension"

func registerPlatform(r *extension.Registry) { r.AddDatabaseProvider(nil) }
`)
	got := problems(t, root, nil)
	if !strings.Contains(got, "DatabaseProvider can be registered") {
		t.Fatalf("a registration in a file no build compiles was counted: %q", got)
	}
}

// TestARegistrationInAnImportedPackagesInitCounts is a control. A package
// imported for its side effect runs its init in the binary whether or not
// anything names it, so that registration is real and must count.
func TestARegistrationInAnImportedPackagesInitCounts(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/cli.go", strings.Replace(cliWithout,
		`import "github.com/antifailure/antifailure/engine/pkg/extension"`,
		"import (\n\t_ \"github.com/antifailure/antifailure/engine/pkg/builtin\"\n"+
			"\t\"github.com/antifailure/antifailure/engine/pkg/extension\"\n)", 1))
	write(t, root, "engine/pkg/builtin/builtin.go", `package builtin

import "github.com/antifailure/antifailure/engine/pkg/extension"

func init() { extension.Default.AddDatabaseProvider(nil) }
`)
	if got := problems(t, root, nil); got != "" {
		t.Fatalf("a registration an imported init makes was not counted: %s", got)
	}
}

// TestARegistrationInAPackageLevelInitializerCounts is a control for the other
// thing that runs before main.
func TestARegistrationInAPackageLevelInitializerCounts(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/cli.go", cliWithout+`
var registered = registerAll(nil)

func registerAll(r *extension.Registry) bool {
	r.AddDatabaseProvider(nil)
	return true
}
`)
	if got := problems(t, root, nil); got != "" {
		t.Fatalf("a registration a package level initializer makes was not counted: %s", got)
	}
}

// TestADeliberatelyEmptySocketWithAReasonPasses is the golden store's case: the
// shipped behaviour is a built in switch, the socket is for somebody's own
// build, and saying so with a reason is what the list is for.
func TestADeliberatelyEmptySocketWithAReasonPasses(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/cli.go", cliWithout)
	l := fixture(nil)
	l.notRegistered = map[string]string{"DatabaseProvider": "the built in switch is the shipped path"}

	if got := problemsWith(t, root, l); got != "" {
		t.Fatalf("a deliberately empty socket with a reason was reported: %s", got)
	}
}

// TestAnExemptionWithNoReasonIsReported: an entry is a reason written down,
// and an entry with nothing written is a silence with a key.
func TestAnExemptionWithNoReasonIsReported(t *testing.T) {
	t.Parallel()
	for _, blank := range []string{"", "   "} {
		root := tree(t, goodExtension, goodEngine)
		write(t, root, "engine/internal/cli/cli.go", cliWithout)
		l := fixture(nil)
		l.notRegistered = map[string]string{"DatabaseProvider": blank}

		got := problemsWith(t, root, l)
		if !strings.Contains(got, "DatabaseProvider is listed in socketcheck's notRegistered with no reason") {
			t.Fatalf("an exemption with the reason %q was accepted: %q", blank, got)
		}
	}
}

// TestAnExemptionThatOutlivesItsGapIsReported is what keeps the list from
// rotting: the moment a shipped binary registers the socket, the entry has to
// go, exactly as the lane that registered the audit sinks had to delete
// AuditSink from notConsulted.
func TestAnExemptionThatOutlivesItsGapIsReported(t *testing.T) {
	t.Parallel()
	l := fixture(nil)
	l.notRegistered = map[string]string{"DatabaseProvider": "a reason that has stopped being true"}

	got := problemsWith(t, tree(t, goodExtension, goodEngine), l)
	if !strings.Contains(got, "DatabaseProvider is listed in socketcheck as registered by no "+
		"shipped binary and engine/cmd/af registers it at engine/internal/cli/cli.go:11. Delete the entry") {
		t.Fatalf("an exemption that outlived its gap was accepted: %q", got)
	}
}

// TestAnExemptionNamingNoSocketIsReported: a socket renamed out from under its
// entry leaves an entry that can never be found stale, because there is
// nothing for it to go stale against.
func TestAnExemptionNamingNoSocketIsReported(t *testing.T) {
	t.Parallel()
	l := fixture(nil)
	l.notRegistered = map[string]string{"EmulatorProvider": "a reason about a socket that was renamed"}

	got := problemsWith(t, tree(t, goodExtension, goodEngine), l)
	if !strings.Contains(got, "EmulatorProvider is listed in socketcheck's notRegistered and is "+
		"not a socket") {
		t.Fatalf("an exemption naming no socket was accepted: %q", got)
	}
}

// TestANotConsultedEntryNamingNoSocketIsReported holds the older list to the
// same rule, which it did not have: an entry for a socket that no longer
// exists passed silently.
func TestANotConsultedEntryNamingNoSocketIsReported(t *testing.T) {
	t.Parallel()
	got := problems(t, tree(t, goodExtension, goodEngine),
		map[string]string{"AuditHook": "a reason about a socket that was renamed"})
	if !strings.Contains(got, "AuditHook is listed in socketcheck's notConsulted and is not a socket") {
		t.Fatalf("a notConsulted entry naming no socket was accepted: %q", got)
	}
}

// TestANotConsultedEntryWithNoReasonIsReported: and the older list's blank
// reason, which it also accepted.
func TestANotConsultedEntryWithNoReasonIsReported(t *testing.T) {
	t.Parallel()
	engine := strings.Replace(goodEngine, `	_, _ = r.DatabaseProviderNamed("docker")`, "", 1)
	got := problems(t, tree(t, goodExtension, engine), map[string]string{"DatabaseProvider": ""})
	if !strings.Contains(got, "DatabaseProvider is listed in socketcheck's notConsulted with no reason") {
		t.Fatalf("a notConsulted entry with no reason was accepted: %q", got)
	}
}

// TestAMainPackageNobodyClassifiedIsReported: whether a binary ships decides
// whether its registrations count, so a new one has to be named before the
// gate will say anything about it.
func TestAMainPackageNobodyClassifiedIsReported(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/cmd/extra/main.go", "package main\n\nfunc main() {}\n")

	got := problems(t, root, nil)
	if !strings.Contains(got, "engine/cmd/extra is a main package socketcheck does not classify") {
		t.Fatalf("an unclassified main package was accepted: %q", got)
	}

	// Named as not shipped, it passes and the report says it was not read.
	l := fixture(nil)
	l.notShipped = map[string]string{"engine/cmd/extra": "a fixture nobody runs"}
	report, err := check(root, l)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 0 || !strings.Contains(report.String(),
		"not read, because no customer runs it: engine/cmd/extra: a fixture nobody runs") {
		t.Fatalf("a main package named as not shipped was not reported as unread:\n%s", report.String())
	}
}

// TestAShippedEntryNamingNoMainPackageIsReported: a binary deleted out from
// under its entry would otherwise leave a list claiming a customer runs
// something that no longer exists.
func TestAShippedEntryNamingNoMainPackageIsReported(t *testing.T) {
	t.Parallel()
	l := fixture(nil)
	l.shipped["engine/cmd/gone"] = "a binary that was deleted"

	got := problemsWith(t, tree(t, goodExtension, goodEngine), l)
	if !strings.Contains(got, "engine/cmd/gone is listed in socketcheck's shipped and there is "+
		"no main package there") {
		t.Fatalf("a shipped entry naming no main package was accepted: %q", got)
	}
}

// TestANotShippedEntryNamingNoMainPackageIsReported: the same, for the other
// list.
func TestANotShippedEntryNamingNoMainPackageIsReported(t *testing.T) {
	t.Parallel()
	l := fixture(nil)
	l.notShipped = map[string]string{"engine/cmd/gone": "a tool that was deleted"}

	got := problemsWith(t, tree(t, goodExtension, goodEngine), l)
	if !strings.Contains(got, "engine/cmd/gone is listed in socketcheck's notShipped and there is "+
		"no main package there") {
		t.Fatalf("a notShipped entry naming no main package was accepted: %q", got)
	}
}

// TestAMethodSharingTheRegistrationNameIsReported: without the type checker a
// registration is recognised by its method's name, which is only sound while
// nothing else in the binary declares that name.
func TestAMethodSharingTheRegistrationNameIsReported(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/builder.go", `package cli

type builder struct{}

func (builder) AddPolicy(n int) {}
`)
	got := problems(t, root, nil)
	if !strings.Contains(got, "engine/internal/cli/builder.go:5 declares builder.AddPolicy") {
		t.Fatalf("a method sharing a registration's name was accepted: %q", got)
	}
}

// checkErr runs the check and returns its error, which is how a tree the gate
// cannot read has to come back: as a failure, never as a pass over less.
func checkErr(t *testing.T, root string) string {
	t.Helper()
	_, err := check(root, fixture(nil))
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestAReleaseWorkflowWithNoMatrixStopsTheCheck: with no platforms, no file
// builds anywhere and every socket would read as empty, which is a finding
// about the gate dressed as one about the product.
func TestAReleaseWorkflowWithNoMatrixStopsTheCheck(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, ".github/workflows/release.yml", "jobs:\n  build:\n    runs-on: ubuntu-latest\n")

	if got := checkErr(t, root); !strings.Contains(got, "has no build matrix entry") {
		t.Fatalf("a release workflow with no matrix did not stop the check: %q", got)
	}
}

// TestAnImportThatCannotBeFollowedStopsTheCheck: a package the walk cannot
// read could hold the registration, so it is a failure to check rather than
// a package to skip.
func TestAnImportThatCannotBeFollowedStopsTheCheck(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/internal/cli/gone.go", `package cli

import _ "github.com/antifailure/antifailure/engine/internal/gone"
`)
	if got := checkErr(t, root); !strings.Contains(got, "cannot be followed") {
		t.Fatalf("an import with no package behind it did not stop the check: %q", got)
	}
}

// TestADotImportStopsTheCheck: a bare name in a file that dot imports could be
// a function from either package, so what it calls cannot be followed.
func TestADotImportStopsTheCheck(t *testing.T) {
	t.Parallel()
	root := tree(t, goodExtension, goodEngine)
	write(t, root, "engine/pkg/builtin/builtin.go", builtinPackage)
	write(t, root, "engine/internal/cli/dot.go", `package cli

import . "github.com/antifailure/antifailure/engine/pkg/builtin"

var _ = RegisterBuiltin
`)
	if got := checkErr(t, root); !strings.Contains(got, "dot imports") {
		t.Fatalf("a dot import did not stop the check: %q", got)
	}
}
