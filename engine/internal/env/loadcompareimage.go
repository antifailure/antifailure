package env

// COMPARING TWO BUILDS OF THE DATABASE, RATHER THAN TWO BUILDS OF THE
// APPLICATION.
//
// WHAT WAS UNREACHABLE AND WHY IT WAS DELIBERATE. The baseline mechanism this
// comparison is built on holds everything constant except the source being
// compiled: the candidate's manifest, the candidate's secrets, the candidate's
// ports, and only the build context and the branch name differ. Its own comment
// says why, and the reason is exactly right: anything else taken from the base
// checkout would let a manifest change move the harness and the application at
// once, and then no difference in the report could be attributed to either.
// database.image lives in that manifest. So both sides of every comparison ran
// one database build, and the experiment a storage engine developer actually
// wants, the same workload and the same rows on two builds of THEIR DATABASE,
// could not be expressed at all.
//
// THE INSIGHT THAT MAKES IT AN EXPERIMENT RATHER THAN A CONFOUNDED READING, and
// it is the same reasoning applied one level out. When only the images differ,
// the two sides must run the SAME APPLICATION REVISION. Before this, a baseline
// resolving to the candidate's own commit was refused outright, so passing a
// baseline image would have dragged in a different application build from the
// base revision's tree as well: both axes would move, and no difference in the
// report could be attributed to either. The refusal is therefore conditional
// now. Same commit and same image is still nothing to compare. Same commit and
// two images is the experiment.
//
// AND THE SAME COMMIT IS BUILT FROM THE SAME TREE, not from an export of it.
// The candidate builds its images from the working tree and the baseline
// ordinarily builds from a clean export of the base revision. With the two
// revisions equal, exporting would reintroduce the difference this is trying to
// remove: an uncommitted edit would be in one side's application and not the
// other's. So on the same commit path the baseline is given the candidate's own
// build root, and the two application images are then the same images.
//
// ONE GOLDEN, AND ONE BUILD WROTE IT. There is one data directory because two
// would mean the two sides answered queries over different rows. It was written
// by whichever build the golden was built on, which is the one database.image
// names, and a side asking for a different build OPENS it. That asymmetry is the
// experiment rather than a compromise, and when the opening fails it is the most
// useful thing this tool can report; see AF-DB-044 and the rebase path in the
// docker provider.

import (
	"context"
	"errors"
	"fmt"

	aferrors "github.com/antifailure/antifailure/engine/internal/errors"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

// CompareAxis is what differed between the two sides of a comparison.
//
// Reported rather than left to be inferred. A reader looking at a table of
// latencies has no way to tell a run that varied the application from one that
// varied the database, and the two answer different questions; a reader who
// guesses wrong attributes a storage regression to their own code or the other
// way round.
type CompareAxis string

const (
	// AxisRevision is the comparison that has always existed: two application
	// revisions over one database build.
	AxisRevision CompareAxis = "revision"
	// AxisImage is one application revision over two database builds.
	AxisImage CompareAxis = "image"
	// AxisBoth is both at once, which is a reading rather than an experiment:
	// a difference cannot be attributed to either axis, and the notes say so
	// in those words.
	AxisBoth CompareAxis = "both"
)

// compareImages is which database build each side runs, and which one wrote the
// golden they share.
type compareImages struct {
	// baseline and candidate are the resolved images, each defaulting to the
	// manifest's database.image, so a run that names neither has both equal to
	// what every run before this one used.
	baseline  string
	candidate string
	// golden is the image the golden was built on, which is the manifest's.
	// Named separately because it is neither side's choice and because it is
	// the answer to "which build wrote these pages".
	golden string
	axis   CompareAxis
}

// varied reports whether the two sides run different database builds.
func (c compareImages) varied() bool { return c.baseline != c.candidate }

// rebasedSides names the sides that open a data directory another build wrote,
// which is every side whose image is not the one the golden was built on.
func (c compareImages) rebasedSides() []compareSide {
	var out []compareSide
	if c.baseline != c.golden {
		out = append(out, sideBase)
	}
	if c.candidate != c.golden {
		out = append(out, sideCandidate)
	}
	return out
}

// resolveCompareImages settles both sides' database builds and the axis.
//
// revisionVaried is passed in rather than recomputed because the caller has
// already resolved the base revision and this must not be able to disagree with
// it: two places deciding whether the revisions differ is one place to decide it
// differently, and the axis would then contradict the commits printed beside it.
func resolveCompareImages(
	m *schema.Manifest, opts LoadCompareOptions, revisionVaried bool,
) compareImages {
	declared := databaseImage(m)
	out := compareImages{
		golden:    declared,
		baseline:  orDefaultImage(opts.BaselineImage, declared),
		candidate: orDefaultImage(opts.Image, declared),
	}
	switch {
	case revisionVaried && out.varied():
		out.axis = AxisBoth
	case out.varied():
		out.axis = AxisImage
	default:
		out.axis = AxisRevision
	}
	return out
}

func orDefaultImage(chosen, declared string) string {
	if chosen != "" {
		return chosen
	}
	return declared
}

// baselineBuildRoot is the tree the base side's application is compiled from,
// with a function that removes it if it was written for this run.
//
// AN EXPORT ONLY WHEN THE REVISIONS DIFFER, and this is the half of the image
// axis a reader would not think of. The candidate compiles from the working
// tree and the baseline ordinarily compiles from a clean archive of the base
// revision, which is right when the two revisions differ and is the confound
// itself when they do not: an uncommitted edit would be in one side's
// application and not the other's, so the two application images would differ
// on the one path whose whole purpose is that they do not. With the revisions
// equal the base side is therefore given the candidate's own build root, and
// the two application images become the same image.
//
// A FUNCTION RATHER THAN FOUR LINES INSIDE LoadCompare, because the property is
// load bearing and was unreachable by any test while it lived there: proving it
// through LoadCompare needs a provider, a runtime, a golden and two container
// builds, and what is actually being claimed is a choice between two trees.
// TestTheBaseSideCompilesTheCandidatesOwnTreeOnOneRevision is what says no to
// removing the condition, and it says it by looking for an uncommitted file in
// the tree that comes back.
func (o *Orchestrator) baselineBuildRoot(
	ctx context.Context, rev string, revisionVaried bool,
) (string, func(), error) {
	if !revisionVaried {
		// buildRoot() rather than opts.BuildRoot, which is what the four lines
		// this replaced said. The two are equivalent by accident and not by
		// statement: an empty BuildRoot means Root, and baselineOrchestrator
		// copies the candidate's options, so an empty tree handed to it resolved
		// to the candidate's Root anyway. Naming the resolved path says what is
		// meant, and it is what lets a test assert an answer rather than an
		// empty string that happens to mean the right thing later.
		return o.buildRoot(), func() {}, nil
	}
	return o.baselineTree(ctx, rev)
}

// ErrLoadBaselineNothingVaried is returned when neither axis differs.
//
// It replaces a refusal that only knew about one axis. The old message said the
// base and this change were the same commit, which was the whole truth while
// the database build could not vary and is half of it now: a caller who typed
// the same image on both sides, or none at all, on a branch level with its base
// has varied nothing, and being told only about the commit sends them to look at
// the wrong flag.
var ErrLoadBaselineNothingVaried = errors.New(
	"the base and this change are the same commit and both sides would run the same " +
		"database image, so there are not two builds to compare; vary the revision with " +
		"--baseline, or the database build with --image or --baseline-image")

// ErrLoadBaselineSameCommit is the name the previous refusal had.
//
// Kept as an alias rather than deleted, because it is the sentinel `af load
// compare` matches on to report "nothing to compare" as a skip rather than as a
// failure, and an exported name that other trees may match on does not get to
// change meaning quietly. The two are one value, so errors.Is answers the same
// for either.
var ErrLoadBaselineSameCommit = ErrLoadBaselineNothingVaried

// checkCompareImages refuses a pairing that cannot mean what it says, before
// either environment is built.
//
// BEFORE, and that word is the requirement rather than a preference. Every check
// here is cheap and every one of them is about a choice already made on the
// command line, while building two environments and two goldens costs minutes.
// A refusal that arrives after them has told somebody something they could have
// been told at once, and charged them for it.
//
// Three refusals:
//
// An image named against a provider that has no images. database.image is the
// docker provider's key: a Neon branch or a Supabase branch is a database
// somebody else runs and there is no container to choose. Accepting the flag
// there would read as a choice and be used for nothing, which is the shape this
// repository keeps finding and calling dead.
//
// A major version mismatch, checked through the provider's own comparison
// against a throwaway container rather than against a tag, because the golden is
// ONE data directory: a build of another major cannot open it, so the honest
// refusal is now and not after the copy.
//
// An image lacking an extension the manifest declares is refused too, by the
// same machinery and under the same code the golden path uses, because the
// extension check runs inside the provider's own image check.
func checkCompareImages(ctx context.Context, o *Orchestrator, imgs compareImages) error {
	if !imgs.varied() && imgs.rebasedSides() == nil {
		// Nothing was named, so nothing new can be wrong and nothing is
		// started. This is the path every existing run takes.
		return nil
	}
	if kind := declaredProvider(o.opts.Manifest); kind != schema.DBDocker {
		return aferrors.Coded(aferrors.AFLOD010, "detail",
			"--image and --baseline-image choose the container a database build runs in, and "+
				"database.provider is "+string(kind)+", which runs databases somebody else "+
				"hosts and has no image to choose; compare two database builds under the "+
				"docker provider, or drop the flag")
	}
	prov, err := o.newDatabaseProvider(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = prov.Close() }()
	checker, ok := prov.(imageChecker)
	if !ok {
		// Unreachable through the switch above, and checked rather than
		// asserted blind: an extension registering a provider called docker is
		// refused by the reserved names, but a future provider gaining the
		// docker kind without this method would otherwise panic here.
		return aferrors.Coded(aferrors.AFLOD010, "detail",
			"this build's database provider cannot check an image before it is used, so "+
				"--image and --baseline-image cannot be honoured safely")
	}
	// Both sides, and in this order, so that a person who named one image sees
	// the refusal about the one they named rather than about the manifest's.
	//
	// The manifest's own image is checked too when it is one of the two, and
	// that is not waste: it is the build that WROTE the golden, and a mismatch
	// between it and database.version is the same defect pointing the other way.
	for _, image := range dedupeImages(imgs.candidate, imgs.baseline) {
		if err := checker.CheckImage(ctx, image); err != nil {
			return err
		}
	}
	return nil
}

// imageChecker is the provider's own "may this image be the database this
// manifest declares" check.
//
// An optional interface rather than a method on provider.Database, because it is
// a question only an image backed provider can answer and adding it to the
// interface would make every hosted provider implement a method that returns nil
// for want of anything to look at.
type imageChecker interface {
	CheckImage(ctx context.Context, image string) error
}

// declaredProvider is the provider kind the manifest asks for, with the same
// default newDatabaseProvider applies, so a manifest that names none is treated
// as docker here and there alike.
func declaredProvider(m *schema.Manifest) schema.DBProvider {
	if m == nil || m.Database == nil || m.Database.Provider == "" {
		return schema.DBDocker
	}
	return m.Database.Provider
}

// dedupeImages keeps the order and drops the empty and the repeated. Two sides
// naming one image is one container to start, and the stock image, which is what
// an empty string means, is checked by the provider on its own path.
func dedupeImages(images ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, image := range images {
		if image == "" || seen[image] {
			continue
		}
		seen[image] = true
		out = append(out, image)
	}
	return out
}

// imageCompareNotes are the sentences this comparison owes a reader when the
// database build varied.
//
// WRITTEN FOR THIS CASE RATHER THAN COPIED, because the existing notes are about
// two applications over one database and every one of them is still true and
// none of them says the thing that matters here. Appended rather than inserted
// so that a reader of the existing block sees what they saw before, with more
// after it.
//
// Nothing is added when no image was named, which is what keeps the report every
// existing run prints identical to the character.
func imageCompareNotes(r *LoadCompareResult) []string {
	// NO EARLY RETURN FOR "NOTHING WAS NAMED", and its absence is deliberate.
	// There was one, and mutation testing showed it could be deleted with every
	// test still green, because it could never change the answer: the switch
	// below names only the two axes that involve an image, and the writer note
	// below that requires a side running a build other than the golden's. With
	// one build both conditions are already false. A guard that cannot fail is
	// the dead code this repository keeps finding, and keeping it would have
	// left the property looking guarded while the structure was what carried it.
	//
	// So the property is structural, and the thing that would break it is a
	// default case added to the switch. WHICH TESTS SAY NO TO THAT WAS MEASURED
	// RATHER THAN ASSUMED, and the first answer written here was wrong: it named
	// TestNoImageNamedAddsNoNote and called it the only one. Adding a default
	// case that appends a sentence turns TWO tests red.
	// TestNoImageNamedAddsNoNote fails on a run that named no image at all, and
	// TestTheGoldensWriterAndItsReadersAreNamed fails because its revision axis
	// case requires exactly one note and gets two. Neither is redundant: only
	// the first covers the input where all three images are the golden's, which
	// is every run that existed before these flags, and only the second covers
	// a revision comparison where some side is on another build.
	imgs := compareImages{
		baseline: r.BaselineImage, candidate: r.CandidateImage, golden: r.GoldenImage,
	}
	var notes []string
	switch r.Axis {
	case AxisBoth:
		// The one note in this file that withdraws a claim rather than adding
		// one. Two axes moved, so the numbers are a reading and not a
		// measurement of either, and saying which axis a difference belongs to
		// is not something this run is entitled to do.
		notes = append(notes, fmt.Sprintf("the application revision AND the database build "+
			"both differ between the two sides, %s on %s against %s on %s, so a difference in "+
			"these numbers cannot be attributed to either one; to attribute it, run the "+
			"comparison twice more varying one axis at a time",
			short(r.Rev), ImageWords(r.BaselineImage), short(r.CandidateRev),
			ImageWords(r.CandidateImage)))
	case AxisImage:
		notes = append(notes, fmt.Sprintf("both sides ran the same application revision %s, "+
			"built from the same tree, and differed only in the database build, %s against %s, "+
			"so a difference in these numbers is the database's and not the application's",
			short(r.CandidateRev), ImageWords(r.BaselineImage), ImageWords(r.CandidateImage)))
	}
	// Who wrote the pages, and who read them. True whenever any side runs a
	// build other than the one the golden was built on, including the case
	// where both sides name the same other build, which is a pairing the axis
	// alone cannot describe.
	if sides := imgs.rebasedSides(); sides != nil {
		notes = append(notes, fmt.Sprintf("the golden's data directory was written by %s and "+
			"opened by %s, so %s read pages another build laid out; a build that could not "+
			"open it at all would have been reported as a finding rather than as a slow "+
			"round, and one that opened it is being measured partly on how well it reads "+
			"another build's layout",
			ImageWords(r.GoldenImage), sideImageWords(sides, r), openerWords(sides)))
	}
	return notes
}

// ImageWords names a build in a sentence. An empty image is the stock Postgres
// image the declared major builds, which is a real answer and not a blank.
//
// Exported because the report renders the same fact in the terminal and a second
// spelling of "the stock image" in the command layer is a second thing to keep in
// step with this one.
func ImageWords(image string) string {
	if image == "" {
		return "the stock Postgres image for the declared major version"
	}
	return image
}

// sideImageWords lists the images the given sides opened the golden with.
func sideImageWords(sides []compareSide, r *LoadCompareResult) string {
	seen := map[string]bool{}
	var out []string
	for _, side := range sides {
		image := r.BaselineImage
		if side == sideCandidate {
			image = r.CandidateImage
		}
		if seen[image] {
			continue
		}
		seen[image] = true
		out = append(out, ImageWords(image))
	}
	return joinWords(out)
}

// openerWords says which sides did the opening, in the words the rest of the
// report uses for them.
func openerWords(sides []compareSide) string {
	out := make([]string, 0, len(sides))
	for _, side := range sides {
		out = append(out, side.String())
	}
	return joinWords(out)
}

func joinWords(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return parts[0] + " and " + joinWords(parts[1:])
}
