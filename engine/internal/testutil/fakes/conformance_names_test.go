package fakes_test

import "github.com/antifailure/antifailure/engine/conformance"

// conformanceBehaviorNames is a thin wrapper so the test above reads the real
// behaviour table rather than a copy of it. A copy would drift, and a drifted
// copy would agree with itself forever.
func conformanceBehaviorNames() []string {
	var out []string
	for _, b := range conformance.Behaviors() {
		out = append(out, b.Name)
	}
	return out
}
