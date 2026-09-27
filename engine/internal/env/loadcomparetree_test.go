package env_test

// THE HALF OF THE IMAGE AXIS THAT IS ABOUT THE APPLICATION.
//
// Comparing two database builds is only an experiment if the application is
// held still, and holding it still is not the same as running the same commit
// on both sides. The candidate compiles from the working tree. The baseline
// ordinarily compiles from a clean archive of the base revision, which is
// exactly right when the revisions differ and is the confound itself when they
// do not: an uncommitted edit would then be in one side's application and not
// the other's, and the two application images would differ on the one path
// whose entire purpose is that they do not.
//
// The uncommitted file is what makes the two paths tell each other apart. An
// assertion that both sides got "the same commit" cannot see this at all, and a
// live comparison cannot see it either unless the edit is inside the build root,
// which is why this is a test and not a reading off a run.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func TestTheBaseSideCompilesTheCandidatesOwnTreeOnOneRevision(t *testing.T) {
	root, base, head := gitRepoWithSubdirectory(t)
	buildRoot := filepath.Join(root, "services", "api")

	// Never committed, so it exists in the working tree and in no revision.
	require.NoError(t, os.WriteFile(filepath.Join(buildRoot, "uncommitted.go"),
		[]byte("package main // an edit nobody committed\n"), 0o644))

	o, err := env.New(env.Options{
		Root:     buildRoot,
		Manifest: &schema.Manifest{Name: "api"},
		Branch:   "feature",
		Progress: func(string) {},
	})
	require.NoError(t, err)

	// ONE REVISION, TWO DATABASE BUILDS. The base side is given the candidate's
	// own tree, so whatever is in one side's application is in the other's.
	same, cleanSame, err := o.BaselineBuildRootForTest(t.Context(), head, false)
	require.NoError(t, err)
	t.Cleanup(cleanSame)
	// The PROPERTY first and the mechanism second, because require stops at the
	// first failure: a mutation that makes this path export would fail on the
	// missing file, and the equality below then needs a break of its own to
	// show it is load bearing rather than a restatement.
	require.FileExists(t, filepath.Join(same, "uncommitted.go"),
		"an uncommitted edit must reach BOTH applications or neither, and this is the "+
			"path where it reaches both")
	require.Equal(t, buildRoot, same,
		"with one revision the base side must compile the candidate's own tree, and the "+
			"path it names must be the tree itself rather than something that merely "+
			"contains the same files")

	// TWO REVISIONS. A clean archive of the base revision, which is the whole
	// reason the archive exists, and the uncommitted edit must not be in it.
	varied, cleanVaried, err := o.BaselineBuildRootForTest(t.Context(), base, true)
	require.NoError(t, err)
	require.NotEqual(t, buildRoot, varied,
		"with two revisions the base side must compile an archive rather than this tree")
	require.FileExists(t, filepath.Join(varied, "Dockerfile"),
		"the archive is the manifest's own subtree, so the build context is in it")
	require.NoFileExists(t, filepath.Join(varied, "uncommitted.go"),
		"an archive of a revision cannot carry an edit nobody committed")
	require.NoFileExists(t, filepath.Join(varied, "later.txt"),
		"the archive is of the BASE revision, and this file landed after it")

	// The cleanup removes what it wrote, and the one for the shared tree does
	// not: a comparison that deleted the candidate's own build root would take
	// the repository with it.
	cleanVaried()
	require.NoDirExists(t, varied, "the archive is removed by its own cleanup")
	cleanSame()
	require.DirExists(t, buildRoot,
		"the shared tree's cleanup must be a no-op, because it is the repository")
	require.FileExists(t, filepath.Join(buildRoot, "uncommitted.go"))
}
