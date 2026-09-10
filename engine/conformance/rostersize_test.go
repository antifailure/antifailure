package conformance

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestEveryQuotedRosterSizeMatchesTheRoster is the instrument for a number
// this repository has already got wrong once, quietly.
//
// THE DEFECT. The marketing site told buyers the Docker runtime "passes all
// thirty-two runtime conformance behaviours against a real daemon", the plan's
// status row said "all 32 of the runtime conformance suite", and the claims
// ledger recorded "The behaviour list in conformance/runtime.go counts exactly
// 32" as PROVEN. All three were true when they were written. Then five
// behaviours were added to runtimeBehaviors below, by three different pull
// requests, and none of the three sentences moved. Nothing anywhere compared
// them, so the roster grew to thirty-seven while the customer-facing page kept
// quoting thirty-two, and the ledger kept quoting a count of the roster as
// evidence that the roster had that count.
//
// That is the failure this repository keeps finding in its own instruments: a
// measurement taken once, written down as prose, and then true only on the day
// it was taken. The remedy is not to correct the three sentences. It is to
// make the next person who adds a behaviour unable to leave them behind.
//
// THE CONVENTION IT ENFORCES, because a gate has to be predictable to survive.
// The token immediately before the words "runtime conformance behaviours" is
// the SIZE OF THE ROSTER and nothing else. A run that did not pass all of them
// is written "35 of 37 runtime conformance behaviours", never "35 runtime
// conformance behaviours", so the denominator stays checkable however the
// numerator moves. Spelled out or in digits, either is accepted, because the
// site writes numbers as words and the plan writes them as digits and neither
// should have to change its voice to satisfy this.
//
// A FILE THAT MATCHES NOTHING IS A FAILURE, not a pass. CLAUDE.md's own words:
// a gate that names a file it cannot check is worse than one that omits it,
// because the listing is what stops anybody asking. So a document whose
// sentence has been reworded past this pattern fails here and has to be either
// rewritten to the convention or taken off the list deliberately.
func TestEveryQuotedRosterSizeMatchesTheRoster(t *testing.T) {
	size := len(runtimeBehaviors)
	// Both spellings the documents are allowed to use.
	want := map[string]bool{
		strconv.Itoa(size):                 true,
		strings.ToLower(spellNumber(size)): true,
	}

	// The token before the phrase, on text whose runs of whitespace have been
	// collapsed. The collapsing is not cosmetic: the site's formatter wraps
	// that sentence between "runtime" and "conformance", so a pattern run
	// against the raw bytes matches nothing at all and the gate would report
	// green having read past the one line it exists for.
	pattern := regexp.MustCompile(`(?i)([A-Za-z0-9-]+) runtime conformance behaviours?`)

	for _, doc := range rosterSizeDocuments {
		t.Run(doc, func(t *testing.T) {
			raw, err := os.ReadFile(doc)
			if err != nil {
				t.Fatalf("%s could not be read (%v). This test names the documents that "+
					"quote the size of the runtime conformance roster, and a named "+
					"document it cannot open is an unanswered question rather than a pass", doc, err)
			}
			text := strings.Join(strings.Fields(string(raw)), " ")

			found := pattern.FindAllStringSubmatch(text, -1)
			if len(found) == 0 {
				t.Fatalf("%s no longer contains the phrase %q, so this gate reads it and "+
					"checks nothing. Either restore the wording, or remove the file from "+
					"rosterSizeDocuments on purpose rather than by rewording past it",
					doc, "<number> runtime conformance behaviours")
			}
			for _, m := range found {
				if quoted := strings.ToLower(m[1]); !want[quoted] {
					t.Errorf("%s says %q but the roster holds %d behaviours (%s). "+
						"A document that quotes the size of this suite has to quote the "+
						"size it actually is; a run that passed fewer is written "+
						"\"<passed> of %d runtime conformance behaviours\"",
						doc, m[0], size, spellNumber(size), size)
				}
			}
		})
	}
}

// rosterSizeDocuments are the documents that state how large this roster is.
//
// Read from engine/conformance, which is why every path climbs two levels.
// Several engine tests already read the repository this way, because the thing
// they are asserting about lives outside their own module and asserting about
// a copy would be asserting about nothing.
var rosterSizeDocuments = []string{
	"../../www/components/pages/product/Twins.tsx",
	"../../docs/plan/STATUS.md",
	"../../docs/plan/notes/claims.md",
}

// TestSpellNumberIsRightAtTheJoins covers the number speller at the places a
// hand written one goes wrong, because a speller that returned the wrong word
// would make the gate above refuse a document that was correct.
func TestSpellNumberIsRightAtTheJoins(t *testing.T) {
	cases := map[int]string{
		1: "one", 9: "nine", 10: "ten", 11: "eleven", 13: "thirteen",
		15: "fifteen", 19: "nineteen", 20: "twenty", 21: "twenty-one",
		30: "thirty", 32: "thirty-two", 37: "thirty-seven", 40: "forty",
		50: "fifty", 99: "ninety-nine",
	}
	for n, word := range cases {
		if got := spellNumber(n); got != word {
			t.Errorf("spellNumber(%d) = %q, want %q", n, got, word)
		}
	}
	// Out of range has to be visible rather than silently empty, because an
	// empty allowed spelling would let any word through the gate above.
	if got := spellNumber(100); !strings.Contains(got, "100") {
		t.Errorf("spellNumber(100) = %q; a number this cannot spell must still "+
			"produce something no document would match by accident", got)
	}
}

// spellNumber writes a number the way the marketing site writes one.
//
// Only up to ninety-nine, which is far past any roster this suite will hold,
// and out of range returns the digits rather than an empty string: an empty
// allowed spelling would make every token in a document acceptable.
func spellNumber(n int) string {
	ones := []string{"zero", "one", "two", "three", "four", "five", "six",
		"seven", "eight", "nine", "ten", "eleven", "twelve", "thirteen",
		"fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}
	tens := []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty",
		"seventy", "eighty", "ninety"}
	switch {
	case n < 0 || n > 99:
		return fmt.Sprintf("%d", n)
	case n < 20:
		return ones[n]
	case n%10 == 0:
		return tens[n/10]
	default:
		return tens[n/10] + "-" + ones[n%10]
	}
}
