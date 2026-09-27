package build

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"

	"github.com/antifailure/antifailure/engine/internal/redact"
)

// buildxAvailable asks only after the daemon refused a sessionless BuildKit
// call. Before handing the CLI a source archive, prove that its explicit host
// reaches the same daemon the engine will run images on. The CLI's selected
// context and Buildx builder are not evidence of that: either may be remote.
func (b *DockerBuilder) buildxAvailable(ctx context.Context) (string, bool) {
	path, err := exec.LookPath("docker")
	if err != nil {
		return "", false
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probe, path, "buildx", "version")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.Env = dockerBuildEnv()
	if cmd.Run() != nil {
		return "", false
	}
	info, err := b.cli.Info(probe, client.InfoOptions{})
	if err != nil || info.Info.ID == "" {
		return "", false
	}
	cmd = exec.CommandContext(probe, path, "--host", b.cli.DaemonHost(), "info", "--format", "{{.ID}}")
	cmd.Env = dockerBuildEnv()
	cliID, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(cliID)) != info.Info.ID {
		return "", false
	}
	return path, true
}

// An explicit --host chooses the engine's endpoint; stripping context and
// builder selectors prevents a user's unrelated remote builder from receiving
// the build context. `docker build` (unlike `docker buildx build`) uses that
// daemon's bundled default builder when no builder override is supplied.
func dockerBuildEnv() []string {
	blocked := map[string]bool{
		"DOCKER_HOST": true, "DOCKER_CONTEXT": true,
		"DOCKER_DEFAULT_PLATFORM": true, "BUILDX_BUILDER": true,
		"BUILDKIT_HOST": true,
	}
	var out []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[strings.ToUpper(key)] {
			out = append(out, entry)
		}
	}
	return out
}

// attemptBuildx sends the exact context tar and managed labels used by the
// daemon API. --load is essential: a successful build left only in a Buildx
// cache is not an image the environment can start or teardown can find.
func (b *DockerBuilder) attemptBuildx(
	ctx context.Context, dockerPath string, req Request, opts client.ImageBuildOptions,
	extra map[string]string, ref string,
) (log []string, buildErr error, err error) {
	args := []string{"--host", b.cli.DaemonHost(), "build", "--load", "--progress=plain", "--tag", ref, "--file", opts.Dockerfile}
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
	cmd.Env = dockerBuildEnv()
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
			if buildxSetupFailure(output.lines) {
				return output.lines, nil, err
			}
			return output.lines, err, nil
		}
		return output.lines, nil, err
	}
	return output.lines, nil, nil
}

// CLI/builder setup failures and exits before any build step get a second try
// with the legacy daemon builder. A Dockerfile error is returned with its
// Buildx output instead of spending minutes rebuilding the same service.
func buildxSetupFailure(lines []string) bool {
	output := strings.ToLower(strings.Join(lines, "\n"))
	for _, message := range []string{
		"cannot connect to the docker daemon",
		"failed to initialize builder",
		"failed to bootstrap builder",
		"failed to find driver",
		"no builder instance found",
	} {
		if strings.Contains(output, message) {
			return true
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			return false
		}
	}
	// A CLI that exits before starting a build has not disproven the image;
	// the legacy daemon path is still able to try it.
	return true
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
