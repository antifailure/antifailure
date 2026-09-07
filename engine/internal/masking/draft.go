package masking

import (
	"fmt"
	"sort"
	"strings"
)

// DraftRules writes down, one explicit rule per column, what the defaults
// decided about a schema, so that the decision is in a file somebody can read
// and edit rather than in this package's source.
//
// Two kinds of column get a rule. A column a default rule matched gets the
// same transform, restated with the default's own reason, so `af mask plan`
// on the result shows it as a rule somebody wrote rather than one they were
// handed. A column no rule matched gets a rule that empties it where that is
// possible, with a reason saying it was unrecognised and is emptied until
// somebody says otherwise. The result is a plan with nothing left to ask:
// zero unclassified columns and zero problems.
//
// Where emptying is not possible the rule says what happened instead of
// pretending. A column that cannot hold null and holds free text is emptied
// through free_text, which writes a sentence in place of the one that was
// there; one under a unique constraint is hashed, because two emptied rows
// would collide; a type this package cannot transform is preserved, and the
// reason names it as the one thing on the list a person still has to decide.
//
// Structural columns, the numbers, times and identifiers, get no rule. Assign
// never asks about them, so a rule would be noise.
func DraftRules(tables []Table, assignments []Assignment) []Rule {
	var rules []Rule
	for _, a := range assignments {
		if a.Column.Generated {
			// Nothing can be written to it, so no rule can apply, and a rule
			// that cannot apply is a Problem in every plan that reads it.
			continue
		}
		switch {
		case a.Unmatched:
			rules = append(rules, unmatchedRule(a))
		case a.Transform != "":
			rules = append(rules, Rule{
				Table: a.Table.String(), Column: a.Column.Name,
				Transform: a.Transform, Link: linkFor(a),
				Why: a.Why,
			})
		}
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Table != rules[j].Table {
			return rules[i].Table < rules[j].Table
		}
		return rules[i].Column < rules[j].Column
	})
	return rules
}

// linkFor keeps the link only when it is not the transform's own name, which
// is what Assign fills in anyway. Writing the implicit link out would make
// every rule two lines longer and say nothing.
func linkFor(a Assignment) string {
	if a.Link == a.Transform {
		return ""
	}
	return a.Link
}

// unmatchedRule is the rule for a column the classifier could not place.
//
// Classified on the canonical type for the same reason Assign is: this file
// writes down what Assign decided, and a draft that disagreed with the plan it
// describes would be a rules file that changes the plan the moment somebody
// applies it.
func unmatchedRule(a Assignment) Rule {
	c := a.Column
	cc := canonical(a.Table, c)
	r := Rule{Table: a.Table.String(), Column: c.Name}
	const because = "Nothing recognised this column, so it is emptied until somebody says otherwise."
	switch {
	case isJSON(cc):
		r.Transform, r.Why = "empty_json", because
	case c.Nullable && !c.Unique:
		r.Transform, r.Why = "nullify", because
	case !looksSensitive(cc):
		// Not a type this package can rewrite, so no transform would run.
		// Preserved and said so, which is the one line in the file that is
		// a question rather than an answer.
		r.Transform = "preserve"
		r.Why = fmt.Sprintf("Nothing recognised this column and its type %s cannot be emptied here, "+
			"so it is copied unchanged. Decide whether that is fine.", c.Type)
	case c.Unique:
		r.Transform = "hash_hex"
		r.Why = "Nothing recognised this column, and it is unique, so it is replaced by a hash " +
			"rather than emptied, which would make every row collide."
	default:
		r.Transform = "free_text"
		r.Why = "Nothing recognised this column, and it cannot hold null, so it is replaced by " +
			"generated text until somebody says otherwise."
	}
	return r
}

// RulesFile renders rules as the masking.yaml the engine reads, with a
// header saying what the file is and how it came to be.
func RulesFile(rules []Rule, source string, tables, columns int) string {
	var b strings.Builder
	b.WriteString("# Masking rules. https://antifailure.dev/docs/concepts/masking\n")
	b.WriteString("#\n")
	b.WriteString("# Written by 'af mask init' from ")
	b.WriteString(source)
	fmt.Fprintf(&b, ", %d tables and %d columns.\n", tables, columns)
	b.WriteString("# One rule per column that carries something about a person, and one per\n")
	b.WriteString("# column nothing recognised, which is emptied until somebody says otherwise.\n")
	b.WriteString("# Each rule says why. Edit freely, and 'af mask plan' shows the result column\n")
	b.WriteString("# by column. A column added later that no rule names is emptied by default and\n")
	b.WriteString("# listed by the plan, so this file cannot go stale silently.\n")
	b.WriteString("#\n")
	b.WriteString("# Numbers, times and identifiers are not listed: nothing is done to them.\n")
	b.WriteString("\n")
	b.WriteString("rules:\n")
	for _, r := range rules {
		fmt.Fprintf(&b, "  - table: %s\n", yamlString(r.Table))
		fmt.Fprintf(&b, "    column: %s\n", yamlString(r.Column))
		fmt.Fprintf(&b, "    transform: %s\n", r.Transform)
		if r.Link != "" {
			fmt.Fprintf(&b, "    link: %s\n", yamlString(r.Link))
		}
		fmt.Fprintf(&b, "    why: %s\n", yamlString(r.Why))
	}
	return b.String()
}

// yamlString quotes a scalar so a column called `on` or a reason with a colon
// in it still reads back as the same string.
func yamlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
