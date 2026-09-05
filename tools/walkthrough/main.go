// Command walkthrough follows the getting started path and times it.
//
// It is the check nobody writes: every step of the quickstart run in order,
// against a real daemon, from a clean state, with a number beside each one. A
// unit test proves a function works. This proves that the sequence a person is
// told to type actually gets them to a running environment, and says how long
// they wait for it.
//
// It used to stop at "a running environment", which was half the path and the
// less interesting half. The product's claim is that agents drive the
// application and return a verdict with evidence, and nothing in this walked
// that: af runner install, af test and the artifacts a run leaves behind were
// all outside the only check that follows the documented sequence. They are in
// it now, and af test is asserted to have EXAMINED something rather than to
// have exited zero, because a run that reached no workflow at all exits zero
// too.
//
// Usage:
//
//	go run ./tools/walkthrough .                 run it, print the timings
//	go run ./tools/walkthrough . --budget 8m     also fail when it takes longer
//	go run ./tools/walkthrough . --example go-api
//
// It always tears down, including when a step failed, because a walkthrough
// that leaves an environment behind is a walkthrough that fails the second
// time it runs for a reason that has nothing to do with the product.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	budget := flag.Duration("budget", 0, "fail when the whole path takes longer than this")
	example := flag.String("example", "go-api", "which directory under examples/ to walk")
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	if err := walk(root, *example, *budget); err != nil {
		fmt.Fprintln(os.Stderr, "walkthrough:", err)
		os.Exit(1)
	}
}

// step is one thing the quickstart tells somebody to do.
type step struct {
	name string
	run  func(ctx context.Context, w *world) error
}

// world is what the steps share: where the example is, which binary to use,
// and what the environment reported.
type world struct {
	dir string
	af  string
	url string
	// envID is what af up called the environment, which is the directory the
	// artifacts land in.
	envID string
}

func walk(root, example string, budget time.Duration) error {
	dir := filepath.Join(root, "examples", example)
	if _, err := os.Stat(filepath.Join(dir, "antifailure.yaml")); err != nil {
		return fmt.Errorf("no manifest at %s, so there is nothing to walk: %w", dir, err)
	}

	af, err := build(root)
	if err != nil {
		return err
	}
	w := &world{dir: dir, af: af}

	// Teardown is registered before the first step that can create anything,
	// and it runs whatever happens below.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if out, err := w.af_(ctx, "down"); err != nil {
			fmt.Fprintf(os.Stderr, "walkthrough: teardown failed, so something may be left behind: %v\n%s\n", err, out)
		}
	}()

	steps := []step{
		{"af start", func(ctx context.Context, w *world) error {
			// The command the installer points at, run first because it is
			// what a new reader runs first. Exit 3 is a legitimate answer
			// here, so this reads the report rather than the status: it has
			// to name a next command, or somebody who ran it learned where
			// they were and not what to do.
			out, _ := w.af_(ctx, "start")
			if !strings.Contains(out, "Your first run") {
				return fmt.Errorf("af start printed no report:\n%s", out)
			}
			if !strings.Contains(out, "Next") {
				return fmt.Errorf("af start named nothing to run next:\n%s", out)
			}
			return nil
		}},
		{"af doctor", func(ctx context.Context, w *world) error {
			// The first thing the quickstart says to run, and the one that is
			// supposed to explain a machine that cannot do this yet.
			_, err := w.af_(ctx, "doctor")
			return err
		}},
		{"af runner install", func(ctx context.Context, w *world) error {
			// A runner already on this machine is not reinstalled, because
			// this walks a developer's own home directory as well as a fresh
			// CI runner, and clobbering somebody's installed runner mid
			// session is not this command's business.
			//
			// The skip is refused wherever CI is set, for the reason this
			// repository keeps relearning: a skip reads as a pass, and on a
			// runner that starts from nothing there is no honest reason to
			// take it.
			if out, err := w.af_(ctx, "runner", "check"); err == nil {
				if os.Getenv("CI") != "" {
					return fmt.Errorf("the runner is already installed on a CI runner, "+
						"which should start from nothing, so this step would prove nothing:\n%s", out)
				}
				fmt.Println("  (already installed, so this step did not exercise the install)")
				return nil
			}
			_, err := w.af_(ctx, "runner", "install")
			return err
		}},
		{"af runner check", func(ctx context.Context, w *world) error {
			// After the install rather than only before it, because the
			// install is what this asserts: a check that passes only because
			// it ran on a machine that was already set up has checked nothing.
			//
			// Read as JSON rather than as text. The first version matched the
			// words in the rendered report and went red against a runner that
			// was correctly pinned, because the terminal had wrapped the line
			// between "pinned by" and "package-lock.json". A check that reads
			// prose is measuring the wrap.
			out, err := w.af_(ctx, "runner", "check", "-o", "json")
			var report struct {
				Path     string `json:"path"`
				Complete bool   `json:"complete"`
				Checks   []struct {
					Name, Result, Detail string
				} `json:"checks"`
			}
			// The document is read before the exit code is judged, because
			// the report says WHICH check failed and the exit code says only
			// that one did. The message a person gets from this step has to
			// be the one they can act on.
			if err != nil {
				return fmt.Errorf("the runner is not ready after af runner install: %w\n%s", err, out)
			}
			if jsonErr := json.Unmarshal([]byte(out), &report); jsonErr != nil {
				return fmt.Errorf("af runner check did not return a document this can read: %w\n%s",
					jsonErr, out)
			}
			if !report.Complete {
				return fmt.Errorf("af runner check exited 0 and reports the runner incomplete:\n%s", out)
			}
			deps := ""
			for _, c := range report.Checks {
				if c.Name == "dependencies" {
					deps = c.Detail
				}
			}
			if deps == "" {
				return fmt.Errorf("af runner check reported nothing about the dependencies:\n%s", out)
			}
			// The lockfile is what makes two people installing one release get
			// one tree, and a release that stopped shipping it would otherwise
			// pass every check here with a differently resolved set.
			if !strings.Contains(deps, "pinned by package-lock.json") {
				return fmt.Errorf("the runner installed without a lockfile, so its dependencies "+
					"are whatever npm resolved today rather than what this release was tested "+
					"with: %s", deps)
			}
			return runnerStarts(ctx, report.Path)
		}},
		{"af explain", func(ctx context.Context, w *world) error {
			out, err := w.af_(ctx, "explain")
			if err != nil {
				return err
			}
			if !strings.Contains(out, "Egress") {
				return fmt.Errorf("explain did not describe the egress policy, which is the part people read")
			}
			return nil
		}},
		{"af up", func(ctx context.Context, w *world) error {
			out, err := w.af_(ctx, "up", "-o", "json")
			if err != nil {
				return err
			}
			w.url = jsonField(out, "url")
			w.envID = jsonField(out, "env_id")
			if w.url == "" {
				return errors.New("up reported no URL, so there is nothing for a person to open")
			}
			return nil
		}},
		{"open the URL", func(ctx context.Context, w *world) error {
			// The quickstart's whole promise in one line: the address it
			// printed serves the application.
			return reachable(ctx, w.url)
		}},
		{"af status", func(ctx context.Context, w *world) error {
			out, err := w.af_(ctx, "status", "-o", "json")
			if err != nil {
				return err
			}
			if !strings.Contains(out, `"running":true`) && !strings.Contains(out, `"running": true`) {
				return fmt.Errorf("status does not say the environment is running:\n%s", out)
			}
			return nil
		}},
		{"af test", func(ctx context.Context, w *world) error {
			// The exit code is deliberately not the assertion.
			//
			// af test exits 0 on unverified, and blocked does not count
			// against a run, so a run that never reached a single workflow
			// exits 0 exactly like a run where everything passed. That is how
			// an entire nightly corpus was green having never once reached an
			// agent. What is asserted here is that the run EXAMINED the
			// workflows the manifest declares, and that at least one of them
			// reached a real verdict about the application.
			out, testErr := w.af_(ctx, "test", "-o", "json")
			counts, err := verdictCounts(out)
			if err != nil {
				return fmt.Errorf("%w; af test said:\n%s", err, out)
			}
			total := counts["passed"] + counts["failed"] + counts["flaky"] +
				counts["blocked"] + counts["unverified"]
			if total == 0 {
				return fmt.Errorf("af test returned a verdict for no workflow at all, "+
					"which is a green run over nothing:\n%s", out)
			}
			if counts["passed"]+counts["failed"]+counts["flaky"] == 0 {
				return fmt.Errorf("all %d workflows came back blocked or unverified, so this "+
					"run says nothing about the application:\n%s", total, out)
			}
			if testErr != nil && counts["failed"] > 0 {
				return fmt.Errorf("%d of %d workflows failed against the example: %w",
					counts["failed"], total, testErr)
			}
			return testErr
		}},
		{"the evidence", func(_ context.Context, w *world) error {
			// A verdict with no video, trace or console log behind it is an
			// opinion. The product's claim is evidence, so the walkthrough
			// looks for it on disk rather than taking the summary's word.
			if w.envID == "" {
				return errors.New("af up reported no environment id, so there is nowhere to look")
			}
			dir := filepath.Join(w.dir, ".antifailure", "artifacts", w.envID)
			entries, err := os.ReadDir(dir)
			if err != nil {
				return fmt.Errorf("no artifacts under %s, so the run produced a verdict and "+
					"nothing to check it against: %w", dir, err)
			}
			if len(entries) == 0 {
				return fmt.Errorf("%s is empty, so the run produced a verdict and nothing to "+
					"check it against", dir)
			}
			return nil
		}},
		{"af start again", func(ctx context.Context, w *world) error {
			// The resumability claim, checked rather than asserted in a
			// comment: after everything above, the same command reports the
			// environment as running and the evidence as present. A first run
			// somebody walked away from here is one they can walk back into.
			out, _ := w.af_(ctx, "start")
			for _, want := range []string{"an environment", "evidence on disk"} {
				if !strings.Contains(out, want) {
					return fmt.Errorf("af start no longer reports %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "none yet") {
				return fmt.Errorf("af start reports no evidence after af test ran:\n%s", out)
			}
			return nil
		}},
		{"af down", func(ctx context.Context, w *world) error {
			_, err := w.af_(ctx, "down")
			return err
		}},
	}

	fmt.Printf("%-16s %10s\n", "step", "seconds")
	total := time.Duration(0)
	for _, s := range steps {
		// Generous, because this kills a step rather than judging it. A slow
		// machine should produce a number the report can show; --budget is
		// what decides whether that number is acceptable. The first run of
		// this had the limit at fifteen minutes and killed an image build that
		// was making progress, which reported a timeout where the honest
		// answer was "this took a while".
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
		start := time.Now()
		err := s.run(ctx, w)
		took := time.Since(start)
		cancel()

		total += took
		fmt.Printf("%-16s %10.1f\n", s.name, took.Seconds())
		if err != nil {
			return fmt.Errorf("%s failed after %s: %w", s.name, took.Round(time.Second), err)
		}
	}

	fmt.Printf("\nwalkthrough: the getting started path took %s\n", total.Round(time.Second))
	if budget > 0 && total > budget {
		return fmt.Errorf("that is longer than the %s budget", budget)
	}
	return nil
}

// runnerStarts loads the runner af runner check named and confirms node can
// resolve it, which is the one claim that command deliberately does not make.
//
// af runner check reads a directory. It reported ready while the run resolved
// a DIFFERENT directory, so the walkthrough learned about a runner with no
// dependencies three steps later, from inside af test, as
//
//	Error [ERR_MODULE_NOT_FOUND]: Cannot find package 'playwright' imported
//	  from /home/runner/work/antifailure/antifailure/runner/src/browser.ts
//
// which names a package and not the fact that the runner was never installed.
// Starting the runner here costs a process and puts that failure at the step
// whose whole job is to answer the question. It exits immediately for want of
// a job document, so this waits on nothing.
func runnerStarts(ctx context.Context, dir string) error {
	if dir == "" {
		return errors.New("af runner check named no path, so there is nothing to start")
	}
	entry := filepath.Join(dir, "src", "main.ts")
	cmd := exec.CommandContext(ctx, "node", "--experimental-strip-types", entry)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	// A non zero exit is expected: with no job document the runner refuses.
	// What is not expected is node failing to load the program at all, which
	// is what an uninstalled dependency looks like.
	if strings.Contains(string(out), "ERR_MODULE_NOT_FOUND") ||
		strings.Contains(string(out), "Cannot find package") {
		return fmt.Errorf("af runner check called %s ready and node cannot load it, so "+
			"af test would die inside the runner rather than reaching a workflow:\n%s",
			dir, out)
	}
	if err != nil && !strings.Contains(string(out), "job document") {
		return fmt.Errorf("the runner at %s did not start: %w\n%s", dir, err, out)
	}
	return nil
}

// build compiles the binary the walkthrough drives, so it walks this working
// tree rather than whatever `af` happens to be on the path.
func build(root string) (string, error) {
	out := filepath.Join(os.TempDir(), "af-walkthrough")
	cmd := exec.Command("go", "build", "-o", out, "./engine/cmd/af")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building af: %w\n%s", err, b)
	}
	return out, nil
}

// af_ runs the binary in the example directory and returns everything it said.
func (w *world) af_(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, w.af, args...)
	cmd.Dir = w.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("af %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return string(out), nil
}

// reachable asks the address the environment printed for an answer.
//
// It retries, because a service can report ready a moment before the port
// forward is accepting, and a walkthrough that fails on that is measuring the
// harness rather than the product.
func reachable(ctx context.Context, url string) error {
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
			last = fmt.Errorf("%s answered %d", url, resp.StatusCode)
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("%s never answered: %w", url, last)
}

// verdictCounts reads the five verdict totals out of af test's JSON.
//
// Read rather than trusted: a document missing them is reported as an error
// here, because a zero read out of an absent field looks exactly like a zero
// read out of a run that examined nothing, and only one of those is a defect
// in the product.
//
// An error document is separated from a report missing a field, because they
// are different findings and the first version reported both as the second.
// af test refusing before it reached a workflow came back as "returned no
// flaky count", which describes the instrument rather than the failure, and
// buried the engine's own message underneath it.
func verdictCounts(doc string) (map[string]int, error) {
	var failed struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(doc), &failed); err == nil && failed.Code != "" {
		return nil, fmt.Errorf("af test refused before it reached a workflow: %s %s",
			failed.Code, failed.Message)
	}

	var parsed struct {
		Passed     *int `json:"passed"`
		Failed     *int `json:"failed"`
		Flaky      *int `json:"flaky"`
		Blocked    *int `json:"blocked"`
		Unverified *int `json:"unverified"`
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		return nil, fmt.Errorf("af test did not return a document this can read: %w", err)
	}
	fields := map[string]*int{
		"passed": parsed.Passed, "failed": parsed.Failed, "flaky": parsed.Flaky,
		"blocked": parsed.Blocked, "unverified": parsed.Unverified,
	}
	out := map[string]int{}
	for name, value := range fields {
		if value == nil {
			return nil, fmt.Errorf("af test returned no %q count, so this cannot tell a run "+
				"that examined nothing from one that passed", name)
		}
		out[name] = *value
	}
	return out, nil
}

// jsonField reads one string field out of a document, without a struct for a
// shape that is only ever read here.
func jsonField(doc, key string) string {
	needle := `"` + key + `"`
	i := strings.Index(doc, needle)
	if i < 0 {
		return ""
	}
	rest := doc[i+len(needle):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	rest = rest[j+1:]
	k := strings.Index(rest, `"`)
	if k < 0 {
		return ""
	}
	return rest[:k]
}
