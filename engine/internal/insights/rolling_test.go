package insights_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/insights"
	"github.com/antifailure/antifailure/engine/pkg/schema"
)

func stmt(sql string) insights.Statement {
	return insights.Statement{Migration: "0002_change.sql", Index: 1, SQL: sql}
}

func changes(t *testing.T, sql ...string) []insights.SchemaChange {
	t.Helper()
	var in []insights.Statement
	for _, s := range sql {
		in = append(in, stmt(s))
	}
	return insights.NarrowingChanges(in)
}

func TestNarrowing_DropColumn(t *testing.T) {
	t.Parallel()
	c := changes(t, "ALTER TABLE customers DROP COLUMN email")
	require.Len(t, c, 1)
	require.Equal(t, insights.ChangeDropColumn, c[0].Kind)
	require.Equal(t, "customers", c[0].Table)
	require.Equal(t, "email", c[0].Column)
	require.Equal(t, "customers.email", c[0].Object())
	require.Contains(t, c[0].Breaks(), "dropped customers.email")
}

func TestNarrowing_RenameColumnCarriesTheNewName(t *testing.T) {
	t.Parallel()
	// The new name is the difference between a report that says what to do
	// and one that says something is wrong. A drop has nothing to point at; a
	// rename does.
	c := changes(t, "ALTER TABLE customers RENAME COLUMN email TO email_address")
	require.Len(t, c, 1)
	require.Equal(t, insights.ChangeRenameColumn, c[0].Kind)
	require.Equal(t, "email", c[0].Column)
	require.Equal(t, "email_address", c[0].NewName)
	require.Contains(t, c[0].Breaks(), "to email_address")
}

func TestNarrowing_TableLevel(t *testing.T) {
	t.Parallel()
	for sql, want := range map[string]insights.SchemaChange{
		"DROP TABLE orders": {Kind: insights.ChangeDropTable, Table: "orders"},
		"DROP TABLE IF EXISTS public.orders": {
			Kind: insights.ChangeDropTable, Table: "orders",
		},
		"DROP VIEW order_summary": {
			Kind: insights.ChangeDropView, Table: "order_summary",
		},
		"DROP MATERIALIZED VIEW order_totals": {
			Kind: insights.ChangeDropView, Table: "order_totals",
		},
		"ALTER TABLE orders RENAME TO purchases": {
			Kind: insights.ChangeRenameTable, Table: "orders", NewName: "purchases",
		},
	} {
		c := changes(t, sql)
		require.Len(t, c, 1, sql)
		require.Equal(t, want.Kind, c[0].Kind, sql)
		require.Equal(t, want.Table, c[0].Table, sql)
		require.Equal(t, want.NewName, c[0].NewName, sql)
	}
}

func TestNarrowing_ColumnLevelActions(t *testing.T) {
	t.Parallel()
	for sql, kind := range map[string]insights.ChangeKind{
		"ALTER TABLE orders ALTER COLUMN total_cents TYPE bigint": insights.ChangeColumnType,
		"ALTER TABLE orders ALTER COLUMN phone SET NOT NULL":      insights.ChangeSetNotNull,
		"ALTER TABLE orders ALTER COLUMN placed_at DROP DEFAULT":  insights.ChangeDropDefault,
		"ALTER TABLE orders ADD COLUMN currency text NOT NULL":    insights.ChangeAddRequired,
	} {
		c := changes(t, sql)
		require.Len(t, c, 1, sql)
		require.Equal(t, kind, c[0].Kind, sql)
		require.Equal(t, "orders", c[0].Table, sql)
		require.NotEmpty(t, c[0].Column, sql)
	}
}

func TestNarrowing_AdditiveChangesAreNotReported(t *testing.T) {
	t.Parallel()
	// The whole cost argument rests on this list. Code that never heard of a
	// column cannot notice it, so a migration made only of these buys nothing
	// by building a second image and bringing up a second environment.
	additive := []string{
		"CREATE TABLE refunds (id serial PRIMARY KEY)",
		"CREATE INDEX CONCURRENTLY orders_placed_idx ON orders (placed_at)",
		"CREATE UNIQUE INDEX customers_email_idx ON customers (email)",
		"ALTER TABLE orders ADD COLUMN note text",
		"ALTER TABLE orders ADD COLUMN currency text NOT NULL DEFAULT 'usd'",
		"CREATE VIEW order_summary AS SELECT id FROM orders",
		"INSERT INTO customers (name) VALUES ('x')",
		"UPDATE orders SET total_cents = 1",
	}
	require.Empty(t, changes(t, additive...))
	require.False(t, insights.Narrowing([]insights.Statement{stmt(additive[3])}))
	require.True(t, insights.Narrowing([]insights.Statement{
		stmt("ALTER TABLE customers DROP COLUMN email"),
	}))
}

func TestNarrowing_SeveralActionsInOneStatement(t *testing.T) {
	t.Parallel()
	// One ALTER TABLE can carry several actions, and a report that shows only
	// the first hides the second. The commas inside numeric(10,2) and inside
	// the CHECK are not action separators.
	c := changes(t,
		"ALTER TABLE orders DROP COLUMN note, ALTER COLUMN total_cents TYPE numeric(10,2), "+
			"ADD CONSTRAINT orders_total_positive CHECK (total_cents > 0)")
	require.Len(t, c, 3)
	require.Equal(t, insights.ChangeDropColumn, c[0].Kind)
	require.Equal(t, "note", c[0].Column)
	require.Equal(t, insights.ChangeColumnType, c[1].Kind)
	require.Equal(t, "total_cents", c[1].Column)
	require.Equal(t, insights.ChangeAddConstraint, c[2].Kind)
	require.Equal(t, "orders_total_positive", c[2].Constraint)
}

func TestNarrowing_QuotedAndSchemaQualifiedNames(t *testing.T) {
	t.Parallel()
	c := changes(t, `ALTER TABLE public."orders" DROP COLUMN IF EXISTS "note"`)
	require.Len(t, c, 1)
	// Unqualified, unquoted and lower case, because that is the form the
	// server's error messages are compared in once they are folded the same
	// way. The schema qualifier sits outside the quotes, so it has to come off
	// first.
	require.Equal(t, "orders", c[0].Table)
	require.Equal(t, "note", c[0].Column)
}

func TestAttribute_NamesTheColumnTheMigrationDropped(t *testing.T) {
	t.Parallel()
	c := changes(t, "ALTER TABLE customers DROP COLUMN email")
	found, other := insights.Attribute([]string{
		"2026/08/30 09:12:03 query customers: ERROR: column \"email\" does not exist (SQLSTATE 42703)",
	}, c)
	require.Empty(t, other)
	require.Len(t, found, 1)
	require.Equal(t, "customers.email", found[0].Object)
	require.Equal(t,
		"9a1b2c3 still reads customers.email, which this migration dropped.",
		found[0].Sentence("9a1b2c3"))
}

func TestAttribute_RefusesToClaimWhatItCannotSupport(t *testing.T) {
	t.Parallel()
	// The error names a table this migration never touched. Matching on the
	// column alone would print a confident sentence about the wrong thing,
	// which is the failure mode that costs a check its reader.
	c := changes(t, "ALTER TABLE customers DROP COLUMN email")
	found, other := insights.Attribute([]string{
		`ERROR: column "email" of relation "suppliers" does not exist`,
	}, c)
	require.Empty(t, found)
	require.Len(t, other, 1)
}

func TestAttribute_AmbiguousColumnIsNotAttributed(t *testing.T) {
	t.Parallel()
	// Two tables lost a column of the same name and the server did not say
	// which one the statement meant. One candidate is an answer; two is a
	// guess.
	c := changes(t,
		"ALTER TABLE customers DROP COLUMN email",
		"ALTER TABLE suppliers DROP COLUMN email")
	found, other := insights.Attribute([]string{`ERROR: column "email" does not exist`}, c)
	require.Empty(t, found)
	require.Len(t, other, 1)
}

func TestAttribute_QualifiedColumnInTheMessage(t *testing.T) {
	t.Parallel()
	c := changes(t, "ALTER TABLE customers DROP COLUMN email")
	found, _ := insights.Attribute([]string{
		"ERROR: column customers.email does not exist",
	}, c)
	require.Len(t, found, 1)
	require.Equal(t, "customers.email", found[0].Object)
}

func TestAttribute_MissingRelationAndBrokenWrites(t *testing.T) {
	t.Parallel()
	c := changes(t,
		"DROP TABLE orders",
		"ALTER TABLE customers ALTER COLUMN phone SET NOT NULL",
		"ALTER TABLE invoices ADD CONSTRAINT invoices_total_positive CHECK (total > 0)")
	found, other := insights.Attribute([]string{
		`ERROR: relation "orders" does not exist`,
		`ERROR: null value in column "phone" of relation "customers" violates not-null constraint`,
		`ERROR: new row for relation "invoices" violates check constraint "invoices_total_positive"`,
	}, c)
	require.Empty(t, other)
	require.Len(t, found, 3)

	var objects []string
	for _, f := range found {
		objects = append(objects, f.Object)
	}
	require.ElementsMatch(t, []string{"orders", "customers.phone", "invoices"}, objects)
	for _, f := range found {
		if f.Object == "customers.phone" {
			require.Contains(t, f.Sentence("abc123"), "still writes customers.phone")
		}
	}
}

func TestAttribute_IgnoresOutputThatIsNotAPostgresError(t *testing.T) {
	t.Parallel()
	c := changes(t, "ALTER TABLE customers DROP COLUMN email")
	found, other := insights.Attribute([]string{
		"", "listening on :3000", "GET /customers 200 4ms",
	}, c)
	require.Empty(t, found)
	require.Empty(t, other)
}

func outcome(name, verdict string) insights.RunnerOutcome {
	return insights.RunnerOutcome{Name: name, Verdict: verdict}
}

func TestGrade_EverythingPasses(t *testing.T) {
	t.Parallel()
	r := insights.GradeRolling(
		[]insights.RunnerOutcome{outcome("a", "pass"), outcome("b", "flaky")},
		nil, nil, nil, "abc123")
	require.Equal(t, insights.RollingPass, r.Verdict)
	require.False(t, r.Failed())
	// A flaky workflow failed once and passed on retry, which is a pass
	// against the migrated schema and somebody else's problem.
	require.Equal(t, insights.RollingPass, r.Workflows[1].Verdict)
}

func TestGrade_FailureConfirmedByTheControlIsAFinding(t *testing.T) {
	t.Parallel()
	c := changes(t, "ALTER TABLE customers DROP COLUMN email")
	r := insights.GradeRolling(
		[]insights.RunnerOutcome{outcome("place-an-order", "fail")},
		map[string]insights.RunnerOutcome{"place-an-order": outcome("place-an-order", "pass")},
		c,
		[]string{`ERROR: column "email" of relation "customers" does not exist`},
		"abc123")
	require.Equal(t, insights.RollingFail, r.Verdict)
	require.True(t, r.Failed())
	require.Equal(t, "pass", r.Workflows[0].Control)
	require.NotNil(t, r.Workflows[0].Cause)
	require.Equal(t, "customers.email", r.Workflows[0].Cause.Object)
	require.Contains(t, r.Explain(), "still reads customers.email, which this migration dropped.")
}

func TestGrade_FailureTheControlAlsoFailsIsUnverified(t *testing.T) {
	t.Parallel()
	// The single most important case in this file. A workflow that fails on
	// the previous release with the migrations and without them is a workflow
	// that release does not pass, and reporting it as a migration break would
	// be a false positive on the check's first outing.
	r := insights.GradeRolling(
		[]insights.RunnerOutcome{outcome("a", "fail")},
		map[string]insights.RunnerOutcome{"a": outcome("a", "fail")},
		changes(t, "ALTER TABLE customers DROP COLUMN email"),
		[]string{`ERROR: column "email" of relation "customers" does not exist`},
		"abc123")
	require.Equal(t, insights.RollingUnverified, r.Verdict)
	require.False(t, r.Failed())
	require.Equal(t, insights.RollingUnverified, r.Workflows[0].Verdict)
	require.Contains(t, r.Workflows[0].Detail, "the schema it was deployed against either")
}

func TestGrade_FailureWithNoControlIsUnverified(t *testing.T) {
	t.Parallel()
	r := insights.GradeRolling(
		[]insights.RunnerOutcome{outcome("a", "fail")}, nil, nil, nil, "abc123")
	require.Equal(t, insights.RollingUnverified, r.Verdict)
	require.Contains(t, r.Workflows[0].Detail, "was not re-run against the schema")
}

func TestGrade_BlockedWorkflowsNeverCountAgainstTheChange(t *testing.T) {
	t.Parallel()
	r := insights.GradeRolling(
		[]insights.RunnerOutcome{outcome("a", "blocked"), outcome("b", "blocked")},
		nil, nil, nil, "abc123")
	require.Equal(t, insights.RollingBlocked, r.Verdict)
	require.False(t, r.Failed())
	require.Contains(t, r.Explain(), "does not count against it")
}

func TestGrade_OnePassAndOneBlockedStillPasses(t *testing.T) {
	t.Parallel()
	// Amber whenever one workflow could not be exercised would be amber most
	// days, and a check that is amber most days is one nobody reads. The
	// blocked one is named in the report instead.
	r := insights.GradeRolling(
		[]insights.RunnerOutcome{outcome("a", "pass"), outcome("b", "blocked")},
		nil, nil, nil, "abc123")
	require.Equal(t, insights.RollingPass, r.Verdict)
	require.Contains(t, r.Explain(), "blocked     b")
}

func TestGrade_NoWorkflowsIsBlocked(t *testing.T) {
	t.Parallel()
	r := insights.GradeRolling(nil, nil, nil, nil, "abc123")
	require.Equal(t, insights.RollingBlocked, r.Verdict)
	require.Contains(t, r.Reason, "nothing was proved")
}

func TestGrade_FailureWithNoIdentifiedCauseSaysSo(t *testing.T) {
	t.Parallel()
	// The previous release swallowed the error, so there is no evidence for a
	// sentence naming a column. The finding still stands, because the control
	// is the evidence for the finding; only the cause is missing.
	r := insights.GradeRolling(
		[]insights.RunnerOutcome{outcome("a", "fail")},
		map[string]insights.RunnerOutcome{"a": outcome("a", "pass")},
		changes(t, "ALTER TABLE customers DROP COLUMN email"),
		[]string{"GET /customers 500"},
		"abc123")
	require.Equal(t, insights.RollingFail, r.Verdict)
	require.Nil(t, r.Workflows[0].Cause)
	text := r.Explain()
	require.Contains(t, text, "has\n              not been identified here")
	// The shortlist somebody would build by hand is offered instead, and it is
	// offered as a list of what the migration takes away rather than as a
	// claim about the failure.
	require.Contains(t, text, "What this migration takes away")
	require.Contains(t, text, "dropped customers.email")
}

func TestGrade_UnattributedErrorsAreShownWithoutAClaim(t *testing.T) {
	t.Parallel()
	r := insights.GradeRolling(
		[]insights.RunnerOutcome{outcome("a", "pass")}, nil,
		changes(t, "ALTER TABLE customers DROP COLUMN email"),
		[]string{`ERROR: relation "sessions" does not exist`},
		"abc123")
	require.Equal(t, insights.RollingPass, r.Verdict)
	require.Len(t, r.Unattributed, 1)
	require.Contains(t, r.Explain(), "not attributed to it")
}

func TestNeedsControl_OnlyFailures(t *testing.T) {
	t.Parallel()
	// The cost argument again: confirming a pass would double the price of the
	// common case to learn nothing.
	require.Equal(t, []string{"b"}, insights.NeedsControl([]insights.RunnerOutcome{
		outcome("a", "pass"), outcome("b", "fail"),
		outcome("c", "blocked"), outcome("d", "flaky"),
	}))
	require.Empty(t, insights.NeedsControl([]insights.RunnerOutcome{outcome("a", "pass")}))
}

func TestConfigureRolling_Defaults(t *testing.T) {
	t.Parallel()
	c := insights.ConfigureRolling(nil)
	require.Equal(t, insights.RollingRisky, c.When)
	require.Equal(t, insights.DefaultRollingAgainst, c.Against)

	// Reached through Configure as well, because that is the one place the
	// engine asks whether a check is on.
	require.Equal(t, c, insights.Configure(nil).Rolling)
	require.Equal(t, c, insights.Configure(&schema.Insights{}).Rolling)
}

func TestConfigureRolling_When(t *testing.T) {
	t.Parallel()
	risky := insights.ConfigureRolling(&schema.RollingCompatibility{When: "risky"})
	require.True(t, risky.On(true))
	require.False(t, risky.On(false))

	always := insights.ConfigureRolling(&schema.RollingCompatibility{When: "always"})
	require.True(t, always.On(true))
	require.True(t, always.On(false))

	never := insights.ConfigureRolling(&schema.RollingCompatibility{When: "never"})
	require.False(t, never.On(true))
	require.False(t, never.On(false))
}

func TestConfigureRolling_Against(t *testing.T) {
	t.Parallel()
	c := insights.Configure(&schema.Insights{
		RollingCompatibility: &schema.RollingCompatibility{Against: "v2.4.0"},
	})
	require.Equal(t, "v2.4.0", c.Rolling.Against)
	require.Equal(t, insights.RollingRisky, c.Rolling.When)
}

func TestRolling_ExplainNamesAnOffCheckRatherThanOmittingIt(t *testing.T) {
	t.Parallel()
	r := &insights.Rolling{
		Verdict: insights.RollingOff,
		Reason:  "insights.rolling_compatibility.when is never",
	}
	require.Contains(t, r.Explain(), "not run: insights.rolling_compatibility.when is never")

	// A nil check renders nothing at all, so a report from a build that never
	// ran it is unchanged.
	var absent *insights.Rolling
	require.Empty(t, absent.Explain())
	require.False(t, absent.Failed())
}

func TestFull_CleanIsFalseWhenThePreviousReleaseBreaks(t *testing.T) {
	t.Parallel()
	// Wiring rather than logic, and it is the wiring that decides whether the
	// pull request comment says anything at all.
	full := insights.Full{Rolling: &insights.Rolling{Verdict: insights.RollingFail}}
	require.False(t, full.Clean())

	full.Rolling = &insights.Rolling{Verdict: insights.RollingBlocked}
	require.True(t, full.Clean())

	require.Contains(t, insights.Full{
		Rolling: &insights.Rolling{
			Verdict: insights.RollingOff, Reason: "nothing narrowing",
		},
	}.Explain(), "Rolling deploy compatibility")
}

func TestRolling_ExplainIsReadableEndToEnd(t *testing.T) {
	t.Parallel()
	c := changes(t, "ALTER TABLE customers DROP COLUMN email")
	r := insights.GradeRolling(
		[]insights.RunnerOutcome{outcome("place-an-order", "fail")},
		map[string]insights.RunnerOutcome{"place-an-order": outcome("place-an-order", "pass")},
		c,
		[]string{`ERROR: column "email" of relation "customers" does not exist`},
		"6d4f0a1")
	r.Against, r.How = "6d4f0a1b2c3d4e5f", "the merge base with origin/main"

	text := r.Explain()
	for _, want := range []string{
		"the previous release is 6d4f0a1b2c3d, the merge base with origin/main",
		"FAIL        place-an-order",
		"still reads customers.email, which this migration dropped.",
		"ALTER TABLE customers DROP COLUMN email",
		"A rolling deploy runs both releases at once",
	} {
		require.Contains(t, text, want)
	}
	require.False(t, strings.Contains(text, "not attributed"))
}
