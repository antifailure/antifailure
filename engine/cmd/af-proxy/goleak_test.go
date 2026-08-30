package main

import (
	"testing"

	"go.uber.org/goleak"
)

// G3 asks for goleak in every package that starts goroutines. This is the
// sidecar: it runs for the whole life of an environment, intercepting DNS and
// terminating TLS, so a goroutine leaked per connection is a leak per request
// the application makes, in the one process with no natural end.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
