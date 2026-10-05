// Command wintest runs the engine's tests on native Windows and refuses to call
// a run green that checked too little to mean it.
//
// Two things about a Windows runner make a plain `go test ./...` an instrument
// that cannot say no. GitHub's Windows machines run Docker in Windows
// containers mode, so every test that needs a Linux container fails against
// it, about 150 of them, which buries the failures that are about Windows. And
// the obvious cure, skipping those tests, is the cure that hides everything:
// a run that skipped its way to green looks identical to one that passed.
//
// So this decides the Docker question from the daemon itself rather than from
// the platform, says which way it went, counts every skip and prints why, and
// fails a run whose passing tests fall below a floor. Everything it leaves out
// is listed below with the reason, in code a reviewer reads, rather than in a
// flag somebody can quietly widen.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// minimumPassed is the fewest passing tests a run may report and still be
// green, counting top level tests and not their subtests.
//
// Measured on windows-latest on 2026-10-05, with Docker out of the picture and
// the host Postgres the workflow starts: 4909 passed, 280 skipped, none failed
// (run 37271153613). The floor is about 3 per cent under that, which is room
// for a few tests to be retired or moved, and far less than losing any sizable
// package: internal/cli alone defines 664. A run below it has lost something
// whole, a package that stopped building or a Postgres that never started,
// and a green summary of that run would be the failure this command exists to
// prevent.
const minimumPassed = 4750

// excludedPackages are not run at all, with the reason. Each is code that
// never executes on a Windows host, so its tests describe another platform.
var excludedPackages = map[string]string{
	"github.com/antifailure/antifailure/engine/cmd/af-proxy": "" +
		"the egress sidecar only ever runs inside the environment's Linux " +
		"container, and its tests assume Linux: /tmp, and a refused " +
		"connection reported as ECONNREFUSED. It is compiled for Windows by " +
		"the vet step, which is all a Windows host ever does with it.",
}

// pendingTests are skipped by name until the code they exercise supports
// Windows, with the reason. A name here is a known gap, not a pass, and it is
// printed on every run so it cannot be forgotten.
var pendingTests = map[string]string{
	"TestSelfUpdateVerifiedArchive|TestSelfUpdateDoesNotDowngradeOrReinstall|" +
		"TestUpdateRecoveryAcrossCommitBoundaries|TestRecoveryClearsItsOwnStagingDirectory|" +
		"TestUpdateLockReleasesWhenTheHandleCloses|TestUpdateSweepsAnAbandonedStage": "" +
		"af update refuses on Windows today, because replacing a running .exe " +
		"needs the running image moved aside first. These tests exercise the " +
		"replacement and come off this list when af update supports Windows.",
}

// dockerMode is what the daemon on this machine can do for the tests.
type dockerMode struct {
	env    []string
	reason string
}

// decideDocker asks the daemon what kind of containers it runs. Only a Linux
// daemon can serve the engine's tests: a Windows containers daemon answers
// every Linux image with "no matching manifest", which is a fact about the
// runner rather than about the code under test. Pointing DOCKER_HOST at a pipe
// nothing listens on makes every Docker test take the same no-daemon skip it
// takes on a laptop with Docker stopped, and AF_SKIP_DOCKER is the
// acknowledgement the suites demand before they accept that skip.
func decideDocker(osType string, err error) dockerMode {
	switch {
	case err != nil:
		return dockerMode{
			env:    []string{"AF_SKIP_DOCKER=1"},
			reason: "no Docker daemon answered (" + err.Error() + "), so tests that need one skip",
		}
	case osType == "linux":
		return dockerMode{
			env:    []string{"AF_REQUIRE_DOCKER=1"},
			reason: "the daemon runs Linux containers, so every Docker test runs and none may skip",
		}
	default:
		return dockerMode{
			env: []string{
				"DOCKER_HOST=npipe:////./pipe/af_wintest_no_linux_daemon",
				"AF_SKIP_DOCKER=1",
			},
			reason: "the daemon runs " + osType + " containers, which cannot run the Linux images " +
				"these tests need, so it is hidden from them and they skip as they would with no daemon",
		}
	}
}

func dockerOSType() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.OSType}}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// event is one line of `go test -json`.
type event struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// result is what a run amounted to.
type result struct {
	passed, skipped int
	failed          []string
	brokenPackages  []string
	skipReasons     map[string]int
	failureOutput   map[string]string
}

var skipLine = regexp.MustCompile(`^\s+\S+_test\.go:\d+: (.*)$`)

// summarise reads a `go test -json` stream. A package that fails without a
// failing test, which is a build failure, a panic in TestMain or a timeout,
// is counted separately, because it is the shape that takes every test in the
// package with it and reports none of them.
func summarise(r io.Reader) (result, error) {
	res := result{skipReasons: map[string]int{}, failureOutput: map[string]string{}}
	output := map[string][]string{}
	failedTests := map[string]bool{}
	subOutput := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var e event
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		key := e.Package + " " + e.Test
		switch e.Action {
		case "output":
			output[key] = append(output[key], e.Output)
		case "pass":
			if e.Test != "" && !strings.Contains(e.Test, "/") {
				res.passed++
			}
		case "skip":
			if e.Test != "" && !strings.Contains(e.Test, "/") {
				res.skipped++
				res.skipReasons[skipReason(output[key])]++
			}
		case "fail":
			if e.Test == "" {
				if !packageHadFailingTest(failedTests, e.Package) {
					res.brokenPackages = append(res.brokenPackages, e.Package)
					res.failureOutput[key] = tail(output[key], 30)
				}
				continue
			}
			failedTests[key] = true
			if parent, _, sub := strings.Cut(e.Test, "/"); sub {
				// A subtest's assertion is printed under the subtest, so the
				// parent's own output says only that it failed. Carried up, or
				// the report names the test and hides why.
				pk := e.Package + " " + parent
				subOutput[pk] += tail(output[key], 30)
				continue
			}
			res.failed = append(res.failed, key)
			res.failureOutput[key] = subOutput[key] + tail(output[key], 30)
		}
	}
	return res, sc.Err()
}

func packageHadFailingTest(failed map[string]bool, pkg string) bool {
	for k := range failed {
		if strings.HasPrefix(k, pkg+" ") {
			return true
		}
	}
	return false
}

func skipReason(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if m := skipLine.FindStringSubmatch(strings.TrimRight(lines[i], "\r\n")); m != nil {
			return m[1]
		}
	}
	return "(no reason given)"
}

func tail(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "")
}

// verdict turns a result into the lines to print and whether the run passed.
func verdict(res result, floor int) (string, bool) {
	var b strings.Builder
	ok := true
	fmt.Fprintf(&b, "wintest: %d passed, %d skipped, %d failed, %d packages broken\n",
		res.passed, res.skipped, len(res.failed), len(res.brokenPackages))
	if len(res.skipReasons) > 0 {
		b.WriteString("\nWhy tests skipped, most common first:\n")
		type kv struct {
			reason string
			n      int
		}
		var all []kv
		for r, n := range res.skipReasons {
			all = append(all, kv{r, n})
		}
		sort.Slice(all, func(i, j int) bool {
			if all[i].n != all[j].n {
				return all[i].n > all[j].n
			}
			return all[i].reason < all[j].reason
		})
		for _, x := range all {
			fmt.Fprintf(&b, "  %5d  %s\n", x.n, x.reason)
		}
	}
	for _, p := range res.brokenPackages {
		ok = false
		fmt.Fprintf(&b, "\nBROKEN PACKAGE %s\n%s", p, res.failureOutput[p+" "])
	}
	for _, f := range res.failed {
		ok = false
		fmt.Fprintf(&b, "\nFAIL %s\n%s", f, res.failureOutput[f])
	}
	if res.passed < floor {
		ok = false
		fmt.Fprintf(&b, "\nOnly %d tests passed, below the floor of %d. A run that lost whole packages "+
			"is not a green run, whatever else it reports.\n", res.passed, floor)
	}
	return b.String(), ok
}

func skipPattern() string {
	var names []string
	for k := range pendingTests {
		names = append(names, k)
	}
	sort.Strings(names)
	return "^(" + strings.Join(names, "|") + ")$"
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if err := run(root); err != nil {
		fmt.Fprintln(os.Stderr, "wintest:", err)
		os.Exit(1)
	}
}

func run(root string) error {
	engine := filepath.Join(root, "engine")

	osType, dockerErr := dockerOSType()
	mode := decideDocker(osType, dockerErr)
	fmt.Println("wintest: Docker:", mode.reason)
	for pkg, why := range excludedPackages {
		fmt.Printf("wintest: not run: %s, because %s\n", pkg, why)
	}
	for names, why := range pendingTests {
		fmt.Printf("wintest: skipped by name until supported: %s, because %s\n", names, why)
	}

	list := exec.Command("go", "list", "./...")
	list.Dir = engine
	list.Stderr = os.Stderr
	out, err := list.Output()
	if err != nil {
		return fmt.Errorf("listing engine packages: %w", err)
	}
	var pkgs []string
	for _, p := range strings.Fields(string(out)) {
		if _, skip := excludedPackages[p]; !skip {
			pkgs = append(pkgs, p)
		}
	}
	for p := range excludedPackages {
		if !strings.Contains(string(out), p) {
			return fmt.Errorf("%s is excluded and no longer exists; remove the exclusion", p)
		}
	}

	args := append([]string{"test", "-json", "-count=1", "-timeout=60m", "-skip", skipPattern()}, pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = engine
	cmd.Env = append(os.Environ(), mode.env...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	res, readErr := summarise(stdout)
	waitErr := cmd.Wait()
	if readErr != nil {
		return fmt.Errorf("reading go test output: %w", readErr)
	}
	report, ok := verdict(res, minimumPassed)
	fmt.Print(report)
	if !ok {
		return fmt.Errorf("the Windows run is not green")
	}
	if waitErr != nil {
		// go test failing with nothing the summary could attribute it to is
		// still a failure, and the one most worth not swallowing.
		return fmt.Errorf("go test exited with %w and the summary found no failure to blame", waitErr)
	}
	return nil
}
