package workload_test

import (
	"testing"

	"github.com/antifailure/antifailure/engine/internal/report"
	"github.com/antifailure/antifailure/engine/internal/workload"
)

// This package's five verdict strings must be the product's, not a sixth
// vocabulary invented here.
//
// This replaces a package level var that computed the same condition and threw
// the answer away. Nothing read it, so a disagreement produced no failure
// anywhere: a guard that could not fail. The linter reported it as unused,
// which was the only signal it ever gave. This assertion fails instead.
func TestVerdictsAreTheProductsNotASixthVocabulary(t *testing.T) {
	for _, v := range []string{
		workload.VerdictFail,
		workload.VerdictFlaky,
		workload.VerdictBlocked,
		workload.VerdictUnverified,
		workload.VerdictPass,
	} {
		if !report.Known(v) {
			t.Errorf("verdict %q is not one the product knows; this package has invented a vocabulary of its own", v)
		}
	}
}
