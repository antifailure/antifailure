// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package feature_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
)

func active(features ...license.Feature) license.Status {
	v := license.NewVerifier(nil)
	return v.Evaluate(license.Claims{
		ID: "l", Org: "acme", Features: features,
		ExpiresAt: time.Now().AddDate(1, 0, 0),
	}, license.Evaluation{Org: "acme", Now: time.Now()})
}

func TestAContextWithNoLicenseGrantsNothing(t *testing.T) {
	t.Parallel()
	// The direction the mistake has to fail in. Code that forgets to attach a
	// license degrades to the community behaviour rather than granting
	// everything, and a bare context.Background() is exactly that case.
	ctx := context.Background()
	for _, f := range license.AllFeatures() {
		require.Falsef(t, feature.Enabled(ctx, f), "a bare context granted %s", f)
	}
	require.Equal(t, license.StateNone, feature.StatusFrom(ctx).State)
}

func TestAContextCarriesExactlyWhatTheLicenseNames(t *testing.T) {
	t.Parallel()
	ctx := feature.With(context.Background(), active(license.FeatureSSO, license.FeatureSCIM))

	require.True(t, feature.Enabled(ctx, license.FeatureSSO))
	require.True(t, feature.Enabled(ctx, license.FeatureSCIM))
	require.False(t, feature.Enabled(ctx, license.FeatureBilling))
}

func TestAValueOfTheWrongTypeInTheContextGrantsNothing(t *testing.T) {
	t.Parallel()
	// Not paranoia: context keys are unexported here, but a future refactor
	// that changes the stored type must fail closed rather than panic or grant.
	ctx := context.WithValue(context.Background(), struct{}{}, "not a status")
	require.False(t, feature.Enabled(ctx, license.FeatureSSO))
}

func TestTheRegistryRecordsWhatWasDeclaredAndNothingElse(t *testing.T) {
	t.Parallel()
	// A UNIT TEST OF THE REGISTRY, and the rename says so because the old name
	// did not. This was TestDeclaredSitesAreRecordedForTheDeadCodeCheck, and it
	// was not the dead code check and could not have been. It declared a site
	// naming ee/web/auth.Handler, a package that has never existed, and then
	// asserted Sites(FeatureCompliance) was empty. That assertion passed because
	// this binary links neither the compliance package nor any other enforcing
	// one, so nothing could have filled the registry, so it could not fail for
	// any reason to do with the product. It is the shape of check this whole
	// lane exists to find, and it was inside the package doing the finding.
	//
	// The dead code check is real and it is in ee/engine/cmd/af, because that
	// is the only package where main.go's imports have run every init and the
	// registry is populated at all. It cannot be written here: this package is
	// imported BY compliance, secrets and policyenforce, so importing them back
	// is a cycle.
	//
	// A name no licence carries, deliberately. Declaring a real feature here
	// would put a site in the global registry for a feature this build does not
	// enforce, which is the false signal the registry exists to make visible.
	const fabricated = license.Feature("a_name_no_licence_carries")

	require.Empty(t, feature.Sites(fabricated),
		"the registry answered for a feature nothing has declared")

	feature.Declare(fabricated, "feature/feature_test.go:TestTheRegistryRecords")
	require.Contains(t, feature.Sites(fabricated),
		"feature/feature_test.go:TestTheRegistryRecords")
	require.Contains(t, feature.Declared(), fabricated)

	// Sites answers about the feature it was asked about. Without this, a Sites
	// that returned every recorded site regardless of key would satisfy every
	// assertion above, and every reconciliation built on it would be comparing
	// one list against itself.
	require.NotContains(t, feature.Sites(license.FeatureBilling),
		"feature/feature_test.go:TestTheRegistryRecords",
		"a site declared for one feature was returned for another")
}

func TestAnExpiredLicenseInAContextGrantsNothing(t *testing.T) {
	t.Parallel()
	v := license.NewVerifier(nil)
	expired := v.Evaluate(license.Claims{
		ID: "l", Org: "acme", Features: license.AllFeatures(),
		ExpiresAt: time.Now().AddDate(-2, 0, 0),
	}, license.Evaluation{Org: "acme", Now: time.Now()})

	ctx := feature.With(context.Background(), expired)
	for _, f := range license.AllFeatures() {
		require.Falsef(t, feature.Enabled(ctx, f), "an expired license granted %s", f)
	}
}
