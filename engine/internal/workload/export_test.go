package workload

// Exported for the tests, which live in package workload_test.

// NamedForTest exposes the join that never renders a dangling colon.
//
// Exported through a test file rather than made public: the join is an internal
// rendering decision and nothing outside this package should call it, but the
// guard it carries is worth a test of its own because the case it defends
// against is unreachable through the ordinary paths today.
func NamedForTest(name, detail string) string { return named(name, detail) }
