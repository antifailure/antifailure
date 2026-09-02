// Package secrets holds the engine's secret material: where a credential is
// loaded from, how it is resolved, and the keyring it can be kept in.
//
// The type that carries the credential itself lives in engine/pkg/secret, one
// directory outward and outside internal, and this file is the alias that lets
// the rest of the engine keep spelling it secrets.Value. It is public there
// because engine/pkg/provider names it in the interfaces a provider has to
// implement, and a type an outside package cannot name is an interface an
// outside package cannot implement. The reasoning is written out where the
// type is.
//
// The alias is an alias and not a wrapper on purpose: secrets.Value and
// secret.Value are the same type to the compiler, so every existing call site,
// struct field and type assertion keeps working and no conversion exists to
// forget.
package secrets

import "github.com/antifailure/antifailure/engine/pkg/secret"

// Value is a secret string that does not print itself. See engine/pkg/secret.
type Value = secret.Value

// Redacted is what a Value renders as anywhere text is produced.
const Redacted = secret.Redacted

// New returns a Value holding s.
func New(s string) Value { return secret.New(s) }

// NewFrom returns a Value holding s, tagged with the source that produced it.
// The source appears in audit events; the value never does.
func NewFrom(s, source string) Value { return secret.NewFrom(s, source) }
