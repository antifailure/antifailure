package env

// A PRESERVE RULE DOES NOT SATISFY A POLICY THAT REQUIRES A COLUMN MASKED.
//
// The masking request a hook reads names the columns the plan will rewrite. A
// preserved column used to be among them, because the plan put it in the column
// list a statement writes, so a hook requiring that column masked approved a
// plan that left it holding exactly what production held. The enterprise
// required_masked_columns policy decides on exactly that list. A preserved column
// is not rewritten and is no longer named, so the same plan is refused, by the
// hook, naming the column, before a row is touched.

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/pkg/extension"
)

// requireMasked refuses a plan the way the enterprise required_masked_columns
// policy does: a column the catalogue holds that the request does not name as
// masked is refused, by name.
type requireMasked struct{ column string }

func (r requireMasked) Name() string { return "required masking" }

func (r requireMasked) CheckMasking(_ context.Context, req extension.MaskingRequest) error {
	if slices.Contains(req.MaskedColumns, r.column) {
		return nil
	}
	return fmt.Errorf("required masking refused this plan: this organization requires %s to be masked "+
		"and no rule in this repository covers it", r.column)
}

func TestMaskDatabase_APreserveRuleDoesNotSatisfyAHookThatRequiresTheColumnMasked(t *testing.T) {
	url := maskEventsDatabase(t, maskEventsSchema)
	o := maskEventsOrchestrator(t)
	registerMasking(o, requireMasked{column: "public.customers.email"})

	rules, err := masking.NewRuleSet([]masking.Rule{
		{Table: "customers", Column: "email", Transform: "preserve", Why: "reviewed and found safe"},
	})
	require.NoError(t, err)

	_, _, err = o.maskDatabase(t.Context(), maskPolicySession(t, o), url, maskEventsKey(t), rules, "h1")
	require.Error(t, err,
		"a hook requiring public.customers.email masked approved a plan whose only rule for it is preserve")
	require.ErrorContains(t, err, "public.customers.email", "the refusal does not name the column")
	require.ErrorContains(t, err, "required masking", "the refusal does not name the policy")

	conn, connErr := pgx.Connect(t.Context(), url.Reveal())
	require.NoError(t, connErr)
	defer func() { _ = conn.Close(context.Background()) }()
	var email string
	require.NoError(t, conn.QueryRow(t.Context(),
		"SELECT email FROM customers ORDER BY id LIMIT 1").Scan(&email))
	require.Equal(t, "ada@example.com", email, "the refused plan rewrote rows anyway")
}
