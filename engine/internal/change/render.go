package change

import (
	"fmt"
	"sort"
	"strings"
)

// Headline is the one sentence a reader gets for free.
//
// It names what the diff touched and how many checks the plan selects. It does
// not grade the change, because this package has no opinion about the change:
// a headline saying "high risk" would be the thing the whole design is built
// to avoid saying.
func (p *Profile) Headline() string {
	switch {
	case p.Files == 0:
		return "No changed files were found, so every check is selected."
	case p.Everything && len(p.Unclassified) == 1 && p.Files == 1:
		return "1 file changed and no rule recognises it, so every check is selected."
	case p.Everything && len(p.Unclassified) > 0:
		return fmt.Sprintf("%s changed and %d of them match no rule, so every check is selected.",
			plural(p.Files, "file", "files"), len(p.Unclassified))
	case p.Everything:
		return fmt.Sprintf("%s changed and the diff was too large to classify, so every check is selected.",
			plural(p.Files, "file", "files"))
	}

	var runs, gaps int
	for _, s := range p.Plan {
		switch {
		case s.Run():
			runs++
		case s.Selected:
			gaps++
		}
	}

	names := surfaceNames(p)
	touching := ""
	if len(names) > 0 {
		touching = ", touching " + englishList(names)
	}

	// The count is of checks that will actually run, not of checks selected.
	// Those differ whenever the manifest has one turned off, and a headline
	// that counted selections would promise work nothing is going to do.
	// "and N more" only reads correctly after a count it is more than. With
	// nothing running there is nothing to be more than, and the sentence has
	// to say the whole of the bad news on its own.
	var tail string
	switch {
	case runs == 0 && gaps == 0:
		tail = "No check will run."
	case runs == 0:
		tail = "No check will run: " +
			plural(gaps, "check is selected and not configured",
				"checks are selected and not configured") + "."
	case gaps == 0:
		tail = plural(runs, "check will run", "checks will run") + "."
	default:
		tail = plural(runs, "check will run", "checks will run") + ", and " +
			plural(gaps, "more is selected and not configured",
				"more are selected and not configured") + "."
	}
	return fmt.Sprintf("%s changed%s. %s", plural(p.Files, "file", "files"), touching, tail)
}

// surfaceNames returns the surfaces in the words a person would use, most
// specific first, so a headline reads as a sentence rather than as a set.
func surfaceNames(p *Profile) []string {
	seen := map[string]bool{}
	var services []string
	for _, f := range p.Facts {
		if f.Surface == SurfaceService {
			if !seen["svc:"+f.Subject] {
				seen["svc:"+f.Subject] = true
				services = append(services, f.Subject)
			}
			continue
		}
		seen[string(f.Surface)] = true
	}
	sort.Strings(services)

	var out []string
	if seen[string(SurfaceSchema)] {
		out = append(out, "the schema")
	}
	switch {
	case len(services) == 1:
		out = append(out, "the "+services[0]+" service")
	case len(services) > 1:
		out = append(out, englishList(services)+" services")
	}
	if seen[string(SurfaceEgress)] {
		out = append(out, "an outbound host")
	}
	for _, s := range []struct {
		surface Surface
		name    string
	}{
		{SurfaceManifest, "the antifailure manifest"},
		{SurfaceMasking, "the masking rules"},
		{SurfaceDependency, "a dependency"},
		{SurfaceBuild, "the build"},
		{SurfaceConfig, "configuration"},
		{SurfaceInfrastructure, "infrastructure"},
		{SurfacePipeline, "continuous integration"},
		{SurfaceTest, "your test suite"},
		{SurfaceAsset, "served assets"},
		{SurfaceDocs, "documentation"},
	} {
		if seen[string(s.surface)] {
			out = append(out, s.name)
		}
	}
	if seen[string(SurfaceCode)] && len(services) == 0 {
		out = append(out, "application source")
	}
	return out
}

func englishList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// Markdown renders the section a pull request comment carries.
//
// The shape follows the rest of the report: the answer first, the table next,
// and the reasoning folded away, because the audience did not ask for this
// comment and has thirty seconds.
func (p *Profile) Markdown() string {
	var b strings.Builder

	fmt.Fprintf(&b, "**What this change touches.** %s\n\n", p.Headline())
	if p.Base != "" {
		fmt.Fprintf(&b, "Measured against `%s`.\n\n", p.Base)
	}

	b.WriteString("| Check | This change | Available |\n| --- | --- | --- |\n")
	for _, s := range p.Plan {
		state := "not selected"
		if s.Selected {
			state = "selected"
		}
		avail := "yes"
		if !s.Available {
			avail = s.Unavailable
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", s.Check, state, avail)
	}
	b.WriteString("\n")

	if gaps := p.Gaps(); len(gaps) > 0 {
		b.WriteString("**Selected and unavailable.** ")
		var parts []string
		for _, g := range gaps {
			parts = append(parts, fmt.Sprintf("`%s`, because %s", g.Check, g.Unavailable))
		}
		b.WriteString(englishList(parts) + ".\n\n")
	}

	b.WriteString("<details><summary>Why each conclusion</summary>\n\n")
	b.WriteString("| File | Surface | Rule | Evidence |\n| --- | --- | --- | --- |\n")
	for _, f := range p.Facts {
		subject := string(f.Surface)
		if f.Subject != "" {
			subject += " " + f.Subject
		}
		fmt.Fprintf(&b, "| `%s`%s | %s | `%s` | %s |\n",
			f.Path, statusNote(f.Status), subject, f.Rule, f.Evidence)
	}
	if len(p.Unclassified) > 0 {
		for _, u := range p.Unclassified {
			fmt.Fprintf(&b, "| `%s` | unknown | none | no rule recognises this path, so every check is selected |\n", u)
		}
	}
	b.WriteString("\n</details>\n\n")

	b.WriteString("<details><summary>What this cannot see</summary>\n\n")
	for _, s := range p.Blind {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	b.WriteString("\n</details>\n\n")
	return b.String()
}

// statusNote marks a path the diff did something other than edit. Left off
// modified, which is most of them and would be noise on every row.
func statusNote(s Status) string {
	if s == "" || s == StatusModified {
		return ""
	}
	return " (" + string(s) + ")"
}

// Explain renders the profile for a terminal.
func (p *Profile) Explain() string {
	var b strings.Builder
	b.WriteString(p.Headline() + "\n\n")

	for _, s := range p.Plan {
		mark := "skip"
		if s.Selected {
			mark = "run"
		}
		detail := ""
		switch {
		case s.Selected && !s.Available:
			mark = "gap"
			detail = s.Unavailable
		case s.Selected && len(s.Because) > 0:
			detail = s.Because[0]
			if len(s.Because) > 1 {
				detail += fmt.Sprintf(" (and %d more)", len(s.Because)-1)
			}
		case !s.Available:
			detail = s.Unavailable
		default:
			detail = "nothing this change touches is exercised by it"
		}
		fmt.Fprintf(&b, "  %-5s %-12s %s\n", mark, s.Check, detail)
	}

	b.WriteString("\nWhy\n")
	for _, f := range p.Facts {
		subject := string(f.Surface)
		if f.Subject != "" {
			subject += " " + f.Subject
		}
		fmt.Fprintf(&b, "  %s%s\n    %s: %s [%s]\n",
			f.Path, statusNote(f.Status), subject, f.Evidence, f.Rule)
	}
	for _, u := range p.Unclassified {
		fmt.Fprintf(&b, "  %s\n    unknown: no rule recognises this path, so every check is selected [none]\n", u)
	}

	b.WriteString("\nWhat this cannot see\n")
	for _, s := range p.Blind {
		b.WriteString("  " + s + "\n")
	}
	return b.String()
}
