package fidelity

import (
	"fmt"
	"sort"
	"strings"
)

// Headline is the first line, and the only one most people read.
//
// It carries the definition with the number, every time, because a percentage
// with no definition is the shape of an invented statistic even when it is not
// one. The count comes first and the percentage second: "18 of 21" is a claim
// somebody can check against the table below it, and "86 percent" is not.
func (i Inventory) Headline() string {
	s := i.Score()
	pct, ok := s.Percent()
	if !ok {
		return "Nothing in this environment could be measured, so there is no score."
	}
	line := fmt.Sprintf("%d of %d measured components are production's own, which is %d percent.",
		s.Reproduced, s.Counted, pct)
	if n := len(s.Excluded); n > 0 {
		line += fmt.Sprintf(" %s excluded and named below.",
			plural(int64(n), "component or dimension is", "components and dimensions are"))
	}
	return line
}

// Explain renders the inventory for a person.
//
// The per dimension verdict comes before the score on purpose. The single
// dimension somebody's change touches is the one they need, and a percentage
// read first is a percentage that decides the question before the table gets a
// chance to.
func (i Inventory) Explain() string {
	var b strings.Builder

	for _, d := range i.Dimensions {
		fmt.Fprintf(&b, "%-14s %s\n", d.Name, verdictLine(d))
		for _, c := range d.Components {
			fmt.Fprintf(&b, "  %-12s %-12s %s\n", trim(c.Name, 12), c.State, c.Detail)
		}
		b.WriteString("\n")
	}

	b.WriteString(i.Headline() + "\n")
	b.WriteString("A component is production's own when the environment reaches the real thing.\n")
	b.WriteString("A substitution, a refusal and an absence are all counted and none of them\n")
	b.WriteString("counts as reproduced. Nothing unmeasured is counted either way.\n")

	if ex := i.Score().Excluded; len(ex) > 0 {
		b.WriteString("\nNot measured, and so not counted:\n")
		for _, e := range sortExclusions(ex) {
			if e.Component == "" {
				fmt.Fprintf(&b, "  %-14s %s\n", e.Dimension, e.Because)
				continue
			}
			fmt.Fprintf(&b, "  %-14s %s: %s\n", e.Dimension, e.Component, e.Because)
		}
	}
	return b.String()
}

// verdictLine renders a dimension's own answer.
func verdictLine(d Dimension) string {
	if d.NotApplicable != "" {
		return "not measured: " + d.NotApplicable
	}
	counts := d.Counts()
	var parts []string
	for _, s := range []State{Reproduced, Substituted, Refused, Absent, Unmeasured} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	return string(d.Verdict()) + " (" + strings.Join(parts, ", ") + ")"
}

// sortExclusions puts exclusions in a stable order so two runs of the same
// environment print the same bytes.
func sortExclusions(ex []Exclusion) []Exclusion {
	out := append([]Exclusion(nil), ex...)
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Dimension != out[b].Dimension {
			return out[a].Dimension < out[b].Dimension
		}
		return out[a].Component < out[b].Component
	})
	return out
}

// ExplainRequirements renders what the manifest required and what happened.
func ExplainRequirements(reqs []Requirement) string {
	if len(reqs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Required by the manifest:\n")
	for _, r := range reqs {
		switch {
		case r.Met:
			fmt.Fprintf(&b, "  %-14s reproduced\n", r.Dimension)
		case !r.Measurable:
			fmt.Fprintf(&b, "  %-14s not measured, so neither met nor broken: %s\n",
				r.Dimension, r.Because)
		default:
			fmt.Fprintf(&b, "  %-14s not reproduced: %s\n", r.Dimension, r.Because)
		}
	}
	return b.String()
}

func trim(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
