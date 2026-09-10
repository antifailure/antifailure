// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package azurepg_test

// The shared database conformance suite, run against the Azure PostgreSQL
// provider.
//
// Every line of the provider runs. What is replaced is the thing on the other
// end of the socket: Resource Manager is fakeazurepg and the Postgres behind it
// is real, so the behaviours that are claims about bytes are checked against
// bytes rather than against a fake's opinion of them.
//
// WHAT THIS IS EVIDENCE OF: that the provider's logic is right. That a golden
// is masked before it is verified and published only if verification passed,
// that a branch holds the golden's rows and is isolated from it and from other
// branches, that the post restore work Azure does not do for you is done here,
// that branching an unverified or missing golden is refused with the code the
// engine knows, that the declared limit is enforced rather than hung on, that
// destroy removes and destroying twice succeeds, that health reports a removed
// branch as gone rather than erroring, and that the provider leaks nothing.
//
// It is NOT evidence that Azure accepts these requests. Nothing here has a
// subscription and nothing here should: section 10 of the plan says no test may
// need one. What stands between this suite and a real flexible server is that
// the request shapes are what Resource Manager documents, and that is not the
// same as Azure having answered.

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/azurepg"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestConformance(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL)

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := azurepg.New(options(t, server))
		require.NoError(t, err)
		return p
	}, conformanceOptions())
}

// conformanceOptions is what this suite runs with, in a function rather than
// inline so that the assertion in verdict_test.go can read the field that
// decides the service owned verdicts.
func conformanceOptions() conformance.Options {
	return conformance.Options{
		// Generous but bounded. Every behaviour here is a restore, which
		// against this fake is a server side file copy on a Postgres other
		// suites are using at the same time, and a hung call must fail the
		// behaviour rather than the job.
		Timeout:  4 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
		// RealService is deliberately NOT set. See verdict_test.go.
	}
}
