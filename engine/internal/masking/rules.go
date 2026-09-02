package masking

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Rules decide what happens to each column.
//
// The default is the important half. A column nobody wrote a rule for is
// reported as unclassified rather than left alone, because "left alone" for a
// column called customer_notes means the notes ship. The plan lists them and
// refuses to run until each one has an answer, even if the answer is "this is
// fine".
//
// Matching is by name and type rather than by an exhaustive list, so a column
// added tomorrow is classified today.

// Rule assigns a transform to the columns it matches.
type Rule struct {
	// Table matches the schema qualified table name, with * as a wildcard.
	// Empty matches every table.
	Table string `json:"table,omitempty" yaml:"table,omitempty"`
	// Column matches the column name, with * as a wildcard.
	Column string `json:"column,omitempty" yaml:"column,omitempty"`
	// Type matches the Postgres type name. Empty matches every type.
	Type string `json:"type,omitempty" yaml:"type,omitempty"`
	// Transform is the name of the transform to apply.
	Transform string `json:"transform" yaml:"transform"`
	// Link makes several columns mask identically. Two columns joined by a
	// foreign key must share one, or the join breaks the moment they are
	// masked to different values.
	Link string `json:"link,omitempty" yaml:"link,omitempty"`
	// Why is one sentence explaining the rule, printed by af mask plan.
	Why string `json:"why,omitempty" yaml:"why,omitempty"`

	table, column *regexp.Regexp
}

// compile prepares a rule for matching.
func (r *Rule) compile() error {
	if r.Transform == "" {
		return fmt.Errorf("masking: a rule for %s has no transform", r.describe())
	}
	if _, ok := Lookup(r.Transform); !ok {
		return fmt.Errorf("masking: %s names the transform %q, which does not exist; there is %s",
			r.describe(), r.Transform, strings.Join(Names(), ", "))
	}
	var err error
	if r.table, err = compileGlob(r.Table); err != nil {
		return fmt.Errorf("masking: the table pattern in %s: %w", r.describe(), err)
	}
	if r.column, err = compileGlob(r.Column); err != nil {
		return fmt.Errorf("masking: the column pattern in %s: %w", r.describe(), err)
	}
	return nil
}

func (r Rule) describe() string {
	parts := []string{}
	if r.Table != "" {
		parts = append(parts, "table "+r.Table)
	}
	if r.Column != "" {
		parts = append(parts, "column "+r.Column)
	}
	if r.Type != "" {
		parts = append(parts, "type "+r.Type)
	}
	if len(parts) == 0 {
		return "the catch all rule"
	}
	return "the rule for " + strings.Join(parts, ", ")
}

func compileGlob(pattern string) (*regexp.Regexp, error) {
	if pattern == "" || pattern == "*" {
		return nil, nil
	}
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// matches reports whether a rule applies, and how specifically.
//
// Specificity decides, not order, for the same reason it does in the egress
// policy: appending a rule must not silently change what an existing one does.
func (r Rule) matches(table Table, col ColumnInfo) (int, bool) {
	score := 0
	if r.table != nil {
		if !r.table.MatchString(table.String()) && !r.table.MatchString(table.Name) {
			return 0, false
		}
		score += 100
	}
	if r.column != nil {
		if !r.column.MatchString(col.Name) {
			return 0, false
		}
		// A literal column name is the most specific thing anybody writes.
		score += 1000
		if !strings.ContainsAny(r.Column, "*?") {
			score += 1000
		}
	}
	if r.Type != "" {
		if !strings.EqualFold(r.Type, col.Type) {
			return 0, false
		}
		score += 10
	}
	return score, true
}

// RuleSet is the rules plus the defaults they sit on top of.
type RuleSet struct {
	rules []Rule
}

// NewRuleSet compiles a set of rules.
//
// A rule that cannot be compiled is refused rather than skipped, because a
// masking configuration that silently enforces less than it says is the worst
// possible failure for this particular subsystem.
func NewRuleSet(rules []Rule) (*RuleSet, error) {
	out := make([]Rule, 0, len(rules)+len(DefaultRules()))
	for _, r := range rules {
		if err := r.compile(); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	// The defaults sit underneath, so a user rule for the same column always
	// wins on specificity and nobody has to restate what is already known.
	for _, r := range DefaultRules() {
		if err := r.compile(); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return &RuleSet{rules: out}, nil
}

// DefaultRules classify the columns that appear in almost every schema.
//
// They exist so that a first run of af mask plan is mostly answered rather
// than mostly questions. Every one is a name that means the same thing in
// every application anybody has written, and each carries the reason it is
// there so a reader can disagree with it specifically.
func DefaultRules() []Rule {
	return []Rule{
		{Column: "email", Transform: "email", Why: "An address a real person reads."},
		{Column: "email_address", Transform: "email", Why: "An address a real person reads."},
		{Column: "*_email", Transform: "email", Why: "An address a real person reads."},
		{Column: "username", Transform: "username", Why: "Identifies a person across systems."},
		{Column: "first_name", Transform: "first_name", Why: "A person's name."},
		{Column: "last_name", Transform: "last_name", Why: "A person's name."},
		{Column: "full_name", Transform: "name", Why: "A person's name."},
		{Column: "name", Type: "text", Transform: "name",
			Why: "A name column on a text type is usually a person's."},
		{Column: "phone", Transform: "phone", Why: "A number that rings a real phone."},
		{Column: "phone_number", Transform: "phone", Why: "A number that rings a real phone."},
		{Column: "*_phone", Transform: "phone", Why: "A number that rings a real phone."},
		{Column: "address", Transform: "address", Why: "Where somebody lives."},
		{Column: "address_line1", Transform: "address", Why: "Where somebody lives."},
		{Column: "address_line2", Transform: "address", Why: "Where somebody lives."},
		{Column: "street", Transform: "address", Why: "Where somebody lives."},
		{Column: "city", Transform: "city", Why: "Where somebody lives."},
		{Column: "postcode", Transform: "postcode", Why: "Where somebody lives."},
		{Column: "postal_code", Transform: "postcode", Why: "Where somebody lives."},
		{Column: "zip", Transform: "postcode", Why: "Where somebody lives."},
		{Column: "zip_code", Transform: "postcode", Why: "Where somebody lives."},
		{Column: "company", Transform: "company", Why: "Identifies a real organisation."},
		{Column: "company_name", Transform: "company", Why: "Identifies a real organisation."},
		{Column: "ip", Transform: "ip", Why: "Locates a person."},
		{Column: "ip_address", Transform: "ip", Why: "Locates a person."},
		{Column: "last_ip", Transform: "ip", Why: "Locates a person."},
		{Column: "password", Transform: "hash_hex", Why: "Never needs to survive, and must not."},
		{Column: "password_hash", Transform: "hash_hex", Why: "Never needs to survive, and must not."},
		{Column: "*_token", Transform: "hash_hex", Why: "Grants access if it escapes."},
		{Column: "*_secret", Transform: "hash_hex", Why: "Grants access if it escapes."},
		{Column: "api_key", Transform: "hash_hex", Why: "Grants access if it escapes."},
		{Column: "*_key", Type: "text", Transform: "hash_hex", Why: "Grants access if it escapes."},
		{Column: "session_token", Transform: "nullify", Why: "A live session must not survive into a copy."},
		{Column: "notes", Transform: "free_text", Why: "Free text holds whatever somebody typed."},
		{Column: "description", Transform: "free_text", Why: "Free text holds whatever somebody typed."},
		{Column: "bio", Transform: "free_text", Why: "Free text holds whatever somebody typed."},
		{Column: "comment", Transform: "free_text", Why: "Free text holds whatever somebody typed."},
		{Column: "body", Transform: "free_text", Why: "Free text holds whatever somebody typed."},
		{Column: "card_number", Transform: "credit_card", Why: "A payment instrument."},
		{Column: "*_url", Transform: "url", Why: "A URL can carry a token in its query."},
		{Column: "avatar", Transform: "url", Why: "A URL can carry a token in its query."},
	}
}

// Assignment is what a column gets, and why.
type Assignment struct {
	Table  Table
	Column ColumnInfo
	// Transform is the name to apply, or empty when nothing matched.
	Transform string
	// Link, when set, makes this column mask identically to others sharing it.
	Link string
	// Why explains the decision in one sentence.
	Why string
	// FromDefault reports whether a built in rule decided, rather than one the
	// user wrote. A plan shows these separately so somebody can see what was
	// decided for them.
	FromDefault bool
	// Unmatched reports that no rule named this column at all, so the
	// transform it carries is the fail-closed default rather than a decision
	// anybody made. A plan still lists it as a question, because "emptied
	// because nobody looked" is not the same fact as "emptied on purpose".
	Unmatched bool
	// Problem, when set, says why this column cannot be masked as assigned.
	Problem string
}

// Masked reports whether anything happens to this column.
func (a Assignment) Masked() bool { return a.Transform != "" && a.Problem == "" }

// Assign classifies every column of every table.
func (rs *RuleSet) Assign(tables []Table) []Assignment {
	defaults := map[string]bool{}
	for _, r := range DefaultRules() {
		defaults[r.describe()+"/"+r.Transform] = true
	}

	var out []Assignment
	for _, t := range tables {
		for _, c := range t.Columns {
			a := Assignment{Table: t, Column: c}

			best := -1
			for _, r := range rs.rules {
				score, ok := r.matches(t, c)
				if !ok || score <= best {
					continue
				}
				best = score
				a.Transform, a.Link, a.Why = r.Transform, r.Link, r.Why
				a.FromDefault = defaults[r.describe()+"/"+r.Transform]
			}

			// Nothing matched. A column nobody has classified is not a column
			// anybody has confirmed is safe, so free text and JSON are emptied
			// rather than copied.
			//
			// This is the documented behaviour and it was not the implemented
			// one. `Assign` used to leave an unmatched column with no transform,
			// `BuildPlan` skips a column with no transform, and the result was
			// that a `notes` column nobody had written a rule for was copied
			// verbatim into every preview environment. The plan did report it,
			// which is why this was survivable, and reporting is not the same
			// as refusing: a report is read once and a default runs every time.
			//
			// Only the types that can hold a sentence. A bigint called quantity
			// needs no rule and emptying it would break every environment for
			// nothing, which is how a fail-closed default gets turned off.
			if a.Transform == "" && !knownStructural(c) {
				a.Unmatched = true
				switch {
				case !looksSensitive(c):
					// A THIRD ANSWER, and the reason it exists.
					//
					// looksSensitive is a known-yes list of six types and there
					// was no known-no list, so everything else fell through with
					// no transform AND no Unmatched, which meant a citext column
					// was neither masked nor reported. Not in the plan at all.
					// That is worse than the not-null case below, which at least
					// prints a line saying what shipped.
					//
					// information_schema reports citext as USER-DEFINED and
					// text[] as ARRAY, so neither is exotic and neither was
					// visible. Reported and NOT masked: emptying it by default
					// would change what an existing golden holds, move every
					// rules_digest, and blank a column some environment needs.
					// Saying so is the half that costs nobody anything.
					a.Why = "This classifier does not recognise the type " + c.Type +
						", so nothing decided what happens to this column and it is " +
						"copied unchanged. The verification scan does not read this " +
						"type either. Give it a transform, or a rule saying it is fine."
				case isJSON(c):
					// A JSON column is emptied whether or not it can hold
					// null, because `empty_json` writes a value rather than
					// removing one. That matters: `jsonb NOT NULL DEFAULT '{}'`
					// is the ordinary way to hold a payload or a detail blob,
					// every one of those is free-form, and nullify is refused
					// on it. Without this the default for the commonest shape
					// of free-form column was to copy it.
					a.Transform = "empty_json"
					a.Link = ""
					a.Why = "No rule names this column, and free-form JSON holds whatever " +
						"the code that wrote it decided to include."
				case c.Generated:
					// The database computes it, so nothing can be written to
					// it at all. Assigning a transform here would turn a
					// column nobody classified into a plan that refuses to
					// run, which is a fail-closed default that closes the
					// wrong thing: the whole environment rather than the one
					// column.
					a.Why = "No rule names this column, and the database computes it, " +
						"so it cannot be emptied. Its value comes from columns that can be."
				case !c.Nullable:
					// It cannot hold null, so the default cannot apply. Said
					// out loud rather than silently skipped: this is the one
					// case where an unclassified column is copied as it is,
					// and the person reading the plan is the one who can fix
					// it with a rule.
					a.Why = "No rule names this column and it cannot hold null, so it is " +
						"copied unchanged. Give it a transform, or the verification scan " +
						"is the only thing standing between it and a preview environment."
				default:
					a.Transform = "nullify"
					a.Link = ""
					a.Why = "No rule names this column, and a column nobody has classified " +
						"is not one anybody has confirmed is safe."
				}
			}

			// Columns holding the same kind of thing mask identically unless
			// somebody says otherwise, and the default link is the transform's
			// own name.
			//
			// Without this each column derives its own subkey, so
			// customers.email and orders.customer_email would map one address
			// to two different fake addresses and the join between them would
			// stop working. A foreign key is the same case: the key column and
			// the column it references share a transform, so they share a
			// link, and the join survives without anybody writing it out.
			if a.Transform != "" && a.Link == "" {
				a.Link = a.Transform
			}
			out = append(out, checkFeasible(a))
		}
	}
	return out
}

// checkFeasible records why an assignment cannot be carried out.
//
// Caught here rather than at execution, because a masking run that fails
// halfway leaves a table partly masked, which is worse than not starting: the
// data is neither real nor safe, and nothing says which rows are which.
func checkFeasible(a Assignment) Assignment {
	if a.Transform == "" {
		return a
	}
	if a.Column.Generated {
		a.Problem = "the database computes this column, so it cannot be written to"
		return a
	}
	t, ok := Lookup(a.Transform)
	if !ok {
		a.Problem = "there is no transform called " + a.Transform
		return a
	}
	if a.Column.Unique && !t.PreservesUniqueness() {
		a.Problem = fmt.Sprintf(
			"%s does not preserve uniqueness and this column has a unique constraint, "+
				"so the update would fail partway through and leave the table half masked",
			a.Transform)
		return a
	}
	if a.Transform == "nullify" && !a.Column.Nullable {
		a.Problem = "the column is not nullable, so it cannot be set to null"
		return a
	}
	return a
}

// Unclassified returns the assignments nothing matched.
//
// These are the point of the whole exercise. A column called customer_notes
// that no rule covers holds whatever somebody typed. It is emptied, because
// the safe default is the one that runs when nobody is paying attention, and
// it is listed here because being emptied is not the same as being understood:
// a column that should have been `free_text` so the layout still gets three
// paragraphs, or `preserve` because it holds a currency code, is a rule
// somebody has to write.
func Unclassified(assignments []Assignment) []Assignment {
	var out []Assignment
	for _, a := range assignments {
		// Keyed on Unmatched rather than on an empty transform, because these
		// now carry one: they are emptied by default, and they are still the
		// list somebody has to work through. A column that is emptied because
		// nobody looked at it is a question, not an answer.
		if a.Unmatched {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Table.String() != out[j].Table.String() {
			return out[i].Table.String() < out[j].Table.String()
		}
		return out[i].Column.Name < out[j].Column.Name
	})
	return out
}

// looksSensitive decides which unmatched columns are worth asking about.
//
// A bigint called quantity is not worth a question, and asking about every
// column in the schema would produce a list nobody reads, which is the same as
// producing no list at all. Free text and unbounded strings are where anything
// unexpected actually lives.
// isJSON reports whether a column holds JSON, which decides how it is emptied.
func isJSON(c ColumnInfo) bool {
	switch strings.ToLower(c.Type) {
	case "json", "jsonb":
		return true
	}
	return false
}

func looksSensitive(c ColumnInfo) bool {
	switch strings.ToLower(c.Type) {
	case "text", "character varying", "character", "json", "jsonb", "xml":
		return true
	}
	return false
}

// knownStructural reports the types worth saying nothing about.
//
// The counterpart to looksSensitive, and the pair is the point: one is a
// known-yes list, this is a known-no list, and a type in NEITHER is one nobody
// has classified. Before this existed the absence of a known-no list meant
// every unrecognised type was silently treated as structural, so citext and
// text[] were passed over as though somebody had decided they were safe.
//
// A bigint called quantity genuinely needs no rule and asking about it would
// produce a list nobody reads, which is the same as producing no list. These
// are the types whose text form cannot carry a sentence somebody typed:
// numbers, times, booleans, and identifiers the database generates.
//
// Deliberately NOT here: bytea, inet, cidr, macaddr, hstore, tsvector, ARRAY
// and USER-DEFINED. A bytea holds whatever was uploaded, an inet locates
// somebody, and the last two are how information_schema reports every array
// and every extension or enum type, citext among them.
func knownStructural(c ColumnInfo) bool {
	switch strings.ToLower(c.Type) {
	case "smallint", "integer", "bigint", "decimal", "numeric", "real",
		"double precision", "money", "smallserial", "serial", "bigserial",
		"boolean", "uuid", "date", "time", "time without time zone",
		"time with time zone", "timestamp", "timestamp without time zone",
		"timestamp with time zone", "interval", "oid", "bit", "bit varying":
		return true
	}
	return false
}

// Problems returns the assignments that cannot be carried out.
func Problems(assignments []Assignment) []Assignment {
	var out []Assignment
	for _, a := range assignments {
		if a.Problem != "" {
			out = append(out, a)
		}
	}
	return out
}
