package env

import (
	"context"

	"github.com/antifailure/antifailure/engine/internal/events"
	"github.com/antifailure/antifailure/engine/internal/secrets"
	"github.com/antifailure/antifailure/engine/pkg/provider"
)

// ServiceFieldForTest exposes serviceField to the package's external tests.
//
// The function decides which service a runtime progress line belongs to, and
// getting it wrong puts a row in the dashboard named after a word that is not
// a service. That is worth a test, and the function has no reason to be part
// of the package's real surface.
func ServiceFieldForTest(line string, names map[string]bool) []events.Field {
	return serviceField(line, names)
}

// NonEmptyForTest exposes nonEmpty to the package's external tests.
func NonEmptyForTest(kv ...string) []events.Field { return nonEmpty(kv...) }

// PickGoldenForTest exposes pickGolden to the package's external tests.
//
// Which golden a branch is made from is the decision this test suite most
// needs to be able to reach directly. Reaching it through Up would need a
// provider, a runtime, a journal and a lock, and the property being asserted
// is a property of one loop.
func PickGoldenForTest(goldens []provider.GoldenVersion, wantRules string) (string, int) {
	return pickGolden(goldens, wantRules)
}

// RunSeedForTest exposes runSeed to the package's external tests.
//
// The seed runs inside a provider callback during a refresh, so reaching it
// through Up would need a daemon, a golden, and five minutes to assert that a
// shell command ran.
func RunSeedForTest(ctx context.Context, o *Orchestrator, seed, candidateURL string) error {
	return o.runSeed(ctx, nil, seed, secrets.New(candidateURL))
}

// SeedRulesHashForTest exposes seedRulesHash.
func SeedRulesHashForTest(seed string) string { return seedRulesHash(seed) }
