package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/env"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// WHAT `af load compare --image` AND `--baseline-image` PRINT AND PUBLISH.
//
// The pinned report in loadcompare_pin_test.go is the other half of this file and
// the two have to be read together: that one proves a comparison with no image
// named prints exactly what the film shows, and these prove that a comparison
// with one named says which axis moved. Neither is worth much alone. A report
// that never changed would be one where the new flags do nothing, and a report
// that says the axis on every run would disagree with the film.

// The flags exist, they default to empty, and empty means "the manifest's
// database.image" one layer down. A flag with a non empty default would hand the
// engine a default dressed as a choice and make database.image unreachable, which
// is the defect changedInt was written for.
func TestTheImageFlagsDefaultToTheManifest(t *testing.T) {
	t.Parallel()
	cmd := newLoadCompareCommand(&Env{})
	for _, name := range []string{"image", "baseline-image"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "--%s must exist", name)
		require.Equal(t, "", f.DefValue,
			"--%s must default to empty, which the engine reads as the manifest's image", name)
		require.False(t, cmd.Flags().Changed(name))
	}
	require.NoError(t, cmd.ParseFlags([]string{"--baseline-image", "postgres:17-alpine"}))
	require.True(t, cmd.Flags().Changed("baseline-image"))
	require.Equal(t, "postgres:17-alpine", cmd.Flags().Lookup("baseline-image").Value.String())
}

// THE REQUIREMENT THE FILM IMPOSES. A result carrying no image adds no line, so
// the provenance block is the two lines it always was. Asserted on the lines
// themselves rather than on the whole report, because the whole report is pinned
// in the test beside this one and a second copy of that block would be a second
// thing to update.
func TestNoImageNamedPrintsNoAxisLine(t *testing.T) {
	t.Parallel()
	require.Nil(t, comparisonAxisLines(&env.LoadCompareResult{
		Rev: "aaa", CandidateRev: "bbb", Axis: env.AxisRevision,
	}))
	require.Nil(t, comparisonAxisLines(&env.LoadCompareResult{
		Axis: env.AxisRevision,
		// A manifest that names an image is still one build on both sides.
		GoldenImage: "pgvector/pgvector:pg17", BaselineImage: "pgvector/pgvector:pg17",
		CandidateImage: "pgvector/pgvector:pg17",
	}))
}

// Which axis differed, in the terminal, for each of the three answers. A reader
// looking at a table of latencies has no other way to tell a database regression
// from an application one.
func TestTheReportNamesTheAxisThatDiffered(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		res  *env.LoadCompareResult
		want []string
		gone []string
	}{
		{
			name: "one revision and two database builds",
			res: &env.LoadCompareResult{
				Axis: env.AxisImage, GoldenImage: "postgres:17-alpine",
				BaselineImage: "postgres:17-alpine", CandidateImage: "pgvector/pgvector:pg17",
			},
			want: []string{
				"the axis that differed is the database build, postgres:17-alpine against " +
					"pgvector/pgvector:pg17, on one application revision",
				"the golden was made on postgres:17-alpine, so a side on another build " +
					"opened a data directory it did not write",
			},
		},
		{
			name: "both axes, which is a reading rather than an experiment",
			res: &env.LoadCompareResult{
				Axis: env.AxisBoth, GoldenImage: "postgres:17-alpine",
				BaselineImage: "postgres:17-alpine", CandidateImage: "pgvector/pgvector:pg17",
			},
			want: []string{
				"BOTH axes differed, the revision and the database build, " +
					"postgres:17-alpine against pgvector/pgvector:pg17",
				"the golden was made on postgres:17-alpine, so a side on another build " +
					"opened a data directory it did not write",
			},
			gone: []string{"on one application revision"},
		},
		{
			name: "the revision, with both sides on a build the golden was not made on",
			res: &env.LoadCompareResult{
				Axis: env.AxisRevision, GoldenImage: "",
				BaselineImage: "pgvector/pgvector:pg17", CandidateImage: "pgvector/pgvector:pg17",
			},
			want: []string{
				"the axis that differed is the revision, and both sides ran the database " +
					"build pgvector/pgvector:pg17",
				"the golden was made on the stock Postgres image for the declared major " +
					"version, so a side on another build opened a data directory it did not write",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := comparisonAxisLines(c.res)
			require.Len(t, got, len(c.want))
			for i, want := range c.want {
				require.Equal(t, want, got[i])
			}
			for _, gone := range c.gone {
				for _, line := range got {
					require.NotContains(t, line, gone)
				}
			}
		})
	}
}

// The provenance block reaches the terminal through the renderer, on both
// workloads. A line that existed and was never printed would be invisible
// exactly where it matters, which is the shape this repository keeps calling
// dead.
func TestTheAxisLineIsActuallyPrintedByTheReport(t *testing.T) {
	t.Parallel()
	res := &env.LoadCompareResult{
		Rev: "1111111111112222", CandidateRev: "1111111111112222",
		How: "the merge base with origin/main", Axis: env.AxisImage,
		GoldenImage: "postgres:17-alpine", BaselineImage: "postgres:17-alpine",
		CandidateImage: "pgvector/pgvector:pg17",
	}
	var buf bytes.Buffer
	e := &Env{Out: NewOutput(&buf, &buf)}
	renderLoadComparison(e, res, &workload.Comparison{Kind: workload.ObservedLoad}, nil,
		workload.VerdictPass)
	out := buf.String()
	require.Contains(t, out, "  111111111111 against 111111111111\n")
	require.Contains(t, out, "  the axis that differed is the database build")
	require.Contains(t, out, "  the golden was made on postgres:17-alpine")
}

// The document says the axis on EVERY run, unlike the terminal, because a reader
// there is a program: an absent key meaning "revision" by convention is a
// convention somebody has to know, and a field is not.
func TestTheDocumentAlwaysCarriesTheAxisAndBothSidesBuilds(t *testing.T) {
	t.Parallel()
	c := &workload.Comparison{Kind: workload.ObservedLoad}

	plain := loadCompareDoc(&env.LoadCompareResult{
		Rev: "aaa", CandidateRev: "bbb", Axis: env.AxisRevision,
	}, c, nil, workload.VerdictPass, nil)
	body, err := json.Marshal(plain)
	require.NoError(t, err)
	require.Contains(t, string(body), `"axis":"revision"`)

	// ALWAYS present, which is a claim about the key and not about its value, so
	// the only way to observe it is a document whose axis is empty. Mutation
	// testing is what asked for this assertion: adding omitempty to the tag left
	// every other assertion here green, because "revision" is not the empty
	// string, and the contract a program relies on is that the key is there to
	// read at all.
	empty, err := json.Marshal(loadCompareDoc(&env.LoadCompareResult{},
		c, nil, workload.VerdictPass, nil))
	require.NoError(t, err)
	require.Contains(t, string(empty), `"axis":""`,
		"the axis must be a field rather than a convention, so it may not be omitted")
	// Absent rather than empty for a run that named no image, so nothing a
	// reader of the existing document parses gained a key with no meaning.
	require.NotContains(t, string(body), `"image"`)
	require.NotContains(t, string(body), `"golden_image"`)

	varied := loadCompareDoc(&env.LoadCompareResult{
		Rev: "aaa", CandidateRev: "aaa", Axis: env.AxisImage,
		GoldenImage: "postgres:17-alpine", BaselineImage: "postgres:17-alpine",
		CandidateImage: "pgvector/pgvector:pg17",
	}, c, nil, workload.VerdictPass, nil)
	require.Equal(t, "image", varied.Axis)
	require.Equal(t, "postgres:17-alpine", varied.GoldenImage)
	require.Equal(t, "postgres:17-alpine", varied.Baseline.Image)
	require.Equal(t, "pgvector/pgvector:pg17", varied.Candidate.Image)

	// And the shape a reader of the old document already parses is untouched:
	// the two sides still carry rev and how under those names.
	require.Equal(t, "aaa", varied.Baseline.Rev)
	require.Equal(t, "aaa", varied.Candidate.Rev)
}

// The skip for a comparison that varies nothing names both axes, because on that
// path both of them are level and a hint naming only the commit sends somebody
// who meant to compare two database builds to the wrong flag.
func TestTheNothingToCompareHintNamesBothAxes(t *testing.T) {
	t.Parallel()
	require.Contains(t, env.ErrLoadBaselineNothingVaried.Error(), "--baseline-image")
	require.Contains(t, env.ErrLoadBaselineNothingVaried.Error(), "same commit")
}
