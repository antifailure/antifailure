package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// The runner is a separate program in a separate language, and installing it
// is the one rough edge between a checkout and a working check.
//
// It is a copy rather than a download, because the source that ships with a
// release is the source that release was tested with. Fetching it separately
// would let the two drift, and a runner one version ahead of the engine that
// speaks to it is a failure nobody can read.

// RunnerHome is where the runner is installed.
func RunnerHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".antifailure", "runner"), nil
}

// RunnerCheckJSON is the machine readable result of af runner check.
//
// Per check rather than one boolean, because "complete: false" was all a
// caller got and it does not say whether node is missing, the dependencies are,
// or there is no runner at all, which are three different things to do next.
type RunnerCheckJSON struct {
	Path     string                `json:"path"`
	Node     string                `json:"node"`
	Complete bool                  `json:"complete"`
	Checks   []RunnerCheckItemJSON `json:"checks"`
}

// RunnerCheckItemJSON is one question and its answer.
type RunnerCheckItemJSON struct {
	Name   string `json:"name"`
	Result string `json:"result"` // ok, fail, warn or skip
	Detail string `json:"detail"`
	Remedy string `json:"remedy,omitempty"`
}

// RunnerInstallJSON is the machine readable result.
//
// Dependencies says how they were resolved rather than only that they were,
// because "installed" is true of both a pinned tree and one npm resolved from
// the ^ ranges this morning, and those are different trees.
type RunnerInstallJSON struct {
	Path         string `json:"path"`
	Files        int    `json:"files"`
	Node         string `json:"node"`
	Browser      string `json:"browser"`
	Dependencies string `json:"dependencies"` // locked or unlocked
	Complete     bool   `json:"complete"`
}

func newRunnerCommand(e *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runner",
		Short: "Install and check the agent runner",
		Long: strings.TrimSpace(`
The runner drives a real browser, so it is a separate program in a separate
language. It is installed from a copy that ships with this engine rather than
downloaded, because the source a release was tested with is the source that
release should run.`),
	}
	cmd.AddCommand(newRunnerInstallCommand(e))
	cmd.AddCommand(newRunnerCheckCommand(e))
	return cmd
}

func newRunnerInstallCommand(e *Env) *cobra.Command {
	var from string
	var skipBrowser bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Put the runner where af test will find it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			source, err := runnerSource(e, from)
			if err != nil {
				return err
			}
			target, err := RunnerHome()
			if err != nil {
				return aferrors.Wrap(err, aferrors.AFAGT004, "detail", err.Error())
			}

			e.Out.Section("Installing the runner")
			copied, err := copyTree(source, target)
			if err != nil {
				return aferrors.Wrap(err, aferrors.AFAGT004,
					"detail", fmt.Sprintf("copying %s to %s: %v", source, target, err))
			}
			e.Out.Printf("  %d files copied to %s\n", copied, target)

			// The dependencies and the browser are downloaded here rather than
			// on the first run, because the first run is somebody's pull
			// request check and a two minute download inside it looks like a
			// hang.
			locked, err := installDependencies(cmd.Context(), e, target)
			if err != nil {
				return err
			}

			browser := "skipped"
			if !skipBrowser {
				if err := runIn(cmd.Context(), target, "npx", "playwright", "install", "chromium"); err != nil {
					// Not fatal. The runner is installed and usable the moment
					// a browser arrives, and a failed download here should not
					// undo the rest.
					e.Out.Printf("  %s the browser could not be downloaded: %v\n",
						e.Out.S(StyleWarn, SymbolWarn), err)
					browser = "failed"
				} else {
					browser = "chromium"
					e.Out.Println("  chromium installed")
				}
			}

			node := nodeVersion(cmd.Context())
			if e.Out.Format == FormatJSON {
				return e.Out.JSON(RunnerInstallJSON{
					Path: target, Files: copied, Node: node,
					Browser: browser, Dependencies: lockState(locked),
					Complete: browser == "chromium",
				})
			}
			e.Out.Printf("\n  Ready. %s will find it without a flag.\n", e.Out.S(StyleBold, "af test"))
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Copy from this directory rather than the one beside the engine")
	cmd.Flags().BoolVar(&skipBrowser, "skip-browser", false, "Do not download the browser")
	return cmd
}

// installDependencies resolves the runner's dependencies, and reports whether
// the tree it produced is the one the release was tested with.
//
// `npm install` was what this ran, and it resolves the ^ ranges in package.json
// afresh every time. playwright ^1.49.0 was 1.49.0 when the runner was written
// and is whatever npm serves today, so two people installing one release of
// Antifailure got two different browsers driving their tests, and a failure one
// of them saw was not reproducible by the other. `npm ci` installs exactly what
// package-lock.json names and refuses if the lockfile and the manifest
// disagree, which is the whole point of shipping the lockfile.
//
// The fallback is deliberately loud rather than silent. A source tree with no
// lockfile still installs, because refusing would strand somebody pointing
// --from at a checkout they are editing, but it says the tree is not pinned
// instead of printing the same "dependencies installed" line for both. A
// message that reads the same whether or not the guarantee holds is how nobody
// finds out it stopped holding.
func installDependencies(ctx context.Context, e *Env, target string) (locked bool, err error) {
	lockfile := filepath.Join(target, "package-lock.json")
	if _, statErr := os.Stat(lockfile); statErr == nil {
		if runErr := runIn(ctx, target, "npm", "ci", "--no-audit", "--no-fund"); runErr != nil {
			return false, aferrors.Wrap(runErr, aferrors.AFAGT004,
				"detail", "npm ci failed: "+runErr.Error())
		}
		e.Out.Println("  dependencies installed, pinned by package-lock.json")
		return true, nil
	}
	if runErr := runIn(ctx, target, "npm", "install", "--no-audit", "--no-fund"); runErr != nil {
		return false, aferrors.Wrap(runErr, aferrors.AFAGT004,
			"detail", "npm install failed: "+runErr.Error())
	}
	e.Out.Printf("  %s dependencies installed, and NOT pinned: %s has no package-lock.json,\n"+
		"    so npm resolved the version ranges in package.json as they are today. Another\n"+
		"    machine installing the same release can get a different tree.\n",
		e.Out.S(StyleWarn, SymbolWarn), target)
	return false, nil
}

// lockState is the JSON spelling of the same fact.
func lockState(locked bool) string {
	if locked {
		return "locked"
	}
	return "unlocked"
}

// runnerCheck is one question this command can answer about the runner, and
// the answer it got.
//
// The remedy travels with the finding rather than being printed once at the
// end, because the old command printed "Install it with: af runner install"
// under every failure, including a missing node, where installing again does
// nothing.
type runnerCheck struct {
	label   string
	symbol  string
	detail  string
	remedy  string
	blocker bool // a false answer here means af test cannot run at all
}

// checkRunner answers what it can about an installed runner without executing
// any of it.
//
// The version this replaced stat'ed src/main.ts and called that "ok runner".
// install.sh put the runner SOURCE at that path, so on every machine installed
// with curl | sh this command reported ok on a tree with no node_modules, and
// the real failure surfaced four steps later inside af test as a node error
// about a module it could not resolve. A check somebody runs specifically to
// find out whether the thing works must fail on that tree.
//
// So it reads the runner's own package.json and asks the three questions that
// tree fails, plus the browser, which it reports separately because af runner
// install treats a failed browser download as non fatal and af test returns
// unverified rather than a wrong answer without one.
//
// What it deliberately does NOT claim: that the runner executes. Knowing that
// needs node, a process, and a browser launch, which is what af test is. The
// browser line says "not checked" rather than "ok" wherever the cache location
// cannot be determined, because answering ok about something unexamined is the
// defect being fixed rather than a smaller version of it.
func checkRunner(ctx context.Context, target string) []runnerCheck {
	out := []runnerCheck{}

	entry := filepath.Join(target, "src", "main.ts")
	if _, err := os.Stat(entry); err != nil {
		return append(out, runnerCheck{
			label: "runner", symbol: SymbolFail,
			detail:  "no runner at " + target,
			remedy:  "Install it with: af runner install",
			blocker: true,
		})
	}
	out = append(out, runnerCheck{label: "runner", symbol: SymbolOK, detail: target})

	manifest, manifestErr := readRunnerManifest(filepath.Join(target, "package.json"))
	if manifestErr != nil {
		out = append(out, runnerCheck{
			label: "dependencies", symbol: SymbolSkip,
			detail:  "not checked: " + manifestErr.Error(),
			blocker: false,
		})
	} else {
		out = append(out, dependencyCheck(target, manifest))
	}

	out = append(out, nodeCheck(nodeVersion(ctx), manifest))
	out = append(out, browserCheck())
	return out
}

// runnerManifest is the part of the runner's package.json this reads. Its own
// manifest rather than a constant here, so the requirement cannot drift from
// the thing that declares it.
type runnerManifest struct {
	Engines struct {
		Node string `json:"node"`
	} `json:"engines"`
	Dependencies map[string]string `json:"dependencies"`
}

func readRunnerManifest(path string) (runnerManifest, error) {
	var m runnerManifest
	blob, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("package.json could not be read")
	}
	if err := json.Unmarshal(blob, &m); err != nil {
		return m, fmt.Errorf("package.json could not be parsed")
	}
	return m, nil
}

// dependencyCheck is the one that would have caught the reproduced tree.
//
// Every declared dependency has to have a directory under node_modules. The
// directory existing on its own is not enough: an interrupted npm install
// leaves one behind, and a check that stops at "node_modules is there" is the
// same shape of lie as the one that stopped at "src/main.ts is there".
func dependencyCheck(target string, m runnerManifest) runnerCheck {
	modules := filepath.Join(target, "node_modules")
	if _, err := os.Stat(modules); err != nil {
		return runnerCheck{
			label: "dependencies", symbol: SymbolFail,
			detail:  "node_modules is missing",
			remedy:  "Install them with: af runner install",
			blocker: true,
		}
	}
	var missing []string
	for name := range m.Dependencies {
		// filepath.Join handles the scoped @scope/name form, which is two
		// directory levels on disk rather than one.
		if _, err := os.Stat(filepath.Join(modules, filepath.FromSlash(name))); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return runnerCheck{
			label: "dependencies", symbol: SymbolFail,
			detail:  "missing " + strings.Join(missing, ", "),
			remedy:  "Install them with: af runner install",
			blocker: true,
		}
	}
	// Present is not the same as pinned, and this command exists because
	// "present" was being reported as readiness once already. A tree installed
	// from a release archive that shipped no lockfile has every dependency and
	// no idea which versions they are, so it says so rather than reporting the
	// same ok as a pinned one. A warning rather than a failure: the runner does
	// run, it just runs something nobody chose.
	if _, err := os.Stat(filepath.Join(target, "package-lock.json")); err != nil {
		return runnerCheck{
			label: "dependencies", symbol: SymbolWarn,
			detail: fmt.Sprintf("%d declared, all present, and not pinned: no package-lock.json",
				len(m.Dependencies)),
			remedy: "Reinstall from a release that ships the lockfile: af runner install",
		}
	}
	return runnerCheck{
		label: "dependencies", symbol: SymbolOK,
		detail: fmt.Sprintf("%d declared, all present, pinned by package-lock.json", len(m.Dependencies)),
	}
}

// nodeCheck compares against the range the runner declares rather than only
// reporting what it found. The old line printed the version and called it ok,
// so a node too old to run the runner passed a check named after readiness.
func nodeCheck(found string, m runnerManifest) runnerCheck {
	want := m.Engines.Node
	if found == "" {
		detail := "not found"
		if want != "" {
			detail = "not found; the runner needs " + want
		}
		return runnerCheck{
			label: "node", symbol: SymbolFail, detail: detail,
			remedy:  "Install node from https://nodejs.org, then run af runner check again.",
			blocker: true,
		}
	}
	if want == "" {
		return runnerCheck{label: "node", symbol: SymbolOK, detail: found}
	}
	ok, comparable := nodeSatisfies(found, want)
	if !comparable {
		// An unparsed range is reported as unparsed. Treating it as satisfied
		// would put this back where it started.
		return runnerCheck{
			label: "node", symbol: SymbolSkip,
			detail: found + ", against an unreadable requirement of " + want,
		}
	}
	if !ok {
		return runnerCheck{
			label: "node", symbol: SymbolFail,
			detail:  found + ", and the runner needs " + want,
			remedy:  "Upgrade node to " + want + ", then run af runner check again.",
			blocker: true,
		}
	}
	return runnerCheck{label: "node", symbol: SymbolOK, detail: found + ", which satisfies " + want}
}

// nodeSatisfies handles the one range shape the runner declares, ">=x.y[.z]".
// Anything else reports as not comparable rather than as satisfied, because a
// range this cannot read is a question it did not answer.
func nodeSatisfies(found, want string) (ok bool, comparable bool) {
	spec, hasPrefix := strings.CutPrefix(strings.TrimSpace(want), ">=")
	if !hasPrefix {
		return false, false
	}
	got, gotOK := parseVersion(strings.TrimPrefix(found, "v"))
	min, minOK := parseVersion(strings.TrimSpace(spec))
	if !gotOK || !minOK {
		return false, false
	}
	for i := range got {
		if got[i] != min[i] {
			return got[i] > min[i], true
		}
	}
	return true, true
}

// parseVersion reads major, minor and patch, defaulting the parts a range like
// ">=22.6" leaves out.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// browserCheck looks for the chromium af runner install downloads.
//
// Not a blocker. af runner install treats a failed browser download as non
// fatal on purpose, and a workflow that needs a page read comes back
// unverified rather than guessed at, so a missing browser degrades the answer
// instead of preventing one. It is still worth naming here, because finding
// out from a run full of unverified verdicts is finding out late.
func browserCheck() runnerCheck {
	dir, known := playwrightBrowsers()
	if !known {
		return runnerCheck{
			label: "browser", symbol: SymbolSkip,
			detail: "not checked: this platform's browser cache location is not known here",
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return runnerCheck{
			label: "browser", symbol: SymbolWarn,
			detail: "no chromium in " + dir,
			remedy: "Download it with: af runner install",
		}
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "chromium") {
			return runnerCheck{label: "browser", symbol: SymbolOK, detail: e.Name()}
		}
	}
	return runnerCheck{
		label: "browser", symbol: SymbolWarn,
		detail: "no chromium in " + dir,
		remedy: "Download it with: af runner install",
	}
}

// playwrightBrowsers is where playwright puts what it downloads. Its own
// environment variable wins, because a machine that sets it has moved them.
func playwrightBrowsers() (string, bool) {
	if p := os.Getenv("PLAYWRIGHT_BROWSERS_PATH"); p != "" {
		return p, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Caches", "ms-playwright"), true
	case "linux":
		return filepath.Join(home, ".cache", "ms-playwright"), true
	default:
		return "", false
	}
}

func newRunnerCheckCommand(e *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Say whether the runner can run",
		Long: strings.TrimSpace(`
Reports each thing af test needs from the runner separately: the source, the
dependencies it declares, a node new enough to run it, and the browser.

It does not claim the runner executes. Knowing that means starting node and
launching a browser, which is what af test is. Anything this cannot determine
is reported as not checked rather than as ok, because a check that answers ok
about something it never examined is worse than one that admits the gap: this
command used to report "ok runner" whenever src/main.ts existed, which was true
of an install with no dependencies at all, and the real failure surfaced much
later inside af test as a node error about a module it could not resolve.`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := RunnerHome()
			if err != nil {
				return err
			}
			results := checkRunner(cmd.Context(), target)

			ready := true
			node := ""
			for _, r := range results {
				if r.blocker && r.symbol != SymbolOK {
					ready = false
				}
				if r.label == "node" && r.symbol != SymbolFail {
					node = strings.SplitN(r.detail, ",", 2)[0]
				}
			}

			if e.Out.Format == FormatJSON {
				return e.Out.JSON(RunnerCheckJSON{
					Path: target, Node: node, Complete: ready,
					Checks: checksJSON(results),
				})
			}
			for _, r := range results {
				e.Out.Status(r.symbol, r.label, r.detail)
			}
			var remedies []string
			for _, r := range results {
				if r.symbol != SymbolOK && r.symbol != SymbolSkip && r.remedy != "" {
					remedies = append(remedies, r.remedy)
				}
			}
			if len(remedies) > 0 {
				e.Out.Println("")
				for _, r := range remedies {
					e.Out.Printf("  %s\n", r)
				}
			}
			if !ready {
				// The trailing hint used to print unconditionally, and the
				// commonest failure of all is a machine with no runner, whose
				// only remedy is that same sentence. So the first command a
				// new install runs answered with "Install it with: af runner
				// install" twice, one line apart. It is printed now only when
				// nothing above already said it, which is the case where a
				// blocker carries no remedy of its own.
				if !namesRunnerInstall(remedies) {
					e.Out.Println("")
					e.Out.Hint("Install it with", "af runner install")
				}
				return &silentError{code: aferrors.ExitConfiguration}
			}
			return nil
		},
	}
}

// namesRunnerInstall reports whether a remedy already told the reader to run
// the command the closing hint would.
func namesRunnerInstall(remedies []string) bool {
	for _, r := range remedies {
		if strings.Contains(r, "af runner install") {
			return true
		}
	}
	return false
}

func checksJSON(results []runnerCheck) []RunnerCheckItemJSON {
	items := make([]RunnerCheckItemJSON, 0, len(results))
	for _, r := range results {
		items = append(items, RunnerCheckItemJSON{
			Name: r.label, Result: r.symbol, Detail: r.detail, Remedy: r.remedy,
		})
	}
	return items
}

// runnerSource finds the runner to copy.
func runnerSource(e *Env, from string) (string, error) {
	candidates := []string{from}
	if from == "" {
		candidates = nil
		// Beside the working directory, for a checkout.
		candidates = append(candidates,
			filepath.Join(e.WorkDir, "runner"),
			filepath.Join(e.WorkDir, "..", "runner"))
		// Beside the binary, for an installed release, which ships the runner
		// next to it rather than fetching it.
		if self, err := os.Executable(); err == nil {
			dir := filepath.Dir(self)
			candidates = append(candidates,
				filepath.Join(dir, "runner"),
				filepath.Join(dir, "..", "share", "antifailure", "runner"))
		}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(c, "src", "main.ts")); err == nil {
			return c, nil
		}
	}
	return "", aferrors.Coded(aferrors.AFAGT004,
		"detail", "no runner source was found; looked in "+strings.Join(candidates, ", "))
}

// copyTree copies the runner's source, and nothing it can rebuild.
func copyTree(source, target string) (int, error) {
	if err := os.MkdirAll(target, 0o755); err != nil {
		return 0, err
	}
	copied := 0
	err := filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			// node_modules is reinstalled rather than copied, because it holds
			// platform specific binaries and copying one machine's to another
			// is how somebody gets a browser that will not start.
			if d.Name() == "node_modules" || d.Name() == "test-results" {
				return fs.SkipDir
			}
			if rel == "." {
				return nil
			}
			return os.MkdirAll(filepath.Join(target, rel), 0o755)
		}
		if err := copyFile(path, filepath.Join(target, rel)); err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, err
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	return err
}

func runIn(ctx context.Context, dir, name string, args ...string) error {
	c, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(c, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := string(out)
		if len(tail) > 800 {
			tail = tail[len(tail)-800:]
		}
		// Wrapped, not formatted. The caller decides what to do from the
		// error underneath, and a %s here turns an exec.ExitError into a
		// string that errors.As can no longer see.
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(tail))
	}
	return nil
}

func nodeVersion(ctx context.Context) string {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, "node", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
