package change_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/change"
	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
)

func byPath(files []change.File) map[string]change.File {
	out := map[string]change.File{}
	for _, f := range files {
		out[f.Path] = f
	}
	return out
}

func TestParseUnifiedDiff_ReadsStatusPathsAndCounts(t *testing.T) {
	files := load(t, "rename-delete-binary.diff")
	got := byPath(files)
	require.Len(t, got, 5)

	assert.Equal(t, change.StatusAdded, got[".github/workflows/ci.yml"].Status)
	assert.Equal(t, change.StatusDeleted, got["docs/README.md"].Status)

	renamed := got["api/payments.ts"]
	assert.Equal(t, change.StatusRenamed, renamed.Status)
	assert.Equal(t, "api/billing.ts", renamed.OldPath,
		"where a rename came from is the one thing the new path cannot tell you")

	logo := got["assets/logo.png"]
	assert.True(t, logo.Binary)
	assert.Empty(t, logo.AddedLines, "a binary file has no lines to read")
}

// The failure this guards is the classic one for a diff parser. Removing the
// line "-- end" produces "--- end" and adding the line "++ plus" produces
// "+++ plus", both of which are file header prefixes. A parser that matched
// them anywhere would decide the file it was reading had become a different
// file, and would lose the rest of the change without saying so.
func TestParseUnifiedDiff_ContentThatLooksLikeAFileHeaderIsNotOne(t *testing.T) {
	files := load(t, "header-lookalikes.diff")
	got := byPath(files)
	require.Len(t, got, 3, "three files, not one per header lookalike")

	notes := got["notes with space.md"]
	assert.Equal(t, 1, notes.Added, "the added line is '++ plus', which is content")
	assert.Equal(t, 1, notes.Removed, "the removed line is '-- end', which is content")
	require.Len(t, notes.AddedLines, 1)
	assert.Equal(t, "++ plus", notes.AddedLines[0].Text)

	main := got["src/main.go"]
	assert.Equal(t, 4, main.Added)
	assert.Equal(t, 1, main.Removed)
}

// Git quotes a path with unusual bytes in it, using the same escapes a C or Go
// string literal does. A parser that took the quoted form literally would
// report a file nobody can open.
func TestParseUnifiedDiff_UnquotesAPathGitHadToEscape(t *testing.T) {
	files := load(t, "header-lookalikes.diff")
	_, ok := byPath(files)["src/café config.yaml"]
	assert.True(t, ok, "the quoted path was not unescaped; got %v", pathsOf(files))
}

// Line numbers come from the hunk header, not from counting the diff, so a
// fact about a line points at something a reviewer can open.
func TestParseUnifiedDiff_LineNumbersAreTheNewFilesOwn(t *testing.T) {
	files := load(t, "header-lookalikes.diff")
	main := byPath(files)["src/main.go"]
	require.Len(t, main.AddedLines, 4)
	assert.Equal(t, 3, main.AddedLines[0].N)
	assert.Equal(t, 6, main.AddedLines[3].N)

	// A hunk that starts at line 1 would come out right even if the header
	// were ignored and counting started from one, so the same assertion is
	// made against a hunk that starts further down.
	billing := byPath(load(t, "migration-and-billing.diff"))["api/billing.ts"]
	require.Len(t, billing.AddedLines, 6)
	assert.Equal(t, 4, billing.AddedLines[0].N)
	assert.Equal(t, 9, billing.AddedLines[5].N)
}

// git show and git format-patch both put a commit message before the first
// header. None of it is a file.
func TestParseUnifiedDiff_IgnoresAnythingBeforeTheFirstHeader(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "unrecognised.diff"))
	require.NoError(t, err)
	prefixed := "commit 0123456789abcdef\nAuthor: Somebody <s@example.com>\n\n    a subject line\n\n" + string(body)

	files, truncated, err := change.ParseUnifiedDiff(strings.NewReader(prefixed))
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, []string{"pipeline/model.qqq"}, pathsOf(files))
}

func pathsOf(files []change.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

// FromGit and ResolveBase are the only parts of this package that touch the
// world, so they are exercised against a real repository rather than a fake
// one. A fake git is a fake answer to the question this package exists to ask.
func TestFromGit_ReadsTheMergeBaseDiffOfARealRepository(t *testing.T) {
	dir := scratchRepo(t)

	base, err := change.ResolveBase(context.Background(), dir, func(string) string { return "" })
	require.NoError(t, err)
	assert.Equal(t, "main", base, "with no remote and no GITHUB_BASE_REF, main is the base")

	files, truncated, err := change.FromGit(context.Background(),
		change.GitOptions{Dir: dir, Base: base, Head: "feature"})
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, []string{"migrations/001.sql", "src/app.go"}, pathsOf(files))

	// The three dot form is what makes this true: main moved on after the
	// branch forked, and the file it moved with is not this change's.
	assert.NotContains(t, pathsOf(files), "unrelated.md")
}

func TestResolveBase_PrefersTheRefTheJobNames(t *testing.T) {
	dir := scratchRepo(t)
	base, err := change.ResolveBase(context.Background(), dir,
		func(k string) string {
			if k == "GITHUB_BASE_REF" {
				return "release"
			}
			return ""
		})
	require.NoError(t, err)
	assert.Equal(t, "release", base)
}

// A checkout cloned one commit deep has no base branch, and the error has to
// say that rather than diffing against nothing and reporting an empty change.
func TestFromGit_SaysSoWhenTheBaseRefIsNotInTheCheckout(t *testing.T) {
	dir := scratchRepo(t)
	_, _, err := change.FromGit(context.Background(),
		change.GitOptions{Dir: dir, Base: "origin/nonexistent", Head: "HEAD"})
	require.Error(t, err)

	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	assert.Equal(t, aferrors.AFDET010, coded.Code())
}

func TestResolveBase_SaysSoWhenNoUsualBaseExists(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", "-q", "-b", "trunk", ".")
	run(t, dir, "git", "config", "user.email", "a@b.c")
	run(t, dir, "git", "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644))
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-qm", "one")

	_, err := change.ResolveBase(context.Background(), dir, func(string) string { return "" })
	require.Error(t, err)
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	assert.Equal(t, aferrors.AFDET010, coded.Code())
}

// scratchRepo builds a repository with a branch that forked before main moved
// on, which is the shape every pull request has and the shape a two dot diff
// gets wrong.
func scratchRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}

	run(t, dir, "git", "init", "-q", "-b", "main", ".")
	run(t, dir, "git", "config", "user.email", "a@b.c")
	run(t, dir, "git", "config", "user.name", "t")
	write("src/app.go", "package main\n")
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-qm", "base")
	run(t, dir, "git", "branch", "release")

	run(t, dir, "git", "checkout", "-qb", "feature")
	write("src/app.go", "package main\n\nfunc handler() {}\n")
	write("migrations/001.sql", "ALTER TABLE t ADD COLUMN c text;\n")
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-qm", "feature")

	run(t, dir, "git", "checkout", "-q", "main")
	write("unrelated.md", "moved on\n")
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-qm", "unrelated")
	run(t, dir, "git", "checkout", "-q", "feature")
	return dir
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "%s %s: %s", name, strings.Join(args, " "), out)
}
