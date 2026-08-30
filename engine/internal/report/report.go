// Package report renders what a run found, for a person reading a pull
// request.
//
// The audience is somebody who did not ask for this comment and has thirty
// seconds. So the first line is the answer, the detail is folded away, and a
// blocked result is visibly not a failure. A comment that reads as a wall of
// red on a pull request that is fine is a comment people mute, and a muted
// comment is worse than none: it is a check everybody believes is running.
package report

import (
	"fmt"
	"sort"
	"strings"
)

// Run is everything one pull request check produced.
type Run struct {
	Environment  string
	URL          string
	Branch       string
	Commit       string
	Golden       string
	Workflows    []Workflow
	Invariants   []Invariant
	Load         *Load
	Egress       *Egress
	Verification *Verification
	Insights     *Insights
	Duration     string
	// DocsBase is where links point, so a self hosted instance can point at
	// its own copy rather than at ours.
	DocsBase string
}

// Workflow is one agent result.
type Workflow struct {
	Name    string
	Verdict string
	Detail  string
	Steps   []string
	Trace   string
}

// Invariant is what the data said after the workflows ran.
//
// Held and Error are separate for the same reason a workflow's failed and
// blocked are: an invariant that could not be asked has not found anything,
// and printing it as a violation would blame the change for our own gap.
type Invariant struct {
	Name        string
	Description string
	Held        bool
	Columns     []string
	Rows        [][]string
	More        bool
	Error       string
}

// Violated reports whether this invariant was shown to be broken.
func (i Invariant) Violated() bool { return i.Error == "" && !i.Held }

// Load is a traffic result.
type Load struct {
	Sent      int
	Rate      float64
	ErrorRate float64
	P95Ms     float64
	Regressed []string
}

// Egress summarises outbound traffic.
type Egress struct {
	Allowed  int
	Refused  int
	Captured int
	Mocked   int
	// Surprises are refused hosts nothing in the manifest mentions, which is
	// usually a dependency somebody added without noticing.
	Surprises []string
}

// Verification is the masking check on the golden.
type Verification struct {
	Clean       bool
	Columns     int
	RowsSampled int64
	Findings    []string
}

// Insights is what the database noticed while the environment ran.
//
// The part of a change nothing else in a pull request can see. A migration
// reviews as a diff and behaves as a plan, and the plan is only visible
// against real data volume, which is the one thing a preview environment has.
type Insights struct {
	// Sequential is a table the run scanned end to end often enough to be
	// worth naming, with how many rows it holds.
	Sequential []Scan
	// Slowest is the single query that spent the most time, and how much.
	Slowest   string
	SlowestMs float64
	// Unused names indexes nothing read, which on a preview is weaker
	// evidence than a scan and is still the thing somebody wants to know
	// before adding another one.
	Unused []string
	// Missing names the extensions that were not installed, so a section
	// that found nothing is distinguishable from one that could not look.
	Missing []string
}

// Scan is one table read end to end.
type Scan struct {
	Table string
	Scans int64
	Rows  int64
}

// Verdict is the one word answer for the whole run.
//
// A failure outranks everything, then flaky, then blocked. Blocked below flaky
// on purpose: a flaky workflow is a real signal about the application and a
// blocked one is a signal about us.
func (r Run) Verdict() string {
	counts := map[string]int{}
	for _, w := range r.Workflows {
		counts[w.Verdict]++
	}
	switch {
	case counts["fail"] > 0, r.InvariantsViolated() > 0:
		return "fail"
	case counts["flaky"] > 0:
		return "flaky"
	case counts["blocked"] > 0:
		return "blocked"
	case counts["unverified"] > 0:
		return "unverified"
	case len(r.Workflows) == 0:
		return "blocked"
	default:
		return "pass"
	}
}

// Headline is the first line, which is the only line most people read.
func (r Run) Headline() string {
	counts := map[string]int{}
	for _, w := range r.Workflows {
		counts[w.Verdict]++
	}
	switch r.Verdict() {
	case "pass":
		if len(r.Invariants) > 0 {
			return fmt.Sprintf("All %d workflows passed, and %s held.",
				len(r.Workflows), plural(len(r.Invariants), "invariant", "invariants"))
		}
		return fmt.Sprintf("All %d workflows passed.", len(r.Workflows))
	case "fail":
		// The invariant is named first when the workflows are all green,
		// because "3 workflows passed" above a failing run is the comment
		// people learn to stop believing.
		if counts["fail"] == 0 {
			return fmt.Sprintf("Every workflow passed and %s did not hold.",
				plural(r.InvariantsViolated(), "invariant", "invariants"))
		}
		if v := r.InvariantsViolated(); v > 0 {
			return fmt.Sprintf("%s failed, and %s did not hold.",
				plural(counts["fail"], "workflow", "workflows"),
				plural(v, "invariant", "invariants"))
		}
		return fmt.Sprintf("%s failed.", plural(counts["fail"], "workflow", "workflows"))
	case "flaky":
		return fmt.Sprintf("%s passed only sometimes.",
			plural(counts["flaky"], "workflow", "workflows"))
	case "blocked":
		if len(r.Workflows) == 0 {
			return "Nothing ran."
		}
		return fmt.Sprintf("%s could not be carried through. Nothing here counts against the change.",
			plural(counts["blocked"], "workflow", "workflows"))
	default:
		return fmt.Sprintf("%s ran without proving anything either way.",
			plural(counts["unverified"], "workflow", "workflows"))
	}
}

// InvariantsViolated counts the invariants shown to be broken.
func (r Run) InvariantsViolated() int {
	n := 0
	for _, i := range r.Invariants {
		if i.Violated() {
			n++
		}
	}
	return n
}

// invariantSection is what the data said, for the comment.
//
// One line when everything held, because a run where nothing is wrong should
// cost the reader one line. The violating rows are shown in full when
// something is wrong, since they are the diagnosis and a reader who has to go
// and run the query themselves has been told there is a problem and not what
// it is.
func (r Run) invariantSection() string {
	var b strings.Builder
	violated := r.InvariantsViolated()
	blocked := 0
	for _, i := range r.Invariants {
		if i.Error != "" {
			blocked++
		}
	}

	if violated == 0 && blocked == 0 {
		fmt.Fprintf(&b, "Invariants: %s held.\n\n",
			plural(len(r.Invariants), "invariant", "invariants"))
		return b.String()
	}

	for _, i := range r.Invariants {
		switch {
		case i.Error != "":
			fmt.Fprintf(&b, "Invariant `%s` could not be checked: %s Nothing here counts against the change.\n\n",
				i.Name, oneLine(i.Error))
		case i.Violated():
			fmt.Fprintf(&b, "**Invariant `%s` does not hold.**", i.Name)
			if i.Description != "" {
				fmt.Fprintf(&b, " %s", i.Description)
			}
			b.WriteString("\n\n")
			b.WriteString(evidenceTable(i))
		}
	}
	return b.String()
}

// evidenceTable renders the violating rows.
func evidenceTable(i Invariant) string {
	if len(i.Columns) == 0 || len(i.Rows) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "| %s |\n", strings.Join(i.Columns, " | "))
	b.WriteString("| " + strings.Repeat("--- | ", len(i.Columns)) + "\n")
	for _, row := range i.Rows {
		cells := make([]string, len(row))
		for j, c := range row {
			cells[j] = oneLine(c)
		}
		fmt.Fprintf(&b, "| %s |\n", strings.Join(cells, " | "))
	}
	if i.More {
		b.WriteString("\nMore rows than these. Run the statement against the branch to see them all.\n")
	}
	b.WriteString("\n")
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// symbol is the mark beside a verdict.
//
// Words rather than coloured circles, because a comment is read in a terminal,
// in an email digest, and by a screen reader, and only one of those renders an
// emoji usefully.
func symbol(verdict string) string {
	switch verdict {
	case "pass":
		return "passed"
	case "fail":
		return "FAILED"
	case "flaky":
		return "flaky"
	case "blocked":
		return "blocked"
	default:
		return "unverified"
	}
}

// Markdown renders the comment.
func (r Run) Markdown() string {
	docs := r.DocsBase
	if docs == "" {
		docs = "https://antifailure.dev/docs"
	}
	var b strings.Builder

	fmt.Fprintf(&b, "### Antifailure: %s\n\n", r.Headline())

	if r.URL != "" {
		fmt.Fprintf(&b, "Environment `%s` is at %s\n\n", r.Environment, r.URL)
	}

	if len(r.Workflows) > 0 {
		b.WriteString("| Workflow | Result | Detail |\n| --- | --- | --- |\n")
		sorted := append([]Workflow(nil), r.Workflows...)
		// Failures first. Somebody scrolling to find the failure is the same
		// as somebody not seeing it.
		sort.SliceStable(sorted, func(i, j int) bool {
			return rank(sorted[i].Verdict) < rank(sorted[j].Verdict)
		})
		for _, w := range sorted {
			detail := w.Detail
			if w.Verdict == "pass" {
				detail = ""
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", w.Name, symbol(w.Verdict), oneLine(detail))
		}
		b.WriteString("\n")
	}

	// Folded, because the audience did not ask for this comment. Somebody who
	// wants the steps opens it; everybody else reads four lines and moves on.
	for _, w := range r.Workflows {
		if w.Verdict == "pass" || len(w.Steps) == 0 {
			continue
		}
		fmt.Fprintf(&b, "<details><summary>How to see <code>%s</code> yourself</summary>\n\n", w.Name)
		for _, step := range w.Steps {
			fmt.Fprintf(&b, "%s\n", step)
		}
		if w.Trace != "" {
			fmt.Fprintf(&b, "\nTrace: `%s`\n", w.Trace)
		}
		b.WriteString("\n</details>\n\n")
	}

	if len(r.Invariants) > 0 {
		b.WriteString(r.invariantSection())
	}

	if v := r.Verification; v != nil {
		if v.Clean {
			fmt.Fprintf(&b,
				"Masking verified: %d columns read back, %d rows sampled, nothing that still parses as real.\n\n",
				v.Columns, v.RowsSampled)
		} else {
			fmt.Fprintf(&b, "**Masking did not verify.** %s\n\n", strings.Join(v.Findings, " "))
		}
	}

	if i := r.Insights; i != nil {
		switch {
		case len(i.Missing) > 0 && len(i.Sequential) == 0 && i.Slowest == "":
			// Said out loud rather than rendered as a clean result. An
			// insights section that looked at nothing and a section that
			// looked and found nothing are the same four words on a pull
			// request, and only one of them is evidence.
			fmt.Fprintf(&b, "Insights could not look: %s.\n\n", strings.Join(i.Missing, ", "))
		default:
			fmt.Fprintf(&b, "Insights: ")
			parts := []string{}
			if len(i.Sequential) > 0 {
				names := make([]string, 0, len(i.Sequential))
				for _, s := range i.Sequential {
					names = append(names, fmt.Sprintf("`%s` (%d rows)", s.Table, s.Rows))
				}
				parts = append(parts, "read end to end: "+strings.Join(names, ", "))
			}
			if i.Slowest != "" {
				parts = append(parts, fmt.Sprintf("slowest query %.0fms", i.SlowestMs))
			}
			if len(i.Unused) > 0 {
				parts = append(parts, fmt.Sprintf("%d indexes nothing read", len(i.Unused)))
			}
			if len(parts) == 0 {
				parts = append(parts, "no table read end to end and no slow query")
			}
			fmt.Fprintf(&b, "%s.\n\n", strings.Join(parts, "; "))
		}
	}

	if e := r.Egress; e != nil {
		fmt.Fprintf(&b, "Outbound: %d allowed, %d refused, %d captured, %d mocked.\n",
			e.Allowed, e.Refused, e.Captured, e.Mocked)
		if len(e.Surprises) > 0 {
			fmt.Fprintf(&b,
				"Refused hosts nothing in the manifest mentions: %s. If this change means to reach one, add a rule.\n",
				strings.Join(e.Surprises, ", "))
		}
		b.WriteString("\n")
	}

	if l := r.Load; l != nil {
		fmt.Fprintf(&b, "Load: %d requests at %.0f a second, p95 %.0fms, %.1f%% failed.\n",
			l.Sent, l.Rate, l.P95Ms, l.ErrorRate*100)
		if len(l.Regressed) > 0 {
			fmt.Fprintf(&b, "Slower than production: %s\n", strings.Join(l.Regressed, ", "))
		}
		b.WriteString("\n")
	}

	if r.Verdict() == "blocked" {
		fmt.Fprintf(&b,
			"Blocked means the environment or the runner could not carry a workflow through. "+
				"It is not counted against this change. [What blocked means](%s/concepts/verdicts)\n\n",
			docs)
	}

	fmt.Fprintf(&b, "<sub>%s", r.Branch)
	if r.Commit != "" {
		fmt.Fprintf(&b, " at `%s`", short(r.Commit))
	}
	if r.Duration != "" {
		fmt.Fprintf(&b, " in %s", r.Duration)
	}
	if r.Golden != "" {
		fmt.Fprintf(&b, ", from golden `%s`", r.Golden)
	}
	fmt.Fprintf(&b, ". <a href=\"%s\">Docs</a></sub>\n", docs)
	return b.String()
}

func rank(verdict string) int {
	switch verdict {
	case "fail":
		return 0
	case "flaky":
		return 1
	case "blocked":
		return 2
	case "unverified":
		return 3
	default:
		return 4
	}
}

// oneLine keeps a table cell a table cell.
func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// Marker identifies this comment so an update replaces it.
//
// A check that adds a comment per push turns a pull request with twelve pushes
// into one with twelve comments, and the twelfth is the only one that is true.
const Marker = "<!-- antifailure:report -->"

// Comment returns the comment body, with the marker.
func (r Run) Comment() string { return Marker + "\n" + r.Markdown() }
