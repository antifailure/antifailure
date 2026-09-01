package controlplane_test

// The seam nobody's suite crossed.
//
// The engine tests what it emits, against a document it wrote itself. The
// control plane tests what it decodes, against a document IT wrote itself. Two
// green suites, and between them a wire neither one had ever put a real
// message on.
//
// It was wrong. `decodeReport` read the run-wide aggregate of a load report
// using the field names of the engine's NATIVE load result, `sent`, `rate` and
// the percentiles nested under `overall`, while the event payload is the
// projected workload result, which spells them `requests`, `achieved_rate` and
// flat. Two of the four kinds were affected and it failed SILENTLY: the
// request count falls back to zero rather than refusing, because the CHECK
// needs a count, so a run that sent twelve hundred requests was recorded as
// having sent none with every percentile null, while every route, threshold
// and piece of evidence beside it decoded perfectly. A console draws that as a
// strange run rather than as a broken decoder.
//
// So this reads the decoder's source and asserts that every name it reaches for
// is a name the engine's own struct tags emit. It is the same shape as
// vocabulary_test.go reading the control plane's EVENT_TYPES: a copy of a
// contract is a second thing to keep in step, and keeping two things in step by
// hand is what produced this.
//
// It fails in both directions. A field the decoder reads and the engine does
// not send is the defect above. A field the engine sends into the aggregate
// that no arm of the decoder reads is a measurement being dropped on the floor,
// which is the same defect wearing the other hat.

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/antifailure/antifailure/engine/internal/workload"
)

// decoderPath is the control plane's report decoder, relative to this package.
const decoderPath = "../../../web/apps/api/src/workloads/results.ts"

// studioDir is the directory that decoder lives in. Its presence is what tells
// a branch predating Workload Studio apart from one that has lost the file.
const studioDir = "../../../web/apps/api/src/workloads"

// aggregateReads matches every field aggregateFor pulls out of the report's
// `result` object, which it holds as `r`.
var aggregateReads = regexp.MustCompile(`\br\.([a-z_][a-z0-9_]*)`)

// nestedReads matches a field read out of a nested object inside the aggregate,
// which is precisely the shape the defect took: `obj(r.overall).p50_ms`.
var nestedReads = regexp.MustCompile(`obj\(r\.([a-z_][a-z0-9_]*)\)`)

// aggregateFunc is the decoder's aggregate reader, which is the only part of
// the file this test is about. The route, threshold and evidence decoders were
// checked by hand against the same payload and agreed; they read their own
// parameter rather than `r`, so they are outside the pattern above anyway.
var aggregateFunc = regexp.MustCompile(`(?s)function aggregateFor\(.*?\n}\n`)

// measuredFields is every JSON name the engine puts inside the report's
// `result` object, read off the struct rather than listed here.
func measuredFields() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(workload.Measured{})
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

func TestTheControlPlaneReadsTheAggregateFieldsTheEngineActuallySends(t *testing.T) {
	if _, err := os.Stat(filepath.Clean(studioDir)); os.IsNotExist(err) {
		// Not a skip that hides anything. This branch does not carry the
		// Studio persistence layer at all, so there is no decoder to compare
		// with, and the assertion below has no subject rather than a subject it
		// failed to look at. The moment that directory exists this test has
		// teeth, and the check below fails loudly if the directory is there and
		// the file is not.
		t.Skip("this tree has no web/apps/api/src/workloads, so there is no report decoder to compare with")
	}

	body, err := os.ReadFile(filepath.Clean(decoderPath))
	if err != nil {
		t.Fatalf("the Studio persistence layer is in this tree and %s is not readable: %v.\n"+
			"Fix the path rather than deleting this test: the drift it guards produced a load run "+
			"recorded as having sent zero requests, and nothing else in either suite looks at it.",
			decoderPath, err)
	}

	aggregate := aggregateFunc.Find(body)
	if aggregate == nil {
		t.Fatalf("%s no longer declares aggregateFor in a form this test can read; fix the "+
			"pattern rather than deleting the test", decoderPath)
	}

	sends := measuredFields()
	if len(sends) < 20 {
		t.Fatalf("the engine's Measured parsed to %d JSON names, which is too few to be the "+
			"real struct and would make every assertion below vacuous", len(sends))
	}

	// Names the decoder is allowed to read that are not measurements. `errors`
	// is the failures-by-reason map, which the engine does send; the other two
	// are alternative spellings the decoder deliberately accepts.
	allowed := []string{"errors", "refused_as_unsafe", "refused_routes"}

	var unsent []string
	for _, m := range aggregateReads.FindAllSubmatch(aggregate, -1) {
		name := string(m[1])
		if sends[name] || slices.Contains(allowed, name) || slices.Contains(unsent, name) {
			continue
		}
		unsent = append(unsent, name)
	}
	sort.Strings(unsent)

	if len(unsent) > 0 {
		t.Errorf("aggregateFor reads %v out of the report's result object and the engine sends "+
			"no such fields.\nThe engine sends: %v.\nA name the decoder reaches for and the "+
			"engine does not send decodes to null, or to zero where the column needs a number, "+
			"and NOTHING SAYS SO: the skipped counter stays at zero and every route and threshold "+
			"beside it decodes perfectly.", unsent, sorted(sends))
	}

	// And the nesting, which is how three of the original five were wrong. The
	// engine's aggregate is flat: there is no object inside it to reach into.
	for _, m := range nestedReads.FindAllSubmatch(aggregate, -1) {
		t.Errorf("aggregateFor reads through a nested %q object inside the report's result, and "+
			"the engine's aggregate is flat. That is the engine's NATIVE load result's shape, "+
			"which is the document the control plane deliberately does not store.", string(m[1]))
	}
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
