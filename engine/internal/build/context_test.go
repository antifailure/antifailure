package build

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

// tree writes a directory from a path to content map and returns its root.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	return root
}

func build(t *testing.T, root string, ignore string) *Context {
	t.Helper()
	ig, err := ParseIgnore(strings.NewReader(ignore))
	require.NoError(t, err)
	c, err := NewContext(ContextOptions{Root: root, Ignore: ig, Service: "web"})
	require.NoError(t, err)
	return c
}

func TestContext_IncludesWhatIsNotIgnored(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{
		"Dockerfile":                  "FROM alpine\n",
		"package.json":                `{"name":"x"}`,
		"src/index.ts":                "export {}\n",
		"node_modules/react/index.js": "module.exports={}\n",
		".git/HEAD":                   "ref: refs/heads/main\n",
	})
	c := build(t, root, "node_modules\n.git\n")

	require.Equal(t, []string{"Dockerfile", "package.json", "src/index.ts"}, c.Files)
	require.True(t, c.Has("src/index.ts"))
	require.False(t, c.Has("node_modules/react/index.js"))
	require.Equal(t, 2, c.Excluded, "the two ignored directories, counted once each")
	require.Positive(t, c.Size())
}

func TestContext_DigestIsTheSameForTheSameContent(t *testing.T) {
	t.Parallel()
	// The whole point. Two clones of one commit on two machines must produce
	// one digest, or the build cache never hits across them.
	files := map[string]string{
		"Dockerfile":   "FROM alpine\nCOPY . /app\n",
		"src/index.ts": "export const a = 1\n",
		"src/util.ts":  "export const b = 2\n",
	}
	a := build(t, tree(t, files), "")
	b := build(t, tree(t, files), "")
	require.Equal(t, a.Digest, b.Digest)
	require.Len(t, a.Digest, 64)
}

func TestContext_DigestIgnoresModificationTimeAndPermissions(t *testing.T) {
	t.Parallel()
	// Both differ between two clones of the same repository, so if either
	// reached the digest the cache would miss every time.
	files := map[string]string{"a.txt": "hello", "run.sh": "#!/bin/sh\n"}
	rootA := tree(t, files)
	rootB := tree(t, files)

	require.NoError(t, os.Chtimes(filepath.Join(rootB, "a.txt"),
		epoch.AddDate(20, 0, 0), epoch.AddDate(20, 0, 0)))
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Chmod(filepath.Join(rootB, "a.txt"), 0o600))
	}
	require.Equal(t, build(t, rootA, "").Digest, build(t, rootB, "").Digest)
}

func TestContext_DigestChangesWithContent(t *testing.T) {
	t.Parallel()
	a := build(t, tree(t, map[string]string{"a.txt": "one"}), "")
	b := build(t, tree(t, map[string]string{"a.txt": "two"}), "")
	require.NotEqual(t, a.Digest, b.Digest)
}

func TestContext_DigestChangesWithTheExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful here")
	}
	t.Parallel()
	// The one permission that matters inside an image. A script that loses it
	// produces a container that exits immediately with "permission denied",
	// so it must be part of what the cache keys on.
	rootA := tree(t, map[string]string{"run.sh": "#!/bin/sh\necho hi\n"})
	rootB := tree(t, map[string]string{"run.sh": "#!/bin/sh\necho hi\n"})
	require.NoError(t, os.Chmod(filepath.Join(rootB, "run.sh"), 0o755))
	require.NotEqual(t, build(t, rootA, "").Digest, build(t, rootB, "").Digest)
}

func TestContext_DigestChangesWhenAFileIsIgnored(t *testing.T) {
	t.Parallel()
	files := map[string]string{"a.txt": "one", "b.txt": "two"}
	root := tree(t, files)
	require.NotEqual(t, build(t, root, "").Digest, build(t, root, "b.txt\n").Digest,
		"changing what the build sees must change what the cache keys on")
}

func TestContext_TarIsReadableAndNormalized(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{
		"Dockerfile": "FROM alpine\n",
		"src/a.ts":   "export const a = 1\n",
	})
	c := build(t, root, "")

	tr := tar.NewReader(c.Tar())
	seen := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		// Compared as an instant, not as a representation: the reader returns
		// it in the local zone, which is the same moment written differently.
		require.True(t, epoch.Equal(h.ModTime), "%s carries a fixed time, got %s", h.Name, h.ModTime)
		require.Zero(t, h.Uid, "%s carries no owner", h.Name)
		require.Zero(t, h.Gid)
		require.Empty(t, h.Uname)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		seen[h.Name] = string(body)
	}
	require.Equal(t, map[string]string{
		"Dockerfile": "FROM alpine\n",
		"src/a.ts":   "export const a = 1\n",
	}, seen)
}

func TestContext_EntriesAreSortedRegardlessOfWalkOrder(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{
		"z.txt": "z", "a.txt": "a", "m/b.txt": "b", "m/a.txt": "a",
	})
	c := build(t, root, "")
	require.Equal(t, []string{"a.txt", "m/a.txt", "m/b.txt", "z.txt"}, c.Files)
	require.True(t, c.Has("m/a.txt"))
	require.False(t, c.Has("m/c.txt"))
}

func TestContext_SymlinkInsideTheRootIsKept(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege here")
	}
	t.Parallel()
	root := tree(t, map[string]string{"real/a.txt": "hello"})
	require.NoError(t, os.Symlink("real/a.txt", filepath.Join(root, "link.txt")))

	c := build(t, root, "")
	require.Contains(t, c.Files, "link.txt")

	tr := tar.NewReader(c.Tar())
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if h.Name == "link.txt" {
			found = true
			require.Equal(t, byte(tar.TypeSymlink), h.Typeflag)
			require.Equal(t, "real/a.txt", h.Linkname)
			require.Zero(t, h.Size)
		}
	}
	require.True(t, found)
}

func TestContext_SymlinkOutsideTheRootIsDropped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege here")
	}
	t.Parallel()
	// A context that reads outside the repository is not reproducible on
	// anyone else's machine, and it is how a file nobody audited ends up in an
	// image layer.
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600))

	root := tree(t, map[string]string{"a.txt": "a"})
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "leak")))
	require.NoError(t, os.Symlink("../..", filepath.Join(root, "up")))

	c := build(t, root, "")
	require.Equal(t, []string{"a.txt"}, c.Files)
	require.Equal(t, 2, c.Excluded)
}

func TestContext_RefusesAContextOverTheSizeLimit(t *testing.T) {
	t.Parallel()
	root := tree(t, map[string]string{"big.bin": strings.Repeat("x", 4096)})
	_, err := NewContext(ContextOptions{Root: root, Service: "web", MaxBytes: 1024})
	require.Error(t, err)

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Equal(t, aferrors.AFBLD003, coded.Code())
	require.Contains(t, coded.Message(), "web", "the message names the service")
	require.Contains(t, coded.Message(), "big.bin", "and the file where the limit was reached")
	require.Contains(t, coded.NextStep(), ".dockerignore")
}

func TestContext_RefusesTooManyFiles(t *testing.T) {
	t.Parallel()
	files := map[string]string{}
	for i := 0; i < 12; i++ {
		files[string(rune('a'+i))+".txt"] = "x"
	}
	_, err := NewContext(ContextOptions{Root: tree(t, files), Service: "worker", MaxFiles: 3})
	require.Error(t, err)

	var coded *aferrors.Error
	require.True(t, aferrors.As(err, &coded))
	require.Equal(t, aferrors.AFBLD004, coded.Code())
	require.Contains(t, coded.Message(), "worker")
	require.Contains(t, coded.Message(), "3")
}

func TestContext_EmptyDirectoryProducesAnEmptyContext(t *testing.T) {
	t.Parallel()
	c := build(t, t.TempDir(), "")
	require.Empty(t, c.Files)
	require.Zero(t, c.Bytes)
	require.False(t, c.Has("anything"))
	// Still a valid, readable archive rather than a nil reader.
	_, err := io.ReadAll(c.Tar())
	require.NoError(t, err)
}

func TestContext_NilIgnoreExcludesNothing(t *testing.T) {
	t.Parallel()
	c, err := NewContext(ContextOptions{Root: tree(t, map[string]string{"a.txt": "a"})})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, c.Files)
}

func TestContext_SkipsAnIgnoredDirectoryWithoutWalkingIt(t *testing.T) {
	t.Parallel()
	// Not a performance nicety. Walking a large node_modules to then discard
	// every entry is the difference between a build that starts in a second
	// and one that appears to hang.
	files := map[string]string{"src/a.ts": "a"}
	for i := 0; i < 200; i++ {
		files["node_modules/pkg"+string(rune('a'+i%26))+"/index.js"] = "x"
	}
	c := build(t, tree(t, files), "node_modules\n")
	require.Equal(t, []string{"src/a.ts"}, c.Files)
	require.Equal(t, 1, c.Excluded, "the directory is counted once, not once per file inside it")
}

func TestContext_ReportsAWalkFailureRatherThanASilentlySmallerContext(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("an unreadable directory is not enforced here")
	}
	t.Parallel()
	// A context missing files because a directory could not be read produces
	// an image missing code, and a runtime failure with nothing in the build
	// output to explain it.
	root := tree(t, map[string]string{"a.txt": "a", "locked/b.txt": "b"})
	locked := filepath.Join(root, "locked")
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	_, err := NewContext(ContextOptions{Root: root, Service: "web"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "locked")
}

func TestHumanBytes_ReadsAsSomebodyWouldSayIt(t *testing.T) {
	t.Parallel()
	require.Equal(t, "512 B", humanBytes(512))
	require.Equal(t, "1.0 KiB", humanBytes(1024))
	require.Equal(t, "1.5 MiB", humanBytes(1536*1024))
	require.Equal(t, "2.0 GiB", humanBytes(2<<30))
}

func TestNormalizeMode_KeepsOnlyTheExecutableDistinction(t *testing.T) {
	t.Parallel()
	require.Equal(t, os.FileMode(0o644), normalizeMode(0o600))
	require.Equal(t, os.FileMode(0o644), normalizeMode(0o666))
	require.Equal(t, os.FileMode(0o755), normalizeMode(0o700))
	require.Equal(t, os.FileMode(0o755), normalizeMode(0o777))
}

func TestContext_TarCanBeReadTwice(t *testing.T) {
	t.Parallel()
	// A retry after a transient daemon failure re-sends the context. A reader
	// that could only be consumed once would turn a retryable failure into a
	// permanent one.
	c := build(t, tree(t, map[string]string{"a.txt": "hello"}), "")
	first, err := io.ReadAll(c.Tar())
	require.NoError(t, err)
	second, err := io.ReadAll(c.Tar())
	require.NoError(t, err)
	require.True(t, bytes.Equal(first, second))
	require.Equal(t, c.Size(), len(first))
}

// An exception inside an excluded directory has to survive the walk.
//
// The bug: the walk pruned an excluded directory whole, so a later `!` rule
// naming a file inside it never ran. `Excluded` was correct and never asked.
// A Dockerfile re-included that way was missing from the context and the daemon
// reported that it could not locate a file that was plainly on disk.
func TestContext_KeepsAFileReIncludedInsideAnExcludedDirectory(t *testing.T) {
	root := tree(t, map[string]string{
		"deploy/docker/app.Dockerfile": "FROM alpine\n",
		"deploy/docker/notes.md":       "not needed in an image",
		"deploy/terraform/main.tf":     "also not needed",
		"main.go":                      "package main",
	})

	c := build(t, root, "deploy\n!deploy/docker/app.Dockerfile\n")

	require.True(t, c.Has("deploy/docker/app.Dockerfile"),
		"the re-included Dockerfile is missing: the walk pruned deploy before the exception ran")
	require.False(t, c.Has("deploy/docker/notes.md"), "the exclusion still applies to everything else")
	require.False(t, c.Has("deploy/terraform/main.tf"))
	require.True(t, c.Has("main.go"))
}

// The pruning it replaced still has to happen, or ignoring node_modules stops
// being fast and starts being a walk of every dependency in the tree.
func TestContext_StillPrunesAnExcludedDirectoryWithNoExceptionInIt(t *testing.T) {
	files := map[string]string{"main.go": "package main"}
	for i := 0; i < 200; i += 1 {
		files["node_modules/pkg"+string(rune('a'+i%26))+"/index.js"] = "x"
	}
	root := tree(t, files)

	// An exception elsewhere, so the rule set has one and the question is
	// whether this directory is judged on its own.
	c := build(t, root, "node_modules\ndeploy\n!deploy/docker/app.Dockerfile\n")

	require.False(t, c.Has("node_modules/pkga/index.js"))
	require.True(t, c.Has("main.go"))
	require.Less(t, len(c.Files), 10,
		"the whole of node_modules was walked, so a rule set with any exception in it "+
			"makes every excluded directory expensive")
}

// A context under the refusal limit and far over anything a compiler reads.
//
// The regression: the control plane's build sent 598 MiB on every run, all but
// two of it a marketing site the image never opens, and nothing in the output
// said so. The two gigabyte refusal did not fire, because the mistake was not
// large enough to be refused, only large enough to turn a forty second build
// into half an hour. What was missing was the sentence naming the directory.
func TestContext_NamesTheDirectoryThatDominatesAnOversizedContext(t *testing.T) {
	big := strings.Repeat("x", 300<<20/3)
	root := tree(t, map[string]string{
		"src/main.go":    "package main",
		"www/a":          big,
		"www/b":          big,
		"www/c":          big,
		"docs/README.md": "hello",
	})
	c := build(t, root, "")

	require.True(t, c.Oversized(), "300 MiB is over the threshold worth mentioning")
	require.Equal(t, "www/", c.Largest)
	require.Contains(t, c.Explain(), "www/")
	require.Contains(t, c.Explain(), "% of it")

	// And the same tree with the directory excluded is unremarkable, which is
	// the state the warning is asking somebody to reach.
	quiet := build(t, root, "www\n")
	require.False(t, quiet.Oversized())
}

// A top level file, rather than a directory, can be the largest entry.
func TestContext_LargestEntryCanBeAFileAtTheRoot(t *testing.T) {
	root := tree(t, map[string]string{
		"fixture.bin": strings.Repeat("x", 4096),
		"src/main.go": "package main",
	})
	c := build(t, root, "")
	require.Equal(t, "fixture.bin", c.Largest)
	require.False(t, c.Oversized(), "4 KiB is not worth a warning")
}

// Tar hands out a reader over the archive rather than a copy of it.
//
// The regression is a cost rather than a wrong answer: the reader was built as
// strings.NewReader(string(c.tarball)), which allocates and copies the whole
// archive every time the daemon is handed one. At 600 MiB that is 600 MiB of
// garbage per build, invisible in the output and invisible in a small test.
// Reading twice already had a test; this one asserts the copy is gone.
func TestContext_TarDoesNotCopyTheArchive(t *testing.T) {
	c := build(t, tree(t, map[string]string{"a.txt": strings.Repeat("a", 8192)}), "")

	r, ok := c.Tar().(*bytes.Reader)
	require.True(t, ok, "Tar must return a reader over the bytes, not over a copy of them")
	require.Equal(t, int64(c.Size()), r.Size())

	// And it still reads the same archive.
	body, err := io.ReadAll(c.Tar())
	require.NoError(t, err)
	require.Equal(t, c.Size(), len(body))
}
