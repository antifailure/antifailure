// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package secrets

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/license"
)

// The licence gate on the method that returns the value, not only on the one
// that describes the source.
//
// WHAT THIS CLOSES. Available has always refused an unlicensed source, and
// every caller in this repository consults it first: the chain skips a source
// that is not available, and so does the single-source lookup beside it. That
// makes the licence a TWO CALL contract, and a two call contract is a
// chokepoint only for as long as everybody remembers the first call. Nothing in
// the type system, in a review, or in a test would have shown a caller that
// went straight to Lookup and was handed the customer's secret with no licence.
//
// It is the same shape as the defect the whole wave is about, one level down:
// the site exists, it is declared, it refuses when asked, and there is a path
// that does not ask.

func TestLookupRefusesWithoutTheLicenceEvenWhenAvailableIsNotConsulted(t *testing.T) {
	t.Parallel()
	backend := &fake{
		describe: "A Fake Store at fake.internal",
		values:   map[string]string{"DATABASE_URL": "the-secret"},
	}
	source := New(backend)

	// Straight to Lookup. No Available call, which is exactly the caller this
	// exists for.
	ctx := withFeatures(context.Background(), license.FeatureSSO)
	value, found, err := source.Lookup(ctx, "DATABASE_URL")

	require.ErrorIs(t, err, ErrUnlicensed)
	require.False(t, found)
	require.Empty(t, value, "the secret was returned to an unlicensed caller")
	// The reason travels with it. "No value" from a lapsed licence and "no
	// value" from a store that is down are the same outcome and different
	// actions, and only one of them is fixed by paying an invoice.
	require.Contains(t, err.Error(), "enterprise_secrets")
}

func TestLookupReturnsTheValueWithTheLicence(t *testing.T) {
	t.Parallel()
	// The other half. Without it the test above is satisfied by a Lookup that
	// refuses everybody, which is a broken store rather than an enforced one.
	backend := &fake{
		describe: "A Fake Store at fake.internal",
		values:   map[string]string{"DATABASE_URL": "the-secret"},
	}
	source := New(backend)

	value, found, err := source.Lookup(
		withFeatures(context.Background(), license.FeatureSecrets), "DATABASE_URL")

	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "the-secret", value)
}

func TestLookupWithNoLicenceAtAllIsRefusedTheSameWay(t *testing.T) {
	t.Parallel()
	// A bare context, which is what code that forgot to attach a licence
	// produces. It must degrade to the community behaviour rather than to
	// granting everything, and the reason has to say a licence is missing
	// rather than that this one does not include the feature.
	backend := &fake{values: map[string]string{"DATABASE_URL": "the-secret"}}
	_, found, err := New(backend).Lookup(context.Background(), "DATABASE_URL")

	require.ErrorIs(t, err, ErrUnlicensed)
	require.False(t, found)
	require.Contains(t, err.Error(), "none is installed")
}
