// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package feature_test

// The third place an entitlement answer lives.
//
// The engine is the first, the control plane is the second, and this is the
// documentation. All three drifted because nothing had ever compared them, and
// the documentation is the one that drifts worst: it is the copy a customer
// reads, a security review quotes and a salesperson repeats, and it is the only
// one of the three that nothing compiles.
//
// What the page said before this existed was twelve names and one sentence:
// "Each is named in the license, so a license permits exactly what was bought."
// The names were right and the sentence was not. Nine of the twelve are named
// in a licence and permit nothing either way, because nothing anywhere asks
// whether they are on, so a customer who bought nine features and one who
// bought none were running the identical product. That is not a wording
// problem. It is the product's own claim about itself being untrue, in the one
// document written to answer exactly that question.
//
// So the table is generated from the catalogue and this test refuses a page
// that has fallen behind it, the same way the transform reference is generated
// from the masking registry. The prose around the markers is left alone,
// because a generator has nothing to say about why any of it is the case.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
)

var updateEntitlements = flag.Bool("update-entitlements", false,
	"rewrite the entitlement table in the licensing page")

const (
	licensingPath     = "../../../docs/src/content/docs/enterprise/licensing.md"
	entitlementsStart = "<!-- entitlements:start -->"
	entitlementsEnd   = "<!-- entitlements:end -->"
	countStart        = "<!-- entitlement-count:start -->"
	countEnd          = "<!-- entitlement-count:end -->"
	namesStart        = "<!-- entitlement-names:start -->"
	namesEnd          = "<!-- entitlement-names:end -->"
)

// effect is the column that decides whether the page is worth reading: what a
// customer who does NOT have this feature gets.
//
// One sentence per state rather than per feature, because the state is the
// answer and a per feature sentence would be a second place to say the same
// thing. The detail lives in the catalogue's Because and in the source.
func effect(e feature.Entitlement) string {
	switch e.State {
	case feature.StateGated:
		if e.ControlPlaneAt != "" {
			return "Withheld by both the engine at `" + e.EnforcedAt + "` and the control plane at `" + e.ControlPlaneAt + "`. Each checks the license."
		}
		// "Withheld" rather than "refused", because for one of the three the
		// licensed behaviour IS a refusal: an unlicensed policy hook permits.
		// "Refused" in that row would read as the environment being refused,
		// which is the opposite of what happens.
		return "Withheld. `" + e.EnforcedAt + "` asks the license, and the feature is off " +
			"when the answer is no."
	case feature.StateFree:
		return "Nothing changes. It is implemented and deliberately available to everyone."
	case feature.StateAbsent:
		return "Nothing changes, because the capability is not built yet."
	case feature.StateEditionGated:
		// Named as a refusal like the other two, because to a customer all
		// three are the same event: they asked for something and were told no
		// on the strength of what they bought. Which process said no, and
		// through which mechanism, is our detail and not theirs.
		return "Withheld. `" + e.EnforcedAt + "` asks the license, and the feature is off " +
			"when the answer is no."
	case feature.StateControlPlaneGated:
		// Named as a refusal, like StateGated, because to a customer the two
		// are the same event: they asked for something and were told no on the
		// strength of what they bought. Which process said no is our detail,
		// not theirs. The code is named because 402 rather than 404 is the
		// whole point of gate.ts, and a reader who greps the engine for this
		// feature and finds nothing needs the sentence that explains why.
		return "Withheld by the control plane. `" + e.ControlPlaneAt + "` asks the license, " +
			"and an unlicensed installation is answered 402 naming the feature rather " +
			"than 404."
	case feature.StateUnmounted:
		return "Nothing changes, and not because it is unbuilt. It is implemented and no " +
			"binary loads it, so nobody has it, paid or not."
	}
	return ""
}

func entitlementTable() string {
	var b strings.Builder
	b.WriteString("| Feature | What it is | Without it |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, e := range feature.Catalogue() {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", e.Feature, e.Summary, effect(e))
	}
	return b.String()
}

// namesSentence is the list of names itself, in the sentence a reader meets
// before the table.
//
// Generated for the reason countSentence gives, and for one more that is
// particular to this sentence. The control plane's catalogue test compares the
// page against the licence by reading a bounded window under this heading,
// because reading the whole page would pick up every name mentioned in prose
// anywhere on it and would pass a page that had stopped listing the catalogue
// at all. The table does not fit in that window and cannot be made to:
// fourteen rows are just under three thousand characters. So the window needs a
// list, and a list typed by hand under a generated table is a fourth copy of
// the fourteen names, sitting inside the one document whose subject is what
// happens when the copies disagree.

func namesSentence() string {
	all := license.AllFeatures()
	if len(all) == 0 {
		return "A license carries no features.\n"
	}
	names := make([]string, 0, len(all))
	for _, f := range all {
		names = append(names, "`"+string(f)+"`")
	}
	list := names[0]
	if len(names) > 1 {
		list = strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	return "The features a license can name are " + list + ".\n"
}

// countSentence is the number, on the page, in the words a customer would use.
//
// Generated rather than typed, for the reason the masking reference gives about
// its uniqueness sentence: a sentence that restates a fact the registry already
// holds is not prose, whatever it looks like, and left by hand beside a
// generated table it is how a page comes to contradict itself.
func countSentence() string {
	engine := len(feature.GatedFeatures()) + len(feature.EditionGatedFeatures())
	plane := len(feature.ControlPlaneGatedFeatures())
	total := len(license.AllFeatures())
	unique := map[license.Feature]bool{}
	for _, group := range [][]license.Feature{feature.GatedFeatures(), feature.EditionGatedFeatures(), feature.ControlPlaneGatedFeatures()} {
		for _, f := range group {
			unique[f] = true
		}
	}
	// Split, because one number hid the error this page was corrected for.
	// Counting only the engine's gates reported three of twelve and read as
	// "nine of these do nothing", when two of the nine were being refused by
	// name in another language. The total is the honest headline and the split
	// is what stops the next reader drawing the old conclusion from it.
	return fmt.Sprintf(
		"Of the %d features a license can carry, **%d are refused when the license does not "+
			"name them**, %d by the engine and %d by the control plane, with some checked by both. The rest are listed "+
			"here anyway, with what actually happens without each one, because a feature that "+
			"is sold and never checked is worth knowing about and the number is only useful "+
			"if it can come back unflattering.\n",
		total, len(unique), engine, plane)
}

// splice replaces the block between one pair of markers.
func splice(page, start, end, body string) (string, error) {
	i := strings.Index(page, start)
	j := strings.Index(page, end)
	if i < 0 || j < 0 || j < i {
		return "", fmt.Errorf(
			"the page has no %s and %s pair, so there is nowhere to put the block", start, end)
	}
	return page[:i] + start + "\n" + body + page[j:], nil
}

func TestTheLicensingPageIsCurrentWithTheCatalogue(t *testing.T) {
	raw, err := os.ReadFile(licensingPath)
	require.NoError(t, err)

	want, err := splice(string(raw), namesStart, namesEnd, namesSentence())
	require.NoError(t, err)
	want, err = splice(want, countStart, countEnd, countSentence())
	require.NoError(t, err)
	want, err = splice(want, entitlementsStart, entitlementsEnd, entitlementTable())
	require.NoError(t, err)

	if *updateEntitlements {
		require.NoError(t, os.WriteFile(licensingPath, []byte(want), 0o644))
		return
	}
	require.Equal(t, want, string(raw),
		"the licensing page is out of date with the entitlement catalogue. Regenerate from "+
			"ee/engine with: GOWORK=off go test ./feature -update-entitlements")
}

func TestEveryLicensedFeatureAppearsOnThePage(t *testing.T) {
	// The page's own claim, checked directly rather than through the diff. The
	// diff above catches both directions and says only that the file differs;
	// this says which feature went missing, which is the sentence somebody
	// reading a failed CI run needs.
	raw, err := os.ReadFile(licensingPath)
	require.NoError(t, err)
	page := string(raw)

	for _, f := range license.AllFeatures() {
		require.Containsf(t, page, "| `"+string(f)+"` |",
			"%s is a feature a licence can carry and the licensing page does not list it", f)
	}
}

// requirementLine is the sentence an enterprise page opens with when it claims
// a licence is needed to use what it describes.
var requirementLine = regexp.MustCompile(
	"\\*Requires an enterprise license with the `([a-z_]+)` feature")

func TestNoPageClaimsALicenceRequirementTheProductDoesNotHave(t *testing.T) {
	// The claim this catches is the one that costs the most, because it is made
	// to the reader who is deciding whether to buy.
	//
	// runtimes.md opened with "Requires an enterprise license with the
	// multi_runtime feature" and nothing anywhere requires it: the scheduler
	// that would enforce placement has no caller outside its own tests, it
	// lives under engine/internal where the enterprise module cannot reach it,
	// and no manifest can express a placement requirement in the first place.
	// The page was selling a licence for a behaviour identical to the one you
	// get without it. Three other pages make the same sentence and all three
	// are true, which is exactly why nobody looked.
	pages, err := filepath.Glob("../../../docs/src/content/docs/enterprise/*.md")
	require.NoError(t, err)
	require.NotEmpty(t, pages, "no enterprise documentation pages were found, so this "+
		"test is scanning nothing")

	gated := map[license.Feature]bool{}
	for _, f := range feature.GatedFeatures() {
		gated[f] = true
	}

	checked := 0
	for _, page := range pages {
		raw, err := os.ReadFile(page)
		require.NoError(t, err)
		for _, m := range requirementLine.FindAllStringSubmatch(string(raw), -1) {
			checked++
			named := license.Feature(m[1])
			entry, ok := feature.Of(named)
			require.Truef(t, ok,
				"%s requires the %s feature and no licence can carry that name",
				filepath.Base(page), named)
			require.Truef(t, gated[named],
				"%s says it requires the %s feature and the catalogue calls that %q, so the "+
					"page promises a licence check the product does not make. Either gate it "+
					"or say what actually happens without it.",
				filepath.Base(page), named, entry.State)
		}
	}
	require.NotZerof(t, checked,
		"no page makes a licence requirement claim, which is either a documentation change "+
			"nobody told this test about or a broken pattern")
	t.Logf("enterprise pages claiming a licence requirement: %d, all gated", checked)
}
