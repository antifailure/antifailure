package proxyimage_test

import (
	"archive/tar"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/proxyimage"
)

func TestSources_MatchTheRealPackages(t *testing.T) {
	t.Parallel()
	// The whole point of generating this rather than writing a standalone
	// proxy: the code deciding live traffic inside an environment and the code
	// answering af net explain are the same code. If they drift, the drift is
	// invisible until a request is allowed that should not have been.
	root := engineRoot(t)
	for name, packaged := range proxyimage.Sources {
		if name == "go.mod" {
			continue
		}
		onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		require.NoError(t, err, "%s is packaged but not in the repository", name)
		require.Equal(t, string(onDisk), packaged,
			"%s has changed since sources.gen.go was generated. Run 'go run ./tools/proxysrc'.", name)
	}
}

func TestSources_CarryEverythingTheBuildNeeds(t *testing.T) {
	t.Parallel()
	require.Contains(t, proxyimage.Sources, "go.mod")
	require.Contains(t, proxyimage.Sources, "cmd/af-proxy/main.go")
	require.Contains(t, proxyimage.Sources, "internal/policy/policy.go")
	require.Contains(t, proxyimage.Sources, "pkg/schema/manifest.go")
}

func TestSources_CarryEveryFileInTheSidecarsPackage(t *testing.T) {
	t.Parallel()
	// The list in tools/proxysrc is written by hand, and a file added to
	// cmd/af-proxy that nobody adds to it is not packaged. Nothing noticed:
	// the engine builds, its tests pass against the real package, and the
	// image build fails inside the daemon minutes later with an undefined
	// symbol. That is exactly what happened when the address guard was added,
	// and it is the kind of failure that reads as a broken Docker rather than
	// as a missing line in a generator.
	root := engineRoot(t)
	dir := filepath.Join(root, "cmd", "af-proxy")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Checked by lookup rather than with Contains, because a failing
		// Contains on this map prints every byte of the sidecar's source at
		// somebody trying to read one missing filename.
		if _, ok := proxyimage.Sources["cmd/af-proxy/"+name]; !ok {
			t.Errorf("cmd/af-proxy/%s is part of the sidecar and is not packaged into its "+
				"image. Add it to sources in tools/proxysrc and run 'go run ./tools/proxysrc'.", name)
		}
	}
}

func TestSources_ImportNothingOutsideTheStandardLibrary(t *testing.T) {
	t.Parallel()
	// The build inside the image downloads no modules, which is what lets it
	// work with no network and no registry. An import added to policy or
	// schema would break that at somebody's first af up; this breaks it here
	// instead.
	//
	// Parsed rather than scanned line by line, because a heuristic over text
	// finds import paths in prose and reports them as dependencies.
	for name, body := range proxyimage.Sources {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, body, parser.ImportsOnly)
		require.NoError(t, err, "%s does not parse", name)

		for _, spec := range file.Imports {
			path, unquoteErr := strconv.Unquote(spec.Path.Value)
			require.NoError(t, unquoteErr)
			if !strings.Contains(strings.Split(path, "/")[0], ".") {
				continue // a standard library package has no dot in its first segment
			}
			require.True(t, strings.HasPrefix(path, "github.com/antifailure/antifailure/engine/"),
				"%s imports %s, which the sidecar's offline build cannot fetch", name, path)
		}
	}
}

func TestTag_ChangesWithTheSources(t *testing.T) {
	t.Parallel()
	first := proxyimage.Tag()
	require.Equal(t, first, proxyimage.Tag(), "the tag is stable for unchanged sources")
	require.True(t, strings.HasPrefix(first, "antifailure/proxy:"))
	require.Len(t, strings.Split(first, ":")[1], 16)
}

func TestBuildContext_IsAReadableArchiveWithADockerfile(t *testing.T) {
	t.Parallel()
	tr := tar.NewReader(proxyimage.BuildContext())
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		body, readErr := io.ReadAll(tr)
		require.NoError(t, readErr)
		require.Equal(t, h.Size, int64(len(body)))
		seen[h.Name] = true
	}
	require.True(t, seen["Dockerfile"])
	require.True(t, seen["cmd/af-proxy/main.go"])
	require.True(t, seen["go.mod"])
}

func TestBuildContext_IsByteIdenticalEveryTime(t *testing.T) {
	t.Parallel()
	// The tag is derived from the sources, so two machines must produce the
	// same archive or the same environment rebuilds the sidecar forever.
	first, err := io.ReadAll(proxyimage.BuildContext())
	require.NoError(t, err)
	second, err := io.ReadAll(proxyimage.BuildContext())
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func engineRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	// From internal/proxyimage up to engine.
	return filepath.Dir(filepath.Dir(wd))
}
