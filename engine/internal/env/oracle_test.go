package env_test

import (
	"archive/tar"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// The two parts of the oracle's orchestration that can be tested without
// bringing two environments up, and both of them are parts that would be
// expensive to get wrong.
//
// The baseline resolution decides what a whole run is compared against, and a
// silent wrong answer there produces a report about the wrong change. The
// untar writes bytes from a commit into a directory beside the user's working
// tree, and an escape there writes into the repository.

// gitRepo builds a small repository with a base branch and a feature branch,
// and returns its path and the two commits.
//
// A real repository rather than a stub, because what is being tested is what
// git says about merge bases, and a stub would be a second implementation of
// git's answer.
func gitRepo(t *testing.T) (root, base, feature string) {
	t.Helper()
	root = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test",
			// A fixed instant, so two commits made in the same second are
			// still distinct and the test does not depend on wall time.
			"GIT_AUTHOR_DATE=2026-08-30T09:00:00Z",
			"GIT_COMMITTER_DATE=2026-08-30T09:00:00Z")
		out, err := cmd.Output()
		require.NoErrorf(t, err, "git %v", args)
		return string(bytes.TrimSpace(out))
	}
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o644))
	}

	run("init", "--initial-branch=main", "-q")
	write("app.go", "package main\n")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	base = run("rev-parse", "HEAD")

	run("checkout", "-q", "-b", "feature")
	write("app.go", "package main // changed\n")
	run("add", ".")
	run("commit", "-q", "-m", "second")
	feature = run("rev-parse", "HEAD")

	// A commit on main after the branch point, which is what makes the merge
	// base and the ref two different answers rather than the same one.
	run("checkout", "-q", "main")
	write("unrelated.go", "package main\n")
	run("add", ".")
	run("commit", "-q", "-m", "third")
	run("checkout", "-q", "feature")
	return root, base, feature
}

func TestTheMergeBaseIsNotTheTipOfTheBaseBranch(t *testing.T) {
	root, base, _ := gitRepo(t)

	rev, how, err := env.ResolveBaselineForTest(root, schema.BaselineMergeBase, "main")
	require.NoError(t, err)
	require.Equal(t, base, rev,
		"the merge base is the commit the two branches share, not main's tip")
	require.Equal(t, "the merge base with main", how)

	tip, howRef, err := env.ResolveBaselineForTest(root, schema.BaselineRef, "main")
	require.NoError(t, err)
	require.NotEqual(t, base, tip, "the ref is main's tip, which has moved on")
	require.Equal(t, "the ref main", howRef)
}

// The how is part of the answer. "The merge base with main" and "the tag
// v2.4.0" answer different questions, and a report that named a commit without
// saying which question it answered would leave the reader to guess.
func TestResolutionSaysHowItDecided(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, how, err := env.ResolveBaselineForTest(root, schema.BaselineMergeBase, "")
	require.NoError(t, err)
	require.Equal(t, "the merge base with main", how,
		"with no ref configured it falls back and says which fallback it used")
}

func TestAnUnresolvableRefIsRefusedRatherThanGuessedAt(t *testing.T) {
	root, _, _ := gitRepo(t)
	_, _, err := env.ResolveBaselineForTest(root, schema.BaselineRef, "does-not-exist")
	require.Error(t, err)
	require.Contains(t, err.Error(), "AF-ORC-003")
}

// tarOf builds an archive from a list of entries.
func tarOf(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, h := range entries {
		header := h
		if header.Typeflag == tar.TypeReg {
			header.Size = int64(len("body"))
		}
		require.NoError(t, w.WriteHeader(&header))
		if header.Typeflag == tar.TypeReg {
			_, err := w.Write([]byte("body"))
			require.NoError(t, err)
		}
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestUntarWritesTheArchive(t *testing.T) {
	dir := t.TempDir()
	body := tarOf(t,
		tar.Header{Name: "cmd/", Typeflag: tar.TypeDir, Mode: 0o755},
		tar.Header{Name: "cmd/main.go", Typeflag: tar.TypeReg, Mode: 0o644},
		tar.Header{Name: "go.mod", Typeflag: tar.TypeReg, Mode: 0o644},
		tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "go.mod"},
	)
	require.NoError(t, env.UntarForTest(dir, bytes.NewReader(body)))

	got, err := os.ReadFile(filepath.Join(dir, "cmd", "main.go"))
	require.NoError(t, err)
	require.Equal(t, "body", string(got))

	// A symlink inside the tree is kept, because a repository that uses one to
	// share a file between services builds differently without it.
	target, err := os.Readlink(filepath.Join(dir, "link"))
	require.NoError(t, err)
	require.Equal(t, "go.mod", target)
}

// The escapes. Each of these would write outside the directory the caller
// promised to remove, which is the user's own repository.
func TestUntarRefusesAPathOutsideTheCheckout(t *testing.T) {
	victim := t.TempDir()
	cases := []struct {
		name  string
		entry tar.Header
	}{
		{"parent segments", tar.Header{Name: "../escaped.go", Typeflag: tar.TypeReg, Mode: 0o644}},
		{"deep parent segments", tar.Header{Name: "a/b/../../../escaped.go", Typeflag: tar.TypeReg, Mode: 0o644}},
		{"absolute path", tar.Header{Name: "/etc/escaped", Typeflag: tar.TypeReg, Mode: 0o644}},
		{"a directory outside", tar.Header{Name: "../escaped/", Typeflag: tar.TypeDir, Mode: 0o755}},
		{"a symlink outside", tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../../secrets"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(victim, "checkout-"+c.name)
			require.NoError(t, os.MkdirAll(dir, 0o755))
			err := env.UntarForTest(dir, bytes.NewReader(tarOf(t, c.entry)))
			require.Error(t, err, "the archive was accepted and should not have been")
			require.Contains(t, err.Error(), "out of the checkout")

			// And nothing landed beside the directory.
			entries, readErr := os.ReadDir(victim)
			require.NoError(t, readErr)
			for _, e := range entries {
				require.Truef(t, filepath.HasPrefix(e.Name(), "checkout-"),
					"%s appeared beside the checkout", e.Name())
			}
		})
	}
}

// The negative control for the escape tests above. Every one of them asserts
// an error, and an untar that refused everything would pass all five while
// being useless.
func TestUntarAcceptsWhatItShould(t *testing.T) {
	dir := t.TempDir()
	err := env.UntarForTest(dir, bytes.NewReader(tarOf(t,
		tar.Header{Name: "./nested/../fine.go", Typeflag: tar.TypeReg, Mode: 0o644},
	)))
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(dir, "fine.go"))
	require.NoError(t, statErr)
}

// A manifest in a subdirectory has to get that subdirectory's tree, not the
// whole repository's. Asking git for the whole tree puts every file one
// directory deeper than the build context expects, and the baseline build then
// fails with no Dockerfile in a tree that plainly has one.
func TestTheBaselineCheckoutIsTheManifestsOwnSubtree(t *testing.T) {
	root, base, _ := gitRepoWithSubdirectory(t)

	o, err := env.New(env.Options{
		Root:     filepath.Join(root, "services", "api"),
		Manifest: &schema.Manifest{Name: "api"},
		Branch:   "feature",
		Progress: func(string) {},
	})
	require.NoError(t, err)

	dir, clean, err := o.BaselineTreeForTest(t.Context(), base)
	require.NoError(t, err)
	t.Cleanup(clean)

	// The service's own files are at the root of the checkout.
	body, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, "FROM scratch\n", string(body))

	// And the rest of the repository is not in it.
	_, err = os.Stat(filepath.Join(dir, "services"))
	require.True(t, os.IsNotExist(err), "the whole repository was archived")
	_, err = os.Stat(filepath.Join(dir, "README.md"))
	require.True(t, os.IsNotExist(err), "a file outside the manifest's directory was archived")

	// The checkout lives under the manifest's own state directory and the
	// cleanup removes it, because a tree left behind is a copy of the source
	// nobody is watching.
	require.Contains(t, dir, filepath.Join(env.StateDir, "oracle"))
	clean()
	_, err = os.Stat(dir)
	require.True(t, os.IsNotExist(err), "the checkout outlived the comparison")
}

func gitRepoWithSubdirectory(t *testing.T) (root, base, head string) {
	t.Helper()
	root = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test",
			"GIT_AUTHOR_DATE=2026-08-30T09:00:00Z",
			"GIT_COMMITTER_DATE=2026-08-30T09:00:00Z")
		out, err := cmd.Output()
		require.NoErrorf(t, err, "git %v", args)
		return string(bytes.TrimSpace(out))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("the repository\n"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "services", "api", "Dockerfile"), []byte("FROM scratch\n"), 0o644))

	run("init", "--initial-branch=main", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "first")
	base = run("rev-parse", "HEAD")

	require.NoError(t, os.WriteFile(
		filepath.Join(root, "services", "api", "later.txt"), []byte("later\n"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "second")
	return root, base, run("rev-parse", "HEAD")
}
