//go:build !darwin && !linux && !windows

package secrets

// No system keyring on this platform yet.
//
// Returning nil rather than an implementation that always fails is deliberate:
// KeyringSource reports "not configured" for a nil ring, the chain skips it,
// and the message a user sees names the sources that were actually considered.
// A stub that errored on every lookup would appear in that list as a source
// that is present and broken, which is a worse thing to read.
//
// macOS reaches the keychain through the security command, Linux reaches the
// freedesktop Secret Service through secret-tool, and Windows calls advapi32
// directly because it has no command that will print a password back. What is
// left is the platforms with no credential store to reach: the BSDs, Plan 9,
// WebAssembly, and whatever a cross compilation is aimed at next. Those get
// nil, and the chain says so rather than pretending a source was tried.
func newSystemKeyring() Keyring { return nil }
