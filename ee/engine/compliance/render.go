package compliance

// Rendering a report as something somebody hands over.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Markdown, because the document's destination is a ticket, an email, or a
// shared drive, and it should read as well in a plain terminal as it does
// rendered. A PDF would need a rendering dependency and would be worse in every
// place this actually ends up.
//
// The header says what the document is not, before it says anything else. A
// reader who takes this for an audit report has been misled by us, and the only
// reliable place to prevent that is the first thing they read.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Markdown renders a report.
func (r Report) Markdown() string {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s evidence: %s\n\n", r.Pack.Name, r.Org)
	fmt.Fprintf(&b, "%s\n\n", r.Pack.Note)
	fmt.Fprintf(&b, "- Framework revision: %s\n", r.Pack.Revision)
	fmt.Fprintf(&b, "- Period: %s to %s\n",
		r.From.UTC().Format("2 January 2006"), r.To.UTC().Format("2 January 2006"))
	fmt.Fprintf(&b, "- Generated: %s\n\n", r.GeneratedAt.UTC().Format("2 January 2006 15:04 MST"))

	counts := r.Counts()
	b.WriteString("## Summary\n\n")
	b.WriteString("| Outcome | Controls | What it means |\n")
	b.WriteString("| --- | --- | --- |\n")
	fmt.Fprintf(&b, "| Evidenced | %d | An artifact exists and says what the control asks about. |\n",
		counts[StateEvidenced])
	fmt.Fprintf(&b, "| Not evidenced | %d | The check ran and there was nothing to show. Not a failure. |\n",
		counts[StateNotEvidenced])
	fmt.Fprintf(&b, "| Failed | %d | The check found evidence that the control is not holding. |\n",
		counts[StateFailed])
	fmt.Fprintf(&b, "| Outside this product | %d | Real controls this system records nothing about. |\n\n",
		counts[StateOutside])

	if r.Failed() {
		// Above the detail, because somebody skimming has to see it. A finding
		// that only appears in row nine of a table is a finding that gets
		// missed by the person who most needed it.
		b.WriteString("> **Something is wrong.** One or more controls have evidence of not " +
			"holding. Those rows are marked `failed` below and each names the artifact.\n\n")
	}
	if len(r.Incomplete) > 0 {
		b.WriteString("> **This report is partial.** Some evidence could not be read, so the " +
			"controls below rest on less than the full period:\n>\n")
		for _, note := range r.Incomplete {
			fmt.Fprintf(&b, "> - %s\n", note)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Controls\n\n")
	for _, result := range r.Results {
		fmt.Fprintf(&b, "### %s %s\n\n", result.Control.ID, result.Control.Title)
		fmt.Fprintf(&b, "**Outcome: %s**\n\n", result.State)
		fmt.Fprintf(&b, "*The framework asks:* %s\n\n", result.Control.Requirement)
		// The scope goes above the finding, because it is what makes the
		// finding readable. "Evidenced" means nothing until somebody knows how
		// much of the control it covers.
		fmt.Fprintf(&b, "*What this product covers:* %s\n\n", result.Control.Scope)
		fmt.Fprintf(&b, "*What the evidence shows:* %s\n\n", result.Detail)
		if len(result.Artifacts) > 0 {
			b.WriteString("*Artifacts:*\n\n")
			for _, artifact := range result.Artifacts {
				fmt.Fprintf(&b, "- `%s`\n", artifact)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// JSON renders the report for a control plane or a pipeline.
//
// A separate shape from the internal one, so that adding a field to the report
// does not change a document somebody is parsing. It carries the state and the
// artifacts and deliberately not the prose, which is for a person.
func (r Report) JSON() ([]byte, error) {
	type control struct {
		ID        string   `json:"id"`
		Title     string   `json:"title"`
		State     string   `json:"state"`
		Detail    string   `json:"detail"`
		Artifacts []string `json:"artifacts,omitempty"`
	}
	type document struct {
		Framework   string    `json:"framework"`
		Revision    string    `json:"revision"`
		Org         string    `json:"org"`
		From        string    `json:"from"`
		To          string    `json:"to"`
		GeneratedAt string    `json:"generated_at"`
		Failed      bool      `json:"failed"`
		Incomplete  []string  `json:"incomplete,omitempty"`
		Controls    []control `json:"controls"`
	}

	out := document{
		Framework: r.Pack.Name, Revision: r.Pack.Revision, Org: r.Org,
		From: r.From.UTC().Format("2006-01-02"), To: r.To.UTC().Format("2006-01-02"),
		GeneratedAt: r.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Failed:      r.Failed(), Incomplete: r.Incomplete,
	}
	for _, result := range r.Results {
		out.Controls = append(out.Controls, control{
			ID: result.Control.ID, Title: result.Control.Title,
			State: string(result.State), Detail: result.Detail, Artifacts: result.Artifacts,
		})
	}
	return json.MarshalIndent(out, "", "  ")
}
