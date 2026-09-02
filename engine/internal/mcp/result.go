package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/report"
)

// The output bounds.
//
// A tool result is read by a model with a finite context, so an unbounded
// result is not generous, it is a result that crowds out the reasoning it was
// meant to inform. The bounds are here rather than at each call site so that
// every tool truncates identically and every truncation is reported the same
// way.
const (
	// maxFindings is how many findings one result carries.
	//
	// Findings are ordered worst first, so the cut always falls on the least
	// important end. A run with four hundred findings has one or two that
	// decide the verdict, and the fortieth is already well past the point
	// where anybody is still reading.
	maxFindings = 40
	// maxEvidencePerPage is how many evidence references one page carries.
	maxEvidencePerPage = 20
	// maxMetrics is how many measurements one result carries, ranked by how
	// far each is from its threshold.
	maxMetrics = 25
	// maxDetailBytes bounds one finding's detail text.
	//
	// The detail is written by the engine and not by the candidate
	// repository, but a migration's own error message can reach it, and that
	// message is attacker influenced text. Bounding it means a hostile
	// migration cannot spend a model's whole context on one finding.
	maxDetailBytes = 600
)

// Result is what every rehearsal tool returns once it has finished.
//
// The field order is the reading order and it is deliberate. The verdict comes
// first because it is the answer; the summary next because it is the reason;
// the findings after that because they are the evidence; and the raw material
// last, behind a cursor, because almost nobody needs it. A model that reads
// only the first two fields has still been told the truth.
type Result struct {
	Kind string `json:"kind"`
	// RunID identifies this run for a later get_rehearsal_run.
	RunID string `json:"run_id"`
	Tool  string `json:"tool"`
	// Status is where the run got to. A result is only complete when this is
	// finished; any other value means the verdict is INCONCLUSIVE.
	Status Status `json:"status"`
	Phase  string `json:"phase,omitempty"`
	// Verdict is the answer, decided by the deterministic evaluator in
	// engine/internal/report and never by a model or by this server.
	Verdict Verdict `json:"verdict"`
	// NativeVerdict is the engine's own richer word: pass, fail, warn, flaky,
	// blocked or unverified. Reported so that collapsing five words into
	// three loses nothing.
	NativeVerdict string `json:"native_verdict,omitempty"`
	// Summary is one to three sentences a person could read aloud.
	Summary string `json:"summary"`
	// Findings are what the run noticed, worst first and bounded.
	Findings FindingPage `json:"findings"`
	// Metrics are the measurements behind the verdict, ranked.
	Metrics []Metric `json:"metrics,omitempty"`
	// Evidence points at the artifacts. Paginated, because a busy run
	// produces more references than any result should carry.
	Evidence EvidencePage `json:"evidence"`
	// Detail is the tool specific evidence: for a migration rehearsal, the
	// locks and the statement timings, with the SQL withheld.
	Detail json.RawMessage `json:"detail,omitempty"`
	// Error describes why a failed run failed. Present only when the status
	// is failed.
	Error *faultDocument `json:"error,omitempty"`
}

// Finding is one thing the run noticed.
type Finding struct {
	// Rule is the manifest key that decides what this finding does, which is
	// also what somebody greps for six months later.
	Rule string `json:"rule"`
	// Level is ignore, warn or fail, from the project's policy.
	Level string `json:"level"`
	Title string `json:"title"`
	// Detail is bounded and never contains bytes copied out of the candidate
	// repository verbatim beyond what the engine already redacted.
	Detail string `json:"detail,omitempty"`
	// Fix is what to write instead, when there is something to write.
	Fix string `json:"fix,omitempty"`
	// Where locates it: a table, a host, a migration file.
	Where string `json:"where,omitempty"`
	// Count is how many things this finding covers.
	Count int `json:"count,omitempty"`
}

// FindingPage is a bounded slice of findings that always states the total.
//
// Total is the count before truncation and it is always present, including
// when nothing was truncated. A caller must never have to infer how much it
// was not shown, and a field that appears only on truncation is a field a
// caller forgets to check.
type FindingPage struct {
	Items []Finding `json:"items"`
	Total int       `json:"total"`
	Shown int       `json:"shown"`
	// Truncated says plainly whether anything was withheld.
	Truncated bool `json:"truncated"`
	// Note explains the truncation in words when there was one, so that a
	// model reading only the prose still learns it is not seeing everything.
	Note string `json:"note,omitempty"`
}

// Metric is one measurement, with the threshold it was judged against.
type Metric struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
	// Threshold is the project's limit, when the metric has one. It comes
	// from the manifest, never from the call.
	Threshold *float64 `json:"threshold,omitempty"`
	// Breached says whether this measurement crossed its threshold.
	Breached bool `json:"breached"`
}

// Evidence is a reference to an artifact, never the artifact itself.
//
// A reference rather than a payload, because the artifacts are large, are
// derived from an untrusted repository, and are mostly not wanted. A caller
// that needs one fetches it deliberately.
type Evidence struct {
	URI  string `json:"uri"`
	Kind string `json:"kind"`
	Note string `json:"note,omitempty"`
}

// EvidencePage is one page of evidence references.
type EvidencePage struct {
	Items []Evidence `json:"items"`
	Total int        `json:"total"`
	Shown int        `json:"shown"`
	// NextCursor is passed back to get_rehearsal_run to read the next page.
	// Empty means this is the last page.
	NextCursor string `json:"next_cursor,omitempty"`
	Truncated  bool   `json:"truncated"`
	Note       string `json:"note,omitempty"`
}

// boundFindings ranks, truncates and reports.
//
// Ranking is by level and then by the order the engine produced them, which is
// already deliberate: engine/internal/cli/ci.go assembles findings in a
// meaningful order and a stable sort preserves it inside each level.
func boundFindings(in []report.Finding) FindingPage {
	items := make([]Finding, 0, len(in))
	for _, f := range in {
		items = append(items, Finding{
			Rule: f.Rule, Level: string(f.Level), Title: clip(f.Title, 200),
			Detail: clip(f.Detail, maxDetailBytes), Fix: clip(f.Fix, 300),
			Where: clip(f.Where, 200), Count: f.Count,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return levelRank(items[i].Level) < levelRank(items[j].Level)
	})

	page := FindingPage{Total: len(items)}
	if len(items) > maxFindings {
		page.Truncated = true
		page.Note = fmt.Sprintf(
			"%d findings were produced and the %d most serious are shown. "+
				"They are ordered worst first, so everything withheld is at or below "+
				"the level of the last one here.", len(items), maxFindings)
		items = items[:maxFindings]
	}
	page.Items, page.Shown = items, len(items)
	if page.Items == nil {
		page.Items = []Finding{}
	}
	return page
}

// levelRank orders findings worst first, mirroring the report package's own
// display order so that the two never disagree about which finding matters.
func levelRank(level string) int {
	switch report.Level(level) {
	case report.LevelFail:
		return 0
	case report.LevelWarn:
		return 1
	default:
		return 2
	}
}

// boundEvidence returns one page starting at the cursor.
//
// A cursor is an offset bound to the run it came from. Binding matters: an
// evidence cursor is not an authorisation and must not become one, so it
// carries the run id and is refused when presented against a different run,
// which stops a cursor from one run being used to walk another's artifacts.
func boundEvidence(runID string, all []Evidence, cursor string) (EvidencePage, *Fault) {
	offset := 0
	if cursor != "" {
		var fault *Fault
		offset, fault = decodeCursor(runID, cursor)
		if fault != nil {
			return EvidencePage{}, fault
		}
	}
	if offset > len(all) {
		offset = len(all)
	}
	end := offset + maxEvidencePerPage
	if end > len(all) {
		end = len(all)
	}
	page := EvidencePage{
		Items: append([]Evidence{}, all[offset:end]...),
		Total: len(all), Shown: end - offset,
	}
	if end < len(all) {
		page.Truncated = true
		page.NextCursor = encodeCursor(runID, end)
		page.Note = fmt.Sprintf(
			"Showing %d to %d of %d references. Pass next_cursor to get_rehearsal_run "+
				"as evidence_cursor for the next page.", offset+1, end, len(all))
	}
	return page, nil
}

// encodeCursor builds an opaque cursor bound to one run.
//
// Opaque to the caller and unauthenticated on purpose: it is a position, not a
// capability. It is checked against the run being read, so tampering with it
// can only produce a refusal or a different page of the same run's evidence,
// which the caller could already see.
func encodeCursor(runID string, offset int) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(runID + ":" + strconv.Itoa(offset)))
}

// decodeCursor reads a cursor and refuses one that belongs to another run.
func decodeCursor(runID, cursor string) (int, *Fault) {
	if len(cursor) > 256 {
		return 0, fieldFault(FaultArgumentTooLarge, "evidence_cursor",
			"This cursor is longer than any this server issues.")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fieldFault(FaultInvalidArgument, "evidence_cursor",
			"This is not a cursor this server issued.")
	}
	id, rest, ok := strings.Cut(string(raw), ":")
	if !ok || id != runID {
		return 0, fieldFault(FaultInvalidArgument, "evidence_cursor",
			"This cursor belongs to a different run.")
	}
	offset, err := strconv.Atoi(rest)
	if err != nil || offset < 0 {
		return 0, fieldFault(FaultInvalidArgument, "evidence_cursor",
			"This is not a cursor this server issued.")
	}
	return offset, nil
}

// boundMetrics ranks measurements and truncates.
//
// Breached metrics first, then the rest. A caller that reads one metric should
// read the one that decided the verdict.
func boundMetrics(in []Metric) []Metric {
	out := append([]Metric{}, in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Breached != out[j].Breached {
			return out[i].Breached
		}
		return false
	})
	if len(out) > maxMetrics {
		out = out[:maxMetrics]
	}
	return out
}

// clip bounds a string on a rune boundary and says that it did.
//
// The marker is not decoration. A silently cut string is a string a reader
// believes is complete, and half a sentence read as a whole one is how a
// truncation becomes a wrong conclusion.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8Boundary(s[cut]) {
		cut--
	}
	return s[:cut] + " [truncated]"
}

// utf8Boundary reports whether b can start a UTF-8 sequence, so that clipping
// never leaves half a character behind.
func utf8Boundary(b byte) bool { return b&0xC0 != 0x80 }
