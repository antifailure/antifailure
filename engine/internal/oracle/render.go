package oracle

import (
	"fmt"
	"sort"
	"strings"
)

// Rendering lives in this package rather than in internal/report, and that is a
// deliberate boundary rather than an accident of who wrote what.
//
// internal/report renders one pull request comment for a run: workflows,
// invariants, egress, load. The oracle produces a different shape, it can run
// on its own without a run around it, and the comment has to be able to carry
// only a summary of it while the full thing goes to a file. Keeping the
// rendering here means the summary and the full report are the same code with
// a different bound, and the comment can embed Summary without this package
// knowing what a comment is.

// Marker identifies an oracle comment so an update replaces it rather than
// adding a second one, for the reason report.Marker exists.
const Marker = "<!-- antifailure:oracle -->"

// Summary is the short form: the headline, the counts, and the worst few
// findings. It is what belongs in a pull request comment beside everything
// else a run produced.
func (r Result) Summary(limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", r.Headline())
	fmt.Fprintf(&b, "Baseline %s (%s), candidate %s.",
		short(r.BaselineRef), r.BaselineHow, short(r.CandidateRef))
	if r.Golden != "" {
		fmt.Fprintf(&b, " Both branched golden `%s`.", r.Golden)
	}
	b.WriteString("\n\n")

	if len(r.Findings) == 0 {
		b.WriteString(r.Ignored.Describe())
		return b.String()
	}

	shown := r.Findings
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	b.WriteString(findingsTable(shown))
	if len(shown) < len(r.Findings) {
		fmt.Fprintf(&b, "\n%s more, in the full report.\n",
			plural(len(r.Findings)-len(shown), "difference", "differences"))
	}
	b.WriteString("\n")
	b.WriteString(r.Ignored.Describe())
	return b.String()
}

// Markdown is the full report.
func (r Result) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Differential oracle: %s\n\n", r.Headline())
	fmt.Fprintf(&b, "Baseline `%s` (%s) against candidate `%s`.",
		short(r.BaselineRef), r.BaselineHow, short(r.CandidateRef))
	if r.Golden != "" {
		fmt.Fprintf(&b, " Both sides branched golden `%s`, so they started from the same rows.",
			r.Golden)
	}
	b.WriteString("\n\n")

	if len(r.Probes) > 0 {
		b.WriteString("| Request | Baseline | Candidate | Differences |\n")
		b.WriteString("| --- | --- | --- | --- |\n")
		for _, p := range r.Probes {
			fmt.Fprintf(&b, "| `%s %s` | %s | %s | %d |\n",
				orDefaultMethod(p.Method), p.Path,
				describeResponse(p.Baseline), describeResponse(p.Candidate), p.Findings)
		}
		b.WriteString("\n")
	}

	if len(r.Findings) > 0 {
		b.WriteString(findingsTable(r.Findings))
		b.WriteString("\n")
	}

	if d := r.Database; d != nil {
		fmt.Fprintf(&b, "Database: %s compared, %s read on the candidate side, bound %d rows a table.\n",
			plural(d.TablesCompared, "table", "tables"),
			plural(d.RowsCompared, "row", "rows"), d.MaxRows)
		for _, note := range d.NotCompared {
			fmt.Fprintf(&b, "- %s\n", note)
		}
		b.WriteString("\n")
	}

	for _, note := range r.Notes {
		fmt.Fprintf(&b, "%s\n", note)
	}
	if len(r.Notes) > 0 {
		b.WriteString("\n")
	}

	b.WriteString(r.Ignored.Describe())
	return b.String()
}

// Comment returns the full report with the marker, for a pull request.
func (r Result) Comment() string { return Marker + "\n" + r.Markdown() }

// findingsTable renders the differences, worst first.
func findingsTable(findings []Finding) string {
	var b strings.Builder
	b.WriteString("| Severity | What | Where | Baseline | Candidate |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, f := range findings {
		where := f.Where
		if f.Path != "" {
			where += " " + f.Path
		}
		if f.Phase != "" {
			where += " (" + string(f.Phase) + ")"
		}
		what := string(f.Kind)
		if f.Detail != "" {
			what += ", " + f.Detail
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` | %s | %s |\n",
			f.Severity, cell(what), where, cell(f.Baseline), cell(f.Candidate))
	}
	return b.String()
}

// Text is the terminal rendering: the same facts without table pipes.
func (r Result) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", r.Headline())
	fmt.Fprintf(&b, "baseline %s (%s), candidate %s",
		short(r.BaselineRef), r.BaselineHow, short(r.CandidateRef))
	if r.Golden != "" {
		fmt.Fprintf(&b, ", golden %s", r.Golden)
	}
	b.WriteString("\n\n")

	for _, p := range r.Probes {
		fmt.Fprintf(&b, "  %-8s %-30s %s against %s, %s\n",
			orDefaultMethod(p.Method), p.Path,
			describeResponse(p.Baseline), describeResponse(p.Candidate),
			plural(p.Findings, "difference", "differences"))
	}
	if len(r.Probes) > 0 {
		b.WriteString("\n")
	}

	bySeverity := map[Severity][]Finding{}
	for _, f := range r.Findings {
		bySeverity[f.Severity] = append(bySeverity[f.Severity], f)
	}
	for _, sev := range []Severity{Critical, Major, Minor} {
		list := bySeverity[sev]
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s\n", strings.ToUpper(sev.String()))
		for _, f := range list {
			where := f.Where
			if f.Path != "" {
				where += " " + f.Path
			}
			if f.Phase != "" {
				where += " (" + string(f.Phase) + ")"
			}
			fmt.Fprintf(&b, "  %s  %s\n", where, f.Kind)
			if f.Detail != "" {
				fmt.Fprintf(&b, "      %s\n", f.Detail)
			}
			if f.Baseline != "" || f.Candidate != "" {
				fmt.Fprintf(&b, "      baseline  %s\n", oneLine(f.Baseline))
				fmt.Fprintf(&b, "      candidate %s\n", oneLine(f.Candidate))
			}
		}
		b.WriteString("\n")
	}

	if d := r.Database; d != nil {
		fmt.Fprintf(&b, "database: %s compared, %s read, bound %d rows a table\n",
			plural(d.TablesCompared, "table", "tables"),
			plural(d.RowsCompared, "row", "rows"), d.MaxRows)
		for _, note := range d.NotCompared {
			fmt.Fprintf(&b, "  %s\n", note)
		}
		b.WriteString("\n")
	}
	for _, note := range r.Notes {
		fmt.Fprintf(&b, "%s\n", note)
	}
	b.WriteString(r.Ignored.Describe())
	return b.String()
}

func describeResponse(r Response) string {
	if r.Err != "" {
		return "no answer"
	}
	if r.ContentType != "" {
		return fmt.Sprintf("%d %s", r.Status, r.ContentType)
	}
	return fmt.Sprintf("%d", r.Status)
}

func orDefaultMethod(m string) string {
	if m == "" {
		return "GET"
	}
	return m
}

// cell keeps a table cell a table cell.
func cell(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	const max = 160
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

// KindCounts groups the findings by kind, for a caller that wants a one line
// summary without walking the list.
func KindCounts(findings []Finding) []string {
	counts := map[Kind]int{}
	for _, f := range findings {
		counts[f.Kind]++
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, fmt.Sprintf("%d %s", counts[Kind(k)], k))
	}
	return out
}
