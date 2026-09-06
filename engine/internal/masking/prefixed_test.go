package masking_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/engine/internal/masking"
	"github.com/antifailure/antifailure/engine/internal/verify"
)

func prefixedKey(t *testing.T) *masking.Key {
	t.Helper()
	k, err := masking.NewKeyFromBytes([]byte("a-test-master-key-for-prefixed-ids"))
	require.NoError(t, err)
	return k
}

func applyWith(t *testing.T, name string, k *masking.Key, c masking.Column, in string) string {
	t.Helper()
	tr, ok := masking.Lookup(name)
	require.True(t, ok, "no transform called %s", name)
	out, err := tr.Apply(k, c, &in)
	require.NoError(t, err)
	require.NotNil(t, out)
	return *out
}

func TestPrefixedID_KeepsThePrefixAndReplacesTheBody(t *testing.T) {
	t.Parallel()
	k := prefixedKey(t)
	col := masking.Column{Table: "billing_customers", Name: "stripe_customer_id", Link: "stripe"}

	out := applyWith(t, "prefixed_id", k, col, "cus_NffrFeUfNV2Hib")
	require.True(t, strings.HasPrefix(out, "cus_"), "the prefix is what the billing code recognises: %s", out)
	require.NotEqual(t, "cus_NffrFeUfNV2Hib", out)
	require.Regexp(t, regexp.MustCompile(`^cus_[0-9a-f]{16}$`), out,
		"a fourteen character body is padded to sixteen hex characters")

	long := applyWith(t, "prefixed_id", k, col, "sub_1MowQVLkdIwHu7ixeRlqHVzs")
	require.Regexp(t, regexp.MustCompile(`^sub_[0-9a-f]{24}$`), long,
		"a longer body keeps its own length")

	// Two segments of prefix, one kept whole: everything up to the last
	// underscore is the kind of thing this is.
	session := applyWith(t, "prefixed_id", k, col, "cs_test_a1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6")
	require.True(t, strings.HasPrefix(session, "cs_test_"), session)
}

func TestPrefixedID_TheSameCustomerMasksTheSameEverywhere(t *testing.T) {
	t.Parallel()
	// Five tables carry stripe_customer_id and one link ties them. The join
	// between them is the whole reason the transform is a hash and not a
	// nullify.
	k := prefixedKey(t)
	a := masking.Column{Table: "billing_customers", Name: "stripe_customer_id", Link: "stripe"}
	b := masking.Column{Table: "subscriptions", Name: "stripe_customer_id", Link: "stripe"}
	require.Equal(t,
		applyWith(t, "prefixed_id", k, a, "cus_NffrFeUfNV2Hib"),
		applyWith(t, "prefixed_id", k, b, "cus_NffrFeUfNV2Hib"))

	// And two different things with the same body do not share one.
	require.NotEqual(t,
		applyWith(t, "prefixed_id", k, a, "cus_NffrFeUfNV2Hib")[4:],
		applyWith(t, "prefixed_id", k, a, "sub_NffrFeUfNV2Hib")[4:])
}

func TestPrefixedID_TheMaskedValueDoesNotTripTheDetectorTheRealOneDoes(t *testing.T) {
	t.Parallel()
	// The arrangement the email transform has with its reserved domains: the
	// shape says which side of the mask a value is on, so the scan can catch
	// a real identifier and pass a masked one.
	var d verify.Detector
	for _, cand := range verify.Detectors() {
		if cand.Name == "provider-identifier" {
			d = cand
		}
	}
	require.NotNil(t, d.Match)
	k := prefixedKey(t)
	col := masking.Column{Table: "invoices", Name: "stripe_invoice_id"}
	real := "in_1MtHbELkdIwHu7ixl4OzzPMv"
	require.True(t, d.Match(real))
	require.False(t, d.Match(applyWith(t, "prefixed_id", k, col, real)))
}

func TestPrefixedID_PreservesNullEmptyAndUniqueness(t *testing.T) {
	t.Parallel()
	tr, _ := masking.Lookup("prefixed_id")
	require.True(t, tr.PreservesUniqueness(),
		"every Stripe id column carries a unique constraint, and the planner refuses a transform without this")
	out, err := tr.Apply(prefixedKey(t), masking.Column{Name: "x"}, nil)
	require.NoError(t, err)
	require.Nil(t, out)
	empty := ""
	out, err = tr.Apply(prefixedKey(t), masking.Column{Name: "x"}, &empty)
	require.NoError(t, err)
	require.Equal(t, "", *out)

	// No prefix at all is still hashed, at sixteen characters.
	require.Regexp(t, `^[0-9a-f]{16}$`, applyWith(t, "prefixed_id", prefixedKey(t), masking.Column{Name: "x"}, "bare"))
}

func TestRepository_TheOwnerMasksLikeTheUsernameItShares(t *testing.T) {
	t.Parallel()
	// The argument the transform exists for. organizations.github_login goes
	// through username under link customer; repositories.full_name goes
	// through repository under the same link, and the owner half has to come
	// out as the same synthetic handle or the matrix shows repositories that
	// belong to nobody on the page.
	k := prefixedKey(t)
	login := masking.Column{Table: "organizations", Name: "github_login", Link: "customer"}
	repo := masking.Column{Table: "repositories", Name: "full_name", Link: "customer"}

	handle := applyWith(t, "username", k, login, "contoso")
	full := applyWith(t, "repository", k, repo, "contoso/ledger")
	owner, name, found := strings.Cut(full, "/")
	require.True(t, found, full)
	require.Equal(t, handle, owner)
	require.NotEqual(t, "ledger", name)
	require.Regexp(t, `^[a-z]+-[a-z0-9]{8}$`, name, "the name half reads as a handle too")

	// Two repositories of one owner keep the owner and differ in the name.
	other := applyWith(t, "repository", k, repo, "contoso/billing")
	require.True(t, strings.HasPrefix(other, handle+"/"))
	require.NotEqual(t, full, other)

	// A value with no slash is a handle.
	require.Equal(t, handle, applyWith(t, "repository", k, repo, "contoso"))
	tr, _ := masking.Lookup("repository")
	require.True(t, tr.PreservesUniqueness())
}

func TestPlan_CopiedUnchangedIsTheHalfOfUnclassifiedThatShips(t *testing.T) {
	t.Parallel()
	// Two outcomes share the unclassified list. The count that matters is
	// the one the default could not empty, and it has to be countable
	// without reading the list.
	tables := []masking.Table{{
		Schema: "public", Name: "subscriptions", PrimaryKey: []string{"id"},
		Columns: []masking.ColumnInfo{
			{Name: "id", Type: "uuid"},
			{Name: "stripe_customer_id", Type: "text"},   // NOT NULL: copied unchanged
			{Name: "memo", Type: "text", Nullable: true}, // emptied by default
			{Name: "sealed", Type: "bytea"},              // unrecognised type: copied unchanged
			{Name: "state", Type: "USER-DEFINED"},        // enum: copied unchanged
			{Name: "email", Type: "text"},                // a default rule names it
		},
	}}
	rules, err := masking.NewRuleSet(nil)
	require.NoError(t, err)
	plan := masking.BuildPlan(tables, rules.Assign(tables), "h")

	require.Len(t, plan.Unclassified, 4)
	require.Equal(t, []string{
		"public.subscriptions.sealed", "public.subscriptions.state",
		"public.subscriptions.stripe_customer_id",
	}, plan.CopiedUnchangedNames(), "sorted the way the unclassified list is")
	require.Len(t, plan.CopiedUnchanged(), 3)
}
