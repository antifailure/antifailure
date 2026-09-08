// Package cloudauth holds the three ways this product proves who it is to a
// cloud, with no vendor SDK anywhere in the credential path.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// It was extracted from ee/engine/secrets, where AWS Signature Version 4, the
// Google metadata and service account exchange, and the Microsoft Entra client
// credentials flow were written once each for the three secret stores. Nothing
// about any of them is specific to reading a secret. A managed Postgres
// provider signs the same way, a runtime that pulls an image signs the same
// way, and every one of those lanes would otherwise write a fourth, a fifth and
// a sixth copy of a signing algorithm where a single wrong byte is a 403 that
// reads like a wrong password.
//
// The reason there is no SDK here is the same reason there was none there.
// Reading a secret, or minting a database token, is one signed request to one
// endpoint. The SDK that would do it brings roughly a hundred packages into a
// module whose whole purpose is holding credentials, and every one of them is
// code with access to them. Signature Version 4 is about a hundred lines of a
// documented algorithm that has not changed since 2012, and it is verified here
// against the worked example AWS publishes rather than against our own idea of
// it. The equivalent for Google and for Azure is an OAuth exchange over forms.
//
// Everything in this package is standard library only, and there is a test that
// fails if that stops being true.
package cloudauth

import (
	"errors"
	"fmt"
)

// ErrRejected reports that the far end refused the credential rather than
// refusing the request.
//
// The distinction is what drives the one refresh rule in ee/engine/secrets: a
// 404 is a miss, a 500 is a service having a bad day, and a 401 is the one case
// where trying again with the same credential is pointless and trying again
// with a new one might work.
//
// The wording is load bearing and is not to be tidied. ee/engine/secrets
// re-exports this exact value so that errors.Is keeps working across the
// package boundary, and it trims this sentence off the front of the detail it
// renders as AF-SEC-002, by matching the text.
var ErrRejected = errors.New("the store rejected the credential")

// ErrNotConfigured reports a credential source that was asked for and never
// given what it needs.
//
// Carried separately so that a caller can say "no key was found in any of the
// four places" rather than reporting a connection failure to an empty host.
var ErrNotConfigured = errors.New("not configured")

// Wrap is the shape every credential path uses to report a refusal, so the
// distinction between a rejected credential and an unreachable service is made
// in one place rather than once per cloud.
func Wrap(kind error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", kind, fmt.Sprintf(format, args...))
}
