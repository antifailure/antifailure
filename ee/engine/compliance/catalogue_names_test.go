// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package compliance

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
)

// The licence catalogue said compliance_packs was "SOC 2 and ISO 27001
// evidence" while this package built SOC 2 and HIPAA and nothing else. That
// sentence is generated into the licensing page, so a buyer read the name of a
// framework `af compliance` has never produced, and nothing compared the two.
//
// shortNames is how the catalogue names each pack. Its keys must be exactly the
// packs this build carries, so a new pack fails here until somebody decides how
// the catalogue names it and writes that name into the summary.
var shortNames = map[string]string{
	"soc2":  "SOC 2",
	"hipaa": "HIPAA",
}

// frameworksWithNoPack are names a summary could reach for and this package has
// no pack for. Listed rather than inferred, because the defect was a real
// framework written beside a real one, which no check on the packs alone sees.
var frameworksWithNoPack = []string{
	"ISO 27001", "ISO/IEC 27001", "PCI DSS", "GDPR", "FedRAMP", "NIST", "SOC 1",
}

func complianceSummary(t *testing.T) string {
	t.Helper()
	e, ok := feature.Of(license.FeatureCompliance)
	require.True(t, ok, "the catalogue has no compliance_packs entry, so there is nothing to compare")
	require.NotEmpty(t, e.Summary, "the compliance_packs entry has no summary")
	return e.Summary
}

func TestEveryPackHasTheNameTheCatalogueUsesForIt(t *testing.T) {
	var packs, named []string
	for key := range Packs() {
		packs = append(packs, key)
	}
	for key := range shortNames {
		named = append(named, key)
	}
	sort.Strings(packs)
	sort.Strings(named)
	require.Equal(t, packs, named,
		"Packs() and shortNames disagree. A pack with no short name is a framework the "+
			"catalogue cannot be checked for, and a short name with no pack is a claim with "+
			"nothing behind it")
}

func TestTheCatalogueNamesEveryPackThisBuildCarries(t *testing.T) {
	summary := complianceSummary(t)
	for key, name := range shortNames {
		require.Contains(t, summary, name,
			"the %s pack exists and the licence catalogue does not name it, so the licensing "+
				"page undersells what the licence grants", key)
	}
}

func TestTheCatalogueNamesNoFrameworkThisBuildHasNoPackFor(t *testing.T) {
	summary := complianceSummary(t)
	for _, name := range frameworksWithNoPack {
		require.False(t, strings.Contains(summary, name),
			"the licence catalogue says compliance_packs covers %s, and Packs() has no pack for "+
				"it, so the licensing page sells evidence nothing produces:\n%s", name, summary)
	}
}
