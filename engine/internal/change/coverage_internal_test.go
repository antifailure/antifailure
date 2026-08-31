package change

import "testing"

// A surface with no entry in the coverage table exercises nothing, which in
// the output is indistinguishable from a surface this product genuinely does
// not cover. Adding a surface constant has to mean deciding which of those two
// it is, so this test fails until the table names it, including when the
// decision is the empty list.
func TestCoverage_EverySurfaceIsDecidedOneWayOrTheOther(t *testing.T) {
	all := []Surface{
		SurfaceSchema, SurfaceService, SurfaceCode, SurfaceAsset,
		SurfaceBuild, SurfaceDependency, SurfaceConfig, SurfaceInfrastructure,
		SurfacePipeline, SurfaceManifest, SurfaceMasking, SurfaceTest,
		SurfaceDocs, SurfaceEgress,
	}
	for _, s := range all {
		if _, ok := coverage[s]; !ok {
			t.Errorf("the surface %q has no entry in the coverage table", s)
		}
	}
	if _, ok := coverage[SurfaceUnknown]; ok {
		t.Error("unknown is the absence of a classification, and an entry for it would give the fail safe a coverage answer")
	}
}

// Every check the coverage table names has to be a check the plan renders,
// or a surface would select something no report ever reports.
func TestCoverage_NamesOnlyRealChecks(t *testing.T) {
	known := map[Check]bool{}
	for _, c := range Checks() {
		known[c] = true
	}
	for surface, checks := range coverage {
		for _, c := range checks {
			if !known[c] {
				t.Errorf("the surface %q selects %q, which Checks does not list", surface, c)
			}
		}
	}
}
