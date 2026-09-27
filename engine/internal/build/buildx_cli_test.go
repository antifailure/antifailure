package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	"github.com/antifailure/antifailure/engine/internal/redact"
)

func TestBuildxOutput_RedactsBeforeProgressAndKeepsABoundedTail(t *testing.T) {
	t.Parallel()
	r := redact.New()
	const secret = "correct-horse-battery-staple-9917"
	require.True(t, r.Register(secret))
	var progress []string
	w := &buildxOutput{redactor: r, progress: func(line string) { progress = append(progress, line) }}
	_, err := w.Write([]byte("step 1 " + secret[:15]))
	require.NoError(t, err)
	_, err = w.Write([]byte(secret[15:] + "\n"))
	require.NoError(t, err)
	for i := 0; i < maxLoggedLines+1; i++ {
		_, err = w.Write([]byte("safe line\n"))
		require.NoError(t, err)
	}
	w.flush()
	require.Len(t, progress, maxLoggedLines+2)
	require.NotContains(t, progress[0], secret)
	require.Contains(t, progress[0], redact.Marker)
	require.Len(t, w.lines, maxLoggedLines)
	require.NotContains(t, strings.Join(w.lines, "\n"), secret)
}

func TestBuildxOutput_DropsOversizedLinesWithoutLeakingFragments(t *testing.T) {
	t.Parallel()
	var progress []string
	w := &buildxOutput{redactor: redact.New(), progress: func(line string) { progress = append(progress, line) }}
	_, err := w.Write([]byte(strings.Repeat("x", maxBuildxLineBytes+1) + "\nnext\n"))
	require.NoError(t, err)
	require.Equal(t, []string{"[build output line exceeded 1 MiB]", "next"}, progress)
}

func TestBuildxAvailable_AbsentDockerKeepsTheLegacyPath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	path, ok := (&DockerBuilder{}).buildxAvailable(t.Context())
	require.False(t, ok)
	require.Empty(t, path)
}

func TestBuildxAvailable_RejectsADifferentDaemonBeforeSendingSource(t *testing.T) {
	b := requireBuilder(t)
	dir := t.TempDir()
	docker := filepath.Join(dir, "docker")
	contents := "#!/bin/sh\nif [ \"$1\" = buildx ]; then echo buildx; else echo another-daemon; fi\n"
	require.NoError(t, os.WriteFile(docker, []byte(contents), 0o755))
	t.Setenv("PATH", dir)
	path, ok := b.buildxAvailable(t.Context())
	require.False(t, ok)
	require.Empty(t, path)
}

func TestBuildxSetupFailure_DistinguishesTheBuilderFromTheDockerfile(t *testing.T) {
	t.Parallel()
	require.True(t, buildxSetupFailure([]string{"ERROR: failed to initialize builder: connection refused"}))
	require.True(t, buildxSetupFailure([]string{"ERROR: builder could not start"}))
	require.False(t, buildxSetupFailure([]string{"#4 ERROR: process /bin/sh exited with code 7"}))
	require.False(t, buildxSetupFailure([]string{"#4 RUN curl https://service.test", "#4 ERROR: connection refused"}))
}

func TestAttemptBuildx_BuildsTheManagedImageVisibleToTheDaemon(t *testing.T) {
	b := requireBuilder(t)
	// A user's selected context and builder may be remote. The archive must go
	// to the daemon the SDK selected before these CLI settings are consulted.
	t.Setenv("DOCKER_CONTEXT", "somewhere-that-does-not-exist")
	t.Setenv("BUILDX_BUILDER", "somewhere-that-does-not-exist")
	dockerPath, ok := b.buildxAvailable(t.Context())
	if !ok {
		t.Skip("Docker Buildx CLI is unavailable")
	}
	c := contextFor(t, map[string]string{"app.txt": "from buildx\n"})
	req := Request{
		Service: "web", Context: c, EnvID: "env-buildx-test",
		Dockerfile: "FROM alpine:3.20\nARG FLAVOR\nCOPY app.txt /app.txt\nRUN printf '%s' \"$FLAVOR\" > /flavor\n",
		Args:       map[string]string{"FLAVOR": "mint"},
	}
	ref := ImageRef(req)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = b.cli.ImageRemove(ctx, ref, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	})
	var progress []string
	req.Progress = func(line string) { progress = append(progress, line) }
	opts := client.ImageBuildOptions{
		Dockerfile: generatedDockerfile + ".web",
		Labels:     dockerutil.Managed(dockerutil.KindService, req.EnvID, time.Now()),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	log, buildErr, err := b.attemptBuildx(ctx, dockerPath, req, opts,
		map[string]string{opts.Dockerfile: req.Dockerfile}, ref)
	require.NoError(t, err)
	require.NoError(t, buildErr, strings.Join(log, "\n"))
	require.NotEmpty(t, progress)
	image, err := b.cli.ImageInspect(ctx, ref)
	require.NoError(t, err, "--load must put the image in the daemon the engine uses")
	require.True(t, dockerutil.IsOurs(image.Config.Labels))
	require.Equal(t, req.EnvID, image.Config.Labels[dockerutil.LabelEnv])
	// The generated Dockerfile consumed the context and the build arg. An
	// image inspect alone would miss a success that built the wrong thing.
	run := exec.CommandContext(ctx, dockerPath, "--host", b.cli.DaemonHost(), "run", "--rm", ref,
		"sh", "-c", "cat /app.txt /flavor")
	run.Env = dockerBuildEnv()
	output, err := run.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, "from buildx\nmint", string(output))
}

func TestAttemptBuildx_ReportsDockerfileFailureWithItsOutput(t *testing.T) {
	b := requireBuilder(t)
	dockerPath, ok := b.buildxAvailable(t.Context())
	if !ok {
		t.Skip("Docker Buildx CLI is unavailable")
	}
	c := contextFor(t, map[string]string{
		"Dockerfile": "FROM alpine:3.20\nRUN echo buildx-reason >&2 && exit 7\n",
	})
	req := Request{Service: "web", Context: c, EnvID: "env-buildx-failure"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	log, buildErr, err := b.attemptBuildx(ctx, dockerPath, req,
		client.ImageBuildOptions{Dockerfile: "Dockerfile"}, nil, ImageRef(req))
	require.NoError(t, err)
	require.Error(t, buildErr)
	require.Contains(t, strings.Join(log, "\n"), "buildx-reason")
}
