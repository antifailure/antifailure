package subset_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/subset"
)

// A schema with the shapes that make subsetting hard: a chain upward, a
// dependent branch, a reference table nothing points from, a table nothing
// connects at all, and a cycle.
func schema() []masking.Table {
	fk := func(name, target string) masking.ColumnInfo {
		return masking.ColumnInfo{Name: name, Type: "bigint", ForeignKey: target}
	}
	id := masking.ColumnInfo{Name: "id", Type: "bigint"}
	return []masking.Table{
		{Schema: "public", Name: "countries", Columns: []masking.ColumnInfo{id}},
		{Schema: "public", Name: "organisations", Columns: []masking.ColumnInfo{
			id, fk("country_id", "public.countries.id"),
		}},
		{Schema: "public", Name: "customers", Columns: []masking.ColumnInfo{
			id, fk("organisation_id", "public.organisations.id"),
		}},
		{Schema: "public", Name: "orders", Columns: []masking.ColumnInfo{
			id, fk("customer_id", "public.customers.id"),
		}},
		{Schema: "public", Name: "order_items", Columns: []masking.ColumnInfo{
			id, fk("order_id", "public.orders.id"),
		}},
		{Schema: "public", Name: "feature_flags", Columns: []masking.ColumnInfo{id}},
		{Schema: "public", Name: "events", Columns: []masking.ColumnInfo{
			id, {Name: "customer_id", Type: "bigint"}, // no declared key
		}},
	}
}

func TestBuild_TakesWhatTheSeedNeedsUpward(t *testing.T) {
	t.Parallel()
	// Mandatory and not optional: an order whose customer is missing is a row
	// that violates its own constraint and a database that will not load.
	plan, err := subset.Build(schema(), subset.Config{
		SeedTable: "customers", SeedWhere: "created_at > now() - interval '30 days'",
	})
	require.NoError(t, err)

	var order []string
	for _, s := range plan.Steps {
		order = append(order, s.Table)
	}
	require.Equal(t, []string{"countries", "organisations", "customers"}, order,
		"a table is copied after everything it references")
}

func TestBuild_DoesNotFollowDependentsByDefault(t *testing.T) {
	t.Parallel()
	// One level from a customer is every order they ever placed, which is most
	// of the database again.
	plan, err := subset.Build(schema(), subset.Config{SeedTable: "customers"})
	require.NoError(t, err)
	for _, s := range plan.Steps {
		require.NotEqual(t, "orders", s.Table)
	}
	require.Contains(t, plan.Unreachable, "public.orders")
}

func TestBuild_FollowsDependentsWhenAsked(t *testing.T) {
	t.Parallel()
	plan, err := subset.Build(schema(), subset.Config{
		SeedTable: "customers", FollowDependents: 2,
	})
	require.NoError(t, err)

	var order []string
	for _, s := range plan.Steps {
		order = append(order, s.Table)
	}
	require.Contains(t, order, "orders")
	require.Contains(t, order, "order_items")
	require.Less(t, indexOf(order, "orders"), indexOf(order, "order_items"),
		"order_items references orders, so orders is copied first")
}

func TestBuild_NarrowsWithEveryReferenceItHas(t *testing.T) {
	t.Parallel()
	// AND rather than OR. A row taken because one reference is present and
	// whose second is missing is a row that cannot be loaded.
	plan, err := subset.Build([]masking.Table{
		{Schema: "public", Name: "a", Columns: []masking.ColumnInfo{{Name: "id"}}},
		{Schema: "public", Name: "b", Columns: []masking.ColumnInfo{{Name: "id"}}},
		{Schema: "public", Name: "joined", Columns: []masking.ColumnInfo{
			{Name: "a_id", ForeignKey: "public.a.id"},
			{Name: "b_id", ForeignKey: "public.b.id"},
		}},
	}, subset.Config{SeedTable: "joined"})
	require.NoError(t, err)

	for _, s := range plan.Steps {
		if s.Table == "joined" {
			continue
		}
		require.True(t, s.Full, "%s is a reference table with nothing to narrow it", s.Table)
	}
}

func TestBuild_ReportsTablesNothingConnects(t *testing.T) {
	t.Parallel()
	// A table that arrives empty and should not have is a bug somebody finds
	// three days later in a test that returns nothing.
	plan, err := subset.Build(schema(), subset.Config{SeedTable: "customers"})
	require.NoError(t, err)
	require.Contains(t, plan.Unreachable, "public.events",
		"events has a customer_id with no declared key, so nothing reaches it")
	require.Contains(t, plan.Unreachable, "public.feature_flags")
	require.Contains(t, plan.Explain(), "virtual_relationships",
		"the explanation says what to do about it")
}

func TestBuild_FollowsADeclaredRelationshipTheSchemaDoesNotHave(t *testing.T) {
	t.Parallel()
	// A relationship expressed in application code is invisible to the schema,
	// and a subset that ignored it would drop every event.
	plan, err := subset.Build(schema(), subset.Config{
		SeedTable: "customers", FollowDependents: 1,
		Virtual: []subset.Relationship{{
			FromTable: "events", FromColumn: "customer_id",
			ToTable: "customers", ToColumn: "id",
		}},
	})
	require.NoError(t, err)

	var found bool
	for _, s := range plan.Steps {
		if s.Table == "events" {
			found = true
			require.Contains(t, s.Where, `"customer_id" IN (SELECT "id" FROM "customers")`)
		}
	}
	require.True(t, found)
	require.NotContains(t, plan.Unreachable, "public.events")

	// Reported separately, because a wrong one produces a broken subset and
	// the schema cannot catch it.
	var virtual int
	for _, r := range plan.Relationships {
		if r.Virtual {
			virtual++
			require.Contains(t, r.String(), "~>")
		}
	}
	require.Equal(t, 1, virtual)
}

func TestBuild_BreaksACycleAndSaysWhere(t *testing.T) {
	t.Parallel()
	// A schema with a foreign key loop cannot be loaded in any order, so one
	// edge is deferred. Saying which is the difference between a subset
	// somebody trusts and one they do not.
	plan, err := subset.Build([]masking.Table{
		{Schema: "public", Name: "users", Columns: []masking.ColumnInfo{
			{Name: "id"}, {Name: "team_id", ForeignKey: "public.teams.id"},
		}},
		{Schema: "public", Name: "teams", Columns: []masking.ColumnInfo{
			{Name: "id"}, {Name: "owner_id", ForeignKey: "public.users.id"},
		}},
	}, subset.Config{SeedTable: "users"})
	require.NoError(t, err)
	require.NotEmpty(t, plan.Cycles)
	require.Contains(t, plan.Explain(), "cycles")
	require.Len(t, plan.Steps, 2, "both tables are still copied")
}

func TestBuild_RefusesASeedThatIsNotThereAndSaysWhatIs(t *testing.T) {
	t.Parallel()
	_, err := subset.Build(schema(), subset.Config{SeedTable: "custmoers"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "customers", "a list of what exists turns a typo into a fix")

	_, err = subset.Build(schema(), subset.Config{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nothing to start from")
}

func TestStep_SQLLimitsWhatIsNarrowedAndNotWhatIsWhole(t *testing.T) {
	t.Parallel()
	// Cutting off a small reference table would leave dangling references in
	// everything that points at it.
	narrowed := subset.Step{Table: "orders", Where: `"customer_id" IN (SELECT "id" FROM "customers")`}
	require.Contains(t, narrowed.SQL(1000), "LIMIT 1000")

	whole := subset.Step{Table: "countries", Full: true}
	require.NotContains(t, whole.SQL(1000), "LIMIT")
	require.Equal(t, `SELECT * FROM "countries"`, whole.SQL(1000))
}

func TestBuild_IsDeterministic(t *testing.T) {
	t.Parallel()
	// Two plans for one schema must be identical, or a diff between them is a
	// diff of map iteration order rather than of anything real.
	cfg := subset.Config{SeedTable: "customers", FollowDependents: 2}
	first, err := subset.Build(schema(), cfg)
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		again, err := subset.Build(schema(), cfg)
		require.NoError(t, err)
		require.Equal(t, first.Explain(), again.Explain())
	}
}

func indexOf(items []string, want string) int {
	for i, s := range items {
		if s == want {
			return i
		}
	}
	return -1
}

var _ = strings.TrimSpace
