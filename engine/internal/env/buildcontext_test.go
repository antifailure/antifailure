package env_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/build"
)

// build.context narrows what a build can see.
//
// The regression: the field was validated, printed by `af explain`, and read
// by nothing, so every service got the whole repository whatever the manifest
// said. A user who set it believed something was happening, which is the worst
// of the three possible behaviours.
//
// Asserted at the layer the field actually changes, which is which directory
// the context is walked from. Driving it through Up would need a daemon and
// would prove less.
func TestBuildContext_NarrowsWhatTheBuildSees(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "web", "app.js"), "app")
	write(t, filepath.Join(root, "web", "Dockerfile"), "FROM node")
	write(t, filepath.Join(root, "infra", "huge.tf"), strings.Repeat("x", 4096))

	whole, err := build.NewContext(build.ContextOptions{Root: root, Service: "web"})
	require.NoError(t, err)
	require.Contains(t, whole.Files, "infra/huge.tf",
		"without build.context the whole repository is sent")

	narrowed, err := build.NewContext(build.ContextOptions{
		Root: filepath.Join(root, "web"), Service: "web",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"Dockerfile", "app.js"}, narrowed.Files)
	require.NotContains(t, narrowed.Files, "infra/huge.tf")
	require.Less(t, narrowed.Bytes, whole.Bytes)
}

// A Dockerfile outside the context is refused with a message that says so.
//
// Docker refuses the same thing for the same reason: a build that reads
// outside its context is not reproducible anywhere else. The failure without
// this is the daemon saying "cannot locate specified Dockerfile" about a file
// anybody can see, which is the confusion AF-BLD-011 already exists to end.
func TestBuildContext_RefusesADockerfileOutsideIt(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "web", "app.js"), "app")
	write(t, filepath.Join(root, "deploy", "web.Dockerfile"), "FROM node")

	narrowed, err := build.NewContext(build.ContextOptions{
		Root: filepath.Join(root, "web"), Service: "web",
	})
	require.NoError(t, err)
	require.False(t, narrowed.Has("deploy/web.Dockerfile"),
		"the Dockerfile is outside the context, so the build cannot be given it")
}

func write(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}
