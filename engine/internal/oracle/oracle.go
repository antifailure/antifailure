// Package oracle compares two versions of the same application running against
// the same starting data, and reports what they did differently.
//
// The shape of the thing: bring the candidate up as usual, bring a second
// environment up from a baseline revision, branch the SAME golden for each so
// both start from identical rows, send both the same requests in the same
// order, and then diff what came back and what ended up in the database. A
// difference is a fact about the change. Everything else in this package
// exists to make that sentence true rather than approximately true.
//
// # What is compared, and what deliberately is not
//
// HTTP responses and database writes. Not events, not outbound effects, not
// traces, not query plans. Those are on the list and they are not here, and
// the reason is that a shallow comparison of six things is worse than a
// complete comparison of two: the first one that cries wolf is the last one
// anybody looks at. What this package does with an HTTP body and a table of
// rows, it does completely.
//
// # Normalisation, which is the whole problem
//
// A byte comparison of two HTTP responses reports a different Date header, a
// different session cookie, a different request id and a different generated
// timestamp on every single request, and a tool that is wrong on its first run
// is never turned on again. So values are normalised before they are compared.
//
// The rule the normalisation obeys: every single thing it declines to compare
// is named in the output, defaults included. An oracle that silently ignores a
// field is worse than one that reports it, because the field it ignored is
// exactly where the bug was. Ignored.Describe renders that list and the
// renderers always print it.
//
// The normalisers are deliberately narrow:
//
//   - A string that parses as a timestamp ON BOTH SIDES is equal. One side a
//     timestamp and the other not is a difference, and so is a timestamp
//     against a missing field.
//   - A string that is a UUID on both sides is equal.
//   - Numbers compare equal within a relative tolerance, so 0.1+0.2 against
//     0.3 is not news and 0.3 against 0.4 is.
//
// What is NOT normalised is as considered as what is. Integer identifiers are
// compared exactly: both databases branch the same golden and receive the same
// requests in the same order, so a sequence that has reached 41 on one side and
// 42 on the other means the candidate wrote a row the baseline did not, which
// is the single most useful thing this package can tell anybody. Normalising
// ids away would have thrown it out. A numeric epoch timestamp is compared
// exactly too, because guessing that a number is a clock from the name of the
// field it sits under would silently ignore an expiry that moved by a day.
//
// # Non-determinism this package does not solve
//
// An opaque token that is neither a UUID nor a timestamp, a CSRF field, a
// random slug: there is no shape to recognise and this package does not guess.
// Ignore it by path. Concurrent writes from a background worker can land in
// either order; rows are matched by primary key so storage order never
// matters, but a worker that writes a different NUMBER of rows on two runs
// will report a difference that is not the change. Both limitations are named
// in the documentation rather than papered over.
package oracle

import (
	"fmt"
	"sort"
	"strings"
)

// Side names one of the two versions under comparison.
type Side string

const (
	// Baseline is the older revision, the one the change is measured against.
	Baseline Side = "baseline"
	// Candidate is the revision under review.
	Candidate Side = "candidate"
)

// Severity ranks a difference by how likely it is to be a regression rather
// than the change somebody meant to make.
//
// The ranking is directional, and that is the whole idea. A candidate that
// stops returning a field, stops writing a row, or turns a served request into
// an error has LOST something the baseline had, and that is almost never
// intended. A candidate that returns an extra field or writes an extra row has
// ADDED something, which is what a feature branch does all day. Ranking both as
// "different" would bury the first in the second.
type Severity int

const (
	// Minor is a difference that a feature branch produces routinely: a new
	// field, a new row, a changed value, a reordered array.
	Minor Severity = iota + 1
	// Major is a difference worth reading every time: a field or a table that
	// disappeared, a value that changed type, a status code that moved, a row
	// whose columns no longer agree.
	Major
	// Critical is a difference that is a regression unless somebody can say
	// why it is not: a request the baseline served and the candidate did not,
	// a status that fell into an error class, a row the baseline wrote and the
	// candidate did not.
	Critical
)

// String renders a severity for output.
func (s Severity) String() string {
	switch s {
	case Critical:
		return "critical"
	case Major:
		return "major"
	case Minor:
		return "minor"
	default:
		return "unknown"
	}
}

// ParseSeverity reads a severity from a manifest value.
//
// "none" is a real answer and returns ok with a zero severity, which every
// comparison outranks, so a project can have the oracle report and never fail.
func ParseSeverity(s string) (Severity, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none", "":
		return 0, true
	case "minor", "any":
		return Minor, true
	case "major":
		return Major, true
	case "critical":
		return Critical, true
	default:
		return 0, false
	}
}

// Kind names what differed. It is part of the JSON output, so these strings
// are an interface and do not change.
type Kind string

const (
	// KindTransport is one side answering and the other not.
	KindTransport Kind = "transport"
	// KindStatusClass is a status that moved between 2xx, 4xx and 5xx.
	KindStatusClass Kind = "status_class"
	// KindStatus is a status that changed inside its class.
	KindStatus Kind = "status"
	// KindContentType is a response that changed media type.
	KindContentType Kind = "content_type"
	// KindHeader is a compared header whose value changed.
	KindHeader Kind = "header"
	// KindBodyMissing is a JSON path the baseline returned and the candidate
	// did not.
	KindBodyMissing Kind = "body_missing"
	// KindBodyExtra is a JSON path the candidate returned and the baseline did
	// not.
	KindBodyExtra Kind = "body_extra"
	// KindBodyType is a JSON value that changed type, such as a number that
	// became a string.
	KindBodyType Kind = "body_type"
	// KindBodyValue is a JSON scalar that changed.
	KindBodyValue Kind = "body_value"
	// KindBodyLength is a JSON array whose length changed.
	KindBodyLength Kind = "body_length"
	// KindBodyOrder is a JSON array with the same members in a different
	// order.
	KindBodyOrder Kind = "body_order"
	// KindBodyBytes is a body that is not JSON and did not match.
	KindBodyBytes Kind = "body_bytes"
	// KindBodyParse is a body that is declared JSON and does not parse on one
	// side.
	KindBodyParse Kind = "body_parse"
	// KindRowMissing is a row present on the baseline and absent on the
	// candidate.
	KindRowMissing Kind = "row_missing"
	// KindRowExtra is a row present on the candidate and absent on the
	// baseline.
	KindRowExtra Kind = "row_extra"
	// KindRowChanged is a row present on both sides whose columns disagree.
	KindRowChanged Kind = "row_changed"
	// KindTableMissing is a table the baseline has and the candidate does not.
	KindTableMissing Kind = "table_missing"
	// KindTableExtra is a table the candidate has and the baseline does not.
	KindTableExtra Kind = "table_extra"
	// KindColumns is a table whose columns differ between the two sides.
	KindColumns Kind = "columns"
)

// Phase says whether a database difference was already there before any request
// was sent.
//
// The distinction is worth the second snapshot it costs. A row that differs
// before the traffic is what the two sets of migrations did; a row that differs
// only afterwards is what the two versions of the application did. Those want
// different readers and different responses, and a report that ran them
// together would send somebody to read application code about a migration.
type Phase string

const (
	// PhaseMigration is a difference present before any request was sent.
	PhaseMigration Phase = "migration"
	// PhaseTraffic is a difference that appeared while the probes ran.
	PhaseTraffic Phase = "traffic"
)

// Finding is one difference between the two sides.
type Finding struct {
	Kind     Kind     `json:"kind"`
	Severity Severity `json:"-"`
	// SeverityName is the severity as a word, for JSON consumers.
	SeverityName string `json:"severity"`
	// Where is the probe this came from, or the qualified table name for a
	// database finding.
	Where string `json:"where"`
	// Path locates the difference inside the response body, or names the row
	// by its primary key.
	Path string `json:"path,omitempty"`
	// Baseline and Candidate are the two values, already normalised and
	// already truncated to something a table cell can hold.
	Baseline  string `json:"baseline,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	// Phase is set on database findings and empty on response findings.
	Phase Phase `json:"phase,omitempty"`
	// Detail is one sentence where the two values do not say it themselves.
	Detail string `json:"detail,omitempty"`
	// order is the probe's position in the plan, so findings sort into the
	// order the requests were sent rather than alphabetically. Unexported
	// because it is a rendering concern and not part of the output.
	order int
}

// newFinding fills the fields that are always derived from the others.
func newFinding(kind Kind, sev Severity, where, path string) Finding {
	return Finding{Kind: kind, Severity: sev, SeverityName: sev.String(), Where: where, Path: path}
}

// AtLeast reports whether any finding is at or above a severity.
//
// A zero threshold means nothing reaches it, which is how "report and never
// fail" is expressed without a second flag.
func AtLeast(findings []Finding, sev Severity) bool {
	if sev == 0 {
		return false
	}
	for _, f := range findings {
		if f.Severity >= sev {
			return true
		}
	}
	return false
}

// Highest returns the worst severity in a set, or zero when the set is empty.
func Highest(findings []Finding) Severity {
	var worst Severity
	for _, f := range findings {
		if f.Severity > worst {
			worst = f.Severity
		}
	}
	return worst
}

// Count returns how many findings sit at each severity.
func Count(findings []Finding) map[Severity]int {
	out := map[Severity]int{}
	for _, f := range findings {
		out[f.Severity]++
	}
	return out
}

// sortFindings puts the findings in reading order: worst first, then in the
// order the requests were sent, then by path.
//
// Somebody scrolling to find the critical finding is the same as somebody not
// seeing it, which is the reason the pull request comment sorts its workflows
// the same way.
func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Severity != b.Severity {
			return a.Severity > b.Severity
		}
		if a.order != b.order {
			return a.order < b.order
		}
		if a.Where != b.Where {
			return a.Where < b.Where
		}
		return a.Path < b.Path
	})
}

// Result is everything one oracle run produced.
type Result struct {
	// Baseline and Candidate describe the two revisions, resolved to commits.
	BaselineRef  string `json:"baseline_ref"`
	CandidateRef string `json:"candidate_ref"`
	// BaselineHow says in words how the baseline was chosen, because
	// "the merge base with origin/main" and "the tag v2.4.0" answer different
	// questions and the report must not leave a reader guessing which was
	// asked.
	BaselineHow string `json:"baseline_how"`
	// Golden is the database version both sides branched, named because the
	// comparison is only meaningful when it is one version and this is where
	// somebody checks.
	Golden string `json:"golden,omitempty"`
	// BaselineEnv and CandidateEnv are the two environment identifiers, so a
	// failed teardown can be finished by hand.
	BaselineEnv  string `json:"baseline_env,omitempty"`
	CandidateEnv string `json:"candidate_env,omitempty"`
	// Probes is what each request did on each side.
	Probes []ProbeResult `json:"probes,omitempty"`
	// Findings is every difference, worst first.
	Findings []Finding `json:"findings"`
	// Ignored is everything the comparison declined to look at, defaults
	// included. Always rendered.
	Ignored Ignored `json:"ignored"`
	// Database summarises what was and was not compared in the two databases,
	// and is nil when the database comparison did not run.
	Database *DatabaseSummary `json:"database,omitempty"`
	// Notes are the things this run could not compare, and why. An oracle that
	// silently skips reads exactly like one that found nothing.
	Notes []string `json:"notes,omitempty"`
	// DurationMs is how long the comparison took, excluding bringing the two
	// environments up.
	DurationMs int64 `json:"duration_ms,omitempty"`
}

// Verdict is the one word answer.
//
// Deliberately not the engine's run verdict vocabulary. This package reports
// what differs; whether a difference fails a check is the caller's decision and
// depends on the manifest's threshold.
func (r Result) Verdict() string {
	switch {
	case len(r.Findings) == 0:
		return "identical"
	case Highest(r.Findings) >= Critical:
		return "regressed"
	default:
		return "differs"
	}
}

// Headline is the first line, which is the only line most people read.
func (r Result) Headline() string {
	// The baseline is named when there is one and left out when there is not,
	// rather than printed as an empty string. "identical to  across 3
	// requests" is the shape of a sentence with a hole in it, and a headline
	// is the one line everybody reads.
	against := ""
	if r.BaselineRef != "" {
		against = " " + short(r.BaselineRef)
	}
	if len(r.Findings) == 0 {
		return fmt.Sprintf("The candidate behaved identically to%s across %s.",
			orTheBaseline(against), plural(len(r.Probes), "request", "requests"))
	}
	counts := Count(r.Findings)
	var parts []string
	for _, sev := range []Severity{Critical, Major, Minor} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
		}
	}
	if against != "" {
		against = " against" + against
	}
	return fmt.Sprintf("%s%s: %s.",
		plural(len(r.Findings), "difference", "differences"),
		against, strings.Join(parts, ", "))
}

// Sort puts the findings in reading order. Called by the comparison entry
// points; exported so a caller that appends its own can restore the order.
func (r *Result) Sort() { sortFindings(r.Findings) }

// orTheBaseline names the baseline, or says "the baseline" when the caller has
// not resolved a revision, which is what a comparison run outside a repository
// looks like.
func orTheBaseline(named string) string {
	if named == "" {
		return " the baseline"
	}
	return named
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func short(ref string) string {
	if len(ref) > 8 && isHex(ref) {
		return ref[:8]
	}
	return ref
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return s != ""
}
