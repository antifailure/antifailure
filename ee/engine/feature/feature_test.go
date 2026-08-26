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

func TestDeclaredSitesAreRecordedForTheDeadCodeCheck(t *testing.T) {
	t.Parallel()
	// A feature a license can grant and nothing checks is a feature that is
	// silently free. The registry is what makes that visible, so it has to
	// actually record.
	feature.Declare(license.FeatureSSO, "ee/web/auth.Handler")
	require.Contains(t, feature.Sites(license.FeatureSSO), "ee/web/auth.Handler")
	require.Contains(t, feature.Declared(), license.FeatureSSO)
	require.Empty(t, feature.Sites(license.FeatureCompliance))
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
