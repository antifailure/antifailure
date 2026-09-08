// Package emulatorcheck drives a cloud vendor's own SDK against the emulator
// an environment would answer with, and it does it with NO ENDPOINT OVERRIDE
// in the code making the calls.
//
// That is the whole claim of the emulator work and it is the only thing worth
// measuring about it. Using LocalStack normally means changing the
// application: an endpoint override, AWS_ENDPOINT_URL, a client constructed
// one way in tests and another way in production. The code under test is then
// not the code that ships, and every result it produces is about a program
// nobody runs. What this repository adds is that the unmodified production
// code path reaches the emulator, so this package's tests are written the way
// an application is written, and the number they publish is a count of the
// lines that had to change to reach the emulator, which is zero.
//
// It lives in the tools module rather than the engine's because the AWS SDK
// is a large dependency and the engine's module graph is what the shipped
// binary is audited from. Nothing here is linked into af.
package emulatorcheck
