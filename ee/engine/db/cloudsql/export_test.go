// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.

package cloudsql

// The one door the external test package needs, and no more.
//
// fastCloneRequest is unexported because nothing outside this package may build
// a clone request. But the test that asserts on its MARSHALLED bytes has to be
// able to produce them, and it lives in cloudsql_test so that it exercises the
// package the way a caller does. This is the standard Go answer to that: a
// single export in a _test.go file, which ships in no binary.
//
// It deliberately exposes the BUILDER rather than the struct, so the test
// cannot construct a request the provider would never send and then assert
// happily about it.

// CloneRequestForTest returns the clone request this provider sends, for the
// test that asserts on its marshalled form.
func CloneRequestForTest(destination string) any { return newFastCloneRequest(destination) }
