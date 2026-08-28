// Command walkthrough follows the getting started path and times it.
//
// It is the check nobody writes: every step of the quickstart run in order,
// against a real daemon, from a clean state, with a number beside each one. A
// unit test proves a function works. This proves that the sequence a person is
// told to type actually gets them to a running environment, and says how long
// they wait for it.
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
		{"af doctor", func(ctx context.Context, w *world) error {
			// The first thing the quickstart says to run, and the one that is
			// supposed to explain a machine that cannot do this yet.
			_, err := w.af_(ctx, "doctor")
			return err
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
