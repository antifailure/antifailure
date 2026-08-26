//go:build !darwin

package secrets

// No system keyring on this platform yet.
//
// Returning nil rather than an implementation that always fails is deliberate:
// KeyringSource reports "not configured" for a nil ring, the chain skips it,
// and the message a user sees names the sources that were actually considered.
// A stub that errored on every lookup would appear in that list as a source
// that is present and broken, which is a worse thing to read.
//
// Linux wants Secret Service over D-Bus, or secret-tool where it exists.
// Windows wants the Credential Manager. Both are worth having and neither is
// worth guessing at: this returns nil until one is written and tested on the
// platform it is for.
func newSystemKeyring() Keyring { return nil }
