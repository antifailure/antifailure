package masking_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

// What af mask init writes has to leave af mask plan with nothing to ask.
//
// The file is one explicit rule per column, and the proof is the round trip:
// classify a schema with the defaults, write the rules down, read them back
// as the engine would, classify again, and the plan reports zero problems and
// zero unclassified columns. A draft that produced either would be a file
// that says every column was decided while the plan still had questions.

func draftSchema() []masking.Table {
	return []masking.Table{
		{Schema: "public", Name: "users", PrimaryKey: []string{"id"}, Columns: []masking.ColumnInfo{
			{Name: "id", Type: "uuid"},
			{Name: "email", Type: "text", Unique: true},
			{Name: "full_name", Type: "text", Nullable: true},
			{Name: "password_hash", Type: "text"},
			{Name: "created_at", Type: "timestamp with time zone"},
			// Nothing recognises these four, and each is a different case.
			{Name: "internal_notes", Type: "text", Nullable: true},
			{Name: "preferences", Type: "jsonb"},
			{Name: "handle", Type: "text", Unique: true},
			{Name: "tagline", Type: "text"},
		}},
		{Schema: "public", Name: "orders", PrimaryKey: []string{"id"}, Columns: []masking.ColumnInfo{
			{Name: "id", Type: "bigint"},
			{Name: "customer_email", Type: "text", Nullable: true},
			{Name: "total_cents", Type: "integer"},
			{Name: "tags", Type: "ARRAY", Nullable: true},
			{Name: "nickname", Type: "citext"},
		}},
	}
}

func draft(t *testing.T, tables []masking.Table) []masking.Rule {
	t.Helper()
	defaults, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	return masking.DraftRules(tables, defaults.Assign(tables))
}

func TestDraftRules_LeaveThePlanWithNothingToAsk(t *testing.T) {
	tables := draftSchema()
	rules := draft(t, tables)

	rs, err := masking.NewRuleSet(rules)
	require.NoError(t, err, "a rule the engine refuses to compile")
	plan := masking.BuildPlan(tables, rs.Assign(tables), "")
	require.Empty(t, plan.Problems, "a drafted rule the plan cannot carry out:\n%s",
		masking.DescribeProblems(plan.Problems))
	require.Empty(t, plan.Unclassified, "a column the draft left for the plan to ask about")
}

func TestDraftRules_RestateTheDefaultsAndEmptyTheRest(t *testing.T) {
	rules := draft(t, draftSchema())
	byColumn := map[string]masking.Rule{}
	for _, r := range rules {
		byColumn[r.Table+"."+r.Column] = r
	}

	// A column a default matched gets the default's transform and reason.
	email := byColumn["public.users.email"]
	require.Equal(t, "email", email.Transform)
	require.Equal(t, "An address a real person reads.", email.Why)
	require.Equal(t, "email", byColumn["public.orders.customer_email"].Transform)
	require.Equal(t, "hash_hex", byColumn["public.users.password_hash"].Transform)

	// A column nothing matched is emptied the way its shape allows, and the
	// reason says it was unrecognised.
	notes := byColumn["public.users.internal_notes"]
	require.Equal(t, "nullify", notes.Transform)
	require.Contains(t, notes.Why, "Nothing recognised this column")
	require.Contains(t, notes.Why, "until somebody says otherwise")
	require.Equal(t, "empty_json", byColumn["public.users.preferences"].Transform)
	require.Equal(t, "hash_hex", byColumn["public.users.handle"].Transform,
		"nullify on a unique column makes every row collide")
	require.Equal(t, "free_text", byColumn["public.users.tagline"].Transform,
		"a column that cannot hold null cannot be nullified")
	require.Equal(t, "nullify", byColumn["public.orders.tags"].Transform)
	preserved := byColumn["public.orders.nickname"]
	require.Equal(t, "preserve", preserved.Transform,
		"a type this package cannot rewrite and cannot null is copied, and the file says so")
	require.Contains(t, preserved.Why, "citext")

	// Numbers, times and identifiers get no rule at all.
	for _, structural := range []string{"public.users.id", "public.users.created_at",
		"public.orders.id", "public.orders.total_cents"} {
		_, present := byColumn[structural]
		require.False(t, present, "%s holds nothing about a person and needs no rule", structural)
	}
}

// The file is read back by the same decoder the engine uses, and every rule
// survives the trip. Unquoted, a reason with a colon and a space in it is a
// YAML mapping and the file does not parse, and a column whose name begins
// with a hash is a comment and is silently gone. The rules below include both
// on purpose, because the drafted reasons happen to contain neither and a
// fixture that cannot fail is not a test of the quoting.
func TestRulesFile_ReadsBackAsTheRulesThatWereWritten(t *testing.T) {
	tables := draftSchema()
	tables[0].Columns = append(tables[0].Columns, masking.ColumnInfo{Name: "on", Type: "text", Nullable: true})
	rules := append(draft(t, tables),
		masking.Rule{Table: "public.users", Column: "#tag", Transform: "preserve",
			Why: "Kept: the team said so, and \"said so\" is on record."},
	)
	body := masking.RulesFile(rules, "the source named by PRODUCTION_DATABASE_URL", len(tables), 15)

	var file struct {
		Rules []masking.Rule `yaml:"rules"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(body), &file))
	require.Len(t, file.Rules, len(rules))
	for i := range rules {
		require.Equal(t, rules[i].Table, file.Rules[i].Table)
		require.Equal(t, rules[i].Column, file.Rules[i].Column)
		require.Equal(t, rules[i].Transform, file.Rules[i].Transform)
		require.Equal(t, rules[i].Why, file.Rules[i].Why)
	}
	require.Contains(t, body, "Written by 'af mask init' from the source named by PRODUCTION_DATABASE_URL")
	require.Contains(t, body, "2 tables and 15 columns")
}
