package masking_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
)

// The two stores here are shaped like the product this wave exists for: a
// Postgres holding people and a ClickHouse holding the events about them, with
// the identifier that joins the two present in both.
//
// The column names and types are the ones that shape appears with in the wild.
// distinct_id is text in Postgres and String in ClickHouse; properties is jsonb
// on one side and a String holding JSON on the other, which is the case that
// makes the check earn its keep.

func postgresPeople() []masking.Table {
	return []masking.Table{{
		Engine: "postgres", Schema: "public", Name: "person",
		PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "bigint"},
			{Name: "uuid", Type: "uuid"},
			{Name: "distinct_id", Type: "text", Nullable: true},
			{Name: "email", Type: "text", Nullable: true},
			{Name: "properties", Type: "jsonb", Nullable: true},
			{Name: "created_at", Type: "timestamp with time zone"},
		},
	}}
}

func clickhouseEvents() []masking.Table {
	return []masking.Table{{
		Engine: "clickhouse", Schema: "default", Name: "events",
		PrimaryKey: []string{"uuid"},
		Columns: []masking.ColumnInfo{
			{Name: "uuid", Type: "UUID"},
			{Name: "event", Type: "String"},
			{Name: "distinct_id", Type: "String", Nullable: true},
			{Name: "email", Type: "Nullable(String)", Nullable: true},
			{Name: "properties", Type: "String", Nullable: true},
			{Name: "timestamp", Type: "DateTime64(6)"},
		},
	}}
}

// oneRulesFile is the point: ONE file, covering both stores, with the types
// written in the Postgres vocabulary because that is the vocabulary every rule
// anybody has already written is in.
func oneRulesFile(t *testing.T) *masking.RuleSet {
	t.Helper()
	rs, err := masking.NewRuleSet([]masking.Rule{
		{Column: "distinct_id", Transform: "hash_hex", Link: "person",
			Why: "The identifier that joins a person to their events."},
		{Column: "properties", Type: "text", Transform: "empty_json",
			Why: "ClickHouse holds this JSON in a String, and it is the same blob " +
				"the Postgres jsonb holds."},
	})
	require.NoError(t, err)
	return rs
}

func stores(t *testing.T, rs *masking.RuleSet) []masking.StoreAssignments {
	t.Helper()
	pg, ch := postgresPeople(), clickhouseEvents()
	return []masking.StoreAssignments{
		{Store: "primary", Assignments: rs.Assign(pg)},
		{Store: "events", Assignments: rs.Assign(ch)},
	}
}

// TestCrossStore_TheSameIdentityMasksToTheSamePersonInBothStores is the lane's
// acceptance and the number it publishes.
func TestCrossStore_TheSameIdentityMasksToTheSamePersonInBothStores(t *testing.T) {
	t.Parallel()
	report, err := masking.CrossStoreCheck(testKey(t), stores(t, oneRulesFile(t)), nil)
	require.NoError(t, err)

	require.True(t, report.OK(), "the report is not clean:\n%s", report.Summary())
	require.Equal(t, 100.0, report.Percent(), report.Summary())
	require.Positive(t, report.Checked, "nothing was compared, which is not a pass")
	t.Log("\n" + report.Summary())

	// And the value itself, not only the verdict. The check compares outputs,
	// so this asserts the same thing one level down: a person's address masks
	// to one address in both stores.
	pg := maskedValue(t, report, "primary", "public.person", "email")
	ch := maskedValue(t, report, "events", "default.events", "email")
	require.Equal(t, pg, ch,
		"one address masked into two people, which is the failure this lane exists for")
	require.NotEqual(t, "ada@lovelace-analytics.co.uk", pg, "the address was not masked at all")
}

// maskedValue masks one probe through one side of a reported pair, so a test
// can assert the value rather than only the verdict.
func maskedValue(t *testing.T, r masking.CrossStoreReport, store, table, column string) string {
	t.Helper()
	for _, p := range r.Pairs {
		for _, side := range []masking.JoinColumn{p.A, p.B} {
			if side.Store != store || side.Table != table || side.Column != column {
				continue
			}
			transform, ok := masking.Lookup(side.Transform)
			require.True(t, ok, side.Transform)
			in := "ada@lovelace-analytics.co.uk"
			out, err := transform.Apply(testKey(t), side.Identity, &in)
			require.NoError(t, err)
			require.NotNil(t, out)
			return *out
		}
	}
	t.Fatalf("%s.%s.%s is not in any reported pair", store, table, column)
	return ""
}

// TestCrossStore_CatchesAJoinKeyMaskedInOneStoreAndCopiedInTheOther is the
// defect the whole lane is named after.
//
// The second store's column is left with no transform, which is exactly what
// happened before the dialect existed: a ClickHouse String matched no rule
// carrying a Postgres type name, so nothing decided about the column and it was
// copied. The Postgres side then holds a fake address and the ClickHouse side
// holds the real one.
func TestCrossStore_CatchesAJoinKeyMaskedInOneStoreAndCopiedInTheOther(t *testing.T) {
	t.Parallel()
	rs := oneRulesFile(t)
	all := stores(t, rs)
	all[1].Assignments = withoutTransform(all[1].Assignments, "email")

	report, err := masking.CrossStoreCheck(testKey(t), all, nil)
	require.NoError(t, err)
	require.False(t, report.OK(), "a copied join key was reported as verified:\n%s", report.Summary())
	require.Less(t, report.Percent(), 100.0)

	// Which side is which, not only that a sentence was produced. The first
	// version of this named them the wrong way round, said the copied column
	// was "masked with" an empty transform, and passed: the assertions were
	// about the words rather than about the columns they were attached to.
	require.Contains(t, report.Summary(),
		"primary.public.person.email is masked with email and "+
			"events.default.events.email is copied unchanged")
	require.Contains(t, report.Summary(), "the other holds the real one")
	require.Contains(t, mismatchedColumns(report), "events.default.events.email")
}

// TestCrossStore_CatchesTwoStoresMaskedWithDifferentTransforms covers the
// subtler half: both sides are masked, so nothing leaks, and the join is still
// broken because the two outputs are different values.
func TestCrossStore_CatchesTwoStoresMaskedWithDifferentTransforms(t *testing.T) {
	t.Parallel()
	rs := oneRulesFile(t)
	all := stores(t, rs)
	all[1].Assignments = withTransform(all[1].Assignments, "email", "hash_hex", "hash_hex")

	report, err := masking.CrossStoreCheck(testKey(t), all, nil)
	require.NoError(t, err)
	require.False(t, report.OK(), report.Summary())
	require.Contains(t, report.Summary(), "different transforms")
	require.Contains(t, report.Summary(), "one identity becomes two people")
}

// TestCrossStore_CatchesTwoStoresMaskedUnderDifferentLinks is the one a reader
// would not predict: the same transform on both sides is not enough.
//
// The subkey is derived from the column identity, and the link IS the identity
// when one is set. Two columns masked by the same transform under different
// links draw from two different subkeys and produce two different fake people
// out of one real one.
func TestCrossStore_CatchesTwoStoresMaskedUnderDifferentLinks(t *testing.T) {
	t.Parallel()
	rs := oneRulesFile(t)
	all := stores(t, rs)
	all[1].Assignments = withTransform(all[1].Assignments, "email", "email", "events_email")

	report, err := masking.CrossStoreCheck(testKey(t), all, nil)
	require.NoError(t, err)
	require.False(t, report.OK(), report.Summary())
	require.Contains(t, report.Summary(), "different identities")
	require.Contains(t, report.Summary(), "give them one link")
}

// TestCrossStore_TheDivergenceARulesFileHasToSettle is the case that made the
// rules file above carry a rule for properties.
//
// jsonb in Postgres and a String holding JSON in ClickHouse are the same blob
// under two type names, and without a rule the classifier empties one as JSON
// and the other as text. Nothing leaks, and the two stores still hold different
// values for one field. Reported rather than tolerated, because the same shape
// with an identifier in it is a broken join.
func TestCrossStore_TheDivergenceARulesFileHasToSettle(t *testing.T) {
	t.Parallel()
	bare, err := masking.NewRuleSet(nil)
	require.NoError(t, err)

	report, err := masking.CrossStoreCheck(testKey(t), stores(t, bare), nil)
	require.NoError(t, err)
	require.False(t, report.OK(),
		"properties is jsonb on one side and String on the other and this was called clean:\n%s",
		report.Summary())
	require.Contains(t, mismatchedColumns(report), "events.default.events.properties")

	// And with the rule, it settles. The escape hatch is a rule that says what
	// the column is, never a way to silence the report.
	settled, err := masking.CrossStoreCheck(testKey(t), stores(t, oneRulesFile(t)), nil)
	require.NoError(t, err)
	require.True(t, settled.OK(), settled.Summary())
}

// TestCrossStore_NothingToCompareIsNotAPass is the check on the check.
//
// A report over two stores that share no identifier has proved nothing, and a
// percentage over an empty set is the shape every vacuous green check has.
func TestCrossStore_NothingToCompareIsNotAPass(t *testing.T) {
	t.Parallel()
	rs, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	left := []masking.Table{{
		Engine: "postgres", Schema: "public", Name: "a", PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "bigint"},
			{Name: "left_note", Type: "text", Nullable: true},
		},
	}}
	right := []masking.Table{{
		Engine: "clickhouse", Schema: "default", Name: "b", PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "Int64"},
			{Name: "right_note", Type: "String", Nullable: true},
		},
	}}
	report, err := masking.CrossStoreCheck(testKey(t), []masking.StoreAssignments{
		{Store: "primary", Assignments: rs.Assign(left)},
		{Store: "events", Assignments: rs.Assign(right)},
	}, nil)
	require.NoError(t, err)
	require.Zero(t, report.Checked)
	require.False(t, report.OK(), "a report that compared nothing came back as a pass")
	require.Equal(t, 0.0, report.Percent())
	require.Contains(t, report.Summary(), "nothing is proved")
}

// TestCrossStore_APairWithNoApplicableProbeIsNotVerified covers the other way a
// check like this goes quietly vacuous: every probe is refused by the
// transform, nothing is compared, and the pair passes anyway.
//
// The first version of this test was itself the failure it describes. It ran
// the ordinary fixture with one nonsense probe and asserted a property of pairs
// whose probe count was zero, and every transform in that fixture accepts any
// string, so no pair had one and the loop body never ran. Removing the guard in
// the production code left it green. It now builds a pair that CAN have nothing
// applicable, and asserts that one did before asserting anything about it.
func TestCrossStore_APairWithNoApplicableProbeIsNotVerified(t *testing.T) {
	t.Parallel()
	rs, err := masking.NewRuleSet([]masking.Rule{
		{Column: "person_id", Transform: "uuid_remap", Link: "person",
			Why: "The person a row is about."},
	})
	require.NoError(t, err)

	left := []masking.Table{{
		Engine: "postgres", Schema: "public", Name: "person", PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "bigint"},
			{Name: "person_id", Type: "uuid", Nullable: true},
		},
	}}
	right := []masking.Table{{
		Engine: "clickhouse", Schema: "default", Name: "events", PrimaryKey: []string{"uuid"},
		Columns: []masking.ColumnInfo{
			{Name: "uuid", Type: "UUID"},
			{Name: "person_id", Type: "Nullable(UUID)", Nullable: true},
		},
	}}
	all := []masking.StoreAssignments{
		{Store: "primary", Assignments: rs.Assign(left)},
		{Store: "events", Assignments: rs.Assign(right)},
	}

	// uuid_remap refuses anything that is not a UUID, and neither probe is one,
	// so both sides refuse both probes and nothing is compared.
	report, err := masking.CrossStoreCheck(testKey(t), all,
		[]string{"not a uuid", "still not a uuid"})
	require.NoError(t, err)

	require.Equal(t, 1, report.Checked, "the fixture did not produce the pair this is about")
	require.Zero(t, report.Pairs[0].Probes,
		"a probe was applicable after all, so this test would pass whatever the "+
			"production code did with a pair that has none")
	require.False(t, report.Pairs[0].Identical)
	require.Contains(t, report.Pairs[0].Reason, "not verified")
	require.False(t, report.OK())

	// And the same pair with a probe that IS a UUID is verified, which is what
	// stops the assertion above from being satisfied by a check that refuses
	// everything.
	verified, err := masking.CrossStoreCheck(testKey(t), all,
		[]string{"01890fa1-9e40-7d3c-8b9a-2f5c6d7e8a90"})
	require.NoError(t, err)
	require.True(t, verified.OK(), verified.Summary())
	require.Equal(t, 1, verified.Pairs[0].Probes)
}

// TestCrossStore_RefusesToCompareOneStore is the argument check, and it is a
// real one: a cross store report over one store would be a perfect score every
// time.
func TestCrossStore_RefusesToCompareOneStore(t *testing.T) {
	t.Parallel()
	all := stores(t, oneRulesFile(t))
	_, err := masking.CrossStoreCheck(testKey(t), all[:1], nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "compares stores")

	_, err = masking.CrossStoreCheck(nil, all, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "needs a key")
}

func withoutTransform(in []masking.Assignment, column string) []masking.Assignment {
	out := append([]masking.Assignment(nil), in...)
	for i := range out {
		if out[i].Column.Name == column {
			out[i].Transform, out[i].Link = "", ""
		}
	}
	return out
}

func withTransform(in []masking.Assignment, column, transform, link string) []masking.Assignment {
	out := append([]masking.Assignment(nil), in...)
	for i := range out {
		if out[i].Column.Name == column {
			out[i].Transform, out[i].Link = transform, link
		}
	}
	return out
}

func mismatchedColumns(r masking.CrossStoreReport) string {
	var names []string
	for _, p := range r.Mismatches() {
		names = append(names, p.A.String(), p.B.String())
	}
	return strings.Join(names, " ")
}
