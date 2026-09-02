// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package compliance

import (
	"context"
	"time"

	"github.com/antifailure/antifailure/ee/engine/feature"
	"github.com/antifailure/antifailure/ee/engine/license"
)

// withFeatures attaches a licence granting exactly these features.
//
// Through the verifier rather than by building a Status by hand, so the tests
// exercise the same evaluation path the product does rather than a state that
// could not occur.
func withFeatures(ctx context.Context, features ...license.Feature) context.Context {
	v := license.NewVerifier(nil)
	return feature.With(ctx, v.Evaluate(license.Claims{
		ID: "test", Org: "test", Plan: "enterprise", Features: features,
		IssuedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(365 * 24 * time.Hour),
	}, license.Evaluation{Org: "test", Now: time.Now()}))
}
