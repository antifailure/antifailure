// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package feature_test

// The instrument for the catalogue, and the only reason the catalogue is worth
// having.
//
// A list of features with a note beside each saying where it is enforced is a
// comment. What makes it a control is that something opens the file the note
// names and refuses to pass when the claim is not true there. entitlements.ts
// says the same thing about its own catalogue and its test does the same thing,
// and admin/controls.ts says it a third time about operator switches. This is
// that pattern applied to the twelve names a licence can carry.
//
// Three claims are checked here and a fourth in ee/engine/cmd/af, which is the
// only place where every enterprise package's init has run and the registry is
// therefore populated. That split is not tidiness. A test in this package
// cannot import compliance, secrets or policyenforce without an import cycle,
// so the registry is empty here by construction and a check written here would
// silently pass on nothing.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
)

// eeEngine is the ee/engine directory, and repoRoot the repository root.
//
// From the test's own path rather than from the working directory, which for a
// Go test is the package directory and would break the moment this file moved.
func eeEngine(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	require.True(t, ok, "the test cannot locate its own source")
	return filepath.Dir(filepath.Dir(self))
}

func repoRoot(t *testing.T) string {
	t.Helper()
	return filepath.Dir(filepath.Dir(eeEngine(t)))
}

// symbolName is feature.SplitSite, which is the one definition of the format.
//
// A local alias rather than a second implementation. This test and the registry
// check in ee/engine/cmd/af read different things and have to read them the
// same way, and two parsers agreeing today is how they come to disagree later.
func symbolName(reference string) (file, symbol string, ok bool) {
	return feature.SplitSite(reference)
}

func TestEveryLicensedFeatureIsInTheCatalogue(t *testing.T) {
	t.Parallel()
	// The direction that catches a feature added to the licence and to nothing
	// else, which is how air_gapped came to be a word with no meaning: it was
	// added to the const block, to two documentation pages and to licensegen,
	// and nothing anywhere asks whether it is on.
	inCatalogue := map[license.Feature]bool{}
	for _, e := range feature.Catalogue() {
		require.False(t, inCatalogue[e.Feature],
			"%s appears twice in the catalogue", e.Feature)
		inCatalogue[e.Feature] = true
	}

	for _, f := range license.AllFeatures() {
		require.Truef(t, inCatalogue[f],
			"the licence sells %s and the entitlement catalogue does not mention it, so "+
				"nobody has written down what a customer without it gets. Add an entry to "+
				"ee/engine/feature/catalogue.go.", f)
	}
}

func TestTheCatalogueSellsNothingTheLicenceCannotCarry(t *testing.T) {
	t.Parallel()
	// The other direction. A catalogue entry for a feature no licence can grant
	// describes a product nobody can buy, and it is exactly as invisible as the
	// first case: both sides look complete from their own end.
	sellable := map[license.Feature]bool{}
	for _, f := range license.AllFeatures() {
		sellable[f] = true
	}
	for _, e := range feature.Catalogue() {
		require.Truef(t, sellable[e.Feature],
			"the catalogue has an entry for %s and no licence can carry that name, so it "+
				"describes something nobody can be sold", e.Feature)
	}
}

func TestAnEntryThatIsNotGatedSaysWhyOutLoud(t *testing.T) {
	t.Parallel()
	// Reported and not enforced is a legitimate state. An UNDOCUMENTED reported
	// and not enforced is indistinguishable from somebody forgetting the check,
	// and this is the assertion that keeps the two apart.
	for _, e := range feature.Catalogue() {
		require.NotEmptyf(t, e.Summary, "%s has no summary", e.Feature)
		switch e.State {
		case feature.StateGated:
			require.NotEmptyf(t, e.EnforcedAt,
				"%s is marked gated and names no enforcement site", e.Feature)
		case feature.StatePlanWide, feature.StateFree, feature.StateAbsent,
			feature.StateUnmounted:
			require.Emptyf(t, e.EnforcedAt,
				"%s names an engine enforcement site and is not marked gated, which is the "+
					"one combination that cannot be true: a site that refuses IS a gate",
				e.Feature)
			require.NotEmptyf(t, e.Because,
				"%s is not gated and does not say why, so a reader cannot tell a deliberate "+
					"decision from a missing check", e.Feature)
		default:
			t.Fatalf("%s has state %q, which is not one of the four", e.Feature, e.State)
		}
	}
}

func TestAGatedEntryNamesAFileThatChecksThatExactFeature(t *testing.T) {
	t.Parallel()
	// The claim that matters, and the reason the reference is path:symbol
	// rather than a name.
	//
	// A bare name proves only that SOME file declares one, which is the
	// sentence admin/controls.ts writes above enforcedBy. This goes one further
	// and requires the named file to contain a call to Enabled naming THIS
	// feature, because a site copied from a neighbouring feature declares the
	// right symbol in the right file and gates on the wrong entitlement. That
	// is not hypothetical here: compliance_packs was declared at
	// "ee/engine/compliance.Pack.Evaluate" for its whole life, and Pack.Evaluate
	// takes no context and therefore cannot ask about a licence at all. The
	// string agreed with itself and with nothing else.
	root := eeEngine(t)
	for _, e := range feature.Catalogue() {
		if e.State != feature.StateGated {
			continue
		}
		file, symbol, ok := symbolName(e.EnforcedAt)
		require.Truef(t, ok, "%s has EnforcedAt %q, which is not path:symbol",
			e.Feature, e.EnforcedAt)

		source, err := os.ReadFile(filepath.Join(root, file))
		require.NoErrorf(t, err,
			"%s says it is enforced in %s and that file cannot be read", e.Feature, file)

		// DEFINED in that file, not merely mentioned in it. Contributed by
		// L7.1, which hit the gap while adopting this format: a site can name a
		// file that contains the Enabled literal for its own reasons and
		// defines the named symbol somewhere else entirely, and a word grep
		// accepts it. compliance/command.go is the live example, because it
		// calls Packs() three times and Packs is declared in frameworks.go.
		//
		// The optional receiver group is the general case this had to grow.
		// Two of the three sites are methods, so the pattern accepts both
		// `func Command(` and `func (s *Source) Available(`.
		require.Regexpf(t,
			regexp.MustCompile(`func (\([^)]*\) )?`+regexp.QuoteMeta(symbol)+`\(`),
			string(source),
			"%s says it is enforced at %s and %s does not DEFINE %s. A file that merely "+
				"mentions the symbol can be a file whose gate lives somewhere else.",
			e.Feature, e.EnforcedAt, file, symbol)

		// The literal call, spelled with `ctx`, which is a real limit and a
		// deliberate one. A site that named its context something else would
		// fail this while being perfectly correct. That is the direction to
		// fail in: a false refusal is noisy and safe, is fixed by following the
		// convention every other call in this module already follows, and the
		// alternative is a looser match that would accept a file gating on a
		// neighbouring feature. tools/figurecheck reaches the same conclusion
		// about its own denylist in the same words.
		constant := featureConstant(t, e.Feature)
		require.Containsf(t, string(source),
			"feature.Enabled(ctx, license."+constant+")",
			"%s says it is enforced at %s and %s contains no "+
				"feature.Enabled(ctx, license.%s) call, so whatever that file does it does "+
				"not consult this entitlement",
			e.Feature, e.EnforcedAt, file, constant)
	}
}

func TestEveryControlPlaneSiteNamesSomethingThatIsStillThere(t *testing.T) {
	t.Parallel()
	// The second of the three places an entitlement answer lives. The engine is
	// the first and the documentation is the third, and the three drift because
	// nothing has ever compared them.
	//
	// This is the Go half. The TypeScript half is in
	// web/apps/api/test/licensed-features.test.ts and reads this same catalogue,
	// so a control plane developer who renames a symbol and never runs the Go
	// suite still sees it fail.
	src := filepath.Join(repoRoot(t), "web", "apps", "api", "src")
	for _, e := range feature.Catalogue() {
		if e.ControlPlaneAt == "" {
			continue
		}
		file, symbol, ok := symbolName(e.ControlPlaneAt)
		require.Truef(t, ok, "%s has ControlPlaneAt %q, which is not path:symbol",
			e.Feature, e.ControlPlaneAt)

		source, err := os.ReadFile(filepath.Join(src, file))
		require.NoErrorf(t, err,
			"%s names %s in the control plane and that file cannot be read",
			e.Feature, e.ControlPlaneAt)

		require.Regexpf(t, regexp.MustCompile(`\b`+regexp.QuoteMeta(symbol)+`\b`), string(source),
			"%s names %s in the control plane and %s declares no %s",
			e.Feature, e.ControlPlaneAt, file, symbol)
	}
}

func TestTheMeasuredNumberIsPublishedRatherThanAsserted(t *testing.T) {
	t.Parallel()
	// The lane's number, printed rather than hidden behind a threshold. A test
	// that asserted "at least three" would go on passing while the product
	// stood still, and a number nobody can see is a number nobody checks.
	//
	// It reads len(license.AllFeatures()) rather than a literal so that a lane
	// adding a feature moves the denominator and not this file.
	total := len(license.AllFeatures())
	gated := feature.GatedFeatures()

	byState := map[feature.State]int{}
	for _, e := range feature.Catalogue() {
		byState[e.State]++
	}

	t.Logf("licensed features with an enforcement site that refuses: %d of %d", len(gated), total)
	for _, f := range gated {
		entry, _ := feature.Of(f)
		t.Logf("  gated     %-22s %s", f, entry.EnforcedAt)
	}
	for _, e := range feature.Catalogue() {
		if e.State != feature.StateGated {
			t.Logf("  %-9s %-22s %s", e.State, e.Feature, firstSentence(e.Because))
		}
	}
	t.Logf("gated %d, plan wide %d, free %d, unmounted %d, absent %d",
		byState[feature.StateGated], byState[feature.StatePlanWide],
		byState[feature.StateFree], byState[feature.StateUnmounted],
		byState[feature.StateAbsent])

	require.Equal(t, total, byState[feature.StateGated]+byState[feature.StatePlanWide]+
		byState[feature.StateFree]+byState[feature.StateUnmounted]+
		byState[feature.StateAbsent],
		"the states do not account for every licensed feature")
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

// featureConstant is the Go identifier for a feature's wire name.
//
// Derived from license.go rather than from a second table, because a second
// table is the thing this whole file exists to prevent. The source is parsed
// for `FeatureX Feature = "x"` and the mapping comes back from that.
func featureConstant(t *testing.T, f license.Feature) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(eeEngine(t), "license", "license.go"))
	require.NoError(t, err)
	matches := regexp.MustCompile(`(Feature\w+)\s+Feature\s+=\s+"([a-z_]+)"`).
		FindAllStringSubmatch(string(source), -1)
	require.NotEmpty(t, matches, "license.go declares no feature constants, so the parse is wrong")
	for _, m := range matches {
		if m[2] == string(f) {
			return m[1]
		}
	}
	t.Fatalf("license.go declares no constant whose value is %q", f)
	return ""
}
