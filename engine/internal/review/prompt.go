package review

// The prompt is the product. A weak prompt turns a strong model into a linter
// that reports style, and a review nobody trusts is one everybody routes around,
// which is the exact fate the warn default is there to avoid. So the instruction
// is specific about what a defect is, insistent that silence beats a guess, and
// strict about the shape of the answer, because a parser can only be tolerant of
// an answer that was meant to be JSON.

import (
	"encoding/json"
	"strconv"
	"strings"
)

// systemPrompt tells the model what a defect is, what to ignore, and how to
// answer. It names the classes this reviewer exists to catch, because a model
// asked to "review the code" reaches for style, and the whole value here is the
// correctness bug a diff introduces that no workflow happened to run.
const systemPrompt = `You are a rigorous senior software engineer reviewing a code change for correctness.

You are given the lines a diff ADDS to each file, each with the line number it has in the new file. Review ONLY those added lines. Do not report problems in code the diff did not add, and do not report anything you would need the rest of the file to be sure about.

Report ONLY high-confidence, concrete, actionable defects that these added lines introduce. Prefer silence to noise: if the change is clean, or you are not sure, return an empty list. A single wrong or speculative finding is worse than a missed one, because it teaches the author to ignore you.

Do NOT report: style, formatting, naming, comments, test coverage, documentation, "consider" or "you might want to" suggestions, or anything that is a preference rather than a defect.

DO look for, in the added lines:
- correctness: a logic error, an off-by-one, an inverted condition, a wrong operator, a case that produces the wrong result.
- edge_case: a boundary the new code does not hold: empty input, zero, a maximum, a first or last element, an absent value.
- error_handling: an error or failure that is ignored, swallowed, or not checked; a nil or undefined dereference on a value that can be nil or undefined.
- concurrency: a data race, a lock taken in the wrong order, a check-then-act that is not atomic, a goroutine or promise whose result is dropped.
- data_shape: a value decoded or accessed as the wrong shape: an object read as an array or the reverse, a field that can be null read as present, a type mismatch that will throw at runtime.
- dead_code: a new function, method, handler, or branch that nothing calls or can reach, which is usually a feature that was wired halfway.
- security: a concrete security mistake the added lines introduce, such as a missing authorization check on a new endpoint, an injection from unsanitized input, or a secret written where it should not be.

Answer with STRICT JSON and nothing else: a single JSON array. Each element is an object with exactly these keys:
- "category": one of correctness, edge_case, error_handling, concurrency, data_shape, dead_code, security.
- "severity": one of low, medium, high.
- "file": the file path, exactly as given.
- "line": the added line number the defect is on, as an integer.
- "title": a short one-line summary of the defect.
- "explanation": one or two sentences on why it is a defect and what goes wrong.
- "suggested_fix": one sentence on the concrete change that fixes it.

If there are no defects, answer with []. Output the JSON array only, with no prose before or after it and no code fences.`

// rawFinding is one entry as the model returns it. Tolerant tags: a model that
// answers "line" as a string still decodes, and a missing field is the zero
// value the mapper checks for rather than a decode failure that would discard
// the whole array.
type rawFinding struct {
	Category     string  `json:"category"`
	Severity     string  `json:"severity"`
	File         string  `json:"file"`
	Line         flexInt `json:"line"`
	Title        string  `json:"title"`
	Explanation  string  `json:"explanation"`
	SuggestedFix string  `json:"suggested_fix"`
}

// parseEntries reads the model's text into entries.
//
// It returns ok=false only when the response holds no JSON array at all, which
// the caller reports as a note rather than a clean pass. A response that is a
// valid but empty array returns (nil, true): the model looked and found nothing,
// which is the common good outcome and must be told apart from "unreadable".
//
// The array is located rather than assumed, because a model that was told to
// answer with JSON and only JSON still sometimes wraps it in a sentence or a
// code fence. The read boundary is tolerant of that packaging and strict about
// the contents.
func parseEntries(text string) ([]rawFinding, bool) {
	body := extractJSONArray(text)
	if body == "" {
		return nil, false
	}
	var entries []rawFinding
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		return nil, false
	}
	return entries, true
}

// extractJSONArray finds the outermost JSON array in a model response, ignoring
// any prose or code fence around it. It scans for the first '[' and the last
// ']', which is enough because the prompt asks for a single top-level array and
// the mapper drops anything malformed inside it.
func extractJSONArray(text string) string {
	t := strings.TrimSpace(text)
	// Strip a leading code fence if the model added one despite the instruction.
	if strings.HasPrefix(t, "```") {
		if i := strings.IndexByte(t, '\n'); i >= 0 {
			t = t[i+1:]
		}
		t = strings.TrimSuffix(strings.TrimSpace(t), "```")
	}
	start := strings.IndexByte(t, '[')
	end := strings.LastIndexByte(t, ']')
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return strings.TrimSpace(t[start : end+1])
}

// flexInt decodes a line number that a model may return as a number or a string.
// A quoted "12" and a bare 12 both become 12, and anything else becomes 0, which
// the mapper reads as "no line" and drops from Where rather than discarding the
// finding.
type flexInt int

func (n *flexInt) UnmarshalJSON(b []byte) error {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) == 0 || string(b) == "null" {
		*n = 0
		return nil
	}
	// A JSON number.
	var i int
	if err := json.Unmarshal(b, &i); err == nil {
		*n = flexInt(i)
		return nil
	}
	// A JSON string wrapping a number, or anything else. A non-numeric string is
	// tolerated as zero rather than failing the whole array decode.
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if j, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			*n = flexInt(j)
		} else {
			*n = 0
		}
		return nil
	}
	*n = 0
	return nil
}
