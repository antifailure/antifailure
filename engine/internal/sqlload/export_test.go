package sqlload

// The internals a test in the external test package needs, and nothing more.
//
// The alternative was exporting them for real, which puts a function on the
// package's surface that only a test calls. This repository keeps finding that
// shape and calling it dead, so the two are separated here: production code
// sees an unexported name and the test file beside it sees a handle.

// ValidateForTest runs the mix check a run does before opening a connection.
func (m *Mix) ValidateForTest() error { return m.validate() }

// GeneratedTypesForTest is every Postgres type a derived parameter can be
// filled with, sorted. The documentation publishes the same list, and the test
// that uses this reads the published page rather than a copy of it.
func GeneratedTypesForTest() []string {
	out := make([]string, 0, len(generators))
	for k := range generators {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}
