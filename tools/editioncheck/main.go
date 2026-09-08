// Command editioncheck proves the community tree does not need the enterprise
// edition, and says why it is red when it is red.
//
// The property is real and worth a gate. A stray Go import of `ee` is already
// a compile error, because `ee/engine` is a separate module outside the
// workspace, so the interesting violation is the one the compiler cannot see:
// a package under `engine` that reads a path inside `ee` at test time, or a
// generator that only works because those files happen to be present. The way
// to catch that is to take `ee` away and run the community suite.
//
// The failure that produced this tool is what happens on the next line. The
// step used to be `rm -rf ee`, then `go build ./...`, then `go test
// ./internal/... -short`, and it reported every one of those as "edition
// boundary". On the night of 2026-09-08, 13 of 25 open pull requests were red
// on this required context and NOT ONE of them was an edition violation. Nine
// distinct engine test failures accounted for all thirteen: a stale
// `pages.gen.go`, a stale `sources.gen.go`, a schema drift, and six emulator
// and egress suites. Every one of them failed identically with `ee` present,
// and the `engine` job was red on all thirteen for the same reason. Thirteen
// lanes were told to look for an enterprise import that was never there.
//
// A gate that fails for a reason it did not test is worse than a gate that
// does not run, because it spends somebody's night on the wrong file. So this
// runs the suite twice when it has to. A package that fails with `ee` gone and
// fails the same way with `ee` present has said nothing about the edition
// boundary: that is the engine job's finding, and this reports it as such and
// passes. A package that PASSES with `ee` present and fails without it is the
// violation this gate exists for, and it is refused.
//
// It can still say no, which is the whole point, and `main_test.go` proves it
// against both shapes: a package that fails either way, which must not be
// called an edition violation, and a package that reads a file under `ee`,
// which must be. A third verdict exists for the case the instrument could not
// decide: a package that fails without `ee`, passes with it, and then passes
// again without it is flaky rather than dependent, and this says so and fails
// rather than accusing it or waving it through.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
)

// hidden is where `ee` is moved while the community suite runs. A rename keeps
// the tree restorable when this is run on a developer's checkout, which
// `rm -rf ee` did not: the CI step it replaces relied on the workspace being
// thrown away afterwards, and the same command in the justfile would have
// deleted a contributor's enterprise source.
const hidden = ".ee-hidden-by-editioncheck"

// The community module and what the community build actually compiles and
// runs. `./internal/...` rather than `./...` mirrors the step this replaces.
const (
	moduleDir = "engine"
	testScope = "./internal/..."
)

// buildPseudoPackage names the compile step in a report, so that "the tree
// does not build without ee" travels through the same classification as a
// failing package instead of being a separate branch nobody reads.
const buildPseudoPackage = "(go build " + moduleDir + ")"

// swap moves ee aside and puts it back, and is safe to call from the signal
// handler as well as from the run.
type swap struct {
	mu     sync.Mutex
	ee     string
	away   string
	hidden bool
}

func (s *swap) hide() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hidden {
		return nil
	}
	if err := os.Rename(s.ee, s.away); err != nil {
		return err
	}
	s.hidden = true
	return nil
}

func (s *swap) restore() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hidden {
		return nil
	}
	if err := os.Rename(s.away, s.ee); err != nil {
		return err
	}
	s.hidden = false
	return nil
}

// recover puts back a tree left aside by a run that died before it could. A
// previous run that was killed between the two renames is the one state a
// developer cannot be expected to recognise, and leaving it means the next
// run reports "no ee directory" about a tree that has one.
func (s *swap) recover() error {
	_, hiddenErr := os.Stat(s.away)
	if hiddenErr != nil {
		return nil
	}
	if _, err := os.Stat(s.ee); err == nil {
		return fmt.Errorf("both %s and %s exist, so a previous run left something behind "+
			"and this cannot tell which tree is the real one. Look before deleting either", s.ee, s.away)
	}
	if err := os.Rename(s.away, s.ee); err != nil {
		return fmt.Errorf("a previous run left %s aside and it could not be put back: %w", s.ee, err)
	}
	fmt.Fprintf(os.Stderr, "a previous run left %s aside and it has been put back\n", s.ee)
	return nil
}

// runner runs a command in a directory under the repository root and returns
// its combined output and whether it succeeded. It is a parameter so that the
// classification can be driven against a fixture tree by the tests.
type runner func(dir string, args ...string) (string, bool)

// report is what the run decided, per package.
type report struct {
	// dependent fails without ee and passes with it. The violation.
	dependent []string
	// unrelated fails both ways. Not this gate's finding.
	unrelated []string
	// unclear could not be decided, which is neither a pass nor a catch.
	unclear []string
}

func (r report) ok() bool { return len(r.dependent) == 0 && len(r.unclear) == 0 }

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if args := flag.Args(); len(args) > 0 {
		*root = args[0]
	}

	rep, err := check(*root, shell)
	if err != nil {
		fmt.Fprintf(os.Stderr, "editioncheck could not run: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(rep.render())
	if !rep.ok() {
		os.Exit(1)
	}
}

// shell is the real runner.
func shell(dir string, args ...string) (string, bool) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	// The enterprise module is outside the workspace and the community build
	// must not be handed a workspace that could resolve it. GOFLAGS is cleared
	// so an ambient one cannot quietly change what is compiled.
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// check hides ee, runs the community build and suite, and attributes every
// failure it finds. ee is restored before it returns, on every path.
func check(root string, run runner) (report, error) {
	sw := &swap{ee: filepath.Join(root, "ee"), away: filepath.Join(root, hidden)}
	if err := sw.recover(); err != nil {
		return report{}, err
	}
	if _, err := os.Stat(sw.ee); err != nil {
		return report{}, fmt.Errorf("no ee directory at %s, so nothing was taken away and nothing was proved: %w", sw.ee, err)
	}
	if err := sw.hide(); err != nil {
		return report{}, fmt.Errorf("could not take ee away: %w", err)
	}
	// A run that is interrupted must not leave somebody's enterprise source
	// under a dotted name. This is why the tree is moved rather than deleted,
	// and the deletion is what the step this replaces did: on a runner that is
	// thrown away it costs nothing, and the same command in the justfile would
	// have destroyed a contributor's working copy.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() {
		if _, open := <-sig; !open {
			return
		}
		_ = sw.restore()
		fmt.Fprintf(os.Stderr, "\ninterrupted, and %s was put back\n", sw.ee)
		os.Exit(1)
	}()
	restore := sw.restore
	hide := sw.hide
	defer func() { _ = restore() }()

	mod := filepath.Join(root, moduleDir)

	// Without ee. A build failure is classified through the same path as a
	// test failure, because "it does not compile without ee" and "it does not
	// compile at all" are the same two answers and only one of them is ours.
	var failed []string
	if _, ok := run(mod, "go", "build", "./..."); !ok {
		failed = append(failed, buildPseudoPackage)
	} else {
		out, ok := run(mod, "go", "test", testScope, "-short", "-count=1", "-timeout", "15m")
		if !ok {
			failed = failedPackages(out)
			if len(failed) == 0 {
				// The suite said no and named nothing. That is an instrument
				// that could not look, not a pass.
				return report{unclear: []string{"the community suite failed without naming a package"}}, nil
			}
		}
	}
	if len(failed) == 0 {
		return report{}, nil
	}

	// With ee. Only the packages that already failed, so the second run costs
	// what the failure costs rather than what the suite costs.
	if err := restore(); err != nil {
		return report{}, fmt.Errorf("could not put ee back: %w", err)
	}
	withEE := map[string]bool{}
	if failed[0] == buildPseudoPackage {
		if _, ok := run(mod, "go", "build", "./..."); !ok {
			withEE[buildPseudoPackage] = true
		}
	} else {
		args := append([]string{"go", "test"}, failed...)
		args = append(args, "-short", "-count=1", "-timeout", "15m")
		out, ok := run(mod, args...)
		if !ok {
			for _, pkg := range failedPackages(out) {
				withEE[pkg] = true
			}
		}
	}

	var rep report
	for _, pkg := range failed {
		if withEE[pkg] {
			rep.unrelated = append(rep.unrelated, pkg)
			continue
		}
		// Accused. Before naming a package as reaching into ee, reproduce it:
		// a test that fails once without ee and passes with it is as likely to
		// be flaky as dependent, and this gate must not turn a flake into an
		// edition violation.
		if err := hide(); err != nil {
			return report{}, fmt.Errorf("could not take ee away for the second look: %w", err)
		}
		var again bool
		if pkg == buildPseudoPackage {
			_, ok := run(mod, "go", "build", "./...")
			again = !ok
		} else {
			_, ok := run(mod, "go", "test", pkg, "-short", "-count=1", "-timeout", "15m")
			again = !ok
		}
		if err := restore(); err != nil {
			return report{}, fmt.Errorf("could not put ee back: %w", err)
		}
		if again {
			rep.dependent = append(rep.dependent, pkg)
		} else {
			rep.unclear = append(rep.unclear, pkg)
		}
	}
	sort.Strings(rep.dependent)
	sort.Strings(rep.unrelated)
	sort.Strings(rep.unclear)
	return rep, nil
}

// failedPackages reads the package names out of a `go test` run.
//
// It reads the per package summary line rather than the lines a failing test
// prints, because a package can fail without any test failing: `[build
// failed]` and `[setup failed]` both arrive on the summary line and on no
// other, and a gate that only counted failing tests would call a package that
// could not compile a pass.
func failedPackages(out string) []string {
	seen := map[string]bool{}
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "FAIL\t") && !strings.HasPrefix(line, "FAIL ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(fields[1], "/") {
			continue
		}
		if seen[fields[1]] {
			continue
		}
		seen[fields[1]] = true
		pkgs = append(pkgs, fields[1])
	}
	sort.Strings(pkgs)
	return pkgs
}

// render says which question was answered and which was not, in the words
// somebody reading a red required context needs.
func (r report) render() string {
	var b strings.Builder
	if len(r.unrelated) > 0 {
		b.WriteString("These packages fail with ee present and with ee absent, so they say\n")
		b.WriteString("nothing about the edition boundary. They are the engine job's finding:\n")
		for _, p := range r.unrelated {
			fmt.Fprintf(&b, "  %s\n", p)
		}
		b.WriteString("Fix them there. This gate is not what is telling you about them.\n\n")
	}
	if len(r.unclear) > 0 {
		b.WriteString("COULD-NOT-LOOK. These failed without ee, passed with it, and then\n")
		b.WriteString("passed again without it, so this cannot tell a flake from a package\n")
		b.WriteString("that reaches into ee:\n")
		for _, p := range r.unclear {
			fmt.Fprintf(&b, "  %s\n", p)
		}
		b.WriteString("\n")
	}
	if len(r.dependent) > 0 {
		b.WriteString("These pass with ee present and fail with ee absent, so the community\n")
		b.WriteString("build needs the enterprise edition, which it does not have:\n")
		for _, p := range r.dependent {
			fmt.Fprintf(&b, "  %s\n", p)
		}
		b.WriteString("\n")
	}
	if r.ok() && len(r.unrelated) == 0 {
		b.WriteString("the community tree builds and passes with ee taken away\n")
	} else if r.ok() {
		b.WriteString("the edition boundary holds: nothing here needed ee\n")
	}
	return b.String()
}
