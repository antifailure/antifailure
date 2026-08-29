package load_test

import (
	"testing"

	"go.uber.org/goleak"
)

// G3 asks for goleak in every package that starts goroutines, and this package
// starts one per virtual user. A leak here is not cosmetic: `af load` is the
// command that deliberately runs thousands of concurrent requests, so a
// goroutine that outlives its run is multiplied by the thing this package
// exists to do.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
