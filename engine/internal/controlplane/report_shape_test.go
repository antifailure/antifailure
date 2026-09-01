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
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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

// aliasDecl matches a name bound to a nested object inside the aggregate, which
// is the one shape a read can take that is not a direct property of `r`.
var aliasDecl = regexp.MustCompile(`const (\w+) = obj\(r\.(\w+)\)`)

// propertyRead matches any `<object>.<field>`, which is filtered afterwards to
// the objects that actually hold the report's aggregate.
var propertyRead = regexp.MustCompile(`\b([A-Za-z_]\w*)\.([a-z_][a-z0-9_]*)\b`)

// aggregateFunc is the decoder's aggregate reader, which is the only part of
// the file this test is about. The route, threshold and evidence decoders read
// their own parameter rather than the aggregate, so they are outside it.
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

// lineComment matches a `//` comment to the end of its line.
//
// Stripped before anything is scanned, and the reason is a false alarm this
// gate produced on a CORRECT file. The tolerant decoder documents the old names
// in prose, "these read `r.overall.p50_ms` and `r.sent` and `r.rate`", and the
// read pattern matched them inside that sentence and reported three reads that
// do not exist. A gate that fires on a file whose only sin is explaining itself
// is a gate somebody disables, and then it protects nothing.
var lineComment = regexp.MustCompile(`//[^\n]*`)

// read is one place the decoder takes a value out of the report's aggregate.
type read struct {
	object string
	field  string
	// fallback is true when this read sits on the right of a `??`, which makes
	// it a second choice rather than the shape being decoded.
	fallback bool
}

// readsIn finds every aggregate read, and whether each is a fallback.
//
// Preceded-by-`??` is the whole distinction this test now turns on, so it is
// determined from the source rather than assumed: the characters before each
// read are walked backwards over whitespace and an opening parenthesis, and the
// two before that decide it.
func readsIn(source []byte) []read {
	// Blanked rather than deleted, so every offset below still lines up with
	// the source and `lineAround` keeps working.
	body := lineComment.ReplaceAllFunc(source, func(c []byte) []byte {
		return bytes.Repeat([]byte(" "), len(c))
	})

	aliases := map[string]bool{}
	for _, m := range aliasDecl.FindAllSubmatch(body, -1) {
		aliases[string(m[1])] = true
	}

	var out []read
	for _, at := range propertyRead.FindAllSubmatchIndex(body, -1) {
		object := string(body[at[2]:at[3]])
		if object != "r" && !aliases[object] {
			continue
		}
		// The alias declaration itself is not a read of a value. Every USE of
		// the alias is checked below, which is where the question actually is.
		if object == "r" && aliasDecl.Match(lineAround(body, at[0])) {
			continue
		}
		out = append(out, read{
			object: object, field: string(body[at[4]:at[5]]),
			fallback: precededByCoalesce(body, at[0]),
		})
	}
	return out
}

func lineAround(body []byte, at int) []byte {
	start := bytes.LastIndexByte(body[:at], '\n') + 1
	end := bytes.IndexByte(body[at:], '\n')
	if end < 0 {
		return body[start:]
	}
	return body[start : at+end]
}

func precededByCoalesce(body []byte, at int) bool {
	i := at - 1
	for i >= 0 && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '(') {
		i--
	}
	return i >= 1 && body[i] == '?' && body[i-1] == '?'
}

func TestTheControlPlaneReadsTheAggregateFieldsTheEngineActuallySends(t *testing.T) {
	if _, err := os.Stat(filepath.Clean(studioDir)); os.IsNotExist(err) {
		// Not a skip that hides anything. This branch does not carry the Studio
		// persistence layer at all, so there is no decoder to compare with, and
		// the assertions below have no subject rather than a subject they
		// failed to look at. The check below fails loudly if the directory is
		// there and the file is not.
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
	// The one name the decoder may read first that is not a JSON tag on
	// Measured. Measured spells the failures-by-reason map `errors` and the
	// decoder decodes it into `errorReasons`, so the two names differ on
	// purpose and only this side of it is a tag.
	//
	// Nothing else goes here. `refused_as_unsafe` used to, and that exemption
	// is what hid the fourth name of a defect everybody was calling three:
	// every entry in a list like this is a place the gate has agreed not to
	// look, so an entry added for convenience is a hole with a comment over it.
	documentName := func(field string) bool { return sends[field] || field == "errors" }

	reads := readsIn(aggregate)
	if len(reads) < 15 {
		t.Fatalf("only %d aggregate reads were found, which is too few to be aggregateFor; "+
			"the pattern has stopped matching and every assertion below is vacuous", len(reads))
	}

	// THE RULE, and it replaces "the native spelling is absent".
	//
	// Accepting a second spelling as a FALLBACK is version-skew support and is
	// fine. Reading one FIRST, or alone, is the decoder declaring which shape it
	// believes it is being sent, and that shape has to be the document's. A gate
	// that fired on a correct tolerant read would be a gate somebody disables,
	// and then it protects nothing.
	seen := map[string]bool{}
	for _, r := range reads {
		if r.fallback {
			// A second choice. It may be any spelling, including a native one,
			// and it may come through a nested alias.
			continue
		}
		if r.object != "r" {
			t.Errorf("aggregateFor reads %s.%s as a FIRST choice, which takes the value out of a "+
				"nested object inside the report's result. The engine's aggregate is flat: the "+
				"nested shape is its NATIVE load result, which is the document the control plane "+
				"deliberately does not store. Read the flat name first and fall back to the "+
				"nested one, not the other way round.", r.object, r.field)
			continue
		}
		if !documentName(r.field) {
			t.Errorf("aggregateFor reads r.%s as a FIRST choice and the engine sends no such "+
				"field.\nThe engine sends: %v.\nA name the decoder reaches for and the engine "+
				"does not send decodes to null, or to zero where the column needs a number, and "+
				"NOTHING SAYS SO: the skipped counter stays at zero and every route and threshold "+
				"beside it decodes perfectly.", r.field, sorted(sends))
			continue
		}
		seen[r.field] = true
	}

	// And the other direction, in two grades, because they are two different
	// bugs and telling somebody the wrong one is how a gate gets ignored.
	fallbackOnly := map[string]bool{}
	for _, r := range reads {
		if r.fallback && r.object == "r" && sends[r.field] {
			fallbackOnly[r.field] = true
		}
	}

	var dropped, demoted []string
	for field := range sends {
		switch {
		case seen[field]:
		case fallbackOnly[field]:
			demoted = append(demoted, field)
		default:
			dropped = append(dropped, field)
		}
	}
	sort.Strings(dropped)
	sort.Strings(demoted)

	// A LIVE DEFECT. The engine sends it and the decoder reads it nowhere, so
	// the measurement is on the floor.
	if len(dropped) > 0 {
		t.Errorf("the engine sends %v inside the report's result and aggregateFor does not read "+
			"them at all, so those measurements are dropped. Either decode them or stop sending "+
			"them.", dropped)
	}

	// NOT A LIVE DEFECT, and the message says so, because a gate that reports
	// an ordering as a bug is a gate somebody stops believing. `a ?? b` returns
	// b whenever a is absent, and these names are always absent, so the value
	// arrives correctly today.
	//
	// It is still worth failing on. The order is the only place the decoder
	// states which shape it believes it is being sent, and a native name
	// standing first is exactly what read as generosity for hours while being a
	// name nothing can send. That reading is what kept a four name defect being
	// called a three name one.
	if len(demoted) > 0 {
		t.Errorf("aggregateFor reads %v only as a fallback, behind a name the engine does not "+
			"send.\nThis is NOT breaking anything today: `a ?? b` yields b whenever a is absent, "+
			"and the name in front is always absent, so the value arrives.\nSwap the operands "+
			"anyway. The order is where the decoder says which shape it expects, and a native "+
			"name in front is what made this whole class read as generosity rather than as a "+
			"bug.", demoted)
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
