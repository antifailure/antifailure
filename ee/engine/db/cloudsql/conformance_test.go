// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql_test

// The shared database conformance suite, run against the Cloud SQL provider.
//
// Every line of the provider runs. What is replaced is the thing on the other
// end of the socket: the Cloud SQL Admin API is fakecloudsql and the Postgres
// behind it is real, so the behaviours that are claims about bytes are checked
// against bytes rather than against a fake's opinion of them.
//
// WHAT THIS IS EVIDENCE OF, stated here rather than left to be inferred.
//
// It is evidence that the provider's logic is right: that a clone is requested
// in the only shape Cloud SQL can serve fast and never any other way, that a
// golden is masked before it is verified and published only if verification
// passed, that a branch holds the golden's rows and is isolated from the golden
// and from other branches, that branching an unverified or missing golden is
// refused with the code the engine knows, that the declared limit is enforced
// rather than hung on, that destroy removes and destroying twice succeeds, that
// health reports a removed branch as gone rather than erroring, and that the
// provider leaks nothing across a whole run.
//
// It is NOT evidence that Google accepts these requests. Nothing here has an
// account and nothing here should: section 10 of the plan says no test may need
// one. What stands between this suite and a real Cloud SQL is that the request
// shapes are what the Admin API documents, and that is not the same as Google
// having answered. The provider's own package comment carries that sentence
// too, because a reader who finds it only in a test file has already been
// misled.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/antifailure/antifailure/ee/engine/db/cloudsql"
	"github.com/antifailure/antifailure/engine/conformance"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

func TestConformance(t *testing.T) {
	server := newFake(t, conformance.DefaultSeedSQL)

	conformance.RunDatabase(t, func(t *testing.T) provider.Database {
		p, err := cloudsql.New(context.Background(), options(t, server))
		require.NoError(t, err)
		return p
	}, conformanceOptions())
}

// conformanceOptions is what this suite runs with, in a function rather than
// inline so that the assertion in verdict_test.go can read the field that
// decides the service owned verdicts.
func conformanceOptions() conformance.Options {
	return conformance.Options{
		// Generous but bounded. Every behaviour here is a clone, which against
		// this fake is a server side file copy on a Postgres other suites are
		// using at the same time, and a hung call must fail the behaviour
		// rather than the job.
		Timeout:  4 * time.Minute,
		SkipSlow: os.Getenv("AF_SKIP_SLOW") != "",
		// RealService is deliberately NOT set, and its absence is what makes
		// CopyOnWrite_BranchTimeMatchesTheDeclaration report unproven rather
		// than a measured verdict. See verdict_test.go, which carries the
		// evidence that setting it here would be false.
	}
}
