package conformance_test

import (
	"testing"

	"go.uber.org/goleak"
)

// G3 asks for goleak in every package that starts goroutines. This suite runs
// every provider through the same behaviours, so a goroutine a provider forgets
// to stop is caught here once rather than separately in each provider's own
// package, and a new provider inherits the check by joining the suite.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
