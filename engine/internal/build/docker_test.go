package build

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/clock"
	"github.com/antifailure/antifailure/engine/internal/dockerutil"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

func requireBuilder(t *testing.T) *DockerBuilder {
	t.Helper()
	if os.Getenv("AF_SKIP_DOCKER") != "" {
		t.Skip("skipped: AF_SKIP_DOCKER is set")
	}
	b, err := NewDockerBuilder(DockerOptions{Clock: clock.New()})
	if err != nil {
		t.Skipf("skipped: no Docker daemon is reachable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := b.cli.Ping(ctx); err != nil {
		_ = b.Close()
		t.Skipf("skipped: the Docker daemon did not respond: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// buildAndClean builds a request and removes whatever image it produced,
// including when the assertion under test is what failed.
func buildAndClean(t *testing.T, b *DockerBuilder, req Request) (Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	res, err := b.Build(ctx, req)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		_, _ = b.cli.ImageRemove(c, ImageRef(req), image.RemoveOptions{Force: true, PruneChildren: true})
	})
	return res, err
}

func contextFor(t *testing.T, files map[string]string) *Context {
	t.Helper()
	c, err := NewContext(ContextOptions{Root: tree(t, files), Service: "web"})
	require.NoError(t, err)
	return c
}

func TestDockerBuilder_BuildsFromADockerfileInTheRepository(t *testing.T) {
	b := requireBuilder(t)
	c := contextFor(t, map[string]string{
		"Dockerfile": "FROM alpine:3.20\nCOPY hello.txt /hello.txt\nCMD [\"cat\", \"/hello.txt\"]\n",
		"hello.txt":  "hello from the context\n",
	})

	var progress []string
	res, err := buildAndClean(t, b, Request{
		Service: "web", Context: c, EnvID: "env-build-1",
		Progress: func(l string) { progress = append(progress, l) },
	})
	require.NoError(t, err)
	require.False(t, res.Cached)
	require.NotEmpty(t, progress, "the build reports progress as it goes")
	require.Positive(t, res.Duration)

	insp, _, err := b.cli.ImageInspectWithRaw(context.Background(), res.ImageRef)
	require.NoError(t, err)
	require.True(t, dockerutil.IsOurs(insp.Config.Labels),
		"the image carries the managed label, or the leak detector cannot find it")
	require.Equal(t, "env-build-1", insp.Config.Labels[dockerutil.LabelEnv])
}

func TestDockerBuilder_SecondBuildOfTheSameContentIsACacheHit(t *testing.T) {
	b := requireBuilder(t)
	c := contextFor(t, map[string]string{
		"Dockerfile": "FROM alpine:3.20\nRUN echo one > /one\n",
	})
	req := Request{Service: "web", Context: c, EnvID: "env-build-2"}

	first, err := buildAndClean(t, b, req)
	require.NoError(t, err)
	require.False(t, first.Cached)

	second, err := b.Build(context.Background(), req)
	require.NoError(t, err)
	require.True(t, second.Cached, "nothing changed, so nothing is built")
	require.Zero(t, second.Duration)
	require.Equal(t, first.ImageRef, second.ImageRef)
}

func TestDockerBuilder_BuildsAGeneratedDockerfile(t *testing.T) {
	b := requireBuilder(t)
	// The buildpack path: the repository has no Dockerfile, and the generated
	// one is injected into the context rather than written to the user's disk.
	c := contextFor(t, map[string]string{"app.txt": "generated build\n"})

	res, err := buildAndClean(t, b, Request{
		Service: "web", Context: c, EnvID: "env-build-3",
		Dockerfile: "FROM alpine:3.20\nCOPY app.txt /app.txt\nCMD [\"cat\", \"/app.txt\"]\n",
	})
	require.NoError(t, err)
	require.False(t, res.Cached)

	// And the user's repository is untouched.
	_, statErr := os.Stat(c.Root + "/Dockerfile")
	require.Error(t, statErr, "nothing was written into the repository")
}

func TestDockerBuilder_ReportsAFailingBuildWithTheOutputThatExplainsIt(t *testing.T) {
	b := requireBuilder(t)
	c := contextFor(t, map[string]string{
		"Dockerfile": "FROM alpine:3.20\nRUN echo the-reason-it-failed >&2 && exit 3\n",
	})

	res, err := buildAndClean(t, b, Request{Service: "worker", Context: c, EnvID: "env-build-4"})
	require.Error(t, err)

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Equal(t, aferrors.AFBLD001, coded.Code())
	require.Contains(t, coded.Message(), "worker", "the message names the service that failed")
	require.NotEmpty(t, res.Log, "the output is kept, because it is the only thing that explains the failure")
	require.Contains(t, strings.Join(res.Log, "\n"), "the-reason-it-failed")
}

func TestDockerBuilder_ReportsAnUnusableDockerfile(t *testing.T) {
	b := requireBuilder(t)
	c := contextFor(t, map[string]string{"Dockerfile": "THIS IS NOT A DOCKERFILE\n"})

	_, err := buildAndClean(t, b, Request{Service: "web", Context: c, EnvID: "env-build-5"})
	require.Error(t, err)
	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
}

func TestDockerBuilder_BuildArgsChangeTheImage(t *testing.T) {
	b := requireBuilder(t)
	c := contextFor(t, map[string]string{
		"Dockerfile": "FROM alpine:3.20\nARG FLAVOUR=none\nRUN echo $FLAVOUR > /flavour\n",
	})
	one := Request{Service: "web", Context: c, EnvID: "e", Args: map[string]string{"FLAVOUR": "one"}}
	two := Request{Service: "web", Context: c, EnvID: "e", Args: map[string]string{"FLAVOUR": "two"}}
	require.NotEqual(t, ImageRef(one), ImageRef(two),
		"a build argument that changes the image must change what the cache keys on")

	resOne, err := buildAndClean(t, b, one)
	require.NoError(t, err)
	resTwo, err := buildAndClean(t, b, two)
	require.NoError(t, err)
	require.NotEqual(t, resOne.ImageRef, resTwo.ImageRef)
}

func TestDockerBuilder_TargetSelectsAStage(t *testing.T) {
	b := requireBuilder(t)
	c := contextFor(t, map[string]string{
		"Dockerfile": "FROM alpine:3.20 AS early\nRUN echo early > /stage\n" +
			"FROM early AS late\nRUN echo late > /stage\n",
	})
	res, err := buildAndClean(t, b, Request{
		Service: "web", Context: c, EnvID: "env-build-6", Target: "early",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.ImageRef)
}

// The proof that matters for the buildpacks: a generated Dockerfile has to
// build a real application, not merely look plausible.
func TestBuildpacks_GenerateSomethingThatActuallyBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in short mode: these pull real language base images")
	}
	b := requireBuilder(t)

	cases := []struct {
		name    string
		files   map[string]string
		command string
		port    int
	}{
		{
			name: "go",
			files: map[string]string{
				"go.mod":  "module example.com/svc\n\ngo 1.23\n",
				"main.go": "package main\n\nfunc main() { println(\"ok\") }\n",
			},
			port: 8080,
		},
		{
			name: "node",
			files: map[string]string{
				"package.json":      `{"name":"svc","version":"1.0.0","scripts":{"start":"node server.js"}}`,
				"package-lock.json": `{"name":"svc","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"svc","version":"1.0.0"}}}`,
				"server.js":         "console.log('ok')\n",
			},
			port: 3000,
		},
		{
			name: "python",
			files: map[string]string{
				"requirements.txt": "",
				"main.py":          "print('ok')\n",
			},
			command: "python main.py",
			port:    8000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := contextFor(t, tc.files)
			bp, ok := DetectBuildpack(c, "", tc.command, tc.port)
			require.True(t, ok, "no buildpack matched")
			require.Equal(t, tc.name, bp.Name)

			res, err := buildAndClean(t, b, Request{
				Service: tc.name, Context: c, EnvID: "env-buildpack", Dockerfile: bp.Dockerfile,
			})
			if err != nil {
				t.Fatalf("the generated Dockerfile does not build:\n%s\n\n%s",
					bp.Dockerfile, strings.Join(res.Log, "\n"))
			}
			require.NotEmpty(t, res.ImageRef)
		})
	}
}

func TestImageRef_IsStableAndDistinguishes(t *testing.T) {
	t.Parallel()
	c := contextFor(t, map[string]string{"a.txt": "a"})
	base := Request{Service: "web", Context: c}

	require.Equal(t, ImageRef(base), ImageRef(base), "the same request names the same image")
	require.True(t, strings.HasPrefix(ImageRef(base), ImageRepo+":web-"))

	changed := []Request{
		{Service: "worker", Context: c},
		{Service: "web", Context: c, Dockerfile: "FROM alpine\n"},
		{Service: "web", Context: c, DockerfilePath: "docker/Dockerfile"},
		{Service: "web", Context: c, Target: "runtime"},
		{Service: "web", Context: c, Args: map[string]string{"A": "1"}},
	}
	for _, r := range changed {
		require.NotEqual(t, ImageRef(base), ImageRef(r),
			"anything that changes the image must change what it is called")
	}

	other := contextFor(t, map[string]string{"a.txt": "b"})
	require.NotEqual(t, ImageRef(base), ImageRef(Request{Service: "web", Context: other}))
}

func TestImageRef_ArgumentOrderDoesNotMatter(t *testing.T) {
	t.Parallel()
	c := contextFor(t, map[string]string{"a.txt": "a"})
	// Go map iteration is randomised, so an unsorted hash would produce a
	// different tag on some runs and the cache would miss for no reason
	// anybody could reproduce.
	first := ImageRef(Request{Service: "web", Context: c,
		Args: map[string]string{"A": "1", "B": "2", "C": "3"}})
	for i := 0; i < 20; i++ {
		require.Equal(t, first, ImageRef(Request{Service: "web", Context: c,
			Args: map[string]string{"C": "3", "B": "2", "A": "1"}}))
	}
}

func TestSanitizeTag_ProducesSomethingDockerAccepts(t *testing.T) {
	t.Parallel()
	require.Equal(t, "web", sanitizeTag("web"))
	require.Equal(t, "my-service", sanitizeTag("My Service"))
	require.Equal(t, "a-b", sanitizeTag("a/b"))
	require.Equal(t, "service", sanitizeTag(""), "a tag cannot be empty")
	require.Equal(t, "service", sanitizeTag("---"))
	require.Len(t, sanitizeTag(strings.Repeat("a", 200)), 100, "a tag is bounded")
}

func TestBuild_RefusesARequestWithNoContext(t *testing.T) {
	t.Parallel()
	b := &DockerBuilder{clock: clock.NewFake(time.Now())}
	_, err := b.Build(context.Background(), Request{Service: "web"})
	require.Error(t, err)
	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Equal(t, aferrors.AFBLD010, coded.Code())
}
