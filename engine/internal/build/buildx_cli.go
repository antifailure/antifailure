package build

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"

	"github.com/antifailure/antifailure/engine/internal/redact"
)

// buildxAvailable asks only after the daemon refused a sessionless BuildKit
// call. The CLI is an optional accelerator: machines without it retain the
// daemon API path and its existing legacy fallback.
func buildxAvailable(ctx context.Context) (string, bool) {
	path, err := exec.LookPath("docker")
	if err != nil {
		return "", false
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probe, path, "buildx", "version")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	return path, cmd.Run() == nil
}

// attemptBuildx sends the exact context tar and managed labels used by the
// daemon API. --load is essential: a successful build left only in a Buildx
// cache is not an image the environment can start or teardown can find.
func (b *DockerBuilder) attemptBuildx(
	ctx context.Context, dockerPath string, req Request, opts client.ImageBuildOptions,
	extra map[string]string, ref string,
) (log []string, buildErr error, err error) {
	args := []string{"buildx", "build", "--load", "--progress=plain", "--tag", ref, "--file", opts.Dockerfile}
	if opts.Target != "" {
		args = append(args, "--target", opts.Target)
	}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	for _, key := range sortedKeys(opts.Labels) {
		args = append(args, "--label", key+"="+opts.Labels[key])
	}
	for _, key := range sortedKeys(req.Args) {
		args = append(args, "--build-arg", key+"="+req.Args[key])
	}
	args = append(args, "-")
	cmd := exec.CommandContext(ctx, dockerPath, args...)
	cmd.Stdin = req.Context.tarWith(extra)
	output := &buildxOutput{redactor: b.redactor, progress: req.Progress}
	cmd.Stdout, cmd.Stderr = output, output
	err = cmd.Run()
	output.flush()
	if ctx.Err() != nil {
		return output.lines, nil, ctx.Err()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return output.lines, err, nil
		}
		return output.lines, nil, err
	}
	return output.lines, nil, nil
}

const maxBuildxLineBytes = 1 << 20

// exec may copy stdout and stderr concurrently. Hold one line at a time, then
// redact before it reaches progress or the bounded failure-log tail. A line
// beyond 1 MiB is discarded whole, not printed in unredacted fragments.
type buildxOutput struct {
	mu       sync.Mutex
	partial  []byte
	dropping bool
	lines    []string
	redactor *redact.Redactor
	progress func(string)
}

func (w *buildxOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, value := range p {
		if value == '\n' {
			w.emit()
			continue
		}
		if w.dropping {
			continue
		}
		if len(w.partial) >= maxBuildxLineBytes {
			w.partial = nil
			w.dropping = true
			continue
		}
		w.partial = append(w.partial, value)
	}
	return len(p), nil
}

func (w *buildxOutput) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.partial) > 0 || w.dropping {
		w.emit()
	}
}

func (w *buildxOutput) emit() {
	line := "[build output line exceeded 1 MiB]"
	if !w.dropping {
		line = w.redactor.String(strings.TrimRight(string(w.partial), "\r"))
	}
	w.partial = nil
	w.dropping = false
	if line == "" {
		return
	}
	if len(w.lines) < maxLoggedLines {
		w.lines = append(w.lines, line)
	} else {
		copy(w.lines, w.lines[1:])
		w.lines[len(w.lines)-1] = line
	}
	if w.progress != nil {
		w.progress(line)
	}
}

var _ io.Writer = (*buildxOutput)(nil)
