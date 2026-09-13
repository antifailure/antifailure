//go:build darwin

package secrets

// KeychainValueLimit is valueLimit, for the tests in secrets_test that prove
// the number against the real security command.
func KeychainValueLimit(service, name string) int { return valueLimit(service, name) }
