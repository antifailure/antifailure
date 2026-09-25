package env

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// COMPARING TWO DATABASE BUILDS: WHICH AXIS MOVED, AND WHAT THE REPORT OWES A
// READER BECAUSE OF IT.
//
// Every test here is about the decision rather than about two containers, and
// that is deliberate: the decision is the part that can be silently wrong. A
// comparison that varied two axes and reported a regression is a set of numbers
// every one of which is a number, and only the axis says it means nothing.

func imageManifest(image string) *schema.Manifest {
	return &schema.Manifest{Name: "ledger", Database: &schema.Database{
		Provider: schema.DBDocker, Version: 17, Image: image,
	}}
}

// The axis is what ACTUALLY differed, over every combination of the two flags
// and the two revisions. The default row is the one that matters most: a run
// that names no image is the revision axis and nothing else, which is what keeps
// every existing comparison what it was.
func TestTheAxisIsWhatActuallyDiffered(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		declared       string
		opts           LoadCompareOptions
		revisionVaried bool
		want           CompareAxis
		wantBase       string
		wantCandidate  string
	}{
		{"nothing named", "", LoadCompareOptions{}, true, AxisRevision, "", ""},
		{"nothing named, manifest image", "pg:a", LoadCompareOptions{}, true,
			AxisRevision, "pg:a", "pg:a"},
		{"a base image alone", "pg:a", LoadCompareOptions{BaselineImage: "pg:b"}, true,
			AxisBoth, "pg:b", "pg:a"},
		{"a base image alone, same commit", "pg:a", LoadCompareOptions{BaselineImage: "pg:b"}, false,
			AxisImage, "pg:b", "pg:a"},
		{"both images, same commit", "pg:a",
			LoadCompareOptions{Image: "pg:c", BaselineImage: "pg:b"}, false,
			AxisImage, "pg:b", "pg:c"},
		{"both images, both the same", "pg:a",
			LoadCompareOptions{Image: "pg:b", BaselineImage: "pg:b"}, true,
			AxisRevision, "pg:b", "pg:b"},
		{"this build's image alone", "pg:a", LoadCompareOptions{Image: "pg:c"}, false,
			AxisImage, "pg:a", "pg:c"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := resolveCompareImages(imageManifest(c.declared), c.opts, c.revisionVaried)
			require.Equal(t, c.want, got.axis)
			require.Equal(t, c.wantBase, got.baseline, "the base side's build")
			require.Equal(t, c.wantCandidate, got.candidate, "this build's side")
			require.Equal(t, c.declared, got.golden, "the golden is always the manifest's build")
		})
	}
}

// The refusal fires when and only when NEITHER axis moved, and it names both of
// them. Same commit with two images is the experiment this lane exists for and
// must be allowed; a different commit with one image is the comparison that
// always existed and must still be allowed.
func TestNothingVariedIsRefusedAndTwoImagesOnOneCommitIsNot(t *testing.T) {
	t.Parallel()
	nothing := resolveCompareImages(imageManifest("pg:a"), LoadCompareOptions{}, false)
	require.False(t, nothing.varied(), "the guard's other half must be false here")

	twoImages := resolveCompareImages(imageManifest("pg:a"),
		LoadCompareOptions{BaselineImage: "pg:b"}, false)
	require.True(t, twoImages.varied(),
		"same commit and two images must be a comparison, not a refusal")

	// The message names both flags, because on this path both axes are level and
	// somebody who meant to vary the database must not be sent to --baseline.
	msg := ErrLoadBaselineNothingVaried.Error()
	require.Contains(t, msg, "--baseline")
	require.Contains(t, msg, "--baseline-image")
	require.Contains(t, msg, "same commit")
	require.Same(t, ErrLoadBaselineNothingVaried, ErrLoadBaselineSameCommit,
		"the old exported name must be the same value, or errors.Is answers differently for it")
}

// The sides that open a data directory they did not write, which is what decides
// whether a branch runs the golden or a copy of it on another build.
func TestOnlyASideOnAnotherBuildOpensADirectoryItDidNotWrite(t *testing.T) {
	t.Parallel()
	require.Nil(t, compareImages{golden: "pg:a", baseline: "pg:a", candidate: "pg:a"}.rebasedSides(),
		"with one build nobody opens another build's directory")
	require.Equal(t, []compareSide{sideBase},
		compareImages{golden: "pg:a", baseline: "pg:b", candidate: "pg:a"}.rebasedSides())
	require.Equal(t, []compareSide{sideCandidate},
		compareImages{golden: "pg:a", baseline: "pg:a", candidate: "pg:b"}.rebasedSides())
	require.Equal(t, []compareSide{sideBase, sideCandidate},
		compareImages{golden: "pg:a", baseline: "pg:b", candidate: "pg:c"}.rebasedSides())
}

// THE REQUIREMENT-8 GUARD AT THIS LAYER. A run that names no image adds no
// sentence, so the block a film shows is the block a film shows.
func TestNoImageNamedAddsNoNote(t *testing.T) {
	t.Parallel()
	require.Empty(t, imageCompareNotes(&LoadCompareResult{
		Axis: AxisRevision, Rev: "aaa", CandidateRev: "bbb",
	}), "the default comparison must gain no sentence")
	require.Empty(t, imageCompareNotes(&LoadCompareResult{
		Axis: AxisRevision, GoldenImage: "pg:a", BaselineImage: "pg:a", CandidateImage: "pg:a",
	}), "a manifest image on both sides is still one build")
}

// Both axes moving WITHDRAWS the attribution rather than adding a caveat to it,
// and it has to say so in words a reader cannot skim past.
func TestBothAxesMovingSaysTheDifferenceCannotBeAttributed(t *testing.T) {
	t.Parallel()
	notes := imageCompareNotes(&LoadCompareResult{
		Axis: AxisBoth, Rev: "1111111111112222", CandidateRev: "3333333333334444",
		GoldenImage: "pg:a", BaselineImage: "pg:b", CandidateImage: "pg:c",
	})
	require.NotEmpty(t, notes)
	require.Contains(t, notes[0], "cannot be attributed to either one")
	require.Contains(t, notes[0], "pg:b")
	require.Contains(t, notes[0], "pg:c")
	require.Contains(t, notes[0], "11111111", "short() abbreviates to eight")
	require.Contains(t, notes[0], "33333333")
}

// One revision and two builds is the experiment, and the note says which fact
// makes it one: the same revision on both sides.
func TestOneRevisionAndTwoBuildsSaysTheDifferenceIsTheDatabases(t *testing.T) {
	t.Parallel()
	notes := imageCompareNotes(&LoadCompareResult{
		Axis: AxisImage, Rev: "aaaaaaaaaaaa", CandidateRev: "aaaaaaaaaaaa",
		GoldenImage: "pg:a", BaselineImage: "pg:a", CandidateImage: "pg:c",
	})
	require.NotEmpty(t, notes)
	require.Contains(t, notes[0], "same application revision")
	require.Contains(t, notes[0], "is the database's and not the application's")
}

// Who wrote the pages and who read them, which is the fact no latency in the
// report carries. Present whenever any side runs another build, INCLUDING the
// pairing the axis alone cannot describe: both sides on one build that is not
// the golden's.
func TestTheGoldensWriterAndItsReadersAreNamed(t *testing.T) {
	t.Parallel()
	one := imageCompareNotes(&LoadCompareResult{
		Axis: AxisImage, GoldenImage: "pg:a", BaselineImage: "pg:a", CandidateImage: "pg:c",
	})
	last := one[len(one)-1]
	require.Contains(t, last, "written by pg:a")
	require.Contains(t, last, "opened by pg:c")
	require.Contains(t, last, "this build")
	require.NotContains(t, last, "the base branch")

	both := imageCompareNotes(&LoadCompareResult{
		Axis: AxisRevision, GoldenImage: "pg:a", BaselineImage: "pg:b", CandidateImage: "pg:b",
	})
	require.Len(t, both, 1, "the axis is the revision, so only the writer note applies")
	require.Contains(t, both[0], "written by pg:a")
	require.Contains(t, both[0], "the base branch and this build")

	// An empty image is the stock one, which is an answer rather than a blank.
	stock := imageCompareNotes(&LoadCompareResult{
		Axis: AxisImage, GoldenImage: "", BaselineImage: "", CandidateImage: "pg:c",
	})
	require.Contains(t, stock[len(stock)-1], "the stock Postgres image")
}

// An image named against a provider that has no images would be read as a choice
// and used for nothing, which is refused rather than ignored. And the check
// itself does nothing at all, reaching no daemon, when no image was named: that
// is what makes the new flags free for every run that does not use them.
func TestAnImageIsRefusedForAProviderThatHasNone(t *testing.T) {
	t.Parallel()
	neon := &Orchestrator{opts: Options{Manifest: &schema.Manifest{Name: "ledger",
		Database: &schema.Database{Provider: schema.DBNeon, Project: "p"}}}}
	err := checkCompareImages(context.Background(), neon, compareImages{
		golden: "", baseline: "pg:b", candidate: "",
	})
	require.Error(t, err)
	var coded *aferrors.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, aferrors.AFLOD010, coded.Code())
	require.Contains(t, err.Error(), "neon")
	require.Contains(t, err.Error(), "no image to choose")

	// Nothing named, so nothing is checked and no provider is built. This
	// orchestrator has no manifest at all, so anything that tried to build one
	// would panic or error, and the nil is the proof.
	require.NoError(t, checkCompareImages(context.Background(), &Orchestrator{},
		compareImages{golden: "pg:a", baseline: "pg:a", candidate: "pg:a"}))
}

// A manifest naming no provider is the docker provider, here and in
// newDatabaseProvider alike, so an image named by such a manifest is not refused
// as belonging to a hosted provider.
func TestAManifestWithNoProviderIsDocker(t *testing.T) {
	t.Parallel()
	require.Equal(t, schema.DBDocker, declaredProvider(nil))
	require.Equal(t, schema.DBDocker, declaredProvider(&schema.Manifest{}))
	require.Equal(t, schema.DBDocker, declaredProvider(&schema.Manifest{Database: &schema.Database{}}))
	require.Equal(t, schema.DBNeon, declaredProvider(&schema.Manifest{
		Database: &schema.Database{Provider: schema.DBNeon}}))
}

// Two sides naming one image is one container to start, and the stock image is
// checked on its own path rather than here.
func TestOnlyTheDistinctNamedImagesAreChecked(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"pg:c", "pg:b"}, dedupeImages("pg:c", "pg:b"))
	require.Equal(t, []string{"pg:b"}, dedupeImages("pg:b", "pg:b"))
	require.Equal(t, []string{"pg:b"}, dedupeImages("", "pg:b"))
	require.Empty(t, dedupeImages("", ""))
}

// The notes reach the result through the same function the report reads, on both
// workloads. A sentence that existed and was never appended would be invisible
// exactly where it matters.
func TestTheImageNotesReachBothWorkloadsNotes(t *testing.T) {
	t.Parallel()
	for _, sql := range []bool{false, true} {
		r := &LoadCompareResult{
			SQL: sql, Axis: AxisImage, Rounds: 4, Golden: "gv_x",
			CandidateRev: "aaaaaaaaaaaa",
			GoldenImage:  "pg:a", BaselineImage: "pg:a", CandidateImage: "pg:c",
		}
		notes := strings.Join(loadCompareNotes(r), "\n")
		require.Contains(t, notes, "differed only in the database build",
			"sql=%v must carry the image note", sql)
	}
}
